// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeKiroSession lays down a <id>.json meta + <id>.jsonl stream under dir.
func writeKiroSession(t *testing.T, dir, id, cwd string, jsonlLines []string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"session_id":"` + id + `","cwd":"` + cwd + `","updated_at":"2026-10-19T03:05:09Z"}`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range jsonlLines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKiroParseJSONLSingleTurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	lines := `{"version":"v1","kind":"Prompt","data":{"content":[{"kind":"text","data":"read w-1002 chat"}],"meta":{"timestamp":1792378374}}}
{"version":"v1","kind":"AssistantMessage","data":{"content":[{"kind":"thinking","data":{"text":"let me look","modelId":"auto"}},{"kind":"text","data":"Here is the summary."},{"kind":"toolUse","data":{"name":"read","toolUseId":"t1","input":"{\"path\":\"/x\"}"}}],"meta":{"timestamp":1792378407}}}
{"version":"v1","kind":"ToolResults","data":{"content":[{"kind":"toolResult","data":{"toolUseId":"t1","status":"success","content":[{"kind":"text","data":"file body"}]}}]}}`
	if err := os.WriteFile(path, []byte(lines+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recs, err := kiroParseJSONL(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	r := recs[0]
	if r.Q != "read w-1002 chat" {
		t.Errorf("Q = %q", r.Q)
	}
	if r.A != "Here is the summary." {
		t.Errorf("A = %q", r.A)
	}
	if r.Thinking != "let me look" {
		t.Errorf("Thinking = %q", r.Thinking)
	}
	if r.Model != "auto" {
		t.Errorf("Model = %q", r.Model)
	}
	if len(r.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(r.ToolCalls))
	}
	if r.ToolCalls[0].Name != "read" {
		t.Errorf("tool name = %q", r.ToolCalls[0].Name)
	}
	if r.ToolCalls[0].Output != "file body" {
		t.Errorf("tool output = %q (ToolResults not backfilled)", r.ToolCalls[0].Output)
	}
	if r.QTime == "" || r.ATime == "" {
		t.Errorf("timestamps not set: q=%q a=%q", r.QTime, r.ATime)
	}
}

func TestKiroParseJSONLMultiTurnAndHalfLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	// Two complete turns, then a trailing half-written line that must be skipped.
	lines := `{"version":"v1","kind":"Prompt","data":{"content":[{"kind":"text","data":"q1"}],"meta":{"timestamp":1}}}
{"version":"v1","kind":"AssistantMessage","data":{"content":[{"kind":"text","data":"a1"}],"meta":{"timestamp":2}}}
{"version":"v1","kind":"Prompt","data":{"content":[{"kind":"text","data":"q2"}],"meta":{"timestamp":3}}}
{"version":"v1","kind":"AssistantMessage","data":{"content":[{"kind":"text","data":"a2"}],"meta":{"timestamp":4}}}
{"version":"v1","kind":"Prompt","data":{"content":[{"kind":"text","dat`
	if err := os.WriteFile(path, []byte(lines+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recs, err := kiroParseJSONL(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 records (half line skipped), got %d", len(recs))
	}
	if recs[0].Q != "q1" || recs[0].A != "a1" || recs[1].Q != "q2" || recs[1].A != "a2" {
		t.Errorf("records mismatch: %+v", recs)
	}
}

func TestKiroFindSessionIDByCwd(t *testing.T) {
	dir := t.TempDir()
	ws := t.TempDir()
	// Two sessions, only one matches the workspace; the matching one is newest.
	writeKiroSession(t, dir, "other", "/some/other/path", []string{
		`{"kind":"Prompt","data":{"content":[{"kind":"text","data":"x"}],"meta":{"timestamp":1}}}`,
	})
	writeKiroSession(t, dir, "mine", ws, []string{
		`{"kind":"Prompt","data":{"content":[{"kind":"text","data":"hi"}],"meta":{"timestamp":10}}}`,
		`{"kind":"AssistantMessage","data":{"content":[{"kind":"text","data":"hello"}],"meta":{"timestamp":11}}}`,
	})
	id := kiroFindSessionID(dir, ws)
	if id != "mine" {
		t.Fatalf("want session id 'mine', got %q", id)
	}
}

func TestKiroProviderLoadMessages(t *testing.T) {
	kiroHome := t.TempDir()
	ws := t.TempDir()
	t.Setenv("KIRO_HOME", kiroHome)
	sessDir := filepath.Join(kiroHome, "sessions", "cli")
	writeKiroSession(t, sessDir, "sid", ws, []string{
		`{"kind":"Prompt","data":{"content":[{"kind":"text","data":"ping"}],"meta":{"timestamp":10}}}`,
		`{"kind":"AssistantMessage","data":{"content":[{"kind":"text","data":"pong"}],"meta":{"timestamp":11}}}`,
	})
	p := kiroSessionProvider{}
	if !p.handles("kiro-cli") || !p.handles("kiro") {
		t.Fatal("provider should handle kiro types")
	}
	if p.handles("claude") {
		t.Fatal("provider must not handle claude")
	}
	recs, err := p.loadMessages("w-1:main.0", ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recs) != 1 || recs[0].Q != "ping" || recs[0].A != "pong" {
		t.Fatalf("unexpected records: %+v", recs)
	}
	// Non-matching workspace yields nothing (not an error).
	empty, err := p.loadMessages("w-1:main.0", "/no/such/ws")
	if err != nil || len(empty) != 0 {
		t.Fatalf("want empty for unmatched cwd, got %d err=%v", len(empty), err)
	}
}
