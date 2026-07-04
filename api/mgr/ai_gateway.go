package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	// aliased: handleAIGatewayProxy has a local var named `audit`, so the
	// package can't keep its default name in this file.
	auditpkg "ttyd-go/mgr/audit"
)

const (
	// 5 attempts (4 retries) — needed because cicyAi's thinking-mode race
	// has empirically ~50% failure rate per call, which would still surface
	// a user-visible error at 3 attempts (1 - 0.5^3 = 87.5% success). At 5
	// attempts we cover 1 - 0.5^5 ≈ 96.9%. 429/5xx retries also benefit.
	aiGatewayMaxAttempts = 5
)

func aiGatewayProxyBaseURL(provider string, agentID string) (*url.URL, error) {
	cfg, _, err := resolveRuntimeAIConfigForAgent(provider, agentID)
	if err != nil {
		return nil, err
	}
	raw := ""
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		raw = strings.TrimSpace(cfg.APIURL)
		if raw == "" {
			raw = "http://2000.run:6543/v1"
		}
	case "anthropic":
		raw = strings.TrimSpace(cfg.AnthropicURL)
		if raw == "" {
			raw = "http://2000.run:6543"
		}
	case "gemini":
		// Google GenAI passthrough — gemini-cli's GOOGLE_GEMINI_BASE_URL lands here.
		raw = strings.TrimSpace(cfg.GeminiURL)
		if raw == "" {
			raw = "https://generativelanguage.googleapis.com"
		}
	}
	if raw == "" {
		return nil, nil
	}
	return url.Parse(raw)
}

func aiGatewayProxyAPIKey(provider string, agentID string) string {
	cfg, _, err := resolveRuntimeAIConfigForAgent(provider, agentID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.APIKey)
}

// aiGatewayEgressProxy returns the proxy URL to route this agent's UPSTREAM
// traffic through, or "" for direct. Honors the provider's explicit egressProxy
// config; otherwise Gemini defaults to the local mihomo mixed listener
// (socks5://127.0.0.1:9001) because Gemini geo-blocks requests from unsupported
// regions (400 "User location is not supported") and the gateway host may sit in
// one — the mihomo exit lands in a supported region.
func aiGatewayEgressProxy(provider, agentID, upstreamHost string) string {
	if cfg, _, err := resolveRuntimeAIConfigForAgent(provider, agentID); err == nil {
		if pc, ok := loadProviderByKey(cfg.Provider); ok && pc != nil {
			if p := strings.TrimSpace(pc.EgressProxy); p != "" {
				return p
			}
		}
	}
	if isGeminiFlavored(upstreamHost, "") {
		return "socks5://127.0.0.1:9001"
	}
	return ""
}

// cicyCloudGatewayHost reports whether the upstream host is a cicy cloud AI
// gateway (gateway.cicy-ai.com and any *.cicy-ai.com / cicy-ai.com), which
// authenticates the team token via Authorization: Bearer rather than x-api-key.
func cicyCloudGatewayHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host == "cicy-ai.com" || strings.HasSuffix(host, ".cicy-ai.com")
}

func isLoopbackRemote(addr string) bool {
	host := addr
	if parsedHost, _, err := net.SplitHostPort(addr); err == nil {
		host = parsedHost
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func joinURLPath(basePath, suffix string) string {
	basePath = strings.TrimRight(basePath, "/")
	suffix = "/" + strings.TrimLeft(suffix, "/")
	if basePath == "" {
		return suffix
	}
	return basePath + suffix
}

func resolveOpenClawProviderTargetPath(basePath, suffix string) string {
	basePath = strings.TrimRight(basePath, "/")
	if basePath != "" && (suffix == basePath || strings.HasPrefix(suffix, basePath+"/")) {
		return suffix
	}
	return joinURLPath(basePath, suffix)
}

func normalizeAIGatewayProvider(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "openai", "responses", "openai-responses":
		return "openai"
	case "anthropic", "messages", "anthropic-messages":
		return "anthropic"
	case "gemini", "google", "google-genai":
		return "gemini"
	default:
		return ""
	}
}

func parseAIGatewayPath(path string) (provider string, agentID string, suffix string, ok bool) {
	const prefix = "/api/ai-gateway/"
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return "", "", "", false
	}
	parts := strings.SplitN(strings.TrimLeft(rest, "/"), "/", 3)
	if len(parts) < 2 {
		return "", "", "", false
	}
	provider = normalizeAIGatewayProvider(parts[0])
	agentID = strings.TrimSpace(parts[1])
	if provider == "" || agentID == "" {
		return "", "", "", false
	}
	suffix = "/"
	if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
		suffix = "/" + strings.TrimLeft(parts[2], "/")
	}
	return provider, agentID, suffix, true
}

