package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Provider balance / quota surfacing for the model picker.
//
// Capability is NOT uniform across providers:
//   - DeepSeek exposes a real balance endpoint (GET /user/balance) → show a number.
//   - Gemini has NO balance/quota API; the only signal is whether a model's
//     generateContent free tier is live (200) or exhausted / limit-0 (429). We
//     surface that as a per-model availability badge from a cheap, CACHED probe.
//   - Everything else: nothing to show.
//
// Probes are cached (balanceCacheTTL) so opening the picker repeatedly does not
// burn the (small) free quota or add latency.

type modelAvail struct {
	Model      string `json:"model"`
	Status     string `json:"status"`               // "ok" | "paid" | "quota" | "error"
	RetryAfter string `json:"retryAfter,omitempty"` // e.g. "38s" — when status=="quota"
}

// retryDelayRe pulls Google's RetryInfo.retryDelay ("38s") out of a 429 body.
var retryDelayRe = regexp.MustCompile(`"retryDelay"\s*:\s*"([^"]+)"`)

type providerBalanceResult struct {
	Provider string       `json:"provider"`
	Kind     string       `json:"kind"` // "balance" | "tier" | "none"
	OK       bool         `json:"ok"`
	Currency string       `json:"currency,omitempty"`
	Total    string       `json:"total,omitempty"`
	Tier     string       `json:"tier,omitempty"` // e.g. Gemini "free"
	Models   []modelAvail `json:"models,omitempty"`
	Error    string       `json:"error,omitempty"`
	CachedAt int64        `json:"cachedAt,omitempty"`
}

const balanceCacheTTL = 10 * time.Minute

var (
	balanceCacheMu sync.Mutex
	balanceCache   = map[string]providerBalanceResult{}
)

func balanceHTTPClient() *http.Client { return &http.Client{Timeout: 15 * time.Second} }

func writeBalanceJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// handleProviderBalance: GET /api/ai-gateway/provider-balance?provider=<key>
func handleProviderBalance(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.URL.Query().Get("provider"))
	if key == "" {
		http.Error(w, "missing provider", http.StatusBadRequest)
		return
	}
	pc, ok := loadProviderByKey(key)
	if !ok || pc == nil {
		writeBalanceJSON(w, providerBalanceResult{Provider: key, Kind: "none", OK: false, Error: "unknown provider"})
		return
	}

	host := providerHost(pc.URL)
	// Gemini availability is read from in-memory recorded traffic (instant, no
	// network) — never cache it, or freshly-recorded outcomes won't show.
	gemini := isGeminiFlavored(host, pc.DefaultModel)

	// Serve from cache (only the network-bound balance kinds) unless ?refresh=1.
	if !gemini && r.URL.Query().Get("refresh") != "1" {
		balanceCacheMu.Lock()
		if c, ok := balanceCache[key]; ok && time.Since(time.Unix(c.CachedAt, 0)) < balanceCacheTTL {
			balanceCacheMu.Unlock()
			writeBalanceJSON(w, c)
			return
		}
		balanceCacheMu.Unlock()
	}

	var res providerBalanceResult
	switch {
	// CiCy cloud gateway must win over the DeepSeek check: gateway providers
	// default to deepseek-* models, which isDeepSeekFlavored would match, and
	// the gateway has no /user/balance — its wallet lives at /api/balance.
	case isCicyGatewayHost(host):
		res = cicyGatewayBalance(pc)
	case isDeepSeekFlavored(host, pc.DefaultModel):
		res = deepSeekBalance(pc)
	case gemini:
		res = geminiAvailability(pc)
	default:
		res = providerBalanceResult{Provider: key, Kind: "none", OK: true}
	}
	res.Provider = key
	res.CachedAt = time.Now().Unix()

	if !gemini {
		balanceCacheMu.Lock()
		balanceCache[key] = res
		balanceCacheMu.Unlock()
	}
	writeBalanceJSON(w, res)
}

func providerHost(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return u.Host
	}
	return rawURL
}

