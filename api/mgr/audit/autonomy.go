package audit

// Autonomous policy agent — Phase 6.
//
// Design philosophy (per user direction 2026-05-25):
//
//   "no human configuration, same as: no human writing code"
//
// Humans do NOT author policy.json. Humans author one short
// ~/cicy-ai/autonomy/autonomy.json file expressing high-level constraints
// (max changes/hour, forbidden actions, lookback window). Within those
// constraints the agent runs on a timer, looks at audit events, decides
// what changes the policy needs, and APPLIES THEM directly.
//
// Every decision is appended to ~/cicy-ai/autonomy/decisions.ndjson so
// the operator can audit the agent itself (the "report to human" channel
// — same as commit messages for AI-written code).
//
// Cooperation with the rest of the audit pipeline:
//   - Reads policy.json (via globalPipeline.CurrentPolicy)
//   - Writes policy.json (via WriteGlobalPolicy → fsnotify reload)
//   - Reads events (via Query)
//   - Writes nothing else.

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
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// AutonomyConfig is the human-authored guardrails file. Missing fields
// fall back to safe defaults. Set Enabled=true to opt in.
type AutonomyConfig struct {
	Enabled          bool          `json:"enabled"`
	Interval         JSONDuration  `json:"interval"`            // default 10m
	Lookback         JSONDuration  `json:"lookback"`            // default 24h
	MaxChangesPerHour int          `json:"max_changes_per_hour"` // default 5
	MaxChangesPerTick int          `json:"max_changes_per_tick"` // default 3
	ForbiddenActions []string      `json:"forbidden_actions"`   // e.g. "enable_preventive_block"
	LLM              AutonomyLLM   `json:"llm"`
}

// AutonomyLLM is the LLM transport config. Endpoint/Model required.
// APIKey defaults to env $AUTONOMY_LLM_API_KEY then $CICY_AI_GATEWAY_LLM_API_KEY.
type AutonomyLLM struct {
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key,omitempty"`
}

// JSONDuration is a JSON-friendly time.Duration (marshals as "10m" / "24h").
type JSONDuration time.Duration

func (d JSONDuration) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Duration(d).String() + `"`), nil
}

func (d *JSONDuration) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("autonomy: invalid duration %q: %w", s, err)
	}
	*d = JSONDuration(parsed)
	return nil
}

// LoadAutonomyConfig reads ~/cicy-ai/autonomy/autonomy.json (or the path
// given), fills defaults, and applies env-var fallback for the API key.
func LoadAutonomyConfig(path string) (*AutonomyConfig, error) {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, "cicy-ai", "autonomy", "autonomy.json")
	}
	cfg := &AutonomyConfig{}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.applyDefaults()
			return cfg, nil
		}
		return nil, fmt.Errorf("autonomy: read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("autonomy: parse: %w", err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *AutonomyConfig) applyDefaults() {
	if c.Interval == 0 {
		c.Interval = JSONDuration(10 * time.Minute)
	}
	if c.Lookback == 0 {
		c.Lookback = JSONDuration(24 * time.Hour)
	}
	if c.MaxChangesPerHour == 0 {
		c.MaxChangesPerHour = 5
	}
	if c.MaxChangesPerTick == 0 {
		c.MaxChangesPerTick = 3
	}
	if c.LLM.APIKey == "" {
		if v := os.Getenv("AUTONOMY_LLM_API_KEY"); v != "" {
			c.LLM.APIKey = v
		} else if v := os.Getenv("CICY_AI_GATEWAY_LLM_API_KEY"); v != "" {
			c.LLM.APIKey = v
		}
	}
	if c.LLM.Endpoint == "" {
		c.LLM.Endpoint = os.Getenv("AUTONOMY_LLM_ENDPOINT")
		if c.LLM.Endpoint == "" {
			c.LLM.Endpoint = os.Getenv("CICY_AI_GATEWAY_LLM_ENDPOINT")
		}
	}
	if c.LLM.Model == "" {
		c.LLM.Model = os.Getenv("AUTONOMY_LLM_MODEL")
		if c.LLM.Model == "" {
			c.LLM.Model = "deepseek-v4-pro"
		}
	}
}

// AutonomyDecision is one append-only record describing what the agent did at
// one tick. Persisted as one JSON line in decisions.ndjson.
type AutonomyDecision struct {
	ID                string             `json:"id"`
	Timestamp         time.Time          `json:"timestamp"`
	Trigger           string             `json:"trigger"` // "interval" | "manual"
	EventsWindowFrom  time.Time          `json:"events_window_from"`
	EventsWindowTo    time.Time          `json:"events_window_to"`
	EventsConsidered  int                `json:"events_considered"`
	LLMResponseText   string             `json:"llm_response_text,omitempty"`
	Actions           []AutonomyDecisionAction   `json:"actions"`
	PolicyHashBefore  string             `json:"policy_hash_before"`
	PolicyHashAfter   string             `json:"policy_hash_after,omitempty"`
	Error             string             `json:"error,omitempty"`
}

