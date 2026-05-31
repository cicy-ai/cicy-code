package main

import (
	"encoding/json"
	"net/url"
	"testing"
)

// Real shapes captured from a non-gateway (MITM) Claude Code run: the audit must
// record /v1/messages inference turns but skip telemetry / token-counting that
// share the same host (api.anthropic.com).
func TestMitmIsAIInferenceRequest(t *testing.T) {
	mustURL := func(s string) *url.URL { u, _ := url.Parse(s); return u }
	jb := func(v map[string]interface{}) []byte { b, _ := json.Marshal(v); return b }

	messagesBody := jb(map[string]interface{}{
		"model":      "claude-opus-4-8",
		"max_tokens": 64000,
		"stream":     true,
		"messages":   []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	})
	chatBody := jb(map[string]interface{}{
		"model":    "deepseek-v4-pro",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	})
	responsesBody := jb(map[string]interface{}{
		"model": "gpt-5.5",
		"input": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	})
	countTokensBody := jb(map[string]interface{}{
		"model":    "claude-opus-4-8",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	})
	eventLogBody := []byte(`{"events":[{"name":"tengu_api_success"}]}`)

	cases := []struct {
		name string
		url  string
		body []byte
		want bool
	}{
		{"anthropic-messages", "https://api.anthropic.com/v1/messages", messagesBody, true},
		{"openai-chat", "https://api.openai.com/v1/chat/completions", chatBody, true},
		{"openai-responses", "https://api.openai.com/v1/responses", responsesBody, true},
		{"event-logging-telemetry", "https://api.anthropic.com/api/event_logging/v2/batch", eventLogBody, false},
		{"count-tokens", "https://api.anthropic.com/v1/messages/count_tokens", countTokensBody, false},
		{"model-list", "https://api.openai.com/v1/models", nil, false},
		{"empty-body", "https://api.anthropic.com/v1/messages", nil, false},
	}
	for _, c := range cases {
		if got := mitmIsAIInferenceRequest(mustURL(c.url), c.body); got != c.want {
			t.Errorf("%s: mitmIsAIInferenceRequest = %v, want %v", c.name, got, c.want)
		}
	}
}
