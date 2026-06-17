package main

import (
	"strings"
	"testing"
)

// The worker-status dot must stay yellow ("working") for the WHOLE busy window —
// from q sent until the answer completes or fails — including while the agent is
// running a tool client-side. A Claude/Codex agentic loop is round-by-round HTTP:
// a round ends with stop_reason=tool_use, its HTTP response completes (no live
// request), the tool runs in the client (no HTTP to the gateway), then the next
// round opens. Without the stop-reason carry, that gap read "completed" (green).
func TestAIGatewayBuildStatusMapKeepsWorkingDuringToolGap(t *testing.T) {
	base := aiGatewayReplySnapshot{
		ToolCalls: []aiGatewayToolCall{{ToolID: "t1", ToolName: "Bash"}},
	}
	// Gap after a tool_use round, no live HTTP → must stay "working" (yellow).
	gap := base
	gap.LastStopReason = "tool_use"
	if sm := aiGatewayBuildStatusMap(aiGatewayCurrentSnapshot{}, gap); sm.Primary != "working" {
		t.Fatalf("tool-use gap must stay working, got %q", sm.Primary)
	}
	// OpenAI chat flavour of the same signal.
	gap.LastStopReason = "tool_calls"
	if sm := aiGatewayBuildStatusMap(aiGatewayCurrentSnapshot{}, gap); sm.Primary != "working" {
		t.Fatalf("tool_calls gap must stay working, got %q", sm.Primary)
	}
	// Final round ends on end_turn → genuinely done.
	done := base
	done.LastStopReason = "end_turn"
	if sm := aiGatewayBuildStatusMap(aiGatewayCurrentSnapshot{}, done); sm.Primary != "completed" {
		t.Fatalf("end_turn must be completed, got %q", sm.Primary)
	}
	// No stop reason (legacy/empty) preserves prior behaviour: completed.
	empty := base
	if sm := aiGatewayBuildStatusMap(aiGatewayCurrentSnapshot{}, empty); sm.Primary != "completed" {
		t.Fatalf("empty stop reason must be completed, got %q", sm.Primary)
	}
}

func TestAIGatewayParseStreamCapturesStopReason(t *testing.T) {
	// Anthropic: stop_reason on message_delta.
	anthropic := "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":3}}\n\n"
	if got := aiGatewayParseStreamResponse([]byte(anthropic)).StopReason; got != "tool_use" {
		t.Fatalf("anthropic stop_reason = %q, want tool_use", got)
	}
	if !aiGatewayStopReasonExpectsToolRound("tool_use") {
		t.Fatalf("tool_use must expect a tool round")
	}
	// OpenAI chat: finish_reason on the final chunk.
	openai := "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"
	if got := aiGatewayParseStreamResponse([]byte(openai)).StopReason; got != "tool_calls" {
		t.Fatalf("openai finish_reason = %q, want tool_calls", got)
	}
	// A normal end-of-turn must NOT be treated as a tool round.
	endTurn := "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n"
	if aiGatewayStopReasonExpectsToolRound(aiGatewayParseStreamResponse([]byte(endTurn)).StopReason) {
		t.Fatalf("end_turn must not expect a tool round")
	}
}