// AutonomyDecisionAction is one component of a tick — either applied or skipped.
type AutonomyDecisionAction struct {
	Kind          string      `json:"kind"`
	Patch         PolicyPatch `json:"patch"`
	Rationale     string      `json:"rationale"`
	Applied       bool        `json:"applied"`
	SkippedReason string      `json:"skipped_reason,omitempty"`
}

// PolicyPatch is the same shape used elsewhere. Defined here to avoid
// cross-file ordering dependency surprises.
type PolicyPatch struct {
	RulesOverride []RuleOverride    `json:"rules_override,omitempty"`
	CustomRules   []CustomRule      `json:"custom_rules,omitempty"`
	AllowList     *AllowList        `json:"allow_list,omitempty"`
	Preventive    *PreventiveConfig `json:"preventive,omitempty"`
}

// StartAutonomy spawns the autonomous agent goroutine if cfg.Enabled.
// Idempotent — calling twice is a no-op.
var (
	autonomyOnce sync.Once
	autonomyCfg  *AutonomyConfig
)

func StartAutonomy(ctx context.Context, cfg *AutonomyConfig) {
	if cfg == nil || !cfg.Enabled {
		log.Printf("[autonomy] disabled")
		return
	}
	if cfg.LLM.Endpoint == "" || cfg.LLM.Model == "" {
		log.Printf("[autonomy] LLM not configured (need endpoint+model) — disabled")
		return
	}
	autonomyOnce.Do(func() {
		autonomyCfg = cfg
		log.Printf("[autonomy] starting: interval=%s lookback=%s max/hr=%d max/tick=%d model=%s",
			time.Duration(cfg.Interval), time.Duration(cfg.Lookback),
			cfg.MaxChangesPerHour, cfg.MaxChangesPerTick, cfg.LLM.Model)
		go autonomyLoop(ctx, cfg)
	})
}

func autonomyLoop(ctx context.Context, cfg *AutonomyConfig) {
	// First tick after a short delay so audit pipeline finishes initializing.
	select {
	case <-time.After(30 * time.Second):
	case <-ctx.Done():
		return
	}
	ticker := time.NewTicker(time.Duration(cfg.Interval))
	defer ticker.Stop()

	for {
		runOneTick(ctx, cfg, "interval")
		select {
		case <-ctx.Done():
			log.Printf("[autonomy] stopped")
			return
		case <-ticker.C:
		}
	}
}

// RunOneTickNow runs a single tick synchronously. Wired to a CLI/HTTP
// trigger for "act now" operator commands.
func RunOneTickNow(ctx context.Context, trigger string) AutonomyDecision {
	cfg := autonomyCfg
	if cfg == nil {
		return AutonomyDecision{
			ID:        "dec-" + uuid.NewString(),
			Timestamp: time.Now().UTC(),
			Trigger:   trigger,
			Error:     "autonomy not started",
		}
	}
	return runOneTick(ctx, cfg, trigger)
}

