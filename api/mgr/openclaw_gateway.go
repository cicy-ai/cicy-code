// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
