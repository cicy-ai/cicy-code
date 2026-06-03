package main

// Adapter between the mitm sub-package and audit-v2.
//
// A MITM-captured turn drives the cooperative gateway's audit session EXACTLY
// like a gateway turn does, so current.json / reply.json are produced by the
// same shared code (same struct, single canonical path, status_map, streamed
// items incl. tool_use, token math, cross-agent reply callbacks):
//
//	gateway:  newAIGatewayAuditSession → writeStartSnapshots →
//	          recordOutboundRequest (Director) →
//	          newAIGatewayAuditReadCloser (ModifyResponse) → completeFromResponse
//	mitm:     same calls, driven from the MITM pump.
//
// On top of that it feeds the raw request/response to the audit-v2 scanner via
// SubmitMitmEvent (the MITM-only hook that lands findings in the per-agent
// ndjson the autonomy loop queries; the gateway has its own audit path).

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"ttyd-go/mgr/audit"
	"ttyd-go/mgr/mitm"
)

type mitmAuditAdapter struct{}

func (mitmAuditAdapter) StartTurn(provider, agentID string, target *url.URL, method string, headers http.Header, body []byte) mitm.AuditTurn {
	// Decompress the request body up front: codex (Rust/reqwest) sends it
	// zstd-encoded, others may use gzip. The inference gate and the session both
	// json-parse the body, so a compressed body would be unparseable → classified
	// as non-inference → no current.json/reply.json. The real (still-compressed)
	// request reaches the upstream via the MITM pump; this decoded copy is
	// audit-only.
	body = aiGatewayMaybeGunzip(headers, body)
	// MITM intercepts an entire whitelisted host (e.g. api.anthropic.com), so it
	// sees ALL traffic to that host — not just model turns. Claude Code, codex,
	// opencode etc. also hit telemetry (/api/event_logging), token-counting
	// (count_tokens), model listing, OAuth, etc. on the same host. Those are NOT
	// AI requests and must never land in current.json / reply.json (they'd
	// overwrite the real /v1/messages turn with an empty snapshot). Only audit
	// genuine inference calls. The local gateway path doesn't need this — only AI
	// requests are ever routed through it.
	if !mitmIsAIInferenceRequest(target, body) {
		return mitmNoopTurn{}
	}

	// Split the full target into base (scheme+host) + suffix (path[?query]) so
	// the session reconstructs the URL without path munging.
	base := &url.URL{Scheme: target.Scheme, Host: target.Host}
	suffix := target.Path
	if target.RawQuery != "" {
		suffix += "?" + target.RawQuery
	}

	// newAIGatewayAuditSession wires replyHooks via newReplyHooksForPane →
	// peekCallbackHooksForPane internally, so we must NOT touch callbacks here.
	sess := newAIGatewayAuditSession(provider, agentID, base, suffix, method, headers, body)
	if err := sess.writeStartSnapshots(); err != nil {
		log.Printf("[mitm] write current snapshot for %s: %v", agentID, err)
	}
	// Record the outbound request just like the gateway's proxy Director, so
	// request-side data (incl. tool_result blocks carried in the next request of
	// a tool turn) is recorded into the session/timeline identically.
	if req, err := http.NewRequest(method, target.String(), nil); err == nil {
		req.Header = headers.Clone()
		sess.recordOutboundRequest(req)
	}
	mitmSubmitAudit(sess, audit.DirectionOutbound, body, "current.json")
	return &mitmAuditTurn{sess: sess}
}

type mitmAuditTurn struct {
	sess *aiGatewayAuditSession

	mu      sync.Mutex
	scanBuf []byte // buffered response copy for the audit-v2 inbound scanner
}

func (t *mitmAuditTurn) WrapResponseBody(inner io.ReadCloser, statusCode int, headers http.Header, contentLength int64) io.ReadCloser {
	// Reuse the gateway's response pipeline verbatim: it streams live chat
	// events, buffers the body, and finalizes reply.json via completeFromResponse
	// on EOF/Close — byte-identical to a gateway turn. Wrap it to also tee a copy
	// to the audit-v2 inbound scanner.
	rc := newAIGatewayAuditReadCloser(inner, t.sess, statusCode, headers, contentLength)
	return &mitmScannerReadCloser{rc: rc, turn: t}
}

