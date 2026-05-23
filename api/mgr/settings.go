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
		result["preview"] = previewMode
		result["hot"] = hotMode
		result["agents"] = effectiveAgentOptions()
		J(w, result)
	case "POST":
		var req M
		readBody(r, &req)
		delete(req, "home")
		delete(req, "lab_mode")
		delete(req, "agents")
		data, _ := json.Marshal(req)
		store.Exec(store.Upsert("global_vars", "key_name", []string{"key_name", "value"}, []string{"value"}), "global_settings", string(data))
		// If the user is configuring global settings with CJK content
		// (workspace dir names, custom titles, etc.), opportunistically
		// install CJK fonts in the background — once per process.
		MaybeEnsureCJKFontsForBytes(data)
		J(w, M{"success": true})
	}
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

func translateTextViaProvider(text string, target string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if cached, ok := loadCachedTranslation(text, target); ok {
		return cached, nil
	}
	// Route through the defaultOpenAi provider (a multi-model OpenAI-compatible
	// gateway) — translation always goes there and asks for deepseek-v4-flash
	// by model name. Fall back to the global default AI if defaultOpenAi isn't
	// configured.
	cfg, ok := loadRuntimeAIConfigForProvider("defaultOpenAi")
	if !ok || strings.TrimSpace(cfg.APIURL) == "" || strings.TrimSpace(cfg.APIKey) == "" {
		cfg = loadRuntimeAIConfig()
	}
	apiURL := strings.TrimRight(strings.TrimSpace(cfg.APIURL), "/")
	apiKey := strings.TrimSpace(cfg.APIKey)
	// Force the flash variant for translation — it's an order of magnitude
	// faster than pro/v3 and quality is plenty for short paragraph translation.
	// Override via ~/cicy-ai/global.json key `translate_model`; otherwise default.
	model := ""
	if raw, ok := readGlobalJSONConfig()["translate_model"].(string); ok {
		model = strings.TrimSpace(raw)
	}
	if model == "" {
		model = "deepseek-v4-flash"
	}
	if apiURL == "" || apiKey == "" {
		return "", fmt.Errorf("translation provider not configured")
	}
	payload := M{
		"model": model,
		"messages": []M{
			{
				"role":    "system",
				"content": "Translate the following technical content into concise Simplified Chinese. Preserve code identifiers, JSON keys, CLI flags, filenames, URLs, and markdown structure. Return translation only.",
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
	req, err := http.NewRequest(http.MethodPost, apiURL+"/chat/completions", bytes.NewReader(body))
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
	storeCachedTranslation(text, target, translated)
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
