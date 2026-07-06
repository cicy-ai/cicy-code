// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type proxySettings struct {
	Password string `json:"password,omitempty"`
	Rule     string `json:"rule,omitempty"`
}

func (p *proxySettings) Empty() bool {
	return p == nil || (strings.TrimSpace(p.Password) == "" && strings.TrimSpace(p.Rule) == "")
}

func proxySettingsFromAny(raw any) (*proxySettings, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("proxy must be an object or null")
	}
	ps := &proxySettings{
		Password: strings.TrimSpace(cfgStringValue(m, "password")),
		Rule:     strings.TrimSpace(cfgStringValue(m, "rule")),
	}
	if ps.Empty() {
		return nil, nil
	}
	return ps, nil
}

func proxySettingsToMap(ps *proxySettings) map[string]any {
	if ps == nil || ps.Empty() {
		return nil
	}
	out := map[string]any{}
	if strings.TrimSpace(ps.Password) != "" {
		out["password"] = strings.TrimSpace(ps.Password)
	}
	if strings.TrimSpace(ps.Rule) != "" {
		out["rule"] = strings.TrimSpace(ps.Rule)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeProxySettingsIntoConfigJSON(existing string, ps *proxySettings) (string, error) {
	cfg, err := parsePaneConfigJSON(existing)
	if err != nil {
		return "", err
	}
	if ps == nil || ps.Empty() {
		delete(cfg, "proxy")
	} else {
		cfg["proxy"] = proxySettingsToMap(ps)
	}
	if len(cfg) == 0 {
		return "{}", nil
	}
	body, err := jsonMarshal(cfg)
	if err != nil {
		return "", err
	}
	return body, nil
}

func extractProxySettingsFromConfigJSON(configJSON string) *proxySettings {
	cfg, err := parsePaneConfigJSON(configJSON)
	if err != nil {
		return nil
	}
	ps, err := proxySettingsFromAny(cfg["proxy"])
	if err != nil {
		return nil
	}
	return ps
}

func jsonMarshal(v map[string]any) (string, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