func stripOpenAIFingerprintHeaders(req *http.Request) {
	for _, name := range []string{
		"X-Stainless-Arch",
		"X-Stainless-Async",
		"X-Stainless-Helper-Method",
		"X-Stainless-Lang",
		"X-Stainless-Os",
		"X-Stainless-Package-Version",
		"X-Stainless-Raw-Response",
		"X-Stainless-Retry-Count",
		"X-Stainless-Runtime",
		"X-Stainless-Runtime-Version",
	} {
		req.Header.Del(name)
	}
}

func stripAIGatewayClientAuthHeaders(req *http.Request) {
	for _, name := range []string{
		"Authorization",
		"X-API-Key",
		"Api-Key",
	} {
		req.Header.Del(name)
	}
}

type aiGatewayRetryTransport struct {
	base     http.RoundTripper
	provider string
	agentID  string
}

func (t *aiGatewayRetryTransport) roundTripper() http.RoundTripper {
	if t != nil && t.base != nil {
		return t.base
	}
	return http.DefaultTransport
}

func aiGatewayShouldRetryStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// aiGatewayBodyLooksLikeUpstreamFlake reads a (small, error-shaped) response
// body and checks for known upstream-flake fingerprints we want to retry past.
// Returns the buffered body so the caller can either discard it (for retry) or
// reattach it to the response (no match → pass error through to the client).
//
// Currently covered:
//   - cicyAi/new-api's thinking-mode race: same body returns
//     `content[].thinking in the thinking mode must be passed back` randomly
//     ~half the time even when the prior assistant has a valid
//     reasoning_content. Empirically a 3rd / 4th replay succeeds.
func aiGatewayBodyLooksLikeUpstreamFlake(resp *http.Response) (bool, []byte) {
	if resp == nil || resp.Body == nil {
		return false, nil
	}
	// Cap at 16 KiB — these errors are tiny JSON objects, anything larger
	// almost certainly isn't a flake we should silently retry.
	const limit = 16 * 1024
	buf, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return false, buf
	}
	body := strings.ToLower(string(buf))
	if strings.Contains(body, "thinking mode must be passed back") {
		return true, buf
	}
	return false, buf
}

// aiGatewayModelFromRequestBody reads the (already-transformed) outgoing request
// body and returns its "model" field — used to learn which model the upstream
// just demanded a reasoning_content passback for. Bounded read; "" on any miss.
func aiGatewayModelFromRequestBody(req *http.Request) string {
	if req == nil || req.GetBody == nil {
		return ""
	}
	rc, err := req.GetBody()
	if err != nil {
		return ""
	}
	defer rc.Close()
	buf, err := io.ReadAll(io.LimitReader(rc, 1*1024*1024))
	if err != nil {
		return ""
	}
	var m map[string]interface{}
	if json.Unmarshal(buf, &m) != nil {
		return ""
	}
	s, _ := m["model"].(string)
	return strings.TrimSpace(s)
}

func aiGatewayRetryDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if value := strings.TrimSpace(resp.Header.Get("Retry-After")); value != "" {
			if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
				return time.Duration(seconds) * time.Second
			}
			if retryAt, err := http.ParseTime(value); err == nil {
				if delay := time.Until(retryAt); delay > 0 {
					return delay
				}
			}
		}
	}
	switch attempt {
	case 1:
		return 350 * time.Millisecond
	case 2:
		return 1100 * time.Millisecond
	default:
		return 2 * time.Second
	}
}

func aiGatewaySleep(ctxDone <-chan struct{}, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctxDone:
		return false
	case <-timer.C:
		return true
	}
}

