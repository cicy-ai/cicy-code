package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestGeminiLiveThinkingPassthrough is a real end-to-end check of the exact
// production path: our request transform → live Gemini OpenAI-compat (stream) →
// the gateway's chatCompletionsToMessagesReader → Anthropic Messages SSE.
//
// Gated on GEMINI_TEST_KEY so it never runs in normal CI. Run with:
//
//	GEMINI_TEST_KEY=... go test ./mgr -run TestGeminiLiveThinkingPassthrough -v
func TestGeminiLiveThinkingPassthrough(t *testing.T) {
	key := os.Getenv("GEMINI_TEST_KEY")
	if key == "" {
		t.Skip("set GEMINI_TEST_KEY to run the live Gemini test")
	}

	// Build the upstream body via the REAL transform from an Anthropic request.
	anthropic := []byte(`{"model":"gemini-2.5-flash","stream":true,"max_tokens":512,` +
		`"messages":[{"role":"user","content":"A bat and ball cost $1.10 total, the bat is $1.00 more than the ball. What is the ball's price? Think briefly, then give the number."}]}`)
	upstreamBody, _, err := transformMessagesRequestToChatCompletions(anthropic, "generativelanguage.googleapis.com")
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if !strings.Contains(string(upstreamBody), "include_thoughts") {
		t.Fatalf("transform did not inject include_thoughts: %s", upstreamBody)
	}

	req, _ := http.NewRequest("POST",
		"https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
		bytes.NewReader(upstreamBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upstream request: %v", err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("upstream HTTP %d: %s", resp.StatusCode, string(b))
	}

	// Pipe the LIVE response through the production reader.
	rc := newChatCompletionsToMessagesReader(resp.Body, "")
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	s := string(out)

	if !strings.Contains(s, "thinking_delta") {
		t.Fatalf("no Anthropic thinking_delta in output:\n%s", truncForDebug([]byte(s), 2000))
	}
	if !strings.Contains(s, "text_delta") {
		t.Fatalf("no Anthropic text_delta in output:\n%s", truncForDebug([]byte(s), 2000))
	}
	if strings.Contains(s, "<thought>") || strings.Contains(s, "</thought>") {
		t.Fatalf("raw <thought> delimiter leaked:\n%s", truncForDebug([]byte(s), 2000))
	}
	t.Logf("OK — live Gemini thinking passed through as Anthropic blocks (%d bytes SSE)", len(s))
}
