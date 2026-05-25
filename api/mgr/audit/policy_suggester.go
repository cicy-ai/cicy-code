package audit

// Policy Agent — LLM-driven policy tuning. Reads recent audit events +
// current effective policy, asks an LLM what would help (lower-FP rules,
// new custom rules, allow_list entries), writes suggestions to disk for
// human review. Operators apply / dismiss via the audit dashboard.
//
// Design: docs/v1/mitm-system-design.md §6 Phase 4 + decision §8.5
// (humans review, agent never writes policy.json directly).
//
// Reuses the LLM transport from ai_remediation.go (same OpenAI-compatible
// /chat/completions contract).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	suggesterDefaultLookbackHours = 168 // 7 days
	suggesterMaxEvents            = 500
	suggesterMaxSuggestions       = 8
)

// Suggestion is one proposed policy change. Persisted in
// ~/cicy-ai/audit/policy.suggestions.json.
type Suggestion struct {
	ID                 string    `json:"id"`
	Kind               string    `json:"kind"`         // allow_list | rule_override | custom_rule | preventive_toggle
	Severity           string    `json:"severity"`     // safe | moderate | dangerous
	Title              string    `json:"title"`
	Rationale          string    `json:"rationale"`
	SupportingEventIDs []string  `json:"supporting_event_ids"`
	Patch              PolicyPatch `json:"patch"`
	Status             string    `json:"status"` // open | applied | dismissed
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// PolicyPatch is a typed sub-set of Policy fields the LLM may suggest
// changes to. We deliberately do NOT use JSON-patch — the surface is small
// enough that typed shapes catch malformed LLM output at unmarshal time.
type PolicyPatch struct {
	RulesOverride []RuleOverride `json:"rules_override,omitempty"`
	CustomRules   []CustomRule   `json:"custom_rules,omitempty"`
	AllowList     *AllowList     `json:"allow_list,omitempty"`
	Preventive    *PreventiveConfig `json:"preventive,omitempty"`
}

// SuggestionsFile is the on-disk envelope.
type SuggestionsFile struct {
	Version            int          `json:"version"`
	GeneratedAt        time.Time    `json:"generated_at"`
	BasedOnEventsFrom  time.Time    `json:"based_on_events_from"`
	BasedOnEventsTo    time.Time    `json:"based_on_events_to"`
	Suggestions        []Suggestion `json:"suggestions"`
}

const policySuggesterSystemPrompt = `You are a senior security policy advisor for an AI request gateway.
You receive (a) the current policy summary, and (b) aggregate statistics
from the last N days of audit events. Your job: propose a small number of
specific changes that will reduce false positives, catch what's currently
missing, or harden weak spots.

Return ONLY a JSON object with this shape — no prose, no markdown:

{
  "suggestions": [
    {
      "kind": "rule_override" | "allow_list" | "custom_rule" | "preventive_toggle",
      "severity": "safe" | "moderate" | "dangerous",
      "title": "<one-line summary>",
      "rationale": "<2-3 sentences explaining the evidence>",
      "supporting_event_ids": ["evt-xxx", "evt-yyy"],
      "patch": { <typed policy sub-object — see Policy schema> }
    }
  ]
}

Constraints:
  - At most 8 suggestions, ordered by impact.
  - "safe" = lowering FP severity, disabling a clearly broken rule,
    adding a narrow allow_list entry for an established internal path.
  - "moderate" = new custom_rule, broader allow_list, severity-up.
  - "dangerous" = enabling preventive.block, changing incident_response,
    or anything that could break production traffic.
  - You will NEVER receive raw payloads. Reason about rule_id + counts.
  - If you have no high-confidence suggestions, return {"suggestions": []}.`

// PolicySuggesterConfig wires the same /chat/completions transport as
// ai_remediation. Reuse the operator's existing API key configuration.
type PolicySuggesterConfig struct {
	Enabled        bool   `json:"enabled"`
	Endpoint       string `json:"endpoint,omitempty"`
	Model          string `json:"model,omitempty"`
	APIKey         string `json:"api_key,omitempty"`
	MaxTokens      int    `json:"max_tokens,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	LookbackHours  int    `json:"lookback_hours,omitempty"`
}

// GeneratePolicySuggestions runs one pass: aggregates events, calls LLM,
// writes the suggestions file atomically. Idempotent in a sense — calling
// it again overwrites the file with a fresh batch. Previously-applied or
// dismissed suggestions are preserved by ID and re-merged.
func GeneratePolicySuggestions(ctx context.Context, cfg PolicySuggesterConfig) error {
	if !cfg.Enabled {
		return fmt.Errorf("policy_suggester disabled")
	}
	if cfg.Endpoint == "" || cfg.Model == "" {
		return fmt.Errorf("policy_suggester endpoint/model not set")
	}
	if cfg.LookbackHours <= 0 {
		cfg.LookbackHours = suggesterDefaultLookbackHours
	}

	to := time.Now().UTC()
	from := to.Add(-time.Duration(cfg.LookbackHours) * time.Hour)
	qr, err := Query(QueryOpts{From: from, To: to, Limit: suggesterMaxEvents})
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}
	stats := buildSuggesterStats(qr.Events)

	pol := globalPipeline.CurrentPolicy()
	if pol == nil {
		return fmt.Errorf("no active policy")
	}
	policySummary := summarizePolicyForSuggester(pol)

	userMsg, err := json.Marshal(map[string]interface{}{
		"current_policy_summary": policySummary,
		"event_stats":            stats,
		"window_from":            from.Format(time.RFC3339),
		"window_to":              to.Format(time.RFC3339),
	})
	if err != nil {
		return err
	}

	raw, err := callPolicySuggesterLLM(ctx, cfg, string(userMsg))
	if err != nil {
		return fmt.Errorf("llm call: %w", err)
	}

	parsed, err := parseSuggesterResponse(raw)
	if err != nil {
		return fmt.Errorf("parse llm response: %w", err)
	}

	// Stamp IDs + timestamps for any new suggestions.
	now := time.Now().UTC()
	for i := range parsed {
		if parsed[i].ID == "" {
			parsed[i].ID = "sg-" + uuid.NewString()
		}
		parsed[i].Status = "open"
		parsed[i].CreatedAt = now
		parsed[i].UpdatedAt = now
	}

	if err := mergeAndWriteSuggestions(parsed, from, to); err != nil {
		return fmt.Errorf("write suggestions: %w", err)
	}
	log.Printf("[audit] policy_suggester wrote %d suggestions (window %s..%s)", len(parsed), from.Format(time.RFC3339), to.Format(time.RFC3339))
	return nil
}

// --- stats / summary ---

type suggesterStats struct {
	WindowEvents int                    `json:"window_events"`
	RuleHits     map[string]ruleHitStat `json:"rule_hits"`
	AgentHits    map[string]int         `json:"agent_hits"`
	ProviderHits map[string]int         `json:"provider_hits"`
	ActionCounts map[string]int         `json:"action_counts"`
}

type ruleHitStat struct {
	Count          int            `json:"count"`
	BySeverity     map[string]int `json:"by_severity"`
	ByAgent        map[string]int `json:"by_agent"`
	SampleEventIDs []string       `json:"sample_event_ids"`
}

func buildSuggesterStats(events []Event) suggesterStats {
	s := suggesterStats{
		RuleHits:     map[string]ruleHitStat{},
		AgentHits:    map[string]int{},
		ProviderHits: map[string]int{},
		ActionCounts: map[string]int{},
	}
	s.WindowEvents = len(events)
	for _, e := range events {
		if e.Identity.AgentID != "" {
			s.AgentHits[e.Identity.AgentID]++
		}
		if e.Subject.Provider != "" {
			s.ProviderHits[e.Subject.Provider]++
		}
		if e.Decision.Action != "" {
			s.ActionCounts[string(e.Decision.Action)]++
		}
		for _, f := range e.Findings {
			hit := s.RuleHits[f.RuleID]
			if hit.BySeverity == nil {
				hit.BySeverity = map[string]int{}
				hit.ByAgent = map[string]int{}
			}
			hit.Count++
			hit.BySeverity[string(f.Severity)]++
			if e.Identity.AgentID != "" {
				hit.ByAgent[e.Identity.AgentID]++
			}
			if len(hit.SampleEventIDs) < 5 {
				hit.SampleEventIDs = append(hit.SampleEventIDs, e.ID)
			}
			s.RuleHits[f.RuleID] = hit
		}
	}
	return s
}

func summarizePolicyForSuggester(p *Policy) map[string]interface{} {
	out := map[string]interface{}{
		"version":   p.Version,
		"enabled":   p.Enabled,
		"fail_mode": p.FailMode,
		"hash":      p.Hash,
	}
	if len(p.RulesOverride) > 0 {
		ovs := []map[string]interface{}{}
		for _, ov := range p.RulesOverride {
			ovs = append(ovs, map[string]interface{}{
				"id":             ov.ID,
				"disabled":       ov.Disabled,
				"severity":       ov.Severity,
				"default_action": ov.DefaultAction,
			})
		}
		out["rules_override"] = ovs
	}
	if len(p.CustomRules) > 0 {
		rules := []map[string]interface{}{}
		for _, r := range p.CustomRules {
			rules = append(rules, map[string]interface{}{
				"id":            r.ID,
				"category":      r.Category,
				"severity":      r.Severity,
				"scan_directions": r.ScanDirections,
				"inline":        r.Inline,
			})
		}
		out["custom_rules"] = rules
	}
	out["preventive_enabled"] = p.Preventive.Enabled
	return out
}

// --- LLM transport ---

func callPolicySuggesterLLM(ctx context.Context, cfg PolicySuggesterConfig, userMsg string) (string, error) {
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2000
	}
	timeoutSec := cfg.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 30
	}

	chatReq := map[string]interface{}{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": policySuggesterSystemPrompt},
			{"role": "user", "content": userMsg},
		},
		"max_tokens":  maxTokens,
		"temperature": 0.2,
	}
	bodyBytes, _ := json.Marshal(chatReq)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	resp, err := defaultAIHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("llm status %d: %s", resp.StatusCode, string(preview))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshal chat response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}
	return chatResp.Choices[0].Message.Content, nil
}

// parseSuggesterResponse tolerates surrounding markdown / explanation by
// finding the first { and last } and unmarshalling the slice between.
func parseSuggesterResponse(raw string) ([]Suggestion, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in response")
	}
	var envelope struct {
		Suggestions []Suggestion `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Suggestions) > suggesterMaxSuggestions {
		envelope.Suggestions = envelope.Suggestions[:suggesterMaxSuggestions]
	}
	return envelope.Suggestions, nil
}