func cloneRetryableRequest(req *http.Request) (*http.Request, error) {
	cloned := req.Clone(req.Context())
	if req.Body == nil {
		return cloned, nil
	}
	if req.GetBody == nil {
		return nil, http.ErrNotSupported
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	cloned.Body = body
	return cloned, nil
}

func (t *aiGatewayRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt := t.roundTripper()
	var lastErr error
	for attempt := 1; attempt <= aiGatewayMaxAttempts; attempt++ {
		attemptReq, err := cloneRetryableRequest(req)
		if err != nil {
			return nil, err
		}
		resp, err := rt.RoundTrip(attemptReq)
		if err != nil {
			lastErr = err
			if attempt >= aiGatewayMaxAttempts {
				return nil, err
			}
			delay := aiGatewayRetryDelay(nil, attempt)
			log.Printf("[ai-gateway] retrying provider=%s agent=%s path=%s after transport error: %v (attempt %d/%d, wait=%s)", t.provider, t.agentID, req.URL.Path, err, attempt+1, aiGatewayMaxAttempts, delay)
			if !aiGatewaySleep(req.Context().Done(), delay) {
				return nil, req.Context().Err()
			}
			continue
		}
		// Beyond the usual 429/5xx, retry 4xx when the body looks like a
		// known upstream-flake signature (cicyAi's thinking-mode race —
		// identical body succeeds on retry).
		shouldRetry := aiGatewayShouldRetryStatus(resp.StatusCode)
		var flakeBody []byte
		if !shouldRetry && resp.StatusCode >= 400 && resp.StatusCode < 500 {
			isFlake, buf := aiGatewayBodyLooksLikeUpstreamFlake(resp)
			flakeBody = buf
			if isFlake {
				// "reasoning_content … must be passed back": split by whether we ALREADY
				// treated this model as DeepSeek (and thus echoed reasoning_content):
				//  - NOT flavored → we sent NO reasoning_content → systematic (an opaque
				//    DeepSeek alias). Retrying the same body won't help. Learn the model
				//    + surface the 400 so the turn re-runs and re-transforms WITH the
				//    passback armed (isDeepSeekFlavored now true).
				//  - ALREADY flavored → we DID echo reasoning_content → this is the
				//    genuine cicyAi thinking-mode race (identical body succeeds on
				//    replay) → retry as before. (Critically, deepseek-* models match by
				//    name and are NOT in the learned set — they must still hit retry.)
				model := aiGatewayModelFromRequestBody(attemptReq)
				if model != "" && !isDeepSeekFlavored(attemptReq.URL.Host, model) {
					markDeepseekReasoningModel(model)
					log.Printf("[ai-gateway] learned reasoning-passback model=%s from upstream 400 provider=%s agent=%s — surfacing 400 so the turn re-runs with reasoning_content", model, t.provider, t.agentID)
				} else {
					shouldRetry = true
					log.Printf("[ai-gateway] upstream flake detected provider=%s agent=%s status=%d — will retry", t.provider, t.agentID, resp.StatusCode)
				}
			}
		}
		if !shouldRetry || attempt >= aiGatewayMaxAttempts {
			if flakeBody != nil {
				// We already drained the body to peek — restore it for the caller.
				resp.Body.Close()
				resp.Body = io.NopCloser(bytes.NewReader(flakeBody))
				resp.ContentLength = int64(len(flakeBody))
			}
			return resp, nil
		}
		delay := aiGatewayRetryDelay(resp, attempt)
		log.Printf("[ai-gateway] retrying provider=%s agent=%s path=%s after upstream status=%d (attempt %d/%d, wait=%s)", t.provider, t.agentID, req.URL.Path, resp.StatusCode, attempt+1, aiGatewayMaxAttempts, delay)
		if flakeBody == nil {
			io.Copy(io.Discard, resp.Body)
		}
		resp.Body.Close()
		if !aiGatewaySleep(req.Context().Done(), delay) {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, req.Context().Err()
		}
	}
	return nil, lastErr
}

func newAIGatewayReverseProxy(targetBase *url.URL, suffix string, provider string, agentID string, audit *aiGatewayAuditSession) *httputil.ReverseProxy {
	target := &url.URL{Scheme: targetBase.Scheme, Host: targetBase.Host}
	proxy := httputil.NewSingleHostReverseProxy(target)
	// Route upstream traffic through a per-provider egress proxy when configured
	// (e.g. Gemini → local mihomo US exit to satisfy its region restriction).
	var baseTransport http.RoundTripper = http.DefaultTransport
	if ep := aiGatewayEgressProxy(provider, agentID, targetBase.Host); ep != "" {
		if pu, perr := url.Parse(ep); perr == nil && pu.Host != "" {
			baseTransport = &http.Transport{
				Proxy:                 http.ProxyURL(pu),
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			}
		} else {
			log.Printf("[ai-gateway] bad egressProxy %q for agent=%s: %v", ep, agentID, perr)
		}
	}
	proxy.Transport = &aiGatewayRetryTransport{
		base:     baseTransport,
		provider: provider,
		agentID:  agentID,
	}
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		req.URL.Path = resolveOpenClawProviderTargetPath(targetBase.Path, suffix)
		req.URL.RawPath = ""
		req.Host = targetBase.Host
		req.Header.Set("X_AGENT_ID", agentID)
		// Internal audit markers — never forward upstream.
		req.Header.Del("X-Cicy-Aux")
		req.Header.Del("X-Cicy-Current-Owned")
		// Drop the client's Accept-Encoding so Go's Transport negotiates gzip
		// itself and transparently DECOMPRESSES the response. With the client
		// header passed through verbatim, upstreams (api.anthropic.com) gzip
		// the SSE stream and the audit tee reads compressed bytes — no "data:"
		// lines parse, so reply.json stays empty and no ai_chunk/thinking_chunk
		// events reach chat-WS subscribers (found 2026-06-05 during the mobile
		// push-stream alignment).
		req.Header.Del("Accept-Encoding")
		stripAIGatewayClientAuthHeaders(req)
		switch provider {
		case "openai":
			stripOpenAIFingerprintHeaders(req)
			if apiKey := aiGatewayProxyAPIKey(provider, agentID); apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+apiKey)
			}
		case "anthropic":
			if apiKey := aiGatewayProxyAPIKey(provider, agentID); apiKey != "" {
				// Bridged case: a cicy/claude agent rides the /anthropic endpoint but the
				// EFFECTIVE upstream is openai-protocol (e.g. opencodeZen → opencode.ai/zen);
				// shouldAdaptAnthropicToChatCompletions translates the body to chat/completions.
				// That upstream authenticates via `Authorization: Bearer`, NOT x-api-key — so a
				// bridged turn was getting 401 "Missing API key". When bridged, send Bearer
				// (the auth must follow the bridge, like resolveRuntimeAIConfigForAgent does).
				//
				// Trust the cicyAdaptMessagesHeader the handler stamps when it actually
				// bridged — re-deriving via shouldAdapt here is unreliable because `suffix`
				// has already been rewritten from /messages to /chat/completions, so the
				// /messages check fails for non-DeepSeek openai upstreams (e.g. Gemini),
				// which would then wrongly get x-api-key and a 400 "Missing Authorization".
				if req.Header.Get(cicyAdaptMessagesHeader) == "1" ||
					shouldAdaptAnthropicToChatCompletions(provider, agentID, targetBase.Host, targetBase.Path, suffix) {
					req.Header.Set("Authorization", "Bearer "+apiKey)
					break
				}
				req.Header.Set("x-api-key", apiKey)
				// cicy cloud gateways (gateway.cicy-ai.com / *.cicy-ai.com) authenticate
				// the team token via Authorization: Bearer only (cloud getToken reads
				// Bearer, not x-api-key) — unlike direct anthropic/deepseek upstreams,
				// which read x-api-key. Send Bearer too when the upstream is a cicy cloud
				// host so the same anthropic config authenticates against either; direct
				// providers ignore the extra header. (Without this, every cicy/claude
				// agent riding gateway.cicy-ai.com gets 401 ai_gateway_unauthorized.)
				if cicyCloudGatewayHost(targetBase.Host) {
					req.Header.Set("Authorization", "Bearer "+apiKey)
				}
			}
		case "gemini":
			// Google GenAI auth header; replace the client's local placeholder key
			// with the configured provider key (passthrough, no body translation).
			if apiKey := aiGatewayProxyAPIKey(provider, agentID); apiKey != "" {
				req.Header.Set("x-goog-api-key", apiKey)
			}
		}
		if audit != nil {
			audit.recordOutboundRequest(req)
		}
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp == nil {
			return nil
		}
		if resp.Body != nil && resp.Request != nil &&
			resp.Request.Header.Get(cicyAdaptResponsesHeader) == "1" &&
			resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// Wrap upstream Chat Completions SSE into a Responses event stream
			// before the audit reader so codex consumes the translated bytes.
			src := resp.Body
			if gatewayDebugResponses() {
				log.Printf("[responses-debug] agent=%s upstream status=%d content-type=%q", agentID, resp.StatusCode, resp.Header.Get("Content-Type"))
				src = newDebugTeeReadCloser(src, "agent="+agentID+" upstream-sse")
			}
			// Format-detecting: pass an already-Responses upstream stream through,
			// only convert genuine chat.completion.chunk streams.
			resp.Body = newCodexResponsesReader(src)
			resp.Header.Set("Content-Type", "text/event-stream; charset=utf-8")
			resp.Header.Del("Content-Length")
			resp.ContentLength = -1
		}
		if resp.Body != nil && resp.Request != nil &&
			resp.Request.Header.Get(cicyAdaptMessagesHeader) == "1" &&
			resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// Same direction for claude: Chat Completions SSE → Anthropic Messages.
			resp.Body = newChatCompletionsToMessagesReader(resp.Body, "")
			resp.Header.Set("Content-Type", "text/event-stream; charset=utf-8")
			resp.Header.Del("Content-Length")
			resp.ContentLength = -1
		}
		// CiCy cloud gateway 402 (insufficient_balance): rewrite the terse upstream
		// body into a user-facing "请充值" message so the riding agent surfaces it.
		rewriteCicyGateway402(resp)
		// Gemini: record per-model availability from real traffic (so the picker
		// never has to probe + burn the tiny free quota) and rewrite raw upstream
		// error bodies into clean Anthropic errors. No-op for non-Gemini / 2xx-body.
		handleGeminiResponseOutcome(resp)
		if audit == nil || resp.Body == nil {
			return nil
		}
		audit.recordOutboundRequest(resp.Request)
		resp.Body = newAIGatewayAuditReadCloser(resp.Body, audit, resp.StatusCode, resp.Header.Clone(), resp.ContentLength)
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if audit != nil {
			audit.completeFromError(err)
		}
		httpErr(w, 502, "ai_gateway_proxy_upstream_error")
	}
	return proxy
}

