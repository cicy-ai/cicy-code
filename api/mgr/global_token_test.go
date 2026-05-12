package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestEnsureGlobalAPITokenSetsAPIToken(t *testing.T) {
	withTempCicyRoot(t)
	token, err := ensureGlobalAPIToken(cicyGlobalJSONPath, "keep-me")
	if err != nil {
		t.Fatalf("ensureGlobalAPIToken error: %v", err)
	}
	if token != "keep-me" {
		t.Fatalf("api token = %q", token)
	}
	data, err := os.ReadFile(cicyGlobalJSONPath)
	if err != nil {
		t.Fatalf("read global.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode global.json: %v", err)
	}
	if anyString(cfg["api_token"]) != "keep-me" {
		t.Fatalf("api_token = %v", cfg["api_token"])
	}
	// proxy_token is no longer managed by cicy-code; the proxy password lives
	// in ~/cicy-ai/db/mihomo.yaml's globalPassword (seeded by cicy-mihomo).
	if _, ok := cfg["proxy_token"]; ok {
		t.Fatalf("proxy_token should not be created when absent, got: %v", cfg["proxy_token"])
	}
}

func TestEnsureGlobalAPITokenPreservesExistingProxyToken(t *testing.T) {
	// Legacy installs may have proxy_token in global.json. We don't manage it
	// anymore but must not strip it on rewrite.
	withTempCicyRoot(t)
	body := `{
  "api_token": "keep-me",
  "proxy_token": "legacy-value"
}`
	if err := os.WriteFile(cicyGlobalJSONPath, []byte(body), 0644); err != nil {
		t.Fatalf("write global.json: %v", err)
	}
	if _, err := ensureGlobalAPIToken(cicyGlobalJSONPath, "different-token"); err != nil {
		t.Fatalf("ensureGlobalAPIToken error: %v", err)
	}
	data, err := os.ReadFile(cicyGlobalJSONPath)
	if err != nil {
		t.Fatalf("read global.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode global.json: %v", err)
	}
	if anyString(cfg["proxy_token"]) != "legacy-value" {
		t.Fatalf("legacy proxy_token must be preserved, got: %v", cfg["proxy_token"])
	}
}
