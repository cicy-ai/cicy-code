package main

import "strings"

// Token pricing model. All values are USD per 1,000,000 tokens and ship as
// COMPLETE built-in defaults — cost works out of the box with no config. Prompt
// caching is priced correctly per tier (cache reads are ~10× cheaper than fresh
// input; cache writes ~1.25× of input), which matters enormously for the
// long-lived, high-cache-hit workloads these agents run.
type modelPricing struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Known      bool
}

// Matched most-specific-first (ordered slice, NOT a map — map iteration order is
// random in Go and would make matching non-deterministic). Keys are matched as
// lowercase substrings of the model id.
var builtinModelPricing = []struct {
	key string
	p   modelPricing
}{
	// Anthropic Claude — confirmed from anthropic.com/pricing (USD per 1M tokens):
	//   tier        input  output  cache-write  cache-read
	//   Fable 5     $10    $50     $12.50       $1.00   (5-min write tier; 1M ctx no surcharge)
	//   Opus 4.8    $5     $25     $6.25        $0.50
	//   Sonnet 4.6  $3     $15     $3.75        $0.30
	//   Haiku 4.5   $1     $5      $1.25        $0.10
	// Fable 5 source: anthropic.com/news/claude-fable-5-mythos-5 (2026-06-09);
	// Mythos 5 ships at the same $10/$50 so the "fable"/"mythos" aliases share it.
	// Version-specific keys sit ABOVE the generic "claude-{tier}-4" substrings so
	// the current flagships price exactly; bare aliases map to today's flagship.
	// (modelPricing field order is Input, Output, CacheRead, CacheWrite.)
	{"claude-fable-5", modelPricing{10, 50, 1, 12.5, true}},
	{"claude-opus-4-8", modelPricing{5, 25, 0.5, 6.25, true}},
	{"claude-sonnet-4-6", modelPricing{3, 15, 0.3, 3.75, true}},
	{"claude-haiku-4-5", modelPricing{1, 5, 0.1, 1.25, true}},
	{"fable", modelPricing{10, 50, 1, 12.5, true}},
	{"mythos", modelPricing{10, 50, 1, 12.5, true}},
	{"opus", modelPricing{5, 25, 0.5, 6.25, true}},
	{"sonnet", modelPricing{3, 15, 0.3, 3.75, true}},
	{"haiku", modelPricing{1, 5, 0.1, 1.25, true}},
	// DeepSeek (cheap; cache read ~0.1× input)
	{"deepseek", modelPricing{0.55, 2.19, 0.07, 0.55, true}},
	// OpenAI (cached input ~0.1× of input; no separate cache-write tier → = input)
	{"gpt-5", modelPricing{5, 15, 0.5, 5, true}},
	{"gpt-4.1", modelPricing{2, 8, 0.5, 2, true}},
	{"gpt-4o", modelPricing{2.5, 10, 1.25, 2.5, true}},
	{"gpt-4", modelPricing{2.5, 10, 1.25, 2.5, true}},
	{"o3", modelPricing{2, 8, 0.5, 2, true}},
	{"o1", modelPricing{15, 60, 7.5, 15, true}},
	// Others (reasonable public-rate approximations)
	{"gemini", modelPricing{1.25, 5, 0.31, 1.25, true}},
	{"qwen", modelPricing{0.4, 1.2, 0.05, 0.4, true}},
	{"kimi", modelPricing{0.6, 2.5, 0.15, 0.6, true}},
	{"glm", modelPricing{0.5, 2, 0.1, 0.5, true}},
}

// fallbackModelPricing represents "no template / price not known". We do NOT
// guess a cost for unknown models — Known=false signals the caller to skip cost
// entirely and show token consumption only. Rates are zero so any accidental
// use yields 0, never a fabricated estimate.
var fallbackModelPricing = modelPricing{0, 0, 0, 0, false}

func resolveModelPricing(model string) modelPricing {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return fallbackModelPricing
	}
	for _, e := range builtinModelPricing {
		if strings.Contains(m, e.key) {
			return e.p
		}
	}
	return fallbackModelPricing
}

// estimateModelCostTokens prices already-counted tokens with cache-aware tiers.
// Inputs are the SEPARATED buckets (Anthropic semantics, which the gateway
// normalizes to and reply.json stores as typed fields): fresh = non-cached
// input, cacheRead = cache hits, cacheWrite = cache writes, output. Returns
// (costUSD, pricingKnown). This is the single source of truth for cost — both
// the gateway's per-request estimate and the analysis report call it, so we
// never charge cache reads at full input price. Models without a confirmed
// price template return (0, false): no cost is computed, only tokens are shown.
func estimateModelCostTokens(model string, fresh, cacheRead, cacheWrite, output int) (float64, bool) {
	p := resolveModelPricing(model)
	if !p.Known {
		return 0, false
	}
	cost := float64(fresh)/1e6*p.Input +
		float64(cacheRead)/1e6*p.CacheRead +
		float64(cacheWrite)/1e6*p.CacheWrite +
		float64(output)/1e6*p.Output
	cost = float64(int(cost*1e6+0.5)) / 1e6
	return cost, p.Known
}

func round6(v float64) float64 { return float64(int(v*1e6+0.5)) / 1e6 }

// costBreakdown splits a request's cost into its billing buckets so the report
// can show, concretely, that (e.g.) output dominates while cache reads are
// nearly free. Each component: {key, label, tokens, rate (USD/1M), cost, pct}.
func costBreakdown(model string, fresh, cacheRead, cacheWrite, output int) M {
	p := resolveModelPricing(model)
	if !p.Known {
		// No confirmed price template → no cost breakdown (tokens-only display).
		return M{"available": false, "pricing_known": false}
	}
	type comp struct {
		key, label string
		tokens     int
		rate, cost float64
	}
	comps := []comp{
		{"fresh_input", "新增输入", fresh, p.Input, round6(float64(fresh) / 1e6 * p.Input)},
		{"cache_read", "缓存读取", cacheRead, p.CacheRead, round6(float64(cacheRead) / 1e6 * p.CacheRead)},
		{"cache_write", "缓存写入", cacheWrite, p.CacheWrite, round6(float64(cacheWrite) / 1e6 * p.CacheWrite)},
		{"output", "输出", output, p.Output, round6(float64(output) / 1e6 * p.Output)},
	}
	total := 0.0
	for _, c := range comps {
		total += c.cost
	}
	total = round6(total)
	items := make([]M, 0, len(comps))
	for _, c := range comps {
		pct := 0.0
		if total > 0 {
			pct = c.cost / total
		}
		items = append(items, M{
			"key": c.key, "label": c.label, "tokens": c.tokens,
			"rate": c.rate, "cost": c.cost, "pct": pct,
		})
	}
	return M{"available": true, "total": total, "pricing_known": p.Known, "components": items}
}
