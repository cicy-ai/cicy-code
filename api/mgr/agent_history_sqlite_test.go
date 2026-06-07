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
		if _, err := agentHistoryUpsertRecord(agentID, current, reply, item.record); err != nil {
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
		if _, err := agentHistoryUpsertRecord(agentID, current, reply, record); err != nil {
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

	if _, err := agentHistoryUpsertRecord(agentID, current, reply, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if _, err := agentHistoryUpsertRecord(agentID, current, reply, second); err != nil {
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

func TestAIGatewaySyncCurrentSnapshotToHistoryDBReturnsMaxInputItemID(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	body := aiGatewayAnnotateCurrentBodyHistoryIDs("w-test-annotate", map[string]interface{}{
		"input": []interface{}{
			map[string]interface{}{
				"role": "user",
				"type": "message",
				"content": []interface{}{
					map[string]interface{}{"type": "input_text", "text": "hello input"},
				},
			},
			map[string]interface{}{
				"role": "assistant",
				"type": "message",
				"content": []interface{}{
					map[string]interface{}{"type": "output_text", "text": "hi"},
				},
			},
		},
	})
	current := aiGatewayCurrentSnapshot{
		TurnID:         "turn-input-1",
		AgentID:        agentID,
		ConversationID: "conv-input-1",
		RequestID:      "req-input-1",
		Provider:       "openai",
		Model:          "gpt-5.5",
		Method:         "POST",
		URL:            "http://127.0.0.1:8008/api/ai-gateway/openai/" + agentID,
		Timestamp:      "2026-05-08T01:00:00Z",
		StartedAt:      "2026-05-08T01:00:00Z",
		UpdatedAt:      "2026-05-08T01:00:01Z",
		Status:         "thinking",
		Body:           body,
		MaxHistoryID:   aiGatewayCurrentBodyMaxHistoryID(body),
	}
	if err := aiGatewayWriteJSONAtomic(filepath.Join(aiGatewayHistoryDir(agentID), "current.json"), current); err != nil {
		t.Fatalf("write current: %v", err)
	}
	snapshotID, err := aiGatewaySyncCurrentSnapshotToHistoryDB(agentID)
	if err != nil {
		t.Fatalf("sync current snapshot: %v", err)
	}
	if snapshotID != 2 {
		t.Fatalf("expected snapshot id 2, got %d", snapshotID)
	}
	conversationID, maxID, err := agentHistoryCurrentMaxID(agentID, "")
	if err != nil {
		t.Fatalf("current max id: %v", err)
	}
	if conversationID != "conv-input-1" {
		t.Fatalf("unexpected conversation_id: %q", conversationID)
	}
	if maxID != 2 {
		t.Fatalf("expected max id 2, got %d", maxID)
	}
	_, item, ok, err := agentHistoryLoadCurrentItemByID(agentID, conversationID, 2)
	if err != nil {
		t.Fatalf("load current item: %v", err)
	}
	if !ok {
		t.Fatalf("expected current item")
	}
	if got := aiGatewayString(item["role"]); got != "assistant" {
		t.Fatalf("unexpected role: %q", got)
	}
}

func TestAIGatewaySyncCurrentSnapshotToHistoryDBReturnsMaxMessagesItemID(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	body := aiGatewayAnnotateCurrentBodyHistoryIDs("w-test-annotate", map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "hello messages"},
				},
			},
			map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "reply messages"},
				},
			},
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "tool_result", "tool_use_id": "call_1", "content": "ok"},
				},
			},
		},
	})
	current := aiGatewayCurrentSnapshot{
		TurnID:         "turn-messages-1",
		AgentID:        agentID,
		ConversationID: "conv-messages-1",
		RequestID:      "req-messages-1",
		Provider:       "anthropic",
		Model:          "claude-opus-4-7",
		Method:         "POST",
		URL:            "http://127.0.0.1:8008/api/ai-gateway/anthropic/" + agentID,
		Timestamp:      "2026-05-08T01:10:00Z",
		StartedAt:      "2026-05-08T01:10:00Z",
		UpdatedAt:      "2026-05-08T01:10:01Z",
		Status:         "thinking",
		Body:           body,
		MaxHistoryID:   aiGatewayCurrentBodyMaxHistoryID(body),
	}
	if err := aiGatewayWriteJSONAtomic(filepath.Join(aiGatewayHistoryDir(agentID), "current.json"), current); err != nil {
		t.Fatalf("write current: %v", err)
	}
	snapshotID, err := aiGatewaySyncCurrentSnapshotToHistoryDB(agentID)
	if err != nil {
		t.Fatalf("sync current snapshot: %v", err)
	}
	if snapshotID != 3 {
		t.Fatalf("expected snapshot id 3, got %d", snapshotID)
	}
	page, err := agentHistoryLoadCurrentItemsPage(agentID, "conv-messages-1", 2, 0)
	if err != nil {
		t.Fatalf("load current page: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(page.Items))
	}
	if got := int64(aiGatewayInt(page.Items[0]["history_id"])); got != 2 {
		t.Fatalf("expected first paged history_id 2, got %d", got)
	}
	if got := int64(aiGatewayInt(page.Items[1]["history_id"])); got != 3 {
		t.Fatalf("expected second paged history_id 3, got %d", got)
	}
	if !page.HasMore || page.NextBefore != 2 {
		t.Fatalf("expected has_more with next_before=2, got has_more=%v next_before=%d", page.HasMore, page.NextBefore)
	}
}

