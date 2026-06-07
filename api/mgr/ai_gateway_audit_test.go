package main

import "testing"

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

func TestAIGatewayBuildMessageRecordPrefersCurrentInputHistoryForCodexCommentary(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
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
