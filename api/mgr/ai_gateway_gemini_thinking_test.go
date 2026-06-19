package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// TestGeminiRequestInjectsIncludeThoughts verifies the Anthropic→ChatCompletions
// request transform opts Gemini into returning thought summaries (extra_body.
// google.thinking_config.include_thoughts) — and only Gemini.
func TestGeminiRequestInjectsIncludeThoughts(t *testing.T) {
	body := []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)

	out, _, err := transformMessagesRequestToChatCompletions(body, "generativelanguage.googleapis.com")
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var dst map[string]interface{}
	if err := json.Unmarshal(out, &dst); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	eb, ok := dst["extra_body"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected extra_body for Gemini, got: %s", out)
	}
	g, _ := eb["google"].(map[string]interface{})
	tc, _ := g["thinking_config"].(map[string]interface{})
	if it, _ := tc["include_thoughts"].(bool); !it {
		t.Fatalf("include_thoughts not true: %s", out)
	}

	// A standard OpenAI host must NOT get the Gemini extension.
	out2, _, _ := transformMessagesRequestToChatCompletions(body[:0:0], "api.openai.com")
	_ = out2
	stdBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	out3, _, _ := transformMessagesRequestToChatCompletions(stdBody, "api.openai.com")
	var dst3 map[string]interface{}
	_ = json.Unmarshal(out3, &dst3)
	if _, has := dst3["extra_body"]; has {
		t.Fatalf("standard OpenAI must not get extra_body: %s", out3)
	}
}

// TestGeminiThinkingDisabledSkipsThoughts verifies an explicit thinking disable
// suppresses the include_thoughts opt-in.
func TestGeminiThinkingDisabledSkipsThoughts(t *testing.T) {
	body := []byte(`{"model":"gemini-2.5-flash","thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hi"}]}`)
	out, _, err := transformMessagesRequestToChatCompletions(body, "generativelanguage.googleapis.com")
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var dst map[string]interface{}
	_ = json.Unmarshal(out, &dst)
	if _, has := dst["extra_body"]; has {
		t.Fatalf("disabled thinking must not request thoughts: %s", out)
	}
}

// TestGeminiSSEThoughtsBecomeAnthropicThinking feeds a Gemini OpenAI-compat SSE
// stream (thought chunks flagged by extra_content.google.thought, reasoning text
// inline in content wrapped in <thought>…</thought>) through the reader and
// asserts the reasoning surfaces as Anthropic thinking blocks, the answer as
// text, and that no <thought> delimiter leaks into either.
func TestGeminiSSEThoughtsBecomeAnthropicThinking(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"g1","choices":[{"index":0,"delta":{"role":"assistant","content":"<thought>Defining the variables. ","extra_content":{"google":{"thought":true}}}}]}`,
		`data: {"id":"g1","choices":[{"index":0,"delta":{"content":"Solving the equation.","extra_content":{"google":{"thought":true}}}}]}`,
		`data: {"id":"g1","choices":[{"index":0,"delta":{"content":"</thought>The ball costs $0.05."}}]}`,
		`data: {"id":"g1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":5,"total_tokens":120}}`,
		`data: [DONE]`,
		``,
	}, "\n\n")

	rc := newChatCompletionsToMessagesReader(io.NopCloser(strings.NewReader(sse)), "")
	outBytes, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := string(outBytes)

	if !strings.Contains(out, "thinking_delta") {
		t.Fatalf("no thinking_delta emitted:\n%s", out)
	}
	if !strings.Contains(out, "Defining the variables") || !strings.Contains(out, "Solving the equation") {
		t.Fatalf("reasoning text missing from thinking:\n%s", out)
	}
	if !strings.Contains(out, "text_delta") || !strings.Contains(out, "The ball costs $0.05") {
		t.Fatalf("answer text missing:\n%s", out)
	}
	if strings.Contains(out, "<thought>") || strings.Contains(out, "</thought>") {
		t.Fatalf("raw <thought> delimiter leaked into output:\n%s", out)
	}
	// The reasoning must NOT also appear in the visible text block.
	if strings.Contains(out, `"text_delta"`) {
		// crude check: the answer delta should not carry "Defining the variables"
		if idx := strings.Index(out, "The ball costs $0.05"); idx >= 0 {
			tail := out[idx:]
			if strings.Contains(tail, "Defining the variables") {
				t.Fatalf("reasoning leaked into answer region:\n%s", out)
			}
		}
	}
}