func runOneTick(ctx context.Context, cfg *AutonomyConfig, trigger string) AutonomyDecision {
	dec := AutonomyDecision{
		ID:        "dec-" + uuid.NewString(),
		Timestamp: time.Now().UTC(),
		Trigger:   trigger,
	}

	// Hourly rate limit — drop the tick entirely if recent decisions
	// already exceeded the per-hour cap.
	recent := recentDecisionsCount(time.Hour)
	if recent >= cfg.MaxChangesPerHour {
		dec.Error = fmt.Sprintf("rate limited (%d/%d in last hour)", recent, cfg.MaxChangesPerHour)
		appendDecision(dec)
		log.Printf("[autonomy] tick skipped: %s", dec.Error)
		return dec
	}

	// Snapshot inputs.
	to := time.Now().UTC()
	from := to.Add(-time.Duration(cfg.Lookback))
	dec.EventsWindowFrom = from
	dec.EventsWindowTo = to

	qr, err := Query(QueryOpts{From: from, To: to, Limit: 1000})
	if err != nil {
		dec.Error = "query: " + err.Error()
		appendDecision(dec)
		return dec
	}
	dec.EventsConsidered = len(qr.Events)

	var policySummary map[string]interface{}
	if globalPipeline != nil {
		if pol := globalPipeline.CurrentPolicy(); pol != nil {
			policySummary = summarizePolicyForSuggesterMin(pol)
			dec.PolicyHashBefore = pol.Hash
		}
	}

	stats := buildAutonomyStats(qr.Events)
	if dec.EventsConsidered == 0 {
		// Nothing to learn from. Log a thin no-op decision and move on.
		appendDecision(dec)
		return dec
	}

	respText, err := callAutonomyLLM(ctx, cfg, policySummary, stats, cfg.ForbiddenActions)
	if err != nil {
		dec.Error = "llm: " + err.Error()
		appendDecision(dec)
		log.Printf("[autonomy] llm failed: %v", err)
		return dec
	}
	dec.LLMResponseText = respText

	proposals, err := parseAutonomyResponse(respText)
	if err != nil {
		dec.Error = "parse: " + err.Error()
		appendDecision(dec)
		return dec
	}

	// Apply up to MaxChangesPerTick proposals; skip the rest with reason.
	applied := 0
	for _, p := range proposals {
		action := AutonomyDecisionAction{Kind: p.Kind, Patch: p.Patch, Rationale: p.Rationale}
		if applied >= cfg.MaxChangesPerTick {
			action.SkippedReason = "per_tick_cap"
			dec.Actions = append(dec.Actions, action)
			continue
		}
		if reason := violatesConstraints(p, cfg); reason != "" {
			action.SkippedReason = reason
			dec.Actions = append(dec.Actions, action)
			continue
		}
		if err := applyPatch(p.Patch); err != nil {
			action.SkippedReason = "apply_error: " + err.Error()
			dec.Actions = append(dec.Actions, action)
			continue
		}
		action.Applied = true
		dec.Actions = append(dec.Actions, action)
		applied++
	}

	if globalPipeline != nil {
		if pol := globalPipeline.CurrentPolicy(); pol != nil {
			dec.PolicyHashAfter = pol.Hash
		}
	}
	appendDecision(dec)
	if applied > 0 {
		log.Printf("[autonomy] tick applied %d / proposed %d", applied, len(proposals))
	}
	return dec
}

// --- LLM ---

const autonomySystemPrompt = `You are the autonomous policy administrator for the cicy-code audit system.

Your job: read the recent audit-event statistics + the current policy summary,
and propose targeted changes to policy.json so the system catches more real
incidents and fewer false positives.

You are operating WITHOUT human approval — your output is applied directly to
policy.json, subject only to numeric rate limits and a forbidden-action list.

Return ONLY a JSON object — no markdown, no commentary outside the JSON:

{
  "actions": [
    {
      "kind": "rule_override" | "allow_list" | "custom_rule" | "preventive_toggle",
      "rationale": "one or two sentences citing the event stats",
      "patch": { <typed PolicyPatch — see below> }
    }
  ]
}

PolicyPatch sub-objects:

{
  "rules_override": [
    { "id": "secret.bearer_token", "severity": "low", "default_action": "log" }
  ],
  "custom_rules": [
    { "id": "custom.foo", "severity": "medium", "scan_directions": ["outbound"],
      "default_action": "log",
      "match": { "type": "regex", "pattern": "...", "flags": "i" } }
  ],
  "allow_list": {
    "paths":          ["/internal/dev"],
    "agents":         ["w-10042"],
    "content_hashes": ["sha256:..."]
  },
  "preventive": { "enabled": true }
}

Guidelines (you are still autonomous; these are heuristics, not rules):
  - Lower-severity / allow_list / disable-broken-rule actions are SAFE.
  - New custom rules and broader allow_list need clear evidence.
  - Touching preventive.enabled = true (inline blocking) is the highest
    impact — be conservative and cite ≥3 supporting events.
  - If you have nothing actionable, return {"actions": []}.

You are FORBIDDEN from these actions on this deployment:`

func callAutonomyLLM(ctx context.Context, cfg *AutonomyConfig, policy map[string]interface{}, stats map[string]interface{}, forbidden []string) (string, error) {
	forbiddenList := strings.Join(forbidden, ", ")
	if forbiddenList == "" {
		forbiddenList = "(none)"
	}
	systemMsg := autonomySystemPrompt + " " + forbiddenList

	userBody, _ := json.Marshal(map[string]interface{}{
		"current_policy_summary": policy,
		"event_stats":            stats,
	})

	chatReq := map[string]interface{}{
		"model": cfg.LLM.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemMsg},
			{"role": "user", "content": string(userBody)},
		},
		"max_tokens":  2000,
		"temperature": 0.2,
	}
	bodyBytes, _ := json.Marshal(chatReq)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.LLM.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.LLM.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.LLM.APIKey)
	}

	resp, err := defaultAIHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(preview))
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
		return "", fmt.Errorf("unmarshal: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices")
	}
	return chatResp.Choices[0].Message.Content, nil
}

