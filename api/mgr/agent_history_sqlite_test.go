package main

import (
	"fmt"
	"path/filepath"
	"testing"
)

func testStepsSlice(value interface{}) []map[string]interface{} {
	switch steps := value.(type) {
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(steps))
		for _, raw := range steps {
			if item, ok := raw.(map[string]interface{}); ok {
				out = append(out, item)
			}
		}
		return out
	case []map[string]interface{}:
		return steps
	default:
		return nil
	}
}

func withTempCicyRoot(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	oldRootDir := cicyRootDir
	oldDBDir := cicyDBDir
	oldProjectsDir := cicyProjectsDir
	oldWorkersDir := cicyWorkersDir
	oldSkillsDir := cicySkillsDir
	oldGlobalJSONPath := cicyGlobalJSONPath
	oldStateDir := cicyStateDir
	oldMachinesConfigPath := cicyMachinesConfigPath
	oldSharedWorkspaceDir := cicySharedWorkspaceDir

	cicyRootDir = root
	cicyDBDir = filepath.Join(root, "db")
	cicyProjectsDir = filepath.Join(root, "projects")
	cicyWorkersDir = filepath.Join(root, "workers")
	cicySkillsDir = filepath.Join(root, "skills")
	cicyGlobalJSONPath = filepath.Join(root, "global.json")
	cicyStateDir = filepath.Join(root, ".cicy")
	cicyMachinesConfigPath = filepath.Join(root, "cicy-node.json")
	cicySharedWorkspaceDir = filepath.Join(root, "shared-workspace")

	t.Cleanup(func() {
		cicyRootDir = oldRootDir
		cicyDBDir = oldDBDir
		cicyProjectsDir = oldProjectsDir
		cicyWorkersDir = oldWorkersDir
		cicySkillsDir = oldSkillsDir
		cicyGlobalJSONPath = oldGlobalJSONPath
		cicyStateDir = oldStateDir
		cicyMachinesConfigPath = oldMachinesConfigPath
		cicySharedWorkspaceDir = oldSharedWorkspaceDir
	})
}

func testHistoryRecord(q, a, qTime, aTime, model string) aiGatewayMessageRecord {
	return aiGatewayMessageRecord{
		Q:     q,
		A:     a,
		QTime: qTime,
		ATime: aTime,
		Model: model,
	}
}

func TestAgentHistoryLoadPageAggregatesAcrossConversationsByDefault(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	records := []struct {
		conversationID string
		record         aiGatewayMessageRecord
	}{
		{
			conversationID: "conv-a",
			record:         testHistoryRecord("q1", "a1", "2026-05-05T01:00:00Z", "2026-05-05T01:00:01Z", "gpt-5.5"),
		},
		{
			conversationID: "conv-b",
			record:         testHistoryRecord("q2", "a2", "2026-05-05T01:00:02Z", "2026-05-05T01:00:03Z", "gpt-5.5"),
		},
		{
			conversationID: "conv-c",
			record:         testHistoryRecord("q3", "a3", "2026-05-05T01:00:04Z", "2026-05-05T01:00:05Z", "gpt-5.5"),
		},
	}
	for _, item := range records {
		current := aiGatewayCurrentSnapshot{
			AgentID:        agentID,
			ConversationID: item.conversationID,
			Model:          item.record.Model,
		}
		reply := aiGatewayReplySnapshot{TurnID: item.conversationID + "-turn"}
		if err := agentHistoryUpsertRecord(agentID, current, reply, item.record); err != nil {
			t.Fatalf("upsert %s: %v", item.conversationID, err)
		}
	}

	page, err := agentHistoryLoadPage(agentID, "", 2, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if page.ConversationID != "" {
		t.Fatalf("expected empty conversation filter for aggregate page, got %q", page.ConversationID)
	}
	if !page.HasMore {
		t.Fatalf("expected has_more for aggregate page")
	}
	if page.NextBefore <= 0 {
		t.Fatalf("expected next_before cursor, got %d", page.NextBefore)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(page.Items))
	}
	if got := page.Items[0]["q"]; got != "q2" {
		t.Fatalf("expected first aggregate item q2, got %#v", got)
	}
	if got := page.Items[1]["q"]; got != "q3" {
		t.Fatalf("expected second aggregate item q3, got %#v", got)
	}

	page2, err := agentHistoryLoadPage(agentID, "", 2, page.NextBefore)
	if err != nil {
		t.Fatalf("load page2: %v", err)
	}
	if page2.HasMore {
		t.Fatalf("expected final aggregate page to have no more items")
	}
	if len(page2.Items) != 1 {
		t.Fatalf("expected 1 remaining item, got %d", len(page2.Items))
	}
	if got := page2.Items[0]["q"]; got != "q1" {
		t.Fatalf("expected remaining aggregate item q1, got %#v", got)
	}
}

