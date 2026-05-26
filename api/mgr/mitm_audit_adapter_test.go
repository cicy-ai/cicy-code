package main

import "testing"

func TestMitmParseUpstream(t *testing.T) {
	// Anthropic streaming, plain text answer, ends end_turn → no tool calls.
	sse := "event: content_block_delta\n" +
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}` + "\n\n" +
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":" world"}}` + "\n\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}` + "\n\n"
	if ans, tools := mitmParseUpstream([]byte(sse)); ans != "Hello world" || tools {
		t.Fatalf("anthropic sse: ans=%q tools=%v", ans, tools)
	}

	// Anthropic streaming ending on tool_use → tool calls in flight.
	tool := `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}` + "\n\n"
	if _, tools := mitmParseUpstream([]byte(tool)); !tools {
		t.Fatalf("anthropic tool_use: expected hasToolCalls")
	}

	// Non-streaming Anthropic JSON.
	js := `{"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`
	if ans, tools := mitmParseUpstream([]byte(js)); ans != "hi" || tools {
		t.Fatalf("anthropic json: ans=%q tools=%v", ans, tools)
	}

	// OpenAI streaming with finish_reason tool_calls.
	oai := `data: {"choices":[{"delta":{"content":"x"},"finish_reason":"tool_calls"}]}` + "\n\n"
	if _, tools := mitmParseUpstream([]byte(oai)); !tools {
		t.Fatalf("openai tool_calls: expected hasToolCalls")
	}

	// Empty body.
	if ans, tools := mitmParseUpstream(nil); ans != "" || tools {
		t.Fatalf("empty: ans=%q tools=%v", ans, tools)
	}
}
