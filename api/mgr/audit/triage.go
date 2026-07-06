// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package audit

// triage.go — adjudicate a single alert: real leak or false positive?
//
// This is the "AI 研判" layer. Given a finding enriched with its field PATH and
// surrounding CONTEXT (see structure.go), it returns a verdict + plain-language
// conclusion + recommended action, so a human sees a judgement instead of raw
// scanner output. When an LLM is configured (autonomy.json) it asks the model;
// otherwise it falls back to a deterministic heuristic so the feature always
// works offline.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// TriageInput is the enriched finding the caller wants adjudicated. The
// frontend already holds the event, so it posts the relevant fields directly.
type TriageInput struct {
	RuleID        string `json:"rule_id"`
	Severity      string `json:"severity"`
	Category      string `json:"category"`
	Path          string `json:"path"`
	Context       string `json:"context"`
	AgentID       string `json:"agent_id"`
	AgentType     string `json:"agent_type"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Direction     string `json:"direction"`
	Action        string `json:"action"`
	PayloadSizeKB int    `json:"payload_size_kb"`
}

// TriageResult is the verdict surfaced in the UI.
type TriageResult struct {
	Verdict    string `json:"verdict"`    // false_positive | low | medium | high | critical
	Confidence string `json:"confidence"` // high | medium | low
	Conclusion string `json:"conclusion"` // one sentence, plain language
	Action     string `json:"action"`     // recommended next step
	Source     string `json:"source"`     // "llm" | "heuristic"
}

const triageSystemPrompt = `You are a senior security analyst triaging a DLP alert raised on AI-agent network traffic. Decide whether it is a REAL secret/PII leak or a FALSE POSITIVE, and recommend an action.

Decisive signals, in order:
1. PATH — where the match sits in the request body.
   - Provider protocol fields (e.g. ".signature" = Anthropic thinking signature), base64 image data (".source.data", "data:image"), or message/diagnostic IDs => almost always FALSE POSITIVE.
   - An auth header, a tool-call argument, or user content => could be REAL.
2. RULE — structured credentials (api_key/sk-, jwt, bearer, aws, private key) are higher signal than the generic "high_entropy" rule, which fires on any random-looking string (base64, hashes, encoded blobs).
3. CONTEXT — the masked surrounding text. If it is wrapped in more base64/encoded data it is likely a blob, not a secret.
4. DESTINATION — outbound to the agent's own sanctioned model provider is far lower risk than to an unknown destination.

Return ONLY a JSON object, no markdown:
{
  "verdict": "false_positive" | "low" | "medium" | "high" | "critical",
  "confidence": "high" | "medium" | "low",
  "conclusion": "<one plain-language sentence a non-expert understands>",
  "action": "<one short recommended action>"
}`

// TriageFinding adjudicates one alert. Never returns nil.
func TriageFinding(ctx context.Context, in TriageInput) *TriageResult {
	cfg := autonomyCfg
	if cfg != nil && cfg.LLM.Endpoint != "" && cfg.LLM.Model != "" {
		if res := triageViaLLM(ctx, cfg, in); res != nil {
			res.Source = "llm"
			return res
		}
	}
	res := heuristicTriage(in)
	res.Source = "heuristic"
	return res
}

func triageViaLLM(ctx context.Context, cfg *AutonomyConfig, in TriageInput) *TriageResult {
	userMsg, _ := json.Marshal(in)
	chatReq := map[string]interface{}{
		"model": cfg.LLM.Model,
		"messages": []map[string]string{
			{"role": "system", "content": triageSystemPrompt},
			{"role": "user", "content": string(userMsg)},
		},
		"max_tokens":  400,
		"temperature": 0.1,
	}
	bodyBytes, _ := json.Marshal(chatReq)

	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.LLM.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.LLM.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.LLM.APIKey)
	}
	resp, err := defaultAIHTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}
	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &chatResp) != nil || len(chatResp.Choices) == 0 {
		return nil
	}
	raw := chatResp.Choices[0].Message.Content
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil
	}
	var out TriageResult
	if json.Unmarshal([]byte(raw[start:end+1]), &out) != nil || out.Verdict == "" {
		return nil
	}
	return &out
}

// heuristicTriage is the deterministic fallback — the same reasoning a senior
// analyst applies in the first five seconds, encoded as rules.
func heuristicTriage(in TriageInput) *TriageResult {
	path := strings.ToLower(in.Path)
	rule := strings.ToLower(in.RuleID)
	ctxL := strings.ToLower(in.Context)

	benignPath := strings.Contains(path, "signature") ||
		strings.Contains(path, ".source.data") ||
		strings.Contains(path, "data:image") ||
		strings.HasSuffix(path, "_id") ||
		strings.Contains(path, "message_id")

	if benignPath {
		return &TriageResult{
			Verdict: "false_positive", Confidence: "high",
			Conclusion: "命中落在协议/ID 字段(" + safePath(in.Path) + "),并非用户机密。",
			Action:     "标记误报,或给该字段加白名单。",
		}
	}

	if strings.Contains(rule, "high_entropy") {
		// Generic entropy rule: lean false positive unless it sits in a header.
		if strings.Contains(path, "header") || strings.Contains(path, "authorization") {
			return &TriageResult{Verdict: "high", Confidence: "medium",
				Conclusion: "高熵串出现在请求头中,可能是凭证泄露。",
				Action:     "立即核实该值,确认是凭证则吊销并改走密钥管理。"}
		}
		return &TriageResult{Verdict: "false_positive", Confidence: "medium",
			Conclusion: "高熵串多为 base64/编码内容(如图片、哈希、签名),通常不是真实密钥。",
			Action:     "抽查上下文确认;是编码数据则标记误报。"}
	}

	// Structured credential rules — these have real signal.
	if strings.Contains(rule, "api_key") || strings.Contains(rule, "jwt") ||
		strings.Contains(rule, "bearer") || strings.Contains(rule, "aws") ||
		strings.Contains(rule, "private_key") {
		return &TriageResult{Verdict: pickSeverity(in.Severity, "high"), Confidence: "medium",
			Conclusion: "检测到结构化凭证特征(" + safePath(in.RuleID) + "),有真实泄露风险。",
			Action:     "确认是否真实密钥;若是,吊销并通过密钥管理传递,勿明文外发。"}
	}

	if in.Category == "pii" || strings.Contains(rule, "pii") {
		return &TriageResult{Verdict: pickSeverity(in.Severity, "medium"), Confidence: "medium",
			Conclusion: "检测到个人信息(PII),需确认是否应外发。",
			Action:     "核实合规性;非必要则脱敏或拦截。"}
	}

	_ = ctxL
	return &TriageResult{Verdict: pickSeverity(in.Severity, "low"), Confidence: "low",
		Conclusion: "命中一条规则,证据不足以判定真伪,建议人工抽查。",
		Action:     "查看命中上下文后再定性。"}
}

func pickSeverity(sev, fallback string) string {
	switch sev {
	case "critical", "high", "medium", "low":
		return sev
	}
	return fallback
}

func safePath(p string) string {
	if len(p) > 60 {
		return p[:60] + "…"
	}
	if p == "" {
		return "未知字段"
	}
	return p
}
