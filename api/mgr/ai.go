package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type cfConfig struct {
	AccountID string `json:"account_id"`
	APIToken  string `json:"api_token"`
}

func getCFConfig() (*cfConfig, error) {
	home, _ := os.UserHomeDir()
	raw, err := os.ReadFile(filepath.Join(home, "global.json"))
	if err != nil {
		return nil, err
	}
	var g map[string]interface{}
	json.Unmarshal(raw, &g)
	cf, _ := g["cf"].(map[string]interface{})
	dev, _ := cf["prod"].(map[string]interface{})
	return &cfConfig{
		AccountID: dev["account_id"].(string),
		APIToken:  dev["api_token"].(string),
	}, nil
}

func callCFAI(messages []map[string]string) (string, error) {
	return callCFModel("@cf/meta/llama-3.1-8b-instruct", messages)
}

func callCFModel(model string, messages []map[string]string) (string, error) {
	return callCFModelWithTokens(model, messages, 4096)
}

func callCFModelWithTokens(model string, messages []map[string]string, maxTokens int) (string, error) {
	cfg, err := getCFConfig()
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]interface{}{"messages": messages, "max_tokens": maxTokens})
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/run/%s", cfg.AccountID, model)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	// Try OpenAI-style response first (gpt-oss-120b)
	var oai struct {
		Result struct {
			Choices []struct {
				Message struct {
					Content          *string `json:"content"`
					ReasoningContent string  `json:"reasoning_content"`
				} `json:"message"`
			} `json:"choices"`
			Response string `json:"response"`
		} `json:"result"`
	}
	json.Unmarshal(raw, &oai)
	if len(oai.Result.Choices) > 0 {
		msg := oai.Result.Choices[0].Message
		if msg.Content != nil && *msg.Content != "" {
			return *msg.Content, nil
		}
		// Fallback: if content is null but reasoning exists, model ran out of tokens on reasoning
		if msg.ReasoningContent != "" {
			return "", fmt.Errorf("model used all tokens on reasoning, try shorter prompt")
		}
	}
	if oai.Result.Response != "" {
		return oai.Result.Response, nil
	}
	return "", fmt.Errorf("cf ai error: %s", string(raw))
}

// POST /api/ai/chat/stream — chat with gpt-oss-120b (non-streaming, SSE wrapper)
func handleAIChatStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []map[string]string `json:"messages"`
		Model    string              `json:"model"`
	}
	readBody(r, &req)
	if len(req.Messages) == 0 {
		httpErr(w, 400, "messages required")
		return
	}
	model := req.Model
	if model == "" {
		model = "@cf/openai/gpt-oss-120b"
	}

	result, err := callCFModel(model, req.Messages)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"success": true, "result": result})
}

// POST /api/ai/chat {"messages":[{"role":"user","content":"hi"}]}
func handleAIChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []map[string]string `json:"messages"`
		Text     string              `json:"text"`
	}
	readBody(r, &req)
	if req.Text != "" && len(req.Messages) == 0 {
		req.Messages = []map[string]string{{"role": "user", "content": req.Text}}
	}
	if len(req.Messages) == 0 {
		httpErr(w, 400, "messages or text required")
		return
	}
	result, err := callCFAI(req.Messages)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"success": true, "result": result})
}

// POST /api/ai/correct {"text":"how r u"}
func handleAICorrect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	readBody(r, &req)
	if req.Text == "" {
		httpErr(w, 400, "text required")
		return
	}
	msgs := []map[string]string{
		{"role": "system", "content": "You are an English grammar corrector. Return ONLY the corrected English text, nothing else."},
		{"role": "user", "content": req.Text},
	}
	result, err := callCFAI(msgs)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"success": true, "result": result})
}

// helper: extract user_id from JWT or use token hash as fallback
func getUserID(r *http.Request) string {
	token := getToken(r)
	if token == "" {
		return ""
	}
	// JWT (OAuth users)
	if strings.Count(token, ".") == 2 {
		sub, _, err := parseJWT(token)
		if err == nil {
			return sub
		}
	}
	// Local token — verify and use fixed user_id
	if verifyToken(token) {
		return "1"
	}
	return ""
}

