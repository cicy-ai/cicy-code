package main

import (
	"encoding/json"
	"testing"
)

// Reproduce locally the EXACT codex /responses request body w-10026 sent to the
// box, run the gateway's request transform, and print/assert the chat/completions
// body it produces. If this body matches the working A/B chat bodies, the empty
// reply is NOT the request transform; if it drops the user message, it is.
func TestTransformResponsesRequest_SimpleUserTurn(t *testing.T) {
	src := []byte(`{"model":"deepseek-v4-pro","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"reply with exactly CXOK_8899"}]}]}`)

	out, model, err := transformResponsesRequestToChatCompletions(src, "")
	if err != nil {
		t.Fatalf("transform error: %v", err)
	}
	t.Logf("resolved model = %q", model)
	t.Logf("transformed chat/completions body =\n%s", string(out))

	var body map[string]interface{}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("transformed body not valid JSON: %v", err)
	}

	msgs, ok := body["messages"].([]interface{})
	if !ok || len(msgs) == 0 {
		t.Fatalf("transform produced NO messages (this would make the upstream return empty)\nbody=%s", string(out))
	}
	// The user's text MUST survive into a user message.
	last, _ := msgs[len(msgs)-1].(map[string]interface{})
	if last == nil || last["role"] != "user" {
		t.Fatalf("last message is not the user turn: %+v", last)
	}
	if c, _ := last["content"].(string); c != "reply with exactly CXOK_8899" {
		t.Fatalf("user content mangled/dropped: %q", last["content"])
	}
	if s, _ := body["stream"].(bool); !s {
		t.Errorf("stream flag lost — upstream may not stream (rewrap then sees a non-SSE body)")
	}
}