// rewriteCicyGateway402 turns the CiCy cloud gateway's 402 insufficient_balance
// response into a clear, user-facing error telling the user to top up. Only
// fires for cicy-ai.com upstreams; other providers' 402s pass through untouched.
// The body is shaped Anthropic-style for /messages callers and OpenAI-style for
// everything else, so claude-code / codex render the message verbatim.
func rewriteCicyGateway402(resp *http.Response) {
	if resp == nil || resp.StatusCode != http.StatusPaymentRequired || resp.Request == nil || resp.Request.URL == nil {
		return
	}
	host := strings.ToLower(resp.Request.URL.Hostname())
	if host != "cicy-ai.com" && !strings.HasSuffix(host, ".cicy-ai.com") {
		return
	}
	if resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}
	msg := "CiCy 云网关:团队钱包余额不足,AI 调用已暂停。请前往 https://cicy-ai.com/dash 充值后重试。 " +
		"(CiCy gateway: insufficient balance — top up at https://cicy-ai.com/dash and retry.)"
	var payload any
	if strings.Contains(resp.Request.URL.Path, "/messages") {
		payload = map[string]any{"type": "error", "error": map[string]any{"type": "payment_required", "message": msg}}
	} else {
		payload = map[string]any{"error": map[string]any{"message": msg, "type": "insufficient_quota", "code": "insufficient_balance"}}
	}
	body, _ := json.Marshal(payload)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Del("Content-Encoding")
}

