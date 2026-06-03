package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeFunctionParametersFillsMissingFields(t *testing.T) {
	out := sanitizeFunctionParameters(map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}},
	})
	if _, ok := out["required"].([]interface{}); !ok {
		t.Fatalf("required must be []interface{}, got %T", out["required"])
	}
}

func TestSanitizeFunctionParametersNullParameters(t *testing.T) {
	out := sanitizeFunctionParameters(nil)
	if out["type"] != "object" {
		t.Fatalf("type=object expected, got %v", out["type"])
	}
	if _, ok := out["properties"].(map[string]interface{}); !ok {
		t.Fatalf("properties must be object, got %T", out["properties"])
	}
	if _, ok := out["required"].([]interface{}); !ok {
		t.Fatalf("required must be array, got %T", out["required"])
	}
}

func TestSanitizeFunctionParametersNullChildren(t *testing.T) {
	out := sanitizeFunctionParameters(map[string]interface{}{
		"properties": nil,
		"required":   nil,
	})
	if _, ok := out["properties"].(map[string]interface{}); !ok {
		t.Fatalf("properties null should be replaced, got %T", out["properties"])
	}
	if _, ok := out["required"].([]interface{}); !ok {
		t.Fatalf("required null should be replaced, got %T", out["required"])
	}
}

func TestResponsesToChatCompletionsToolsHaveRequired(t *testing.T) {
	body := []byte(`{
		"model": "deepseek-v4-pro",
		"input": [{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"tools": [{"type":"function","name":"read_file","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}]
	}`)
	out, _, err := transformResponsesRequestToChatCompletions(body)
	if err != nil {
		t.Fatalf("translation error: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tools, _ := got["tools"].([]interface{})
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	tool := tools[0].(map[string]interface{})
	fn := tool["function"].(map[string]interface{})
	params := fn["parameters"].(map[string]interface{})
	if _, ok := params["required"].([]interface{}); !ok {
		t.Fatalf("parameters.required missing — DeepSeek will reject")
	}
	// Sanity: still serializes valid JSON with no null required.
	if strings.Contains(string(out), `"required":null`) {
		t.Fatalf("required must not serialize as null")
	}
}

func TestMessagesToChatCompletionsToolsHaveRequired(t *testing.T) {
	body := []byte(`{
		"model": "deepseek-v4-pro",
		"messages": [{"role":"user","content":"hi"}],
		"tools": [{"name":"read_file","description":"r","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}]
	}`)
	out, _, err := transformMessagesRequestToChatCompletions(body)
	if err != nil {
		t.Fatalf("translation error: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tools, _ := got["tools"].([]interface{})
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	fn := tools[0].(map[string]interface{})["function"].(map[string]interface{})
	params := fn["parameters"].(map[string]interface{})
	if _, ok := params["required"].([]interface{}); !ok {
		t.Fatalf("parameters.required missing — DeepSeek will reject")
	}
}

// Streaming Responses→ChatCompletions requests must ask the upstream for usage
// (stream_options.include_usage), else OpenAI-compatible upstreams bill usage
// server-side but never stream it — the gateway/audit then sees 0 tokens/cache.
func TestTransformResponsesRequestAddsStreamOptionsUsage(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-pro","stream":true,"input":[{"type":"message","role":"user","content":"hi"}]}`)
	out, _, err := transformResponsesRequestToChatCompletions(body)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	so, ok := m["stream_options"].(map[string]interface{})
	if !ok || so["include_usage"] != true {
		t.Fatalf("stream_options.include_usage not set: %s", out)
	}
	// Non-stream requests must NOT carry stream_options.
	body2 := []byte(`{"model":"deepseek-v4-pro","input":[{"type":"message","role":"user","content":"hi"}]}`)
	out2, _, _ := transformResponsesRequestToChatCompletions(body2)
	var m2 map[string]interface{}
	_ = json.Unmarshal(out2, &m2)
	if _, present := m2["stream_options"]; present {
		t.Fatalf("non-stream request should not get stream_options: %s", out2)
	}
}

// Upstream chat-completions usage (incl. DeepSeek cache fields) must map into the
// Responses usage shape so the audit records tokens + cache_read.
func TestChatUsageToResponsesUsageMapsCache(t *testing.T) {
	// prompt_tokens_details.cached_tokens form
	u := chatUsageToResponsesUsage(map[string]interface{}{
		"prompt_tokens": float64(468), "completion_tokens": float64(91), "total_tokens": float64(559),
		"prompt_tokens_details": map[string]interface{}{"cached_tokens": float64(13184)},
	})
	if u["input_tokens"] != float64(468) || u["output_tokens"] != float64(91) {
		t.Fatalf("token map wrong: %#v", u)
	}
	d, _ := u["input_tokens_details"].(map[string]interface{})
	if d == nil || d["cached_tokens"] != float64(13184) {
		t.Fatalf("cached_tokens not mapped: %#v", u)
	}
	// deepseek flat prompt_cache_hit_tokens form
	u2 := chatUsageToResponsesUsage(map[string]interface{}{
		"prompt_tokens": float64(500), "prompt_cache_hit_tokens": float64(400),
	})
	d2, _ := u2["input_tokens_details"].(map[string]interface{})
	if d2 == nil || d2["cached_tokens"] != float64(400) {
		t.Fatalf("flat prompt_cache_hit_tokens not mapped: %#v", u2)
	}
	if chatUsageToResponsesUsage(map[string]interface{}{}) != nil {
		t.Fatalf("empty usage should map to nil")
	}
}

// claude→DeepSeek: streaming Messages→ChatCompletions must also request usage,
// and the upstream usage must map into Anthropic shape (input_tokens uncached-only
// + cache_read_input_tokens).
func TestTransformMessagesRequestAddsStreamOptionsUsage(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-pro","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	out, _, err := transformMessagesRequestToChatCompletions(body)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	so, ok := m["stream_options"].(map[string]interface{})
	if !ok || so["include_usage"] != true {
		t.Fatalf("stream_options.include_usage not set: %s", out)
	}
}

func TestChatUsageToAnthropicUsageSubtractsCache(t *testing.T) {
	u := chatUsageToAnthropicUsage(map[string]interface{}{
		"prompt_tokens": float64(13669), "completion_tokens": float64(366),
		"prompt_tokens_details": map[string]interface{}{"cached_tokens": float64(13440)},
	})
	if u["input_tokens"] != float64(13669-13440) {
		t.Fatalf("input_tokens should be uncached-only (229): %#v", u)
	}
	if u["cache_read_input_tokens"] != float64(13440) {
		t.Fatalf("cache_read_input_tokens not mapped: %#v", u)
	}
	if u["output_tokens"] != float64(366) {
		t.Fatalf("output_tokens wrong: %#v", u)
	}
}