type autonomyProposal struct {
	Kind      string      `json:"kind"`
	Rationale string      `json:"rationale"`
	Patch     PolicyPatch `json:"patch"`
}

func parseAutonomyResponse(raw string) ([]autonomyProposal, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in response")
	}
	var env struct {
		Actions []autonomyProposal `json:"actions"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &env); err != nil {
		return nil, err
	}
	return env.Actions, nil
}

// --- constraints + apply ---

func violatesConstraints(p autonomyProposal, cfg *AutonomyConfig) string {
	for _, f := range cfg.ForbiddenActions {
		if f == "enable_preventive_block" && p.Patch.Preventive != nil && p.Patch.Preventive.Enabled {
			return "forbidden: enable_preventive_block"
		}
		if f == "custom_rules_add" && len(p.Patch.CustomRules) > 0 {
			return "forbidden: custom_rules_add"
		}
		if f == "rules_override" && len(p.Patch.RulesOverride) > 0 {
			return "forbidden: rules_override"
		}
		if f == "allow_list" && p.Patch.AllowList != nil {
			return "forbidden: allow_list"
		}
	}
	if !hasAnyPatch(&p.Patch) {
		return "empty_patch"
	}
	return ""
}

func hasAnyPatch(p *PolicyPatch) bool {
	if p == nil {
		return false
	}
	if len(p.RulesOverride) > 0 || len(p.CustomRules) > 0 {
		return true
	}
	if p.AllowList != nil && (len(p.AllowList.Paths) > 0 || len(p.AllowList.Agents) > 0 || len(p.AllowList.ContentHashes) > 0) {
		return true
	}
	if p.Preventive != nil {
		return true
	}
	return false
}

// applyPatch loads policy.json, merges patch in, and atomically rewrites
// via WriteGlobalPolicy. Reuses the validate-and-rename path so any
// in-memory pipeline picks the change up via fsnotify within ~200ms.
func applyPatch(patch PolicyPatch) error {
	path := DefaultPolicyPath()
	if path == "" {
		return fmt.Errorf("cannot resolve policy.json path")
	}
	var current map[string]interface{}
	body, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		seed, _ := json.Marshal(DefaultPolicy())
		_ = json.Unmarshal(seed, &current)
	} else {
		if err := json.Unmarshal(body, &current); err != nil {
			return fmt.Errorf("parse current policy: %w", err)
		}
	}
	if current == nil {
		current = map[string]interface{}{}
	}
	mergeAutonomyPatch(current, patch)
	merged, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	_, err = WriteGlobalPolicy(merged)
	return err
}

func mergeAutonomyPatch(current map[string]interface{}, patch PolicyPatch) {
	if len(patch.RulesOverride) > 0 {
		current["rules_override"] = mergeByIDList(
			readMapList(current, "rules_override"),
			marshalList(patch.RulesOverride))
	}
	if len(patch.CustomRules) > 0 {
		current["custom_rules"] = mergeByIDList(
			readMapList(current, "custom_rules"),
			marshalList(patch.CustomRules))
	}
	if patch.AllowList != nil {
		existing, _ := current["allow_list"].(map[string]interface{})
		if existing == nil {
			existing = map[string]interface{}{}
		}
		existing["paths"] = mergeStringSetAuto(existing["paths"], patch.AllowList.Paths)
		existing["content_hashes"] = mergeStringSetAuto(existing["content_hashes"], patch.AllowList.ContentHashes)
		existing["agents"] = mergeStringSetAuto(existing["agents"], patch.AllowList.Agents)
		current["allow_list"] = existing
	}
	if patch.Preventive != nil {
		b, _ := json.Marshal(patch.Preventive)
		var pm map[string]interface{}
		_ = json.Unmarshal(b, &pm)
		existing, _ := current["preventive"].(map[string]interface{})
		if existing == nil {
			existing = map[string]interface{}{}
		}
		for k, v := range pm {
			existing[k] = v
		}
		current["preventive"] = existing
	}
}

func marshalList[T any](items []T) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		b, _ := json.Marshal(it)
		var m map[string]interface{}
		_ = json.Unmarshal(b, &m)
		out = append(out, m)
	}
	return out
}

func readMapList(current map[string]interface{}, key string) []map[string]interface{} {
	raw, ok := current[key]
	if !ok {
		return nil
	}
	items, _ := raw.([]interface{})
	out := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func mergeByIDList(existing, patch []map[string]interface{}) []interface{} {
	byID := map[string]map[string]interface{}{}
	var order []string
	push := func(m map[string]interface{}) {
		id, _ := m["id"].(string)
		if id == "" {
			id = fmt.Sprintf("__noid_%d", len(order))
		}
		if _, exists := byID[id]; !exists {
			order = append(order, id)
		}
		byID[id] = m
	}
	for _, m := range existing {
		push(m)
	}
	for _, m := range patch {
		push(m)
	}
	out := make([]interface{}, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func mergeStringSetAuto(existing interface{}, add []string) []interface{} {
	seen := map[string]bool{}
	var out []interface{}
	if items, ok := existing.([]interface{}); ok {
		for _, v := range items {
			s, _ := v.(string)
			if s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	for _, s := range add {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// --- stats summary used by the LLM ---

func buildAutonomyStats(events []Event) map[string]interface{} {
	ruleHits := map[string]map[string]interface{}{}
	agentHits := map[string]int{}
	providerHits := map[string]int{}
	actionCounts := map[string]int{}
	for _, e := range events {
		if e.Identity.AgentID != "" {
			agentHits[e.Identity.AgentID]++
		}
		if e.Subject.Provider != "" {
			providerHits[e.Subject.Provider]++
		}
		if e.Decision.Action != "" {
			actionCounts[string(e.Decision.Action)]++
		}
		for _, f := range e.Findings {
			hit, ok := ruleHits[f.RuleID]
			if !ok {
				hit = map[string]interface{}{
					"count":      0,
					"by_agent":   map[string]int{},
					"severities": map[string]int{},
				}
				ruleHits[f.RuleID] = hit
			}
			hit["count"] = hit["count"].(int) + 1
			if e.Identity.AgentID != "" {
				by := hit["by_agent"].(map[string]int)
				by[e.Identity.AgentID]++
			}
			sev := hit["severities"].(map[string]int)
			sev[string(f.Severity)]++
		}
	}
	return map[string]interface{}{
		"events":        len(events),
		"rule_hits":     ruleHits,
		"agent_hits":    agentHits,
		"provider_hits": providerHits,
		"action_counts": actionCounts,
	}
}

func summarizePolicyForSuggesterMin(p *Policy) map[string]interface{} {
	out := map[string]interface{}{
		"version":            p.Version,
		"enabled":            p.Enabled,
		"fail_mode":          p.FailMode,
		"hash":               p.Hash,
		"preventive_enabled": p.Preventive.Enabled,
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
		out["custom_rules_count"] = len(p.CustomRules)
	}
	return out
}

// --- decisions persistence ---

var decisionsMu sync.Mutex

func decisionsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "cicy-ai", "autonomy", "decisions.ndjson")
}

func appendDecision(d AutonomyDecision) {
	decisionsMu.Lock()
	defer decisionsMu.Unlock()

	path := decisionsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("[autonomy] mkdir decisions: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("[autonomy] open decisions: %v", err)
		return
	}
	defer f.Close()
	body, err := json.Marshal(d)
	if err != nil {
		log.Printf("[autonomy] marshal decision: %v", err)
		return
	}
	body = append(body, '\n')
	if _, err := f.Write(body); err != nil {
		log.Printf("[autonomy] write decision: %v", err)
	}
}

// recentDecisionsCount tallies decisions in the last `window` whose at
// least one action was applied. Used to enforce per-hour rate caps.
func recentDecisionsCount(window time.Duration) int {
	decisionsMu.Lock()
	defer decisionsMu.Unlock()

	path := decisionsPath()
	body, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-window).UTC()
	count := 0
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var d AutonomyDecision
		if err := json.Unmarshal(line, &d); err != nil {
			continue
		}
		if d.Timestamp.Before(cutoff) {
			continue
		}
		for _, a := range d.Actions {
			if a.Applied {
				count++
				break
			}
		}
	}
	return count
}

// ReadDecisions returns the most recent N decisions (newest first) for
// the operator-facing "what did the agent do" surface.
func ReadDecisions(limit int) []AutonomyDecision {
	if limit <= 0 {
		limit = 100
	}
	decisionsMu.Lock()
	defer decisionsMu.Unlock()
	body, err := os.ReadFile(decisionsPath())
	if err != nil {
		return nil
	}
	var all []AutonomyDecision
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var d AutonomyDecision
		if err := json.Unmarshal(line, &d); err != nil {
			continue
		}
		all = append(all, d)
	}
	// Newest first.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}