// aiGatewayActiveProvider returns the providerConfig currently routing for this
// agent: the per-pane runtime_ai override if set, else the agent_type default.
// Returns nil when no provider can be resolved (legacy/empty config).
func aiGatewayActiveProvider(agentID string) *providerConfig {
	key := ""
	if agentType := loadPaneAgentType(agentID); agentType != "" {
		key = loadDefaultProviderKeyForAgentType(agentType)
	}
	if ov, _ := loadPaneRuntimeAIOverride(agentID); ov != nil && strings.TrimSpace(ov.ProviderName) != "" {
		key = ov.ProviderName
	}
	if key == "" {
		return nil
	}
	if p, ok := loadProviderByKey(key); ok {
		return p
	}
	return nil
}

// applyGatewayModelMapping rewrites the "model" field of a JSON request body
// using the active provider's modelMapping. No-op when there's no mapping, no
// model field, or the mapped value is unchanged.
func applyGatewayModelMapping(agentID string, body []byte) []byte {
	p := aiGatewayActiveProvider(agentID)
	if p == nil || len(p.ModelMapping) == 0 {
		return body
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	cur, ok := m["model"].(string)
	if !ok || cur == "" {
		return body
	}
	mapped := p.applyModelMapping(cur)
	if mapped == cur {
		return body
	}
	m["model"] = mapped
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	log.Printf("[ai-gateway] %s model remapped %s -> %s", agentID, cur, mapped)
	return out
}

// coerceModelToProvider guarantees the outbound "model" is one the active
// provider actually serves. The UI model picker can leave a model selected that
// belongs to a DIFFERENT provider — e.g. opencodeZen's "north-mini-code-free"
// lingering after the provider was switched to DeepSeek — and forwarding that
// foreign model makes the upstream 400 ("supported API model names are
// deepseek-v4-pro or deepseek-v4-flash, but you passed north-mini-code-free").
//
// Runs AFTER applyGatewayModelMapping + the pane-default override (so it sees the
// final model): take the current model, apply the provider's mapping, and if the
// provider declares a model list that still doesn't contain it, fall back to the
// provider's defaultModel. No-op when the provider declares no model list.
func coerceModelToProvider(agentID string, body []byte) []byte {
	p := aiGatewayActiveProvider(agentID)
	if p == nil || len(p.Models) == 0 {
		return body
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	cur, ok := m["model"].(string)
	if !ok || cur == "" {
		return body
	}
	final := p.coerceModel(cur)
	if final == cur {
		return body
	}
	m["model"] = final
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	log.Printf("[ai-gateway] %s model %q not served by provider (models=%v) → coerced to %q", agentID, cur, p.Models, final)
	return out
}

func handleAIGatewayProxy(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		httpErr(w, 403, "ai_gateway_proxy_loopback_only")
		return
	}

	provider, agentID, suffix, ok := parseAIGatewayPath(r.URL.Path)
	if !ok {
		httpErr(w, 404, "ai_gateway_proxy_path_invalid")
		return
	}

	targetBase, err := aiGatewayProxyBaseURL(provider, agentID)
	if errors.Is(err, ErrRuntimeAIProviderMismatch) {
		httpErr(w, 409, "ai_gateway_provider_mismatch")
		return
	}
	if errors.Is(err, ErrRuntimeAIProviderNotFound) {
		httpErr(w, 404, "ai_gateway_provider_not_found")
		return
	}
	if err != nil || targetBase == nil || targetBase.Scheme == "" || targetBase.Host == "" {
		httpErr(w, 503, "ai_gateway_proxy_target_invalid")
		return
	}

	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		httpErr(w, 400, "ai_gateway_proxy_read_body_failed")
		return
	}
	// Apply the active provider's model mapping so models the upstream relay
	// has no channel for (a newer claude-opus-4-8, Claude Code's background
	// claude-sonnet-* small model, …) get rewritten onto a model it serves.
	//
	// gemini is a pure Google GenAI passthrough — its body uses Google's schema
	// (contents/parts), NOT Anthropic/OpenAI, so this rewrite chain (model mapping,
	// enable_thinking injection) would corrupt it. Google rejects the foreign keys
	// outright (`Unknown name "enable_thinking"`), breaking real gemini-cli
	// multi-turn. Skip the entire chain for gemini.
	//
	// NOTE: the old "greeting strip" (delete system+tools when the last message is
	// "hi") was removed — it left the model with no persona and no tools, so a
	// bare "hi" produced rambling/garbage (e.g. deepseek emitting a synthetic
	// thinking block). A request must always carry its system prompt + tools.
	if provider != "gemini" {
		requestBody = applyGatewayModelMapping(agentID, requestBody)
		requestBody = agentInspectorRewriteRequestBody(provider, agentID, requestBody, targetBase.Host)
		// Last line of defense: never forward a model the active provider doesn't
		// serve (UI can leave a foreign model selected after a provider switch).
		requestBody = coerceModelToProvider(agentID, requestBody)
	}

	// Codex (Responses API) → Chat Completions adaptation: only api.openai.com
	// natively serves /v1/responses; for any other upstream we translate the
	// request body + path, and ModifyResponse wraps the SSE stream the other
	// way. Header marks the request so ModifyResponse knows to wrap.
	if shouldAdaptForCodexResponses(targetBase.Host, suffix) {
		if newBody, _, err := transformResponsesRequestToChatCompletions(requestBody, targetBase.Host); err == nil {
			if gatewayDebugResponses() {
				log.Printf("[responses-debug] agent=%s host=%s transformed_chat_body=%s", agentID, targetBase.Host, truncForDebug(newBody, 3000))
			}
			requestBody = newBody
			suffix = rewriteSuffixForChatCompletions(suffix)
			r.Header.Set(cicyAdaptResponsesHeader, "1")
		} else {
			log.Printf("[ai-gateway] codex responses->chat translation failed for %s: %v", agentID, err)
		}
	}

	// Anthropic Messages → Chat Completions adaptation: translate request + wrap SSE
	// both ways. Fires for (a) DeepSeek hosts that only speak Chat Completions, and
	// (b) ANY openai-protocol provider configured behind the /anthropic endpoint —
	// this is what lets the cicy agent (which always speaks Anthropic Messages) ride
	// on either an anthropic- OR an openai-protocol provider: the gateway bridges the
	// request down to Chat Completions and wraps the SSE back up to Anthropic Messages,
	// so the agent's consumption path stays single-format. Native anthropic upstreams
	// (provider protocol "anthropic", or DeepSeek's /anthropic passthrough) skip this.
	if shouldAdaptAnthropicToChatCompletions(provider, agentID, targetBase.Host, targetBase.Path, suffix) {
		if newBody, _, err := transformMessagesRequestToChatCompletions(requestBody, targetBase.Host); err == nil {
			requestBody = newBody
			suffix = rewriteSuffixForDeepSeekChatCompletionsFromMessages(suffix)
			// Gemini's OpenAI-compat base path already carries the API version
			// (provider url = …/v1beta/openai). The client's suffix is /v1/messages,
			// rewritten to /v1/chat/completions; joined onto the base that becomes
			// …/v1beta/openai/v1/chat/completions — a bad path Gemini rejects with a
			// misleading 400 "Missing Authorization header". Collapse to the bare
			// /chat/completions so the join yields …/v1beta/openai/chat/completions.
			if isGeminiFlavored(targetBase.Host, "") && strings.HasSuffix(suffix, "/chat/completions") {
				suffix = "/chat/completions"
			}
			r.Header.Set(cicyAdaptMessagesHeader, "1")
		} else {
			log.Printf("[ai-gateway] messages->chat translation failed for %s: %v", agentID, err)
		}
	}

	r.Body = io.NopCloser(bytes.NewReader(requestBody))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(requestBody)), nil
	}
	r.ContentLength = int64(len(requestBody))

	// Inline preventive interception (outbound). Scan what the agent is about to
	// send to the model; if a block-action rule fires, refuse to forward (403,
	// see writeGatewayBlocked). The block event is recorded inside PreventiveCheck
	// (submitPreventiveBlock) WITH conversation/turn/history identity so the audit
	// log can answer "哪个会话/哪条消息"; PreventiveCheck short-circuits cheaply
	// when no block/redact rule is configured.
	var blkBody interface{}
	_ = json.Unmarshal(requestBody, &blkBody)
	blkMap := aiGatewayMap(blkBody)
	blkCodex := aiGatewayExtractCodexTurnMetadata(r.Header)
	blkConvID := aiGatewayFirstNonEmpty(
		aiGatewayExtractSessionIDFromHeaders(r.Header, blkCodex),
		aiGatewayExtractSessionIDFromBody(blkMap),
		aiGatewayExtractConversationID(blkMap),
	)
	blkTurnID := aiGatewayFirstNonEmpty(blkCodex.TurnID, aiGatewayString(blkMap["turn_id"]))
	blkHistID := ""
	if h := aiGatewayCurrentBodyMaxHistoryID(aiGatewayAnnotateCurrentBodyHistoryIDs(agentID, aiGatewayCloneJSONValue(blkBody))); h > 0 {
		blkHistID = strconv.Itoa(h)
	}
	if dec := auditpkg.PreventiveCheck(auditpkg.Envelope{
		AgentID:        agentID,
		SourceChannel:  auditpkg.SourceGateway,
		Direction:      auditpkg.DirectionOutbound,
		Provider:       provider,
		ConversationID: blkConvID,
		TurnID:         blkTurnID,
		HistoryID:      blkHistID,
		Payload:        requestBody,
	}); dec.Action == auditpkg.ActionBlock {
		log.Printf("[ai-gateway] BLOCKED outbound agent=%s conv=%s history=%s suffix=%s event=%s — 403 blocked_by_audit",
			agentID, blkConvID, blkHistID, suffix, dec.EventID)
		writeGatewayBlocked(w, agentID, requestBody, dec)
		return
	}

	audit := newAIGatewayAuditSession(provider, agentID, targetBase, suffix, r.Method, r.Header.Clone(), requestBody)
	if err := audit.writeStartSnapshots(); err != nil {
		log.Printf("[ai-gateway] write current snapshot failed for %s: %v", agentID, err)
	}

	proxy := newAIGatewayReverseProxy(targetBase, suffix, provider, agentID, audit)
	proxy.ServeHTTP(w, r)
}

