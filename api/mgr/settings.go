// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		home, _ := os.UserHomeDir()
		var val []byte
		err := store.QueryRow("SELECT `value` FROM global_vars WHERE `key_name`='global_settings'").Scan(&val)
		result := M{}
		if err != nil || val == nil {
			result = M{"favor": M{"dir": []string{}, "cmd": []string{}}}
		} else if err := json.Unmarshal(val, &result); err != nil {
			result = M{}
		}
		result["home"] = home
		result["lab_mode"] = labMode
		result["dev"] = devMode
		// Report the EFFECTIVE mode, not just the flag: CICY_PREVIEW_DIST alone
		// puts us in preview (see previewDistDir), and --hot outranks both.
		result["preview"] = !hotMode && previewDistDir() != ""
		result["hot"] = hotMode
		result["cdn"] = cdnMode
		// Team-Helper mode: read by the SPA to hide left-sidebar entries
		// (audit / im / gateway / skills) so the trial drawer stays
		// laser-focused on the chat pane.
		result["helper_mode"] = helperMode
		// Team-Helper LLM model: operator-configurable here (POST persists it in
		// the blob); the headless cicy 团队助手 reads it via helperModelSetting().
		// Always surface the key (default "") so the UI can render the control.
		if _, ok := result["helper_model"]; !ok {
			result["helper_model"] = ""
		}
		result["agents"] = effectiveAgentOptions()
		// Mobile QR onboarding: the reachable public URL (tunneled domain or LAN
		// IP) is configured in ~/cicy-ai/global.json ("public_url") and edited via
		// the Settings → General field. Present ⇒ exposed so the UI renders the
		// "scan to add to mobile" QR; absent ⇒ "" and the UI hides the icon. No
		// CICY_PUBLIC_URL env fallback — global.json is the single source.
		result["public_url"] = configuredPublicURL()
		// 派发 chat 附件上传上限(MB)。持久化在 ~/cicy-ai/global.json
		// ("max_attachment_mb"),通用配置里可改;缺省 100。前端据此约束上传。
		result["max_attachment_mb"] = configuredMaxAttachmentMB()
		J(w, result)
	case "POST":
		var req M
		readBody(r, &req)
		// Settings updates are patches. Start from the persisted blob so changing
		// one General switch cannot erase unrelated settings such as routing or
		// provider preferences.
		merged := globalSettingsBlob()
		delete(req, "home")
		delete(req, "lab_mode")
		delete(req, "helper_mode")
		delete(req, "agents")
		// public_url is persisted to ~/cicy-ai/global.json (NOT the global_settings
		// DB blob) so the launcher, runtime, and QR all read one source. Only act
		// when the key is present; an empty string clears it.
		if raw, ok := req["public_url"]; ok {
			pu, _ := raw.(string)
			pu = strings.TrimSpace(pu)
			providersFileMu.Lock()
			cfg := readGlobalJSONConfig()
			if pu == "" {
				delete(cfg, "public_url")
			} else {
				cfg["public_url"] = pu
			}
			werr := writeGlobalJSONConfig(cfg)
			providersFileMu.Unlock()
			if werr != nil {
				httpErr(w, 500, "write global.json: "+werr.Error())
				return
			}
			delete(req, "public_url") // don't duplicate into the DB blob
		}
		// max_attachment_mb persisted to ~/cicy-ai/global.json (same single-source
		// pattern as public_url) so a partial POST doesn't clobber the blob.
		if raw, ok := req["max_attachment_mb"]; ok {
			mb := 0
			if n, isNum := raw.(float64); isNum {
				mb = int(n)
			}
			providersFileMu.Lock()
			cfg := readGlobalJSONConfig()
			if mb > 0 {
				cfg["max_attachment_mb"] = mb
			} else {
				delete(cfg, "max_attachment_mb")
			}
			werr := writeGlobalJSONConfig(cfg)
			providersFileMu.Unlock()
			if werr != nil {
				httpErr(w, 500, "write global.json: "+werr.Error())
				return
			}
			delete(req, "max_attachment_mb") // don't duplicate into the DB blob
		}
		for key, value := range req {
			merged[key] = value
		}
		data, _ := json.Marshal(merged)
		store.Exec(store.Upsert("global_vars", "key_name", []string{"key_name", "value"}, []string{"value"}), "global_settings", string(data))
		// If the user is configuring global settings with CJK content
		// (workspace dir names, custom titles, etc.), opportunistically
		// install CJK fonts in the background — once per process.
		MaybeEnsureCJKFontsForBytes(data)
		J(w, M{"success": true})
	}
}

// configuredPublicURL returns the deployment's reachable public URL from
// ~/cicy-ai/global.json ("public_url"), trimmed. Empty when unset — callers
// treat absent as "no public URL configured" (the QR icon hides). This is the
// single source for the in-app/QR public URL (CICY_PUBLIC_URL is no longer
// consulted here).
func configuredPublicURL() string {
	if s, ok := readGlobalJSONConfig()["public_url"].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// configuredMaxAttachmentMB returns the dispatcher-chat attachment upload cap in
// MB from ~/cicy-ai/global.json ("max_attachment_mb"). Defaults to 100 when
// unset or invalid. Enforced client-side; this is the single configurable source.
func configuredMaxAttachmentMB() int {
	if v, ok := readGlobalJSONConfig()["max_attachment_mb"]; ok {
		if n, isNum := v.(float64); isNum && n > 0 {
			return int(n)
		}
	}
	return 100
}

// globalSettingsBlob loads the persisted global_settings JSON object (the
// free-form blob written by POST /api/settings/global). Empty map when
// unset/unparseable.
func globalSettingsBlob() map[string]interface{} {
	var val []byte
	if err := store.QueryRow("SELECT `value` FROM global_vars WHERE `key_name`='global_settings'").Scan(&val); err != nil || len(val) == 0 {
		return map[string]interface{}{}
	}
	m := map[string]interface{}{}
	if err := json.Unmarshal(val, &m); err != nil {
		return map[string]interface{}{}
	}
	return m
}

// helperModelSetting returns the Team-Helper LLM model configured in global
// settings (key "helper_model"), or "" when unset.
func helperModelSetting() string {
	if s, ok := globalSettingsBlob()["helper_model"].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func handleFileExists(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = home + path[1:]
	}
	_, err := os.Stat(path)
	J(w, M{"exists": err == nil, "path": path})
}

func translateCachePath(text string, target string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(target) + "\n" + strings.TrimSpace(text)))
	// Persistent location under ~/logs/translate-cache/ — survives restart.
	base := filepath.Join(cicyLogsDir, "translate-cache")
	if cicyLogsDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, "logs", "translate-cache")
		} else {
			base = filepath.Join(os.TempDir(), "cicy-translate-cache")
		}
	}
	return filepath.Join(base, hex.EncodeToString(hash[:])+".json")
}