func TestAgentHistoryUpsertRecordDoesNotMergeSameQuestionAcrossConversations(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	recordA := testHistoryRecord("same-q", "a1", "2026-05-05T01:00:00Z", "2026-05-05T01:00:02Z", "gpt-5.5")
	recordB := testHistoryRecord("same-q", "a2", "2026-05-05T01:05:00Z", "2026-05-05T01:05:03Z", "gpt-5.5")

	currentA := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-a",
		Model:          "gpt-5.5",
	}
	currentB := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-b",
		Model:          "gpt-5.5",
	}
	replyA := aiGatewayReplySnapshot{TurnID: "turn-a"}
	replyB := aiGatewayReplySnapshot{TurnID: "turn-b"}

	idA, err := agentHistoryUpsertRecord(agentID, currentA, replyA, recordA)
	if err != nil {
		t.Fatalf("upsert conv-a: %v", err)
	}
	idB, err := agentHistoryUpsertRecord(agentID, currentB, replyB, recordB)
	if err != nil {
		t.Fatalf("upsert conv-b: %v", err)
	}
	if idA <= 0 || idB <= 0 {
		t.Fatalf("expected positive history ids, got %d and %d", idA, idB)
	}
	if idA == idB {
		t.Fatalf("expected distinct history ids across conversations, got %d", idA)
	}

	pageA, err := agentHistoryLoadPage(agentID, "conv-a", 10, 0)
	if err != nil {
		t.Fatalf("load conv-a: %v", err)
	}
	pageB, err := agentHistoryLoadPage(agentID, "conv-b", 10, 0)
	if err != nil {
		t.Fatalf("load conv-b: %v", err)
	}
	if len(pageA.Items) != 1 || len(pageB.Items) != 1 {
		t.Fatalf("expected one item per conversation, got %d and %d", len(pageA.Items), len(pageB.Items))
	}
	if got := pageA.Items[0]["a"]; got != "a1" {
		t.Fatalf("unexpected conv-a answer: %#v", got)
	}
	if got := pageB.Items[0]["a"]; got != "a2" {
		t.Fatalf("unexpected conv-b answer: %#v", got)
	}
}

