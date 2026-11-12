// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Live status of native-session agents (kiro) for poll_data.
//
// poll_data read every agent's status from reply.json — a file only gateway-
// audited CLIs have. A kiro agent therefore had NO status entry at all: its card
// could not tell working from idle, the stop button and the message queue ran
// on a 30-second guess, and a message sent while kiro was mid-turn was typed
// straight into the busy CLI. The session log already knows (see inFlight), so
// publish it in the same shape the cards consume.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// kiroSessionIndexTTL bounds how stale the cwd→session map may be. Locating a
// session reads every session meta file, far too much to repeat per agent per
// poll; one scan serves every kiro agent for the next few seconds.
const kiroSessionIndexTTL = 3 * time.Second

var kiroSessionIndex = struct {
	sync.Mutex
	dir string
	at  time.Time
	ids map[string]string // abs cwd → most recently updated session id
}{}

func kiroSessionIDCached(dir, workspace string) string {
	if dir == "" || strings.TrimSpace(workspace) == "" {
		return ""
	}
	want, _ := filepath.Abs(workspace)
	kiroSessionIndex.Lock()
	defer kiroSessionIndex.Unlock()
	if kiroSessionIndex.ids == nil || kiroSessionIndex.dir != dir || time.Since(kiroSessionIndex.at) > kiroSessionIndexTTL {
		kiroSessionIndex.ids, kiroSessionIndex.dir, kiroSessionIndex.at = kiroBuildSessionIndex(dir), dir, time.Now()
	}
	return kiroSessionIndex.ids[want]
}

func kiroBuildSessionIndex(dir string) map[string]string {
	ids := map[string]string{}
	updated := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ids
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var meta kiroSessionMeta
		if json.Unmarshal(raw, &meta) != nil || strings.TrimSpace(meta.Cwd) == "" {
			continue
		}
		cwd, _ := filepath.Abs(meta.Cwd)
		id := meta.SessionID
		if id == "" {
			id = strings.TrimSuffix(e.Name(), ".json")
		}
		if prev, ok := updated[cwd]; !ok || meta.UpdatedAt > prev {
			ids[cwd], updated[cwd] = id, meta.UpdatedAt
		}
	}
	return ids
}

// nativeSessionLiteMetrics is agentInspectorLiteMetrics for a native-session
// agent, or nil when the agent is not one / has no session yet.
func nativeSessionLiteMetrics(paneID string) M {
	if !(kiroSessionProvider{}).handles(nativeSessionAgentType(paneID)) {
		return nil
	}
	workspace := paneWorkspace(normPaneID(paneID))
	dir := kiroSessionsDir()
	sid := kiroSessionIDCached(dir, workspace)
	if sid == "" {
		return nil
	}
	view, ok := loadNativeSessionViewAt(kiroSessionProvider{}, sid, filepath.Join(dir, sid+".jsonl"))
	if !ok {
		return nil
	}
	return view.liteMetrics(agentSuspended(workspace))
}

// liteMetrics: the poll_data status entry. A suspended (minimized) agent has no
// CLI running, so an unanswered last message is not work in progress.
func (v nativeSessionView) liteMetrics(suspended bool) M {
	status, complete := "idle", true
	if !suspended && v.inFlight() {
		status, complete = "working", false
	}
	stamp := v.ModTime.UTC().Format(time.RFC3339)
	out := M{
		"status":     status,
		"complete":   complete,
		"started_at": stamp,
		"updated_at": stamp,
		"model":      v.Model,
		"source":     "native-session",
	}
	for i := len(v.Items) - 1; i >= 0; i-- {
		if nativeItemIsUserPrompt(v.Items[i]) {
			out["latest_question"] = truncateRunes(nativeItemText(v.Items[i]), 1200)
			break
		}
	}
	return out
}