// writeGatewayBlocked refuses a blocked outbound request. Contract (agreed with
// w-10081, ChatHistory): HTTP 403 + a JSON body carrying the specific reason.
// The chat client keys off the X-Cicy-Audit-Blocked header (NOT the status
// code), so it treats this as a terminal "已拦截" turn — no retry, no hang — and
// renders body.message inline as the concrete reason, the same way the 余额不足
// (insufficient balance) error surfaces its message. 403's standard reason
// phrase ("Forbidden") is cosmetic; the body + headers carry the detail. The
// client also still accepts the legacy 200/SSE shape, so either side can switch
// first with zero regression window.
// auditBlockMessage builds the human-facing block reason shared by BOTH block
// paths (gateway writeGatewayBlocked + MITM synthetic), so cicy/网关 CLI/MITM
// surface an identical "已拦截" message. Keep this the single source of truth.
func auditBlockMessage(ruleIDs []string, eventID string) string {
	return fmt.Sprintf(
		"命中审计拦截规则【%s】,包含敏感内容(疑似密钥/令牌),已被 cicy-code 审计策略实时阻断,未发送给模型。请移除敏感内容后重试。(事件 ID: %s)",
		strings.Join(ruleIDs, ", "), eventID)
}

func writeGatewayBlocked(w http.ResponseWriter, agentID string, requestBody []byte, dec auditpkg.PreventiveDecision) {
	ruleIDs := make([]string, 0, len(dec.Findings))
	for _, f := range dec.Findings {
		ruleIDs = append(ruleIDs, f.RuleID)
	}
	message := auditBlockMessage(ruleIDs, dec.EventID)

	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("X-Cicy-Audit-Blocked", dec.EventID)
	h.Set("X-Cicy-Audit-Rules", strings.Join(ruleIDs, ","))
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":    "blocked_by_audit",
		"action":   "block",
		"event_id": dec.EventID,
		"rules":    ruleIDs,
		"message":  message,
	})
	log.Printf("[ai-gateway] BLOCKED outbound agent=%s rules=%v event=%s — sent 403 blocked_by_audit", agentID, ruleIDs, dec.EventID)
}

