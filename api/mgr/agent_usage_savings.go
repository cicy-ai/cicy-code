package main

import "time"

// Cost-savings quantification. Diagnosis only tells a user WHAT is wrong; what
// actually changes behavior is showing the money. This module turns each
// suggestion into an estimated "$X / month if adopted".
//
// The core, defensible fact for these long-lived agents: a token that lives in
// the context window is re-read (as a cache hit) on EVERY subsequent request,
// so its real recurring cost is
//     cache_read_rate × requests_per_month
// — tiny per token, large over a month of high-frequency turns. Output tokens,
// by contrast, are paid once at the (5×-of-input) output rate and never cached.
//
// All projections are explicitly ESTIMATES: we state the assumption (trim %,
// compaction %) in each item's `basis` and never silently inflate. The monthly
// request cadence is derived from the observed gap between usage-log records.

// usageProjection is the projection basis derived from the usage log.
type usageProjection struct {
	pricing       modelPricing
	reqPerMonth   float64
	windowReqs    int
	spanDays      float64
	lowConfidence bool // true when the window is a short burst, not a representative span
	// average per-request real-token buckets over the observed window
	avgFresh      float64
	avgCacheRead  float64
	avgCacheWrite float64
	avgOutput     float64
}

// usageCostPredictionEnabled gates the savings/cost-prediction feature. It is
// OFF by default (built-in default, no config required) because the projection
// is only trustworthy once real, contract-accurate pricing is configured —
// shipping speculative "save $X/month" numbers on guessed list prices would
// mislead. Flip global.json `cost_prediction` to true to enable.
func usageCostPredictionEnabled() bool {
	if v, ok := readGlobalJSONConfig()["cost_prediction"].(bool); ok {
		return v
	}
	return false
}

// usageTurnAgg is the consistent per-turn rollup. A "turn" is one user message
// that may spawn many gateway requests (the agentic tool loop); each request
// re-sends the full context, so the turn's real cost is the SUM of every
// request's (input + cache + output), all priced together. usage.jsonl records
// are per-request and internally consistent, which is exactly what we need —
// unlike reply.json, whose input is a single-request snapshot while its output
// is accumulated, so pricing it directly wildly overstates the output share.
type usageTurnAgg struct {
	turnID      string
	model       string
	requests    int
	input       int
	output      int
	cacheRead   int
	cacheCreate int
	cost        float64
	costKnown   bool
}

// aggregateLatestTurn sums the most recent turn's per-request records. `recent`
// is newest-first; a turn's requests are contiguous at the front, so we collect
// while the turn id matches and stop at the first older turn. Empty turn ids
// (older logs) fall back to the single latest record so we never sum unrelated
// requests together.
func aggregateLatestTurn(recent []agentUsageLogRecord) usageTurnAgg {
	var agg usageTurnAgg
	if len(recent) == 0 {
		return agg
	}
	agg.turnID = recent[0].TurnID
	agg.costKnown = true
	for _, r := range recent {
		if r.TurnID != agg.turnID {
			break
		}
		agg.requests++
		agg.input += r.InputTokens
		agg.output += r.OutputTokens
		agg.cacheRead += r.CacheReadInputTokens
		agg.cacheCreate += r.CacheCreationInputTokens
		if agg.model == "" {
			agg.model = r.Model
		}
		fresh := r.InputTokens - r.CacheReadInputTokens - r.CacheCreationInputTokens
		if fresh < 0 {
			fresh = 0
		}
		c, known := estimateModelCostTokens(r.Model, fresh, r.CacheReadInputTokens, r.CacheCreationInputTokens, r.OutputTokens)
		agg.cost += c
		agg.costKnown = agg.costKnown && known
		if agg.turnID == "" { // can't group without an id → latest record only
			break
		}
	}
	agg.cost = round6(agg.cost)
	return agg
}

func parseUsageTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// buildUsageProjection computes the request cadence and average token buckets
// from the usage log (newest-first). With no usable history it returns a
// zero-cadence projection, so every per-suggestion saving falls back to 0
// (the UI then simply omits the money line instead of showing a fake number).
func buildUsageProjection(model string, recent []agentUsageLogRecord) usageProjection {
	proj := usageProjection{pricing: resolveModelPricing(model), windowReqs: len(recent)}
	if len(recent) == 0 {
		return proj
	}
	newest := parseUsageTS(recent[0].TS)
	oldest := parseUsageTS(recent[len(recent)-1].TS)
	if !newest.IsZero() && !oldest.IsZero() && newest.After(oldest) {
		proj.spanDays = newest.Sub(oldest).Hours() / 24.0
	}
	if proj.spanDays > 0 {
		// Honesty guard: never annualize a short burst to a 24/7 rate. If the
		// observed window is under a day (e.g. a 2-hour test session), treat it
		// as ONE active day's worth of work rather than extrapolating the burst
		// rate round-the-clock — that would massively overstate savings and burn
		// trust. The projection is then conservative (under-promises) and flagged
		// low-confidence so the UI can say so.
		effSpan := proj.spanDays
		if effSpan < 1.0 {
			effSpan = 1.0
			proj.lowConfidence = true
		}
		proj.reqPerMonth = float64(len(recent)) / effSpan * 30.0
	}
	var sf, sr, sw, so float64
	for _, r := range recent {
		fresh := r.InputTokens - r.CacheReadInputTokens - r.CacheCreationInputTokens
		if fresh < 0 {
			fresh = 0
		}
		sf += float64(fresh)
		sr += float64(r.CacheReadInputTokens)
		sw += float64(r.CacheCreationInputTokens)
		so += float64(r.OutputTokens)
	}
	n := float64(len(recent))
	proj.avgFresh = sf / n
	proj.avgCacheRead = sr / n
	proj.avgCacheWrite = sw / n
	proj.avgOutput = so / n
	return proj
}

// recurringMonthly is the monthly cost of `tokens` that persist in the context
// window and are re-read (cache hit) on every request.
func (p usageProjection) recurringMonthly(tokens float64) float64 {
	if p.reqPerMonth <= 0 || tokens <= 0 {
		return 0
	}
	return tokens / 1e6 * p.pricing.CacheRead * p.reqPerMonth
}

// savingsObj builds the per-suggestion money line. usdMonth==0 → returned as 0
// and the UI hides the line, so we never display a fabricated figure.
func savingsObj(usdMonth float64, basis string) M {
	return M{
		"usd_month": round6(usdMonth),
		"basis":     basis,
	}
}
