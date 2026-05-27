package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
)

// The tool_use produced in one response has no result yet — it arrives in the
// next (continuation) request's body. aiGatewayInjectToolResultsIntoItems folds
// that result back onto the matching tool_use item so reply.json carries it.
func TestInjectToolResultsIntoItems(t *testing.T) {
	items := []map[string]interface{}{
		{"id": 1, "type": "tool_use", "tool_id": "toolu_1", "name": "read", "input": map[string]interface{}{"p": "/x"}},
		{"id": 2, "type": "text", "text": "ok"},
		{"id": 3, "type": "tool_use", "tool_id": "toolu_2", "name": "write", "input": map[string]interface{}{}},
	}
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": []interface{}{
				map[string]interface{}{"type": "tool_result", "tool_use_id": "toolu_1", "content": "RESULT-X"},
				map[string]interface{}{"type": "tool_result", "tool_use_id": "toolu_2", "content": "RESULT-Y"},
			}},
		},
	}
	out := aiGatewayInjectToolResultsIntoItems(items, body)

	got := map[string]string{}
	for _, it := range out {
		if it["type"] == "tool_use" {
			got[aiGatewayString(it["tool_id"])] = aiGatewayFlattenPromptValue(it["output"])
		}
	}
	if got["toolu_1"] != "RESULT-X" || got["toolu_2"] != "RESULT-Y" {
		t.Fatalf("tool outputs not injected: %#v", got)
	}

	// Idempotent: a tool_use that already has an output is not clobbered.
	items[0]["output"] = "ALREADY"
	body2 := map[string]interface{}{"messages": []interface{}{
		map[string]interface{}{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "tool_result", "tool_use_id": "toolu_1", "content": "NEW"},
		}},
	}}
	out = aiGatewayInjectToolResultsIntoItems(items, body2)
	if aiGatewayFlattenPromptValue(out[0]["output"]) != "ALREADY" {
		t.Fatalf("existing output was clobbered: %v", out[0]["output"])
	}
}

// End-to-end through newAIGatewayAuditSession (the shared path for both the
// gateway proxy and the MITM adapter): turn 1 emits a tool_use into reply.json,
// the turn-1 continuation request carries the tool_result, and the next session
// (inheriting the prior items) must fold the output onto the tool_use item.
func TestSessionContinuationCarriesToolResultIntoReply(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-19007"
	base, _ := url.Parse("https://api.anthropic.com")
	hdr := http.Header{"Content-Type": []string{"application/json"}}

	// Turn 1: a plain user prompt → response with a tool_use block.
	req1 := []byte(`{"model":"m","messages":[{"role":"user","content":"read the file"}]}`)
	s1 := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, req1)
	if err := s1.writeStartSnapshots(); err != nil {
		t.Fatalf("turn1 start: %v", err)
	}
	resp1 := `{"content":[{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"/x"}}],` +
		`"stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":3}}`
	s1.completeFromResponse(200, hdr, []byte(resp1), nil)

	// Turn 1 continuation: the CLI feeds the tool_result back.
	req2 := []byte(`{"model":"m","messages":[
		{"role":"user","content":"read the file"},
		{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"/x"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"FILE-BODY"}]}
	]}`)
	s2 := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, req2)
	if err := s2.writeStartSnapshots(); err != nil {
		t.Fatalf("turn2 start: %v", err)
	}

	replyData, err := os.ReadFile(aiGatewayReplySnapshotPath(agent))
	if err != nil {
		t.Fatalf("read reply.json: %v", err)
	}
	var lite struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(replyData, &lite); err != nil {
		t.Fatalf("parse reply.json: %v", err)
	}
	found := false
	for _, it := range lite.Items {
		if it["type"] == "tool_use" && aiGatewayString(it["tool_id"]) == "toolu_1" {
			if aiGatewayFlattenPromptValue(it["output"]) != "FILE-BODY" {
				t.Fatalf("tool_use output not in reply.json: %s", replyData)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("inherited tool_use not found in reply.json: %s", replyData)
	}
}