func TestAgentHistoryUpsertRecordDoesNotMergeSameQuestionAcrossTurnsInSameConversation(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	current := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-same",
		Model:          "gpt-5.5",
	}
	recordA := testHistoryRecord("same-q", "a1", "2026-05-05T01:00:00Z", "2026-05-05T01:00:02Z", "gpt-5.5")
	recordB := testHistoryRecord("same-q", "a2", "2026-05-05T01:05:00Z", "2026-05-05T01:05:03Z", "gpt-5.5")

	idA, err := agentHistoryUpsertRecord(agentID, current, aiGatewayReplySnapshot{TurnID: "turn-a"}, recordA)
	if err != nil {
		t.Fatalf("upsert turn-a: %v", err)
	}
	idB, err := agentHistoryUpsertRecord(agentID, current, aiGatewayReplySnapshot{TurnID: "turn-b"}, recordB)
	if err != nil {
		t.Fatalf("upsert turn-b: %v", err)
	}
	if idA <= 0 || idB <= 0 || idA == idB {
		t.Fatalf("expected distinct history ids for repeated prompt in same conversation, got %d and %d", idA, idB)
	}

	page, err := agentHistoryLoadPage(agentID, "conv-same", 10, 0)
	if err != nil {
		t.Fatalf("load conv-same: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 persisted turns, got %d", len(page.Items))
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

	if _, err := agentHistoryUpsertRecord(agentID, current, reply, record); err != nil {
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

func TestAgentHistoryUpsertRecordDoesNotMergeAcrossChangingConversationIDs(t *testing.T) {
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

	ids := make([]int64, 0, len(records))
	for _, item := range records {
		historyID, err := agentHistoryUpsertRecord(agentID, item.current, item.reply, item.record)
		if err != nil {
			t.Fatalf("upsert evolving turn: %v", err)
		}
		ids = append(ids, historyID)
	}
	for i, historyID := range ids {
		if historyID <= 0 {
			t.Fatalf("expected positive history id at index %d, got %d", i, historyID)
		}
		if i > 0 && historyID == ids[0] {
			t.Fatalf("expected distinct history ids across changing conversations, got %v", ids)
		}
	}

	page, err := agentHistoryLoadPage(agentID, "", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("expected 3 history items, got %d", len(page.Items))
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
					"type":      "function_call",
					"name":      "exec_command",
					"call_id":   "call_1",
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
					"type":      "function_call",
					"name":      "exec_command",
					"call_id":   "call_2",
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

	if _, err := agentHistoryUpsertRecord(agentID, current, reply, record); err != nil {
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

func TestAIGatewaySyncLatestCurrentHistorySkipsOnlyActiveTurn(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	current := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-active-only",
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
					"type":    "web_search_call",
					"status":  "completed",
					"action":  map[string]interface{}{"query": "Codex official docs"},
					"call_id": "call_2",
					"name":    "web_search_call",
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
	if err := aiGatewayWriteJSONAtomic(filepath.Join(aiGatewayHistoryDir(agentID), "current.json"), current); err != nil {
		t.Fatalf("write current: %v", err)
	}
	aiGatewayStoreLiveReplySnapshot(agentID, reply)

	historyID, err := aiGatewaySyncLatestCurrentHistory(agentID)
	if err != nil {
		t.Fatalf("sync latest current history: %v", err)
	}
	if historyID != 0 {
		t.Fatalf("expected no history id when current only contains the active turn, got %d", historyID)
	}

	page, err := agentHistoryLoadPage(agentID, "conv-active-only", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("expected no persisted item before a new question appears, got %d", len(page.Items))
	}
}

func TestAIGatewaySyncLatestCurrentHistoryPersistsOnlyPreviousTurns(t *testing.T) {
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
	aiGatewayStoreLiveReplySnapshot(agentID, reply)

	historyID, err := aiGatewaySyncLatestCurrentHistory(agentID)
	if err != nil {
		t.Fatalf("sync current history: %v", err)
	}
	if historyID <= 0 {
		t.Fatalf("expected positive history id from sync because the previous turn should flush, got %d", historyID)
	}

	page, err := agentHistoryLoadPage(agentID, "conv-sync-simple", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected only the previous completed turn to persist, got %d", len(page.Items))
	}
	if got := page.Items[0]["a"]; got != "alpha" {
		t.Fatalf("expected persisted previous answer alpha, got %#v", got)
	}
}

func TestAIGatewaySyncLatestCurrentHistoryPersistsOnlyPreviousTurnsForClaude(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	current := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-sync-claude",
		Model:          "claude-opus-4-7",
		Timestamp:      "2026-05-05T01:00:00Z",
		UpdatedAt:      "2026-05-05T01:00:05Z",
		Body: map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{
					"role": "user",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "Reply with exactly one word: alpha"},
					},
				},
				map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "alpha"},
					},
				},
				map[string]interface{}{
					"role": "user",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "Reply with exactly one word: beta"},
					},
				},
				map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "beta"},
					},
				},
			},
		},
	}
	if err := aiGatewayWriteJSONAtomic(filepath.Join(aiGatewayHistoryDir(agentID), "current.json"), current); err != nil {
		t.Fatalf("write current: %v", err)
	}

	historyID, err := aiGatewaySyncLatestCurrentHistory(agentID)
	if err != nil {
		t.Fatalf("sync current history: %v", err)
	}
	if historyID <= 0 {
		t.Fatalf("expected positive history id from sync because the previous Claude turn should flush, got %d", historyID)
	}

	page, err := agentHistoryLoadPage(agentID, "conv-sync-claude", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected only the previous Claude turn to persist, got %d", len(page.Items))
	}
	if got := page.Items[0]["a"]; got != "alpha" {
		t.Fatalf("expected persisted Claude previous answer alpha, got %#v", got)
	}
}

