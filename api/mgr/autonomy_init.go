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
