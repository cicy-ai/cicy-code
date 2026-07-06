// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Usage analysis (P1): a product-facing breakdown of the latest turn's token
// spend. Reads two on-disk snapshots the gateway already writes:
//   current.json — the in-flight provider request (system / tools / messages)
//   reply.json   — the latest completed turn's REAL usage (tokens + cost)
// and returns KPIs, a per-segment composition estimate, and a cache-efficiency
// split. Segment token counts are ESTIMATES (the provider doesn't return a
// per-section breakdown); the real input total comes from reply.json and the
// estimates are scaled to it for honest absolute numbers.

// estTokens roughly estimates tokens for a string: ASCII ~4 chars/token, CJK
// ~1.5 chars/token. Good enough for proportions, never billed against.
func estTokens(s string) int {
	if s == "" {
		return 0
	}
	ascii, cjk := 0, 0
	for _, r := range s {
		if r >= 0x2E80 {
			cjk++
		} else {
			ascii++
		}
	}
	n := ascii/4 + int(float64(cjk)/1.5)
	if n == 0 && len(s) > 0 {
		n = 1
	}
	return n
}

func estJSONTokens(v interface{}) int {
	if v == nil {
		return 0
	}
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return estTokens(string(b))
}

func usageAnalysisReadJSON(path string) map[string]interface{} {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

func asInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func asFloat(v interface{}) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func asString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func handleAgentUsageAnalysisByPane(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/usage-analysis/")
	paneID := shortPaneID(strings.Trim(path, "/"))
	if paneID == "" {
		httpErr(w, http.StatusBadRequest, "pane id required")
		return
	}
	J(w, computeUsageAnalysis(paneID))
}

func computeUsageAnalysis(paneID string) M {
	dir := aiGatewayHistoryDir(paneID)
	current := usageAnalysisReadJSON(filepath.Join(dir, "current.json"))
	reply := usageAnalysisReadJSON(filepath.Join(dir, "reply.json"))

	out := M{"pane_id": paneID}

	// --- KPIs + cache split: aggregate the latest TURN from usage.jsonl ---
	// usage.jsonl holds one internally-consistent record per gateway request
	// (matching input/output/cache). A turn = one user message → N tool-loop
	// requests, so the real cost is the SUM of all its requests. reply.json can't
	// be priced directly: its input is a single-request snapshot while its output
	// is accumulated (even across turns), which made output look like ~99% of
	// spend. We aggregate the latest turn here and fall back to reply.json only
	// when the log is unavailable.
	recent := agentUsageLogRead(paneID, 200) // newest-first
	turn := aggregateLatestTurn(recent)

	modelForCost := asString(current["model"])
	if modelForCost == "" {
		modelForCost = asString(reply["model"])
	}
	if modelForCost == "" {
		modelForCost = turn.model
	}

	var inputTokens, outputTokens, cacheRead, cacheCreate int
	var cost float64
	var costKnown bool
	if turn.requests > 0 {
		inputTokens, outputTokens = turn.input, turn.output
		cacheRead, cacheCreate = turn.cacheRead, turn.cacheCreate
		cost, costKnown = turn.cost, turn.costKnown
		if turn.model != "" {
			modelForCost = turn.model
		}
	} else {
		inputTokens = asInt(reply["input_tokens"])
		outputTokens = asInt(reply["output_tokens"])
		cacheRead = asInt(reply["cache_read_input_tokens"])
		cacheCreate = asInt(reply["cache_creation_input_tokens"])
		f := inputTokens - cacheRead - cacheCreate
		if f < 0 {
			f = 0
		}
		cost, costKnown = estimateModelCostTokens(modelForCost, f, cacheRead, cacheCreate, outputTokens)
	}
	totalTokens := inputTokens + outputTokens
	fresh := inputTokens - cacheRead - cacheCreate
	if fresh < 0 {
		fresh = 0
	}
	hitRate := 0.0
	if inputTokens > 0 {
		hitRate = float64(cacheRead) / float64(inputTokens)
	}
	out["kpis"] = M{
		"input_tokens":                inputTokens,
		"output_tokens":               outputTokens,
		"total_tokens":                totalTokens,
		"cache_read_input_tokens":     cacheRead,
		"cache_creation_input_tokens": cacheCreate,
		"cost_credit":                 cost,
		"cost_pricing_known":          costKnown,
		"cost_available":              costKnown,
		"cache_hit_rate":              hitRate,
		"turn_id":                     turn.turnID,
		"turn_requests":               turn.requests,
		"status":                      asString(reply["status"]),
		"started_at":                  asString(reply["started_at"]),
		"updated_at":                  asString(reply["updated_at"]),
	}
	out["cache"] = M{
		"read":     cacheRead,
		"creation": cacheCreate,
		"fresh":    fresh,
		"input":    inputTokens,
		"hit_rate": hitRate,
	}
	// Provider's raw usage object (verbatim, for auditing) — kept per-request
	// (the LATEST single request) so it stays internally consistent and the user
	// can reconcile it against one real API call. Prefer reply.json's stored
	// usage map; fall back to the latest log record, never the accumulated totals.
	rawUsage, _ := reply["usage"].(map[string]interface{})
	if len(rawUsage) == 0 {
		if len(recent) > 0 {
			r := recent[0]
			rawUsage = map[string]interface{}{
				"input_tokens":                r.InputTokens,
				"cache_read_input_tokens":     r.CacheReadInputTokens,
				"cache_creation_input_tokens": r.CacheCreationInputTokens,
				"output_tokens":               r.OutputTokens,
			}
		} else {
			rawUsage = map[string]interface{}{
				"input_tokens":                inputTokens,
				"cache_read_input_tokens":     cacheRead,
				"cache_creation_input_tokens": cacheCreate,
				"output_tokens":               outputTokens,
			}
		}
	}
	out["raw_usage"] = rawUsage
	// Cost breakdown by billing bucket, over the SAME per-turn totals as the cost
	// KPI — so the buckets sum to the headline cost and the output share is real
	// (not the artifact of accumulated output vs single-request input).
	out["cost_breakdown"] = costBreakdown(modelForCost, fresh, cacheRead, cacheCreate, outputTokens)

	// --- This-request composition (estimated from current.json.body) ---
	model := asString(current["model"])
	provider := asString(current["provider"])
	if model == "" {
		model = asString(reply["model"])
	}
	out["model"] = model
	out["provider"] = provider

	body, _ := current["body"].(map[string]interface{})
	systemEst := estJSONTokens(bodyField(body, "system"))
	tools, _ := bodyField(body, "tools").([]interface{})
	toolsEst := estJSONTokens(bodyField(body, "tools"))
	messages, _ := bodyField(body, "messages").([]interface{})
	messagesEst, historyDetail := analyzeHistory(messages)

	estTotal := systemEst + toolsEst + messagesEst
	// Scale the composition to a SINGLE request's input (current.json is one
	// request), not the per-turn total used for cost — otherwise a multi-request
	// turn would inflate each segment. This keeps "本次请求构成" honest.
	realInput := asInt(reply["input_tokens"])
	if realInput <= 0 && len(recent) > 0 {
		realInput = recent[0].InputTokens
	}
	scale := func(seg int) int {
		if estTotal <= 0 || realInput <= 0 {
			return seg
		}
		return int(float64(seg) / float64(estTotal) * float64(realInput))
	}
	pct := func(seg int) float64 {
		if estTotal <= 0 {
			return 0
		}
		return float64(seg) / float64(estTotal)
	}
	segments := []M{
		{"key": "system", "label": "System", "est_tokens": systemEst, "pct": pct(systemEst), "scaled_tokens": scale(systemEst)},
		{"key": "tools", "label": "Tools", "est_tokens": toolsEst, "pct": pct(toolsEst), "scaled_tokens": scale(toolsEst), "count": len(tools)},
		{"key": "history", "label": "History", "est_tokens": messagesEst, "pct": pct(messagesEst), "scaled_tokens": scale(messagesEst), "messages": len(messages)},
	}
	out["breakdown"] = M{
		"est_total":  estTotal,
		"real_input": realInput,
		"segments":   segments,
		"available":  body != nil,
	}
	out["history_detail"] = historyDetail
	nodes := detectOptimizableNodes(body, tools, messages, estTotal)
	out["optimizable_nodes"] = nodes
	// Explicitly report which trackable components were NOT found, so the UI can
	// render "未检测到" instead of a blank — absence becomes a stated conclusion.
	// Only skills/mcp: a missing /compact or CLAUDE.md is a good thing, not a gap.
	seenKind := map[string]bool{}
	for _, n := range nodes {
		if k, ok := n["kind"].(string); ok {
			seenKind[k] = true
		}
	}
	absent := []string{}
	for _, k := range []string{"skills", "mcp"} {
		if !seenKind[k] {
			absent = append(absent, k)
		}
	}
	out["optimizable_nodes_absent"] = absent

	// --- P2: historical series (chronological, oldest→newest) from usage.jsonl ---
	series := make([]M, 0, len(recent))
	for i := len(recent) - 1; i >= 0; i-- {
		r := recent[i]
		series = append(series, M{
			"ts":             r.TS,
			"input":          r.InputTokens,
			"output":         r.OutputTokens,
			"cache_read":     r.CacheReadInputTokens,
			"total":          r.TotalTokens,
			"cost":           r.CostCredit,
			"latency_ms":     r.LatencyMS,
			"cache_hit_rate": usageHitRate(r.InputTokens, r.CacheReadInputTokens),
		})
	}
	out["history"] = series

	// --- P3: optimization suggestions (rule engine) + savings quantification ---
	// Savings/cost-prediction is OFF by default and only attached when enabled
	// (see usageCostPredictionEnabled). When off, suggestions carry no money line
	// and no top-level `savings` summary, so the UI hides the whole prediction.
	predict := usageCostPredictionEnabled()
	proj := usageProjection{}
	if predict {
		proj = buildUsageProjection(modelForCost, recent)
	}
	suggestions := buildUsageSuggestions(segments, historyDetail, hitRate, inputTokens, totalTokens, recent, proj, predict)
	out["suggestions"] = suggestions
	out["cost_prediction_enabled"] = predict

	if predict {
		// Top-level savings summary: the headline is the single biggest lever (max,
		// not sum — levers overlap, e.g. tool-output trimming is a subset of history
		// compaction, so summing would double-count).
		headline := 0.0
		for _, s := range suggestions {
			if sv, ok := s["savings"].(M); ok {
				if v, ok := sv["usd_month"].(float64); ok && v > headline {
					headline = v
				}
			}
		}
		out["savings"] = M{
			"headline_usd_month": round6(headline),
			"req_per_month":      round6(proj.reqPerMonth),
			"window_reqs":        proj.windowReqs,
			"span_days":          round6(proj.spanDays),
			"pricing_known":      proj.pricing.Known,
			"low_confidence":     proj.lowConfidence,
		}
	}

	// --- Cache-hit diagnosis over the last few requests ---
	out["cache_diag"] = buildCacheDiag(paneID, 2)

	return out
}

func usageHitRate(input, cacheRead int) float64 {
	if input <= 0 {
		return 0
	}
	return float64(cacheRead) / float64(input)
}

func segmentPct(segments []M, key string) float64 {
	for _, s := range segments {
		if s["key"] == key {
			if p, ok := s["pct"].(float64); ok {
				return p
			}
		}
	}
	return 0
}

func segmentInt(segments []M, key, field string) int {
	for _, s := range segments {
		if s["key"] == key {
			if v, ok := s[field].(int); ok {
				return v
			}
		}
	}
	return 0
}

// buildUsageSuggestions turns the analysis into actionable, product-facing
// advice. Each item: {level, key, title, detail, savings:{usd_month, basis}}.
// The savings figure makes each lever concrete: at the agent's observed request
// cadence, how much money does adopting it save per month. We price persistent
// context at the recurring cache-read cost (it's re-read every turn), and state
// the trim/compaction assumption in `basis` so the number is auditable.
func buildUsageSuggestions(segments []M, historyDetail M, hitRate float64, inputTokens, totalTokens int, recent []agentUsageLogRecord, proj usageProjection, predict bool) []M {
	out := []M{}
	// addSavings attaches the money line only when cost prediction is enabled.
	addSavings := func(m M, usdMonth float64, basis string) M {
		if predict {
			m["savings"] = savingsObj(usdMonth, basis)
		}
		return m
	}

	histScaled := float64(segmentInt(segments, "history", "scaled_tokens"))
	histEstTotal := 0
	if historyDetail != nil {
		histEstTotal, _ = historyDetail["total"].(int)
	}
	// realFromEst maps an estimated within-history token count to its real,
	// reply.json-scaled value, so savings are priced against real billed tokens.
	realFromEst := func(estTok int) float64 {
		if histEstTotal <= 0 || histScaled <= 0 {
			return float64(estTok)
		}
		return histScaled * float64(estTok) / float64(histEstTotal)
	}

	historyPct := segmentPct(segments, "history")
	if historyPct > 0.7 {
		msgCount := segmentInt(segments, "history", "messages")
		// Lever: compact/summarize ~50% of history. Those tokens stop being
		// re-read every turn → recurring cache-read cost removed.
		save := proj.recurringMonthly(histScaled * 0.5)
		out = append(out, addSavings(M{
			"level":  "warn",
			"key":    "history_dominates",
			"title":  "历史对话占据了大部分上下文",
			"detail": fmtSuggestf("历史消息占输入约 %.0f%%（%d 条）。考虑总结/压缩早期对话，或开一个新会话来降低每轮成本。", historyPct*100, msgCount),
		}, save, "按压缩约 50% 历史、且这些 token 每轮以缓存读取价重复计费估算"))
	}

	// Drill into history: if tool outputs (command/file dumps) dominate the
	// history, that's the single most actionable lever.
	if historyDetail != nil {
		histTotal := histEstTotal
		if byKind, ok := historyDetail["by_kind"].([]M); ok && histTotal > 0 {
			for _, kk := range byKind {
				if kk["kind"] == "tool_result" {
					tok, _ := kk["tokens"].(int)
					cnt, _ := kk["count"].(int)
					share := float64(tok) / float64(histTotal)
					if share > 0.4 {
						// Lever: trim ~60% of tool output. Real tokens × recurring.
						save := proj.recurringMonthly(realFromEst(tok) * 0.6)
						out = append(out, addSavings(M{
							"level":  "warn",
							"key":    "tool_output_bloat",
							"title":  "工具输出占据了大部分历史",
							"detail": fmtSuggestf("工具输出（命令/文件读取结果）约 %s tokens，占历史的 %.0f%%（%d 段）。这些是最容易裁剪的部分——避免把大文件/长命令输出整段塞进上下文，或在工具层做截断。", fmtTokens(float64(tok)), share*100, cnt),
						}, save, "按裁剪约 60% 工具输出、且这些 token 每轮以缓存读取价重复计费估算"))
					}
					break
				}
			}
		}
		// Surface the single biggest block as a concrete pointer.
		if top, ok := historyDetail["top"].([]M); ok && len(top) > 0 {
			b := top[0]
			btok, _ := b["tokens"].(int)
			if histTotal > 0 && float64(btok)/float64(histTotal) > 0.25 {
				save := proj.recurringMonthly(realFromEst(btok))
				out = append(out, addSavings(M{
					"level":  "info",
					"key":    "single_big_block",
					"title":  "单条消息异常大",
					"detail": fmtSuggestf("最大的一段「%v」约 %s tokens（占历史 %.0f%%）：%v", b["label"], fmtTokens(float64(btok)), float64(btok)/float64(histTotal)*100, b["preview"]),
				}, save, "按移除该段、且其 token 每轮以缓存读取价重复计费估算"))
			}
		}
	}

	if inputTokens > 0 && hitRate < 0.5 {
		// Lever: stabilize the prefix so it hits cache. The waste is the prefix
		// being re-written (cache_write, 1.25×) instead of read (0.1×). When
		// caching is fully cold (no reads, no writes) the whole prefix is paid at
		// full input rate, so price ~80% of fresh at the (input − read) delta.
		p := proj.pricing
		var save float64
		if proj.reqPerMonth > 0 {
			if proj.avgCacheWrite > 0 {
				save = proj.avgCacheWrite * (p.CacheWrite - p.CacheRead) / 1e6 * proj.reqPerMonth
			} else if proj.avgCacheRead == 0 {
				save = proj.avgFresh * 0.8 * (p.Input - p.CacheRead) / 1e6 * proj.reqPerMonth
			}
		}
		out = append(out, addSavings(M{
			"level":  "critical",
			"key":    "low_cache_hit",
			"title":  "缓存命中率偏低",
			"detail": fmtSuggestf("本轮缓存命中率仅 %.0f%%。上下文头部（system / tools）若每轮变化会使缓存失效——检查是否在 prompt 里注入了时间戳等动态内容。", hitRate*100),
		}, save, "按稳定前缀后、原本被重写/全价计费的 token 改以缓存读取价计费的差额估算（保守）"))
	}

	toolsPct := segmentPct(segments, "tools")
	toolsCount := segmentInt(segments, "tools", "count")
	if toolsPct > 0.2 || toolsCount >= 30 {
		toolsScaled := float64(segmentInt(segments, "tools", "scaled_tokens"))
		save := proj.recurringMonthly(toolsScaled * 0.4)
		out = append(out, addSavings(M{
			"level":  "info",
			"key":    "tools_heavy",
			"title":  "工具定义占用较多 token",
			"detail": fmtSuggestf("Tools 占输入约 %.0f%%（%d 个）。精简用不到的 skill/工具可直接缩小每轮上下文。", toolsPct*100, toolsCount),
		}, save, "按精简约 40% 工具定义、且工具 token 每轮以缓存读取价重复计费估算"))
	}

	// Input-token upward trend: compare the latest few requests' average input
	// against the older window. recent is newest-first.
	if len(recent) >= 8 {
		newAvg := avgInput(recent[:4])
		oldAvg := avgInput(recent[len(recent)-4:])
		if oldAvg > 0 && newAvg > oldAvg*1.3 {
			// The growth itself is already costing extra each turn.
			save := proj.recurringMonthly(newAvg - oldAvg)
			out = append(out, addSavings(M{
				"level":  "warn",
				"key":    "input_growing",
				"title":  "输入 token 呈上升趋势",
				"detail": fmtSuggestf("最近几次请求的平均输入（约 %s）比早前（约 %s）高出 %.0f%%，成本会随之增长——通常是历史累积所致。", fmtTokens(newAvg), fmtTokens(oldAvg), (newAvg/oldAvg-1)*100),
			}, save, "按已增长的输入部分每轮以缓存读取价重复计费估算的额外月成本"))
		}
	}

	if totalTokens > 0 && out != nil && len(out) == 0 {
		out = append(out, M{
			"level":  "info",
			"key":    "healthy",
			"title":  "用量状况良好",
			"detail": "当前没有明显的优化点：缓存命中良好、上下文构成合理。",
		})
	}
	return out
}

func avgInput(recs []agentUsageLogRecord) float64 {
	if len(recs) == 0 {
		return 0
	}
	sum := 0
	for _, r := range recs {
		sum += r.InputTokens
	}
	return float64(sum) / float64(len(recs))
}

func bodyField(body map[string]interface{}, key string) interface{} {
	if body == nil {
		return nil
	}
	return body[key]
}

func fmtSuggestf(format string, a ...interface{}) string {
	return strings.TrimSpace(fmt.Sprintf(format, a...))
}

func fmtTokens(n float64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", int(n))
	}
	if n < 1000000 {
		return fmt.Sprintf("%.1fk", n/1000)
	}
	return fmt.Sprintf("%.2fm", n/1000000)
}

