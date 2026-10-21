// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Fork for kiro-cli agents. kiro never goes through the gateway/MITM, so its
// workspace has no .cicy/history/current.json and agent-summary produces
// nothing — the fork used to start with an empty dump (0 B files in the
// preview). kiro writes its own conversation to ~/.kiro/sessions/cli/<id>.jsonl,
// so render that into the same transcript format agent-summary emits and hand
// the fork the same summary/current.md — parity with claude's fork.

const (
	forkToolResultHead      = 400
	forkToolResultErrorHead = 1000
)

// kiroForkSources returns the kiro session files (jsonl transcript + json meta)
// for the workspace and a freshly written transcript at
// <workspace>/.cicy/history/summary/current.md. ok=false when the workspace has
// no kiro session, so the caller falls back to the gateway/agent-summary path.
func kiroForkSources(srcWorkspace string) (sessionJSONL, sessionMeta, summaryPath string, ok bool) {
	ws := strings.TrimSpace(srcWorkspace)
	dir := kiroSessionsDir()
	if ws == "" || dir == "" {
		return "", "", "", false
	}
	id := kiroFindSessionID(dir, ws)
	if id == "" {
		return "", "", "", false
	}
	sessionJSONL = filepath.Join(dir, id+".jsonl")
	sessionMeta = filepath.Join(dir, id+".json")
	records, err := kiroParseJSONL(sessionJSONL)
	if err != nil || len(records) == 0 {
		return "", "", "", false
	}
	summaryDir := filepath.Join(ws, ".cicy", "history", "summary")
	if err := os.MkdirAll(summaryDir, 0o755); err != nil {
		return "", "", "", false
	}
	target := filepath.Join(summaryDir, id+".md")
	if err := os.WriteFile(target, []byte(renderForkTranscript(records)), 0o644); err != nil {
		return "", "", "", false
	}
	// Same stable entry point agent-summary maintains.
	cur := filepath.Join(summaryDir, "current.md")
	_ = os.Remove(cur)
	if err := os.Symlink(target, cur); err != nil {
		summaryPath = target
	} else {
		summaryPath = cur
	}
	return sessionJSONL, sessionMeta, summaryPath, true
}

// renderForkTranscript mirrors agent-summary's output shape:
// Q: / A (thinking): / A (tool): / TOOL RESULT: / A:.
func renderForkTranscript(records []aiGatewayMessageRecord) string {
	var b strings.Builder
	for _, rec := range records {
		if q := strings.TrimSpace(rec.Q); q != "" {
			fmt.Fprintf(&b, "Q: %s\n", q)
		}
		if th := strings.TrimSpace(rec.Thinking); th != "" {
			fmt.Fprintf(&b, "A (thinking): %s\n", th)
		}
		for _, tc := range rec.ToolCalls {
			fmt.Fprintf(&b, "A (tool): %s: %s\n", tc.Name, forkHead(tc.Input, 200))
			if out := strings.TrimSpace(tc.Output); out != "" {
				limit := forkToolResultHead
				if strings.Contains(strings.ToLower(out[:min(len(out), 200)]), "error") {
					limit = forkToolResultErrorHead
				}
				fmt.Fprintf(&b, "TOOL RESULT: %s\n", forkHead(out, limit))
			}
		}
		if a := strings.TrimSpace(rec.A); a != "" {
			fmt.Fprintf(&b, "A: %s\n", a)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func forkHead(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	// Don't cut a UTF-8 rune in half.
	cut := n
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return fmt.Sprintf("%s …[+%d chars]", s[:cut], len(s)-cut)
}
