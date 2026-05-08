package main

import (
	"strings"
	"testing"
)

func TestAgentInspectorBuildToolDisplayStripsExecCommandWrapper(t *testing.T) {
	tool := agentInspectorBuildToolDisplay(aiGatewayMessageToolCall{
		Name:   "exec_command",
		Input:  `{"cmd":"sed -n '1,20p' demo.txt"}`,
		Output: "Chunk ID: 123\nWall time: 0.0000 seconds\nProcess exited with code 0\nOriginal token count: 5\nOutput:\nalpha\nbeta",
	})
	if got := aiGatewayString(tool["arg"]); got != "sed -n '1,20p' demo.txt" {
		t.Fatalf("tool arg = %q", got)
	}
	if got := aiGatewayString(tool["result"]); got != "alpha\nbeta" {
		t.Fatalf("tool result = %q", got)
	}
}

func TestAgentInspectorBuildToolSummaryUsesPreviewOnly(t *testing.T) {
	tool := agentInspectorBuildToolSummary(M{
		"name":   "exec_command",
		"arg":    "printf 'hello world'",
		"result": strings.Repeat("x", 500),
	})
	if got := aiGatewayString(tool["name"]); got != "exec_command" {
		t.Fatalf("tool name = %q", got)
	}
	if _, ok := tool["arg"]; ok {
		t.Fatalf("expected no arg in summary, got %#v", tool["arg"])
	}
	if _, ok := tool["result"]; ok {
		t.Fatalf("expected no result in summary, got %#v", tool["result"])
	}
}

func TestAgentInspectorHistoryThinkingFromCommentaryItem(t *testing.T) {
	item := map[string]interface{}{
		"role":  "assistant",
		"phase": "commentary",
		"content": []interface{}{
			map[string]interface{}{"type": "output_text", "text": "I’m creating the demo file first."},
		},
	}

	if got := agentInspectorHistoryThinkingFromItem(item); got != "I’m creating the demo file first." {
		t.Fatalf("expected commentary text as thinking, got %q", got)
	}
	if got := agentInspectorHistoryAnswerFromItem(item); got != "" {
		t.Fatalf("expected commentary item to be excluded from answer, got %q", got)
	}
}