// POST /api/apps/create — AI generates an app from user description
func handleCreateApp(w http.ResponseWriter, r *http.Request) {
	uid := getUserID(r)
	if uid == "" {
		httpErr(w, 401, "login required")
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
	}
	readBody(r, &req)
	if req.Prompt == "" {
		httpErr(w, 400, "prompt required")
		return
	}

	// Generate HTML and metadata in parallel
	type aiResult struct {
		html string
		name string
		icon string
		err  error
	}
	htmlCh := make(chan aiResult, 1)
	metaCh := make(chan aiResult, 1)

	go func() {
		htmlPrompt := `生成一个完整的单页 HTML 应用。要求：
- 完整的 <!DOCTYPE html> 文档
- 自己写的 CSS 和 JS 内联
- 需要外部库（如 Chart.js、Three.js 等）必须用 CDN script 标签引入，禁止内联库代码
- 深色主题，现代美观，移动端适配
- 如需外部数据用免费 API（如 CoinGecko）
- 只输出 HTML 代码，不要解释，不要 markdown 代码块`
		msgs := []map[string]string{
			{"role": "system", "content": htmlPrompt},
			{"role": "user", "content": req.Prompt},
		}
		html, err := callCFModelWithTokens("@cf/openai/gpt-oss-120b", msgs, 8192)
		htmlCh <- aiResult{html: html, err: err}
	}()

	go func() {
		msgs := []map[string]string{
			{"role": "system", "content": "根据描述给出应用名（2-4字）和emoji图标。只回复JSON：{\"name\":\"名称\",\"icon\":\"emoji\"}"},
			{"role": "user", "content": req.Prompt},
		}
		result, err := callCFModel("@cf/openai/gpt-oss-120b", msgs)
		metaCh <- aiResult{html: result, err: err}
	}()

	htmlRes := <-htmlCh
	metaRes := <-metaCh

	if htmlRes.err != nil {
		httpErr(w, 500, "AI error: "+htmlRes.err.Error())
		return
	}

	html := cleanHTML(htmlRes.html)
	if !containsStr(html, "<html") && !containsStr(html, "<!doctype") {
		httpErr(w, 500, "AI failed to generate valid HTML")
		return
	}

	name, icon := "New App", "✨"
	if metaRes.err == nil {
		var meta struct {
			Name string `json:"name"`
			Icon string `json:"icon"`
		}
		raw := metaRes.html
		if start := indexOf(raw, "{"); start >= 0 {
			if end := lastIndexOf(raw, "}"); end > start {
				if json.Unmarshal([]byte(raw[start:end+1]), &meta) == nil {
					if meta.Name != "" {
						name = meta.Name
					}
					if meta.Icon != "" {
						icon = meta.Icon
					}
				}
			}
		}
	}

	appID := fmt.Sprintf("%d", time.Now().UnixNano())
	_, err := db.Exec("INSERT INTO user_apps (id, user_id, name, icon, html) VALUES (?,?,?,?,?)",
		appID, uid, name, icon, html)
	if err != nil {
		httpErr(w, 500, "save error: "+err.Error())
		return
	}

	J(w, M{
		"success": true,
		"app": M{
			"id":   appID,
			"name": name,
			"icon": icon,
			"url":  fmt.Sprintf("/api/apps/%s/", appID),
		},
	})
}

func cleanHTML(s string) string {
	// Remove ```html ... ``` wrapper
	if start := indexOf(s, "```"); start >= 0 {
		after := s[start+3:]
		// skip language tag
		if nl := indexOf(after, "\n"); nl >= 0 {
			after = after[nl+1:]
		}
		if end := indexOf(after, "```"); end >= 0 {
			return after[:end]
		}
		return after
	}
	// Find first <!DOCTYPE or <html
	for _, tag := range []string{"<!DOCTYPE", "<!doctype", "<html"} {
		if i := indexOf(s, tag); i > 0 {
			return s[i:]
		}
	}
	return s
}

func containsStr(s, sub string) bool {
	return indexOf(s, sub) >= 0 || indexOf(s, strings.ToUpper(sub)) >= 0 || indexOf(s, strings.ToLower(sub)) >= 0
}

// GET /api/apps — list user's apps
func handleListApps(w http.ResponseWriter, r *http.Request) {
	uid := getUserID(r)
	if uid == "" {
		httpErr(w, 401, "login required")
		return
	}
	rows, err := db.Query("SELECT id, name, icon, created_at FROM user_apps WHERE user_id=? ORDER BY created_at DESC", uid)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	var apps []M
	for rows.Next() {
		var id, name, icon, created string
		rows.Scan(&id, &name, &icon, &created)
		apps = append(apps, M{"id": id, "name": name, "icon": icon, "url": fmt.Sprintf("/api/apps/%s/", id), "created_at": created})
	}
	if apps == nil {
		apps = []M{}
	}
	J(w, M{"success": true, "apps": apps})
}

