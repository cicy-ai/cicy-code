package main

// Adapter between the mitm sub-package and audit-v2's autonomy pipeline.
//
// Each MITM-captured turn:
//   1. Writes a simple current.json + reply.json snapshot under
//      ~/cicy-ai/workers/<agent>/.cicy/history/mitm/<turn>/ so dashboards
//      and forensics tooling can read the same field shape they get from
//      the cooperative gateway path (with "source": "mitm" stamped).
//   2. Submits an audit.Envelope (outbound + inbound) through
//      audit.SubmitMitmEvent so the scanner runs and findings land in
//      the per-agent ndjson the autonomy loop queries.
//
// Decoupled from ai_gateway_audit.go (which lives in the cooperative
// gateway path with its own evolving signatures). Keeps audit-v2's MITM
// pipeline isolated from cooperative-side drift.

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ttyd-go/mgr/audit"
	"ttyd-go/mgr/mitm"
)

type mitmAuditAdapter struct{}

func (mitmAuditAdapter) StartTurn(provider, agentID string, target *url.URL, method string, headers http.Header, body []byte) mitm.AuditTurn {
	turnID := fmt.Sprintf("turn-%d", time.Now().UnixNano())
	conversationID := headers.Get(mitm.HeaderTraceID)
	if conversationID == "" {
		conversationID = turnID
	}

	turn := &mitmAuditTurn{
		turnID:         turnID,
		agentID:        agentID,
		conversationID: conversationID,
		provider:       provider,
		method:         method,
		target:         target,
		requestBody:    append([]byte(nil), body...),
		startedAt:      time.Now().UTC().Format(time.RFC3339Nano),
	}
	// Drain any cross-agent reply callbacks queued against this pane, exactly
	// like the cooperative gateway does at the start of each turn. Each upstream
	// request is one StartTurn, so the tool_call re-queue semantics carry over.
	turn.hooks = drainCallbackHooksForPane(agentID)
	turn.writeCurrentSnapshot(headers)

	// Outbound audit submit — scanner inspects the request body for
	// secrets / PII so the autonomy loop has findings to act on.
	if payload, err := json.Marshal(turn.currentSnapshot(headers)); err == nil {
		audit.SubmitMitmEvent(audit.Envelope{
			AgentID:        agentID,
			TurnID:         turnID,
			ConversationID: conversationID,
			Provider:       provider,
			Direction:      audit.DirectionOutbound,
			Payload:        payload,
			PayloadRef:     fmt.Sprintf("current.json#%s", turnID),
		})
	}
	return turn
}

type mitmAuditTurn struct {
	turnID         string
	agentID        string
	conversationID string
	provider       string
	method         string
	target         *url.URL
	requestBody    []byte
	startedAt      string
	hooks          []aiGatewayReplyHook // cross-agent reply callbacks drained at StartTurn

	mu             sync.Mutex
	responseBuf    []byte
	responseStatus int
	responseHdr    http.Header
}

func (t *mitmAuditTurn) WrapResponseBody(inner io.ReadCloser, statusCode int, headers http.Header, _ int64) io.ReadCloser {
	t.responseStatus = statusCode
	t.responseHdr = headers
	return &mitmResponseTee{inner: inner, owner: t}
}

func (t *mitmAuditTurn) Fail(err error) {
	reply := map[string]interface{}{
		"turn_id":    t.turnID,
		"status":     "error",
		"error":      err.Error(),
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
		"source":     "mitm",
	}
	t.writeReplyJSON(reply)
	if payload, mErr := json.Marshal(reply); mErr == nil {
		audit.SubmitMitmEvent(audit.Envelope{
			AgentID:        t.agentID,
			TurnID:         t.turnID,
			ConversationID: t.conversationID,
			Provider:       t.provider,
			Direction:      audit.DirectionInbound,
			Payload:        payload,
			PayloadRef:     fmt.Sprintf("reply.json#%s", t.turnID),
		})
	}
	t.emitSnapshot("failed", err.Error(), false)
}

