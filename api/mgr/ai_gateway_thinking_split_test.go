package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// Standard OpenAI-style upstreams (non-deepseek host AND non-deepseek model) must
// get a CLEAN translation: no DeepSeek-private `thinking` field forced on, and no
// `reasoning_content` sprinkled on assistant history. This is the regression that
// silently killed thinking for every openai-style provider (opencodeZen, etc.).
func TestMessagesTransformStandardOpenAIHasNoDeepSeekFields(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"thinking": {"type":"enabled"},
		"messages": [
			{"role":"user","content":"hi"},
			{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"hello"}]}
		]
	}`)
	out, _, err := transformMessagesRequestToChatCompletions(body, "api.opencodezen.example")
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := m["thinking"]; present {
		t.Fatalf("standard openai must NOT carry a thinking field: %s", out)
	}
	for _, raw := range m["messages"].([]interface{}) {
		msg := raw.(map[string]interface{})
		if _, present := msg["reasoning_content"]; present {
			t.Fatalf("standard openai must NOT carry reasoning_content: %#v", msg)
		}
	}
}

// DeepSeek (detected by model name even on a non-deepseek/proxy host) must carry
// the resolved V4 thinking switch through, and echo a non-empty reasoning_content
// on assistant history (real thinking when present) to satisfy its validator.
func TestMessagesTransformDeepSeekCarriesThinkingAndReasoning(t *testing.T) {
	body := []byte(`{
		"model": "deepseek-v4",
		"thinking": {"type":"enabled","budget_tokens":2048},
		"messages": [
			{"role":"user","content":"hi"},
			{"role":"assistant","content":[{"type":"thinking","thinking":"step one"},{"type":"text","text":"hello"}]}
		]
	}`)
	// Host is a proxy (no "deepseek") — detection must still fire on the model id.
	out, _, err := transformMessagesRequestToChatCompletions(body, "gateway.cicy-ai.com")
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	th, ok := m["thinking"].(map[string]interface{})
	if !ok || th["type"] != "enabled" {
		t.Fatalf("deepseek must carry thinking:{type:enabled}: %s", out)
	}
	if _, present := th["budget_tokens"]; present {
		t.Fatalf("only type should be carried, not budget_tokens: %#v", th)
	}
	var asst map[string]interface{}
	for _, raw := range m["messages"].([]interface{}) {
		msg := raw.(map[string]interface{})
		if msg["role"] == "assistant" {
			asst = msg
		}
	}
	if asst["reasoning_content"] != "step one" {
		t.Fatalf("deepseek assistant must echo real thinking as reasoning_content: %#v", asst)
	}
}

// passthrough/disabled policy carried via src.thinking → deepseek emits disabled.
func TestMessagesTransformDeepSeekThinkingDisabledByDefault(t *testing.T) {
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	out, _, _ := transformMessagesRequestToChatCompletions(body, "")
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	th, ok := m["thinking"].(map[string]interface{})
	if !ok || th["type"] != "disabled" {
		t.Fatalf("deepseek with no thinking field should default to disabled: %s", out)
	}
}

// The response reader must surface upstream reasoning_content as an Anthropic
// `thinking` content block — universally (DeepSeek AND standard openai upstreams).
func TestChatCompletionsToMessagesEmitsThinkingBlock(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"x","choices":[{"delta":{"reasoning_content":"let me think"}}]}`,
		`data: {"id":"x","choices":[{"delta":{"reasoning_content":" more"}}]}`,
		`data: {"id":"x","choices":[{"delta":{"content":"the answer"}}]}`,
		`data: {"id":"x","choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	rc := newChatCompletionsToMessagesReader(io.NopCloser(strings.NewReader(sse)), "deepseek-v4")
	outBytes, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := string(outBytes)
	if !strings.Contains(out, `"type":"thinking"`) {
		t.Fatalf("expected a thinking content_block_start: %s", out)
	}
	if !strings.Contains(out, `"type":"thinking_delta"`) || !strings.Contains(out, "let me think") {
		t.Fatalf("expected thinking_delta with reasoning text: %s", out)
	}
	// Thinking block must be closed before the text block opens (index order 0→1).
	thinkingStop := strings.Index(out, `"content_block_stop"`)
	textStart := strings.Index(out, `"type":"text"`)
	if thinkingStop < 0 || textStart < 0 || thinkingStop > textStart {
		t.Fatalf("thinking block must close before text opens: %s", out)
	}
}
