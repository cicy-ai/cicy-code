package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestIsCicyGatewayHost(t *testing.T) {
	for host, want := range map[string]bool{
		"gateway.cicy-ai.com":     true,
		"cicy-ai.com":             true,
		"cicy-ai.com:443":         true,
		"acme.team.cicy-ai.com":   true,
		"api.deepseek.com":        false,
		"evilcicy-ai.com":         false, // suffix must be a real subdomain boundary
		"cicy-ai.com.evil.com":    false,
		"generativelanguage.googleapis.com": false,
	} {
		if got := isCicyGatewayHost(host); got != want {
			t.Fatalf("isCicyGatewayHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// The gateway wallet probe parses /api/balance and caches per (base|key), so a
// picker listing several providers that share one gateway+key hits the cloud once.
func TestCicyGatewayBalanceCachesPerKey(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/balance" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"balance_usd":12.5,"currency":"USD"}`))
	}))
	defer srv.Close()

	pcA := &providerConfig{URL: srv.URL, APIKey: "sk-cicy-shared-key"}
	pcB := &providerConfig{URL: srv.URL + "/v1", APIKey: "sk-cicy-shared-key"} // same host+key, path differs

	r1 := cicyGatewayBalance(pcA)
	if !r1.OK || r1.Total != "12.50" || r1.Currency != "USD" {
		t.Fatalf("first probe = %+v", r1)
	}
	r2 := cicyGatewayBalance(pcB)
	if !r2.OK || r2.Total != "12.50" {
		t.Fatalf("second probe = %+v", r2)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("upstream hit %d times, want 1 (shared-cache dedupe)", n)
	}
}