func TestAgentInspectorBuildCurrentOnlyInputTurnsPromotesCommentaryToThinking(t *testing.T) {
	current := aiGatewayCurrentSnapshot{
		Model: "gpt-5.5",
		Body: map[string]interface{}{
			"input": []interface{}{
				map[string]interface{}{
					"role": "user",
					"type": "message",
					"content": []interface{}{
						map[string]interface{}{"type": "input_text", "text": "Write ws-demo-test.js and read it back"},
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
					"input":   "*** Begin Patch\n*** Add File: ws-demo-test.js\n+console.log('hi');\n*** End Patch",
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

	turns := agentInspectorBuildCurrentOnlyInputTurns(current)
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(turns))
	}
	steps, _ := turns[0]["steps"].([]M)
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	if got := steps[0]["type"]; got != "thinking" {
		t.Fatalf("expected first step thinking, got %#v", got)
	}
	if got := steps[1]["type"]; got != "tool" {
		t.Fatalf("expected second step tool, got %#v", got)
	}
	if got := steps[2]["type"]; got != "text" {
		t.Fatalf("expected third step text, got %#v", got)
	}
	if got := turns[0]["a"]; got != "Done." {
		t.Fatalf("expected final answer only, got %#v", got)
	}
}

func TestAgentInspectorHistoryThinkingFromClaudeToolUseMessage(t *testing.T) {
	item := map[string]interface{}{
		"role": "assistant",
		"content": []interface{}{
			map[string]interface{}{"type": "thinking", "thinking": "Reason about the options."},
			map[string]interface{}{"type": "text", "text": "I’ll choose the smaller option."},
			map[string]interface{}{"type": "tool_use", "id": "call_1", "name": "TaskCreate", "input": map[string]interface{}{"subject": "Compare"}},
		},
	}

	gotThinking := agentInspectorHistoryThinkingFromItem(item)
	if gotThinking == "" {
		t.Fatalf("expected claude tool-use message to produce thinking text")
	}
	if gotAnswer := agentInspectorHistoryAnswerFromItem(item); gotAnswer != "" {
		t.Fatalf("expected claude tool-use message to be excluded from answer, got %q", gotAnswer)
	}
}

func TestAgentInspectorBuildCurrentOnlyClaudeTurnsPromotesThinkingBlocks(t *testing.T) {
	current := aiGatewayCurrentSnapshot{
		Model: "claude-opus-4-7",
		Body: map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{
					"role": "user",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "Write ws-demo-claude-test.js and read it back"},
					},
				},
				map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "thinking", "thinking": "Compare two approaches."},
						map[string]interface{}{"type": "text", "text": "I’ll use the smaller ws approach."},
						map[string]interface{}{"type": "tool_use", "id": "call_1", "name": "TaskCreate", "input": map[string]interface{}{"subject": "Compare"}},
					},
				},
				map[string]interface{}{
					"role": "user",
					"content": []interface{}{
						map[string]interface{}{"type": "tool_result", "tool_use_id": "call_1", "content": "Task ok"},
					},
				},
				map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "Done."},
					},
				},
			},
		},
	}

	turns := agentInspectorBuildCurrentOnlyClaudeTurns(current)
	if len(turns) != 3 {
		t.Fatalf("expected user + work assistant + final assistant turns, got %d", len(turns))
	}
	assistantTurn := turns[1]
	steps, _ := assistantTurn["steps"].([]M)
	if len(steps) < 3 {
		t.Fatalf("expected at least 3 steps, got %d", len(steps))
	}
	if got := steps[0]["type"]; got != "thinking" {
		t.Fatalf("expected first assistant step thinking, got %#v", got)
	}
	if got := steps[1]["type"]; got != "thinking" {
		t.Fatalf("expected second assistant step thinking, got %#v", got)
	}
	if got := steps[2]["type"]; got != "tool" {
		t.Fatalf("expected third assistant step tool, got %#v", got)
	}
}

func TestAgentInspectorBuildCurrentOnlyPersistedTurnsKeepsClaudeTimelineWithQuestion(t *testing.T) {
	current := aiGatewayCurrentSnapshot{
		Model: "claude-opus-4-7",
		Body: map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{
					"role": "user",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "Write ws-demo-claude-timeline.js and read it back"},
					},
				},
				map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "thinking", "thinking": "Compare two ways."},
						map[string]interface{}{"type": "text", "text": "I’ll inspect the existing file first."},
						map[string]interface{}{"type": "tool_use", "id": "call_1", "name": "Read", "input": map[string]interface{}{"file_path": "ws-demo-claude-timeline.js"}},
					},
				},
				map[string]interface{}{
					"role": "user",
					"content": []interface{}{
						map[string]interface{}{"type": "tool_result", "tool_use_id": "call_1", "content": "not found"},
					},
				},
				map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "Done."},
					},
				},
			},
		},
	}

	turns := agentInspectorBuildCurrentOnlyPersistedTurns(current)
	if len(turns) != 1 {
		t.Fatalf("expected single persisted turn, got %d", len(turns))
	}
	if got := turns[0]["q"]; got != "Write ws-demo-claude-timeline.js and read it back" {
		t.Fatalf("unexpected question: %#v", got)
	}
	if got := turns[0]["a"]; got != "Done." {
		t.Fatalf("unexpected final answer: %#v", got)
	}
	steps, _ := turns[0]["steps"].([]M)
	if len(steps) != 4 {
		t.Fatalf("expected 4 ordered steps, got %d", len(steps))
	}
	if got := steps[0]["type"]; got != "thinking" {
		t.Fatalf("expected step 0 thinking, got %#v", got)
	}
	if got := steps[1]["type"]; got != "thinking" {
		t.Fatalf("expected step 1 thinking, got %#v", got)
	}
	if got := steps[2]["type"]; got != "tool" {
		t.Fatalf("expected step 2 tool, got %#v", got)
	}
	if got := steps[3]["type"]; got != "text" {
		t.Fatalf("expected step 3 text, got %#v", got)
	}
}

