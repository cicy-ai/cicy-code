package main

// HTTP endpoints for the Policy Agent (Phase 4).
//
//   GET  /api/audit/policy/suggestions          → list current suggestions
//   POST /api/audit/policy/suggestions/generate → kick off one LLM pass
//   POST /api/audit/policy/suggestions/apply    → body: {"id": "..."}, merges patch into policy.json
//   POST /api/audit/policy/suggestions/dismiss  → body: {"id": "..."}, marks status=dismissed
//
// Wiring in main.go: see registerPolicySuggesterRoutes.
//
// UI integration (Phase 4 follow-up): AuditDashboard.tsx adds a
// "Suggestions" tab that calls these endpoints. The PolicyForm.tsx form
// stays unchanged — it's still the operator's direct edit path.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	"ttyd-go/mgr/audit"
)

func registerPolicySuggesterRoutes() {
	http.HandleFunc("/api/audit/policy/suggestions", wa(handlePolicySuggestionsList))
	http.HandleFunc("/api/audit/policy/suggestions/generate", wa(handlePolicySuggestionsGenerate))
	http.HandleFunc("/api/audit/policy/suggestions/apply", wa(handlePolicySuggestionsApply))
	http.HandleFunc("/api/audit/policy/suggestions/dismiss", wa(handlePolicySuggestionsDismiss))
}

func handlePolicySuggestionsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	suggestions, err := audit.LoadPolicySuggestions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(suggestions)
}

func handlePolicySuggestionsGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := loadPolicySuggesterConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Run synchronously so the operator sees errors immediately.
	if err := audit.GeneratePolicySuggestions(r.Context(), cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handlePolicySuggestionsApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	hash, err := audit.ApplySuggestion(body.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "applied", "policy_hash": hash})
}

func handlePolicySuggestionsDismiss(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if err := audit.SetSuggestionStatus(body.ID, "dismissed"); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "dismissed"})
}

// loadPolicySuggesterConfig pulls LLM endpoint + model from the same env
// vars cicy-code already uses for ai_remediation, falling back to the
// CICY_AI_GATEWAY_LLM_* family. Keeping the operator from configuring
// LLM in three different places is more important than schema purity.
func loadPolicySuggesterConfig() (audit.PolicySuggesterConfig, error) {
	cfg := audit.PolicySuggesterConfig{
		Enabled:        true,
		Endpoint:       strings.TrimSpace(os.Getenv("CICY_AI_GATEWAY_LLM_ENDPOINT")),
		Model:          strings.TrimSpace(os.Getenv("CICY_AI_GATEWAY_LLM_MODEL")),
		APIKey:         strings.TrimSpace(os.Getenv("CICY_AI_GATEWAY_LLM_API_KEY")),
		MaxTokens:      2000,
		TimeoutSeconds: 30,
		LookbackHours:  168,
	}
	if cfg.Endpoint == "" {
		// Fall back to ai_remediation env vars if separately configured.
		cfg.Endpoint = strings.TrimSpace(os.Getenv("AUDIT_AI_REMEDIATION_ENDPOINT"))
		cfg.Model = strings.TrimSpace(os.Getenv("AUDIT_AI_REMEDIATION_MODEL"))
		cfg.APIKey = strings.TrimSpace(os.Getenv("AUDIT_AI_REMEDIATION_API_KEY"))
	}
	if cfg.Endpoint == "" || cfg.Model == "" {
		return cfg, errPolicySuggesterUnconfigured
	}
	return cfg, nil
}

var errPolicySuggesterUnconfigured = newPlainError("policy_suggester: set CICY_AI_GATEWAY_LLM_ENDPOINT + CICY_AI_GATEWAY_LLM_MODEL (or AUDIT_AI_REMEDIATION_*)")

func newPlainError(msg string) error {
	return &plainError{msg: msg}
}

type plainError struct{ msg string }

func (p *plainError) Error() string { return p.msg }

// silence "unused import" when log is included but no log call lands here.
var _ = log.Printf
// silence "unused import" if context not used in trimmed wiring.
var _ = context.Background