func TestAgentHistoryLoadPageHonorsConversationFilter(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	for i, q := range []string{"q1", "q2", "q3"} {
		record := testHistoryRecord(
			q,
			fmt.Sprintf("a%d", i+1),
			fmt.Sprintf("2026-05-05T01:00:0%dZ", i),
			fmt.Sprintf("2026-05-05T01:00:1%dZ", i),
			"gpt-5.5",
		)
		current := aiGatewayCurrentSnapshot{
			AgentID:        agentID,
			ConversationID: "conv-only",
			Model:          record.Model,
		}
		reply := aiGatewayReplySnapshot{TurnID: fmt.Sprintf("conv-only-turn-%d", i+1)}
		if err := agentHistoryUpsertRecord(agentID, current, reply, record); err != nil {
			t.Fatalf("upsert %s: %v", q, err)
		}
	}

	page, err := agentHistoryLoadPage(agentID, "conv-only", 2, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if page.ConversationID != "conv-only" {
		t.Fatalf("expected conversation_id conv-only, got %q", page.ConversationID)
	}
	if !page.HasMore {
		t.Fatalf("expected filtered page to have more")
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(page.Items))
	}
	if got := page.Items[0]["q"]; got != "q2" {
		t.Fatalf("expected q2 first, got %#v", got)
	}
	if got := page.Items[1]["q"]; got != "q3" {
		t.Fatalf("expected q3 second, got %#v", got)
	}
}

func TestAgentHistoryUpsertRecordUpdatesExistingTurn(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	current := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-upsert",
		Model:          "gpt-5.5",
	}
	reply := aiGatewayReplySnapshot{TurnID: "turn-1"}
	first := testHistoryRecord("same-q", "", "2026-05-05T01:00:00Z", "", "gpt-5.5")
	second := testHistoryRecord("same-q", "same-a", "2026-05-05T01:00:00Z", "2026-05-05T01:00:02Z", "gpt-5.5")

	if err := agentHistoryUpsertRecord(agentID, current, reply, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := agentHistoryUpsertRecord(agentID, current, reply, second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	page, err := agentHistoryLoadPage(agentID, "conv-upsert", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected single merged turn, got %d", len(page.Items))
	}
	if got := page.Items[0]["a"]; got != "same-a" {
		t.Fatalf("expected updated answer, got %#v", got)
	}
}

func TestAgentHistoryLoadPagePreservesThinkingToolTimeline(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	current := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-timeline",
		Model:          "claude-opus-4-1",
	}
	reply := aiGatewayReplySnapshot{TurnID: "turn-timeline"}
	record := aiGatewayMessageRecord{
		Q:        "timeline-q",
		A:        "final answer",
		QTime:    "2026-05-05T01:00:00Z",
		ATime:    "2026-05-05T01:00:10Z",
		Model:    "claude-opus-4-1",
		Thinking: "first think",
		ToolCalls: []aiGatewayMessageToolCall{
			{Name: "read_file", Input: "{\"path\":\"/tmp/a\"}", Index: 1},
			{Name: "exec_command", Input: "{\"cmd\":\"pwd\"}", Output: "/tmp", Index: 2},
		},
	}

	if err := agentHistoryUpsertRecord(agentID, current, reply, record); err != nil {
		t.Fatalf("upsert timeline record: %v", err)
	}

	page, err := agentHistoryLoadPage(agentID, "conv-timeline", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(page.Items))
	}

	steps := testStepsSlice(page.Items[0]["steps"])
	if steps == nil {
		t.Fatalf("expected steps array, got %#v", page.Items[0]["steps"])
	}
	if len(steps) != 4 {
		t.Fatalf("expected 4 timeline steps, got %d", len(steps))
	}
	stepType := func(index int) string {
		return fmt.Sprint(steps[index]["type"])
	}
	if got := stepType(0); got != "thinking" {
		t.Fatalf("expected step 0 to be thinking, got %q", got)
	}
	if got := stepType(1); got != "tool" {
		t.Fatalf("expected step 1 to be tool, got %q", got)
	}
	if got := stepType(2); got != "tool" {
		t.Fatalf("expected step 2 to be tool, got %q", got)
	}
	if got := stepType(3); got != "text" {
		t.Fatalf("expected step 3 to be text, got %q", got)
	}

	if got := page.Items[0]["a"]; got != "final answer" {
		t.Fatalf("expected assistant answer to exclude thinking, got %#v", got)
	}
}

