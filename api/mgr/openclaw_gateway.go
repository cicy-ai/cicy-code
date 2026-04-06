package main

import (
	"bytes"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
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
		"session_key": "main",
	})
}

func aiGatewayProxyBaseURL(provider string) (*url.URL, error) {
	raw := ""
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		raw = strings.TrimSpace(os.Getenv("CICY_API_URL"))
		if raw == "" {
			raw = "http://2000.run:6543/v1"
		}
	case "anthropic":
		raw = strings.TrimSpace(os.Getenv("CICY_ANTHROPIC_URL"))
		if raw == "" {
			raw = "http://2000.run:6543"
		}
	}
	if raw == "" {
		return nil, nil
	}
	return url.Parse(raw)
}

func aiGatewayProxyAPIKey() string {
	if key := strings.TrimSpace(os.Getenv("CICY_API_KEY")); key != "" {
		return key
	}
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
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

func newAIGatewayReverseProxy(targetBase *url.URL, suffix string, provider string, agentID string, audit *aiGatewayAuditSession) *httputil.ReverseProxy {
	target := &url.URL{Scheme: targetBase.Scheme, Host: targetBase.Host}
	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		req.URL.Path = resolveOpenClawProviderTargetPath(targetBase.Path, suffix)
		req.URL.RawPath = ""
		req.Host = targetBase.Host
		req.Header.Set("User-Agent", "cicy-ai-gateway/1.0")
		req.Header.Set("X_AGENT_SHORT_ID", agentID)
		req.Header.Set("X-Agent-Short-Id", agentID)
		switch provider {
		case "openai":
			stripOpenAIFingerprintHeaders(req)
			if apiKey := aiGatewayProxyAPIKey(); apiKey != "" && strings.TrimSpace(req.Header.Get("Authorization")) == "" {
				req.Header.Set("Authorization", "Bearer "+apiKey)
			}
		case "anthropic":
			if apiKey := aiGatewayProxyAPIKey(); apiKey != "" && strings.TrimSpace(req.Header.Get("x-api-key")) == "" {
				req.Header.Set("x-api-key", apiKey)
			}
		}
		if audit != nil {
			audit.recordOutboundRequest(req)
		}
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if audit == nil || resp == nil || resp.Body == nil {
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

	targetBase, err := aiGatewayProxyBaseURL(provider)
	if err != nil || targetBase == nil || targetBase.Scheme == "" || targetBase.Host == "" {
		httpErr(w, 503, "ai_gateway_proxy_target_invalid")
		return
	}

	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		httpErr(w, 400, "ai_gateway_proxy_read_body_failed")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(requestBody))

	audit := newAIGatewayAuditSession(provider, agentID, targetBase, suffix, r.Method, requestBody)
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

	targetBase, err := aiGatewayProxyBaseURL("openai")
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
