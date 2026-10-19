// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Native-session → 会话 tab (v3).
//
// The chat view (会话 tab) reads three endpoints — history-ids, current-history
// and current-reply — that were all built on current.json. Official-login agents
// (kiro-cli, …) never write current.json, so the tab only showed the greeting.
//
// This file renders a native session as the SAME wire shape those endpoints
// already serve: an ordered list of Anthropic-style messages
// ({role, content:[{type:text|thinking|tool_use|tool_result,…}]}) with 1-based
// positional ids, and the session id as the conversation id. The frontend needs
// no change. Native logs are append-only and written in real time, so every line
// is already "committed": the live tail is just "is the last message a user
// message the agent hasn't answered yet".

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// nativeSessionView is one agent's native session flattened to chat items.
type nativeSessionView struct {
	SessionID string
	Model     string
	Items     []map[string]interface{} // ids 1..len(Items), oldest first
	ModTime   time.Time
}

// nativeSessionItemsProvider is implemented by providers that can serve the
// chat view directly (ordered message items, not just Q/A records).
type nativeSessionItemsProvider interface {
	nativeSessionProvider
	// sessionFile returns the session id and the on-disk log path for workspace,
	// or ("","") when no session matches.
	sessionFile(workspace string) (sessionID, path string)
	// parseItems flattens the log at path into ordered chat items + model.
	parseItems(path string) (items []map[string]interface{}, model string, err error)
}

// nativeViewCache memoises the parse per log file keyed on (mtime,size): the
// chat view polls current-reply every 500 ms and session logs reach megabytes.
var nativeViewCache = struct {
	mu    sync.Mutex
	items map[string]nativeViewCacheEntry
}{items: map[string]nativeViewCacheEntry{}}

type nativeViewCacheEntry struct {
	modTime time.Time
	size    int64
	view    nativeSessionView
}

// loadNativeSessionView returns the chat-view rendering of paneID's native
// session, or ok=false when the agent has no native-session provider or no
// session yet (callers then fall back to the current.json path unchanged).
func loadNativeSessionView(paneID string) (nativeSessionView, bool) {
	atype := nativeSessionAgentType(paneID)
	if atype == "" {
		return nativeSessionView{}, false
	}
	for _, p := range nativeSessionProviders {
		ip, ok := p.(nativeSessionItemsProvider)
		if !ok || !p.handles(atype) {
			continue
		}
		sid, path := ip.sessionFile(paneWorkspace(normPaneID(paneID)))
		if sid == "" || path == "" {
			return nativeSessionView{}, false
		}
		st, err := os.Stat(path)
		if err != nil {
			return nativeSessionView{}, false
		}
		nativeViewCache.mu.Lock()
		if e, hit := nativeViewCache.items[path]; hit && e.size == st.Size() && e.modTime.Equal(st.ModTime()) {
			nativeViewCache.mu.Unlock()
			return e.view, len(e.view.Items) > 0
		}
		nativeViewCache.mu.Unlock()
		items, model, err := ip.parseItems(path)
		if err != nil {
			return nativeSessionView{}, false
		}
		view := nativeSessionView{SessionID: sid, Model: model, Items: items, ModTime: st.ModTime()}
		nativeViewCache.mu.Lock()
		nativeViewCache.items[path] = nativeViewCacheEntry{modTime: st.ModTime(), size: st.Size(), view: view}
		nativeViewCache.mu.Unlock()
		return view, len(items) > 0
	}
	return nativeSessionView{}, false
}

// nativeItemIsUserPrompt reports whether item is a real human question (role
// user with text), as opposed to a tool_result carrier.
func nativeItemIsUserPrompt(item map[string]interface{}) bool {
	if aiGatewayString(item["role"]) != "user" {
		return false
	}
	return nativeItemText(item) != ""
}

func nativeItemText(item map[string]interface{}) string {
	var b strings.Builder
	for _, part := range aiGatewaySlice(item["content"]) {
		m := aiGatewayMap(part)
		if aiGatewayString(m["type"]) == "text" {
			b.WriteString(aiGatewayString(m["text"]))
		}
	}
	return strings.TrimSpace(b.String())
}

// prompts builds the prompts-only list for history-ids.
func (v nativeSessionView) prompts() []aiGatewayUserPrompt {
	out := []aiGatewayUserPrompt{}
	for i, item := range v.Items {
		if nativeItemIsUserPrompt(item) {
			out = append(out, aiGatewayUserPrompt{ID: i + 1, TS: aiGatewayString(item["ts"]), Content: nativeItemText(item)})
		}
	}
	return out
}

// window returns items with ids in (upper-limit, upper], tagged for the view.
func (v nativeSessionView) window(limit int, before int64) agentHistoryPage {
	maxID := int64(len(v.Items))
	upper := maxID
	if before > 0 && before-1 < upper {
		upper = before - 1
	}
	if upper <= 0 {
		return agentHistoryPage{ConversationID: v.SessionID}
	}
	lower := upper - int64(limit) + 1
	if lower < 1 {
		lower = 1
	}
	out := make([]M, 0, upper-lower+1)
	for id := lower; id <= upper; id++ {
		next := aiGatewayCloneAnyMap(v.Items[id-1])
		next["id"] = id
		next["history_id"] = id
		next["conversation_id"] = v.SessionID
		if v.Model != "" && next["model"] == nil {
			next["model"] = v.Model
		}
		out = append(out, next)
	}
	hasMore := lower > 1
	nextBefore := int64(0)
	if hasMore {
		nextBefore = lower
	}
	return agentHistoryPage{Items: out, ConversationID: v.SessionID, NextBefore: nextBefore, HasMore: hasMore}
}

