package audit

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

// AIRemediation is the JSON contract the AI returns. Each field is a
// human-readable section the email template uses verbatim (no further
// parsing). Empty fields render to a "(none)" line in the template so
// partial responses still produce a usable email.
type AIRemediation struct {
	Summary          string   `json:"summary"`
	SeverityExplain  string   `json:"severity_explain"`
	ImmediateActions []string `json:"immediate_actions"` // 受众=责任人/owner:处置已发生的泄露(revoke/rotate/排查)
	AgentGuidance    []string `json:"agent_guidance"`    // 受众=涉事 agent:行为修正,从源头治本
	LongerTerm       []string `json:"longer_term"`
}

const aiSystemPrompt = `You are a senior security incident-response advisor for an
AI-agent platform. You receive structured metadata about a data-protection
audit finding from the cicy-code audit system. Produce a JSON object with these
exact fields and nothing else:

  summary             One-sentence bilingual (zh-CN + en) description of what
                      was caught.
  severity_explain    Why this severity is correct, in 1-2 sentences.
  immediate_actions   2-5 actions for the RESPONSIBLE PERSON / owner to contain
                      the leak that already happened (damage control). For
                      credential/secret/API-key findings this MUST include
                      revoking the leaked credential, rotating/re-issuing it,
                      and auditing its recent use. Example: "Revoke the leaked
                      GitHub token immediately and issue a new one; review its
                      recent API calls for misuse." Each item a complete
                      imperative sentence.
  agent_guidance      1-4 actions for the OFFENDING AGENT ITSELF to fix its own
                      behaviour so it stops happening at the source (root cause).
                      Be concrete to what it did. Example: if the agent passed a
                      token in plaintext to curl → "Stop putting the token on the
                      command line; read it from an environment variable
                      ($GITHUB_TOKEN) or a secrets file instead." Each item a
                      complete imperative sentence addressed to the agent.
  longer_term         1-3 longer-term hardening suggestions.

Constraints:
  - Output ONLY the JSON object. No prose, no markdown fences.
  - You will NEVER receive the original prompt payload. Reason about the rule
    id + masked preview alone.
  - immediate_actions = contain the consequence (stop the bleeding);
    agent_guidance = change the behaviour (cure the cause). Keep them distinct.
  - Every item must be a concrete, executable step — never vague advice like
    "be careful with secrets".
  - Suggestions are advisory; recipients apply enterprise SOP.`

// aiHTTPClient is the minimal interface used by callAIRemediation. The
// production path uses *http.Client; tests inject a stub.
type aiHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

var defaultAIHTTPClient aiHTTPClient = &http.Client{Timeout: 12 * time.Second}

// buildAIPrompt returns the JSON-serializable user message describing one
// audit event. Strictly metadata + masked previews — no raw payload.
func buildAIPrompt(e Event) (string, error) {
	type findingLite struct {
		RuleID     string `json:"rule_id"`
		Severity   string `json:"severity"`
		Category   string `json:"category"`
		MatchCount int    `json:"match_count"`
		Preview    string `json:"preview,omitempty"`
	}
	out := struct {
		Severity   string        `json:"severity"`
		AgentID    string        `json:"agent_id"`
		AgentType  string        `json:"agent_type"`
		Provider   string        `json:"provider"`
		Model      string        `json:"model"`
		Action     string        `json:"action_taken"`
		Applied    bool          `json:"applied"`
		Findings   []findingLite `json:"findings"`
	}{
		Severity:  string(topSeverity(e.Findings)),
		AgentID:   e.Identity.AgentID,
		AgentType: e.Identity.AgentType,
		Provider:  e.Subject.Provider,
		Model:     e.Subject.Model,
		Action:    string(e.Decision.Action),
		Applied:   e.Decision.Applied,
	}
	for _, f := range e.Findings {
		fl := findingLite{
			RuleID:     f.RuleID,
			Severity:   string(f.Severity),
			Category:   f.Category,
			MatchCount: f.MatchCount,
		}
		if len(f.Spans) > 0 {
			fl.Preview = f.Spans[0].Preview
		}
		out.Findings = append(out.Findings, fl)
	}
	body, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// callAIRemediation does one POST to an OpenAI-compatible
// /chat/completions endpoint. Returns nil + error on disabled/missing
// endpoint, timeout, HTTP error, or unparseable response — caller MUST
// fall back to the placeholder template in every nil-return case.
func callAIRemediation(ctx context.Context, cfg AIRemediationConfig, e Event) (*AIRemediation, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("ai_remediation disabled")
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("ai_remediation.endpoint empty")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("ai_remediation.model empty")
	}

	userBody, err := buildAIPrompt(e)
	if err != nil {
		return nil, err
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 600
	}
	timeoutSec := cfg.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 10
	}

	chatReq := map[string]interface{}{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": aiSystemPrompt},
			{"role": "user", "content": userBody},
		},
		"max_tokens":  maxTokens,
		"temperature": 0.2,
	}
	reqBody, _ := json.Marshal(chatReq)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	url := strings.TrimRight(endpoint, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if k := strings.TrimSpace(cfg.APIKey); k != "" {
		httpReq.Header.Set("Authorization", "Bearer "+k)
	}

	resp, err := defaultAIHTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ai_remediation http %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("ai_remediation parse chat envelope: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("ai_remediation no choices")
	}
	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	// Strip ```json fences if the model added them despite instructions.
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var out AIRemediation
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, fmt.Errorf("ai_remediation parse output JSON: %w (raw: %s)", err, truncate(content, 200))
	}
	return &out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
