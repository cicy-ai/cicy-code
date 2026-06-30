package main

import (
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// memoryWriteHook is an INDEPENDENT gateway reply hook (peer of the cross-agent
// reply-callback and the IM push hook — assembled in newReplyHooksForPane). It
// watches a turn's tool calls for writes into an agent's Layer 1 auto-memory and,
// when it sees one, seeds a PENDING entry in the Layer 2 team knowledge store and
// briefs the 知识专员.
//
// It is deliberately decoupled from audit: its own enable flag
// (knowledge_hook_enabled, default ON) gates it — NEVER audit_enabled — and it
// touches none of audit's policy / scanner / forward machinery. The only thing it
// shares with audit is the gateway hook interface it plugs into.

const knowledgeSpecialistRoleTemplate = "knowledge-specialist"

// knowledgeMemoryDedupWindow collapses repeated writes to the same
// (source_pane, file_path) into a single dispatch; within the window the entry's
// content is refreshed to the latest write instead of spawning duplicates.
const knowledgeMemoryDedupWindow = 90 * time.Second

type knowledgeMemoryDispatch struct {
	knowledgeID string
	at          time.Time
}

var (
	knowledgeMemoryMu     sync.Mutex
	knowledgeMemoryRecent = map[string]knowledgeMemoryDispatch{} // "pane|path" → last dispatch
)

// knowledgeHookEnabled reports whether the memory-write hook runs. Defaults to
// true when unset — and is read from its OWN settings key, completely
// independent of audit_enabled.
func knowledgeHookEnabled() bool {
	v, ok := globalSettingsBlob()["knowledge_hook_enabled"]
	if !ok {
		return true
	}
	b, _ := v.(bool)
	return b
}

// knowledgeSpecialistDefaultPane is the fallback governing pane — the master
// (w-1001), which is always on duty, so memory-hook briefs always have a home.
const knowledgeSpecialistDefaultPane = "w-1001:main.0"

// knowledgeSpecialistConfigKey pins the governing pane in global.json (a FILE, not
// the DB) — set via `cicy-knowledge specialist <pane>` / POST /api/knowledge/specialist.
const knowledgeSpecialistConfigKey = "knowledge_specialist_pane"

// knowledgeSpecialistPaneID resolves which pane governs the knowledge store. It is
// CONFIG-driven — read from global.json, NOT a DB role query (which used to pick
// "most recently updated agent carrying the role", an implicit/flaky heuristic).
// The operator pins it explicitly; unset → defaults to the master pane (w-1001).
func knowledgeSpecialistPaneID() string {
	if v, ok := readGlobalJSONConfig()[knowledgeSpecialistConfigKey].(string); ok {
		if p := strings.TrimSpace(v); p != "" {
			return normPaneID(p)
		}
	}
	return knowledgeSpecialistDefaultPane
}

// setKnowledgeSpecialistPane pins (or, with "", clears → back to default) the
// governing pane in global.json.
func setKnowledgeSpecialistPane(pane string) error {
	cfg := readGlobalJSONConfig()
	if p := normPaneID(strings.TrimSpace(pane)); p != "" {
		cfg[knowledgeSpecialistConfigKey] = p
	} else {
		delete(cfg, knowledgeSpecialistConfigKey)
	}
	return writeGlobalJSONConfig(cfg)
}

type memoryWriteHook struct {
	sourcePane string
}

func (h *memoryWriteHook) handleEvents(_ []aiGatewayReplyEvent) {}
func (h *memoryWriteHook) onItems(_ []map[string]interface{})    {}

func (h *memoryWriteHook) finalize(reply aiGatewayReplySnapshot) {
	if h == nil || len(reply.ToolCalls) == 0 {
		return
	}
	// Collect memory-write candidates from THIS request's tool calls (reply.ToolCalls
	// is per-request, not accumulated) — pure parsing, no DB/IO yet.
	type cand struct{ path, content, kind string }
	var cands []cand
	for _, tc := range reply.ToolCalls {
		switch strings.TrimSpace(tc.ToolName) {
		case "Write", "Edit", "NotebookEdit":
		default:
			continue
		}
		path, content := parseMemoryToolCall(tc.Arguments)
		if k := memoryWriteKind(path); k != "" {
			cands = append(cands, cand{path: path, content: content, kind: k})
		}
	}
	if len(cands) == 0 {
		return
	}
	// Only consult the (independent) enable flag once a real memory write exists.
	if !knowledgeHookEnabled() {
		return
	}
	for _, c := range cands {
		if c.kind == "projectmem" {
			// Shared claude pool: instantly live, NOT a pending-gated canon entry.
			// Don't insert/brief per write (would flood) — debounced patrol nudge.
			noteProjectMemWrite(projectMemPoolSlug(c.path))
			continue
		}
		h.dispatch(c.path, knowledgeTitleFromMemory(c.path, c.content), c.content)
	}
}

// dispatch writes (or, on a repeat write, overwrites) a pending proposal file in
// the knowledge store's _inbox for a memory write, and — only on the first write
// in the dedup window — briefs the 知识专员. The proposal file uses a STABLE slug
// derived from (source_pane, memory file), so repeated writes overwrite the same
// _inbox/<slug>.md (latest content wins) instead of spawning duplicates; once the
// specialist has moved it out of _inbox, insertKnowledge won't re-create it.
// The brief is an independent send (NOT audit's forward chain).
func (h *memoryWriteHook) dispatch(path, title, body string) {
	pane := normPaneID(h.sourcePane)
	slug := knowledgeMemorySlug(pane, path)

	// File write always happens (latest content wins); skip is handled inside
	// insertKnowledge when the entry has already been governed out of _inbox.
	id, err := insertKnowledge(knowledgeRow{
		ID: slug, Title: title, Body: body,
		SourcePane: pane, SourceKind: "memory-hook", OriginRef: path,
	})
	if err != nil {
		log.Printf("[knowledge-hook] inbox write failed slug=%s: %v", slug, err)
		return
	}

	// Brief the specialist at most once per (pane,file) per window.
	key := pane + "|" + path
	now := time.Now()
	knowledgeMemoryMu.Lock()
	prev, seen := knowledgeMemoryRecent[key]
	fresh := !seen || now.Sub(prev.at) > knowledgeMemoryDedupWindow
	knowledgeMemoryRecent[key] = knowledgeMemoryDispatch{knowledgeID: id, at: now}
	knowledgeMemoryMu.Unlock()
	if !fresh {
		return
	}
	log.Printf("[knowledge-hook] seeded pending knowledge id=%s pane=%s path=%s", id, shortPaneID(pane), path)

	spec := knowledgeSpecialistPaneID()
	if spec == "" {
		log.Printf("[knowledge-hook] no 知识专员 provisioned (role_template=%s); pending %s awaits review", knowledgeSpecialistRoleTemplate, id)
		return
	}
	brief := fmt.Sprintf("📚 [knowledge] %s 写入 memory %s → 已投待评审 _inbox/%s.md。请核实后处置:cicy-knowledge get %s → promote / reject / supersede。",
		shortPaneID(pane), path, id, id)
	// Deliver the cicy-agent-msg way (the 知识专员 is a headless cicy agent with no
	// tmux pane; send-keys would silently fail).
	deliverAgentMessage(spec, brief)
}

// knowledgeMemorySlug is a deterministic slug for a memory file so repeated
// writes map to the same _inbox proposal: mem-<pane>-<memory file stem>.
func knowledgeMemorySlug(pane, path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return knowledgeSlugify("mem-" + shortPaneID(pane) + "-" + stem)
}

// parseMemoryToolCall extracts the target file path and the written content from
// a Write/Edit/NotebookEdit tool call's raw JSON arguments. file_path falls back
// to notebook_path/path; content falls back to new_string (Edit) / new_source
// (NotebookEdit).
func parseMemoryToolCall(arguments string) (path, content string) {
	if strings.TrimSpace(arguments) == "" {
		return "", ""
	}
	var m map[string]interface{}
	if json.Unmarshal([]byte(arguments), &m) != nil {
		return "", ""
	}
	path = strings.TrimSpace(firstStringField(m, "file_path", "notebook_path", "path"))
	content = firstStringField(m, "content", "new_string", "new_source")
	return path, content
}

func firstStringField(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// isMemoryFilePath reports whether a write targets an agent's Layer 1
// auto-memory: a file under ~/.claude/projects/*/memory/. The MEMORY.md index
// itself is excluded — it's a table of contents, not a knowledge fact.
func isMemoryFilePath(p string) bool {
	p = filepath.ToSlash(strings.TrimSpace(p))
	if p == "" {
		return false
	}
	if filepath.Base(p) == "MEMORY.md" {
		return false
	}
	return strings.Contains(p, "/.claude/projects/") && strings.Contains(p, "/memory/")
}

// knowledgeTitleFromMemory derives a human title for a memory write: the
// frontmatter `name:` if present, else the first heading / non-empty body line,
// else the file's basename. Clipped to a sane length.
func knowledgeTitleFromMemory(path, content string) string {
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if i == 0 && line == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			if line == "---" {
				inFrontmatter = false
				continue
			}
			if strings.HasPrefix(line, "name:") {
				if v := strings.TrimSpace(strings.TrimPrefix(line, "name:")); v != "" {
					return clipTitle(v)
				}
			}
			continue
		}
		if t := strings.TrimSpace(strings.TrimLeft(line, "#")); t != "" {
			return clipTitle(t)
		}
	}
	return clipTitle(filepath.Base(path))
}

func clipTitle(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		return strings.TrimSpace(s[:120])
	}
	return s
}