func TestAIGatewayAnnotateCurrentBodyHistoryIDsAddsSequentialIDs(t *testing.T) {
	body := map[string]interface{}{
		"history": []interface{}{
			map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": "old"}}},
		},
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": "alpha"}}},
			map[string]interface{}{"role": "assistant", "content": []interface{}{map[string]interface{}{"type": "text", "text": "beta"}}},
		},
		"input": []interface{}{
			map[string]interface{}{"role": "user", "type": "message", "content": []interface{}{map[string]interface{}{"type": "input_text", "text": "gamma"}}},
			map[string]interface{}{"role": "assistant", "type": "message", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": "delta"}}},
		},
	}
	annotated := aiGatewayMap(aiGatewayAnnotateCurrentBodyHistoryIDs("w-test-annotate", body))
	if _, ok := annotated["history"]; ok {
		t.Fatalf("expected top-level history to be removed from current body")
	}
	messages := aiGatewaySlice(annotated["messages"])
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if got := aiGatewayInt(aiGatewayMap(messages[0])["id"]); got != 1 {
		t.Fatalf("expected message[0] id=1, got %#v", got)
	}
	if got := aiGatewayInt(aiGatewayMap(messages[1])["id"]); got != 2 {
		t.Fatalf("expected message[1] id=2, got %#v", got)
	}
	input := aiGatewaySlice(annotated["input"])
	if len(input) != 2 {
		t.Fatalf("expected 2 input items, got %d", len(input))
	}
	if got := aiGatewayInt(aiGatewayMap(input[0])["id"]); got != 1 {
		t.Fatalf("expected input[0] id=1, got %#v", got)
	}
	if got := aiGatewayInt(aiGatewayMap(input[1])["id"]); got != 2 {
		t.Fatalf("expected input[1] id=2, got %#v", got)
	}
	if got := aiGatewayCurrentBodyMaxHistoryID(annotated); got != 2 {
		t.Fatalf("expected max_history_id=2, got %d", got)
	}
}

// Regression for the repeated-content id-collision bug: three identical "hi"
// turns with replies in between (the full conversation snapshot) must number
// 1..N purely by POSITION, never by content. The old fingerprint-anchoring path
// mis-anchored the newest "hi" onto the previous "hi"'s id and clamped the rest
// to 1 — collapsing all three his onto a single id. Position-only numbering makes
// the his land on 1/3/5 and the answer slot (maxID+1) on 6.
func TestAIGatewayAnnotateRepeatedMessagesNumberByPositionNotContent(t *testing.T) {
	mkUser := func(text string) map[string]interface{} {
		return map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": text}}}
	}
	mkAsst := func(text string) map[string]interface{} {
		return map[string]interface{}{"role": "assistant", "content": []interface{}{map[string]interface{}{"type": "text", "text": text}}}
	}
	body := map[string]interface{}{
		"messages": []interface{}{
			mkUser("hi"), mkAsst("reply-1"), mkUser("hi"), mkAsst("reply-2"), mkUser("hi"),
		},
	}
	annotated := aiGatewayMap(aiGatewayAnnotateCurrentBodyHistoryIDs("w-test-repeat", body))
	messages := aiGatewaySlice(annotated["messages"])
	want := []int{1, 2, 3, 4, 5}
	if len(messages) != len(want) {
		t.Fatalf("expected %d messages, got %d", len(want), len(messages))
	}
	for i, w := range want {
		if got := aiGatewayInt(aiGatewayMap(messages[i])["id"]); got != w {
			t.Fatalf("message[%d] id=%d, want %d (repeated content must not collide)", i, got, w)
		}
	}
	if got := aiGatewayCurrentBodyMaxHistoryID(annotated); got != 5 {
		t.Fatalf("max_history_id=%d, want 5 → reply slot would be 6", got)
	}
}

