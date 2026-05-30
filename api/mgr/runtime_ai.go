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
	ProviderName string `json:"provider_name,omitempty"`
}

type runtimeAIDefaultSummary struct {
	ProviderName  string `json:"provider_name,omitempty"`
	ProviderLabel string `json:"provider_label,omitempty"`
	Model         string `json:"model,omitempty"`
}

func (o *runtimeAIOverride) Empty() bool {
	return o == nil || strings.TrimSpace(o.ProviderName) == ""
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
	return map[string]any{
		"provider_name": strings.TrimSpace(ov.ProviderName),
	}
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
	if raw, ok := cfg["proxy"]; ok {
		ps, err := proxySettingsFromAny(raw)
		if err != nil {
			return "", err
		}
		if ps == nil {
			delete(cfg, "proxy")
		} else {
			cfg["proxy"] = proxySettingsToMap(ps)
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
	if store == nil {
		return nil, nil
	}
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

func runtimeAIDefaultSummaryForAgentType(agentType string) *runtimeAIDefaultSummary {
	provider, ok := loadProviderForAgentType(agentType)
	if !ok || provider == nil {
		return nil
	}
	label := strings.TrimSpace(provider.Name)
	if label == "" {
		label = strings.TrimSpace(provider.Key)
	}
	return &runtimeAIDefaultSummary{
		ProviderName:  strings.TrimSpace(provider.Key),
		ProviderLabel: label,
		Model:         providerDefaultModelForAgentType(provider, agentType),
	}
}

// loadPaneAgentType returns the agent_type for a given agent ID
func loadPaneAgentType(agentID string) string {
	paneID := normPaneID(agentID)
	var agentType sql.NullString
	if err := store.QueryRow("SELECT agent_type FROM agent_config WHERE pane_id=?", paneID).Scan(&agentType); err != nil {
		return ""
	}
	if !agentType.Valid {
		return ""
	}
	return strings.TrimSpace(agentType.String)
}

// loadPaneDefaultModel returns the default_model for a given agent ID.
// Used by the gateway request rewriter to hot-swap the model on each request
// without restarting the CLI.
func loadPaneDefaultModel(agentID string) string {
	paneID := normPaneID(agentID)
	var v sql.NullString
	if err := store.QueryRow("SELECT default_model FROM agent_config WHERE pane_id=?", paneID).Scan(&v); err != nil {
		return ""
	}
	if !v.Valid {
		return ""
	}
	return strings.TrimSpace(v.String)
}

func resolveRuntimeAIConfigForAgent(providerProtocol string, agentID string) (runtimeAIConfig, *runtimeAIOverride, error) {
	// First, try to get provider based on agent type from new providers config
	agentType := loadPaneAgentType(agentID)
	var cfg runtimeAIConfig
	var foundNewConfig bool

	if agentType != "" {
		// Try to find default provider for this agent type in new config
		if providerKey := loadDefaultProviderKeyForAgentType(agentType); providerKey != "" {
			if specific, ok := loadRuntimeAIConfigForProvider(providerKey); ok {
				cfg = specific
				foundNewConfig = true
			}
		}
	}

	// Fallback to legacy config if new config not found
	if !foundNewConfig {
		cfg = loadRuntimeAIConfig()
	}

	ov, err := loadPaneRuntimeAIOverride(agentID)
	if err != nil || ov == nil {
		return cfg, ov, err
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
