// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKiroForkSourcesWritesTranscriptFromSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIRO_HOME", home)
	dir := filepath.Join(home, "sessions", "cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	id := "11111111-2222-3333-4444-555555555555"
	meta := `{"session_id":"` + id + `","cwd":"` + ws + `","updated_at":"2026-10-21T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	jsonl := strings.Join([]string{
		`{"version":"v1","kind":"Prompt","data":{"content":[{"kind":"text","data":"列一下目录"}],"meta":{"timestamp":1792379437}}}`,
		`{"version":"v1","kind":"AssistantMessage","data":{"content":[{"kind":"text","data":"好的"},{"kind":"toolUse","data":{"toolUseId":"t1","name":"shell","input":{"command":"ls"}}}],"meta":{"timestamp":1792379438}}}`,
		`{"version":"v1","kind":"ToolResults","data":{"content":[{"kind":"toolResult","data":{"toolUseId":"t1","content":[{"kind":"text","data":"a.txt"}],"status":"success"}}]}}`,
		`{"version":"v1","kind":"AssistantMessage","data":{"content":[{"kind":"text","data":"只有 a.txt"}],"meta":{"timestamp":1792379439}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}

	gotJSONL, gotMeta, summary, ok := kiroForkSources(ws)
	if !ok {
		t.Fatal("expected kiro fork sources to resolve")
	}
	if gotJSONL != filepath.Join(dir, id+".jsonl") || gotMeta != filepath.Join(dir, id+".json") {
		t.Fatalf("unexpected session files: %s %s", gotJSONL, gotMeta)
	}
	if summary != filepath.Join(ws, ".cicy", "history", "summary", "current.md") {
		t.Fatalf("summary path = %s", summary)
	}
	body, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"Q: 列一下目录", "A: "} {
		if !strings.Contains(text, want) {
			t.Fatalf("transcript missing %q:\n%s", want, text)
		}
	}

	if _, _, _, ok := kiroForkSources(t.TempDir()); ok {
		t.Fatal("a workspace with no kiro session must fall back (ok=false)")
	}
}
