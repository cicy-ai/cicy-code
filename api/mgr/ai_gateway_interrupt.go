// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"sync"
	"time"
)

// User-initiated interrupts for CLI agents (claude / codex / …).
//
// The web "stop" button sends Ctrl+C to the agent's tmux pane. The CLI then
// aborts its in-flight API request, which the gateway sees as a client hang-up
// (stream error / dead stream) and — without this registry — seals the turn as
// `failed` with a "生成失败" item. That is wrong twice over: the history shows a
// scary failure for something the user asked for, and the "new prompt replaces
// the failed turn" logic then makes the previous question disappear.
//
// handleSendKeys records the interrupt here; completeFromResponse consumes it
// (within a short window) and seals the round as a clean user stop instead:
// status completed + the same "cancelled" outcome marker the cicy agent uses,
// which the history view renders as 已停止生成.

const aiGatewayUserInterruptTTL = 20 * time.Second

var (
	aiGatewayUserInterruptMu sync.Mutex
	aiGatewayUserInterrupts  = map[string]time.Time{}
)

func aiGatewayMarkUserInterrupt(agentID string) {
	id := shortPaneID(normPaneID(strings.TrimSpace(agentID)))
	if id == "" {
		return
	}
	aiGatewayUserInterruptMu.Lock()
	aiGatewayUserInterrupts[id] = time.Now()
	aiGatewayUserInterruptMu.Unlock()
}

// aiGatewayConsumeUserInterrupt reports whether a user interrupt was recorded
// for the agent within the TTL, clearing it so only ONE round is reclassified.
func aiGatewayConsumeUserInterrupt(agentID string) bool {
	id := shortPaneID(normPaneID(strings.TrimSpace(agentID)))
	if id == "" {
		return false
	}
	aiGatewayUserInterruptMu.Lock()
	defer aiGatewayUserInterruptMu.Unlock()
	at, ok := aiGatewayUserInterrupts[id]
	if !ok {
		return false
	}
	delete(aiGatewayUserInterrupts, id)
	return time.Since(at) <= aiGatewayUserInterruptTTL
}

// keysLookLikeInterrupt: tmux send-keys spellings of Ctrl+C.
func keysLookLikeInterrupt(keys string) bool {
	k := strings.TrimSpace(keys)
	return k == "C-c" || k == "^C" || k == "\x03"
}
