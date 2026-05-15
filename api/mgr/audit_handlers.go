package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ttyd-go/mgr/audit"
)

// Routes registered in main.go:
//   GET /api/audit/events                — list events with filters
//   GET /api/audit/events/{id}           — single event detail
//   GET /api/audit/stats                 — aggregations
//   GET /api/audit/agents                — agents that have any events

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
