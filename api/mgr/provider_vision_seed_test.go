// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// Fresh install: the default block already carries zhipuVision + the vision
// route; ensureVisionProvider must then be a no-op (no duplicate, route kept).
func TestVisionSeed_FreshInstall(t *testing.T) {
	withTempGlobalJSON(t)

	ensureDefaultProviders()
	ensureVisionProvider()

	defaults, keys := ocSeedReadProviders(t)
	if defaults["vision"] != "zhipuVision" {
		t.Errorf("default[vision] = %v, want zhipuVision", defaults["vision"])
	}
	if n := countKey(keys, "zhipuVision"); n != 1 {
		t.Errorf("zhipuVision item count = %d, want 1", n)
	}
}

// Existing install WITHOUT any zhipu provider: backfill seeds the keyless item
// and points the vision route at it. Second run must not duplicate.
func TestVisionSeed_BackfillExistingInstall(t *testing.T) {
	withTempGlobalJSON(t)

	cfg := map[string]any{"providers": map[string]any{
		"default": map[string]any{"claude": "deepseek_claude"},
		"items": []any{
			map[string]any{"key": "deepseek_claude", "name": "DeepSeek", "url": "https://api.deepseek.com/anthropic"},
		},
	}}
	if err := writeGlobalJSONConfig(cfg); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	ensureVisionProvider()
	ensureVisionProvider() // idempotent

	defaults, keys := ocSeedReadProviders(t)
	if defaults["vision"] != "zhipuVision" {
		t.Errorf("default[vision] = %v, want zhipuVision", defaults["vision"])
	}
	if n := countKey(keys, "zhipuVision"); n != 1 {
		t.Errorf("zhipuVision item count = %d, want 1 (idempotence)", n)
	}
}

// Existing install that ALREADY has a bigmodel.cn provider under a custom key:
// no duplicate is seeded — the vision route targets the operator's provider.
func TestVisionSeed_ReusesOperatorZhipuProvider(t *testing.T) {
	withTempGlobalJSON(t)

	cfg := map[string]any{"providers": map[string]any{
		"default": map[string]any{},
		"items": []any{
			map[string]any{"key": "myZhipu", "name": "智谱 GLM", "url": "https://open.bigmodel.cn/api/paas/v4", "apiKey": "sk-x"},
		},
	}}
	if err := writeGlobalJSONConfig(cfg); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	ensureVisionProvider()

	defaults, keys := ocSeedReadProviders(t)
	if defaults["vision"] != "myZhipu" {
		t.Errorf("default[vision] = %v, want myZhipu (operator's provider)", defaults["vision"])
	}
	if countKey(keys, "zhipuVision") != 0 {
		t.Errorf("zhipuVision was seeded next to an existing bigmodel.cn provider")
	}
}

// An operator-set vision route is never overwritten.
func TestVisionSeed_KeepsExistingRoute(t *testing.T) {
	withTempGlobalJSON(t)

	cfg := map[string]any{"providers": map[string]any{
		"default": map[string]any{"vision": "custom"},
		"items": []any{
			map[string]any{"key": "custom", "name": "X", "url": "https://example.com/v1"},
		},
	}}
	if err := writeGlobalJSONConfig(cfg); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	ensureVisionProvider()

	defaults, _ := ocSeedReadProviders(t)
	if defaults["vision"] != "custom" {
		t.Errorf("default[vision] = %v, want custom (operator route kept)", defaults["vision"])
	}
}

func countKey(keys []string, want string) int {
	n := 0
	for _, k := range keys {
		if k == want {
			n++
		}
	}
	return n
}
