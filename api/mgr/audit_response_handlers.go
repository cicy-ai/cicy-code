package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"ttyd-go/mgr/audit"
)

// handleAuditReadiness — GET /api/audit/readiness. Snapshot of whether the
// incident-response chain is wired end to end (owner configured? mail
// deliverable? IM bound? preventive on? AI研判 on?). w-10000 calls this on
// startup to体检 and surface the gaps to the operator.
func handleAuditReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	J(w, audit.GetResponseReadiness())
}

// handleAuditNotify — POST /api/audit/notify {event_id, note}. Escalates an
// event to its responsible person(s) by email. Called by w-10000 (via
// `cicy-policy notify`) when it decides a finding warrants human attention.
// note = the advisor's own assessment, prepended to the email.
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
