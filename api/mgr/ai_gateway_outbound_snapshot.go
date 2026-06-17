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
	var parsed interface{}
	if json.Unmarshal(body, &parsed) != nil {
		parsed = string(body)
	}
	newTurn := aiGatewayOutboundIsNewTurn(body)

	aiGatewayOutboundSnapshots.mu.Lock()
	snap := aiGatewayOutboundSnapshots.items[agentID]
	if snap == nil || snap.ConversationID != conversationID {
		snap = &aiGatewayOutboundSnapshot{ConversationID: conversationID, AgentID: agentID}
		aiGatewayOutboundSnapshots.items[agentID] = snap
	}
	snap.Requests = append(snap.Requests, aiGatewayOutboundRequestSnap{
		Seq:       len(snap.Requests) + 1,
		TurnID:    turnID,
		RequestID: requestID,
		Timestamp: ts.UTC().Format(time.RFC3339Nano),
		NewTurn:   newTurn,
		Body:      parsed,
	})
	snap.UpdatedAt = ts.UTC().Format(time.RFC3339Nano)
	out := *snap // copy for writing outside the lock
	aiGatewayOutboundSnapshots.mu.Unlock()

	_ = aiGatewayWriteJSONAtomic(aiGatewayOutboundSnapshotPath(agentID), out)
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
