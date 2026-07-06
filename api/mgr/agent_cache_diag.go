// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Cache diagnosis: per request we persist a LIGHTWEIGHT fingerprint of the
// cacheable prefix (system / tools / first message hashes + segment estimates)
// plus the request's actual cache outcome. Comparing the last few requests
// pinpoints WHY prompt caching missed — Anthropic-style caching keys on the
// exact prefix, so any change to system/tools/early-history before a cache
// breakpoint invalidates everything after it; and the cache also expires after
// ~5 minutes of inactivity. Full request bodies (≈1.7MB) are NOT stored.

const compositionLogFileName = "composition.jsonl"

// cacheTTLSeconds is Anthropic's prompt-cache TTL (5 min). A gap larger than
// this between requests means the cache expired regardless of prefix equality.
const cacheTTLSeconds = 300

type agentCompositionRecord struct {
	TS                   string  `json:"ts"`
	RequestID            string  `json:"request_id,omitempty"`
	SystemHash           string  `json:"system_hash"`
	ToolsHash            string  `json:"tools_hash"`
	FirstMsgHash         string  `json:"first_msg_hash"`
	ToolsCount           int     `json:"tools_count"`
	MsgCount             int     `json:"msg_count"`
	SystemEst            int     `json:"system_est"`
	ToolsEst             int     `json:"tools_est"`
	HistoryEst           int     `json:"history_est"`
	InputTokens          int     `json:"input_tokens"`
	CacheReadInputTokens int     `json:"cache_read_input_tokens"`
	HitRate              float64 `json:"hit_rate"`
}

var compositionLogMu sync.Mutex

func hashJSON(v interface{}) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write(b)
	return fmt.Sprintf("%016x", h.Sum64())
}

func compositionLogPath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), compositionLogFileName)
}

func aiGatewayAppendComposition(agentID string, rec agentCompositionRecord) {
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
	path := compositionLogPath(agentID)
	compositionLogMu.Lock()
	defer compositionLogMu.Unlock()
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

// agentCompositionRead returns up to `limit` most-recent records, newest-first.
func agentCompositionRead(agentID string, limit int) []agentCompositionRecord {
	if limit <= 0 {
		limit = 5
	}
	path := compositionLogPath(strings.TrimSpace(agentID))
	compositionLogMu.Lock()
	defer compositionLogMu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return []agentCompositionRecord{}
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	out := []agentCompositionRecord{}
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		s := strings.TrimSpace(lines[i])
		if s == "" {
			continue
		}
		var rec agentCompositionRecord
		if json.Unmarshal([]byte(s), &rec) == nil {
			out = append(out, rec)
		}
	}
	return out
}