// deepSeekBalance queries DeepSeek's real balance endpoint.
func deepSeekBalance(pc *providerConfig) providerBalanceResult {
	out := providerBalanceResult{Kind: "balance"}
	base := strings.TrimRight(pc.URL, "/")
	// pc.URL may carry a /v1 etc.; balance lives at the host root /user/balance.
	if u, err := url.Parse(pc.URL); err == nil && u.Host != "" {
		base = u.Scheme + "://" + u.Host
	}
	req, _ := http.NewRequest("GET", base+"/user/balance", nil)
	req.Header.Set("Authorization", "Bearer "+pc.APIKey)
	req.Header.Set("Accept", "application/json")
	resp, err := balanceHTTPClient().Do(req)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		out.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return out
	}
	var parsed struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency     string `json:"currency"`
			TotalBalance string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		out.Error = "parse: " + err.Error()
		return out
	}
	out.OK = true
	if len(parsed.BalanceInfos) > 0 {
		out.Currency = parsed.BalanceInfos[0].Currency
		out.Total = parsed.BalanceInfos[0].TotalBalance
	}
	return out
}

// isCicyGatewayHost reports whether the upstream is the CiCy cloud gateway
// (cicy-ai.com or any subdomain, e.g. gateway.cicy-ai.com).
func isCicyGatewayHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.Index(h, ":"); i >= 0 {
		h = h[:i]
	}
	return h == "cicy-ai.com" || strings.HasSuffix(h, ".cicy-ai.com")
}

// cicyGatewayBalance queries the CiCy cloud gateway wallet (GET /api/balance,
// authenticated by the provider's sk-cicy team key). Several provider entries
// (defaultAnthropic / defaultOpenAi …) typically share ONE gateway + key = ONE
// wallet, so results are cached per (base|key): opening a picker that lists
// many models/providers hits the cloud at most once per TTL.
var (
	cicyBalanceMu    sync.Mutex
	cicyBalanceCache = map[string]providerBalanceResult{}
)

