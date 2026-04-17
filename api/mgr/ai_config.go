package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
}

func globalJSONPath() string {
	home, _ := os.UserHomeDir()
	if strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, "global.json")
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
			DefaultClaudeModel:   "opus[1m]",
			CodexModel:           "gpt-5.4",
			OpenClawModel:        "claude-sonnet-4-6",
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
			DefaultClaudeModel:   "opus[1m]",
			CodexModel:           "gpt-5.4",
			OpenClawModel:        "claude-sonnet-4-6",
		}
	}
}

func loadRuntimeAIConfig() runtimeAIConfig {
	cfg := readGlobalJSONConfig()
	ai := cfgMapValue(cfg, "ai")
	providerMap := cfgMapValue(ai, "provider")
	provider := cfgStringValue(ai, "currentProvider")
	if provider == "" {
		provider = "cicyAi"
	}
	result := defaultRuntimeAIConfig(provider, cfg)

	selectedProvider := cfgMapValue(providerMap, provider)
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
		result.DefaultClaudeModel = "opus[1m]"
	}
	if result.CodexModel == "" {
		result.CodexModel = "gpt-5.4"
	}
	if result.OpenClawModel == "" {
		result.OpenClawModel = "claude-sonnet-4-6"
	}
	return result
}
