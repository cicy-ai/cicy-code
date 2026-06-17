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

// normalizeThinkingMode canonicalizes a thinking-control value to one of
// "disabled" | "enabled" | "passthrough" (or "" when unrecognized/empty).
func normalizeThinkingMode(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "disabled", "disable", "off", "false", "0", "no":
		return "disabled"
	case "enabled", "enable", "on", "true", "1", "yes":
		return "enabled"
	case "passthrough", "pass", "as-is", "asis", "raw":
		return "passthrough"
	}
	return ""
}

// paneThinkingMode reads a per-pane thinking override from agent_config.config
// JSON ({"thinking":"disabled|enabled|passthrough"}). Empty when unset/invalid.
func paneThinkingMode(agentID string) string {
	if store == nil {
		return ""
	}
	var config sql.NullString
	if err := store.QueryRow("SELECT config FROM agent_config WHERE pane_id=?", normPaneID(agentID)).Scan(&config); err != nil {
		return ""
	}
	if !config.Valid || strings.TrimSpace(config.String) == "" {
		return ""
	}
	cfg, err := parsePaneConfigJSON(config.String)
	if err != nil {
		return ""
	}
	if s, ok := cfg["thinking"].(string); ok {
		return normalizeThinkingMode(s)
	}
	return ""
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
		// The overridden provider's protocol must match the request path
		// (/anthropic vs /openai). A mismatch means runtime_ai points at an
		// incompatible provider — most commonly an opencode cross-protocol provider
		// switch that hasn't been followed by a pane restart (the boot-time
		// opencode.json adapter + base path are stale). Surface a clear 409 instead
		// of forwarding a body the upstream will reject.
		if pc, pok := loadProviderByKey(ov.ProviderName); pok && pc != nil {
			actual := normalizeAIGatewayProvider(pc.Protocol)
			want := normalizeAIGatewayProvider(providerProtocol)
			if actual != "" && want != "" && actual != want {
				// The gateway bridges an openai-protocol provider behind the
				// /anthropic endpoint (Anthropic Messages → Chat Completions, see
				// shouldAdaptAnthropicToChatCompletions) — so that single direction is
				// NOT a real mismatch and must be allowed (this is what lets the cicy
				// agent ride an openai provider). Every other cross-protocol pairing
				// has no bridge, so keep surfacing the clear 409.
				bridged := want == "anthropic" && actual == "openai"
				if !bridged {
					return cfg, ov, ErrRuntimeAIProviderMismatch
				}
			}
		}
		// 自愈:override 指向的 provider 解析出**空 apiKey**(常见于换了 providers 配置后,
		// 这条 per-agent override 仍指向旧的/已清空 key 的 provider)→ 不要带着空 key 去撞
		// 上游 401「Missing API key」。回落到 agent-type 默认 provider(cfg 此刻就是它,有可用
		// key)。用户重新选模型会重写这条 override,这里是没重选时的兜底。
		if strings.TrimSpace(specific.APIKey) == "" && strings.TrimSpace(cfg.APIKey) != "" {
			return cfg, ov, nil
		}
		cfg = specific
	}
	return cfg, ov, nil
}
