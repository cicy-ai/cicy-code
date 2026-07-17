// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// Fresh install: default block carries doubaoVoice + the voice route already;
// the backfill must then be a no-op.
func TestVoiceSeed_FreshInstall(t *testing.T) {
	withTempGlobalJSON(t)

	ensureDefaultProviders()
	ensureVoiceProvider()

	defaults, keys := ocSeedReadProviders(t)
	if defaults["voice"] != "doubaoVoice" {
		t.Errorf("default[voice] = %v, want doubaoVoice", defaults["voice"])
	}
	if n := countKey(keys, "doubaoVoice"); n != 1 {
		t.Errorf("doubaoVoice item count = %d, want 1", n)
	}
}

// Existing install without any speech provider: backfill seeds the keyless
// item and points the voice route at it; idempotent on a second run.
func TestVoiceSeed_BackfillExistingInstall(t *testing.T) {
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

	ensureVoiceProvider()
	ensureVoiceProvider()

	defaults, keys := ocSeedReadProviders(t)
	if defaults["voice"] != "doubaoVoice" {
		t.Errorf("default[voice] = %v, want doubaoVoice", defaults["voice"])
	}
	if n := countKey(keys, "doubaoVoice"); n != 1 {
		t.Errorf("doubaoVoice item count = %d, want 1 (idempotence)", n)
	}
}

// An operator with an openspeech.bytedance.com provider under a custom key
// keeps theirs as the route target; no duplicate is seeded.
func TestVoiceSeed_ReusesOperatorProvider(t *testing.T) {
	withTempGlobalJSON(t)

	cfg := map[string]any{"providers": map[string]any{
		"default": map[string]any{},
		"items": []any{
			map[string]any{"key": "myDoubao", "name": "豆包", "url": "https://openspeech.bytedance.com", "apiKey": "uuid-x"},
		},
	}}
	if err := writeGlobalJSONConfig(cfg); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	ensureVoiceProvider()

	defaults, keys := ocSeedReadProviders(t)
	if defaults["voice"] != "myDoubao" {
		t.Errorf("default[voice] = %v, want myDoubao", defaults["voice"])
	}
	if countKey(keys, "doubaoVoice") != 0 {
		t.Errorf("doubaoVoice was seeded next to an existing openspeech provider")
	}
}

// The provider write path must accept protocol "voice" (else the seeded item
// could never be edited through the API — not even to fill its key).
func TestVoiceSeed_ProtocolVoiceAccepted(t *testing.T) {
	if _, err := sanitizeProviderDraft(map[string]any{
		"key": "doubaoVoice", "name": "豆包语音", "protocol": "voice",
		"url": "https://openspeech.bytedance.com", "apiKey": "uuid",
	}, nil); err != nil {
		t.Fatalf("protocol voice rejected: %v", err)
	}
	if _, err := sanitizeProviderDraft(map[string]any{
		"key": "x", "name": "x", "protocol": "bogus", "url": "https://x", "apiKey": "",
	}, nil); err == nil {
		t.Fatalf("bogus protocol accepted")
	}
}
