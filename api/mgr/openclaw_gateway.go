package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	aiGatewayMaxAttempts = 3
)

func firstForwardedHeaderValue(value string) string {
	if value == "" {
		return ""
	}
	part := strings.Split(value, ",")[0]
	return strings.TrimSpace(part)
}

func openClawProxyWebSocketURL(r *http.Request) string {
	scheme := "ws"
	if proto := strings.ToLower(firstForwardedHeaderValue(r.Header.Get("X-Forwarded-Proto"))); proto == "https" || (proto == "" && r.TLS != nil) {
		scheme = "wss"
	}

	host := firstForwardedHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}

	return scheme + "://" + host + "/openclaw/"
}

type openClawSessionStoreEntry struct {
	UpdatedAt int64 `json:"updatedAt"`
}

func resolvePreferredOpenClawSessionKey() string {
	const fallback = "main"
	const prefix = "agent:main:openclaw-weixin:direct:"

	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return fallback
	}

	stateDir := filepath.Join(home, ".openclaw-"+primaryWorkerSession)
	storePath := filepath.Join(stateDir, "agents", "main", "sessions", "sessions.json")
	accountsDir := filepath.Join(stateDir, "openclaw-weixin", "accounts")

	latestAccountSession := func() string {
		entries, err := os.ReadDir(accountsDir)
		if err != nil || len(entries) == 0 {
			return ""
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".json") ||
				strings.HasSuffix(name, ".sync.json") ||
				strings.HasSuffix(name, ".context-tokens.json") {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return ""
		}
		body, err := os.ReadFile(filepath.Join(accountsDir, names[len(names)-1]))
		if err != nil {
			return ""
		}
		var payload struct {
			UserID string `json:"userId"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return ""
		}
		userID := strings.ToLower(strings.TrimSpace(payload.UserID))
		if userID == "" {
			return ""
		}
		return prefix + userID
	}

	body, err := os.ReadFile(storePath)
	if err != nil {
		if sessionKey := latestAccountSession(); sessionKey != "" {
			return sessionKey
		}
		return fallback
	}

	store := map[string]openClawSessionStoreEntry{}
	if err := json.Unmarshal(body, &store); err != nil {
		if sessionKey := latestAccountSession(); sessionKey != "" {
			return sessionKey
		}
		return fallback
	}

	bestKey := ""
	bestUpdatedAt := int64(0)
	for key, entry := range store {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if bestKey == "" || entry.UpdatedAt > bestUpdatedAt || (entry.UpdatedAt == bestUpdatedAt && key < bestKey) {
			bestKey = key
			bestUpdatedAt = entry.UpdatedAt
		}
	}
	if bestKey != "" {
		return bestKey
	}
	if sessionKey := latestAccountSession(); sessionKey != "" {
		return sessionKey
	}
	return fallback
}

func handleOpenClawGatewayInfo(w http.ResponseWriter, r *http.Request) {
	if !ensureOpenClawGatewayReady() {
		httpErr(w, 503, "openclaw_gateway_not_ready")
		return
	}

	token := resolveOpenClawGatewayToken()
	if token == "" {
		httpErr(w, 503, "openclaw_gateway_token_not_ready")
		return
	}

	J(w, M{
		"ws_url":      openClawProxyWebSocketURL(r),
		"token":       token,
		"session_key": resolvePreferredOpenClawSessionKey(),
	})
}

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
		if !aiGatewayShouldRetryStatus(resp.StatusCode) || attempt >= aiGatewayMaxAttempts {
			return resp, nil
		}
		delay := aiGatewayRetryDelay(resp, attempt)
		log.Printf("[ai-gateway] retrying provider=%s agent=%s path=%s after upstream status=%d (attempt %d/%d, wait=%s)", t.provider, t.agentID, req.URL.Path, resp.StatusCode, attempt+1, aiGatewayMaxAttempts, delay)
		io.Copy(io.Discard, resp.Body)
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
	proxy.Transport = &aiGatewayRetryTransport{
		base:     http.DefaultTransport,
		provider: provider,
		agentID:  agentID,
	}
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		req.URL.Path = resolveOpenClawProviderTargetPath(targetBase.Path, suffix)
		req.URL.RawPath = ""
		req.Host = targetBase.Host
		req.Header.Set("User-Agent", "cicy-ai-gateway/1.0")
		req.Header.Set("X_AGENT_SHORT_ID", agentID)
		req.Header.Set("X-Agent-Short-Id", agentID)
		stripAIGatewayClientAuthHeaders(req)
		switch provider {
		case "openai":
			stripOpenAIFingerprintHeaders(req)
			if apiKey := aiGatewayProxyAPIKey(provider, agentID); apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+apiKey)
			}
		case "anthropic":
			if apiKey := aiGatewayProxyAPIKey(provider, agentID); apiKey != "" {
				req.Header.Set("x-api-key", apiKey)
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
			model := ""
			if mediaType := resp.Header.Get("Content-Type"); strings.Contains(mediaType, "event-stream") {
				if id := resp.Request.URL.Query().Get("model"); id != "" {
					model = id
				}
			}
			resp.Body = newChatCompletionsToResponsesReader(resp.Body, model)
			resp.Header.Set("Content-Type", "text/event-stream; charset=utf-8")
			resp.Header.Del("Content-Length")
			resp.ContentLength = -1
		}
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
	requestBody = agentInspectorRewriteRequestBody(provider, agentID, requestBody)

	// DeepSeek + codex (Responses API) adaptation: DeepSeek only speaks Chat
	// Completions, so translate the request body and the upstream path; mark
	// the request so ModifyResponse can wrap the SSE stream the other way.
	if shouldAdaptDeepSeekForCodex(targetBase.Host, suffix) {
		if newBody, _, err := transformResponsesRequestToChatCompletions(requestBody); err == nil {
			requestBody = newBody
			suffix = rewriteSuffixForDeepSeekChatCompletions(suffix)
			r.Header.Set(cicyAdaptResponsesHeader, "1")
		} else {
			log.Printf("[ai-gateway] deepseek responses->chat translation failed for %s: %v", agentID, err)
		}
	}

	r.Body = io.NopCloser(bytes.NewReader(requestBody))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(requestBody)), nil
	}
	r.ContentLength = int64(len(requestBody))

	audit := newAIGatewayAuditSession(provider, agentID, targetBase, suffix, r.Method, r.Header.Clone(), requestBody)
	if err := audit.writeStartSnapshots(); err != nil {
		log.Printf("[ai-gateway] write current snapshot failed for %s: %v", agentID, err)
	}

	proxy := newAIGatewayReverseProxy(targetBase, suffix, provider, agentID, audit)
	proxy.ServeHTTP(w, r)
}

func handleOpenClawProviderProxy(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		httpErr(w, 403, "openclaw_provider_proxy_loopback_only")
		return
	}

	targetBase, err := aiGatewayProxyBaseURL("openai", "openclaw")
	if err != nil || targetBase == nil || targetBase.Scheme == "" || targetBase.Host == "" {
		httpErr(w, 503, "openclaw_provider_proxy_target_invalid")
		return
	}

	const prefix = "/api/openclaw/provider"
	suffix := strings.TrimPrefix(r.URL.Path, prefix)
	if suffix == r.URL.Path {
		httpErr(w, 404, "openclaw_provider_proxy_path_invalid")
		return
	}
	if suffix == "" {
		suffix = "/"
	}

	proxy := newAIGatewayReverseProxy(targetBase, suffix, "openai", "openclaw", nil)
	proxy.ServeHTTP(w, r)
}