func TestAgentInspectorBuildCurrentOnlyPersistedTurnsSanitizesClaudeQuestionAndWorkingText(t *testing.T) {
	current := aiGatewayCurrentSnapshot{
		Model: "claude-opus-4-7",
		Body: map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{
					"role": "user",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "<system-reminder>\nskills and runtime metadata\n</system-reminder>"},
						map[string]interface{}{"type": "text", "text": "<system-reminder>\nToday is 2026-05-05.\n</system-reminder>"},
						map[string]interface{}{"type": "text", "text": "Write ws-demo-claude-e2e.js and read it back"},
					},
				},
				map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "thinking", "thinking": "Compare ws and raw upgrade."},
						map[string]interface{}{"type": "text", "text": "I’ll inspect the target first."},
						map[string]interface{}{"type": "tool_use", "id": "call_1", "name": "Bash", "input": map[string]interface{}{"command": "ls -1"}},
					},
				},
				map[string]interface{}{
					"role": "user",
					"content": []interface{}{
						map[string]interface{}{"type": "tool_result", "tool_use_id": "call_1", "content": "missing"},
					},
				},
				map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "I chose ws because it keeps the demo small."},
					},
				},
				map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "tool_use", "id": "call_2", "name": "Write", "input": map[string]interface{}{"file_path": "ws-demo-claude-e2e.js"}},
					},
				},
				map[string]interface{}{
					"role": "user",
					"content": []interface{}{
						map[string]interface{}{"type": "tool_result", "tool_use_id": "call_2", "content": "created"},
					},
				},
				map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "Done.\n- Used ws.\n- Verified file."},
					},
				},
			},
		},
	}

	turns := agentInspectorBuildCurrentOnlyPersistedTurns(current)
	if len(turns) != 1 {
		t.Fatalf("expected single persisted turn, got %d", len(turns))
	}
	if got := turns[0]["q"]; got != "Write ws-demo-claude-e2e.js and read it back" {
		t.Fatalf("unexpected sanitized question: %#v", got)
	}
	if got := turns[0]["a"]; got != "Done.\n- Used ws.\n- Verified file." {
		t.Fatalf("expected only final answer in a, got %#v", got)
	}
	steps, _ := turns[0]["steps"].([]M)
	if len(steps) != 6 {
		t.Fatalf("expected 6 ordered steps, got %d: %#v", len(steps), steps)
	}
	if got := steps[0]["type"]; got != "thinking" {
		t.Fatalf("expected step 0 thinking, got %#v", got)
	}
	if got := steps[1]["type"]; got != "thinking" {
		t.Fatalf("expected step 1 thinking, got %#v", got)
	}
	if got := steps[2]["type"]; got != "tool" {
		t.Fatalf("expected step 2 tool, got %#v", got)
	}
	if got := steps[3]["type"]; got != "thinking" {
		t.Fatalf("expected working text as thinking, got %#v", got)
	}
	if got := steps[4]["type"]; got != "tool" {
		t.Fatalf("expected step 4 tool, got %#v", got)
	}
	if got := steps[5]["type"]; got != "text" {
		t.Fatalf("expected final step text, got %#v", got)
	}
}