func TestAgentHistoryUpsertRecordMergesEvolvingTurnAcrossChangingConversationIDs(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	q := "Create a local file named ws-demo.js and then read it back"

	records := []struct {
		current aiGatewayCurrentSnapshot
		reply   aiGatewayReplySnapshot
		record  aiGatewayMessageRecord
	}{
		{
			current: aiGatewayCurrentSnapshot{AgentID: agentID, ConversationID: "conv-a", Model: "gpt-5.5"},
			reply:   aiGatewayReplySnapshot{TurnID: "turn-a"},
			record: aiGatewayMessageRecord{
				Q:         q,
				A:         "first update",
				QTime:     "2026-05-05T01:00:00Z",
				ATime:     "2026-05-05T01:00:05Z",
				Model:     "gpt-5.5",
				ToolCalls: []aiGatewayMessageToolCall{{Name: "apply_patch", Input: "patch-a", Index: 1}},
			},
		},
		{
			current: aiGatewayCurrentSnapshot{AgentID: agentID, ConversationID: "conv-b", Model: "gpt-5.5"},
			reply:   aiGatewayReplySnapshot{TurnID: "turn-b"},
			record: aiGatewayMessageRecord{
				Q:         q,
				A:         "second update",
				QTime:     "2026-05-05T01:00:06Z",
				ATime:     "2026-05-05T01:00:10Z",
				Model:     "gpt-5.5",
				ToolCalls: []aiGatewayMessageToolCall{{Name: "exec_command", Input: "cat ws-demo.js", Index: 2}},
			},
		},
		{
			current: aiGatewayCurrentSnapshot{AgentID: agentID, ConversationID: "conv-c", Model: "gpt-5.5"},
			reply:   aiGatewayReplySnapshot{TurnID: "turn-c"},
			record: aiGatewayMessageRecord{
				Q:         q,
				A:         "final answer",
				QTime:     "2026-05-05T01:00:12Z",
				ATime:     "2026-05-05T01:00:16Z",
				Model:     "gpt-5.5",
				ToolCalls: []aiGatewayMessageToolCall{{Name: "web_search_call", Input: "Codex OpenAI official", Index: 3}},
			},
		},
	}

	for _, item := range records {
		if err := agentHistoryUpsertRecord(agentID, item.current, item.reply, item.record); err != nil {
			t.Fatalf("upsert evolving turn: %v", err)
		}
	}

	page, err := agentHistoryLoadPage(agentID, "", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected single merged history item, got %d", len(page.Items))
	}
	if got := page.Items[0]["a"]; got != "final answer" {
		t.Fatalf("expected final answer, got %#v", got)
	}
}