// --- persistence ---

var suggesterMu sync.Mutex

func policySuggestionsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cicy-ai", "audit", "policy.suggestions.json"), nil
}

// LoadPolicySuggestions reads the suggestions file. Returns an empty
// envelope (not an error) if the file doesn't exist.
func LoadPolicySuggestions() (*SuggestionsFile, error) {
	suggesterMu.Lock()
	defer suggesterMu.Unlock()
	return loadPolicySuggestionsLocked()
}

func loadPolicySuggestionsLocked() (*SuggestionsFile, error) {
	path, err := policySuggestionsPath()
	if err != nil {
		return nil, err
	}
	out := &SuggestionsFile{Version: 1, Suggestions: []Suggestion{}}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return nil, err
	}
	return out, nil
}

// mergeAndWriteSuggestions takes a fresh batch and merges with any prior
// applied/dismissed entries (preserved by ID + status). New entries are
// appended; duplicates by ID overwrite.
func mergeAndWriteSuggestions(fresh []Suggestion, from, to time.Time) error {
	suggesterMu.Lock()
	defer suggesterMu.Unlock()

	prior, err := loadPolicySuggestionsLocked()
	if err != nil {
		return err
	}
	byID := map[string]Suggestion{}
	for _, s := range prior.Suggestions {
		// Preserve human decisions; drop stale "open" entries that the new
		// pass didn't reaffirm.
		if s.Status == "applied" || s.Status == "dismissed" {
			byID[s.ID] = s
		}
	}
	for _, s := range fresh {
		byID[s.ID] = s
	}
	merged := make([]Suggestion, 0, len(byID))
	for _, s := range byID {
		merged = append(merged, s)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].CreatedAt.After(merged[j].CreatedAt)
	})

	out := SuggestionsFile{
		Version:           1,
		GeneratedAt:       time.Now().UTC(),
		BasedOnEventsFrom: from,
		BasedOnEventsTo:   to,
		Suggestions:       merged,
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	path, _ := policySuggestionsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SetSuggestionStatus marks one suggestion by ID. Used by dismiss + apply.
func SetSuggestionStatus(id, status string) error {
	if status != "applied" && status != "dismissed" && status != "open" {
		return fmt.Errorf("invalid status %q", status)
	}
	suggesterMu.Lock()
	defer suggesterMu.Unlock()
	prior, err := loadPolicySuggestionsLocked()
	if err != nil {
		return err
	}
	found := false
	for i := range prior.Suggestions {
		if prior.Suggestions[i].ID == id {
			prior.Suggestions[i].Status = status
			prior.Suggestions[i].UpdatedAt = time.Now().UTC()
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("suggestion %s not found", id)
	}
	body, err := json.MarshalIndent(prior, "", "  ")
	if err != nil {
		return err
	}
	path, _ := policySuggestionsPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LookupSuggestion fetches one by ID; nil + error if not found.
func LookupSuggestion(id string) (*Suggestion, error) {
	prior, err := LoadPolicySuggestions()
	if err != nil {
		return nil, err
	}
	for i := range prior.Suggestions {
		if prior.Suggestions[i].ID == id {
			s := prior.Suggestions[i]
			return &s, nil
		}
	}
	return nil, fmt.Errorf("suggestion %s not found", id)
}