func (t *mitmAuditTurn) finish() {
	t.mu.Lock()
	body := t.responseBuf
	t.mu.Unlock()

	reply := map[string]interface{}{
		"turn_id":     t.turnID,
		"status":      "completed",
		"status_code": t.responseStatus,
		"headers":     t.responseHdr,
		"updated_at":  time.Now().UTC().Format(time.RFC3339Nano),
		"source":      "mitm",
	}
	if len(body) > 0 {
		var parsed interface{}
		if err := json.Unmarshal(body, &parsed); err == nil {
			reply["body"] = parsed
		} else {
			reply["body_raw"] = string(body)
		}
	}
	t.writeReplyJSON(reply)

	if payload, mErr := json.Marshal(reply); mErr == nil {
		audit.SubmitMitmEvent(audit.Envelope{
			AgentID:        t.agentID,
			TurnID:         t.turnID,
			ConversationID: t.conversationID,
			Provider:       t.provider,
			Direction:      audit.DirectionInbound,
			Payload:        payload,
			PayloadRef:     fmt.Sprintf("reply.json#%s", t.turnID),
		})
	}

	// Canonical reply snapshot + cross-agent reply callback (parity with the
	// cooperative gateway path, so MITM-captured agents can callback too).
	answer, hasToolCalls := mitmParseUpstream(body)
	status := "completed"
	if t.responseStatus != 0 && (t.responseStatus < 200 || t.responseStatus >= 300) {
		status = "failed"
	}
	t.emitSnapshot(status, answer, hasToolCalls)
}

type mitmResponseTee struct {
	inner io.ReadCloser
	owner *mitmAuditTurn
}

func (t *mitmResponseTee) Read(p []byte) (int, error) {
	n, err := t.inner.Read(p)
	if n > 0 {
		t.owner.mu.Lock()
		t.owner.responseBuf = append(t.owner.responseBuf, p[:n]...)
		t.owner.mu.Unlock()
	}
	return n, err
}

func (t *mitmResponseTee) Close() error {
	t.owner.finish()
	return t.inner.Close()
}

// --- snapshot helpers ---

func (t *mitmAuditTurn) currentSnapshot(headers http.Header) map[string]interface{} {
	out := map[string]interface{}{
		"turn_id":         t.turnID,
		"agent_id":        t.agentID,
		"conversation_id": t.conversationID,
		"provider":        t.provider,
		"url":             t.target.String(),
		"method":          t.method,
		"headers":         headers,
		"started_at":      time.Now().UTC().Format(time.RFC3339Nano),
		"source":          "mitm",
	}
	if len(t.requestBody) > 0 {
		var parsed interface{}
		if err := json.Unmarshal(t.requestBody, &parsed); err == nil {
			out["body"] = parsed
		} else {
			out["body_raw"] = string(t.requestBody)
		}
	}
	return out
}

func (t *mitmAuditTurn) writeCurrentSnapshot(headers http.Header) {
	path := historyTurnPath(t.agentID, t.turnID, "current.json")
	writeAtomicJSON(path, t.currentSnapshot(headers))
}

func (t *mitmAuditTurn) writeReplyJSON(reply interface{}) {
	path := historyTurnPath(t.agentID, t.turnID, "reply.json")
	writeAtomicJSON(path, reply)
}

func historyTurnPath(agentID, turnID, filename string) string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, "cicy-ai", "workers", agentID, ".cicy", "history", "mitm", turnID, filename)
}

func writeAtomicJSON(path string, value interface{}) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("[mitm] mkdir %s: %v", path, err)
		return
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		log.Printf("[mitm] marshal %s: %v", path, err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		log.Printf("[mitm] write %s: %v", path, err)
		return
	}
	_ = os.Rename(tmp, path)
}