func TestAgentHistoryUpsertRecordPrefersCurrentTimelineOrderForCodex(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	current := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-current-order",
		Model:          "gpt-5.5",
		Body: map[string]interface{}{
			"input": []interface{}{
				map[string]interface{}{
					"role": "user",
					"type": "message",
					"content": []interface{}{
						map[string]interface{}{"type": "input_text", "text": "Write ws-demo-order.js and read it back"},
					},
				},
				map[string]interface{}{
					"role":  "assistant",
					"type":  "message",
					"phase": "commentary",
					"content": []interface{}{
						map[string]interface{}{"type": "output_text", "text": "I’m comparing two tiny websocket shapes first."},
					},
				},
				map[string]interface{}{
					"type":    "function_call",
					"name":    "exec_command",
					"call_id": "call_1",
					"arguments": "{\"cmd\":\"pwd\"}",
				},
				map[string]interface{}{
					"type":    "function_call_output",
					"call_id": "call_1",
					"output":  "/tmp/workspace",
				},
				map[string]interface{}{
					"role":  "assistant",
					"type":  "message",
					"phase": "commentary",
					"content": []interface{}{
						map[string]interface{}{"type": "output_text", "text": "I found an existing file, so I’m reading it before editing."},
					},
				},
				map[string]interface{}{
					"type":    "function_call",
					"name":    "exec_command",
					"call_id": "call_2",
					"arguments": "{\"cmd\":\"sed -n '1,120p' ws-demo-order.js\"}",
				},
				map[string]interface{}{
					"type":    "function_call_output",
					"call_id": "call_2",
					"output":  "const x = 1;",
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
		TurnID:    "turn-current-order",
		Status:    "completed",
		StartedAt: "2026-05-05T01:00:00Z",
		UpdatedAt: "2026-05-05T01:00:10Z",
	}
	record := aiGatewayMessageRecord{
		Q:        "Write ws-demo-order.js and read it back",
		A:        "Done.",
		QTime:    "2026-05-05T01:00:00Z",
		ATime:    "2026-05-05T01:00:10Z",
		Model:    "gpt-5.5",
		Thinking: "flattened thinking that should not replace ordered steps",
		ToolCalls: []aiGatewayMessageToolCall{
			{Name: "exec_command", Input: "{\"cmd\":\"pwd\"}", Output: "/tmp/workspace", Index: 1},
			{Name: "exec_command", Input: "{\"cmd\":\"sed -n '1,120p' ws-demo-order.js\"}", Output: "const x = 1;", Index: 2},
		},
	}

	if err := agentHistoryUpsertRecord(agentID, current, reply, record); err != nil {
		t.Fatalf("upsert record: %v", err)
	}

	page, err := agentHistoryLoadPage(agentID, "conv-current-order", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(page.Items))
	}
	steps := testStepsSlice(page.Items[0]["steps"])
	if steps == nil {
		t.Fatalf("expected steps array, got %#v", page.Items[0]["steps"])
	}
	if len(steps) != 5 {
		t.Fatalf("expected 5 ordered steps, got %d", len(steps))
	}
	stepType := func(index int) string {
		return fmt.Sprint(steps[index]["type"])
	}
	if got := stepType(0); got != "thinking" {
		t.Fatalf("expected step 0 thinking, got %q", got)
	}
	if got := stepType(1); got != "tool" {
		t.Fatalf("expected step 1 tool, got %q", got)
	}
	if got := stepType(2); got != "thinking" {
		t.Fatalf("expected step 2 thinking, got %q", got)
	}
	if got := stepType(3); got != "tool" {
		t.Fatalf("expected step 3 tool, got %q", got)
	}
	if got := stepType(4); got != "text" {
		t.Fatalf("expected step 4 text, got %q", got)
	}
}

func TestAIGatewaySyncLatestCurrentHistoryRepairsPreviousTurnBeforeNextQuestion(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	partialCurrent := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-sync",
		Model:          "gpt-5.5",
		Timestamp:      "2026-05-05T01:00:00Z",
		UpdatedAt:      "2026-05-05T01:00:10Z",
		Body: map[string]interface{}{
			"input": []interface{}{
				map[string]interface{}{
					"role": "user",
					"type": "message",
					"content": []interface{}{
						map[string]interface{}{"type": "input_text", "text": "Write ws-demo-sync.js and read it back"},
					},
				},
				map[string]interface{}{
					"role":  "assistant",
					"type":  "message",
					"phase": "commentary",
					"content": []interface{}{
						map[string]interface{}{"type": "output_text", "text": "First thinking."},
					},
				},
				map[string]interface{}{
					"type":    "custom_tool_call",
					"name":    "apply_patch",
					"call_id": "call_1",
					"input":   "*** Begin Patch\n*** Add File: ws-demo-sync.js\n+console.log('hi')\n*** End Patch",
				},
				map[string]interface{}{
					"type":    "custom_tool_call_output",
					"call_id": "call_1",
					"output":  "Success",
				},
				map[string]interface{}{
					"role":  "assistant",
					"type":  "message",
					"phase": "commentary",
					"content": []interface{}{
						map[string]interface{}{"type": "output_text", "text": "Second thinking."},
					},
				},
				map[string]interface{}{
					"type":      "web_search_call",
					"status":    "completed",
					"action":    map[string]interface{}{"query": "Codex official docs"},
					"call_id":   "call_2",
					"name":      "web_search_call",
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
		TurnID:    "turn-sync",
		Status:    "completed",
		StartedAt: "2026-05-05T01:00:00Z",
		UpdatedAt: "2026-05-05T01:00:10Z",
		Models:    []string{"gpt-5.5"},
	}

	partialRecord := aiGatewayMessageRecord{
		Q:     "Write ws-demo-sync.js and read it back",
		A:     "Second thinking.Done.",
		QTime: "2026-05-05T01:00:00Z",
		ATime: "2026-05-05T01:00:05Z",
		Model: "gpt-5.5",
	}
	if err := agentHistoryUpsertRecord(agentID, partialCurrent, reply, partialRecord); err != nil {
		t.Fatalf("seed partial record: %v", err)
	}
	if err := aiGatewayWriteJSONAtomic(filepath.Join(aiGatewayHistoryDir(agentID), "current.json"), partialCurrent); err != nil {
		t.Fatalf("write current: %v", err)
	}
	if err := aiGatewayWriteJSONAtomic(filepath.Join(aiGatewayHistoryDir(agentID), "reply.json"), reply); err != nil {
		t.Fatalf("write reply: %v", err)
	}

	if err := aiGatewaySyncLatestCurrentHistory(agentID); err != nil {
		t.Fatalf("sync latest current history: %v", err)
	}

	page, err := agentHistoryLoadPage(agentID, "conv-sync", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(page.Items))
	}
	if got := page.Items[0]["a"]; got != "Done." {
		t.Fatalf("expected repaired final answer, got %#v", got)
	}
	steps := testStepsSlice(page.Items[0]["steps"])
	if len(steps) != 5 {
		t.Fatalf("expected repaired 5-step timeline, got %d", len(steps))
	}
}

func TestAIGatewaySyncLatestCurrentHistoryPersistsMultipleSimpleTurns(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	current := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-sync-simple",
		Model:          "gpt-5.5",
		Timestamp:      "2026-05-05T01:00:00Z",
		UpdatedAt:      "2026-05-05T01:00:05Z",
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
					"role": "assistant",
					"type": "message",
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
					"role": "assistant",
					"type": "message",
					"phase": "final_answer",
					"content": []interface{}{
						map[string]interface{}{"type": "output_text", "text": "beta"},
					},
				},
			},
		},
	}
	reply := aiGatewayReplySnapshot{
		TurnID:    "turn-sync-simple",
		Status:    "completed",
		StartedAt: "2026-05-05T01:00:00Z",
		UpdatedAt: "2026-05-05T01:00:05Z",
		Models:    []string{"gpt-5.5"},
	}
	if err := aiGatewayWriteJSONAtomic(filepath.Join(aiGatewayHistoryDir(agentID), "current.json"), current); err != nil {
		t.Fatalf("write current: %v", err)
	}
	if err := aiGatewayWriteJSONAtomic(filepath.Join(aiGatewayHistoryDir(agentID), "reply.json"), reply); err != nil {
		t.Fatalf("write reply: %v", err)
	}

	if err := aiGatewaySyncLatestCurrentHistory(agentID); err != nil {
		t.Fatalf("sync current history: %v", err)
	}

	page, err := agentHistoryLoadPage(agentID, "conv-sync-simple", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(page.Items))
	}
	if got := page.Items[0]["a"]; got != "alpha" {
		t.Fatalf("expected first answer alpha, got %#v", got)
	}
	if got := page.Items[1]["a"]; got != "beta" {
		t.Fatalf("expected second answer beta, got %#v", got)
	}
}

