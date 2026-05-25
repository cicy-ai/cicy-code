package audit

// decision_explain — ask the autonomy agent to narrate a past decision in
// human-readable form. Reuses the configured LLM + endpoint; the prompt
// is purely about explaining the decision (NOT proposing a new one).
//
// Used by the Decisions tab when the operator wants a sanity check on
// "why did the agent do this?" — the equivalent of asking a teammate to
// explain their PR.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ExplanationResult is what /api/audit/decisions/{id}/explain returns.
type ExplanationResult struct {
	DecisionID  string `json:"decision_id"`
	Summary     string `json:"summary"`     // one-line headline
	WhatChanged string `json:"what_changed"`
	WhyNow      string `json:"why_now"`
	Impact      string `json:"impact"`      // who is affected
	Confidence  string `json:"confidence"`  // "high" / "medium" / "low"
	RawMarkdown string `json:"raw_markdown,omitempty"` // freeform fallback
}

const explainSystemPrompt = `You are reviewing a past decision made by the autonomous policy agent.
You did NOT make this decision yourself — your job is forensic: explain
clearly to a human reviewer what the agent did, what evidence it cited,
and what the impact is.

Return ONLY a JSON object — no markdown fences:

{
  "summary":      "<one-sentence headline>",
  "what_changed": "<2-3 sentences listing the concrete policy.json edits>",
  "why_now":      "<2-3 sentences about the events / FP rate / patterns that triggered this>",
  "impact":       "<who is affected — which agents, which hosts, which rules>",
  "confidence":   "high" | "medium" | "low"
}

Confidence guidance:
  - "high"   = the rationale clearly cites concrete event statistics.
  - "medium" = the rationale is plausible but not strongly evidenced.
  - "low"    = the rationale is vague, generic, or contradicted by the data shown.`

// ExplainDecision looks up the decision by ID, sends it to the LLM, and
// returns a structured explanation. Falls back to a stub explanation if
// autonomy isn't configured (so the UI button never returns 500).
func ExplainDecision(ctx context.Context, id string) (*ExplanationResult, error) {
	if id == "" {
		return nil, fmt.Errorf("empty decision id")
	}
	dec, ok := findDecision(id)
	if !ok {
		return nil, fmt.Errorf("decision %s not found", id)
	}

	cfg := autonomyCfg
	if cfg == nil || cfg.LLM.Endpoint == "" || cfg.LLM.Model == "" {
		// Autonomy not configured — return a deterministic stub explanation
		// derived from the decision itself.
		return stubExplanation(dec), nil
	}

	body, _ := json.Marshal(map[string]interface{}{
		"decision_id":         dec.ID,
		"timestamp":           dec.Timestamp,
		"trigger":             dec.Trigger,
		"events_considered":   dec.EventsConsidered,
		"events_window_from":  dec.EventsWindowFrom,
		"events_window_to":    dec.EventsWindowTo,
		"actions":             dec.Actions,
		"policy_hash_before":  dec.PolicyHashBefore,
		"policy_hash_after":   dec.PolicyHashAfter,
		"error":               dec.Error,
		"llm_response_text":   dec.LLMResponseText,
	})

	raw, err := callExplainLLM(ctx, cfg, string(body))
	if err != nil {
		// Don't fail the request — fall back to stub.
		stub := stubExplanation(dec)
		stub.RawMarkdown = "LLM call failed: " + err.Error()
		return stub, nil
	}
	out := parseExplainResponse(raw)
	out.DecisionID = dec.ID
	return out, nil
}

func findDecision(id string) (AutonomyDecision, bool) {
	all := ReadDecisions(10000)
	for _, d := range all {
		if d.ID == id {
			return d, true
		}
	}
	return AutonomyDecision{}, false
}

func callExplainLLM(ctx context.Context, cfg *AutonomyConfig, userMsg string) (string, error) {
	chatReq := map[string]interface{}{
		"model": cfg.LLM.Model,
		"messages": []map[string]string{
			{"role": "system", "content": explainSystemPrompt},
			{"role": "user", "content": userMsg},
		},
		"max_tokens":  1200,
		"temperature": 0.1,
	}
	bodyBytes, _ := json.Marshal(chatReq)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
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
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
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
		return "", err
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices")
	}
	return chatResp.Choices[0].Message.Content, nil
}

func parseExplainResponse(raw string) *ExplanationResult {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return &ExplanationResult{RawMarkdown: strings.TrimSpace(raw)}
	}
	var out ExplanationResult
	if err := json.Unmarshal([]byte(raw[start:end+1]), &out); err != nil {
		return &ExplanationResult{RawMarkdown: strings.TrimSpace(raw)}
	}
	return &out
}

// stubExplanation builds a deterministic plain-English explanation from
// the decision struct itself. Used when LLM isn't configured / fails so
// the UI button never produces a 500.
func stubExplanation(dec AutonomyDecision) *ExplanationResult {
	applied := 0
	skipped := 0
	for _, a := range dec.Actions {
		if a.Applied {
			applied++
		} else {
			skipped++
		}
	}
	r := &ExplanationResult{
		DecisionID: dec.ID,
		Summary:    fmt.Sprintf("Tick triggered by %s — %d/%d actions applied.", dec.Trigger, applied, applied+skipped),
		Impact:     fmt.Sprintf("policy hash %s → %s", short(dec.PolicyHashBefore), short(dec.PolicyHashAfter)),
		Confidence: "low",
	}
	if dec.Error != "" {
		r.WhyNow = "Tick failed: " + dec.Error
		r.WhatChanged = "No policy changes were applied."
		return r
	}
	if applied == 0 {
		r.WhatChanged = "No policy changes applied this tick."
		r.WhyNow = fmt.Sprintf("Agent considered %d events but found nothing actionable above constraints.", dec.EventsConsidered)
		return r
	}
	var kinds []string
	var why []string
	for _, a := range dec.Actions {
		if a.Applied {
			kinds = append(kinds, a.Kind)
			if a.Rationale != "" {
				why = append(why, a.Rationale)
			}
		}
	}
	r.WhatChanged = "Applied: " + strings.Join(kinds, ", ")
	if len(why) > 0 {
		r.WhyNow = strings.Join(why, " ")
	} else {
		r.WhyNow = fmt.Sprintf("Decision based on %d events in the window.", dec.EventsConsidered)
	}
	return r
}

func short(hash string) string {
	if hash == "" {
		return "(none)"
	}
	if strings.HasPrefix(hash, "sha256:") {
		s := hash[7:]
		if len(s) > 8 {
			return "sha256:" + s[:8] + "…"
		}
	}
	if len(hash) > 12 {
		return hash[:12] + "…"
	}
	return hash
}
