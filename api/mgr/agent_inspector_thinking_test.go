package main

import (
	"strings"
	"testing"
)

func TestShouldDisableThinkingForHostKeepsOfficialAPIsUntouched(t *testing.T) {
	for _, h := range []string{"api.openai.com", "api.openai.com:443", "API.Anthropic.com"} {
		if shouldDisableThinkingForHost(h) {
			t.Fatalf("must not rewrite thinking for official host %q", h)
		}
	}
}

func TestShouldDisableThinkingForHostAppliesToEveryoneElse(t *testing.T) {
	for _, h := range []string{"47.251.54.120:8029", "api.deepseek.com", "newapi.local", ""} {
		if !shouldDisableThinkingForHost(h) {
			t.Fatalf("must rewrite thinking for non-official host %q", h)
		}
	}
}

func TestShouldDisableThinkingForHostBoundary(t *testing.T) {
	// Lookalikes that should NOT match the official-API allow-list.
	for _, h := range []string{"api.openai.com.evil.example", "evil-api.anthropic.com.attacker"} {
		if !shouldDisableThinkingForHost(h) {
			t.Fatalf("lookalike %q must still get the thinking rewrite", h)
		}
	}
	// strings.Contains keeps real subdomains safe; document the trade-off.
	_ = strings.Contains
}

// NOTE: the old agentInspectorDisableThinking (hardcoded enable_thinking=false +
// synthetic thinking-block injection) was replaced by the config-driven
// agentInspectorApplyThinking using the correct DeepSeek V4 `thinking:{type:...}`
// param — see TestAgentInspectorApplyThinking / TestNormalizeThinkingMode.
