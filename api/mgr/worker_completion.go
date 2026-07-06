// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log"
	"strings"
	"sync"
	"time"
)

type workerPendingPrompt struct {
	Seq    int64
	Text   string
	SentAt time.Time
}

var workerCompletionState = struct {
	mu      sync.Mutex
	nextSeq int64
	pending map[string]workerPendingPrompt
}{
	pending: map[string]workerPendingPrompt{},
}

func paneRole(paneID string) string {
	var role string
	_ = store.QueryRow("SELECT COALESCE(role,'') FROM agent_config WHERE pane_id=?", normPaneID(paneID)).Scan(&role)
	return strings.ToLower(strings.TrimSpace(role))
}

func isWorkerPane(paneID string) bool {
	shortID := shortPaneID(normPaneID(paneID))
	if shortID == "" {
		return false
	}
	if paneRole(paneID) == "worker" {
		return true
	}
	return strings.HasPrefix(shortID, "w-2")
}

func shouldTrackWorkerPrompt(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "work done") {
		return false
	}
	return true
}

func markWorkerPromptSubmitted(paneID string, text string) {
	if !isWorkerPane(paneID) || !shouldTrackWorkerPrompt(text) {
		return
	}
	shortID := shortPaneID(normPaneID(paneID))
	workerCompletionState.mu.Lock()
	defer workerCompletionState.mu.Unlock()
	workerCompletionState.nextSeq++
	state := workerPendingPrompt{
		Seq:    workerCompletionState.nextSeq,
		Text:   strings.TrimSpace(text),
		SentAt: time.Now(),
	}
	workerCompletionState.pending[shortID] = state
	log.Printf("[worker-complete] pending worker=%s seq=%d text=%q", shortID, state.Seq, state.Text)
}

func completeWorkerPromptIfIdle(paneID string, old, new paneSt) {
	shortID := shortPaneID(normPaneID(paneID))
	if shortID == "" || !isWorkerPane(paneID) {
		return
	}
	newStatus := ""
	if new.Status != nil {
		newStatus = strings.ToLower(strings.TrimSpace(*new.Status))
	}
	if newStatus != "idle" {
		return
	}
	oldStatus := ""
	if old.Status != nil {
		oldStatus = strings.ToLower(strings.TrimSpace(*old.Status))
	}
	if oldStatus == "idle" {
		return
	}
	workerCompletionState.mu.Lock()
	state, ok := workerCompletionState.pending[shortID]
	if ok {
		delete(workerCompletionState.pending, shortID)
	}
	workerCompletionState.mu.Unlock()
	if !ok {
		return
	}
	log.Printf("[worker-complete] done worker=%s seq=%d duration=%s text=%q", shortID, state.Seq, time.Since(state.SentAt).Round(time.Millisecond), state.Text)
	notifyActiveMastersByTmuxSend(paneID, "["+shortID+"]work done")
}