func (t *mitmAuditTurn) Fail(err error) {
	t.sess.completeFromError(err)
	mitmSubmitAudit(t.sess, audit.DirectionInbound, []byte(err.Error()), "reply.json")
}

// mitmScannerReadCloser wraps the gateway's audit read closer (which builds
// reply.json) and additionally buffers the response so it can be fed to the
// audit-v2 scanner on Close.
type mitmScannerReadCloser struct {
	rc   *aiGatewayAuditReadCloser
	turn *mitmAuditTurn
}

func (m *mitmScannerReadCloser) Read(p []byte) (int, error) {
	n, err := m.rc.Read(p)
	if n > 0 {
		m.turn.mu.Lock()
		m.turn.scanBuf = append(m.turn.scanBuf, p[:n]...)
		m.turn.mu.Unlock()
	}
	return n, err
}

func (m *mitmScannerReadCloser) Close() error {
	err := m.rc.Close() // gateway finalize: reply.json + reply callbacks
	m.turn.mu.Lock()
	body := m.turn.scanBuf
	m.turn.mu.Unlock()
	// Feed the scanner plaintext when the upstream gzipped the body, matching the
	// reply.json parse path — otherwise PII/secret scans run over gzip bytes.
	body = aiGatewayMaybeGunzip(m.rc.headers, body)
	mitmSubmitAudit(m.turn.sess, audit.DirectionInbound, body, "reply.json")
	return err
}

// mitmIsAIInferenceRequest reports whether an intercepted MITM request is an
// actual model-inference call (Anthropic /v1/messages, OpenAI /chat/completions
// or /responses, legacy /completions). It is deliberately strict so non-AI
// traffic that shares the same whitelisted host — telemetry (/api/event_logging),
// token counting (count_tokens), model listing (/models), OAuth, etc. — is NOT
// recorded into current.json / reply.json.
//
// Signal: an inference request is a JSON body carrying a "model" plus one of
// messages / input / prompt. That cleanly excludes event_logging (no model) and
// count_tokens (excluded by path even though it carries model+messages).
func mitmIsAIInferenceRequest(target *url.URL, body []byte) bool {
	if target == nil || len(body) == 0 {
		return false
	}
	p := strings.ToLower(target.Path)
	switch {
	case strings.Contains(p, "/event_logging"),
		strings.Contains(p, "count_tokens"),
		strings.HasSuffix(p, "/models"),
		strings.Contains(p, "/models/"):
		return false
	}
	var b map[string]interface{}
	if json.Unmarshal(body, &b) != nil {
		return false
	}
	if _, ok := b["model"]; !ok {
		return false
	}
	_, hasMessages := b["messages"]
	_, hasInput := b["input"]
	_, hasPrompt := b["prompt"]
	return hasMessages || hasInput || hasPrompt
}

// mitmNoopTurn is returned for non-inference MITM requests: it does nothing, so
// no session is opened and current.json / reply.json are left untouched.
type mitmNoopTurn struct{}

func (mitmNoopTurn) WrapResponseBody(inner io.ReadCloser, _ int, _ http.Header, _ int64) io.ReadCloser {
	return inner
}
func (mitmNoopTurn) Fail(error) {}

// mitmSubmitAudit feeds the raw payload to the audit-v2 scanner with the
// session's identity so secrets/PII findings land in the per-agent ndjson.
func mitmSubmitAudit(s *aiGatewayAuditSession, dir string, payload []byte, ref string) {
	if s == nil || len(payload) == 0 {
		return
	}
	audit.SubmitMitmEvent(audit.Envelope{
		AgentID:        s.agentID,
		TurnID:         s.turnID,
		ConversationID: s.conversationID,
		Provider:       s.provider,
		Model:          s.model,
		Direction:      dir,
		Payload:        append([]byte(nil), payload...),
		PayloadRef:     ref,
	})
}