// gatewayRequestIsStream reports whether the request body asks for a streaming
// response ("stream": true). Anthropic/OpenAI both use this flag. Defaults to
// true on a parse miss — streaming is the common case and an SSE reply is more
// broadly tolerated by clients than a JSON object handed to a stream reader.
func gatewayRequestIsStream(body []byte) bool {
	var m struct {
		Stream *bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &m); err == nil && m.Stream != nil {
		return *m.Stream
	}
	return true
}

// ── Temporary, env-gated diagnostics for the codex /responses empty-reply bug ──
// All of this is INERT unless CICY_DEBUG_RESPONSES=1 is set in the server env, so
// it cannot affect production or non-Windows paths. It dumps the transformed
// chat/completions request body and the raw upstream SSE the rewrap reader sees,
// so we can tell whether the upstream actually streamed content.

func gatewayDebugResponses() bool {
	return os.Getenv("CICY_DEBUG_RESPONSES") == "1"
}

func truncForDebug(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…(+" + strconv.Itoa(len(b)-max) + "B)"
}

// debugTeeReadCloser copies the first ~4KB flowing through it into a buffer and
// logs it once (at EOF or when the cap is reached), without disturbing the
// stream the real reader consumes.
type debugTeeReadCloser struct {
	inner  io.ReadCloser
	tag    string
	buf    bytes.Buffer
	logged bool
}

func newDebugTeeReadCloser(inner io.ReadCloser, tag string) *debugTeeReadCloser {
	return &debugTeeReadCloser{inner: inner, tag: tag}
}

func (d *debugTeeReadCloser) flush() {
	if d.logged {
		return
	}
	d.logged = true
	log.Printf("[responses-debug] %s upstream_bytes=%s", d.tag, truncForDebug(d.buf.Bytes(), 4096))
}

func (d *debugTeeReadCloser) Read(p []byte) (int, error) {
	n, err := d.inner.Read(p)
	if n > 0 && d.buf.Len() < 4096 {
		d.buf.Write(p[:n])
	}
	if err != nil {
		d.flush()
	}
	return n, err
}

func (d *debugTeeReadCloser) Close() error {
	d.flush()
	return d.inner.Close()
}
