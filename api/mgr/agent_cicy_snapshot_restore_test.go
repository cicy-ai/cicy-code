package main

import (
	"testing"
)

func TestCicyStripWireAnnotationsDropsDisplayIDKeepsProtocolID(t *testing.T) {
	msg := map[string]interface{}{
		"id":   7, // display annotation → must go
		"role": "assistant",
		"content": []interface{}{
			map[string]interface{}{"type": "tool_use", "id": "toolu_abc", "name": "todo_add", "input": map[string]interface{}{}},
			map[string]interface{}{"type": "text", "text": "done", "cache_control": map[string]interface{}{"type": "ephemeral"}},
		},
	}
	out := cicyStripWireAnnotations(msg)
	if _, has := out["id"]; has {
		t.Fatalf("display id must be stripped")
	}
	blocks := out["content"].([]interface{})
	tu := blocks[0].(map[string]interface{})
	if tu["id"] != "toolu_abc" {
		t.Fatalf("protocol tool_use id must be preserved, got %v", tu["id"])
	}
	txt := blocks[1].(map[string]interface{})
	if _, has := txt["cache_control"]; has {
		t.Fatalf("cache_control must be stripped on restore")
	}
	// original untouched (non-destructive)
	if _, has := msg["id"]; !has {
		t.Fatalf("source message must not be mutated")
	}
}

func TestCicyMessagesFromCurrentBodyAnthropicShape(t *testing.T) {
	body := map[string]interface{}{
		"system": []interface{}{map[string]interface{}{"type": "text", "text": "PM persona"}},
		"messages": []interface{}{
			map[string]interface{}{"id": 1, "role": "user", "content": "hi"},
			map[string]interface{}{"id": 2, "role": "assistant", "content": []interface{}{
				map[string]interface{}{"type": "text", "text": "hello"},
			}},
		},
	}
	msgs := cicyMessagesFromCurrentBody(body)
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
	for i, m := range msgs {
		if _, has := m["id"]; has {
			t.Fatalf("message %d still carries a display id", i)
		}
	}
	if msgs[0]["content"] != "hi" {
		t.Fatalf("user content must survive verbatim")
	}
}

func TestCicyMessagesFromCurrentBodyChatShape(t *testing.T) {
	// What the anthropic→chat bridge writes when the provider is openai-protocol.
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "system", "content": "PM persona"},
			map[string]interface{}{"id": 1, "role": "user", "content": "加个todo"},
			map[string]interface{}{"id": 2, "role": "assistant", "content": "", "reasoning_content": ".",
				"tool_calls": []interface{}{map[string]interface{}{
					"id": "call_1", "type": "function",
					"function": map[string]interface{}{"name": "todo_add", "arguments": `{"text":"x"}`},
				}},
			},
			map[string]interface{}{"id": 3, "role": "tool", "tool_call_id": "call_1", "content": "ok #5"},
			map[string]interface{}{"id": 4, "role": "assistant", "content": "已加 #5", "reasoning_content": "user wants a todo"},
		},
	}
	msgs := cicyMessagesFromCurrentBody(body)
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages (system dropped, tool merged), got %d: %v", len(msgs), msgs)
	}
	// assistant tool_calls → tool_use block
	a1 := msgs[1]["content"].([]interface{})
	tu := a1[0].(map[string]interface{})
	if tu["type"] != "tool_use" || tu["id"] != "call_1" || tu["name"] != "todo_add" {
		t.Fatalf("tool_calls must convert to tool_use, got %v", tu)
	}
	// role:tool → user tool_result
	tr := msgs[2]["content"].([]interface{})[0].(map[string]interface{})
	if tr["type"] != "tool_result" || tr["tool_use_id"] != "call_1" || tr["content"] != "ok #5" {
		t.Fatalf("tool message must convert to tool_result, got %v", tr)
	}
	// "." placeholder reasoning skipped; real reasoning → thinking block
	a2 := msgs[3]["content"].([]interface{})
	first := a2[0].(map[string]interface{})
	if first["type"] != "thinking" || first["thinking"] != "user wants a todo" {
		t.Fatalf("real reasoning_content must restore as thinking, got %v", first)
	}
	if len(msgs[1]["content"].([]interface{})) != 1 {
		t.Fatalf("placeholder '.' reasoning must NOT become a thinking block")
	}
}

func TestCicyAssistantFromReplyItemsSingleRound(t *testing.T) {
	items := []map[string]interface{}{
		{"id": 1, "type": "thinking", "thinking": "the user greeted"},
		{"id": 2, "type": "text", "text": "Hi! 有什么需要推进的?"},
	}
	current := []M{{"role": "user", "content": "hi"}}
	msg, ok := cicyAssistantFromReplyItems(items, current)
	if !ok {
		t.Fatalf("single-round reply must rebuild")
	}
	blocks := msg["content"].([]interface{})
	if len(blocks) != 1 {
		t.Fatalf("thinking is dropped, text kept: want 1 block, got %d", len(blocks))
	}
	b := blocks[0].(map[string]interface{})
	if b["type"] != "text" || b["text"] != "Hi! 有什么需要推进的?" {
		t.Fatalf("unexpected block %v", b)
	}
	if _, has := b["id"]; has {
		t.Fatalf("display id must not leak into the rebuilt message")
	}
}

func TestCicyAssistantFromReplyItemsSuffixAfterRecordedToolRound(t *testing.T) {
	// Round 1 (tool_use tu_1) is already inside current.json via round 2's
	// request; only the final text after it is new.
	items := []map[string]interface{}{
		{"id": 1, "type": "text", "text": "我来加"},
		{"id": 2, "type": "tool_use", "tool_id": "tu_1", "name": "todo_add", "input": map[string]interface{}{"text": "x"}},
		{"id": 3, "type": "text", "text": "加好了 #5"},
	}
	current := []M{
		{"role": "user", "content": "加个todo"},
		{"role": "assistant", "content": []interface{}{
			map[string]interface{}{"type": "text", "text": "我来加"},
			map[string]interface{}{"type": "tool_use", "id": "tu_1", "name": "todo_add", "input": map[string]interface{}{"text": "x"}},
		}},
		{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "tool_result", "tool_use_id": "tu_1", "content": "ok #5"},
		}},
	}
	msg, ok := cicyAssistantFromReplyItems(items, current)
	if !ok {
		t.Fatalf("suffix must rebuild")
	}
	blocks := msg["content"].([]interface{})
	if len(blocks) != 1 {
		t.Fatalf("only the post-tool suffix is new; want 1 block, got %d: %v", len(blocks), blocks)
	}
	if blocks[0].(map[string]interface{})["text"] != "加好了 #5" {
		t.Fatalf("wrong suffix block: %v", blocks[0])
	}
}

func TestCicyRequestMessagesStripsDisplayIDs(t *testing.T) {
	history := []M{
		{"id": 1, "role": "user", "content": "hi"},
		{"id": 2, "role": "assistant", "content": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
		}},
		{"id": 3, "role": "user", "content": "再说一遍"},
	}
	out := cicyRequestMessages(history)
	for i, m := range out {
		if _, has := m["id"]; has {
			t.Fatalf("request message %d still carries a display id", i)
		}
	}
	// in-memory history untouched
	if _, has := history[0]["id"]; !has {
		t.Fatalf("session history must not be mutated by request build")
	}
}