func TestAgentHistoryUpsertRecordAddsTextStepWhenPersistedItemHasNoSteps(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	current := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-text-step",
		Model:          "gpt-5.5",
	}
	reply := aiGatewayReplySnapshot{TurnID: "turn-text-step"}
	record := aiGatewayMessageRecord{
		Q:     "Reply with exactly one word: gamma",
		A:     "gamma",
		QTime: "2026-05-05T01:00:00Z",
		ATime: "2026-05-05T01:00:01Z",
		Model: "gpt-5.5",
	}
	item := M{
		"q":     record.Q,
		"a":     "",
		"steps": []M{},
		"model": record.Model,
	}
	if err := agentHistoryUpsertRecordWithItem(agentID, current, reply, record, item); err != nil {
		t.Fatalf("upsert with item: %v", err)
	}

	page, err := agentHistoryLoadPage(agentID, "conv-text-step", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(page.Items))
	}
	if got := page.Items[0]["a"]; got != "gamma" {
		t.Fatalf("expected answer gamma, got %#v", got)
	}
	steps := testStepsSlice(page.Items[0]["steps"])
	if len(steps) != 1 {
		t.Fatalf("expected 1 text step, got %d", len(steps))
	}
	if got := fmt.Sprint(steps[0]["type"]); got != "text" {
		t.Fatalf("expected text step, got %q", got)
	}
}
