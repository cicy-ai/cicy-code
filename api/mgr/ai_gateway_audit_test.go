package main

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestAIGatewayMaybeRebuildMessagesFileDoesNotWriteMessagesJSON(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	current := aiGatewayCurrentSnapshot{
		AgentID:   agentID,
		Timestamp: "2026-05-05T01:00:00Z",
		Model:     "gpt-5.5",
		Body: map[string]interface{}{
			"input": []interface{}{
				map[string]interface{}{
					"role": "user",
					"type": "message",
					"content": []interface{}{
						map[string]interface{}{"type": "input_text", "text": "Write ws-demo-audit-2.js"},
					},
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
		TurnID:    "turn-1",
		Status:    "completed",
		StartedAt: "2026-05-05T01:00:00Z",
		UpdatedAt: "2026-05-05T01:00:01Z",
		Answer:    "Done.",
	}
	if err := aiGatewayWriteJSONAtomic(filepath.Join(aiGatewayHistoryDir(agentID), "current.json"), current); err != nil {
		t.Fatalf("write current: %v", err)
	}
	if err := aiGatewayWriteJSONAtomic(filepath.Join(aiGatewayHistoryDir(agentID), "reply.json"), reply); err != nil {
		t.Fatalf("write reply: %v", err)
	}

	file, err := aiGatewayMaybeRebuildMessagesFile(agentID)
	if err != nil {
		t.Fatalf("rebuild messages file: %v", err)
	}
	if len(file.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(file.Messages))
	}
	if _, err := os.Stat(aiGatewayMessagesPath(agentID)); !os.IsNotExist(err) {
		t.Fatalf("expected messages.json to stay absent, stat err=%v", err)
	}
}
