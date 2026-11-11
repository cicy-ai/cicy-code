// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

const threadTestSSE = `event: message_start
data: {"type":"message_start","message":{"id":"msg_A","role":"assistant","content":[]}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Looking "}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"now."}}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"Bash","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}
`

func TestThreadAssistantFromSSE(t *testing.T) {
	id, msg := aiGatewayThreadAssistantFromResponse([]byte(threadTestSSE))
	if id != "msg_A" {
		t.Fatalf("message id = %q", id)
	}
	blocks := aiGatewaySlice(msg["content"])
	if len(blocks) != 2 {
		t.Fatalf("want text + tool_use (thinking dropped), got %d blocks: %v", len(blocks), blocks)
	}
	if got := aiGatewayString(aiGatewayMap(blocks[0])["text"]); got != "Looking now." {
		t.Fatalf("text = %q", got)
	}
	tool := aiGatewayMap(blocks[1])
	if aiGatewayString(tool["id"]) != "toolu_1" || aiGatewayString(aiGatewayMap(tool["input"])["command"]) != "ls" {
		t.Fatalf("tool block = %v", tool)
	}
}

func userText(text string) map[string]interface{} {
	return map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": text}}}
}

// A continue request must be expanded to the full conversation, and chains
// (main vs. subagent) must stay separate because they are keyed by message id.
func TestExpandThreadBodyRebuildsFullConversation(t *testing.T) {
	agent := "w-thread-test"
	first := []interface{}{userText("fix the bug")}
	aiGatewayThreadRemember(agent, first, []byte(threadTestSSE))
	// An unrelated chain on the same agent (a subagent).
	aiGatewayThreadRemember(agent, []interface{}{userText("subagent task")}, []byte(strings.ReplaceAll(threadTestSSE, "msg_A", "msg_SUB")))

	delta := map[string]interface{}{
		"model":  "m",
		"thread": map[string]interface{}{"type": "continue", "previous_message_id": "msg_A"},
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "tool_result", "tool_use_id": "toolu_1", "content": "file.txt"},
		}}},
	}
	full, orphan := aiGatewayExpandThreadBody(agent, "conv", delta)
	if orphan {
		t.Fatal("known predecessor reported as orphan")
	}
	msgs := aiGatewaySlice(full["messages"])
	if len(msgs) != 3 {
		t.Fatalf("want user + assistant + tool_result, got %d", len(msgs))
	}
	if got := aiGatewayExtractQuestion(full); !strings.Contains(got, "fix the bug") {
		t.Fatalf("question lost after expansion: %q", got)
	}
	if aiGatewayString(aiGatewayMap(msgs[1])["role"]) != "assistant" {
		t.Fatalf("second message should be the remembered assistant reply: %v", msgs[1])
	}
	if len(aiGatewaySlice(delta["messages"])) != 1 {
		t.Fatal("wire body was mutated")
	}
	// Numbering covers the whole conversation, not just the delta.
	if got := aiGatewayCurrentBodyMaxHistoryID(aiGatewayAnnotateCurrentBodyHistoryIDs(agent, aiGatewayCloneJSONValue(full))); got != 3 {
		t.Fatalf("max history id = %d, want 3", got)
	}
}

func TestExpandThreadBodyOrphanAndPlainRequests(t *testing.T) {
	plain := map[string]interface{}{"messages": []interface{}{userText("hi")}}
	if got, orphan := aiGatewayExpandThreadBody("w-thread-test-2", "conv", plain); orphan || len(aiGatewaySlice(got["messages"])) != 1 {
		t.Fatalf("plain request changed: orphan=%v body=%v", orphan, got)
	}
	unknown := map[string]interface{}{
		"thread":   map[string]interface{}{"type": "continue", "previous_message_id": "msg_never_seen"},
		"messages": []interface{}{userText("continue")},
	}
	if _, orphan := aiGatewayExpandThreadBody("w-thread-test-2", "conv", unknown); !orphan {
		t.Fatal("continue request with unknown predecessor must be an orphan")
	}
}

func TestThreadStoreIsBounded(t *testing.T) {
	agent := "w-thread-test-3"
	for i := 0; i < aiGatewayThreadKeepPerAgent+5; i++ {
		aiGatewayThreadPut(agent, "msg_"+string(rune('a'+i)), []interface{}{userText("x")})
	}
	if n := len(aiGatewayThreadStore.byAgent[agent]); n != aiGatewayThreadKeepPerAgent {
		t.Fatalf("store holds %d entries, want %d", n, aiGatewayThreadKeepPerAgent)
	}
	if aiGatewayThreadGet(agent, "msg_a") != nil {
		t.Fatal("oldest entry should have been evicted")
	}
}
