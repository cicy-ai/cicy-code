package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Cross-agent usage statistics, aggregated from the usage_log table.
//
//	GET /api/agents/usage-stats
//	  ?days=N        only requests in the last N days (default: all time)
//	  &pane_id=w-…   restrict to one agent (default: all agents)
//
// Response:
//
//	{
//	  "totals":   { requests, input_tokens, output_tokens, total_tokens,
//	                cache_read, cache_create, cost_credit, cache_hit_rate, last_ts },
//	  "by_agent": [ { key:"w-10001", ...same aggregate fields... }, ... ],  // desc by total_tokens
//	  "by_model": [ { key:"claude-opus-4-8", ... }, ... ]
//	}
func handleAgentUsageStats(w http.ResponseWriter, r *http.Request) {
	if store == nil {
		J(w, M{"totals": M{}, "by_agent": []M{}, "by_model": []M{}})
		return
	}

	where := make([]string, 0, 2)
	args := make([]interface{}, 0, 2)
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if d, err := strconv.Atoi(raw); err == nil && d > 0 {
			cutoff := time.Now().UTC().AddDate(0, 0, -d).Format(time.RFC3339Nano)
			where = append(where, "ts >= ?")
			args = append(args, cutoff)
		}
	}
	if p := shortPaneID(strings.TrimSpace(r.URL.Query().Get("pane_id"))); p != "" {
		where = append(where, "pane_id = ?")
		args = append(args, p)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	J(w, M{
		"totals":   usageStatsTotals(whereSQL, args),
		"by_agent": usageStatsGroup("pane_id", whereSQL, args),
		"by_model": usageStatsGroup("model", whereSQL, args),
	})
}

// usageAggCols is the SUM/COUNT projection shared by the totals and group
// queries; the scan order below must match it exactly.
const usageAggCols = `COUNT(*),
	COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(total_tokens),0),
	COALESCE(SUM(cache_read_input_tokens),0), COALESCE(SUM(cache_creation_input_tokens),0),
	COALESCE(SUM(cost_credit),0), COALESCE(MAX(ts),'')`

func scanUsageAgg(scan func(dest ...interface{}) error) (M, bool) {
	var requests, input, output, total, cacheRead, cacheCreate int64
	var cost float64
	var lastTS string
	if err := scan(&requests, &input, &output, &total, &cacheRead, &cacheCreate, &cost, &lastTS); err != nil {
		return nil, false
	}
	hitRate := 0.0
	if input > 0 {
		hitRate = float64(cacheRead) / float64(input)
	}
	return M{
		"requests":       requests,
		"input_tokens":   input,
		"output_tokens":  output,
		"total_tokens":   total,
		"cache_read":     cacheRead,
		"cache_create":   cacheCreate,
		"cost_credit":    cost,
		"cache_hit_rate": hitRate,
		"last_ts":        lastTS,
	}, true
}

func usageStatsTotals(whereSQL string, args []interface{}) M {
	row := store.QueryRow("SELECT "+usageAggCols+" FROM usage_log"+whereSQL, args...)
	if m, ok := scanUsageAgg(row.Scan); ok {
		return m
	}
	return M{}
}

// usageStatsGroup aggregates grouped by `col` ("pane_id" or "model" — a fixed
// identifier, never user input), ordered by total tokens desc.
func usageStatsGroup(col, whereSQL string, args []interface{}) []M {
	rows, err := store.Query("SELECT "+col+", "+usageAggCols+
		" FROM usage_log"+whereSQL+" GROUP BY "+col+" ORDER BY SUM(total_tokens) DESC", args...)
	if err != nil {
		return []M{}
	}
	defer rows.Close()

	out := make([]M, 0, 16)
	for rows.Next() {
		var key string
		var requests, input, output, total, cacheRead, cacheCreate int64
		var cost float64
		var lastTS string
		if err := rows.Scan(&key, &requests, &input, &output, &total, &cacheRead, &cacheCreate, &cost, &lastTS); err != nil {
			continue
		}
		hitRate := 0.0
		if input > 0 {
			hitRate = float64(cacheRead) / float64(input)
		}
		out = append(out, M{
			"key":            key,
			"requests":       requests,
			"input_tokens":   input,
			"output_tokens":  output,
			"total_tokens":   total,
			"cache_read":     cacheRead,
			"cache_create":   cacheCreate,
			"cost_credit":    cost,
			"cache_hit_rate": hitRate,
			"last_ts":        lastTS,
		})
	}
	return out
}
