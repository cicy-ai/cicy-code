package mitm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubBreaker returns a fixed decision for every call.
type stubBreaker struct {
	mu     sync.Mutex
	calls  []BreakerRequest
	decide func(BreakerRequest) BreakerDecision
}

func (s *stubBreaker) Check(req BreakerRequest) BreakerDecision {
	s.mu.Lock()
	s.calls = append(s.calls, req)
	s.mu.Unlock()
	if s.decide != nil {
		return s.decide(req)
	}
	return BreakerDecision{Action: BreakerActionPass}
}

// startTestServer is the minimum scaffold for Phase 3 breaker tests:
// fake upstream + MITM server with stub audit + stub breaker.
func startTestServer(t *testing.T, breaker BreakerHook, upstreamHandler http.HandlerFunc) (clientPost func(t *testing.T, body string) (*http.Response, error), hook *fakeAuditHook, stop func()) {
	t.Helper()
	upstreamHost, upstreamPool, stopUpstream := startFakeUpstream(t, upstreamHandler)
	_, upstreamPortStr, _ := net.SplitHostPort(upstreamHost)

	dir := t.TempDir()
	cfg := &Config{
		Enabled:      true,
		SOCKS5Listen: "127.0.0.1:0",
		CA: CAConfig{
			CertPath: filepath.Join(dir, "ca.crt"), KeyPath: filepath.Join(dir, "ca.key"),
			LeafCacheSize: 4, LeafValidYears: 1,
		},
		Hosts: HostsConfig{Whitelist: []string{"127.0.0.1"}},
		Node:  NodeConfig{ID: "node-breaker", FinalHop: true, MaxHops: 5},
		Upstream: UpstreamConfig{
			Mode: "direct", DialTimeout: Duration(5 * time.Second), TLSTimeout: Duration(5 * time.Second),
		},
		Identity: IdentityConfig{Rules: []IdentityRule{{Kind: "socks5_username"}, {Kind: "fallback", Value: "mitm:{host}"}}},
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	hook = &fakeAuditHook{}
	srv, err := NewServer(cfg, hook, breaker)
	if err != nil {
		t.Fatal(err)
	}
	srv.dialer.cas = upstreamPool
	srv.dialer.chain = true

	ln, err := net.Listen("tcp", cfg.SOCKS5Listen)
	if err != nil {
		t.Fatal(err)
	}
	srv.listener = ln
	srv.wg.Add(1)
	go srv.acceptLoop(context.Background(), ln, srv.handleConn)

	mitmAddr := ln.Addr().String()
	clientPool := x509.NewCertPool()
	clientPool.AppendCertsFromPEM(srv.RootCertPEM())

	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := net.Dial("tcp", mitmAddr)
		if err != nil {
			return nil, err
		}
		target := "127.0.0.1:" + upstreamPortStr
		if err := socks5ClientHandshakeWithUser(conn, "w-test", target); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext:     dial,
			TLSClientConfig: &tls.Config{RootCAs: clientPool, ServerName: "127.0.0.1"},
		},
		Timeout: 10 * time.Second,
	}
	clientPost = func(t *testing.T, body string) (*http.Response, error) {
		return client.Post("https://127.0.0.1/v1/messages", "application/json", strings.NewReader(body))
	}
	stop = func() {
		srv.Stop()
		stopUpstream()
	}
	return
}

func TestBreaker_PassActionForwards(t *testing.T) {
	upstreamCalled := false
	post, hook, stop := startTestServer(t, &stubBreaker{decide: func(BreakerRequest) BreakerDecision {
		return BreakerDecision{Action: BreakerActionPass}
	}}, func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		fmt.Fprint(w, `{"ok":true}`)
	})
	defer stop()

	resp, err := post(t, `{"hello":"world"}`)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !upstreamCalled {
		t.Fatal("upstream not called on pass")
	}
	if len(hook.turns) != 1 {
		t.Fatalf("audit turns=%d", len(hook.turns))
	}
}

func TestBreaker_BlockReturns403(t *testing.T) {
	upstreamCalled := false

	post, hook, stop := startTestServer(t, &stubBreaker{decide: func(BreakerRequest) BreakerDecision {
		return BreakerDecision{
			Action:  BreakerActionBlock,
			Reason:  "test: secret detected",
			RuleID:  "test.secret",
			EventID: "evt-test-1",
			Rules:   []string{"test.secret"},
			Message: "命中审计拦截规则【test.secret】,已被 cicy-code 审计策略实时阻断。(事件 ID: evt-test-1)",
		}
	}}, func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
	})
	defer stop()

	resp, err := post(t, `{"prompt":"sk-abc"}`)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	// UNIFIED block contract: 403 (non-retryable), same as the gateway path.
	if resp.StatusCode != 403 {
		t.Fatalf("status=%d, want 403", resp.StatusCode)
	}
	if upstreamCalled {
		t.Fatal("upstream should NOT be called on block")
	}
	if resp.Header.Get("X-Cicy-Audit-Blocked") != "evt-test-1" {
		t.Errorf("X-Cicy-Audit-Blocked=%q", resp.Header.Get("X-Cicy-Audit-Blocked"))
	}
	if resp.Header.Get("X-Cicy-Audit-Rules") != "test.secret" {
		t.Errorf("X-Cicy-Audit-Rules=%q", resp.Header.Get("X-Cicy-Audit-Rules"))
	}
	if resp.Header.Get(HeaderBlockedBy) != "node-breaker" {
		t.Errorf("X-Cicy-Mitm-Blocked-By=%q", resp.Header.Get(HeaderBlockedBy))
	}
	body, _ := io.ReadAll(resp.Body)
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %s", body)
	}
	if parsed["error"] != "blocked_by_audit" {
		t.Errorf("body.error=%v, want blocked_by_audit", parsed["error"])
	}
	if !strings.Contains(string(body), "test.secret") {
		t.Errorf("body missing rule id: %s", body)
	}
	if !strings.Contains(string(body), "审计策略实时阻断") {
		t.Errorf("body missing unified message: %s", body)
	}
	// Turn was still recorded with Fail.
	if len(hook.turns) != 1 || hook.turns[0].Err == nil {
		t.Errorf("expected audit turn with Err set, got: %+v", hook.turns)
	}
}

