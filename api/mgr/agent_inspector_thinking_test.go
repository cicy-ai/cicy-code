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

func TestAgentInspectorDisableThinkingSwitchesOff(t *testing.T) {
	out := agentInspectorDisableThinking(map[string]interface{}{
		"thinking":        map[string]interface{}{"type": "enabled", "budget_tokens": 1024},
		"enable_thinking": true,
	})
	if _, present := out["thinking"]; present {
		t.Fatalf("`thinking` config should be stripped")
	}
	if v, _ := out["enable_thinking"].(bool); v {
		t.Fatalf("`enable_thinking` must be forced to false")
	}
}

func TestAgentInspectorDisableThinkingInjectsAnthropicPlaceholder(t *testing.T) {
	out := agentInspectorDisableThinking(map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "hello"},
				},
			},
		},
	})
	msgs := out["messages"].([]interface{})
	content := msgs[0].(map[string]interface{})["content"].([]interface{})
	if len(content) != 2 {
		t.Fatalf("want 2 blocks after injection, got %d", len(content))
	}
	first := content[0].(map[string]interface{})
	if first["type"] != "thinking" {
		t.Fatalf("first block should be thinking, got %v", first["type"])
	}
}

func TestAgentInspectorDisableThinkingInjectsOpenAIPlaceholder(t *testing.T) {
	out := agentInspectorDisableThinking(map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "assistant", "content": "hello"},
		},
	})
	msg := out["messages"].([]interface{})[0].(map[string]interface{})
	if _, ok := msg["reasoning_content"]; !ok {
		t.Fatalf("OpenAI-style assistant must get reasoning_content placeholder")
	}
}

func TestAgentInspectorDisableThinkingPreservesExistingThinking(t *testing.T) {
	out := agentInspectorDisableThinking(map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "thinking", "thinking": "real reasoning", "signature": "sig"},
					map[string]interface{}{"type": "text", "text": "hello"},
				},
			},
		},
	})
	content := out["messages"].([]interface{})[0].(map[string]interface{})["content"].([]interface{})
	if len(content) != 2 {
		t.Fatalf("must not duplicate when thinking already present")
	}
	first := content[0].(map[string]interface{})
	if first["thinking"] != "real reasoning" {
		t.Fatalf("existing thinking content must be preserved, got %v", first["thinking"])
	}
}

func TestAgentInspectorDisableThinkingLeavesUserMessages(t *testing.T) {
	out := agentInspectorDisableThinking(map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	msg := out["messages"].([]interface{})[0].(map[string]interface{})
	if msg["content"] != "hi" {
		t.Fatalf("user messages must be left alone")
	}
	if _, present := msg["reasoning_content"]; present {
		t.Fatalf("user messages must not get reasoning_content")
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