// inFlight: the agent still owes a reply. Native logs are written one completed
// block at a time, so the log is SILENT both while the model generates and while
// a tool runs. Busy therefore means the last message is either
//   - a user message (question or tool_result) not yet answered, or
//   - an assistant message ending in tool_use (the tool is running; its result
//     line only lands when it finishes — observed: a 4-minute tool run with zero
//     log writes in between).
// A stale log (agent killed mid-turn) stops reading as busy after
// nativeInFlightMaxAge so the composer never locks forever.
const nativeInFlightMaxAge = 10 * time.Minute

func (v nativeSessionView) inFlight() bool {
	if len(v.Items) == 0 || time.Since(v.ModTime) > nativeInFlightMaxAge {
		return false
	}
	last := v.Items[len(v.Items)-1]
	switch aiGatewayString(last["role"]) {
	case "user":
		return true
	case "assistant":
		blocks := aiGatewaySlice(last["content"])
		if len(blocks) == 0 {
			return false
		}
		return aiGatewayString(aiGatewayMap(blocks[len(blocks)-1])["type"]) == "tool_use"
	}
	return false
}

// replyPayload is the current-reply response for a native session. Every line
// is already committed, so the answer slot is always maxID+1 with no content;
// while in flight it reads "working" so the view shows its thinking placeholder.
func (v nativeSessionView) replyPayload(paneID string) M {
	maxID := int64(len(v.Items))
	status, complete := "idle", true
	if v.inFlight() {
		status, complete = "working", false
	}
	question := ""
	for i := len(v.Items) - 1; i >= 0; i-- {
		if nativeItemIsUserPrompt(v.Items[i]) {
			question = nativeItemText(v.Items[i])
			break
		}
	}
	return M{
		"pane_id":               paneID,
		"conversation_id":       v.SessionID,
		"reply_conversation_id": v.SessionID,
		"history_id":            maxID + 1,
		"turn_id":               "",
		"status":                status,
		"complete":              complete,
		"question":              question,
		"answer":                "",
		"thinking":              "",
		"items":                 []interface{}{},
		"model":                 v.Model,
		"source":                "native-session",
	}
}

// ─────────────────────────── kiro ───────────────────────────

func (kiroSessionProvider) sessionFile(workspace string) (string, string) {
	dir := kiroSessionsDir()
	id := kiroFindSessionID(dir, workspace)
	if id == "" {
		return "", ""
	}
	return id, filepath.Join(dir, id+".jsonl")
}

// parseItems maps kiro lines to Anthropic-shaped messages:
//
//	Prompt           → {role:user, content:[text]}
//	AssistantMessage → {role:assistant, content:[thinking?, text?, tool_use…]}
//	ToolResults      → {role:user, content:[tool_result…]}
func (kiroSessionProvider) parseItems(path string) ([]map[string]interface{}, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	var items []map[string]interface{}
	model := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var kl kiroLine
		if json.Unmarshal([]byte(line), &kl) != nil {
			continue // trailing half-written line
		}
		var d kiroMsgData
		if json.Unmarshal(kl.Data, &d) != nil {
			continue
		}
		ts := tsToRFC3339(d.Meta.Timestamp)
		switch kl.Kind {
		case "Prompt":
			text := kiroJoinText(d.Content)
			if text == "" {
				continue
			}
			items = append(items, map[string]interface{}{
				"role":    "user",
				"content": []interface{}{map[string]interface{}{"type": "text", "text": text}},
				"ts":      ts,
			})
		case "AssistantMessage":
			var blocks []interface{}
			for _, c := range d.Content {
				switch c.Kind {
				case "thinking":
					var th kiroThinking
					if json.Unmarshal(c.Data, &th) == nil {
						if model == "" && th.ModelID != "" {
							model = th.ModelID
						}
						if strings.TrimSpace(th.Text) != "" {
							blocks = append(blocks, map[string]interface{}{"type": "thinking", "thinking": th.Text})
						}
					}
				case "text":
					var s string
					if json.Unmarshal(c.Data, &s) == nil && s != "" {
						blocks = append(blocks, map[string]interface{}{"type": "text", "text": s})
					}
				case "toolUse":
					var tu kiroToolUse
					if json.Unmarshal(c.Data, &tu) == nil {
						var input interface{} = tu.Input
						var parsed map[string]interface{}
						if json.Unmarshal([]byte(tu.Input), &parsed) == nil {
							input = parsed
						}
						blocks = append(blocks, map[string]interface{}{"type": "tool_use", "id": tu.ToolUseID, "name": tu.Name, "input": input})
					}
				}
			}
			if len(blocks) == 0 {
				continue
			}
			msg := map[string]interface{}{"role": "assistant", "content": blocks, "ts": ts}
			if model != "" {
				msg["model"] = model
			}
			items = append(items, msg)
		case "ToolResults":
			var blocks []interface{}
			for _, c := range d.Content {
				if c.Kind != "toolResult" {
					continue
				}
				var tr kiroToolResultData
				if json.Unmarshal(c.Data, &tr) != nil {
					continue
				}
				blocks = append(blocks, map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": tr.ToolUseID,
					"content":     kiroJoinText(tr.Content),
					"is_error":    tr.Status != "" && tr.Status != "success",
				})
			}
			if len(blocks) > 0 {
				items = append(items, map[string]interface{}{"role": "user", "content": blocks, "ts": ts})
			}
		}
	}
	return items, model, nil
}
