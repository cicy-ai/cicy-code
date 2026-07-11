package main

// Claude Code Task subagents (and WebFetch summarizers etc.) reuse the pane's
// session header, so their API calls arrive under the SAME conversation id as
// the main thread — but they are a DIFFERENT thread (their first user message
// is the task prompt, not the conversation's). Treated as mainline they reset
// reply.json mid-turn (viewers' live tail replaced by subagent output) and
// their completed reply can be inherited into the next main continuation.
// They must classify as auxiliary ("sidechain"): fully isolated from the main
// snapshots, but still billed (usage log keeps the spend, tagged).

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestSidechainRequestLeavesMainReplyUntouched(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-19401"
	base, _ := url.Parse("https://api.anthropic.com")
	hdr := http.Header{"Content-Type": []string{"application/json"}}
	meta := `"metadata":{"session_id":"sess-sc-1"},`

	// Main turn round 1: q → tool_use (turn active, mid tool loop).
	req1 := []byte(`{"model":"m",` + meta + `"messages":[{"role":"user","content":"main question"}]}`)
	s1 := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, req1)
	if s1.auxiliary {
		t.Fatalf("main round misclassified as auxiliary (%q)", s1.auxKind)
	}
	if err := s1.writeStartSnapshots(); err != nil {
		t.Fatalf("main start: %v", err)
	}
	resp1 := `{"content":[{"type":"text","text":"working on it"},{"type":"tool_use","id":"toolu_1","name":"Task","input":{"prompt":"subagent task prompt"}}],` +
		`"stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":3}}`
	s1.completeFromResponse(200, hdr, []byte(resp1), nil)
	mainTurn := s1.reply.TurnID

	snapshotBefore, err := os.ReadFile(aiGatewayReplySnapshotPath(agent))
	if err != nil {
		t.Fatalf("read reply.json: %v", err)
	}

	// Subagent call: SAME session id, but a fresh thread whose first user
	// message is the task prompt.
	side := []byte(`{"model":"m",` + meta + `"messages":[{"role":"user","content":"subagent task prompt"}]}`)
	s2 := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, side)
	if !s2.auxiliary || s2.auxKind != "sidechain" {
		t.Fatalf("subagent call not classified sidechain: aux=%t kind=%q", s2.auxiliary, s2.auxKind)
	}
	if err := s2.writeStartSnapshots(); err != nil {
		t.Fatalf("sidechain start: %v", err)
	}
	s2.completeFromResponse(200, hdr, []byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`), nil)

	snapshotAfter, err := os.ReadFile(aiGatewayReplySnapshotPath(agent))
	if err != nil {
		t.Fatalf("read reply.json after sidechain: %v", err)
	}
	if string(snapshotBefore) != string(snapshotAfter) {
		t.Fatalf("sidechain call disturbed main reply.json:\nbefore: %s\nafter:  %s", snapshotBefore, snapshotAfter)
	}

	// Main continuation (tool_result back, same first message) is NOT sidechain
	// and inherits the main turn.
	req2 := []byte(`{"model":"m",` + meta + `"messages":[
		{"role":"user","content":"main question"},
		{"role":"assistant","content":[{"type":"text","text":"working on it"},{"type":"tool_use","id":"toolu_1","name":"Task","input":{"prompt":"subagent task prompt"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"subagent finished: ok"}]}
	]}`)
	s3 := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, req2)
	if s3.auxiliary {
		t.Fatalf("main continuation misclassified as auxiliary (%q)", s3.auxKind)
	}
	if s3.reply.TurnID != mainTurn {
		t.Fatalf("main continuation lost its turn: %q -> %q", mainTurn, s3.reply.TurnID)
	}
	foundTool := false
	for _, it := range s3.reply.Items {
		if aiGatewayString(it["type"]) == "tool_use" && aiGatewayString(it["tool_id"]) == "toolu_1" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatalf("main continuation lost inherited items: %#v", s3.reply.Items)
	}

	// Sidechain spend stays on the books, tagged aux_kind=sidechain.
	usageData, err := os.ReadFile(agentUsageLogPath(agent))
	if err != nil {
		t.Fatalf("read usage log: %v", err)
	}
	if !strings.Contains(string(usageData), `"aux_kind":"sidechain"`) {
		t.Fatalf("sidechain usage not recorded/tagged: %s", usageData)
	}
}

// A post-compact mainline continuation (first message = compaction preamble,
// same conversation) must NOT classify as sidechain — its rounds own reply.json.
func TestCompactContinuationIsNotSidechain(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-19402"
	base, _ := url.Parse("https://api.anthropic.com")
	hdr := http.Header{"Content-Type": []string{"application/json"}}
	meta := `"metadata":{"session_id":"sess-sc-2"},`

	req1 := []byte(`{"model":"m",` + meta + `"messages":[{"role":"user","content":"original question"}]}`)
	s1 := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, req1)
	if err := s1.writeStartSnapshots(); err != nil {
		t.Fatalf("start: %v", err)
	}
	s1.completeFromResponse(200, hdr, []byte(`{"content":[{"type":"text","text":"answer"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`), nil)

	compact := []byte(`{"model":"m",` + meta + `"messages":[{"role":"user","content":"This session is being continued from a previous conversation that ran out of context. Summary: ..."}]}`)
	s2 := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, compact)
	if s2.auxiliary && s2.auxKind == "sidechain" {
		t.Fatalf("post-compact mainline misclassified as sidechain")
	}
}
