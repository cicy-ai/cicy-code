package main

import (
	"strings"
	"testing"
)

func TestProviderRequestViewExtractsCodexHTTPSFallbackPromptAndTools(t *testing.T) {
	current := aiGatewayCurrentSnapshot{
		Provider: "openai",
		Model:    "gpt-test",
		Method:   "POST",
		URL:      "https://chatgpt.com/backend-api/codex/responses",
		Body: M{
			"model": "gpt-test",
			"input": []interface{}{
				M{
					"type": "additional_tools",
					"role": "developer",
					"tools": []interface{}{
						M{
							"type":        "function",
							"name":        "exec_command",
							"description": "Run a command",
							"parameters": M{
								"type": "object",
								"properties": M{
									"cmd": M{"type": "string", "description": "Command to run"},
								},
								"required": []interface{}{"cmd"},
							},
						},
					},
				},
				M{
					"type": "message",
					"role": "developer",
					"content": []interface{}{
						M{"type": "input_text", "text": "You are Codex. Follow repository instructions."},
					},
				},
				M{
					"type": "message",
					"role": "user",
					"content": []interface{}{
						M{"type": "input_text", "text": "Fix the bug"},
					},
				},
			},
		},
	}

	view := agentInspectorProviderRequestView("w-test", current, aiGatewayReplySnapshot{})
	if got := aiGatewayString(view["request_kind"]); got != "openai_responses" {
		t.Fatalf("request kind = %q, want openai_responses", got)
	}
	if got := aiGatewayInt(view["tool_count"]); got != 1 {
		t.Fatalf("tool count = %d, want 1; view=%#v", got, view)
	}

	sections, ok := view["sections"].([]agentInspectorProviderRequestSection)
	if !ok {
		t.Fatalf("unexpected sections type: %T", view["sections"])
	}
	var developer, tools agentInspectorProviderRequestSection
	for _, section := range sections {
		switch section.Type {
		case "developer_messages":
			developer = section
		case "tools":
			tools = section
		}
	}
	developerItems := developer.Items
	if len(developerItems) != 1 || !strings.Contains(aiGatewayString(aiGatewayMap(developerItems[0])["text"]), "You are Codex") {
		t.Fatalf("developer prompt missing: %#v", developer)
	}
	toolItems := tools.Items
	if len(toolItems) != 1 || aiGatewayString(aiGatewayMap(toolItems[0])["name"]) != "exec_command" {
		t.Fatalf("embedded tools missing: %#v", tools)
	}
}

func TestProviderRequestViewExtractsOpenCodeChatCompletionsSystemPrompt(t *testing.T) {
	current := aiGatewayCurrentSnapshot{
		Provider: "openai",
		Model:    "big-pickle",
		Method:   "POST",
		URL:      "https://opencode.ai/zen/v1/chat/completions",
		Body: M{
			"model": "big-pickle",
			"messages": []interface{}{
				M{"role": "system", "content": "You are opencode. Follow AGENTS.md."},
				M{"role": "user", "content": "Fix the bug"},
			},
		},
	}

	view := agentInspectorProviderRequestView("w-test", current, aiGatewayReplySnapshot{})
	if got := aiGatewayString(view["request_kind"]); got != "openai_chat_completions" {
		t.Fatalf("request kind = %q, want openai_chat_completions", got)
	}
	sections, ok := view["sections"].([]agentInspectorProviderRequestSection)
	if !ok {
		t.Fatalf("unexpected sections type: %T", view["sections"])
	}
	var prompt agentInspectorProviderRequestSection
	for _, section := range sections {
		if section.Type == "prompt" {
			prompt = section
			break
		}
	}
	if len(prompt.Items) != 1 || !strings.Contains(aiGatewayString(aiGatewayMap(prompt.Items[0])["text"]), "You are opencode") {
		t.Fatalf("system prompt missing: %#v", prompt)
	}
}
