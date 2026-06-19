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
	GeminiURL            string // base URL for the Google GenAI (gemini-cli) protocol
	DefaultOpencodeModel string
	DefaultClaudeModel   string
	CodexModel           string
	GeminiModel          string
	OpenClawModel        string
	HermesModel          string
}

// providerConfig represents a single provider from the new providers.items array
type providerConfig struct {
	Name          string            `json:"name"`
	Key           string            `json:"key"`
	URL           string            `json:"url"`
	APIKey        string            `json:"apiKey"`
	Protocol      string            `json:"protocol"` // "anthropic" or "openai"
	DefaultModel  string            `json:"defaultModel"`
	DefaultModels map[string]string `json:"defaultModels"`
	ModelMapping  map[string]string `json:"modelMapping"`
	Models        []string          `json:"models"`
	// EgressProxy, when set (e.g. "socks5://127.0.0.1:9001"), routes this
	// provider's UPSTREAM traffic through that proxy. Needed for geo-restricted
	// upstreams (Gemini rejects requests from unsupported regions with 400
	// "User location is not supported") when the gateway host sits in a blocked
	// region — point it at a mihomo listener with a supported exit. Empty = direct.
	EgressProxy string `json:"egressProxy"`
}

type runtimeAIProviderOption struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Protocol string   `json:"protocol"`
	Models   []string `json:"models,omitempty"`
}