func cicyGatewayBalance(pc *providerConfig) providerBalanceResult {
	out := providerBalanceResult{Kind: "balance", Currency: "USD"}
	base := strings.TrimRight(pc.URL, "/")
	if u, err := url.Parse(pc.URL); err == nil && u.Host != "" {
		base = u.Scheme + "://" + u.Host
	}
	ck := base + "|" + pc.APIKey
	cicyBalanceMu.Lock()
	if c, ok := cicyBalanceCache[ck]; ok && time.Since(time.Unix(c.CachedAt, 0)) < balanceCacheTTL {
		cicyBalanceMu.Unlock()
		return c
	}
	cicyBalanceMu.Unlock()

	req, _ := http.NewRequest("GET", base+"/api/balance", nil)
	req.Header.Set("Authorization", "Bearer "+pc.APIKey)
	req.Header.Set("Accept", "application/json")
	resp, err := balanceHTTPClient().Do(req)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != 200 {
		out.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return out
	}
	var parsed struct {
		Success    bool    `json:"success"`
		BalanceUSD float64 `json:"balance_usd"`
		Currency   string  `json:"currency"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || !parsed.Success {
		out.Error = "parse: unexpected response"
		return out
	}
	out.OK = true
	if parsed.Currency != "" {
		out.Currency = parsed.Currency
	}
	out.Total = strconv.FormatFloat(parsed.BalanceUSD, 'f', 2, 64)
	out.CachedAt = time.Now().Unix()
	cicyBalanceMu.Lock()
	cicyBalanceCache[ck] = out
	cicyBalanceMu.Unlock()
	return out
}

// geminiAvailability reports per-model availability WITHOUT probing — Gemini's
// free tier is tiny (e.g. 20 req/day for 2.5-flash), so a live probe would burn
// the very quota it measures. Instead we read outcomes RECORDED from real gateway
// traffic (recordGeminiOutcome, fed by ModifyResponse). A model not yet exercised
// shows "unknown" until its first real request.
func geminiAvailability(pc *providerConfig) providerBalanceResult {
	out := providerBalanceResult{Kind: "tier", Tier: "free", OK: true}
	rec := geminiRecordedOutcomes()
	for _, m := range pc.Models {
		if a, ok := rec[m]; ok {
			out.Models = append(out.Models, a)
		} else {
			out.Models = append(out.Models, modelAvail{Model: m, Status: "unknown"})
		}
	}
	return out
}

// ── Recorded Gemini outcomes (populated from real gateway traffic) ───────────

var (
	geminiOutcomeMu sync.Mutex
	geminiOutcome   = map[string]modelAvail{}
)

func recordGeminiOutcome(model string, m modelAvail) {
	if strings.TrimSpace(model) == "" {
		return
	}
	geminiOutcomeMu.Lock()
	geminiOutcome[model] = m
	geminiOutcomeMu.Unlock()
}

func geminiRecordedOutcomes() map[string]modelAvail {
	geminiOutcomeMu.Lock()
	defer geminiOutcomeMu.Unlock()
	out := make(map[string]modelAvail, len(geminiOutcome))
	for k, v := range geminiOutcome {
		out[k] = v
	}
	return out
}

// classifyGeminiStatus maps an upstream status+body to an availability verdict.
func classifyGeminiStatus(model string, status int, body []byte) modelAvail {
	a := modelAvail{Model: model}
	switch {
	case status >= 200 && status < 300:
		a.Status = "ok"
	case status == 429:
		if bytes.Contains(body, []byte("limit: 0")) {
			a.Status = "paid" // not offered on the free tier at all
		} else {
			a.Status = "quota" // free quota temporarily exhausted
			if mm := retryDelayRe.FindSubmatch(body); len(mm) == 2 {
				a.RetryAfter = string(mm[1])
			}
		}
	default:
		a.Status = "error"
	}
	return a
}

// modelFromRequest extracts the "model" field from a buffered request body.
func modelFromRequest(req *http.Request) string {
	if req == nil || req.GetBody == nil {
		return ""
	}
	b, err := req.GetBody()
	if err != nil {
		return ""
	}
	defer b.Close()
	data, _ := io.ReadAll(io.LimitReader(b, 1<<20))
	var m struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(data, &m)
	return m.Model
}

// geminiReadableError turns a raw Gemini error body into a one-line human message.
func geminiReadableError(model string, status int, body []byte) string {
	s := string(body)
	if status == 429 {
		if strings.Contains(s, "limit: 0") {
			return fmt.Sprintf("Gemini %s 不在免费档(每日额度为 0),需在 Google Cloud 项目开启 billing 才能使用。", model)
		}
		delay := ""
		if mm := retryDelayRe.FindStringSubmatch(s); len(mm) == 2 {
			delay = mm[1]
		}
		if delay != "" {
			return fmt.Sprintf("Gemini %s 免费额度已用尽(每日上限),约 %s 后(或太平洋时间次日午夜)恢复;建议开启 billing 提升额度。", model, delay)
		}
		return fmt.Sprintf("Gemini %s 免费额度已用尽;建议开启 billing。", model)
	}
	// Surface the upstream's own message when present (body may be array-wrapped).
	var ge struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if json.Unmarshal([]byte(trimmed), &arr) == nil && len(arr) > 0 {
			_ = json.Unmarshal(arr[0], &ge)
		}
	} else {
		_ = json.Unmarshal([]byte(trimmed), &ge)
	}
	if ge.Error.Message != "" {
		return fmt.Sprintf("Gemini %s 调用失败(HTTP %d):%s", model, status, ge.Error.Message)
	}
	return fmt.Sprintf("Gemini %s 调用失败(HTTP %d)。", model, status)
}

// handleGeminiResponseOutcome records availability from real traffic AND, for
// bridged Anthropic-Messages turns, rewrites a raw Gemini error body into a clean
// Anthropic-shaped error JSON (so claude renders a readable message instead of
// dumping the upstream's raw error as the assistant answer). No-op for non-Gemini.
func handleGeminiResponseOutcome(resp *http.Response) {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return
	}
	if !isGeminiFlavored(resp.Request.URL.Host, "") {
		return
	}
	model := modelFromRequest(resp.Request)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		recordGeminiOutcome(model, modelAvail{Model: model, Status: "ok"})
		return
	}
	// Error: read the (small) body, record the verdict.
	var buf []byte
	if resp.Body != nil {
		buf, _ = io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		resp.Body.Close()
	}
	recordGeminiOutcome(model, classifyGeminiStatus(model, resp.StatusCode, buf))
	if resp.Request.Header.Get(cicyAdaptMessagesHeader) != "1" {
		resp.Body = io.NopCloser(bytes.NewReader(buf)) // restore consumed body
		return
	}
	etype := "api_error"
	switch resp.StatusCode {
	case 429:
		etype = "rate_limit_error"
	case 400, 403:
		etype = "invalid_request_error"
	}
	clean, _ := json.Marshal(map[string]interface{}{
		"type":  "error",
		"error": map[string]interface{}{"type": etype, "message": geminiReadableError(model, resp.StatusCode, buf)},
	})
	resp.Body = io.NopCloser(bytes.NewReader(clean))
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Del("Content-Length")
	resp.ContentLength = int64(len(clean))
}
