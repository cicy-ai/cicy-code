package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"ttyd-go/mgr/audit"
)

// handleAuditReadiness — GET /api/audit/readiness. Snapshot of whether the
// incident-response chain is wired end to end (owner configured? mail
// deliverable? IM bound? preventive on? AI研判 on?). The 审核策略专员 (the
// audit advisor, or an operator) calls this to体检 and surface the gaps.
func handleAuditReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	J(w, audit.GetResponseReadiness())
}

// handleAuditNotify — POST /api/audit/notify {event_id, note}. Escalates an
// event to its responsible person(s) by email. Called by the 审核策略专员 when
// it decides a finding warrants extra human attention (the auto owner-alert
// already fired at hit time). note = the advisor's assessment, prepended.
func handleAuditNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req struct {
		EventID string `json:"event_id"`
		Note    string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid_json")
		return
	}
	eventID := strings.TrimSpace(req.EventID)
	if eventID == "" {
		httpErr(w, http.StatusBadRequest, "event_id_required")
		return
	}
	if err := audit.SendOwnerIncidentByID(eventID, req.Note); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, M{"ok": true, "event_id": eventID})
}

// handleAuditChannelsTest — POST /api/audit/channels/test {to}. Sends a
// synthetic test alert through the active channels (email + WeChat if bound)
// so the operator can confirm delivery without a real finding. Used when
// setting up / verifying notification channels.
func handleAuditChannelsTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var req struct {
		To string `json:"to"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	summary, err := audit.SendTestNotificationGlobal(req.To)
	if err != nil {
		J(w, M{"ok": false, "summary": summary, "error": err.Error()})
		return
	}
	J(w, M{"ok": true, "summary": summary})
}

// handleWeChatBindPrompt — POST /api/im/wechat/prompt. Broadcasts a
// `wechat_bind_request` chat-WS event to all connected UI clients so the
// frontend pops the WeChat bind (QR-scan) modal — anywhere the operator is.
// Lets an agent pull up the modal in the browser instead of printing a QR
// link in its terminal.
func handleWeChatBindPrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	hub.broadcastAll(ChatEvent{Type: "wechat_bind_request"})
	J(w, M{"ok": true})
}
