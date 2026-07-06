// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// mitm-smoke is a minimal standalone driver that exercises the cicy-mitm
// pipeline end-to-end without requiring the full cicy-code binary.
//
// What it does:
//   1. Loads config from --config (default: ./mitm-smoke.json).
//   2. Spins up an mitm.Server with a file-writing AuditHook that
//      simulates current.json / reply.json output to --history-root.
//   3. Listens for SIGINT / SIGTERM.
//
// What it does NOT do:
//   - Pull in the real ai_gateway_audit pipeline (that lives in
//     package main and brings the whole cicy-code dependency tree).
//   - Implement PreventiveCheck (Phase 3 breaker stays as noop here).
//
// Use this to validate Phase 1 + 1.5 + provider parsing in isolation.
// For the real audit submission path, run the full cicy-code binary.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"ttyd-go/mgr/mitm"
)

func main() {
	configPath := flag.String("config", "", "path to mitm config (defaults to ~/cicy-ai/mitm/config.json)")
	historyRoot := flag.String("history-root", "/tmp/mitm-smoke", "where to write current.json / reply.json")
	flag.Parse()

	cfg, err := mitm.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if !cfg.Enabled {
		log.Fatalf("mitm not enabled in config — set enabled: true")
	}
	if err := os.MkdirAll(*historyRoot, 0o700); err != nil {
		log.Fatalf("mkdir history: %v", err)
	}

	hook := &fileAuditHook{Root: *historyRoot}
	srv, err := mitm.NewServer(cfg, hook, nil) // no breaker in smoke driver
	if err != nil {
		log.Fatalf("new server: %v", err)
	}
	if srv == nil {
		log.Fatal("server disabled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("start: %v", err)
	}

	log.Printf("[mitm-smoke] up. history=%s socks5=%s node=%s", *historyRoot, cfg.SOCKS5Listen, cfg.Node.ID)
	log.Printf("[mitm-smoke] try: curl --proxy socks5h://%s --cacert <ca.crt> https://<host>/...", cfg.SOCKS5Listen)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Printf("[mitm-smoke] shutting down...")
	srv.Stop()
}

// --- fileAuditHook ---

// fileAuditHook mirrors a subset of newAIGatewayAuditSession: it persists
// the request body + headers to current.json (per-turn) and accumulates
// the response body to reply.json on close. NOT a substitute for the
// real audit adapter — exists only to validate Phase 1 wiring.
type fileAuditHook struct {
	Root string
}

func (h *fileAuditHook) StartTurn(provider, agentID string, target *url.URL, method string, headers http.Header, body []byte) mitm.AuditTurn {
	turnID := fmt.Sprintf("turn-%d", time.Now().UnixNano())
	turn := &fileAuditTurn{
		root:    h.Root,
		turnID:  turnID,
		agentID: agentID,
	}
	current := map[string]interface{}{
		"turn_id":         turnID,
		"agent_id":        agentID,
		"provider":        provider,
		"url":             target.String(),
		"method":          method,
		"headers":         headers,
		"started_at":      time.Now().UTC().Format(time.RFC3339Nano),
		"source":          "mitm",
	}
	if len(body) > 0 {
		var parsed interface{}
		if err := json.Unmarshal(body, &parsed); err == nil {
			current["body"] = parsed
		} else {
			current["body_raw"] = string(body)
		}
	}
	writeJSON(filepath.Join(h.Root, agentID, turnID, "current.json"), current)
	return turn
}

type fileAuditTurn struct {
	root      string
	turnID    string
	agentID   string
	mu        sync.Mutex
	buf       []byte
	statusCode int
	headers   http.Header
}

func (t *fileAuditTurn) WrapResponseBody(inner io.ReadCloser, statusCode int, headers http.Header, contentLength int64) io.ReadCloser {
	t.statusCode = statusCode
	t.headers = headers
	return &teeCloser{src: inner, owner: t}
}

func (t *fileAuditTurn) Fail(err error) {
	reply := map[string]interface{}{
		"turn_id":    t.turnID,
		"status":     "error",
		"error":      err.Error(),
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	writeJSON(filepath.Join(t.root, t.agentID, t.turnID, "reply.json"), reply)
}

func (t *fileAuditTurn) finish() {
	t.mu.Lock()
	body := t.buf
	t.mu.Unlock()
	reply := map[string]interface{}{
		"turn_id":     t.turnID,
		"status":      "completed",
		"status_code": t.statusCode,
		"headers":     t.headers,
		"updated_at":  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if len(body) > 0 {
		var parsed interface{}
		if err := json.Unmarshal(body, &parsed); err == nil {
			reply["body"] = parsed
		} else {
			reply["body_raw"] = string(body)
		}
	}
	writeJSON(filepath.Join(t.root, t.agentID, t.turnID, "reply.json"), reply)
}

type teeCloser struct {
	src   io.ReadCloser
	owner *fileAuditTurn
}

func (t *teeCloser) Read(p []byte) (int, error) {
	n, err := t.src.Read(p)
	if n > 0 {
		t.owner.mu.Lock()
		t.owner.buf = append(t.owner.buf, p[:n]...)
		t.owner.mu.Unlock()
	}
	return n, err
}
func (t *teeCloser) Close() error {
	t.owner.finish()
	return t.src.Close()
}

func writeJSON(path string, value interface{}) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("mkdir %s: %v", path, err)
		return
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		log.Printf("marshal %s: %v", path, err)
		return
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		log.Printf("write %s: %v", path, err)
	}
}
