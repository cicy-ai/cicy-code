package main

// The "msg done" callback (🔔 [w-xxx] msg X → done) must fire at the RECEIVER's
// real end-of-turn — and ONLY then. Auxiliary gateway sessions (suggestion,
// title, recap wakeups, Claude's max_tokens=1 quota probes, Task-subagent
// sidechains) terminate with status=completed and zero tool calls, so they slip
// straight through the hook's tool gate; before completeFromResponse gated
// finalize on !s.auxiliary, any of them could consume the pending callback and
// report "done" while the real task was still running (or hadn't started).
// These tests drive real audit sessions end-to-end and pin that timing.

import (
	"net/http"
	"net/url"
	"testing"
)

func newTestAuditSession(t *testing.T, agent, body string) *aiGatewayAuditSession {
	t.Helper()
	base, _ := url.Parse("https://api.anthropic.com")
	hdr := http.Header{"Content-Type": []string{"application/json"}}
	s := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, []byte(body))
	if err := s.writeStartSnapshots(); err != nil {
		t.Fatalf("writeStartSnapshots: %v", err)
	}
	return s
}

func completeOK(t *testing.T, s *aiGatewayAuditSession, respJSON string) {
	t.Helper()
	s.completeFromResponse(200, http.Header{"Content-Type": []string{"application/json"}}, []byte(respJSON), nil)
}

const finalAnswerResp = `{"content":[{"type":"text","text":"all done"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`

// A sidechain (Task subagent) completing with end_turn/no tools must NOT fire
// the pending msg-done callback; the main turn's own terminal round fires it.
func TestAuxSidechainDoesNotFireMsgDone(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	resetPendingCallbacks()

	const master, recv = "w-20001", "w-19402"
	meta := `"metadata":{"session_id":"sess-aux-1"},`

	if err := insertAgentMessage("mx", master, recv, "📮 [w-20001] do X", true, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	registerReplyCallback(recv, master, "mx", true, "")

	// Main turn round 1: q → tool_use (Task). Callback must survive: completed
	// but ToolCalls>0 (intermediate round).
	s1 := newTestAuditSession(t, recv, `{"model":"m",`+meta+`"messages":[{"role":"user","content":"main question"}]}`)
	if s1.auxiliary {
		t.Fatalf("main round misclassified aux (%q)", s1.auxKind)
	}
	completeOK(t, s1, `{"content":[{"type":"text","text":"working"},{"type":"tool_use","id":"toolu_1","name":"Task","input":{"prompt":"sub prompt"}}],"stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":3}}`)
	if st, _ := msgStatus(t, "mx"); st != "sent" {
		t.Fatalf("intermediate tool round fired msg done: status=%s", st)
	}

	// Subagent sidechain completes with end_turn and NO tools — the premature-
	// fire vector. Must not consume the callback or flip the message.
	s2 := newTestAuditSession(t, recv, `{"model":"m",`+meta+`"messages":[{"role":"user","content":"sub prompt"}]}`)
	if !s2.auxiliary || s2.auxKind != "sidechain" {
		t.Fatalf("not sidechain: aux=%t kind=%q", s2.auxiliary, s2.auxKind)
	}
	completeOK(t, s2, finalAnswerResp)
	if st, _ := msgStatus(t, "mx"); st != "sent" {
		t.Fatalf("sidechain completion fired msg done early: status=%s", st)
	}
	if n := pendingLen(recv); n != 1 {
		t.Fatalf("sidechain consumed the pending callback: remaining=%d", n)
	}

	// Main continuation (tool_result back) ends the turn with no tools → NOW it
	// fires: message flips done, pending consumed.
	s3 := newTestAuditSession(t, recv, `{"model":"m",`+meta+`"messages":[
		{"role":"user","content":"main question"},
		{"role":"assistant","content":[{"type":"text","text":"working"},{"type":"tool_use","id":"toolu_1","name":"Task","input":{"prompt":"sub prompt"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"sub ok"}]}
	]}`)
	if s3.auxiliary {
		t.Fatalf("main continuation misclassified aux (%q)", s3.auxKind)
	}
	completeOK(t, s3, finalAnswerResp)
	if st, _ := msgStatus(t, "mx"); st != "done" {
		t.Fatalf("main terminal round did not fire msg done: status=%s", st)
	}
	if n := pendingLen(recv); n != 0 {
		t.Fatalf("terminal round did not consume pending: remaining=%d", n)
	}
}

// Claude's quota probe (max_tokens=1 ping) fires on its own schedule — often
// BEFORE the receiver even starts the requested task. It must never report done.
func TestAuxQuotaProbeDoesNotFireMsgDone(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	resetPendingCallbacks()

	const master, recv = "w-20001", "w-19403"

	if err := insertAgentMessage("mq", master, recv, "📮 [w-20001] do Y", true, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	registerReplyCallback(recv, master, "mq", true, "")

	probe := newTestAuditSession(t, recv, `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"quota"}]}`)
	if !probe.auxiliary {
		t.Fatalf("quota probe not classified aux (kind=%q)", probe.auxKind)
	}
	completeOK(t, probe, `{"content":[{"type":"text","text":"q"}],"stop_reason":"max_tokens","usage":{"input_tokens":1,"output_tokens":1}}`)
	if st, _ := msgStatus(t, "mq"); st != "sent" {
		t.Fatalf("quota probe fired msg done: status=%s", st)
	}
	if n := pendingLen(recv); n != 1 {
		t.Fatalf("quota probe consumed the pending callback: remaining=%d", n)
	}
}
