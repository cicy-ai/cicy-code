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

func ocSeedReadProviders(t *testing.T) (defaults map[string]any, itemKeys []string) {
	t.Helper()
	cfg := readGlobalJSONConfig()
	block, _ := cfg["providers"].(map[string]any)
	if block == nil {
		t.Fatalf("providers block missing")
	}
	defaults, _ = block["default"].(map[string]any)
	for _, it := range providersItemsSlice(block) {
		itemKeys = append(itemKeys, providerItemKey(it))
	}
	return
}

func ocSeedContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// Fresh install: ensureDefaultProviders seeds the block (default routing now sends
// cicy/codex/opencode to opencodeZen), then ensureOpenCodeZenProvider adds the item.
func TestOpenCodeZenSeed_FreshInstall(t *testing.T) {
	withTempGlobalJSON(t)

	ensureDefaultProviders()
	ensureOpenCodeZenProvider()

	defaults, keys := ocSeedReadProviders(t)
	for _, at := range []string{"cicy", "codex", "opencode"} {
		if defaults[at] != "opencodeZen" {
			t.Errorf("default[%q] = %v, want opencodeZen", at, defaults[at])
		}
	}
	if defaults["claude"] != "defaultAnthropic" {
		t.Errorf("default[claude] = %v, want defaultAnthropic (claude needs an anthropic provider)", defaults["claude"])
	}
	if !ocSeedContains(keys, "opencodeZen") {
		t.Errorf("opencodeZen item missing; keys=%v", keys)
	}
}

// Existing install: items already present (so ensureDefaultProviders no-ops and the
// operator's default routing is preserved); ensureOpenCodeZenProvider only tops up
// the missing item, idempotently.
func TestOpenCodeZenSeed_TopUpExisting(t *testing.T) {
	withTempGlobalJSON(t)

	seed := map[string]any{"providers": map[string]any{
		"default": map[string]any{"cicy": "defaultAnthropic"},
		"items":   []any{map[string]any{"key": "defaultAnthropic", "protocol": "anthropic"}},
	}}
	if err := writeGlobalJSONConfig(seed); err != nil {
		t.Fatal(err)
	}

	ensureDefaultProviders()    // must no-op (items already exist)
	ensureOpenCodeZenProvider() // must append opencodeZen item only

	defaults, keys := ocSeedReadProviders(t)
	if defaults["cicy"] != "defaultAnthropic" {
		t.Errorf("existing default[cicy] clobbered: got %v", defaults["cicy"])
	}
	if !ocSeedContains(keys, "opencodeZen") {
		t.Errorf("opencodeZen not topped up; keys=%v", keys)
	}

	before := len(keys)
	ensureOpenCodeZenProvider() // second run must not duplicate
	_, keys2 := ocSeedReadProviders(t)
	if len(keys2) != before {
		t.Errorf("opencodeZen duplicated: %v", keys2)
	}
}
