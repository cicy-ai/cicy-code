// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log"
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

// aiGatewayInterruptSealDelay: how long an interrupted CLI gets to hang up its
// in-flight request (completeFromResponse then seals the round itself).
var aiGatewayInterruptSealDelay = 2 * time.Second

// aiGatewayScheduleInterruptSeal covers the interrupt that completeFromResponse
// never sees: Ctrl+C while a TOOL is running. There is no request in flight
// then — reply.json sits at "working" (last stop_reason tool_use) waiting for a
// follow-up request the stopped CLI will never send. The card stayed busy
// forever and every later message was queued instead of sent.
func aiGatewayScheduleInterruptSeal(agentID string) {
	id := shortPaneID(normPaneID(strings.TrimSpace(agentID)))
	if id == "" {
		return
	}
	at := time.Now()
	time.AfterFunc(aiGatewayInterruptSealDelay, func() {
		defer func() { recover() }()
		if aiGatewaySealInterruptedReply(id, at) {
			log.Printf("[ai-gateway] interrupted tool run sealed agent=%s", id)
		}
	})
}

// aiGatewaySealInterruptedReply finalizes a reply that is still non-terminal
// and has not been written since the interrupt. A reply updated after the
// interrupt is live again (or was sealed by its own request) and is left alone.
func aiGatewaySealInterruptedReply(agentID string, interruptedAt time.Time) bool {
	reply, err := aiGatewayReadReplySnapshotFile(agentID)
	if err != nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(reply.Status))
	if status == "" || status == "idle" || status == "done" || isAIGatewayReplyTerminal(status) {
		return false
	}
	if updated, perr := time.Parse(time.RFC3339, strings.TrimSpace(reply.UpdatedAt)); perr == nil && updated.After(interruptedAt.Add(-time.Second)) {
		return false
	}
	marker := cicyOutcomeMarkerText("cancelled")
	reply.Items = append(reply.Items, map[string]interface{}{
		"id":           len(reply.Items) + 1,
		"type":         "text",
		"text":         marker,
		"cicy_outcome": "cancelled",
	})
	reply.Status = "completed"
	reply.LastStopReason = ""
	reply.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	// This interrupt is spent: it must not reclassify a later, unrelated failure.
	aiGatewayConsumeUserInterrupt(agentID)
	return aiGatewayWriteReplySnapshot(agentID, reply) == nil
}
