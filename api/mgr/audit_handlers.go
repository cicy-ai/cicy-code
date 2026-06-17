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

// handleAuditTriage adjudicates one alert (real leak vs false positive) using
// the configured LLM, falling back to a deterministic heuristic. POST body is
// an audit.TriageInput (the enriched finding the UI already holds).
func handleAuditTriage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var in audit.TriageInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	J(w, audit.TriageFinding(r.Context(), in))
}

// handleAuditSnapshot returns the redacted, plaintext request snapshot archived
// for a notify alert (meta.snapshot_ref). Secrets are already masked, so this
// is safe to read for review / compliance.
func handleAuditSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		httpErr(w, http.StatusBadRequest, "missing_ref")
		return
	}
	data, err := audit.ReadSnapshot(ref)
	if err != nil {
		httpErr(w, http.StatusNotFound, "snapshot_not_found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

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

// handleAuditRules returns the builtin rule catalog (secret/PII detectors +
// behaviour rules) so the UI can show what the pipeline enforces out of the box.
func handleAuditRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	// When the policy is config-managed the builtins have been materialized into
	// custom_rules, so the hardcoded catalog would only duplicate them. Return an
	// empty catalog — every rule is shown (and edited) from the config instead.
	if audit.CurrentPolicyManaged() {
		J(w, M{"rules": []any{}})
		return
	}
	J(w, M{"rules": audit.RuleCatalog()})
}

// handleAuditRulesTest runs a matcher (regex|js) against sample text so the UI
// can let authors verify a rule before saving.
//
//	POST /api/audit/rules/test  { "match_type": "regex|js", "pattern": "...", "text": "..." }
//	→ { "matches": [{start,end,preview}], "count": N }  or  { "error": "..." }
func handleAuditRulesTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req struct {
		MatchType string `json:"match_type"`
		Pattern   string `json:"pattern"`
		Text      string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, http.StatusBadRequest, "bad_json")
		return
	}
	spans, err := audit.TestRuleMatcher(req.MatchType, req.Pattern, req.Text)
	if err != nil {
		J(w, M{"error": err.Error(), "matches": []any{}, "count": 0})
		return
	}
	J(w, M{"matches": spans, "count": len(spans)})
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
//	  "agent_id":         "w-1001",                  // REQUIRED
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

