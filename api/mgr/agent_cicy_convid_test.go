package main

import (
	"regexp"
	"testing"
)

var uuidV4Shape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestCicyNewConversationIDIsRandomUUID(t *testing.T) {
	a := cicyNewConversationID()
	b := cicyNewConversationID()
	if !uuidV4Shape.MatchString(a) {
		t.Fatalf("conversation id %q is not a v4 UUID", a)
	}
	if a == b {
		t.Fatalf("two generated conversation ids must differ, both were %q", a)
	}
}

func TestCicySeededSnapshotReplacesOnlyMessages(t *testing.T) {
	live := aiGatewayCurrentSnapshot{
		TurnID:         "t-1",
		AgentID:        "w-9",
		ConversationID: "old-conv",
		RequestID:      "req-1",
		Provider:       "anthropic",
		Model:          "deepseek-v4-pro",
		URL:            "https://gateway.example/v1/messages",
		Method:         "POST",
		Headers:        map[string][]string{"Content-Type": {"application/json"}},
		Body: map[string]interface{}{
			"model":      "deepseek-v4-pro",
			"max_tokens": 2048,
			"system":     []interface{}{map[string]interface{}{"type": "text", "text": "PM persona"}},
			"tools":      []interface{}{map[string]interface{}{"name": "todo_add"}},
			"messages": []interface{}{
				map[string]interface{}{"id": 1, "role": "user", "content": "hi"},
			},
		},
		Status:     "completed",
		Timestamp:  "2026-06-10T00:00:00Z",
		StartedAt:  "2026-06-10T00:00:00Z",
		RequestIDs: []string{"req-1"},
	}
	summary := []M{{"role": "user", "content": "[summary] …"}}
	out := cicySeededSnapshot(live, "w-9", "new-conv", "2026-06-10T01:00:00Z", summary)

	// Conversation-scoped fields carry over verbatim…
	if out.Provider != "anthropic" || out.Model != "deepseek-v4-pro" || out.URL != live.URL ||
		out.Method != "POST" || out.Headers == nil {
		t.Fatalf("conversation-scoped fields must carry over, got %+v", out)
	}
	// …while per-TURN identifiers reset (a seed is not a wire request; stale
	// turn/request ids made a cleared conversation look like the old one).
	if out.TurnID != "" || out.RequestID != "" || len(out.RequestIDs) != 0 || len(out.ActiveRequestIDs) != 0 {
		t.Fatalf("per-turn ids must reset in a seed, got %+v", out)
	}
	if out.ConversationID != "new-conv" {
		t.Fatalf("conversation id must be the seed's, got %q", out.ConversationID)
	}
	body := out.Body.(map[string]interface{})
	if _, has := body["system"]; !has {
		t.Fatalf("body.system must survive the seed")
	}
	if _, has := body["tools"]; !has {
		t.Fatalf("body.tools must survive the seed")
	}
	if body["max_tokens"] != 2048 {
		t.Fatalf("body extras must survive the seed")
	}
	msgs := body["messages"].([]interface{})
	if len(msgs) != 1 {
		t.Fatalf("messages must be replaced by the seed content, got %d", len(msgs))
	}
	if msgs[0].(map[string]interface{})["content"] != "[summary] …" {
		t.Fatalf("seeded message wrong: %v", msgs[0])
	}
}

func TestCicySeededSnapshotKeepsChatShapeSystemMessage(t *testing.T) {
	live := aiGatewayCurrentSnapshot{
		Body: map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{"role": "system", "content": "PM persona"},
				map[string]interface{}{"id": 1, "role": "user", "content": "hi"},
			},
		},
	}
	out := cicySeededSnapshot(live, "w-9", "c", "2026-06-10T01:00:00Z", []M{{"role": "user", "content": "[summary]"}})
	msgs := out.Body.(map[string]interface{})["messages"].([]interface{})
	if len(msgs) != 2 {
		t.Fatalf("chat-shape seed must keep the leading system message, got %d msgs", len(msgs))
	}
	if msgs[0].(map[string]interface{})["role"] != "system" {
		t.Fatalf("first message must stay the system persona")
	}
}
