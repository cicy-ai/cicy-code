package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

type runtimeAIConfig struct {
	Provider             string
	APIKey               string
	APIURL               string
	AnthropicURL         string
	DefaultOpencodeModel string
	DefaultClaudeModel   string
	CodexModel           string
	OpenClawModel        string
	HermesModel          string
}

func globalJSONPath() string {
	return cicyGlobalJSONPath
}

func readGlobalJSONConfig() map[string]any {
	path := globalJSONPath()
	if path == "" {
		return map[string]any{}
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return map[string]any{}
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil || cfg == nil {
		return map[string]any{}
	}
	return cfg
}

func cfgMapValue(root map[string]any, key string) map[string]any {
	raw, _ := root[key]
	result, _ := raw.(map[string]any)
	if result == nil {
		return map[string]any{}
	}
	return result
}

func cfgStringValue(root map[string]any, key string) string {
	value, _ := root[key]
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strings.TrimSpace(strconv.FormatFloat(v, 'f', -1, 64))
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func normalizeClaudeModel(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "opus[1m]":
		return "claude-opus-4-7"
	default:
		return value
	}
}

func defaultRuntimeAIConfig(provider string, cfg map[string]any) runtimeAIConfig {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "cicyAi"
	}
	switch strings.ToLower(provider) {
	case "2000run":
		return runtimeAIConfig{
			Provider:             "2000Run",
			APIKey:               cfgStringValue(cfg, "2000RunApikey"),
			APIURL:               "http://2000.run:6543/v1",
			AnthropicURL:         "http://2000.run:6543",
			DefaultOpencodeModel: "gpt-5.4",
			DefaultClaudeModel:   "claude-opus-4-7",
			CodexModel:           "gpt-5.4",
			OpenClawModel:        "gpt-5.5",
			HermesModel:          "gpt-5.5",
		}
	default:
		baseURL := strings.TrimRight(cfgStringValue(cfg, "cicyAiUrl"), "/")
		if baseURL == "" {
			baseURL = "https://cicy-ai.com"
		}
		return runtimeAIConfig{
			Provider:             "cicyAi",
			APIKey:               cfgStringValue(cfg, "cicyAiapikey"),
			APIURL:               baseURL + "/v1",
			AnthropicURL:         baseURL,
			DefaultOpencodeModel: "gpt-5.4",
			DefaultClaudeModel:   "claude-opus-4-7",
			CodexModel:           "gpt-5.4",
			OpenClawModel:        "gpt-5.5",
			HermesModel:          "gpt-5.5",
		}
	}
}

func normalizeHermesModel(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "":
		return "gpt-5.5"
	case "gpt5.5":
		return "gpt-5.5"
	case "gpt5.4":
		return "gpt-5.4"
	case "claude4.7", "claude-4.7", "cluade4.7", "cluade-4.7", "opus[1m]":
		return "claude-opus-4-7"
	default:
		return value
	}
}

func loadRuntimeAIConfig() runtimeAIConfig {
	cfg := readGlobalJSONConfig()
	ai := cfgMapValue(cfg, "ai")
	provider := cfgStringValue(ai, "currentProvider")
	if provider == "" {
		provider = "cicyAi"
	}
	if result, ok := loadRuntimeAIConfigForProvider(provider); ok {
		return result
	}
	return defaultRuntimeAIConfig(provider, cfg)
}

func loadRuntimeAIConfigForProvider(provider string) (runtimeAIConfig, bool) {
	cfg := readGlobalJSONConfig()
	ai := cfgMapValue(cfg, "ai")
	providerMap := cfgMapValue(ai, "provider")
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return runtimeAIConfig{}, false
	}
	result := defaultRuntimeAIConfig(provider, cfg)

	selectedProvider := cfgMapValue(providerMap, provider)
	if len(selectedProvider) == 0 {
		return runtimeAIConfig{}, false
	}
	if value := cfgStringValue(selectedProvider, "apiKey"); value != "" {
		result.APIKey = value
	}
	if value := cfgStringValue(selectedProvider, "apiUrl"); value != "" {
		result.APIURL = value
	}
	if value := cfgStringValue(selectedProvider, "anthropicUrl"); value != "" {
		result.AnthropicURL = value
	}
	if value := cfgStringValue(selectedProvider, "defaultOpencodeModel"); value != "" {
		result.DefaultOpencodeModel = value
	}
	if value := cfgStringValue(selectedProvider, "defaultModel"); value != "" && result.DefaultOpencodeModel == "" {
		result.DefaultOpencodeModel = value
	}
	if value := cfgStringValue(selectedProvider, "defaultClaudeModel"); value != "" {
		result.DefaultClaudeModel = value
	}
	if value := cfgStringValue(selectedProvider, "claudeModel"); value != "" && result.DefaultClaudeModel == "" {
		result.DefaultClaudeModel = value
	}
	if value := cfgStringValue(selectedProvider, "codexModel"); value != "" {
		result.CodexModel = value
	}
	if value := cfgStringValue(selectedProvider, "openclawModel"); value != "" {
		result.OpenClawModel = value
	}
	if value := cfgStringValue(selectedProvider, "hermesModel"); value != "" {
		result.HermesModel = value
	}

	if result.Provider == "" {
		result.Provider = provider
	}
	if result.APIURL == "" {
		result.APIURL = "https://cicy-ai.com/v1"
	}
	if result.AnthropicURL == "" {
		result.AnthropicURL = "https://cicy-ai.com"
	}
	if result.DefaultOpencodeModel == "" {
		result.DefaultOpencodeModel = "gpt-5.4"
	}
	if result.DefaultClaudeModel == "" {
		result.DefaultClaudeModel = "claude-opus-4-7"
	}
	result.DefaultClaudeModel = normalizeClaudeModel(result.DefaultClaudeModel)
	if result.CodexModel == "" {
		result.CodexModel = "gpt-5.4"
	}
	if result.OpenClawModel == "" {
		result.OpenClawModel = "gpt-5.5"
	}
	result.HermesModel = normalizeHermesModel(result.HermesModel)
	return result, true
}
