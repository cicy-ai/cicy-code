package main

import "testing"

// Real opencode request shapes captured from a non-gateway (MITM) run.
// The title-generation request runs concurrently with the main answer and must
// NOT pollute reply.json/current.json — it is classified "title" and lands in
// title.json instead.
func TestAiGatewayAuxiliaryKind(t *testing.T) {
	titleBody := map[string]interface{}{
		"model":      "big-pickle",
		"max_tokens": 32000,
		"tools":      []interface{}{},
		"messages": []interface{}{
			map[string]interface{}{"role": "system", "content": "You are a title generator. You output ONLY a thread title. Nothing else."},
			map[string]interface{}{"role": "user", "content": "Generate a title for this conversation:\n"},
			map[string]interface{}{"role": "user", "content": "归并排序怎么实现"},
		},
	}
	mainBody := map[string]interface{}{
		"model":      "big-pickle",
		"max_tokens": 32000,
		"tools":      []interface{}{map[string]interface{}{"name": "bash"}},
		"messages": []interface{}{
			map[string]interface{}{"role": "system", "content": "You are opencode, an interactive CLI tool that helps users with software engineering tasks."},
			map[string]interface{}{"role": "user", "content": "归并排序怎么实现"},
		},
	}
	// Anthropic-native shape: system is a top-level string.
	titleAnthropic := map[string]interface{}{
		"system": "You are a title generator. You output ONLY a thread title.",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Generate a title for this conversation:\nfoo"},
		},
	}
	suggestionMeta := map[string]interface{}{
		"metadata": map[string]interface{}{"purpose": "suggestion"},
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}
	// FALSE-POSITIVE GUARD: a normal coding conversation that merely *discusses*
	// the title-generator feature (its system prompt is the agent prompt, and the
	// title phrases only appear in user/assistant content) must NOT be classified
	// as a title request — otherwise its real reply.json/current.json get skipped.
	discussTitleFeature := map[string]interface{}{
		"system": "You are Claude Code, Anthropic's official CLI for Claude.",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "implement: detect when system is \"You are a title generator\" and the user sends \"Generate a title for this conversation\""},
			map[string]interface{}{"role": "assistant", "content": "Done — title.json now holds the title generator output."},
		},
	}
	// Anthropic main request whose system is a block array (not a string).
	anthropicMain := map[string]interface{}{
		"system": []interface{}{
			map[string]interface{}{"type": "text", "text": "You are opencode, an interactive CLI tool."},
		},
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "generate a title for this conversation please"}},
	}

	cases := []struct {
		name     string
		question string
		body     map[string]interface{}
		want     string
	}{
		{"title-openai", "归并排序怎么实现", titleBody, "title"},
		{"title-anthropic", "foo", titleAnthropic, "title"},
		{"main-answer", "归并排序怎么实现", mainBody, ""},
		{"suggestion-question", "[SUGGESTION MODE: next step]", nil, "suggestion"},
		{"suggestion-meta", "hi", suggestionMeta, "suggestion"},
		{"discuss-title-feature-not-title", "", discussTitleFeature, ""},
		{"anthropic-main-mentions-title-not-title", "", anthropicMain, ""},
		// Claude Code quota health-check: max_tokens:1 + "quota" → probe (auxiliary),
		// must NOT pollute current/reply.json as a stray "quota" turn.
		{"quota-probe", "quota", map[string]interface{}{"model": "claude-haiku-4-5-20251001", "max_tokens": 1, "messages": []interface{}{map[string]interface{}{"role": "user", "content": "quota"}}}, "probe"},
		// A real turn with a normal max_tokens is NOT a probe.
		{"normal-max-tokens-not-probe", "实现归并排序", map[string]interface{}{"max_tokens": 8000, "messages": []interface{}{map[string]interface{}{"role": "user", "content": "实现归并排序"}}}, ""},
	}
	for _, c := range cases {
		if got := aiGatewayAuxiliaryKind(c.question, c.body); got != c.want {
			t.Errorf("%s: aiGatewayAuxiliaryKind = %q, want %q", c.name, got, c.want)
		}
	}
}