func TestAIGatewaySyncLatestCurrentHistorySkipsUserOnlyPendingTurn(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	current := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-pending-only",
		Model:          "gpt-5.5",
		Timestamp:      "2026-05-05T01:00:00Z",
		UpdatedAt:      "2026-05-05T01:00:01Z",
		Body: map[string]interface{}{
			"input": []interface{}{
				map[string]interface{}{
					"role": "user",
					"type": "message",
					"content": []interface{}{
						map[string]interface{}{"type": "input_text", "text": "prompt not submitted yet"},
					},
				},
			},
		},
	}
	if err := aiGatewayWriteJSONAtomic(filepath.Join(aiGatewayHistoryDir(agentID), "current.json"), current); err != nil {
		t.Fatalf("write current: %v", err)
	}
	aiGatewayStoreLiveReplySnapshot(agentID, aiGatewayReplySnapshot{})

	historyID, err := aiGatewaySyncLatestCurrentHistory(agentID)
	if err != nil {
		t.Fatalf("sync current history: %v", err)
	}
	if historyID != 0 {
		t.Fatalf("expected no history id for user-only pending turn, got %d", historyID)
	}
	page, err := agentHistoryLoadPage(agentID, "conv-pending-only", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("expected no persisted items for user-only pending turn, got %d", len(page.Items))
	}
}

func TestAIGatewaySyncLatestCurrentHistoryMergesContinuationGoIntoPreviousTurn(t *testing.T) {
	withTempCicyRoot(t)

	agentID := "w-10001"
	current := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: "conv-sync-go",
		Model:          "gpt-5.5",
		Timestamp:      "2026-05-05T01:00:00Z",
		UpdatedAt:      "2026-05-05T01:00:20Z",
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
	reply := aiGatewayReplySnapshot{
		TurnID:    "turn-sync-go",
		Status:    "completed",
		StartedAt: "2026-05-05T01:00:00Z",
		UpdatedAt: "2026-05-05T01:00:20Z",
		Models:    []string{"gpt-5.5"},
	}
	if err := aiGatewayWriteJSONAtomic(filepath.Join(aiGatewayHistoryDir(agentID), "current.json"), current); err != nil {
		t.Fatalf("write current: %v", err)
	}
	aiGatewayStoreLiveReplySnapshot(agentID, reply)

	historyID, err := aiGatewaySyncLatestCurrentHistory(agentID)
	if err != nil {
		t.Fatalf("sync current history: %v", err)
	}
	if historyID != 0 {
		t.Fatalf("expected no history id because continuation has not been closed by a new q, got %d", historyID)
	}

	page, err := agentHistoryLoadPage(agentID, "conv-sync-go", 10, 0)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("expected no persisted item until a subsequent new q closes the continuation turn, got %d", len(page.Items))
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
	if _, err := agentHistoryUpsertRecordWithItem(agentID, current, reply, record, item); err != nil {
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
