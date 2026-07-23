// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Issue #30 write-side guard. boot.sh's read-side fix refuses to resume a
// conversation whose transcript is not under the pane's own claude project
// dir; this guard watches the OTHER end — the moment a conversation_id is
// persisted into an agent's current.json — and logs a WARN when that id's
// transcript provably lives under a DIFFERENT agent's project dir. It warns
// instead of refusing: current.json is also the UI's conversation snapshot,
// so refusing the write would blank the pane's chat display, and with the
// read-side fix in place a foreign id can no longer self-perpetuate anyway.

var (
	resumeGuardMu   sync.Mutex
	resumeGuardSeen = map[string]string{} // agentID → last conversation_id checked
)

// claudeProjectDirSlug mirrors claude CLI's project-dir naming: every byte of
// the absolute workspace path that is not [A-Za-z0-9] becomes '-'.
func claudeProjectDirSlug(workspace string) string {
	b := []byte(workspace)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

func warnOnForeignConversationID(agentID, convID string) {
	convID = strings.TrimSpace(convID)
	// Empty or the 8-hex gateway fallback id — not a claude session, nothing to own.
	if convID == "" || len(convID) == 8 {
		return
	}
	// Only re-check when the pane's conversation id actually changes.
	resumeGuardMu.Lock()
	if resumeGuardSeen[agentID] == convID {
		resumeGuardMu.Unlock()
		return
	}
	resumeGuardSeen[agentID] = convID
	resumeGuardMu.Unlock()

	if loadPaneAgentType(agentID) != "claude" {
		return
	}
	var ws string
	_ = store.QueryRow("SELECT workspace FROM agent_config WHERE pane_id=?", normPaneID(agentID)).Scan(&ws)
	ws = strings.TrimSpace(ws)
	if ws == "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	own := filepath.Join(home, ".claude", "projects", claudeProjectDirSlug(ws), convID+".jsonl")
	if _, err := os.Stat(own); err == nil {
		return // transcript is where it belongs
	}
	// Not in its own dir: a brand-new session's transcript may simply not exist
	// yet — only warn when it demonstrably lives under someone else's dir.
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", convID+".jsonl"))
	if len(matches) > 0 {
		log.Printf("[resume-guard] WARN agent=%s conversation %s transcript lives outside this agent's project dir (expected %s, found %s) — possible cross-agent session, see issue #30",
			shortPaneID(agentID), convID, own, matches[0])
	}
}
