package main

// Harness runtimes (Claude Code, codex) append injected notices — task-tool
// reminders, "the user sent a message while you were working" — as TRAILING
// role=system/developer messages on top of a tool-continuation request. The
// continuation classifier must skip them and judge by the last real message;
// misreading such a request as a NEW turn wipes reply.Items mid-turn (the live
// tail collapses on every reminder), fragments per-turn token/cost accounting,
// and drains the IM reply binding so the turn's real answer never reaches
// WeChat/TG. These tests pin the skip for both wire shapes.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
)

func TestToolContinuationSkipsTrailingSystemMessages(t *testing.T) {
	parse := func(s string) map[string]interface{} {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatalf("bad fixture: %v", err)
		}
		return m
	}

	// Anthropic shape: tool_result user message + trailing system reminder.
	anthropic := parse(`{"messages":[
		{"role":"user","content":"do the thing"},
		{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]},
		{"role":"system","content":"The task tools haven't been used recently."}
	]}`)
	if !aiGatewayIsToolContinuation(anthropic) {
		t.Fatalf("reminder-tailed anthropic continuation misread as new turn")
	}

	// Multiple trailing injections (system + developer) still skip.
	multi := parse(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]},
		{"role":"system","content":"reminder one"},
		{"role":"developer","content":"reminder two"}
	]}`)
	if !aiGatewayIsToolContinuation(multi) {
		t.Fatalf("multi-reminder-tailed continuation misread as new turn")
	}

	// A REAL new q with a trailing reminder must still be a new turn.
	newQ := parse(`{"messages":[
		{"role":"assistant","content":"previous answer"},
		{"role":"user","content":"a brand new question"},
		{"role":"system","content":"The task tools haven't been used recently."}
	]}`)
	if aiGatewayIsToolContinuation(newQ) {
		t.Fatalf("real new q with trailing reminder wrongly treated as continuation")
	}

	// Codex Responses shape: function_call_output + trailing developer message.
	codex := parse(`{"input":[
		{"type":"function_call","call_id":"c1","name":"exec_command","arguments":"{}"},
		{"type":"function_call_output","call_id":"c1","output":"ok"},
		{"type":"message","role":"developer","content":"harness notice"}
	]}`)
	if !aiGatewayIsToolContinuation(codex) {
		t.Fatalf("developer-tailed codex continuation misread as new turn")
	}

	// All-system messages (degenerate) → not a continuation.
	degenerate := parse(`{"messages":[{"role":"system","content":"only a reminder"}]}`)
	if aiGatewayIsToolContinuation(degenerate) {
		t.Fatalf("system-only request wrongly treated as continuation")
	}
}

// End-to-end through newAIGatewayAuditSession: a reminder-tailed continuation
// must keep the SAME turn (inherited items — including the tool_result fold —
// stay in reply.json) instead of resetting the reply dir.
func TestReminderTailedContinuationKeepsReplyItems(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-19301"
	base, _ := url.Parse("https://api.anthropic.com")
	hdr := http.Header{"Content-Type": []string{"application/json"}}

	// Turn round 1: plain q → tool_use response. metadata.session_id mirrors real
	// Claude Code traffic — the conversation-guard on inheritance keys off it.
	req1 := []byte(`{"model":"m","metadata":{"session_id":"sess-rem-1"},"messages":[{"role":"user","content":"read the file"}]}`)
	s1 := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, req1)
	if err := s1.writeStartSnapshots(); err != nil {
		t.Fatalf("round1 start: %v", err)
	}
	resp1 := `{"content":[{"type":"text","text":"on it"},{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"/x"}}],` +
		`"stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":3}}`
	s1.completeFromResponse(200, hdr, []byte(resp1), nil)
	turn1 := s1.reply.TurnID

	// Round 2: tool_result continuation + trailing system reminder.
	req2 := []byte(`{"model":"m","metadata":{"session_id":"sess-rem-1"},"messages":[
		{"role":"user","content":"read the file"},
		{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"/x"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"FILE-BODY"}]},
		{"role":"system","content":"The task tools haven't been used recently. Consider using TaskCreate."}
	]}`)
	s2 := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, req2)
	if s2.reply.TurnID != turn1 {
		t.Fatalf("reminder-tailed continuation started a NEW turn: %q -> %q", turn1, s2.reply.TurnID)
	}
	if err := s2.writeStartSnapshots(); err != nil {
		t.Fatalf("round2 start: %v", err)
	}

	replyData, err := os.ReadFile(aiGatewayReplySnapshotPath(agent))
	if err != nil {
		t.Fatalf("read reply.json: %v", err)
	}
	var lite struct {
		TurnID string                   `json:"turn_id"`
		Items  []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(replyData, &lite); err != nil {
		t.Fatalf("parse reply.json: %v", err)
	}
	if lite.TurnID != turn1 {
		t.Fatalf("reply.json turn changed across reminder-tailed continuation: %q -> %q", turn1, lite.TurnID)
	}
	foundTool := false
	for _, it := range lite.Items {
		if it["type"] == "tool_use" && aiGatewayString(it["tool_id"]) == "toolu_1" {
			foundTool = true
			if aiGatewayFlattenPromptValue(it["output"]) != "FILE-BODY" {
				t.Fatalf("tool_result not folded on reminder-tailed continuation: %s", replyData)
			}
		}
	}
	if !foundTool {
		t.Fatalf("round-1 items wiped by reminder-tailed continuation: %s", replyData)
	}
}
