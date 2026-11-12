// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func promptLogTestEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	oldWorkers := cicyWorkersDir
	cicyWorkersDir = filepath.Join(root, "workers")
	t.Cleanup(func() { cicyWorkersDir = oldWorkers })
	t.Setenv("HOME", filepath.Join(root, "home"))
	return root
}

func promptLogContents(entries []agentPromptLogEntry) []string {
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Content)
	}
	return out
}

func writeTranscript(t *testing.T, root, cid string, rows ...string) string {
	t.Helper()
	dir := filepath.Join(root, "home", ".claude", "projects", "-work")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, cid+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, row := range rows {
		if _, err := f.WriteString(row + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// A snapshot-fed agent keeps every prompt it was ever sent, including the ones
// a later compaction removed from current.json, and repeats stay separate.
func TestPromptLogKeepsPromptsDroppedFromContext(t *testing.T) {
	promptLogTestEnv(t)
	agentPromptLogIngestSnapshot("w-9001", "conv-a", []aiGatewayUserPrompt{
		{ID: 1, TS: "2026-11-12T08:00:00Z", Content: "first"},
		{ID: 5, TS: "2026-11-12T08:05:00Z", Content: "继续"},
	})
	// After compaction only the newest prompts are still in the snapshot.
	agentPromptLogIngestSnapshot("w-9001", "conv-a", []aiGatewayUserPrompt{
		{ID: 2, TS: "2026-11-12T08:05:00Z", Content: "继续"},
		{ID: 4, TS: "2026-11-12T08:30:00Z", Content: "继续"},
	})
	got := promptLogContents(agentPromptLogCollect("w-9001"))
	if strings.Join(got, "|") != "first|继续|继续" {
		t.Fatalf("log = %q", got)
	}
}

// Claude Code agents are fed from the transcript: typed rows and mid-turn
// messages are kept, tool results and harness rows are not, and a second
// collect only reads what was appended.
func TestPromptLogReadsClaudeTranscriptIncrementally(t *testing.T) {
	root := promptLogTestEnv(t)
	cid := "11111111-2222-3333-4444-555555555555"
	if err := os.MkdirAll(aiGatewayConversationDir("w-9002", cid), 0755); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, root, cid,
		`{"type":"user","promptSource":"typed","timestamp":"2026-11-12T08:00:00.123Z","message":{"role":"user","content":"<pasted_content id=\"1\">\n你是 agent w-1 的分身(fork)——它的延续\n</pasted_content>"}}`,
		`{"type":"user","promptSource":"typed","timestamp":"2026-11-12T08:01:00.500Z","message":{"role":"user","content":"看下这张图\n\n[a.png](/home/x/cicy-ai/assets/a.png)"}}`,
		`{"type":"user","timestamp":"2026-11-12T08:01:30Z","message":{"role":"user","content":[{"type":"tool_result","content":"\"promptSource\" in a tool result"}]}}`,
		`{"type":"attachment","timestamp":"2026-11-12T08:02:00.000Z","attachment":{"type":"queued_command","prompt":"图片要能预览","commandMode":"prompt","origin":{"kind":"human"}}}`,
		`{"type":"user","promptSource":"queued","timestamp":"2026-11-12T08:02:04.000Z","message":{"role":"user","content":"图片要能预览"}}`,
		`{"type":"attachment","timestamp":"2026-11-12T08:02:30.000Z","attachment":{"type":"queued_command","prompt":"task done","origin":{"kind":"task-notification"}}}`,
	)
	// A transcript agent must not also be fed from the snapshot.
	agentPromptLogIngestSnapshot("w-9002", cid, []aiGatewayUserPrompt{{ID: 1, TS: "2026-11-12T09:00:00Z", Content: "snapshot only"}})

	first := agentPromptLogCollect("w-9002")
	if got := strings.Join(promptLogContents(first), "|"); got != "看下这张图\n\n[a.png](/home/x/cicy-ai/assets/a.png)|图片要能预览" {
		t.Fatalf("first collect = %q", got)
	}
	if first[0].TS != "2026-11-12T08:01:00Z" || first[0].ConversationID != cid {
		t.Fatalf("entry = %+v", first[0])
	}

	writeTranscript(t, root, cid,
		`{"type":"user","promptSource":"typed","timestamp":"2026-11-12T08:10:00Z","message":{"role":"user","content":[{"type":"text","text":"发版"}]}}`,
	)
	second := agentPromptLogCollect("w-9002")
	if got := strings.Join(promptLogContents(second), "|"); !strings.HasSuffix(got, "|发版") || len(second) != 3 {
		t.Fatalf("second collect = %q", got)
	}
	if again := agentPromptLogCollect("w-9002"); len(again) != 3 {
		t.Fatalf("re-collect duplicated entries: %d", len(again))
	}
}

func TestPromptLogHandler(t *testing.T) {
	promptLogTestEnv(t)
	agentPromptLogIngestSnapshot("w-9003", "conv-a", []aiGatewayUserPrompt{{ID: 1, TS: "2026-11-12T08:00:00Z", Content: "hello"}})

	rec := httptest.NewRecorder()
	handleAgentPromptLog(rec, httptest.NewRequest(http.MethodGet, "/api/agents/prompt-log?panes=w-9003,w-9004,../etc", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Agents map[string][]agentPromptLogEntry `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Agents) != 2 || len(body.Agents["w-9003"]) != 1 || body.Agents["w-9003"][0].Content != "hello" || body.Agents["w-9004"] == nil {
		t.Fatalf("body = %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handleAgentPromptLog(rec, httptest.NewRequest(http.MethodGet, "/api/agents/prompt-log", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing panes status = %d", rec.Code)
	}
}
