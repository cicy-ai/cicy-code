package main

import (
	"path/filepath"
	"testing"
)

// withTempGlobalJSON points global.json at a fresh temp file for the test, so the
// seeders never touch the real ~/cicy-ai/global.json. Safe under `go test ./...`.
func withTempGlobalJSON(t *testing.T) {
	t.Helper()
	saved := cicyGlobalJSONPath
	cicyGlobalJSONPath = filepath.Join(t.TempDir(), "global.json")
	t.Cleanup(func() { cicyGlobalJSONPath = saved })
}

func seedReadProviders(t *testing.T) (defaults map[string]any, items map[string]map[string]any) {
	t.Helper()
	cfg := readGlobalJSONConfig()
	block, _ := cfg["providers"].(map[string]any)
	if block == nil {
		t.Fatalf("providers block missing")
	}
	defaults, _ = block["default"].(map[string]any)
	items = map[string]map[string]any{}
	for _, it := range providersItemsSlice(block) {
		items[providerItemKey(it)] = providerItemMap(it)
	}
	return
}

// Fresh install: the seed is self-contained — every route points at a provider
// that is itself seeded (no dangling opencodeZen), chat routes go to DeepSeek.
func TestProviderSeedIsSelfContainedAndDefaultsToDeepSeek(t *testing.T) {
	withTempGlobalJSON(t)
	ensureDefaultProviders()
	ensureTranslateRoute()
	ensureVisionProvider()
	ensureVoiceProvider()
	defaults, items := seedReadProviders(t)
	want := map[string]string{
		"claude": "deepseek_claude", "cicy": "deepseek_claude",
		"codex": "deepseek", "opencode": "deepseek", "translate": "deepseek",
		"stt": "groqStt", "vision": "zhipuVision", "voice": "doubaoVoice",
	}
	for route, key := range want {
		if defaults[route] != key {
			t.Errorf("default[%s] = %v, want %s", route, defaults[route], key)
		}
		if _, ok := items[defaults[route].(string)]; !ok {
			t.Errorf("route %s points at %v which is not seeded", route, defaults[route])
		}
	}
	if _, ok := items["opencodeZen"]; ok {
		t.Errorf("opencodeZen must not be seeded any more")
	}
}

// Boot must never rewrite an operator's providers: a deleted provider stays
// deleted and an edited one keeps its edits across the whole setup sequence.
func TestProviderSetupNeverOverwritesOperatorConfig(t *testing.T) {
	withTempGlobalJSON(t)
	ensureDefaultProviders()
	ensureTranslateRoute()
	cfg := readGlobalJSONConfig()
	block := providersBlock(cfg)
	var kept []any
	for _, it := range providersItemsSlice(block) {
		m := providerItemMap(it)
		switch providerItemKey(it) {
		case "deepseek":
			continue // operator deleted it
		case "deepseek_claude":
			m["url"] = "https://my-relay.example/anthropic" // operator edited it
			m["models"] = []any{"deepseek-v4-pro"}
		}
		kept = append(kept, it)
	}
	block["items"] = kept
	cfg["providers"] = block
	if err := writeGlobalJSONConfig(cfg); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ { // two boots
		ensureDefaultProviders()
		ensureTranslateRoute()
		applyGatewayEnvToDefaultProviders()
		ensureVisionProvider()
		ensureVoiceProvider()
	}
	_, items := seedReadProviders(t)
	if _, ok := items["deepseek"]; ok {
		t.Errorf("deleted provider came back on boot")
	}
	if _, ok := items["opencodeZen"]; ok {
		t.Errorf("opencodeZen resurrected on boot")
	}
	if items["deepseek_claude"]["url"] != "https://my-relay.example/anthropic" {
		t.Errorf("operator edit overwritten: %v", items["deepseek_claude"]["url"])
	}
}

// Compatibility helpers for the vision / voice seed tests.
func ocSeedReadProviders(t *testing.T) (defaults map[string]any, itemKeys []string) {
	t.Helper()
	d, items := seedReadProviders(t)
	for k := range items {
		itemKeys = append(itemKeys, k)
	}
	return d, itemKeys
}

func ocSeedContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
