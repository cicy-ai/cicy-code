package main

// startAutonomy wires the audit-v2 autonomous policy agent into the
// server startup sequence. Disabled by default — operator opts in by
// creating ~/cicy-ai/autonomy/autonomy.json with {"enabled": true, ...}.
//
// HTTP surface:
//
//   GET  /api/audit/decisions      — recent agent decisions (forensic view)
//   POST /api/audit/decisions/run  — synchronous "act now" trigger

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"ttyd-go/mgr/audit"
)

func startAutonomy() {
	cfg, err := audit.LoadAutonomyConfig("")
	if err != nil {
		log.Printf("[autonomy] config load failed (disabled): %v", err)
		return
	}
	audit.StartAutonomy(context.Background(), cfg)

	http.HandleFunc("/api/audit/decisions", wa(handleAutonomyDecisions))
	http.HandleFunc("/api/audit/decisions/run", wa(handleAutonomyRunNow))
	http.HandleFunc("/api/audit/decisions/explain/", wa(handleAutonomyExplain))
	http.HandleFunc("/api/audit/decisions/revert/", wa(handleAutonomyRevert))
	http.HandleFunc("/api/audit/decisions/", wa(handleAutonomyDecisionByID))
}

func handleAutonomyDecisions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 100
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}
	decisions := audit.ReadDecisions(limit)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"decisions": decisions,
		"count":     len(decisions),
	})
}

func handleAutonomyRunNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	dec := audit.RunOneTickNow(r.Context(), "manual")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dec)
}

// handleAutonomyExplain — POST /api/audit/decisions/explain/<id>
// Returns a structured human-readable narrative of a past decision.
func handleAutonomyExplain(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/audit/decisions/explain/"):]
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	result, err := audit.ExplainDecision(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// handleAutonomyDecisionByID — GET /api/audit/decisions/<id>
// Returns a single decision by ID for deep-linking from the UI.
// Routed via the trailing-slash path. Skip the prefixes that are
// served by their own handlers (run / explain/ / revert/).
func handleAutonomyDecisionByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Path[len("/api/audit/decisions/"):]
	if id == "" {
		// Fall back to the list handler to avoid breaking
		// /api/audit/decisions/  (trailing slash, no id).
		handleAutonomyDecisions(w, r)
		return
	}
	dec := audit.ReadDecisionByID(id)
	if dec == nil {
		http.Error(w, "decision "+id+" not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dec)
}

// handleAutonomyRevert — POST /api/audit/decisions/revert/<id>
// Reverts a specific decision via `git revert` in the audit dir. fsnotify
// in the audit pipeline picks up the resulting policy.json change.
func handleAutonomyRevert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Path[len("/api/audit/decisions/revert/"):]
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	result, err := audit.RevertDecision(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