func TestAIGatewayBuildMessageRecordPrefersCurrentInputHistoryForCodexCommentary(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-1001"
	current := aiGatewayCurrentSnapshot{
		AgentID: agentID,
		Model:   "gpt-5.5",
		Body: map[string]interface{}{
			"input": []interface{}{
				map[string]interface{}{
					"role": "user",
					"type": "message",
					"content": []interface{}{
						map[string]interface{}{"type": "input_text", "text": "Write ws-demo-audit.js and read it back"},
					},
				},
				map[string]interface{}{
					"role":  "assistant",
					"type":  "message",
					"phase": "commentary",
					"content": []interface{}{
						map[string]interface{}{"type": "output_text", "text": "I’m writing the file first."},
					},
				},
				map[string]interface{}{
					"type":    "custom_tool_call",
					"name":    "apply_patch",
					"call_id": "call_1",
					"input":   "*** Begin Patch\n*** Add File: ws-demo-audit.js\n+console.log('hi');\n*** End Patch",
				},
				map[string]interface{}{
					"type":    "custom_tool_call_output",
					"call_id": "call_1",
					"output":  "Success",
				},
				map[string]interface{}{
					"role":  "assistant",
					"type":  "message",
					"phase": "final_answer",
					"content": []interface{}{
						map[string]interface{}{"type": "output_text", "text": "Done."},
					},
				},
			},
		},
	}
	reply := aiGatewayReplySnapshot{
		TurnID:   "turn-1",
		Answer:   "I’m writing the file first.",
		Status:   "completed",
		Thinking: "",
	}

	record, ok, err := aiGatewayBuildMessageRecord(agentID, current, "Write ws-demo-audit.js and read it back", reply)
	if err != nil {
		t.Fatalf("build message record: %v", err)
	}
	if !ok {
		t.Fatalf("expected record")
	}
	if got := record.A; got != "Done." {
		t.Fatalf("expected final answer from current input history, got %q", got)
	}
	if got := record.Thinking; got != "I’m writing the file first." {
		t.Fatalf("expected commentary promoted to thinking, got %q", got)
	}
	if len(record.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(record.ToolCalls))
	}
	if got := record.ToolCalls[0].Name; got != "apply_patch" {
		t.Fatalf("expected apply_patch tool name, got %q", got)
	}
}

func TestAIGatewaySanitizeUserQuestionStripsEnvironmentContext(t *testing.T) {
	raw := `<environment_context>
<cwd>/home/cicy/cicy-ai/workers/w-10008</cwd>
<shell>bash</shell>
<current_date>2026-05-06</current_date>
<timezone>Etc/UTC</timezone>
</environment_context>

hi 1`

	if got := aiGatewaySanitizeUserQuestion(raw); got != "hi 1" {
		t.Fatalf("sanitize user question = %q, want %q", got, "hi 1")
	}
}

func TestExtractArgSupportsCodexCmdAndTaskTools(t *testing.T) {
	if got := extractArg(map[string]interface{}{"cmd": "sed -n '1,20p' demo.txt"}, "exec_command"); got != "sed -n '1,20p' demo.txt" {
		t.Fatalf("extractArg cmd = %q", got)
	}
	if got := extractArg(map[string]interface{}{"subject": "Create demo file"}, "TaskCreate"); got != "Create demo file" {
		t.Fatalf("extractArg TaskCreate = %q", got)
	}
	if got := extractArg(map[string]interface{}{"taskId": "3", "status": "completed"}, "TaskUpdate"); got != "task #3 -> completed" {
		t.Fatalf("extractArg TaskUpdate = %q", got)
	}
	if got := extractArg(map[string]interface{}{
		"plan": []interface{}{
			map[string]interface{}{"step": "Create demo file", "status": "completed"},
			map[string]interface{}{"step": "Read file back", "status": "in_progress"},
		},
	}, "update_plan"); got != "Read file back" {
		t.Fatalf("extractArg update_plan = %q", got)
	}
}

// audit-v2: the per-turn audit unit assembled at completeFromResponse must be
// {outbound q + reply tool_calls}, and must surface secrets that ride in tool
// arguments so the builtin rules can catch them. Empty turns produce no unit.
func TestAIGatewayBuildTurnAuditUnit(t *testing.T) {
	if u := aiGatewayBuildTurnAuditUnit("  ", nil); u != nil {
		t.Errorf("empty q + no tool calls must be nil, got %q", u)
	}
	u := string(aiGatewayBuildTurnAuditUnit(
		"push my code",
		[]aiGatewayToolCall{{ToolName: "Bash", Arguments: `{"cmd":"export AKIAIOSFODNN7EXAMPLE"}`}},
	))
	for _, want := range []string{"出站 q", "push my code", "tool_call", "Bash", "AKIAIOSFODNN7EXAMPLE"} {
		if !strings.Contains(u, want) {
			t.Errorf("audit unit missing %q in:\n%s", want, u)
		}
	}
}
