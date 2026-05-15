package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeFunctionParametersFillsMissingFields(t *testing.T) {
	out := sanitizeFunctionParameters(map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}},
	})
	if _, ok := out["required"].([]interface{}); !ok {
		t.Fatalf("required must be []interface{}, got %T", out["required"])
	}
}

func TestSanitizeFunctionParametersNullParameters(t *testing.T) {
	out := sanitizeFunctionParameters(nil)
	if out["type"] != "object" {
		t.Fatalf("type=object expected, got %v", out["type"])
	}
	if _, ok := out["properties"].(map[string]interface{}); !ok {
		t.Fatalf("properties must be object, got %T", out["properties"])
	}
	if _, ok := out["required"].([]interface{}); !ok {
		t.Fatalf("required must be array, got %T", out["required"])
	}
}

func TestSanitizeFunctionParametersNullChildren(t *testing.T) {
	out := sanitizeFunctionParameters(map[string]interface{}{
		"properties": nil,
		"required":   nil,
	})
	if _, ok := out["properties"].(map[string]interface{}); !ok {
		t.Fatalf("properties null should be replaced, got %T", out["properties"])
	}
	if _, ok := out["required"].([]interface{}); !ok {
		t.Fatalf("required null should be replaced, got %T", out["required"])
	}
}

func TestResponsesToChatCompletionsToolsHaveRequired(t *testing.T) {
	body := []byte(`{
		"model": "deepseek-v4-pro",
		"input": [{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"tools": [{"type":"function","name":"read_file","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}]
	}`)
	out, _, err := transformResponsesRequestToChatCompletions(body)
	if err != nil {
		t.Fatalf("translation error: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tools, _ := got["tools"].([]interface{})
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	tool := tools[0].(map[string]interface{})
	fn := tool["function"].(map[string]interface{})
	params := fn["parameters"].(map[string]interface{})
	if _, ok := params["required"].([]interface{}); !ok {
		t.Fatalf("parameters.required missing — DeepSeek will reject")
	}
	// Sanity: still serializes valid JSON with no null required.
	if strings.Contains(string(out), `"required":null`) {
		t.Fatalf("required must not serialize as null")
	}
}

func TestMessagesToChatCompletionsToolsHaveRequired(t *testing.T) {
	body := []byte(`{
		"model": "deepseek-v4-pro",
		"messages": [{"role":"user","content":"hi"}],
		"tools": [{"name":"read_file","description":"r","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}]
	}`)
	out, _, err := transformMessagesRequestToChatCompletions(body)
	if err != nil {
		t.Fatalf("translation error: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tools, _ := got["tools"].([]interface{})
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	fn := tools[0].(map[string]interface{})["function"].(map[string]interface{})
	params := fn["parameters"].(map[string]interface{})
	if _, ok := params["required"].([]interface{}); !ok {
		t.Fatalf("parameters.required missing — DeepSeek will reject")
	}
}
