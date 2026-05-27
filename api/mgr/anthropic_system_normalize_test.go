package main

import (
	"encoding/json"
	"testing"
)

// Guards the fix for: strict Anthropic-compatible gateways (new-api behind
// gateway.cicy-ai.com) reject `system` unless it's a TextBlock array. Prompt
// injection leaves it as a string (or a string/object mix), so normalization
// must coerce every shape into [{"type":"text","text":...}].
func TestAgentInspectorNormalizeAnthropicSystem(t *testing.T) {
	asBlocks := func(v any) []map[string]any {
		arr, ok := v.([]interface{})
		if !ok {
			t.Fatalf("system is not an array: %T", v)
		}
		out := make([]map[string]any, 0, len(arr))
		for _, e := range arr {
			m, ok := e.(map[string]interface{})
			if !ok {
				t.Fatalf("system element is not an object: %T", e)
			}
			out = append(out, m)
		}
		return out
	}

	// 1. string system → single text block
	b := agentInspectorNormalizeAnthropicSystem(map[string]interface{}{"system": "you are helpful"})
	blocks := asBlocks(b["system"])
	if len(blocks) != 1 || blocks[0]["type"] != "text" || blocks[0]["text"] != "you are helpful" {
		t.Fatalf("string case: %#v", b["system"])
	}

	// 2. mixed array (bare string + object, object missing type) → all proper text blocks
	b = agentInspectorNormalizeAnthropicSystem(map[string]interface{}{
		"system": []interface{}{
			"injected rules",
			map[string]interface{}{"text": "base prompt"},
			map[string]interface{}{"type": "text", "text": "already typed"},
		},
	})
	blocks = asBlocks(b["system"])
	if len(blocks) != 3 {
		t.Fatalf("mixed case len: %#v", b["system"])
	}
	for i, blk := range blocks {
		if blk["type"] != "text" {
			t.Fatalf("mixed case block %d missing type: %#v", i, blk)
		}
	}

	// 3. empty string → drop system entirely
	b = agentInspectorNormalizeAnthropicSystem(map[string]interface{}{"system": "   "})
	if _, ok := b["system"]; ok {
		t.Fatalf("empty string should drop system: %#v", b["system"])
	}

	// 4. no system key → untouched
	b = agentInspectorNormalizeAnthropicSystem(map[string]interface{}{"model": "x"})
	if _, ok := b["system"]; ok {
		t.Fatalf("should not add system: %#v", b)
	}

	// 5. result must marshal to valid JSON with array system
	b = agentInspectorNormalizeAnthropicSystem(map[string]interface{}{"system": "hi"})
	out, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := round["system"].([]interface{}); !ok {
		t.Fatalf("system should be array after round-trip: %s", out)
	}
}
