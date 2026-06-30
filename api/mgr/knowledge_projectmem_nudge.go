package main

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Project-mem — the shared claude auto-memory pool at
// ~/cicy-ai/memory/project-mem/<slug>/ — is governed DIFFERENTLY from the canon
// store. A write there is INSTANTLY shared with same-project claude agents (B
// recalls it next turn), so it can't sit "pending" like a canon _inbox entry, and
// briefing the 知识专员 on every write would flood it (many claude agents write the
// default pool, every turn). So instead of per-write push we DEBOUNCE: at most one
// "patrol" nudge per pool per window (or once enough writes pile up). Governance
// here is RETROACTIVE curation — prune noise/dupes/errors, and lift cross-project
// gems into canon via `cicy-knowledge add` — not intake gatekeeping.

const (
	projectMemNudgeWindow    = 30 * time.Minute // ≤ one patrol nudge per pool per window
	projectMemNudgeThreshold = 12               // …unless this many writes pile up first
)

type projectMemNudgeState struct {
	count     int
	lastNudge time.Time
}

var (
	projectMemNudgeMu sync.Mutex
	projectMemNudges  = map[string]*projectMemNudgeState{} // pool slug → state
)

// memoryWriteKind classifies a memory-write path: "canon" (an agent's Layer-1
// auto-memory under ~/.claude/projects/*/memory/ → feeds the canon _inbox via the
// per-write brief), "projectmem" (the shared pool under .../memory/project-mem/
// <slug>/ → debounced patrol nudge, NOT pending-gated), or "" (not a memory write).
func memoryWriteKind(p string) string {
	q := filepath.ToSlash(strings.TrimSpace(p))
	if q == "" || filepath.Base(q) == "MEMORY.md" {
		return ""
	}
	if strings.Contains(q, "/memory/project-mem/") {
		return "projectmem"
	}
	if isMemoryFilePath(p) {
		return "canon"
	}
	return ""
}

// projectMemPoolSlug extracts <slug> from a .../memory/project-mem/<slug>/... path.
func projectMemPoolSlug(p string) string {
	q := filepath.ToSlash(p)
	i := strings.Index(q, "/project-mem/")
	if i < 0 {
		return ""
	}
	rest := q[i+len("/project-mem/"):]
	if j := strings.Index(rest, "/"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// noteProjectMemWrite records a write to a project-mem pool and, when the debounce
// window has elapsed (or enough writes accumulated), nudges the 知识专员 to patrol
// that pool. Concurrency-safe; the brief send happens outside the lock.
func noteProjectMemWrite(poolSlug string) {
	if strings.TrimSpace(poolSlug) == "" {
		return
	}
	log.Printf("[project-mem] memory write detected → pool=%s", poolSlug)
	projectMemNudgeMu.Lock()
	st := projectMemNudges[poolSlug]
	if st == nil {
		st = &projectMemNudgeState{}
		projectMemNudges[poolSlug] = st
	}
	st.count++
	// A zero lastNudge → time.Since is huge ≥ window → fires on the pool's first
	// write (a heads-up that the pool is active), then debounces.
	fire := time.Since(st.lastNudge) >= projectMemNudgeWindow || st.count >= projectMemNudgeThreshold
	if !fire {
		projectMemNudgeMu.Unlock()
		return
	}
	n := st.count
	st.count = 0
	st.lastNudge = time.Now()
	projectMemNudgeMu.Unlock()

	spec := knowledgeSpecialistPaneID()
	if spec == "" {
		return
	}
	brief := fmt.Sprintf(
		"📒 [project-mem] %s 池自上次巡检后有 %d 条新/改记忆。请巡检治理:剪噪音 / 合重复 / 纠错;跨项目有复用价值的用 `cicy-knowledge add` 提升进 canon。\n  ls -lt ~/cicy-ai/memory/project-mem/%s/",
		poolSlug, n, poolSlug)
	deliverAgentMessage(spec, brief)
}

// deliverAgentMessage sends text to an agent the way `cicy-agent msg` does — the
// ONLY correct path for a HEADLESS cicy agent (the 知识专员 is one): it has no tmux
// pane, so sendTextToPane (send-keys) silently fails. Route cicy → in-process
// deliverCicyMessage; terminal agents (claude/codex/…) → send-keys as before.
func deliverAgentMessage(paneID, text string) {
	paneID = normPaneID(paneID)
	if paneID == "" || strings.TrimSpace(text) == "" {
		return
	}
	if paneAgentType(paneID) == "cicy" {
		short := shortPaneID(paneID)
		if ws := paneWorkspace(short); ws != "" {
			go deliverCicyMessage(short, ws, text)
		}
		return
	}
	_ = sendTextToPane(paneID, text, true)
}