// GET /api/apps/{id}/ — serve the app HTML
func handleServeApp(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path: /api/apps/12345/ or /api/apps/12345
	path := r.URL.Path
	// Remove prefix and trailing slash
	id := path[len("/api/apps/"):]
	if len(id) > 0 && id[len(id)-1] == '/' {
		id = id[:len(id)-1]
	}
	if id == "" {
		httpErr(w, 400, "app id required")
		return
	}
	var html string
	err := db.QueryRow("SELECT html FROM user_apps WHERE id=?", id).Scan(&html)
	if err != nil {
		httpErr(w, 404, "app not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func lastIndexOf(s, sub string) int {
	for i := len(s) - len(sub); i >= 0; i-- {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// OpenAI-compatible /v1/chat/completions proxy → CF Workers AI
func handleV1ChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.WriteHeader(204)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	// Parse request
	var req struct {
		Model    string                   `json:"model"`
		Messages []map[string]interface{} `json:"messages"`
		Stream   bool                     `json:"stream"`
		MaxToks  int                      `json:"max_tokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"bad request"}}`, 400)
		return
	}

	// Map model names → CF model IDs
	modelMap := map[string]string{
		"gpt-3.5-turbo":    "@cf/meta/llama-3.1-8b-instruct-fp8",
		"gpt-4":            "@cf/deepseek-ai/deepseek-r1-distill-qwen-32b",
		"gpt-4o":           "@cf/deepseek-ai/deepseek-r1-distill-qwen-32b",
		"llama-3.1-8b":     "@cf/meta/llama-3.1-8b-instruct-fp8",
		"deepseek-r1-32b":  "@cf/deepseek-ai/deepseek-r1-distill-qwen-32b",
	}
	cfModel := req.Model
	if mapped, ok := modelMap[req.Model]; ok {
		cfModel = mapped
	}

	cfg, err := getCFConfig()
	if err != nil {
		http.Error(w, `{"error":{"message":"config error"}}`, 500)
		return
	}

	maxToks := req.MaxToks
	if maxToks == 0 {
		maxToks = 4096
	}

	// Forward to CF AI OpenAI-compat endpoint
	cfURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/v1/chat/completions", cfg.AccountID)
	body, _ := json.Marshal(map[string]interface{}{
		"model":      cfModel,
		"messages":   req.Messages,
		"max_tokens": maxToks,
		"stream":     req.Stream,
	})

	cfReq, _ := http.NewRequest("POST", cfURL, bytes.NewReader(body))
	cfReq.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	cfReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(cfReq)
	if err != nil {
		http.Error(w, `{"error":{"message":"upstream error"}}`, 502)
		return
	}
	defer resp.Body.Close()

	// Pass through headers
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if req.Stream {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// /v1/models — list available models
func handleV1Models(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	models := []map[string]interface{}{
		{"id": "gpt-3.5-turbo", "object": "model", "owned_by": "cicy-ai"},
		{"id": "gpt-4", "object": "model", "owned_by": "cicy-ai"},
		{"id": "gpt-4o", "object": "model", "owned_by": "cicy-ai"},
		{"id": "llama-3.1-8b", "object": "model", "owned_by": "cicy-ai"},
		{"id": "deepseek-r1-32b", "object": "model", "owned_by": "cicy-ai"},
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"object": "list", "data": models})
}

// POST /stt — Speech-to-Text via Google Cloud Speech API
func handleSTT(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.WriteHeader(204)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", 400)
		return
	}
	defer file.Close()
	audio, _ := io.ReadAll(file)

	// Get access token from gcloud
	out, err := exec.Command("gcloud", "auth", "print-access-token").Output()
	if err != nil {
		http.Error(w, "gcloud auth failed", 500)
		return
	}
	token := strings.TrimSpace(string(out))

	// Build request
	// Detect encoding from content type or file header
	contentType := r.FormValue("mime")
	encoding := "WEBM_OPUS"
	if strings.Contains(contentType, "ogg") {
		encoding = "OGG_OPUS"
	}
	// Check webm magic bytes: 0x1A45DFA3
	if len(audio) > 4 && audio[0] == 0x1a && audio[1] == 0x45 && audio[2] == 0xdf && audio[3] == 0xa3 {
		encoding = "WEBM_OPUS"
	}

	b64 := base64.StdEncoding.EncodeToString(audio)
	body := map[string]interface{}{
		"config": map[string]interface{}{
			"encoding":                   encoding,
			"sampleRateHertz":            48000,
			"languageCode":               "zh-CN",
			"alternativeLanguageCodes":    []string{"en-US"},
			"enableAutomaticPunctuation": true,
		},
		"audio": map[string]interface{}{
			"content": b64,
		},
	}
	bodyJSON, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "https://speech.googleapis.com/v1/speech:recognize", bytes.NewReader(bodyJSON))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-user-project", "project-28447ebb-8b5a-4c03-9ac")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "speech api error", 502)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[stt] audio=%d bytes, google status=%d, resp=%s", len(audio), resp.StatusCode, strings.ReplaceAll(string(respBody), "\n", " "))
	var result struct {
		Results []struct {
			Alternatives []struct {
				Transcript string `json:"transcript"`
			} `json:"alternatives"`
		} `json:"results"`
	}
	json.Unmarshal(respBody, &result)

	text := ""
	for _, r := range result.Results {
		if len(r.Alternatives) > 0 {
			text += r.Alternatives[0].Transcript
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]string{"text": text})
}
