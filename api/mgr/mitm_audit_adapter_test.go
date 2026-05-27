package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The MITM adapter now reuses the gateway's aiGatewayParseResponse (via the
// shared audit session) so reply.json is field-consistent with the gateway.
// Guard the response shapes the old MITM-specific parser used to cover.
func TestMitmReusesGatewayParser(t *testing.T) {
	sseHdr := http.Header{"Content-Type": []string{"text/event-stream"}}

	sse := `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}` + "\n\n" +
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":" world"}}` + "\n\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}` + "\n\n"
	if p := aiGatewayParseResponse(sseHdr, []byte(sse)); p.Answer != "Hello world" || len(p.ToolCalls) != 0 {
		t.Fatalf("anthropic sse: answer=%q tools=%d", p.Answer, len(p.ToolCalls))
	}

	js := `{"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`
	if p := aiGatewayParseResponse(http.Header{}, []byte(js)); p.Answer != "hi" {
		t.Fatalf("anthropic json: answer=%q", p.Answer)
	}
}

// End-to-end: a MITM turn must write current.json + reply.json through the
// gateway's audit session — same single canonical paths (no per-turn
// mitm/<turn>/ subdir), same struct/format/token math as a gateway turn.
func TestMitmAdapterWritesCanonicalSnapshots(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-19003"

	adapter := mitmAuditAdapter{}
	target, _ := url.Parse("https://opencode.ai/zen/v1/messages")
	reqBody := []byte(`{"model":"big-pickle","messages":[{"role":"user","content":"hi"}]}`)
	turn := adapter.StartTurn("anthropic", agent, target, "POST",
		http.Header{"Content-Type": []string{"application/json"}}, reqBody)

	// current.json at the canonical single path; gateway fields populated.
	curPath := aiGatewayCurrentSnapshotPath(agent)
	curData, err := os.ReadFile(curPath)
	if err != nil {
		t.Fatalf("current.json not at canonical path %s: %v", curPath, err)
	}
	if _, err := os.Stat(filepath.Join(aiGatewayHistoryDir(agent), "mitm")); err == nil {
		t.Fatalf("legacy mitm/ subdir should not exist")
	}
	var cur map[string]interface{}
	_ = json.Unmarshal(curData, &cur)
	if cur["model"] != "big-pickle" || cur["url"] != "https://opencode.ai/zen/v1/messages" {
		t.Fatalf("current.json fields: %s", curData)
	}

	resp := `{"content":[{"type":"text","text":"CANON"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`
	rc := turn.WrapResponseBody(io.NopCloser(strings.NewReader(resp)), 200,
		http.Header{"Content-Type": []string{"application/json"}}, int64(len(resp)))
	_, _ = io.ReadAll(rc)
	_ = rc.Close()

	// reply.json at the canonical path, gateway-lite format (items + tokens).
	replyData, err := os.ReadFile(aiGatewayReplySnapshotPath(agent))
	if err != nil {
		t.Fatalf("reply.json not at canonical path: %v", err)
	}
	var lite map[string]interface{}
	if err := json.Unmarshal(replyData, &lite); err != nil {
		t.Fatalf("reply.json parse: %v", err)
	}
	if s, _ := lite["status"].(string); s == "failed" {
		t.Fatalf("status should not be failed: %s", replyData)
	}
	if lite["input_tokens"] != float64(5) || lite["output_tokens"] != float64(3) {
		t.Fatalf("tokens not parsed via shared session: %s", replyData)
	}
	items, _ := lite["items"].([]interface{})
	if len(items) == 0 {
		t.Fatalf("items empty: %s", replyData)
	}
	found := false
	for _, it := range items {
		b, _ := it.(map[string]interface{})
		if b["type"] == "text" && b["text"] == "CANON" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reply items missing text 'CANON': %s", replyData)
	}
}