// emitSnapshot writes the canonical reply.json (so cicy_agent_get_last_reply
// and dashboards read MITM turns identically to gateway turns) and fires any
// cross-agent reply callbacks attached at StartTurn.
func (t *mitmAuditTurn) emitSnapshot(status, answer string, hasToolCalls bool) {
	snap := aiGatewayReplySnapshot{
		TurnID:    t.turnID,
		Status:    status,
		StartedAt: t.startedAt,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Answer:    answer,
	}
	if hasToolCalls {
		// Non-empty signals "tool calls still in flight" so the callback hook
		// defers to the next request in the same turn (see replyCallbackHook).
		snap.ToolCalls = []aiGatewayToolCall{{}}
	}
	writeAtomicJSON(filepath.Join(aiGatewayHistoryDir(t.agentID), "reply.json"), snap)
	for _, h := range t.hooks {
		if h != nil {
			h.finalize(snap)
		}
	}
}

// mitmParseUpstream best-effort extracts the assistant answer text and whether
// the turn ended on a tool call, from a complete upstream response body
// (Anthropic / OpenAI, streaming SSE or a single JSON object). Enough to drive
// the reply snapshot + the callback's fire/defer decision.
func mitmParseUpstream(body []byte) (answer string, hasToolCalls bool) {
	if len(body) == 0 {
		return "", false
	}
	text := string(body)
	if strings.Contains(text, "data:") && strings.Contains(text, "\n") {
		var sb strings.Builder
		for _, line := range strings.Split(text, "\n") {
			_, payload := aiGatewayParseSSELine(line)
			if payload == nil {
				continue
			}
			if d, ok := payload["delta"].(map[string]interface{}); ok {
				if aiGatewayString(d["type"]) == "text_delta" {
					if s, ok := d["text"].(string); ok { // raw: streamed text spaces are significant
						sb.WriteString(s)
					}
				}
				if aiGatewayString(d["stop_reason"]) == "tool_use" || aiGatewayString(d["finish_reason"]) == "tool_calls" {
					hasToolCalls = true
				}
			}
			if cb, ok := payload["content_block"].(map[string]interface{}); ok && aiGatewayString(cb["type"]) == "tool_use" {
				hasToolCalls = true
			}
			if choices, ok := payload["choices"].([]interface{}); ok {
				for _, c := range choices {
					cm, ok := c.(map[string]interface{})
					if !ok {
						continue
					}
					if dd, ok := cm["delta"].(map[string]interface{}); ok {
						if s, ok := dd["content"].(string); ok {
							sb.WriteString(s)
						}
					}
					if aiGatewayString(cm["finish_reason"]) == "tool_calls" {
						hasToolCalls = true
					}
				}
			}
		}
		if sb.Len() > 0 || hasToolCalls {
			return sb.String(), hasToolCalls
		}
	}
	var obj map[string]interface{}
	if json.Unmarshal(body, &obj) != nil {
		return "", false
	}
	if aiGatewayString(obj["stop_reason"]) == "tool_use" {
		hasToolCalls = true
	}
	if content, ok := obj["content"].([]interface{}); ok {
		var sb strings.Builder
		for _, b := range content {
			bm, ok := b.(map[string]interface{})
			if !ok {
				continue
			}
			switch aiGatewayString(bm["type"]) {
			case "text":
				if s, ok := bm["text"].(string); ok {
					sb.WriteString(s)
				}
			case "tool_use":
				hasToolCalls = true
			}
		}
		return sb.String(), hasToolCalls
	}
	if choices, ok := obj["choices"].([]interface{}); ok && len(choices) > 0 {
		if cm, ok := choices[0].(map[string]interface{}); ok {
			if aiGatewayString(cm["finish_reason"]) == "tool_calls" {
				hasToolCalls = true
			}
			if msg, ok := cm["message"].(map[string]interface{}); ok {
				s, _ := msg["content"].(string)
				return s, hasToolCalls
			}
		}
	}
	return "", hasToolCalls
}
