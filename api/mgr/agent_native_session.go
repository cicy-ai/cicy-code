// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Native-session history (v3).
//
// Agents that run under their own official login — kiro-cli, codex, opencode,
// and official-login claude — do NOT route their model traffic through the
// CiCy gateway, so there is no current.json / reply.json snapshot for them.
// But every one of these CLIs already writes its OWN conversation log to disk
// in real time (kiro/claude/codex = append-only JSONL, opencode = sqlite).
//
// This file reads those native logs directly and normalises them into the same
// []aiGatewayMessageRecord the inspector history view already renders — no
// gateway, no MITM, no snapshot files involved. Each CLI gets a provider; the
// inspector consults them for a matching agent_type before falling back to the
// (lossy, truncation-prone) tmux-capture path.
//
// alpha.1 ships the kiro provider. claude/codex/opencode providers are left as
// the obvious next implementations behind the same interface.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// tsToRFC3339 converts a Unix-seconds timestamp to the RFC3339 string the
// inspector history view expects. Zero/negative yields "".
func tsToRFC3339(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// nativeSessionAgentType reads agent_config.agent_type for a pane id. Returns ""
// when unknown (then no provider matches and the caller falls back as before).
func nativeSessionAgentType(agentID string) string {
	if store == nil {
		return ""
	}
	var at string
	_ = store.QueryRow("SELECT COALESCE(agent_type,'') FROM agent_config WHERE pane_id=?", normPaneID(agentID)).Scan(&at)
	return at
}

// nativeSessionProvider reads a CLI's own on-disk conversation log for one
// agent and returns it as normalised message records (oldest turn first).
type nativeSessionProvider interface {
	// handles reports whether this provider owns the given normalised agent_type.
	handles(agentType string) bool
	// loadMessages locates the session belonging to workspace and parses it.
	// Returns (nil, nil) when no session matches — that is not an error, just
	// "nothing to show from here".
	loadMessages(agentID, workspace string) ([]aiGatewayMessageRecord, error)
}

// nativeSessionProviders is the ordered registry. The first provider whose
// handles() matches an agent's type wins. Append claude/codex/opencode here as
// they are implemented.
var nativeSessionProviders = []nativeSessionProvider{
	kiroSessionProvider{},
}

// nativeSessionMessages returns the normalised history for agentID from whatever
// native-session provider owns its type, or (nil,false) when none applies / the
// session is empty. The bool reports whether a provider claimed the type at all
// (so the caller can decide whether native-session is even the right source).
func nativeSessionMessages(agentID, agentType, workspace string) ([]aiGatewayMessageRecord, bool) {
	for _, p := range nativeSessionProviders {
		if !p.handles(agentType) {
			continue
		}
		msgs, err := p.loadMessages(agentID, workspace)
		if err != nil || len(msgs) == 0 {
			return nil, true
		}
		return msgs, true
	}
	return nil, false
}

// ─────────────────────────── kiro provider ───────────────────────────

// kiro-cli (2.31.x, V3/KAS) writes one session per conversation under
//
//	$KIRO_HOME/sessions/cli/<uuid>.json    ← metadata { cwd, title, updated_at }
//	$KIRO_HOME/sessions/cli/<uuid>.jsonl   ← append-only turn stream
//	$KIRO_HOME/sessions/cli/<uuid>.lock    ← { pid } while the session is live
//
// KIRO_HOME defaults to ~/.kiro. Each .jsonl line is {version,kind,data} where
// kind ∈ {Prompt, AssistantMessage, ToolResults}. One Prompt + the following
// Assistant/Tool lines up to the next Prompt form one Q/A record.
type kiroSessionProvider struct{}

func (kiroSessionProvider) handles(agentType string) bool {
	return normalizeAgentType(agentType) == "kiro-cli"
}

func kiroSessionsDir() string {
	if h := strings.TrimSpace(os.Getenv("KIRO_HOME")); h != "" {
		return filepath.Join(h, "sessions", "cli")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kiro", "sessions", "cli")
}

// kiroSessionMeta is the subset of <uuid>.json we care about.
type kiroSessionMeta struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	UpdatedAt string `json:"updated_at"`
}

// kiroFindSessionID picks the session whose cwd == workspace. When several
// match (history across restarts), the most recently updated wins.
func kiroFindSessionID(dir, workspace string) string {
	if dir == "" || strings.TrimSpace(workspace) == "" {
		return ""
	}
	wantAbs, _ := filepath.Abs(workspace)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	type cand struct {
		id      string
		updated string
	}
	var cands []cand
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var meta kiroSessionMeta
		if json.Unmarshal(raw, &meta) != nil {
			continue
		}
		metaAbs, _ := filepath.Abs(meta.Cwd)
		if meta.Cwd != workspace && metaAbs != wantAbs {
			continue
		}
		id := meta.SessionID
		if id == "" {
			id = strings.TrimSuffix(e.Name(), ".json")
		}
		cands = append(cands, cand{id: id, updated: meta.UpdatedAt})
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].updated > cands[j].updated })
	return cands[0].id
}