// kindLabel maps an internal block kind to a human label (zh).
func kindLabel(kind string) string {
	switch kind {
	case "user_text":
		return "用户消息"
	case "assistant_text":
		return "助手回复"
	case "tool_use":
		return "工具调用"
	case "tool_result":
		return "工具输出"
	case "thinking":
		return "思考"
	default:
		return kind
	}
}

func historyPreview(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}

// analyzeHistory walks the messages array and returns (estTotalTokens, detail).
// detail breaks the history down by block kind (user/assistant text, tool_use,
// tool_result, thinking) and surfaces the largest individual blocks — because a
// few giant tool outputs (file dumps / command output) usually dominate.
func analyzeHistory(messages []interface{}) (int, M) {
	type kindAgg struct {
		tokens int
		count  int
	}
	kinds := map[string]*kindAgg{}
	type bigBlock struct {
		Idx     int    `json:"idx"`
		Role    string `json:"role"`
		Kind    string `json:"kind"`
		Label   string `json:"label"`
		Preview string `json:"preview"`
		Tokens  int    `json:"tokens"`
		Source  string `json:"source,omitempty"`
	}
	bigs := []bigBlock{}
	total := 0

	add := func(kind string, tok int) {
		a := kinds[kind]
		if a == nil {
			a = &kindAgg{}
			kinds[kind] = a
		}
		a.tokens += tok
		a.count++
		total += tok
	}

	for i, mi := range messages {
		m, _ := mi.(map[string]interface{})
		if m == nil {
			continue
		}
		role := asString(m["role"])
		switch c := m["content"].(type) {
		case string:
			tok := estTokens(c)
			add(role+"_text", tok)
			bigs = append(bigs, bigBlock{i, role, role + "_text", kindLabel(role + "_text"), historyPreview(c), tok, classifyResidentNode(role, c)})
		case []interface{}:
			for _, bi := range c {
				b, _ := bi.(map[string]interface{})
				if b == nil {
					continue
				}
				bt := asString(b["type"])
				var tok int
				var kind, prev, src string
				switch bt {
				case "text":
					txt := asString(b["text"])
					tok = estTokens(txt)
					kind = role + "_text"
					prev = historyPreview(txt)
					src = classifyResidentNode(role, txt)
				case "tool_use":
					tok = estJSONTokens(b["input"]) + estTokens(asString(b["name"]))
					kind = "tool_use"
					prev = asString(b["name"])
				case "tool_result":
					tok = estJSONTokens(b["content"])
					kind = "tool_result"
					prev = historyPreview(toolResultText(b["content"]))
				case "thinking":
					tok = estTokens(asString(b["thinking"]))
					kind = "thinking"
					prev = historyPreview(asString(b["thinking"]))
				default:
					tok = estJSONTokens(b)
					kind = bt
					if kind == "" {
						kind = "other"
					}
					prev = bt
				}
				add(kind, tok)
				bigs = append(bigs, bigBlock{i, role, kind, kindLabel(kind), prev, tok, src})
			}
		}
	}

	// by_kind sorted desc by tokens, with pct
	byKind := []M{}
	for kind, a := range kinds {
		pct := 0.0
		if total > 0 {
			pct = float64(a.tokens) / float64(total)
		}
		byKind = append(byKind, M{"kind": kind, "label": kindLabel(kind), "tokens": a.tokens, "count": a.count, "pct": pct})
	}
	sort.Slice(byKind, func(i, j int) bool {
		ai, _ := byKind[i]["tokens"].(int)
		aj, _ := byKind[j]["tokens"].(int)
		return ai > aj
	})

	// top largest blocks
	sort.Slice(bigs, func(i, j int) bool { return bigs[i].Tokens > bigs[j].Tokens })
	topN := bigs
	if len(topN) > 6 {
		topN = topN[:6]
	}
	top := []M{}
	for _, b := range topN {
		if b.Tokens <= 0 {
			continue
		}
		pct := 0.0
		if total > 0 {
			pct = float64(b.Tokens) / float64(total)
		}
		top = append(top, M{"idx": b.Idx, "role": b.Role, "kind": b.Kind, "label": b.Label, "preview": b.Preview, "tokens": b.Tokens, "pct": pct, "source": b.Source})
	}

	return total, M{
		"message_count": len(messages),
		"total":         total,
		"by_kind":       byKind,
		"top":           top,
	}
}