// buildCacheDiag explains the cache outcome of the most recent `display`
// requests. It reads ONE extra record beyond `display` so the oldest displayed
// row can still diff against its real predecessor (gap + changed-prefix) instead
// of being mislabeled as the baseline; the extra record is trimmed before
// returning. Returns a map ready for JSON.
func buildCacheDiag(agentID string, display int) M {
	if display < 1 {
		display = 1
	}
	recs := agentCompositionRead(agentID, display+1)
	rows := make([]M, 0, len(recs))
	// recs is newest-first; emit oldest→newest with a "changed vs previous"
	// annotation so the UI can show the timeline left→right.
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		row := M{
			"ts":          r.TS,
			"request_id":  r.RequestID,
			"hit_rate":    r.HitRate,
			"input":       r.InputTokens,
			"cache_read":  r.CacheReadInputTokens,
			"system_est":  r.SystemEst,
			"tools_est":   r.ToolsEst,
			"history_est": r.HistoryEst,
			"msg_count":   r.MsgCount,
		}
		// compare against the chronologically previous record (recs[i+1]) and
		// derive a single plain-language reason + severity. The KEY principle:
		// judge by the ACTUAL cache hit rate, not by raw hash equality. A
		// system/tools hash can change every request (e.g. a date injected into
		// the system prompt) yet the cache still hits ~99% — that change is
		// harmless and must NOT be flagged red. We only blame a prefix change
		// when the hit rate actually dropped.
		const healthyHit = 0.8
		reason, level := "ok", "ok"
		if i+1 >= len(recs) {
			row["changed"] = []string{}
			row["gap_seconds"] = 0
			row["expired"] = false
			reason, level = "first", "neutral"
		} else {
			prev := recs[i+1]
			changed := []string{}
			if r.SystemHash != prev.SystemHash {
				changed = append(changed, "system")
			}
			if r.ToolsHash != prev.ToolsHash {
				changed = append(changed, "tools")
			}
			if r.FirstMsgHash != prev.FirstMsgHash {
				changed = append(changed, "first_msg")
			}
			gap := tsGapSeconds(prev.TS, r.TS)
			expired := gap > cacheTTLSeconds
			row["changed"] = changed
			row["gap_seconds"] = gap
			row["expired"] = expired

			switch {
			case r.HitRate >= healthyHit:
				// Cache is working. Note (without alarming) if the prefix shifted.
				if len(changed) > 0 {
					reason, level = "ok_dynamic", "ok"
				} else {
					reason, level = "ok", "ok"
				}
			case expired:
				reason, level = "expired", "warn"
			case sliceHas(changed, "system"):
				reason, level = "miss_system", "bad"
			case sliceHas(changed, "tools"):
				reason, level = "miss_tools", "bad"
			case sliceHas(changed, "first_msg"):
				reason, level = "miss_first_msg", "bad"
			default:
				reason, level = "miss_other", "warn"
			}
		}
		row["reason"] = reason
		row["row_level"] = level
		rows = append(rows, row)
	}

	// Trim the extra comparison-only record: show only the last `display` rows,
	// each already annotated against its real predecessor above.
	if len(rows) > display {
		rows = rows[len(rows)-display:]
	}

	diag := M{"rows": rows, "available": len(recs) > 0}

	// Verdict + suggestions based on the latest request vs the previous one.
	if len(recs) >= 1 {
		latest := recs[0]
		var verdict string
		var level string
		if latest.InputTokens > 0 && latest.HitRate >= 0.8 {
			verdict = "缓存工作正常"
			level = "info"
		} else if len(recs) >= 2 {
			prev := recs[1]
			gap := tsGapSeconds(prev.TS, latest.TS)
			switch {
			case latest.SystemHash != prev.SystemHash:
				verdict = "System 提示在两次请求间发生变化，使整个前缀缓存失效（常见原因：system 里注入了时间戳/日期等动态内容）"
				level = "critical"
			case latest.ToolsHash != prev.ToolsHash:
				verdict = "工具集（tools 定义）发生变化，使缓存前缀失效——检查是否在请求间增删了 skill/工具"
				level = "critical"
			case latest.FirstMsgHash != prev.FirstMsgHash:
				verdict = "历史头部消息被改写（本应 append-only），导致缓存前缀失效"
				level = "critical"
			case gap > cacheTTLSeconds:
				verdict = fmt.Sprintf("两次请求间隔约 %d 秒，超过缓存 TTL(~5 分钟)，缓存已过期", gap)
				level = "warn"
			default:
				verdict = "前缀未变化但命中率偏低——可能是 provider 未启用缓存，或上轮缓存写入尚未生效"
				level = "warn"
			}
		} else {
			verdict = "样本不足（仅 1 次请求），再发起一次即可对比"
			level = "info"
		}
		diag["verdict"] = verdict
		diag["level"] = level
		diag["latest_hit_rate"] = latest.HitRate
	}
	return diag
}

func sliceHas(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func tsGapSeconds(prev, cur string) int {
	p, e1 := time.Parse(time.RFC3339Nano, prev)
	c, e2 := time.Parse(time.RFC3339Nano, cur)
	if e1 != nil || e2 != nil {
		return 0
	}
	d := c.Sub(p).Seconds()
	if d < 0 {
		d = -d
	}
	return int(d)
}