func TestAgentInspectorBuildCurrentOnlyPersistedTurnsMergesClaudeWebSearchSubPrompt(t *testing.T) {
	current := aiGatewayCurrentSnapshot{
		Model: "claude-opus-4-7",
		Body: map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{
					"role": "user",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "Find the latest Python stable release and answer briefly"},
					},
				},
				map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "thinking", "thinking": "I need to verify the latest stable release first."},
						map[string]interface{}{"type": "text", "text": "I’m checking the current Python stable release."},
						map[string]interface{}{"type": "tool_use", "id": "call_1", "name": "WebSearch", "input": map[string]interface{}{"query": "Python latest stable release 2026 official stable release"}},
					},
				},
				map[string]interface{}{
					"role": "user",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "Perform a web search for the query: Python latest stable release 2026 official stable release"},
					},
				},
				map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "Latest stable is Python 3.14.4."},
					},
				},
			},
		},
	}

	turns := agentInspectorBuildCurrentOnlyPersistedTurns(current)
	if len(turns) != 1 {
		t.Fatalf("expected merged persisted turn, got %d", len(turns))
	}
	if got := turns[0]["q"]; got != "Find the latest Python stable release and answer briefly" {
		t.Fatalf("unexpected question: %#v", got)
	}
	if got := turns[0]["a"]; got != "Latest stable is Python 3.14.4." {
		t.Fatalf("unexpected final answer: %#v", got)
	}
	steps, _ := turns[0]["steps"].([]M)
	if len(steps) != 4 {
		t.Fatalf("expected 4 merged steps, got %d: %#v", len(steps), steps)
	}
	if got := steps[0]["type"]; got != "thinking" {
		t.Fatalf("expected step 0 thinking, got %#v", got)
	}
	if got := steps[1]["type"]; got != "thinking" {
		t.Fatalf("expected step 1 thinking, got %#v", got)
	}
	if got := steps[2]["type"]; got != "tool" {
		t.Fatalf("expected step 2 tool, got %#v", got)
	}
	if got := steps[3]["type"]; got != "text" {
		t.Fatalf("expected step 3 text, got %#v", got)
	}
}

func TestAgentInspectorBuildPersistedTurnsFromFullCurrentIncludesMultipleInputTurns(t *testing.T) {
	current := aiGatewayCurrentSnapshot{
		Model: "gpt-5.5",
		Body: map[string]interface{}{
			"input": []interface{}{
				map[string]interface{}{
					"role": "user",
					"type": "message",
					"content": []interface{}{
						map[string]interface{}{"type": "input_text", "text": "Reply with exactly one word: alpha"},
					},
				},
				map[string]interface{}{
					"role":  "assistant",
					"type":  "message",
					"phase": "final_answer",
					"content": []interface{}{
						map[string]interface{}{"type": "output_text", "text": "alpha"},
					},
				},
				map[string]interface{}{
					"role": "user",
					"type": "message",
					"content": []interface{}{
						map[string]interface{}{"type": "input_text", "text": "Reply with exactly one word: beta"},
					},
				},
				map[string]interface{}{
					"role":  "assistant",
					"type":  "message",
					"phase": "final_answer",
					"content": []interface{}{
						map[string]interface{}{"type": "output_text", "text": "beta"},
					},
				},
			},
		},
	}

	turns := agentInspectorBuildPersistedTurnsFromFullCurrent(current)
	if len(turns) != 2 {
		t.Fatalf("expected 2 persisted turns, got %d", len(turns))
	}
	if got := turns[0]["a"]; got != "alpha" {
		t.Fatalf("expected first answer alpha, got %#v", got)
	}
	if got := turns[1]["a"]; got != "beta" {
		t.Fatalf("expected second answer beta, got %#v", got)
	}
	steps0, _ := turns[0]["steps"].([]M)
	steps1, _ := turns[1]["steps"].([]M)
	if len(steps0) != 1 || len(steps1) != 1 {
		t.Fatalf("expected single text step for each turn, got %d and %d", len(steps0), len(steps1))
	}
	if got := steps0[0]["type"]; got != "text" {
		t.Fatalf("expected first turn step text, got %#v", got)
	}
	if got := steps1[0]["type"]; got != "text" {
		t.Fatalf("expected second turn step text, got %#v", got)
	}
}

