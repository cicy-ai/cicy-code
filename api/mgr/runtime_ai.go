package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrRuntimeAIProviderMismatch = errors.New("runtime_ai provider_protocol mismatch")
	ErrRuntimeAIProviderNotFound = errors.New("runtime_ai provider_name not found")
)

type runtimeAIOverride struct {
	ProviderName     string `json:"provider_name,omitempty"`
	ProviderProtocol string `json:"provider_protocol,omitempty"`
	Model            string `json:"model,omitempty"`
}

func (o *runtimeAIOverride) Empty() bool {
	return o == nil || (strings.TrimSpace(o.ProviderName) == "" && strings.TrimSpace(o.ProviderProtocol) == "" && strings.TrimSpace(o.Model) == "")
}

func normalizeRuntimeAIProtocol(raw string, model string) string {
	protocol := normalizeAIGatewayProvider(raw)
	if protocol != "" {
		return protocol
	}
	lowerModel := strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(lowerModel, "claude-") {
		return "anthropic"
	}
	if strings.HasPrefix(lowerModel, "gpt-") {
		return "openai"
	}
	return ""
}

func runtimeAIOverrideFromAny(raw any) (*runtimeAIOverride, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("runtime_ai must be an object or null")
	}
	ov := &runtimeAIOverride{
		ProviderName: strings.TrimSpace(cfgStringValue(m, "provider_name")),
		Model:        strings.TrimSpace(cfgStringValue(m, "model")),
	}
	rawProtocol := strings.TrimSpace(cfgStringValue(m, "provider_protocol"))
	ov.ProviderProtocol = normalizeRuntimeAIProtocol(rawProtocol, ov.Model)
	if rawProtocol != "" && ov.ProviderProtocol == "" {
		return nil, fmt.Errorf("runtime_ai.provider_protocol must be openai or anthropic")
	}
	if ov.ProviderName != "" {
		if _, ok := loadRuntimeAIConfigForProvider(ov.ProviderName); !ok {
			return nil, ErrRuntimeAIProviderNotFound
		}
	}
	if ov.Empty() {
		return nil, nil
	}
	return ov, nil
}

func runtimeAIOverrideToMap(ov *runtimeAIOverride) map[string]any {
	if ov == nil || ov.Empty() {
		return nil
	}
	out := map[string]any{}
	if strings.TrimSpace(ov.ProviderName) != "" {
		out["provider_name"] = strings.TrimSpace(ov.ProviderName)
	}
	if strings.TrimSpace(ov.ProviderProtocol) != "" {
		out["provider_protocol"] = strings.TrimSpace(ov.ProviderProtocol)
	}
	if strings.TrimSpace(ov.Model) != "" {
		out["model"] = strings.TrimSpace(ov.Model)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parsePaneConfigJSON(configJSON string) (map[string]any, error) {
	trimmed := strings.TrimSpace(configJSON)
	if trimmed == "" {
		return map[string]any{}, nil
	}
	cfg := map[string]any{}
	if err := json.Unmarshal([]byte(trimmed), &cfg); err != nil {
		return nil, fmt.Errorf("config must be valid JSON object")
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return cfg, nil
}

func normalizePaneConfigJSON(configJSON string) (string, error) {
	cfg, err := parsePaneConfigJSON(configJSON)
	if err != nil {
		return "", err
	}
	if raw, ok := cfg["runtime_ai"]; ok {
		ov, err := runtimeAIOverrideFromAny(raw)
		if err != nil {
			return "", err
		}
		if ov == nil {
			delete(cfg, "runtime_ai")
		} else {
			cfg["runtime_ai"] = runtimeAIOverrideToMap(ov)
		}
	}
	if len(cfg) == 0 {
		return "{}", nil
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func mergeRuntimeAIIntoConfigJSON(existing string, ov *runtimeAIOverride) (string, error) {
	cfg, err := parsePaneConfigJSON(existing)
	if err != nil {
		return "", err
	}
	if ov == nil || ov.Empty() {
		delete(cfg, "runtime_ai")
	} else {
		cfg["runtime_ai"] = runtimeAIOverrideToMap(ov)
	}
	if len(cfg) == 0 {
		return "{}", nil
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func extractRuntimeAIFromConfigJSON(configJSON string) *runtimeAIOverride {
	cfg, err := parsePaneConfigJSON(configJSON)
	if err != nil {
		return nil
	}
	ov, err := runtimeAIOverrideFromAny(cfg["runtime_ai"])
	if err != nil {
		return nil
	}
	return ov
}

func loadPaneRuntimeAIOverride(agentID string) (*runtimeAIOverride, error) {
	paneID := normPaneID(agentID)
	var config sql.NullString
	if err := store.QueryRow("SELECT config FROM agent_config WHERE pane_id=?", paneID).Scan(&config); err != nil {
		return nil, err
	}
	if !config.Valid || strings.TrimSpace(config.String) == "" {
		return nil, nil
	}
	return extractRuntimeAIFromConfigJSON(config.String), nil
}

func resolveRuntimeAIConfigForAgent(providerProtocol string, agentID string) (runtimeAIConfig, *runtimeAIOverride, error) {
	cfg := loadRuntimeAIConfig()
	ov, err := loadPaneRuntimeAIOverride(agentID)
	if err != nil || ov == nil {
		return cfg, ov, err
	}
	protocol := normalizeAIGatewayProvider(providerProtocol)
	if ov.ProviderProtocol != "" && protocol != "" && ov.ProviderProtocol != protocol {
		return cfg, ov, ErrRuntimeAIProviderMismatch
	}
	if ov.ProviderName != "" {
		specific, ok := loadRuntimeAIConfigForProvider(ov.ProviderName)
		if !ok {
			return cfg, ov, ErrRuntimeAIProviderNotFound
		}
		cfg = specific
	}
	return cfg, ov, nil
}