// --- Optimizable-node detection -------------------------------------------
// Surfaces the well-known RESIDENT components that ride EVERY request so users
// can act on them to shrink the context window:
//   compact   — the /compact artifact (a summary + frozen tool outputs baked
//               into history; only /clear truly removes it)
//   claude_md — the CLAUDE.md / project-instruction reminder
//   skills    — the skills catalog (a system-reminder listing available skills)
//   mcp       — MCP server tool schemas (resident in body.tools)
// Text components are matched by a stable signature because they move between
// body.system and body.messages depending on agent type/version; MCP is matched
// by tool naming. Token counts are estimates (proportions only), as elsewhere.

// isMCPToolName reports whether a tool name is an MCP-provided tool. Claude Code
// namespaces them as mcp__<server>__<tool>.
func isMCPToolName(name string) bool {
	return strings.HasPrefix(name, "mcp__") || strings.Contains(name, "mcp__")
}

// classifyResidentNode returns the optimizable-component kind for a text block,
// or "" when it isn't a recognizable one. role is the enclosing message role
// ("" for body.system blocks). Order matters: the more specific signatures win.
func classifyResidentNode(role, text string) string {
	// Assistant-authored text is NEVER a resident injected component: every
	// optimizable node (compact summary/snapshot, CLAUDE.md reminder, skills
	// catalog, MCP schema) is harness/user/tool-injected, not model output. This
	// guard kills false positives where the assistant merely DISCUSSES or QUOTES
	// a marker (e.g. pasting "<system-reminder>\nThe following skills are…" while
	// explaining the feature) — which otherwise gets mis-flagged as a real node.
	if role == "assistant" {
		return ""
	}
	trimmed := strings.TrimSpace(text)
	switch {
	case strings.Contains(text, "This session is being continued from a previous conversation"):
		return "compact_summary" // the /compact summary prose (a user message)
	case strings.Contains(text, "Called the ") && strings.Contains(text, "Result of calling the "):
		// Transient tool outputs frozen verbatim into a persistent history block
		// by /compact. We do NOT key off role=="system" alone: Claude Code also
		// injects recurring harness reminders (e.g. the task-tools nudge) as
		// role:system, and those aren't user-optimizable — matching them misleads.
		return "compact_snapshot"
	case strings.Contains(text, "# claudeMd") ||
		(strings.Contains(text, "CLAUDE.md") && strings.Contains(text, "project instructions")):
		return "claude_md"
	case strings.HasPrefix(trimmed, "<system-reminder>") &&
		strings.Contains(text, "The following skills are available"):
		// The genuine skills catalog: a system-reminder block that BEGINS with the
		// reminder tag and carries the canonical enumeration header. Requiring the
		// prefix (not a bare Contains) rejects prose that merely cites the phrase.
		return "skills"
	}
	return ""
}