// handleAuditAllowlist lists or removes allow_list entries.
//
//	GET    /api/audit/allowlist
//	  → { "content_hashes": [...], "paths": [...], "agents": [...] }
//	DELETE /api/audit/allowlist
//	  body: { "category": "content_hash"|"path"|"agent", "value": "..." }
//	  → 204 (removed or already absent); 400 bad input; 500 disk error
//
// The add side stays at POST /api/audit/allowlist/content (content hashes
// are the only thing the FP button writes); removal here covers all three
// buckets so the management UI can clear any entry.
func handleAuditAllowlist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		p, err := audit.LoadPolicy(audit.DefaultPolicyPath())
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{
			"content_hashes": p.AllowList.ContentHashes,
			"paths":          p.AllowList.Paths,
			"agents":         p.AllowList.Agents,
		})
	case http.MethodDelete:
		var req struct {
			Category string `json:"category"`
			Value    string `json:"value"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid_json")
			return
		}
		var cat audit.AllowListCategory
		switch req.Category {
		case "content_hash", "content_hashes":
			cat = audit.AllowCategoryContentHash
		case "path", "paths":
			cat = audit.AllowCategoryPath
		case "agent", "agents":
			cat = audit.AllowCategoryAgent
		default:
			httpErr(w, http.StatusBadRequest, "unknown_category")
			return
		}
		path, err := audit.RemoveFromAllowList(cat, req.Value)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if path == "" {
			log.Printf("[audit] allowlist remove no-op (absent): cat=%s val=%s", req.Category, req.Value)
		} else {
			log.Printf("[audit] allowlist removed cat=%s val=%s written=%s", req.Category, req.Value, path)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
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

// ackLogoSVG is the cicy four-point-star mark (brand-blue gradient), inlined so
// the ack page is fully self-contained.
const ackLogoSVG = `<svg width="38" height="38" viewBox="0 0 96 96" fill="none" xmlns="http://www.w3.org/2000/svg" style="margin-bottom:20px;"><defs><linearGradient id="m" x1="16" y1="12" x2="80" y2="84" gradientUnits="userSpaceOnUse"><stop stop-color="#60A5FA"/><stop offset=".55" stop-color="#2563EB"/><stop offset="1" stop-color="#1E3A8A"/></linearGradient></defs><path d="M48 11L39.5 33.3L16 29.5L31 48L16 66.5L39.5 62.7L48 85L56.5 62.7L80 66.5L65 48L80 29.5L56.5 33.3Z" fill="url(#m)" stroke="url(#m)" stroke-width="8" stroke-linejoin="round" stroke-linecap="round"/></svg>`

func ackHTML(w http.ResponseWriter, status int, errMsg, eventID string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	var accent, icon, iconBg, iconBorder, title, sub, card string
	if errMsg != "" {
		accent = "linear-gradient(90deg,#f59e0b,#d97706)"
		icon, iconBg, iconBorder = "⏳", "#fffbeb", "#fde68a"
		title = "确认链接无法使用"
		sub = "该链接可能已过期(有效期 30 天)、已被使用或无效。<br>请登录审计台直接处理该告警。"
		card = `<div style="background:#fff7ed;border:1px solid #fed7aa;border-radius:10px;padding:12px 16px;text-align:left;color:#9a3412;font-size:12px;word-break:break-all;">` + escapeHTML(errMsg) + `</div>`
	} else {
		accent = "linear-gradient(90deg,#22c55e,#16a34a)"
		icon, iconBg, iconBorder = "✓", "#ecfdf5", "#bbf7d0"
		title = "告警已确认"
		sub = `此告警已在审计台标记为<b style="color:#16a34a;">「已处理」</b>。<br>感谢你的及时处置,可关闭本页面。`
		card = `<div style="background:#f8fafc;border:1px solid #eef0f4;border-radius:10px;padding:13px 16px;text-align:left;"><div style="color:#94a3b8;font-size:11px;margin-bottom:4px;">事件 ID</div><div style="color:#475569;font-size:12px;font-family:ui-monospace,Menlo,Consolas,monospace;word-break:break-all;">` + escapeHTML(eventID) + `</div></div>`
	}

	page := `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>CiCy Code 审计 · 确认告警</title></head>` +
		`<body style="margin:0;min-height:100vh;background:radial-gradient(1200px 600px at 50% -10%,#1e293b 0%,#0b1120 60%);font-family:-apple-system,'Segoe UI',Roboto,'PingFang SC','Microsoft YaHei',Arial,sans-serif;display:flex;align-items:center;justify-content:center;padding:48px 16px;box-sizing:border-box;">` +
		`<table role="presentation" cellpadding="0" cellspacing="0" width="440" style="width:440px;max-width:100%;background:#fff;border-radius:18px;box-shadow:0 20px 60px rgba(0,0,0,.45);overflow:hidden;">` +
		`<tr><td style="height:5px;background:` + accent + `;"></td></tr>` +
		`<tr><td style="padding:40px 36px 30px;text-align:center;">` + ackLogoSVG +
		`<div style="width:72px;height:72px;margin:0 auto 20px;border-radius:50%;background:` + iconBg + `;border:1px solid ` + iconBorder + `;line-height:72px;font-size:34px;">` + icon + `</div>` +
		`<div style="color:#0f172a;font-size:21px;font-weight:700;margin-bottom:8px;">` + title + `</div>` +
		`<p style="margin:0 0 22px;color:#64748b;font-size:14px;line-height:1.7;">` + sub + `</p>` + card +
		`</td></tr>` +
		`<tr><td style="padding:14px 36px 24px;border-top:1px solid #f1f5f9;text-align:center;"><span style="color:#94a3b8;font-size:11px;">CiCy Code 审计 · 登录审计台可查看完整事件详情</span></td></tr>` +
		`</table></body></html>`
	_, _ = w.Write([]byte(page))
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
