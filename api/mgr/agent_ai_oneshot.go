package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// aiOneShotComplete makes a single, fire-and-forget chat completion that does
// NOT touch any agent's conversation/history. It reuses the same provider
// resolution as the translate feature (defaultAnthropic → defaultOpenAi →
// global default; the CiCyAi gateway is OpenAI-compatible at
// <url>/v1/chat/completions). Default model is deepseek-v4-pro, overridable via
// global.json `diagnose_model`. Used for the usage "AI deep diagnosis".
func aiOneShotComplete(systemPrompt, userPrompt, modelOverride string) (string, error) {
	cfg, ok := loadRuntimeAIConfigForProvider("defaultAnthropic")
	if !ok || strings.TrimSpace(cfg.APIURL) == "" || strings.TrimSpace(cfg.APIKey) == "" {
		cfg, ok = loadRuntimeAIConfigForProvider("defaultOpenAi")
	}
	if !ok || strings.TrimSpace(cfg.APIURL) == "" || strings.TrimSpace(cfg.APIKey) == "" {
		cfg = loadRuntimeAIConfig()
	}
	apiURL := strings.TrimRight(strings.TrimSpace(cfg.APIURL), "/")
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiURL == "" || apiKey == "" {
		return "", fmt.Errorf("AI provider not configured")
	}
	model := strings.TrimSpace(modelOverride)
	if model == "" {
		if raw, ok := readGlobalJSONConfig()["diagnose_model"].(string); ok {
			model = strings.TrimSpace(raw)
		}
	}
	if model == "" {
		model = "deepseek-v4-pro"
	}
	payload := M{
		"model": model,
		"messages": []M{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"temperature": 0.3,
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
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("AI request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
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
		return "", fmt.Errorf("AI returned empty response")
	}
	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}