// nodeBlockText pulls the text out of a content block (object {type,text} or a
// bare string).
func nodeBlockText(v interface{}) string {
	if b, ok := v.(map[string]interface{}); ok {
		return asString(b["text"])
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// detectOptimizableNodes scans body.system, body.messages and body.tools and
// aggregates the resident optimizable components by kind, with their estimated
// token cost, location list, and share of the whole request (estTotal).
func detectOptimizableNodes(body map[string]interface{}, tools, messages []interface{}, estTotal int) []M {
	type agg struct {
		tokens int
		count  int
		locs   []string
		seen   map[string]bool
	}
	order := []string{}
	nodes := map[string]*agg{}
	// The aggregate panel collapses the two /compact sub-kinds (summary + frozen
	// snapshot) into one actionable "compact" row — same root cause, same fix
	// (/clear). The per-block `source` chip keeps the finer sub-kind so the two
	// rows in "最大的几段" read distinctly instead of looking like a duplicate.
	bump := func(kind string, tok int, loc string) {
		if strings.HasPrefix(kind, "compact") {
			kind = "compact"
		}
		a := nodes[kind]
		if a == nil {
			a = &agg{seen: map[string]bool{}}
			nodes[kind] = a
			order = append(order, kind)
		}
		a.tokens += tok
		a.count++
		if loc != "" && !a.seen[loc] {
			a.seen[loc] = true
			a.locs = append(a.locs, loc)
		}
	}

	// body.system (string or []block)
	switch sys := bodyField(body, "system").(type) {
	case []interface{}:
		for _, bi := range sys {
			txt := nodeBlockText(bi)
			if k := classifyResidentNode("", txt); k != "" {
				bump(k, estTokens(txt), "system")
			}
		}
	case string:
		if k := classifyResidentNode("", sys); k != "" {
			bump(k, estTokens(sys), "system")
		}
	}

	// body.messages (history)
	for i, mi := range messages {
		m, _ := mi.(map[string]interface{})
		if m == nil {
			continue
		}
		role := asString(m["role"])
		loc := fmt.Sprintf("#%d", i+1)
		switch c := m["content"].(type) {
		case string:
			if k := classifyResidentNode(role, c); k != "" {
				bump(k, estTokens(c), loc)
			}
		case []interface{}:
			for _, bi := range c {
				txt := nodeBlockText(bi)
				if txt == "" {
					continue
				}
				if k := classifyResidentNode(role, txt); k != "" {
					bump(k, estTokens(txt), loc)
				}
			}
		}
	}

	// body.tools — MCP server schemas (resident every request)
	mcpTok, mcpCount := 0, 0
	for _, ti := range tools {
		t, _ := ti.(map[string]interface{})
		if t == nil {
			continue
		}
		if isMCPToolName(asString(t["name"])) {
			mcpTok += estJSONTokens(t)
			mcpCount++
		}
	}
	if mcpCount > 0 {
		bump("mcp", mcpTok, "tools")
		nodes["mcp"].count = mcpCount // count = number of MCP tools, not blocks
	}

	out := []M{}
	for _, kind := range order {
		a := nodes[kind]
		pct := 0.0
		if estTotal > 0 {
			pct = float64(a.tokens) / float64(estTotal)
		}
		out = append(out, M{
			"kind":      kind,
			"tokens":    a.tokens,
			"count":     a.count,
			"locations": a.locs,
			"pct":       pct,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		ti, _ := out[i]["tokens"].(int)
		tj, _ := out[j]["tokens"].(int)
		return ti > tj
	})
	return out
}

func toolResultText(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		parts := []string{}
		for _, bi := range c {
			if b, ok := bi.(map[string]interface{}); ok {
				if txt := asString(b["text"]); txt != "" {
					parts = append(parts, txt)
				}
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