func loadCachedTranslation(text string, target string) (string, bool) {
	path := translateCachePath(text, target)
	body, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		return "", false
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", false
	}
	if strings.TrimSpace(payload.Text) == "" {
		return "", false
	}
	return strings.TrimSpace(payload.Text), true
}

func storeCachedTranslation(text string, target string, translated string) {
	translated = strings.TrimSpace(translated)
	if translated == "" {
		return
	}
	path := translateCachePath(text, target)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	body, err := json.Marshal(M{"text": translated})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(body, '\n'), 0644)
}

func openAIChatCompletionsURL(apiURL string) string {
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if strings.HasSuffix(apiURL, "/chat/completions") {
		return apiURL
	}
	return apiURL + "/chat/completions"
}

func translateTextViaProvider(text string, target string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	target = strings.TrimSpace(target)
	if target == "" {
		target = "zh-CN"
	}
	target = regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(target, "")
	// Translation has its own configurable provider route. This keeps the
	// marketplace independent from claude/codex routing and lets operators pick
	// any OpenAI-compatible provider in Settings → Agent routing.
	providerKey := "opencodeZen"
	if providers := loadProvidersConfig(); providers != nil {
		if key := strings.TrimSpace(providers.Default["translate"]); key != "" {
			providerKey = key
		}
	}
	// Include the routed provider and cache schema in the cache namespace.
	// Otherwise a bad result produced by a previous provider remains visible
	// after the operator switches the translation route.
	cacheTarget := "v3:" + providerKey + ":" + target
	if cached, ok := loadCachedTranslation(text, cacheTarget); ok {
		return cached, nil
	}
	cfg, ok := loadRuntimeAIConfigForProvider(providerKey)
	if !ok || strings.TrimSpace(cfg.APIURL) == "" || strings.TrimSpace(cfg.APIKey) == "" {
		cfg = loadRuntimeAIConfig()
	}
	apiURL := strings.TrimRight(strings.TrimSpace(cfg.APIURL), "/")
	apiKey := strings.TrimSpace(cfg.APIKey)
	// Provider records may intentionally store a complete chat-completions
	// endpoint (OpenCode Zen does). runtimeAIConfig normalizes OpenAI providers
	// as base URLs for gateway proxying, so use the provider record verbatim for
	// this direct request.
	var routedProvider *providerConfig
	if provider, found := loadProviderByKey(providerKey); found {
		routedProvider = provider
		if rawURL := strings.TrimRight(strings.TrimSpace(provider.URL), "/"); rawURL != "" {
			apiURL = rawURL
		}
		if rawKey := strings.TrimSpace(provider.APIKey); rawKey != "" {
			apiKey = rawKey
		}
	}
	// Use the routed provider's default model. An operator can override it with
	// ~/cicy-ai/global.json key `translate_model`.
	model := ""
	if raw, ok := readGlobalJSONConfig()["translate_model"].(string); ok {
		model = strings.TrimSpace(raw)
	}
	if model == "" {
		if routedProvider != nil {
			model = strings.TrimSpace(routedProvider.DefaultModel)
		}
	}
	if model == "" {
		model = strings.TrimSpace(cfg.DefaultOpencodeModel)
	}
	if model == "" {
		model = strings.TrimSpace(cfg.CodexModel)
	}
	if model == "" {
		model = "deepseek-v4-pro"
	}
	if apiURL == "" || apiKey == "" {
		return "", fmt.Errorf("translation provider not configured")
	}
	payload := M{
		"model": model,
		"messages": []M{
			{
				"role":    "system",
				"content": "Translate the following technical content into the language identified by BCP-47 tag " + target + ". Preserve code identifiers, JSON keys, CLI flags, filenames, URLs, and markdown structure. Return translation only.",
			},
			{
				"role":    "user",
				"content": text,
			},
		},
		"temperature": 0.2,
	}
	if strings.TrimSpace(target) != "" {
		payload["metadata"] = M{"target_language": target}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, openAIChatCompletionsURL(apiURL), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("translation failed: %s", strings.TrimSpace(string(respBody)))
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("translation empty")
	}
	translated := strings.TrimSpace(result.Choices[0].Message.Content)
	storeCachedTranslation(text, cacheTarget, translated)
	return translated, nil
}

func handleTranslateText(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text   string `json:"text"`
		Target string `json:"target"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "invalid request body")
		return
	}
	translated, err := translateTextViaProvider(req.Text, req.Target)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"success": true, "text": translated})
}

func handleCorrectEnglish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text   string `json:"text"`
		Target string `json:"target"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "invalid request body")
		return
	}
	translated, err := translateTextViaProvider(req.Text, req.Target)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"success": true, "text": translated})
}
