package main

import (
	"io"
	"strings"
	"testing"
)

// A realistic DeepSeek/OpenAI-compatible Chat Completions SSE stream: a couple
// of content deltas then a terminal chunk carrying finish_reason + usage, then
// [DONE]. This is exactly what the gateway proxies back from the upstream when
// codex's /responses request has been translated to /chat/completions.
const sampleChatSSE = `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"CXOK_"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"8899"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}

data: [DONE]

`

func rewrapToResponses(t *testing.T, chatSSE string) string {
	t.Helper()
	rc := newChatCompletionsToResponsesReader(io.NopCloser(strings.NewReader(chatSSE)), "deepseek-v4-pro")
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(out)
}

// The core codex fidelity check: a normal upstream answer MUST surface as a
// Responses event stream that carries the assistant text both incrementally
// (output_text.delta) and in the terminal response.completed output. If this
// regresses, codex shows an empty reply (the answer_len=0 symptom).
func TestResponsesRewrap_TextSurfaces(t *testing.T) {
	got := rewrapToResponses(t, sampleChatSSE)

	mustContain := []string{
		"event: response.created",
		"event: response.output_item.added",
		"event: response.output_text.delta",
		`"delta":"CXOK_"`,
		`"delta":"8899"`,
		"event: response.output_text.done",
		`"text":"CXOK_8899"`,
		"event: response.completed",
		"data: [DONE]",
	}
	for _, frag := range mustContain {
		if !strings.Contains(got, frag) {
			t.Errorf("rewrapped stream missing %q\n--- full output ---\n%s", frag, got)
		}
	}
}

// The terminal response.completed.output must contain the full assistant text —
// this is what codex reads to render the final message and what the audit reader
// counts for answer_len. An empty output here is the exact answer_len=0 failure.
func TestResponsesRewrap_CompletedOutputNotEmpty(t *testing.T) {
	got := rewrapToResponses(t, sampleChatSSE)
	if !strings.Contains(got, `"output_text"`) || !strings.Contains(got, "CXOK_8899") {
		t.Fatalf("response.completed carried no assistant text (answer_len=0 repro)\n%s", got)
	}
}

// A non-streaming upstream body (a single JSON object, NOT SSE `data:` lines)
// produces an EMPTY Responses stream — the reader only understands SSE. This
// documents one concrete way codex would get an empty reply, so the box-side
// curl diagnostic can confirm/deny whether the upstream actually streamed.
func TestResponsesRewrap_NonStreamingBodyYieldsEmpty(t *testing.T) {
	nonStream := `{"id":"chatcmpl-x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"CXOK_8899"},"finish_reason":"stop"}]}`
	got := rewrapToResponses(t, nonStream)
	if strings.Contains(got, "CXOK_8899") {
		t.Fatalf("expected non-streaming body to yield empty (reader is SSE-only), got:\n%s", got)
	}
}

// Native-Responses upstreams (api.openai.com, the cicy cloud gateway) must NOT
// be adapted — codex's /responses is passed through so the upstream's native
// Responses stream reaches codex intact. Only chat-only upstreams get adapted.
func TestShouldAdaptForCodexResponses(t *testing.T) {
	cases := []struct {
		host, suffix string
		want         bool
	}{
		{"api.openai.com", "/responses", false},
		{"gateway.cicy-ai.com", "/responses", false},      // the box's actual upstream — the bug
		{"gateway.cicy-ai.com:443", "/responses", false},  // host:port form
		{"foo.cicy-ai.com", "/responses", false},          // any cicy cloud subdomain
		{"api.deepseek.com", "/responses", true},          // chat-only → adapt
		{"my-one-api.example.com", "/responses", true},    // chat-only relay → adapt
		{"api.deepseek.com", "/chat/completions", false},  // not a responses call
	}
	for _, c := range cases {
		if got := shouldAdaptForCodexResponses(c.host, c.suffix); got != c.want {
			t.Errorf("shouldAdaptForCodexResponses(%q,%q)=%v want %v", c.host, c.suffix, got, c.want)
		}
	}
}

// reasoning_content with NO content delta surfaces reasoning but leaves the
// assistant message text empty — another route to answer_len=0 (a reasoner that
// ignored thinking:disabled and returned only reasoning).
func TestResponsesRewrap_ReasoningOnlyHasNoText(t *testing.T) {
	reasoningOnly := `data: {"id":"c1","choices":[{"index":0,"delta":{"reasoning_content":"hmm"},"finish_reason":null}]}

data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
	got := rewrapToResponses(t, reasoningOnly)
	if strings.Contains(got, "event: response.output_text.delta") {
		t.Fatalf("reasoning-only stream should not emit output_text.delta, got:\n%s", got)
	}
	if !strings.Contains(got, "event: response.completed") {
		t.Fatalf("must still terminate with response.completed, got:\n%s", got)
	}
}
