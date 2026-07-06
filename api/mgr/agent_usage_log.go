// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Per-AI-request usage log. Each completed (non-auxiliary) gateway request
// appends one JSONL line to <historyDir>/usage.jsonl with its latency and token
// breakdown. The "分析 → 用量" UI tab tails this file and refreshes in near
// real-time. Records are append-only and never rewritten, so a tail read is
// cheap and the UI can diff by request_id.

const usageLogFileName = "usage.jsonl"

type agentUsageLogRecord struct {
	TS                       string `json:"ts"`                          // completion time, RFC3339Nano
	ConversationID           string `json:"conversation_id,omitempty"`
	TurnID                   string `json:"turn_id,omitempty"`
	RequestID                string `json:"request_id,omitempty"`
	Provider                 string `json:"provider,omitempty"`
	Model                    string `json:"model,omitempty"`
	Status                   string `json:"status,omitempty"`            // "completed" | "failed"
	StatusCode               int    `json:"status_code,omitempty"`
	ReplyStartMS             int    `json:"reply_start_ms"`              // 发起 → reply 开始（首字节/TTFT）
	LatencyMS                int    `json:"latency_ms"`                  // 发起 → reply 结束（总耗时）
	InputTokens              int    `json:"input_tokens"`
	OutputTokens             int    `json:"output_tokens"`
	CacheReadInputTokens     int    `json:"cache_read_input_tokens"`     // 输入缓存（命中）
	CacheCreationInputTokens int    `json:"cache_creation_input_tokens"` // 输出缓存（写入）
	TotalTokens              int    `json:"total_tokens"`
	CostCredit               float64 `json:"cost_credit,omitempty"`
}

var usageLogMu sync.Mutex

func agentUsageLogPath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), usageLogFileName)
}

// aiGatewayAppendUsageLog appends one record as a JSONL line. Best-effort: any
// error is swallowed so logging never blocks or fails the request path.
func aiGatewayAppendUsageLog(agentID string, rec agentUsageLogRecord) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	if strings.TrimSpace(rec.TS) == "" {
		rec.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	path := agentUsageLogPath(agentID)

	usageLogMu.Lock()
	defer usageLogMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

// agentUsageLogRead returns up to `limit` of the most recent records, newest
// first. Missing file → empty slice.
func agentUsageLogRead(agentID string, limit int) []agentUsageLogRecord {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	if limit <= 0 {
		limit = 200
	}
	path := agentUsageLogPath(agentID)

	usageLogMu.Lock()
	defer usageLogMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return []agentUsageLogRecord{}
	}
	defer f.Close()

	// Ring buffer over lines so we only retain the newest `limit` without
	// holding the whole file.
	buf := make([]agentUsageLogRecord, 0, limit)
	start := 0
	count := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var rec agentUsageLogRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}
		if len(buf) < limit {
			buf = append(buf, rec)
		} else {
			buf[start] = rec
			start = (start + 1) % limit
		}
		count++
	}

	// Reassemble in chronological order, then reverse to newest-first.
	ordered := make([]agentUsageLogRecord, 0, len(buf))
	if count <= limit {
		ordered = append(ordered, buf...)
	} else {
		ordered = append(ordered, buf[start:]...)
		ordered = append(ordered, buf[:start]...)
	}
	out := make([]agentUsageLogRecord, 0, len(ordered))
	for i := len(ordered) - 1; i >= 0; i-- {
		out = append(out, ordered[i])
	}
	return out
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