func TestAgentInspectorBuildPersistedTurnsFromFullCurrentMergesContinuationGoIntoPreviousTurn(t *testing.T) {
	current := aiGatewayCurrentSnapshot{
		Model: "gpt-5.5",
		Body: map[string]interface{}{
			"input": []interface{}{
				map[string]interface{}{
					"role": "user",
					"type": "message",
					"content": []interface{}{
						map[string]interface{}{"type": "input_text", "text": "Write codex-history-e2e.yaml and read it back"},
					},
				},
				map[string]interface{}{
					"role":  "assistant",
					"type":  "message",
					"phase": "final_answer",
					"content": []interface{}{
						map[string]interface{}{"type": "output_text", "text": "503 No available accounts"},
					},
				},
				map[string]interface{}{
					"role": "user",
					"type": "message",
					"content": []interface{}{
						map[string]interface{}{"type": "input_text", "text": "go"},
					},
				},
				map[string]interface{}{
					"role":  "assistant",
					"type":  "message",
					"phase": "commentary",
					"content": []interface{}{
						map[string]interface{}{"type": "output_text", "text": "I’m creating the YAML first."},
					},
				},
				map[string]interface{}{
					"type":    "custom_tool_call",
					"name":    "apply_patch",
					"call_id": "call_1",
					"input":   "*** Begin Patch\n*** Add File: codex-history-e2e.yaml\n+alpha: 1\n+beta: 2\n*** End Patch",
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

	turns := agentInspectorBuildPersistedTurnsFromFullCurrent(current)
	if len(turns) != 1 {
		t.Fatalf("expected continuation go to merge into previous turn, got %d turns: %#v", len(turns), turns)
	}
	if got := turns[0]["q"]; got != "Write codex-history-e2e.yaml and read it back" {
		t.Fatalf("unexpected merged question: %#v", got)
	}
	if got := turns[0]["a"]; got != "Done." {
		t.Fatalf("expected final continuation answer, got %#v", got)
	}
	steps, _ := turns[0]["steps"].([]M)
	if len(steps) != 3 {
		t.Fatalf("expected merged continuation timeline with 3 steps, got %d: %#v", len(steps), steps)
	}
	if got := steps[0]["type"]; got != "thinking" {
		t.Fatalf("expected step 0 thinking, got %#v", got)
	}
	if got := steps[1]["type"]; got != "tool" {
		t.Fatalf("expected step 1 tool, got %#v", got)
	}
	if got := steps[2]["type"]; got != "text" {
		t.Fatalf("expected step 2 text, got %#v", got)
	}
}

func TestAgentInspectorSkipsInternalClaudeTitleSnapshot(t *testing.T) {
	current := aiGatewayCurrentSnapshot{
		Model: "claude-haiku-4-5-20251001",
		Body: map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{
					"role": "user",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "Reply with exactly two short bullets: one says alpha, one says beta.\rhi-claude-sync"},
					},
				},
			},
			"system": []interface{}{
				map[string]interface{}{"type": "text", "text": "Generate a concise, sentence-case title (3-7 words) that captures the main topic or goal of this coding session."},
				map[string]interface{}{"type": "text", "text": "Return JSON with a single \"title\" field."},
			},
			"output_config": map[string]interface{}{
				"format": map[string]interface{}{
					"type": "json_schema",
				},
			},
		},
	}

	if turns := agentInspectorBuildCurrentOnlyPersistedTurns(current); len(turns) != 0 {
		t.Fatalf("expected current-only persisted turns to skip internal title snapshot, got %#v", turns)
	}
	if turns := agentInspectorBuildPersistedTurnsFromFullCurrent(current); len(turns) != 0 {
		t.Fatalf("expected full-current persisted turns to skip internal title snapshot, got %#v", turns)
	}
}
