package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ttyd-go/mgr/audit"
)

// Routes registered in main.go (all wa() = bearer-auth, except ack):
//   GET  /api/audit/events                    — list events with filters
//   GET  /api/audit/events/{id}               — single event detail
//   GET  /api/audit/stats                     — aggregations
//   GET  /api/audit/agents                    — agents that have any events
//   POST /api/audit/ingest                    — mitmproxy webhook (Channel B)
//   POST /api/audit/allowlist/content         — mark a content SHA as false-positive
//   GET/POST /api/audit/policy                — read / write global policy.json
//   GET/POST /api/audit/policy/agents/{id}    — read / write per-agent override
//   GET  /api/audit/policy/effective/{id}     — merged (global ⊕ agent) view
//   GET  /api/audit/ack?token=...             — PUBLIC incident-email ack landing

func handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	opts := parseQueryOpts(r)
	result, err := audit.Query(opts)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, result)
}

func handleAuditEventByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/audit/events/"))
	id = strings.TrimRight(id, "/")
	if id == "" {
		httpErr(w, http.StatusBadRequest, "missing_id")
		return
	}
	event, err := audit.GetEventByID(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if event == nil {
		httpErr(w, http.StatusNotFound, "not_found")
		return
	}
	J(w, event)
}

func handleAuditStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	opts := parseQueryOpts(r)
	stats, err := audit.ComputeStats(opts)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, stats)
}