func (p kiroSessionProvider) loadMessages(agentID, workspace string) ([]aiGatewayMessageRecord, error) {
	dir := kiroSessionsDir()
	id := kiroFindSessionID(dir, workspace)
	if id == "" {
		return nil, nil
	}
	return kiroParseJSONL(filepath.Join(dir, id+".jsonl"))
}

// kiro line shapes (only the fields we read).
type kiroLine struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}
type kiroContentItem struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}
type kiroMsgData struct {
	Content []kiroContentItem `json:"content"`
	Meta    struct {
		Timestamp int64 `json:"timestamp"`
	} `json:"meta"`
}
type kiroThinking struct {
	Text    string `json:"text"`
	ModelID string `json:"modelId"`
}
type kiroToolUse struct {
	Name      string        `json:"name"`
	ToolUseID string        `json:"toolUseId"`
	Input     kiroToolInput `json:"input"`
}

// kiroToolInput holds a toolUse "input" as a JSON string. Older kiro-cli wrote
// it as an encoded string; 2.31.x writes a JSON object. Declaring it a plain
// string made the object form fail to unmarshal, silently dropping every tool
// call (the 会话 tab then showed orphan "tool_result" cards with no names).
type kiroToolInput string

func (k *kiroToolInput) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" || trimmed == "null" {
		*k = ""
		return nil
	}
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*k = kiroToolInput(s)
		return nil
	}
	*k = kiroToolInput(trimmed) // object/array: keep raw JSON text
	return nil
}
type kiroToolResultData struct {
	ToolUseID string            `json:"toolUseId"`
	Status    string            `json:"status"`
	Content   []kiroContentItem `json:"content"`
}

// kiroParseJSONL turns a kiro session .jsonl into normalised records. A trailing
// half-written line (kiro appends mid-turn, so the last line may be incomplete)
// is skipped rather than failing the whole parse.
func kiroParseJSONL(path string) ([]aiGatewayMessageRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var records []aiGatewayMessageRecord
	var cur *aiGatewayMessageRecord
	// toolIdx maps a toolUseId → index into cur.ToolCalls so ToolResults can
	// backfill Output onto the matching call.
	toolIdx := map[string]int{}

	flush := func() {
		if cur != nil {
			records = append(records, *cur)
			cur = nil
			toolIdx = map[string]int{}
		}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // allow long tool-result lines
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var kl kiroLine
		if json.Unmarshal([]byte(line), &kl) != nil {
			continue // half-written / malformed line — skip
		}
		switch kl.Kind {
		case "Prompt":
			flush()
			var d kiroMsgData
			_ = json.Unmarshal(kl.Data, &d)
			rec := aiGatewayMessageRecord{
				Q:     kiroJoinText(d.Content),
				QTime: tsToRFC3339(d.Meta.Timestamp),
			}
			cur = &rec
		case "AssistantMessage":
			if cur == nil {
				cur = &aiGatewayMessageRecord{}
			}
			var d kiroMsgData
			_ = json.Unmarshal(kl.Data, &d)
			for _, c := range d.Content {
				switch c.Kind {
				case "text":
					var s string
					if json.Unmarshal(c.Data, &s) == nil {
						cur.A += s
					}
				case "thinking":
					var th kiroThinking
					if json.Unmarshal(c.Data, &th) == nil {
						cur.Thinking += th.Text
						if cur.Model == "" && th.ModelID != "" {
							cur.Model = th.ModelID
						}
					}
				case "toolUse":
					var tu kiroToolUse
					if json.Unmarshal(c.Data, &tu) == nil {
						cur.ToolCalls = append(cur.ToolCalls, aiGatewayMessageToolCall{
							Name:  tu.Name,
							Input: string(tu.Input),
							Index: len(cur.ToolCalls),
						})
						if tu.ToolUseID != "" {
							toolIdx[tu.ToolUseID] = len(cur.ToolCalls) - 1
						}
					}
				}
			}
			if d.Meta.Timestamp > 0 {
				cur.ATime = tsToRFC3339(d.Meta.Timestamp)
			}
		case "ToolResults":
			if cur == nil {
				continue
			}
			var d kiroMsgData
			if json.Unmarshal(kl.Data, &d) != nil {
				continue
			}
			for _, c := range d.Content {
				if c.Kind != "toolResult" {
					continue
				}
				var tr kiroToolResultData
				if json.Unmarshal(c.Data, &tr) != nil {
					continue
				}
				if idx, ok := toolIdx[tr.ToolUseID]; ok && idx < len(cur.ToolCalls) {
					cur.ToolCalls[idx].Output = kiroJoinText(tr.Content)
				}
			}
		}
	}
	flush()
	return records, nil
}

// kiroJoinText concatenates all string-valued "text" content items.
func kiroJoinText(items []kiroContentItem) string {
	var b strings.Builder
	for _, c := range items {
		if c.Kind != "text" {
			continue
		}
		var s string
		if json.Unmarshal(c.Data, &s) == nil {
			b.WriteString(s)
		}
	}
	return b.String()
}
