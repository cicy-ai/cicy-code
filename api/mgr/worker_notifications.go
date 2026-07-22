// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"database/sql"
	"log"
	"strings"
	"sync"
	"time"
)

func listActiveMasterPanesForWorker(workerPaneID string) ([]string, string, error) {
	shortPane := shortPaneID(workerPaneID)
	if shortPane == "" {
		return nil, "", nil
	}
	rows, err := store.Query(`SELECT pa.pane_id FROM pane_agents pa WHERE pa.agent_name=? AND pa.status='active'`, shortPane)
	if err != nil {
		return nil, shortPane, err
	}
	defer rows.Close()
	var masters []string
	for rows.Next() {
		var masterPane string
		if err := rows.Scan(&masterPane); err != nil && err != sql.ErrNoRows {
			continue
		}
		masters = append(masters, masterPane)
	}
	return masters, shortPane, nil
}

func notifyActiveMastersForWorker(workerPaneID string, evt ChatEvent, logLabel string) {
	masters, shortPane, err := listActiveMasterPanesForWorker(workerPaneID)
	if err != nil {
		log.Printf("[hook] list active masters failed for worker=%s: %v", shortPaneID(workerPaneID), err)
		return
	}
	if shortPane == "" {
		return
	}
	for _, masterPane := range masters {
		hub.broadcast(masterPane, evt)
		if logLabel != "" {
			log.Printf("[hook] notified master %s (chatbus): %s", masterPane, logLabel)
		}
	}
}

// desktopNotifyPromptLimit caps the prompt snippet carried in a desktop
// notification body — the OS banner shows ~2-4 lines, anything longer is noise.
const desktopNotifyPromptLimit = 120

// desktopNotifyDebounce rate-limits notifications to one per agent per window —
// a safety net against any finalize path that fires more than once per turn.
const desktopNotifyDebounceWindow = 30 * time.Second

var (
	desktopNotifyLastMu sync.Mutex
	desktopNotifyLast   = map[string]time.Time{}
)

func desktopNotifyDebounce(shortPane string) bool {
	desktopNotifyLastMu.Lock()
	defer desktopNotifyLastMu.Unlock()
	now := time.Now()
	if last, ok := desktopNotifyLast[shortPane]; ok && now.Sub(last) < desktopNotifyDebounceWindow {
		return false
	}
	desktopNotifyLast[shortPane] = now
	return true
}

// notifyWorkerReplyFinished (issue #27): when a worker with desktop_notify=1
// finishes a USER-prompted turn, push an OS desktop notification to every
// connected cicy-desktop (via its `notify` electronRPC tool). Agent-originated
// turns (📮-stamped cicy-agent msg, 🔔 callback wake-ups) never notify — the
// whole point is "my prompt is done", not inter-agent chatter.
//
// Callers pass the turn's terminal snapshot: status, the sanitized user
// question, and the tool_call count of the finalizing response — a completed
// response that still carries tool_calls is a mid-turn round, not the end.
func notifyWorkerReplyFinished(workerPaneID string, replyStatus string, question string, toolCalls int) {
	shortPane := shortPaneID(workerPaneID)
	if shortPane == "" {
		return
	}
	status := strings.ToLower(strings.TrimSpace(aiGatewayFirstNonEmpty(replyStatus, "completed")))
	// Completed-only, and only the turn's final round (no tool_calls in flight).
	// `failed` deliberately does NOT notify: an interrupted turn finalizes every
	// aborted in-flight request as failed — 2026-07-22 this fired a "reply
	// failed!" banner every ~7s until the CLI settled. Failure is visible in the
	// UI; the notification contract is "your prompt is done".
	if status != "completed" || toolCalls > 0 {
		return
	}
	if !desktopNotifyDebounce(shortPane) {
		log.Printf("[hook] desktop-notify debounced worker=%s", shortPane)
		return
	}
	var enabled sql.NullBool
	if err := store.QueryRow(`SELECT COALESCE(desktop_notify, 0) FROM agent_config WHERE pane_id=?`, normPaneID(workerPaneID)).Scan(&enabled); err != nil || !enabled.Bool {
		return
	}
	q := strings.TrimSpace(question)
	// Agent-originated turns: cicy-agent msg stamps 📮 [w-x], callback wake-ups
	// start with 🔔/⚠️ status lines. Both are inter-agent traffic — skip.
	if q == "" || strings.HasPrefix(q, "📮") || cicyNotifyRe.MatchString(q) {
		log.Printf("[hook] desktop-notify skipped worker=%s status=%s (agent-originated or empty prompt)", shortPane, status)
		return
	}
	if runes := []rune(q); len(runes) > desktopNotifyPromptLimit {
		q = string(runes[:desktopNotifyPromptLimit]) + "…"
	}
	title := shortPane + " reply completed!"
	evt := ChatEvent{Type: "desktop_event", Data: M{
		"type":      "rpc_call",
		"tool":      "notify",
		"args":      M{"title": title, "body": q},
		"requestId": "notify-" + shortPane + "-" + randomAssetID(4),
	}}
	sent := hub.broadcastElectronAll(evt)
	log.Printf("[hook] desktop-notify worker=%s status=%s clients=%d", shortPane, status, sent)
}

func notifyActiveMastersByTmuxSend(workerPaneID string, text string) {
	masters, shortPane, err := listActiveMasterPanesForWorker(workerPaneID)
	if err != nil {
		log.Printf("[hook] list active masters failed for tmux-send worker=%s: %v", shortPaneID(workerPaneID), err)
		return
	}
	if shortPane == "" {
		return
	}
	log.Printf("[hook] tmux-send trigger worker=%s active_masters=%s text=%q", shortPane, strings.Join(masters, ","), text)
	for _, masterPane := range masters {
		if err := sendTextToPane(masterPane, text, true); err != nil {
			log.Printf("[hook] tmux-send master %s failed for worker %s: %v", masterPane, shortPane, err)
			continue
		}
		log.Printf("[hook] tmux-sent master %s: %s", masterPane, text)
	}
}