// providersConfig represents the new providers configuration structure
type providersConfig struct {
	Default map[string]string `json:"default"` // agent_type -> provider_key
	Items   []providerConfig  `json:"items"`
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
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return runtimeAIConfig{}, false
	}

	// Try new providers config first
	if pc, ok := loadProviderByKey(provider); ok {
		result := runtimeAIConfig{
			Provider:             pc.Key,
			APIKey:               pc.APIKey,
			DefaultOpencodeModel: providerDefaultModelForAgentType(pc, "opencode"),
			DefaultClaudeModel:   providerDefaultModelForAgentType(pc, "claude"),
			CodexModel:           providerDefaultModelForAgentType(pc, "codex"),
			GeminiModel:          providerDefaultModelForAgentType(pc, "gemini"),
			OpenClawModel:        providerDefaultModelForAgentType(pc, "openclaw"),
			HermesModel:          providerDefaultModelForAgentType(pc, "hermes"),
		}
		// Set URL based on protocol
		baseURL := strings.TrimRight(pc.URL, "/")
		switch pc.Protocol {
		case "anthropic":
			result.AnthropicURL = baseURL
			result.APIURL = baseURL + "/v1"
		case "gemini", "google":
			// Google GenAI passthrough: gemini-cli points GOOGLE_GEMINI_BASE_URL at
			// the gateway, which reverse-proxies to this base as-is (no translation).
			result.GeminiURL = baseURL
		default:
			// openai protocol
			result.APIURL = baseURL
			if !strings.HasSuffix(baseURL, "/v1") {
				result.APIURL = baseURL + "/v1"
			}
			result.AnthropicURL = strings.TrimSuffix(baseURL, "/v1")
		}
		// Apply defaults
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

	// Fallback to legacy ai.provider config
	ai := cfgMapValue(cfg, "ai")
	providerMap := cfgMapValue(ai, "provider")
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

// loadProvidersConfig loads the new providers configuration from global.json
// Returns nil if the new structure doesn't exist (fallback to legacy config)
func loadProvidersConfig() *providersConfig {
	cfg := readGlobalJSONConfig()
	providersRaw, ok := cfg["providers"]
	if !ok {
		return nil
	}
	providersMap, ok := providersRaw.(map[string]any)
	if !ok || len(providersMap) == 0 {
		return nil
	}

	result := &providersConfig{
		Default: make(map[string]string),
		Items:   []providerConfig{},
	}

	// Parse default mapping
	if defaultRaw, ok := providersMap["default"]; ok {
		if defaultMap, ok := defaultRaw.(map[string]any); ok {
			for agentType, providerKey := range defaultMap {
				if keyStr, ok := providerKey.(string); ok {
					result.Default[agentType] = keyStr
				}
			}
		}
	}

	// Parse items array
	if itemsRaw, ok := providersMap["items"]; ok {
		if itemsArr, ok := itemsRaw.([]any); ok {
			for _, itemRaw := range itemsArr {
				if itemMap, ok := itemRaw.(map[string]any); ok {
					pc := providerConfig{
						Name:          cfgStringValue(itemMap, "name"),
						Key:           cfgStringValue(itemMap, "key"),
						URL:           cfgStringValue(itemMap, "url"),
						APIKey:        cfgStringValue(itemMap, "apiKey"),
						Protocol:      cfgStringValue(itemMap, "protocol"),
						DefaultModel:  cfgStringValue(itemMap, "defaultModel"),
						DefaultModels: make(map[string]string),
						ModelMapping:  make(map[string]string),
						Models:        []string{},
						EgressProxy:   cfgStringValue(itemMap, "egressProxy"),
					}

					// Parse defaultModels (per agent type)
					if dmRaw, ok := itemMap["defaultModels"]; ok {
						if dmMap, ok := dmRaw.(map[string]any); ok {
							for agentType, model := range dmMap {
								if modelStr, ok := model.(string); ok {
									key := strings.TrimSpace(strings.ToLower(agentType))
									if key != "" {
										pc.DefaultModels[key] = strings.TrimSpace(modelStr)
									}
								}
							}
						}
					}

					// Parse modelMapping
					if mappingRaw, ok := itemMap["modelMapping"]; ok {
						if mappingMap, ok := mappingRaw.(map[string]any); ok {
							for from, to := range mappingMap {
								if toStr, ok := to.(string); ok {
									pc.ModelMapping[from] = toStr
								}
							}
						}
					}

					// Parse models array
					if modelsRaw, ok := itemMap["models"]; ok {
						if modelsArr, ok := modelsRaw.([]any); ok {
							for _, m := range modelsArr {
								if mStr, ok := m.(string); ok {
									pc.Models = append(pc.Models, mStr)
								}
							}
						}
					}

					result.Items = append(result.Items, pc)
				}
			}
		}
	}

	if len(result.Items) == 0 {
		return nil
	}
	return result
}

// loadProviderByKey finds a provider by its key from the new providers.items array
func loadProviderByKey(key string) (*providerConfig, bool) {
	providers := loadProvidersConfig()
	if providers == nil {
		return nil, false
	}
	key = strings.TrimSpace(key)
	for i := range providers.Items {
		if providers.Items[i].Key == key {
			return &providers.Items[i], true
		}
	}
	return nil, false
}

// loadDefaultProviderKeyForAgentType returns the default provider key for a given agent type
func loadDefaultProviderKeyForAgentType(agentType string) string {
	providers := loadProvidersConfig()
	if providers == nil {
		return ""
	}
	agentType = strings.TrimSpace(strings.ToLower(agentType))
	// The CiCy lite agent (legacy "dispatcher") speaks the Anthropic protocol
	// and has its own routing slot ("cicy", default deepseek-v4-pro via the
	// seeded defaultAnthropic). Configs from before the slot existed have no
	// "cicy" entry — fall back to riding the claude provider chain.
	if agentType == "dispatcher" {
		agentType = "cicy"
	}
	if agentType == "cicy" {
		if key, ok := providers.Default["cicy"]; ok && strings.TrimSpace(key) != "" {
			return key
		}
		agentType = "claude"
	}
	if key, ok := providers.Default[agentType]; ok {
		return key
	}
	return ""
}

// loadProviderForAgentType returns the provider config for a given agent type
func loadProviderForAgentType(agentType string) (*providerConfig, bool) {
	key := loadDefaultProviderKeyForAgentType(agentType)
	if key == "" {
		return nil, false
	}
	return loadProviderByKey(key)
}

func loadAllProviderConfigs() []providerConfig {
	providers := loadProvidersConfig()
	if providers == nil || len(providers.Items) == 0 {
		return nil
	}
	items := make([]providerConfig, 0, len(providers.Items))
	for _, item := range providers.Items {
		items = append(items, item)
	}
	return items
}

func runtimeAIExpectedProtocolForAgentType(agentType string) string {
	switch normalizeAgentType(agentType) {
	case "claude", "cicy-claude", "kiro-cli":
		return "anthropic"
	case "codex", "openclaw", "hermes":
		return "openai"
	case "opencode", "cicy", "gemini":
		// opencode speaks both protocols natively. cicy always speaks Anthropic
		// Messages, but the gateway bridges an openai-protocol provider down to
		// Chat Completions and wraps the SSE back (shouldAdaptAnthropicToChatCompletions),
		// so EITHER provider works — no protocol filter in the routing/model/override
		// pickers, and no cross-protocol 400 when a cicy agent is pinned to openai.
		//
		// gemini rides a dedicated /gemini passthrough: the gateway always targets
		// Google by the path provider segment ("gemini" → GeminiURL/default Google)
		// regardless of the backing provider's DECLARED protocol — only the provider
		// key is used. Enforcing a "gemini" protocol here would (a) reject the seeded
		// defaultOpenAi default with a 400 and (b) filter every openai/anthropic
		// provider out of gemini's pickers (no gemini-protocol provider exists in the
		// local protocol whitelist). So leave it unrestricted, like cicy. The user
		// just points it at a provider holding a real Google key.
		return ""
	default:
		return ""
	}
}

func providerDefaultModelForAgentType(provider *providerConfig, agentType string) string {
	if provider == nil {
		return ""
	}
	agentType = strings.TrimSpace(strings.ToLower(agentType))
	if provider.DefaultModels != nil {
		if value := strings.TrimSpace(provider.DefaultModels[agentType]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(provider.DefaultModel)
}

func runtimeAIProviderOptionsForAgentType(agentType string) []runtimeAIProviderOption {
	providers := loadProvidersConfig()
	expectedProtocol := runtimeAIExpectedProtocolForAgentType(agentType)
	if providers != nil && len(providers.Items) > 0 {
		options := make([]runtimeAIProviderOption, 0, len(providers.Items))
		for _, p := range providers.Items {
			protocol := normalizeAIGatewayProvider(p.Protocol)
			if expectedProtocol != "" && protocol != "" && protocol != expectedProtocol {
				continue
			}
			key := strings.TrimSpace(p.Key)
			if key == "" {
				continue
			}
			label := strings.TrimSpace(p.Name)
			if label == "" {
				label = key
			}
			option := runtimeAIProviderOption{
				Key:      key,
				Label:    label,
				Protocol: protocol,
			}
			if len(p.Models) > 0 {
				option.Models = append([]string(nil), p.Models...)
			}
			options = append(options, option)
		}
		return options
	}
	legacy := runtimeAIProviderNames()
	if len(legacy) == 0 {
		return nil
	}
	options := make([]runtimeAIProviderOption, 0, len(legacy))
	for _, key := range legacy {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		options = append(options, runtimeAIProviderOption{Key: key, Label: key})
	}
	return options
}

// listProviderKeys returns all provider keys from the new providers.items array
func listProviderKeys() []string {
	providers := loadProvidersConfig()
	if providers == nil {
		return nil
	}
	keys := make([]string, 0, len(providers.Items))
	for _, p := range providers.Items {
		keys = append(keys, p.Key)
	}
	return keys
}

// listProviderNames returns all provider display names from the new providers.items array
func listProviderNames() []string {
	providers := loadProvidersConfig()
	if providers == nil {
		return nil
	}
	names := make([]string, 0, len(providers.Items))
	for _, p := range providers.Items {
		names = append(names, p.Name)
	}
	return names
}

// hasNewProvidersConfig returns true if the new providers configuration exists
func hasNewProvidersConfig() bool {
	return loadProvidersConfig() != nil
}

// applyModelMapping applies the provider's model mapping to the requested model.
// Resolution order: exact match wins; then prefix keys ending in "*"
// ("claude-sonnet*") with the longest matching prefix; then the wildcard key ""
// (if present and non-empty) as a catch-all default. Used to route models the
// upstream relay doesn't have a channel for (e.g. a newer claude-opus-4-8, or
// Claude Code's background small-model claude-sonnet-*) onto a model it does.
func (p *providerConfig) applyModelMapping(requestedModel string) string {
	if p.ModelMapping == nil {
		return requestedModel
	}
	if mapped, ok := p.ModelMapping[requestedModel]; ok && mapped != "" {
		return mapped
	}
	bestPrefixLen := -1
	bestPrefixVal := ""
	for k, v := range p.ModelMapping {
		if v == "" || !strings.HasSuffix(k, "*") {
			continue
		}
		prefix := strings.TrimSuffix(k, "*")
		if strings.HasPrefix(requestedModel, prefix) && len(prefix) > bestPrefixLen {
			bestPrefixLen = len(prefix)
			bestPrefixVal = v
		}
	}
	if bestPrefixLen >= 0 {
		return bestPrefixVal
	}
	if fallback, ok := p.ModelMapping[""]; ok && fallback != "" {
		return fallback
	}
	return requestedModel
}

// coerceModel returns a model name the provider actually serves. It first applies
// the provider's model mapping, then — if the provider declares a non-empty model
// list and the (mapped) model still isn't in it — falls back to the provider's
// defaultModel. This stops a model selected for a DIFFERENT provider (e.g. left
// over in the UI after switching provider) from being forwarded to an upstream
// that rejects it. No-op (returns the mapped/original) when the provider declares
// no model list or has no defaultModel to fall back to.
func (p *providerConfig) coerceModel(requestedModel string) string {
	if p == nil || requestedModel == "" {
		return requestedModel
	}
	mapped := p.applyModelMapping(requestedModel)
	if len(p.Models) == 0 {
		return mapped
	}
	for _, name := range p.Models {
		if name == mapped {
			return mapped
		}
	}
	if def := strings.TrimSpace(p.DefaultModel); def != "" {
		return def
	}
	return mapped
}

// getEffectiveURL returns the URL for API requests based on protocol
// For anthropic protocol, returns the base URL
// For openai protocol, returns the URL (which should already include /v1 if needed)
func (p *providerConfig) getEffectiveURL() string {
	return strings.TrimRight(p.URL, "/")
}

// getOpenAIURL returns the URL for OpenAI-compatible API requests
func (p *providerConfig) getOpenAIURL() string {
	url := p.getEffectiveURL()
	if p.Protocol == "openai" && !strings.HasSuffix(url, "/v1") {
		return url + "/v1"
	}
	return url
}
