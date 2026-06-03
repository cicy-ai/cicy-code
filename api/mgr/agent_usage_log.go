package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Per-AI-request usage log. Each completed (non-auxiliary) gateway request
// inserts one row into the `usage_log` table (see db.go). This replaces the
// old per-agent usage.jsonl file so usage can be aggregated across ALL agents
// (see agent_usage_stats.go). The single-agent "分析 → 用量" tab reads the
// newest rows for one pane via agentUsageLogRead.

type agentUsageLogRecord struct {
	TS                       string  `json:"ts"` // completion time, RFC3339Nano
	ConversationID           string  `json:"conversation_id,omitempty"`
	TurnID                   string  `json:"turn_id,omitempty"`
	RequestID                string  `json:"request_id,omitempty"`
	Provider                 string  `json:"provider,omitempty"`
	Model                    string  `json:"model,omitempty"`
	Status                   string  `json:"status,omitempty"` // "completed" | "failed"
	StatusCode               int     `json:"status_code,omitempty"`
	ReplyStartMS             int     `json:"reply_start_ms"` // 发起 → reply 开始（首字节/TTFT）
	LatencyMS                int     `json:"latency_ms"`     // 发起 → reply 结束（总耗时）
	InputTokens              int     `json:"input_tokens"`
	OutputTokens             int     `json:"output_tokens"`
	CacheReadInputTokens     int     `json:"cache_read_input_tokens"`     // 输入缓存（命中）
	CacheCreationInputTokens int     `json:"cache_creation_input_tokens"` // 输出缓存（写入）
	TotalTokens              int     `json:"total_tokens"`
	CostCredit               float64 `json:"cost_credit,omitempty"`
}

// aiGatewayAppendUsageLog inserts one usage row. Best-effort: any error is
// swallowed so logging never blocks or fails the request path.
func aiGatewayAppendUsageLog(agentID string, rec agentUsageLogRecord) {
	agentID = shortPaneID(strings.TrimSpace(agentID))
	if agentID == "" || store == nil {
		return
	}
	if strings.TrimSpace(rec.TS) == "" {
		rec.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, _ = store.Exec(`INSERT INTO usage_log
		(pane_id, ts, conversation_id, turn_id, request_id, provider, model, status, status_code,
		 reply_start_ms, latency_ms, input_tokens, output_tokens, cache_read_input_tokens,
		 cache_creation_input_tokens, total_tokens, cost_credit)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		agentID, rec.TS, rec.ConversationID, rec.TurnID, rec.RequestID, rec.Provider, rec.Model,
		rec.Status, rec.StatusCode, rec.ReplyStartMS, rec.LatencyMS, rec.InputTokens, rec.OutputTokens,
		rec.CacheReadInputTokens, rec.CacheCreationInputTokens, rec.TotalTokens, rec.CostCredit)
}

// agentUsageLogRead returns up to `limit` of the most recent records for one
// pane, newest first. Missing data → empty slice.
func agentUsageLogRead(agentID string, limit int) []agentUsageLogRecord {
	agentID = shortPaneID(strings.TrimSpace(agentID))
	if agentID == "" || store == nil {
		return []agentUsageLogRecord{}
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := store.Query(`SELECT ts, conversation_id, turn_id, request_id, provider, model, status,
		status_code, reply_start_ms, latency_ms, input_tokens, output_tokens, cache_read_input_tokens,
		cache_creation_input_tokens, total_tokens, cost_credit
		FROM usage_log WHERE pane_id=? ORDER BY id DESC LIMIT ?`, agentID, limit)
	if err != nil {
		return []agentUsageLogRecord{}
	}
	defer rows.Close()

	out := make([]agentUsageLogRecord, 0, limit)
	for rows.Next() {
		var r agentUsageLogRecord
		if err := rows.Scan(&r.TS, &r.ConversationID, &r.TurnID, &r.RequestID, &r.Provider, &r.Model,
			&r.Status, &r.StatusCode, &r.ReplyStartMS, &r.LatencyMS, &r.InputTokens, &r.OutputTokens,
			&r.CacheReadInputTokens, &r.CacheCreationInputTokens, &r.TotalTokens, &r.CostCredit); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out // newest-first (ORDER BY id DESC)
}

// handleAgentUsageLogByPane serves GET /api/agents/usage-log/<paneID>?limit=N.
func handleAgentUsageLogByPane(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/usage-log/")
	paneID := shortPaneID(strings.Trim(path, "/"))
	if paneID == "" {
		httpErr(w, http.StatusBadRequest, "pane id required")
		return
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	records := agentUsageLogRead(paneID, limit)
	J(w, M{
		"pane_id": paneID,
		"records": records,
	})
}