// handleAuditIngest is the mitmproxy webhook for Channel B (agents that
// bypass the cicy AI gateway and talk to external LLM providers directly).
//
//	POST /api/audit/ingest
//	Authorization: Bearer <token>
//	Content-Type: application/json
//	{
//	  "agent_id":         "w-10001",                  // REQUIRED
//	  "direction":        "outbound" | "inbound",     // REQUIRED
//	  "payload":          "<request or reply body>",  // REQUIRED
//	  "payload_encoding": "utf8" | "base64",          // default utf8
//
//	  "agent_type":       "claude",                   // optional but recommended
//	  "user_id":          "u-abc",
//	  "session_id":       "sess-xyz",
//	  "turn_id":          "turn_xxx",
//	  "conversation_id":  "conv_xxx",
//	  "provider":         "anthropic",
//	  "model":            "claude-opus-4-7",
//	  "payload_ref":      "mitm:flow-id-..."          // free-form, for forensics
//	}
//
// Responses:
//   204 — accepted (event will be processed asynchronously)
//   400 — bad request (missing required field, bad encoding, ...)
//   401 — not authenticated (handled by wa() middleware)
func handleAuditIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req struct {
		AgentID         string `json:"agent_id"`
		AgentType       string `json:"agent_type"`
		UserID          string `json:"user_id"`
		SessionID       string `json:"session_id"`
		TurnID          string `json:"turn_id"`
		ConversationID  string `json:"conversation_id"`
		Provider        string `json:"provider"`
		Model           string `json:"model"`
		Direction       string `json:"direction"`
		Payload         string `json:"payload"`
		PayloadEncoding string `json:"payload_encoding"`
		PayloadRef      string `json:"payload_ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if strings.TrimSpace(req.AgentID) == "" {
		httpErr(w, http.StatusBadRequest, "agent_id_required")
		return
	}
	if req.Direction != audit.DirectionOutbound && req.Direction != audit.DirectionInbound {
		httpErr(w, http.StatusBadRequest, "direction_must_be_outbound_or_inbound")
		return
	}

	var payload []byte
	switch strings.ToLower(strings.TrimSpace(req.PayloadEncoding)) {
	case "", "utf8", "utf-8":
		payload = []byte(req.Payload)
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(req.Payload)
		if err != nil {
			httpErr(w, http.StatusBadRequest, "payload_base64_decode_failed")
			return
		}
		payload = decoded
	default:
		httpErr(w, http.StatusBadRequest, "unknown_payload_encoding")
		return
	}

	payloadRef := req.PayloadRef
	if payloadRef == "" {
		payloadRef = "mitm:" + req.Direction + "/" + req.AgentID
		if req.TurnID != "" {
			payloadRef += "/" + req.TurnID
		}
	}

	env := audit.Envelope{
		AgentID:        req.AgentID,
		AgentType:      req.AgentType,
		UserID:         req.UserID,
		SessionID:      req.SessionID,
		SourceChannel:  audit.SourceMitm,
		TurnID:         req.TurnID,
		ConversationID: req.ConversationID,
		Provider:       req.Provider,
		Model:          req.Model,
		Direction:      req.Direction,
		Payload:        payload,
		PayloadRef:     payloadRef,
	}

	// Phase 3 inline preventive check. Runs only when policy.preventive.enabled
	// is true.
	//   block  -> 451; mitmproxy script must drop the upstream request
	//   redact -> 200 + modified payload; mitmproxy script forwards the
	//             modified body (NOT the original) to the LLM provider
	//   none   -> standard 204; mitmproxy forwards the original
	if dec := audit.PreventiveCheck(env); dec.Action != audit.ActionNone {
		ruleIDs := make([]string, 0, len(dec.Findings))
		for _, f := range dec.Findings {
			ruleIDs = append(ruleIDs, f.RuleID)
		}
		body := map[string]interface{}{
			"action":    string(dec.Action),
			"event_id":  dec.EventID,
			"reason":    dec.Reason,
			"rules_hit": ruleIDs,
		}
		w.Header().Set("Content-Type", "application/json")
		switch dec.Action {
		case audit.ActionBlock:
			body["blocked"] = true
			w.WriteHeader(451)
		case audit.ActionRedact:
			body["payload"] = base64.StdEncoding.EncodeToString(dec.ModifiedPayload)
			body["payload_encoding"] = "base64"
			body["pre_redact_ref"] = dec.PreRedactRef
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(w).Encode(body)
		return
	}

	audit.SubmitMitmEvent(env)
	w.WriteHeader(http.StatusNoContent)
}

// handleAuditAllowlistContent lets an auditor add the SHA256 of a payload
// to policy.allow_list.content_hashes so future events with identical
// content are suppressed. Idempotent; concurrent calls serialized
// behind audit.AddToAllowList's mutex.
//
//	POST /api/audit/allowlist/content
//	{ "sha256": "sha256:abc...", "reason": "false positive: ..." }
//
// Response: 204 on success (added or already present); 400 on bad input;
// 500 on disk error.
func handleAuditAllowlistContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req struct {
		SHA256 string `json:"sha256"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if !strings.HasPrefix(req.SHA256, "sha256:") {
		httpErr(w, http.StatusBadRequest, "sha256_must_have_prefix")
		return
	}
	path, err := audit.AddToAllowList(audit.AllowCategoryContentHash, req.SHA256, req.Reason)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if path == "" {
		log.Printf("[audit] FP mark idempotent (already in allow_list): sha=%s", req.SHA256)
	} else {
		log.Printf("[audit] FP marked sha=%s reason=%q written=%s", req.SHA256, req.Reason, path)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAuditAck is the public landing page for incident-email
// "confirm/解除" links. No bearer auth — the HMAC-signed token IS the
// proof of identity. Verifies, records a meta_alert_ack event, and
// returns a minimal HTML page.
//
//	GET /api/audit/ack?token=<base64url(payload)>.<hex(hmac)>
//
// Possible responses:
//   200 + HTML: ack recorded
//   400 + HTML: token missing / malformed
//   403 + HTML: signature mismatch
//   410 + HTML: token expired
//   500 + HTML: store append failed
//
// The recorded event keeps the chain intact (it's just one more line in
// the global index + the "meta-audit" per-agent file).
func handleAuditAck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		ackHTML(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		ackHTML(w, http.StatusBadRequest, "missing token", "")
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		ackHTML(w, http.StatusInternalServerError, "server: home unresolved", "")
		return
	}
	auditRoot := filepath.Join(home, "cicy-ai", "audit")

	eventID, err := audit.VerifyAckToken(auditRoot, token)
	if err != nil {
		status := http.StatusForbidden
		if strings.Contains(err.Error(), "expired") {
			status = http.StatusGone
		} else if strings.Contains(err.Error(), "malformed") || strings.Contains(err.Error(), "decode") {
			status = http.StatusBadRequest
		}
		ackHTML(w, status, err.Error(), "")
		return
	}

	ua := r.UserAgent()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	ip := clientRemoteIP(r)
	metaID, err := audit.RecordAck(eventID, "", ua, ip)
	if err != nil {
		ackHTML(w, http.StatusInternalServerError, "record: "+err.Error(), "")
		return
	}
	log.Printf("[audit] alert ack event=%s meta_event=%s ua=%q ip=%s", eventID, metaID, ua, ip)
	ackHTML(w, http.StatusOK, "", eventID)
}

func clientRemoteIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	return r.RemoteAddr
}

