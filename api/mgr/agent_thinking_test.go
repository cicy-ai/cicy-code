package main

import (
	"reflect"
	"testing"
)

func TestNormalizeThinkingMode(t *testing.T) {
	cases := map[string]string{
		"disabled": "disabled", "off": "disabled", "false": "disabled", "0": "disabled",
		"enabled": "enabled", "on": "enabled", "true": "enabled", "1": "enabled",
		"passthrough": "passthrough", "as-is": "passthrough",
		"": "", "garbage": "",
	}
	for in, want := range cases {
		if got := normalizeThinkingMode(in); got != want {
			t.Fatalf("normalizeThinkingMode(%q)=%q, want %q", in, got, want)
		}
	}
}

// The gateway must use the CORRECT DeepSeek V4 param (thinking:{type:...}), never
// the legacy enable_thinking (which v4 ignores), and must be driven by `mode`
// (config), not hardcoded.
func TestAgentInspectorApplyThinking(t *testing.T) {
	disabled := map[string]interface{}{"type": "disabled"}
	enabled := map[string]interface{}{"type": "enabled"}

	// disabled (openai): sets thinking:{type:disabled}, strips legacy enable_thinking.
	{
		p := map[string]interface{}{"enable_thinking": false, "extra_body": map[string]interface{}{"enable_thinking": false}}
		p = agentInspectorApplyThinking(p, "openai", "disabled")
		if _, ok := p["enable_thinking"]; ok {
			t.Fatalf("disabled: legacy enable_thinking must be stripped")
		}
		if !reflect.DeepEqual(p["thinking"], disabled) {
			t.Fatalf("disabled openai: thinking=%#v, want %#v", p["thinking"], disabled)
		}
		if eb, ok := p["extra_body"].(map[string]interface{}); ok {
			if _, ok := eb["enable_thinking"]; ok {
				t.Fatalf("disabled: extra_body.enable_thinking must be stripped")
			}
		}
	}

	// disabled (anthropic): MUST be explicit {"type":"disabled"} — third-party
	// anthropic-compatible fronts default thinking ON when the field is omitted
	// (the w-1001 "thinking won't turn off" bug), so omission is not a disable.
	{
		p := map[string]interface{}{"thinking": map[string]interface{}{"type": "enabled"}}
		p = agentInspectorApplyThinking(p, "anthropic", "disabled")
		if !reflect.DeepEqual(p["thinking"], disabled) {
			t.Fatalf("disabled anthropic: thinking=%#v, want %#v", p["thinking"], disabled)
		}
	}

	// enabled: thinking:{type:enabled}.
	{
		p := agentInspectorApplyThinking(map[string]interface{}{"enable_thinking": false}, "openai", "enabled")
		if !reflect.DeepEqual(p["thinking"], enabled) {
			t.Fatalf("enabled: thinking=%#v, want %#v", p["thinking"], enabled)
		}
	}

	// passthrough: respect whatever the client sent (don't override thinking), but
	// still strip the legacy enable_thinking.
	{
		clientThinking := map[string]interface{}{"type": "enabled"}
		p := map[string]interface{}{"thinking": clientThinking, "enable_thinking": true}
		p = agentInspectorApplyThinking(p, "openai", "passthrough")
		if _, ok := p["enable_thinking"]; ok {
			t.Fatalf("passthrough: legacy enable_thinking must still be stripped")
		}
		if !reflect.DeepEqual(p["thinking"], clientThinking) {
			t.Fatalf("passthrough: client thinking must be preserved, got %#v", p["thinking"])
		}
	}
}
