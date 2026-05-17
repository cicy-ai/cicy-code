package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// handleSTT routes audio to whichever STT backend is configured.
//
// Backend selection (first match wins):
//   1. providers.default.stt == "cloudflare-ai" → Cloudflare Workers AI Whisper
//      (uses cf.prod.account_id + cf.prod.api_token from global.json)
//   2. providers.default.stt == "<key>"          → that openai-compatible provider
//   3. (no default)                              → first openai-protocol provider
//                                                  with an apiKey set
//
// Cloudflare Workers AI is the default recommendation: 10k neurons/day free
// tier, no per-service signup, and the user already has cf credentials wired
// up for the tunnel.
func handleSTT(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httpErr(w, 400, "multipart parse: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpErr(w, 400, "file field required")
		return
	}
	defer file.Close()
	audioBytes, err := io.ReadAll(file)
	if err != nil {
		httpErr(w, 500, "read audio: "+err.Error())
		return
	}

	defaultKey := strings.TrimSpace(loadSTTDefaultKey())
	if strings.EqualFold(defaultKey, "cloudflare-ai") || strings.EqualFold(defaultKey, "cf-ai") || strings.EqualFold(defaultKey, "cloudflare") {
		runCloudflareAISTT(w, r, audioBytes, safeFilename(header.Filename))
		return
	}
	runOpenAICompatibleSTT(w, r, audioBytes, safeFilename(header.Filename), defaultKey)
}

// --- Cloudflare Workers AI ---

func runCloudflareAISTT(w http.ResponseWriter, r *http.Request, audio []byte, _ string) {
	cf := loadCloudflareCreds()
	if cf == nil {
		httpErr(w, 500, "cloudflare credentials missing (.cf.prod.account_id + api_token)")
		return
	}
	// `@cf/openai/whisper` accepts raw audio bytes as the request body. Its
	// newer sibling `whisper-large-v3-turbo` advertises a JSON schema but
	// rejects every variant we tried — stick with the basic model that works.
	model := strings.TrimSpace(r.FormValue("model"))
	if model == "" {
		model = "@cf/openai/whisper"
	}
	endpoint := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/run/%s", cf.AccountID, model)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(audio))
	if err != nil {
		httpErr(w, 500, "build req: "+err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+cf.APIToken)
	req.Header.Set("Content-Type", "application/octet-stream")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[stt] cloudflare error: %v", err)
		httpErr(w, 502, "cloudflare error: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		log.Printf("[stt] cloudflare %d: %s", resp.StatusCode, string(body))
		httpErr(w, resp.StatusCode, fmt.Sprintf("cloudflare %d: %s", resp.StatusCode, string(body)))
		return
	}

	// Cloudflare envelope: {success: true, result: {text: "..."}, ...}
	var cfResp struct {
		Success bool `json:"success"`
		Result  struct {
			Text string `json:"text"`
		} `json:"result"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &cfResp); err != nil {
		httpErr(w, 502, "parse cloudflare response: "+err.Error())
		return
	}
	if !cfResp.Success {
		msg := "cloudflare reported failure"
		if len(cfResp.Errors) > 0 {
			msg += ": " + cfResp.Errors[0].Message
		}
		httpErr(w, 502, msg)
		return
	}

	// Normalize to {text: "..."} so the mobile client doesn't care about backend.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"text": cfResp.Result.Text})
}

type cloudflareCreds struct {
	AccountID string
	APIToken  string
}

func loadCloudflareCreds() *cloudflareCreds {
	cfg := readGlobalJSONConfig()
	cfRaw, ok := cfg["cf"].(map[string]any)
	if !ok {
		return nil
	}
	prodRaw, ok := cfRaw["prod"].(map[string]any)
	if !ok {
		return nil
	}
	account := strings.TrimSpace(anyToString(prodRaw["account_id"]))
	token := strings.TrimSpace(anyToString(prodRaw["api_token"]))
	if account == "" || token == "" {
		return nil
	}
	return &cloudflareCreds{AccountID: account, APIToken: token}
}

func anyToString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}


// --- OpenAI-compatible (whisper-1, SiliconFlow SenseVoice, etc.) ---

func runOpenAICompatibleSTT(w http.ResponseWriter, r *http.Request, audio []byte, filename, defaultKey string) {
	provider, ok := pickOpenAISTTProvider(defaultKey)
	if !ok {
		httpErr(w, 500, "no openai-compatible provider configured for stt")
		return
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		httpErr(w, 500, "build upstream body: "+err.Error())
		return
	}
	if _, err := fw.Write(audio); err != nil {
		httpErr(w, 500, "copy audio: "+err.Error())
		return
	}
	model := strings.TrimSpace(r.FormValue("model"))
	if model == "" {
		model = strings.TrimSpace(provider.DefaultModel)
	}
	if model == "" {
		model = "whisper-1"
	}
	_ = mw.WriteField("model", model)
	if lang := strings.TrimSpace(r.FormValue("language")); lang != "" {
		_ = mw.WriteField("language", lang)
	}
	if err := mw.Close(); err != nil {
		httpErr(w, 500, "close upstream body: "+err.Error())
		return
	}

	upstreamURL := sttUpstreamURL(provider.URL)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, &body)
	if err != nil {
		httpErr(w, 500, "build req: "+err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[stt] upstream %s error: %v", upstreamURL, err)
		httpErr(w, 502, "upstream error: "+err.Error())
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		log.Printf("[stt] upstream %s %d: %s", upstreamURL, resp.StatusCode, string(respBody))
		httpErr(w, resp.StatusCode, fmt.Sprintf("upstream %d: %s", resp.StatusCode, string(respBody)))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(respBody)
}

func loadSTTDefaultKey() string {
	cfg := loadProvidersConfig()
	if cfg == nil {
		return ""
	}
	return cfg.Default["stt"]
}

func pickOpenAISTTProvider(defaultKey string) (*providerConfig, bool) {
	cfg := loadProvidersConfig()
	if cfg == nil {
		return nil, false
	}
	if defaultKey != "" {
		if p, ok := loadProviderByKey(defaultKey); ok {
			return p, true
		}
	}
	for i := range cfg.Items {
		if strings.EqualFold(cfg.Items[i].Protocol, "openai") && strings.TrimSpace(cfg.Items[i].APIKey) != "" {
			return &cfg.Items[i], true
		}
	}
	return nil, false
}

func sttUpstreamURL(base string) string {
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed + "/audio/transcriptions"
	}
	return trimmed + "/v1/audio/transcriptions"
}

func safeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "audio.m4a"
	}
	if i := strings.LastIndexAny(s, "/\\"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return "audio.m4a"
	}
	return s
}