func ackHTML(w http.ResponseWriter, status int, errMsg, eventID string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	var body string
	if errMsg != "" {
		body = `<h2>cicy-code audit: ack failed</h2><p>` + escapeHTML(errMsg) + `</p>`
	} else {
		body = `<h2>cicy-code audit: 已确认</h2>
<p>Event <code>` + escapeHTML(eventID) + `</code> 已被记录为 acknowledged.</p>
<p>You may close this tab. To view details, log in to the cicy-code audit dashboard.</p>`
	}
	_, _ = w.Write([]byte(`<!doctype html>
<html><head><meta charset="utf-8"><title>cicy-code audit</title>
<style>body{font:14px/1.5 -apple-system,Segoe UI,sans-serif;max-width:560px;margin:60px auto;padding:0 16px;color:#222}
h2{color:#0a0a0a;font-weight:600}code{background:#f3f4f6;padding:2px 6px;border-radius:3px}</style>
</head><body>` + body + `</body></html>`))
}

func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// handleAuditPolicyGlobal — GET returns raw policy.json bytes; POST
// validates + atomic writes a new global policy. fsnotify reloads the
// running pipeline within ~200ms.
func handleAuditPolicyGlobal(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		raw, err := audit.ReadGlobalPolicyRaw()
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpErr(w, http.StatusBadRequest, "body_read_failed")
			return
		}
		hash, err := audit.WriteGlobalPolicy(body)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("[audit] policy.json updated via API, hash=%s len=%d", hash, len(body))
		J(w, M{"ok": true, "policy_hash": hash})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

// agentIDFromPath pulls the trailing path segment after a known prefix.
// Returns "" when the segment is missing or contains a "/" (subpaths
// not supported here).
func agentIDFromPath(urlPath, prefix string) string {
	if !strings.HasPrefix(urlPath, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(urlPath, prefix)
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	return rest
}

// handleAuditPolicyAgent — GET returns the per-agent override JSON (or
// `{}` if no override); POST atomically writes new contents.
//
//   /api/audit/policy/agents/{agent_id}
func handleAuditPolicyAgent(w http.ResponseWriter, r *http.Request) {
	agentID := agentIDFromPath(r.URL.Path, "/api/audit/policy/agents/")
	if agentID == "" {
		httpErr(w, http.StatusBadRequest, "agent_id_required")
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "home_unresolved")
		return
	}
	workersRoot := filepath.Join(home, "cicy-ai", "workers")

	switch r.Method {
	case http.MethodGet:
		ov, err := audit.LoadAgentOverride(workersRoot, agentID)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if ov == nil {
			_, _ = w.Write([]byte("{}"))
			return
		}
		_ = json.NewEncoder(w).Encode(ov)
	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpErr(w, http.StatusBadRequest, "body_read_failed")
			return
		}
		ov := &audit.AgentOverride{}
		if len(body) > 0 {
			if err := json.Unmarshal(body, ov); err != nil {
				httpErr(w, http.StatusBadRequest, "invalid_json: "+err.Error())
				return
			}
		}
		if err := audit.SaveAgentOverride(workersRoot, agentID, ov); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("[audit] agent override updated agent=%s len=%d", agentID, len(body))
		J(w, M{"ok": true, "agent_id": agentID})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

// handleAuditPolicyEffective — read-only view of (global ⊕ per-agent)
// merged into a single *Policy for the given agent.
//
//   GET /api/audit/policy/effective/{agent_id}
func handleAuditPolicyEffective(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	agentID := agentIDFromPath(r.URL.Path, "/api/audit/policy/effective/")
	if agentID == "" {
		httpErr(w, http.StatusBadRequest, "agent_id_required")
		return
	}
	pol := audit.CurrentEffectivePolicy(agentID)
	if pol == nil {
		httpErr(w, http.StatusInternalServerError, "pipeline_not_initialized")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pol)
}

func handleAuditAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	agents, err := audit.Agents()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, M{"agents": agents})
}

func parseQueryOpts(r *http.Request) audit.QueryOpts {
	q := r.URL.Query()
	opts := audit.QueryOpts{
		AgentID:   strings.TrimSpace(q.Get("agent_id")),
		Direction: strings.TrimSpace(q.Get("direction")),
	}
	if v := strings.TrimSpace(q.Get("from")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			opts.From = t
		}
	}
	if v := strings.TrimSpace(q.Get("to")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			opts.To = t
		}
	}
	if v := q.Get("severity"); v != "" {
		opts.Severities = audit.SeveritiesFromCSV(v)
	}
	if v := q.Get("rule_id"); v != "" {
		for _, r := range strings.Split(v, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				opts.RuleIDs = append(opts.RuleIDs, r)
			}
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.Offset = n
		}
	}
	return opts
}
