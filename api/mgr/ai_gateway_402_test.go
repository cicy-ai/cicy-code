package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func resp402(target string) *http.Response {
	req := httptest.NewRequest("POST", target, nil)
	return &http.Response{
		StatusCode: http.StatusPaymentRequired,
		Request:    req,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"detail":"insufficient_balance"}`)),
	}
}

// A cicy-gateway 402 is rewritten into a user-facing top-up message, shaped for
// the calling protocol (Anthropic /messages vs OpenAI).
func TestRewriteCicyGateway402(t *testing.T) {
	r := resp402("https://gateway.cicy-ai.com/v1/messages")
	rewriteCicyGateway402(r)
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), "充值") || !strings.Contains(string(b), "payment_required") {
		t.Fatalf("anthropic-shape rewrite missing: %s", b)
	}

	r = resp402("https://gateway.cicy-ai.com/v1/chat/completions")
	rewriteCicyGateway402(r)
	b, _ = io.ReadAll(r.Body)
	if !strings.Contains(string(b), "insufficient_balance") || !strings.Contains(string(b), "https://cicy-ai.com/dash") {
		t.Fatalf("openai-shape rewrite missing: %s", b)
	}

	// non-cicy upstream 402 must pass through untouched
	r = resp402("https://api.openai.com/v1/chat/completions")
	rewriteCicyGateway402(r)
	b, _ = io.ReadAll(r.Body)
	if string(b) != `{"detail":"insufficient_balance"}` {
		t.Fatalf("foreign 402 was rewritten: %s", b)
	}
}
