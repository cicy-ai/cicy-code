package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestEnsureGlobalAPITokenCreatesProxyToken(t *testing.T) {
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
	if anyString(cfg["proxy_token"]) == "" {
		t.Fatalf("proxy_token should be initialized: %v", cfg["proxy_token"])
	}
}

func TestEnsureGlobalAPITokenPreservesProxyToken(t *testing.T) {
	withTempCicyRoot(t)
	body := `{
  "api_token": "keep-me",
  "proxy_token": "keep-proxy"
}`
	if err := os.WriteFile(cicyGlobalJSONPath, []byte(body), 0644); err != nil {
		t.Fatalf("write global.json: %v", err)
	}
	_, err := ensureGlobalAPIToken(cicyGlobalJSONPath, "keep-me")
	if err != nil {
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
	if anyString(cfg["proxy_token"]) != "keep-proxy" {
		t.Fatalf("proxy_token not preserved: %v", cfg["proxy_token"])
	}
}
