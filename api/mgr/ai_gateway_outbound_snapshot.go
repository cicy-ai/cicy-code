// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// outbound.json — diagnostic snapshot of EVERY outbound HTTP request body for the
// current conversation, accumulated across ALL tool-loop rounds (one q → the q
// request + each continuation request). Mirrors reply.json so you can see exactly
// what is being SENT to the model at each round. Rebinds (resets) when the
// conversation changes. The body is the RAW outbound wire request (not q-only
// filtered) — the point is to inspect what actually goes outbound.

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"time"
)

type aiGatewayOutboundRequestSnap struct {
	Seq       int         `json:"seq"`
	TurnID    string      `json:"turn_id,omitempty"`
	RequestID string      `json:"request_id,omitempty"`
	Timestamp string      `json:"ts"`
	NewTurn   bool        `json:"new_turn"` // last message is a user prompt (q), not a tool_result → starts a new turn
	Body      interface{} `json:"body"`

	// bytes is the retained size of Body — the trim budget's unit. Not persisted.
	bytes int
}

type aiGatewayOutboundSnapshot struct {
	ConversationID string                         `json:"conversation_id"`
	AgentID        string                         `json:"agent_id"`
	UpdatedAt      string                         `json:"updated_at"`
	Requests       []aiGatewayOutboundRequestSnap `json:"requests"`
}

var aiGatewayOutboundSnapshots = struct {
	mu    sync.Mutex
	items map[string]*aiGatewayOutboundSnapshot
}{items: map[string]*aiGatewayOutboundSnapshot{}}

// aiGatewayOutboundMaxRounds bounds how many outbound request bodies are kept
// per conversation (in memory AND in outbound.json). Each body is the full wire
// request, so an uncapped slice grew without bound on long tool-loop
// conversations. The most recent rounds are the useful diagnostic; older ones
// are trimmed and freed.
const aiGatewayOutboundMaxRounds = 10

// aiGatewayOutboundMaxBytes bounds the retained bodies by SIZE, not just count.
// Rounds is the wrong dimension: each body is the whole conversation sent that
// round, so on a large context one body is tens of megabytes and "10 rounds"
// meant 587 MB — held in memory AND rewritten to disk in full on EVERY gateway
// round-trip. Trim oldest-first until the retained bodies fit. The newest round
// is always kept verbatim (a truncated body is not a diagnostic), so a single
// huge body still costs its own size — but never 10× it.
const aiGatewayOutboundMaxBytes = 32 << 20 // 32 MB per agent

func aiGatewayOutboundSnapshotPath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "outbound.json")
}

// aiGatewayAppendOutbound records one round's outbound request body into
// outbound.json, accumulating across the conversation's rounds. Resets on a new
// conversation. Both gateway and MITM call it (they share completeFromResponse).
func aiGatewayAppendOutbound(agentID, conversationID, turnID, requestID string, body []byte, ts time.Time) {
	if agentID == "" || len(body) == 0 {
		return
	}
	// Store the RAW request bytes, not a parsed interface{} tree. Unmarshaling
	// into nested map[string]interface{} inflates memory 2–5× over the raw bytes
	// (bytes.growSlice + json.unquote dominated the heap). json.RawMessage marshals
	// back verbatim, so outbound.json is byte-identical — we just stop paying the
	// parse-tree overhead for every retained round.
	var bodyVal interface{}
	if json.Valid(body) {
		bodyVal = json.RawMessage(append([]byte(nil), body...))
	} else {
		bodyVal = string(body)
	}
	newTurn := aiGatewayOutboundIsNewTurn(body)

	aiGatewayOutboundSnapshots.mu.Lock()
	snap := aiGatewayOutboundSnapshots.items[agentID]
	if snap == nil || snap.ConversationID != conversationID {
		snap = &aiGatewayOutboundSnapshot{ConversationID: conversationID, AgentID: agentID}
		aiGatewayOutboundSnapshots.items[agentID] = snap
	}
	nextSeq := 1
	if n := len(snap.Requests); n > 0 {
		nextSeq = snap.Requests[n-1].Seq + 1 // monotonic even after trimming
	}
	snap.Requests = append(snap.Requests, aiGatewayOutboundRequestSnap{
		Seq:       nextSeq,
		TurnID:    turnID,
		RequestID: requestID,
		Timestamp: ts.UTC().Format(time.RFC3339Nano),
		NewTurn:   newTurn,
		Body:      bodyVal,
		bytes:     len(body),
	})
	snap.Requests = aiGatewayOutboundTrim(snap.Requests)
	snap.UpdatedAt = ts.UTC().Format(time.RFC3339Nano)
	out := *snap // copy for writing outside the lock
	aiGatewayOutboundSnapshots.mu.Unlock()

	// Compact, not MarshalIndent: this file is rewritten IN FULL on every gateway
	// round-trip, so pretty-printing it is pure CPU and allocation on the hot path.
	// It is machine-read (jq / the inspector), not eyeballed raw.
	_ = aiGatewayWriteJSONAtomicCompact(aiGatewayOutboundSnapshotPath(agentID), out)
}

// aiGatewayOutboundTrim drops the oldest rounds until the retained set fits BOTH
// budgets (count and bytes). Each entry's Body is the entire conversation as sent
// that round, so an unbounded — or count-only-bounded — slice pinned O(N) copies
// of a multi-megabyte history per agent, and rewrote every one of them to disk on
// each request. The newest round always survives, whatever its size: an empty or
// truncated outbound.json would be worse than a large one.
//
// Copies into a fresh slice so the trimmed-off bodies are actually released —
// reslicing alone keeps them alive in the backing array.
func aiGatewayOutboundTrim(reqs []aiGatewayOutboundRequestSnap) []aiGatewayOutboundRequestSnap {
	if len(reqs) == 0 {
		return reqs
	}
	start := 0
	if n := len(reqs) - aiGatewayOutboundMaxRounds; n > 0 {
		start = n
	}
	total := 0
	for _, r := range reqs[start:] {
		total += r.bytes
	}
	// Always keep the last entry, so never trim past len-1.
	for total > aiGatewayOutboundMaxBytes && start < len(reqs)-1 {
		total -= reqs[start].bytes
		start++
	}
	if start == 0 {
		return reqs
	}
	return append([]aiGatewayOutboundRequestSnap(nil), reqs[start:]...)
}

// dropOutboundSnapshot releases an agent's retained outbound bodies. The map is
// keyed by agentID and is otherwise never pruned, so without this a torn-down
// pane kept pinning up to aiGatewayOutboundMaxBytes for the life of the process.
func dropOutboundSnapshot(agentID string) {
	aiGatewayOutboundSnapshots.mu.Lock()
	delete(aiGatewayOutboundSnapshots.items, agentID)
	delete(aiGatewayOutboundSnapshots.items, shortPaneID(agentID))
	aiGatewayOutboundSnapshots.mu.Unlock()
}

// aiGatewayOutboundIsNewTurn reports whether this outbound request STARTS a new
// turn — its last message is a human prompt (user role, text content) rather than
// a tool_result (which marks a continuation round of the same turn).
func aiGatewayOutboundIsNewTurn(body []byte) bool {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil || len(req.Messages) == 0 {
		return false
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "user" {
		return false
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(last.Content, &blocks) != nil {
		return true // content is a string = a plain prompt → new turn
	}
	for _, b := range blocks {
		if b.Type != "tool_result" {
			return true
		}
	}
	return false
}
