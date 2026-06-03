package main

// Alignment guarantees for codex turns: what lands in reply.json (the items the
// history/chat UI renders) must match what the codex CLI actually produced (the
// tmux pane output) — for BOTH the non-gateway path (ChatGPT-account Responses
// API over MITM, which uses `custom_tool_call`) and the gateway path (codex via
// the local gateway, which uses `function_call`). These drive the exact same
// production code path the proxy/MITM use: newAIGatewayAuditSession →
// newAIGatewayAuditReadCloser (live SSE stream) → completeFromResponse.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

// runCodexStreamTurn streams an SSE response body through the live audit read
// closer exactly like the proxy does, then returns the persisted reply.json
// items. provider/base let the same helper drive gateway and non-gateway.
func runCodexStreamTurn(t *testing.T, provider, agent string, base *url.URL, suffix string, reqBody, sseBody string) []map[string]interface{} {
	t.Helper()
	hdr := http.Header{"Content-Type": []string{"application/json"}}
	s := newAIGatewayAuditSession(provider, agent, base, suffix, "POST", hdr, []byte(reqBody))
	if err := s.writeStartSnapshots(); err != nil {
		t.Fatalf("writeStartSnapshots: %v", err)
	}
	respHdr := http.Header{"Content-Type": []string{"text/event-stream"}}
	rc := newAIGatewayAuditReadCloser(io.NopCloser(bytes.NewReader([]byte(sseBody))), s, 200, respHdr, int64(len(sseBody)))
	if _, err := io.ReadAll(rc); err != nil {
		t.Fatalf("stream read: %v", err)
	}
	_ = rc.Close()

	data, err := os.ReadFile(aiGatewayReplySnapshotPath(agent))
	if err != nil {
		t.Fatalf("read reply.json: %v", err)
	}
	var lite struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(data, &lite); err != nil {
		t.Fatalf("parse reply.json: %v\n%s", err, data)
	}
	return lite.Items
}

func itemsText(items []map[string]interface{}, kind string) string {
	parts := []string{}
	for _, it := range items {
		if aiGatewayString(it["type"]) == kind {
			if v := strings.TrimSpace(aiGatewayString(it[kind])); v != "" {
				parts = append(parts, v)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func toolUseItems(items []map[string]interface{}) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, it := range items {
		if aiGatewayString(it["type"]) == "tool_use" {
			out = append(out, it)
		}
	}
	return out
}

// data: lines that aiGatewayParseSSELine consumes; the JSON `type` field drives
// the Responses-event handling.
func sseData(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: ")
		b.WriteString(e)
		b.WriteString("\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

const codexPatch = `*** Begin Patch
*** Update File: ws-server.js
@@
-const server = new WebSocket.Server({ port: 8080 });
+const server = new WebSocket.Server({ port: 8090 });
*** End Patch`

// NON-GATEWAY codex: apply_patch arrives as ONE custom_tool_call carrying both
// an item id (ctc_…) and a call id (call_…). The input-stream events key it by
// item id; the output-item events key it by call id with the real name. reply.json
// must contain that call ONCE (named apply_patch), never a second generic
// "custom_tool_call"-named copy — otherwise the UI shows the patch twice.
func TestCodexNonGatewayReplyAlignsSingleApplyPatch(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-19101"
	base, _ := url.Parse("https://chatgpt.com")
	reqBody := `{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":"把端口改成 8090"}]}`

	patchJSON, _ := json.Marshal(codexPatch + "\n")
	patchItemJSON, _ := json.Marshal(codexPatch)
	sse := sseData(
		`{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant","content":[]},"output_index":0}`,
		`{"type":"response.output_text.delta","delta":"我只改服务端端口这一处。","item_id":"msg_1","output_index":0}`,
		`{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"我只改服务端端口这一处。"}]},"output_index":0}`,
		`{"type":"response.output_item.added","item":{"id":"ctc_1","type":"custom_tool_call","call_id":"call_1","input":"","name":"apply_patch"},"output_index":1}`,
		`{"type":"response.custom_tool_call_input.delta","delta":"*** Begin Patch\n","item_id":"ctc_1","output_index":1}`,
		`{"type":"response.custom_tool_call_input.delta","delta":"...","item_id":"ctc_1","output_index":1}`,
		`{"type":"response.custom_tool_call_input.done","input":`+string(patchJSON)+`,"item_id":"ctc_1","output_index":1}`,
		`{"type":"response.output_item.done","item":{"id":"ctc_1","type":"custom_tool_call","call_id":"call_1","input":`+string(patchItemJSON)+`,"name":"apply_patch","status":"completed"},"output_index":1}`,
		`{"type":"response.output_text.delta","delta":"已把端口改成 8090。","item_id":"msg_2","output_index":2}`,
		`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}`,
	)

	items := runCodexStreamTurn(t, "openai", agent, base, "/backend-api/codex/responses", reqBody, sse)

	tools := toolUseItems(items)
	patchTools := 0
	for _, it := range tools {
		name := aiGatewayString(it["name"])
		if name == "custom_tool_call" {
			t.Fatalf("reply.json kept a generic 'custom_tool_call'-named tool_use (the duplicate): %#v\nall items: %s", it, mustJSON(items))
		}
		if strings.Contains(aiGatewayFlattenPromptValue(it["input"]), "WebSocket.Server") {
			patchTools++
		}
	}
	if patchTools != 1 {
		t.Fatalf("apply_patch must appear exactly once, got %d patch tools in %s", patchTools, mustJSON(items))
	}
	// The answer text (what tmux shows) must be present and not duplicated.
	answer := itemsText(items, "text")
	if !strings.Contains(answer, "已把端口改成 8090") {
		t.Fatalf("final answer missing from reply.json: %q", answer)
	}
}

// GATEWAY codex: tools come through as `function_call` (e.g. exec_command), not
// custom_tool_call — so there is no two-id duplication. reply.json must carry the
// tool ONCE with its real name and the answer text, and must NEVER contain a
// "custom_tool_call"-named item (proving the non-gateway dedup leaves it intact).
func TestCodexGatewayReplyAlignsFunctionCall(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-19102"
	base, _ := url.Parse("https://gateway.cicy-ai.com")
	reqBody := `{"model":"deepseek-v4-pro","input":[{"type":"message","role":"user","content":"检查 node 版本"}]}`

	sse := sseData(
		`{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_00_1","name":"exec_command","arguments":""},"output_index":0}`,
		`{"type":"response.function_call_arguments.done","arguments":"{\"cmd\":\"node -v\"}","item_id":"fc_1","output_index":0}`,
		`{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","call_id":"call_00_1","name":"exec_command","arguments":"{\"cmd\":\"node -v\"}"},"output_index":0}`,
		`{"type":"response.output_text.delta","delta":"已执行 node -v。","item_id":"msg_1","output_index":1}`,
		`{"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":3}}}`,
	)

	items := runCodexStreamTurn(t, "openai", agent, base, "/v1/chat/completions", reqBody, sse)

	tools := toolUseItems(items)
	exec := 0
	for _, it := range tools {
		if aiGatewayString(it["name"]) == "custom_tool_call" {
			t.Fatalf("gateway path produced a 'custom_tool_call' item (dedup should never touch it): %s", mustJSON(items))
		}
		if aiGatewayString(it["name"]) == "exec_command" {
			exec++
		}
	}
	if exec != 1 {
		t.Fatalf("exec_command must appear exactly once, got %d in %s", exec, mustJSON(items))
	}
	if a := itemsText(items, "text"); !strings.Contains(a, "已执行 node -v") {
		t.Fatalf("gateway answer missing from reply.json: %q", a)
	}
}

// A codex agentic turn spans multiple requests; after an apply_patch the next
// request's input ends with `custom_tool_call_output`. That must be recognised as
// a tool CONTINUATION so reply.Items keeps accumulating — not reset to a fresh
// turn (which would drop everything before the apply_patch).
func TestCodexContinuationKeepsItemsAcrossCustomToolCallOutput(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-19103"
	base, _ := url.Parse("https://chatgpt.com")
	suffix := "/backend-api/codex/responses"

	// Turn req 1: produces a text + an apply_patch custom_tool_call.
	req1 := `{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":"改端口"}]}`
	patchItemJSON, _ := json.Marshal(codexPatch)
	patchJSON, _ := json.Marshal(codexPatch + "\n")
	sse1 := sseData(
		`{"type":"response.output_text.delta","delta":"我先改端口。","item_id":"msg_1","output_index":0}`,
		`{"type":"response.output_item.added","item":{"id":"ctc_1","type":"custom_tool_call","call_id":"call_1","input":"","name":"apply_patch"},"output_index":1}`,
		`{"type":"response.custom_tool_call_input.delta","delta":"*** Begin Patch\n","item_id":"ctc_1","output_index":1}`,
		`{"type":"response.custom_tool_call_input.done","input":`+string(patchJSON)+`,"item_id":"ctc_1","output_index":1}`,
		`{"type":"response.output_item.done","item":{"id":"ctc_1","type":"custom_tool_call","call_id":"call_1","input":`+string(patchItemJSON)+`,"name":"apply_patch","status":"completed"},"output_index":1}`,
		`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":4}}}`,
	)
	items1 := runCodexStreamTurn(t, "openai", agent, base, suffix, req1, sse1)
	if len(items1) == 0 {
		t.Fatalf("turn 1 produced no items")
	}

	// Continuation request: input ends with custom_tool_call_output (the patch
	// result). Opening the session must INHERIT the prior items.
	req2 := `{"model":"gpt-5.5","input":[
		{"type":"message","role":"user","content":"改端口"},
		{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"...patch..."},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"Update succeeded"}
	]}`
	hdr := http.Header{"Content-Type": []string{"application/json"}}
	s2 := newAIGatewayAuditSession("openai", agent, base, suffix, "POST", hdr, []byte(req2))
	if err := s2.writeStartSnapshots(); err != nil {
		t.Fatalf("turn2 start: %v", err)
	}
	data, _ := os.ReadFile(aiGatewayReplySnapshotPath(agent))
	var lite struct {
		Items []map[string]interface{} `json:"items"`
	}
	_ = json.Unmarshal(data, &lite)
	// The apply_patch from turn 1 must survive into the continuation's reply.json.
	found := false
	for _, it := range lite.Items {
		if aiGatewayString(it["type"]) == "tool_use" && aiGatewayString(it["name"]) == "apply_patch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("continuation after custom_tool_call_output RESET the turn (apply_patch lost): %s", data)
	}
}

func mustJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// The live-tail dedup set must contain ONLY the current turn's intermediate
// assistant text (messages after the last user message) — never a past answer.
// Otherwise a live reply that reuses a phrase from an old turn gets wrongly
// blanked (this regressed gateway agents whose history holds hundreds of answers).
func TestCommittedAssistantTextsScopedToCurrentTurn(t *testing.T) {
	current := aiGatewayCurrentSnapshot{
		Body: map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "老问题"},
				map[string]interface{}{"role": "assistant", "content": "历史答案 PAST-ANSWER"},
				map[string]interface{}{"role": "user", "content": "新问题"},
				map[string]interface{}{"role": "assistant", "content": "本轮中间文本 CURRENT-INTERMEDIATE"},
			},
		},
	}
	set := aiGatewayCommittedAssistantTexts(current)
	if !set["本轮中间文本 CURRENT-INTERMEDIATE"] {
		t.Fatalf("current-turn intermediate text should be in the dedup set: %v", set)
	}
	if set["历史答案 PAST-ANSWER"] {
		t.Fatalf("historical answer must NOT be in the dedup set (would blank a reused live reply): %v", set)
	}
}

// A tool-using turn ends on a tool_result (role:user). The dedup scope must skip
// those and anchor on the last REAL question, or it yields an EMPTY set and the
// live tail re-renders the whole turn — duplicating its thinking + every
// intermediate text (the "ui 多了几个 item" bug). Committed thinking must be
// collected too, so the live tail doesn't repeat the thinking the committed turn
// now renders.
func TestCommittedTurnSkipsToolResultsAndExcludesThinking(t *testing.T) {
	current := aiGatewayCurrentSnapshot{
		Body: map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "老问题"},
				map[string]interface{}{"role": "assistant", "content": "历史答案 PAST"},
				map[string]interface{}{"role": "user", "content": "新问题 NEW-Q"},
				map[string]interface{}{"role": "assistant", "content": []interface{}{
					map[string]interface{}{"type": "thinking", "thinking": "THINK-A"},
					map[string]interface{}{"type": "text", "text": "中间文本 MID"},
					map[string]interface{}{"type": "tool_use", "id": "call_1", "name": "Write", "input": map[string]interface{}{}},
				}},
				map[string]interface{}{"role": "user", "content": []interface{}{
					map[string]interface{}{"type": "tool_result", "tool_use_id": "call_1", "content": "ok"},
				}},
				map[string]interface{}{"role": "assistant", "content": []interface{}{
					map[string]interface{}{"type": "text", "text": "步骤2 STEP2"},
					map[string]interface{}{"type": "tool_use", "id": "call_2", "name": "Bash", "input": map[string]interface{}{}},
				}},
				map[string]interface{}{"role": "user", "content": []interface{}{
					map[string]interface{}{"type": "tool_result", "tool_use_id": "call_2", "content": "ok"},
				}},
			},
		},
	}
	texts := aiGatewayCommittedAssistantTexts(current)
	if !texts["中间文本 MID"] || !texts["步骤2 STEP2"] {
		t.Fatalf("tool-ending turn: both committed texts must be in the set (old code returned empty): %v", texts)
	}
	if texts["历史答案 PAST"] {
		t.Fatalf("a previous turn's answer must NOT leak into the current-turn set: %v", texts)
	}
	think := aiGatewayCommittedAssistantThinking(current)
	if !think["THINK-A"] {
		t.Fatalf("current-turn committed thinking must be collected for live-tail exclusion: %v", think)
	}
}

// The dedup helper drops BOTH codex shadow forms — the generic
// "custom_tool_call"-named copy AND the empty-named function_call shadow — when a
// real-named sibling has the same input, and never a real-named tool. It must
// also keep two distinct real calls that happen to share input (e.g. two `pwd`s),
// dropping only their nameless shadows.
func TestDedupShadowToolCallItemsLeavesRealToolsAlone(t *testing.T) {
	// Real codex exec_command inputs are MAPS keyed by "cmd" (plus login/workdir/
	// …), NOT strings — aiGatewayFlattenPromptValue returns "" for those (no
	// text/content/input/output/thinking key), so a flatten-based dedup silently
	// failed in production while a string-input test passed. Use map inputs here.
	lsInput := map[string]interface{}{"cmd": "ls", "login": true, "workdir": "/w"}
	pwdInput := map[string]interface{}{"cmd": "pwd", "login": true, "workdir": "/w"}
	items := []map[string]interface{}{
		// apply_patch (real) + its custom_tool_call shadow (same input)
		{"type": "tool_use", "name": "apply_patch", "tool_id": "call_1", "input": "PATCH"},
		{"type": "tool_use", "name": "custom_tool_call", "tool_id": "ctc_1", "input": "PATCH"},
		// exec_command (real) + its EMPTY-named function_call shadow (same MAP input)
		{"type": "tool_use", "name": "exec_command", "tool_id": "call_2", "input": lsInput},
		{"type": "tool_use", "name": "", "tool_id": "fc_2", "input": lsInput},
		// two distinct real exec_command with identical input → both kept, both
		// shadows dropped
		{"type": "tool_use", "name": "exec_command", "tool_id": "call_3", "input": pwdInput},
		{"type": "tool_use", "name": "", "tool_id": "fc_3", "input": pwdInput},
		{"type": "tool_use", "name": "exec_command", "tool_id": "call_4", "input": pwdInput},
		{"type": "tool_use", "name": "", "tool_id": "fc_4", "input": pwdInput},
		{"type": "text", "text": "done"},
	}
	out := aiGatewayDedupShadowToolCallItems(items)
	names := []string{}
	for _, it := range out {
		if aiGatewayString(it["type"]) == "tool_use" {
			names = append(names, aiGatewayString(it["name"]))
		}
	}
	got := strings.Join(names, ",")
	if got != "apply_patch,exec_command,exec_command,exec_command" {
		t.Fatalf("dedup should drop all nameless/generic shadows and keep every real call, got tools: %s", got)
	}
}

// The real-world double-record: through MITM the codex response loses its
// `text/event-stream` Content-Type, so the live SSE parse is skipped and
// completeFromResponse re-parses the whole buffered body. That parse records the
// one custom_tool_call TWICE — once from custom_tool_call_input.done (keyed by
// item id, generic "custom_tool_call" name) and once from output_item.done
// (keyed by call id, real "apply_patch" name). reply.json must still carry the
// patch ONCE and never a "custom_tool_call"-named copy.
func TestCodexNonGatewayDedupsBufferedApplyPatch(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-19104"
	base, _ := url.Parse("https://chatgpt.com")
	patchItemJSON, _ := json.Marshal(codexPatch)
	patchJSON, _ := json.Marshal(codexPatch + "\n")
	sse := sseData(
		`{"type":"response.output_text.delta","delta":"我先改端口。","item_id":"msg_1","output_index":0}`,
		`{"type":"response.output_item.added","item":{"id":"ctc_1","type":"custom_tool_call","call_id":"call_1","input":"","name":"apply_patch"},"output_index":1}`,
		`{"type":"response.custom_tool_call_input.delta","delta":"*** Begin Patch\n","item_id":"ctc_1","output_index":1}`,
		`{"type":"response.custom_tool_call_input.done","input":`+string(patchJSON)+`,"item_id":"ctc_1","output_index":1}`,
		`{"type":"response.output_item.done","item":{"id":"ctc_1","type":"custom_tool_call","call_id":"call_1","input":`+string(patchItemJSON)+`,"name":"apply_patch","status":"completed"},"output_index":1}`,
		`{"type":"response.output_text.delta","delta":"已把端口改成 8090。","item_id":"msg_2","output_index":2}`,
		`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}`,
	)

	hdr := http.Header{"Content-Type": []string{"application/json"}}
	s := newAIGatewayAuditSession("openai", agent, base, "/backend-api/codex/responses", "POST", hdr,
		[]byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":"改端口"}]}`))
	if err := s.writeStartSnapshots(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// MITM-style: NO text/event-stream header → live parse skipped → buffered
	// re-parse in completeFromResponse (where the dup is produced).
	rc := newAIGatewayAuditReadCloser(io.NopCloser(bytes.NewReader([]byte(sse))), s, 200, http.Header{"Content-Type": []string{"application/json"}}, int64(len(sse)))
	if _, err := io.ReadAll(rc); err != nil {
		t.Fatalf("stream read: %v", err)
	}
	_ = rc.Close()

	data, _ := os.ReadFile(aiGatewayReplySnapshotPath(agent))
	var lite struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(data, &lite); err != nil {
		t.Fatalf("parse reply.json: %v", err)
	}
	patch, generic := 0, 0
	for _, it := range toolUseItems(lite.Items) {
		switch aiGatewayString(it["name"]) {
		case "apply_patch":
			patch++
		case "custom_tool_call":
			generic++
		}
	}
	if generic != 0 {
		t.Fatalf("buffered MITM parse left a 'custom_tool_call' duplicate: %s", data)
	}
	if patch != 1 {
		t.Fatalf("apply_patch must appear exactly once after dedup, got %d: %s", patch, data)
	}
}

// ---------------------------------------------------------------------------
// Full codex agentic-turn alignment: a real multi-step codex turn spans several
// HTTP requests (one tool call per request, then a final text-only request),
// each continuing the prior via function_call_output. These two tests drive the
// SAME event stream through BOTH transports — the ONLY production difference
// between gateway and non-gateway codex is whether the upstream's
// `text/event-stream` Content-Type survives:
//
//   - gateway      → header kept     → LIVE SSE parse merges fc_/call_ → no shadow
//   - non-gateway  → MITM strips it  → BUFFERED re-parse double-records → shadow,
//                                       which aiGatewayDedupShadowToolCallItems
//                                       must collapse.
//
// Both must end with IDENTICAL reply.json: (1) items strictly ordered
// text→tool→text→tool→text, (2) token usage captured & accumulated correctly,
// (3) each exec_command present exactly once with its real name and round-tripped
// cmd input (no nameless/generic shadow).

type codexTurnResult struct {
	Items                    []map[string]interface{} `json:"items"`
	Model                    string                   `json:"model"`
	InputTokens              int                      `json:"input_tokens"`
	OutputTokens             int                      `json:"output_tokens"`
	CacheReadInputTokens     int                      `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int                      `json:"cache_creation_input_tokens"`
	TotalTokens              int                      `json:"total_tokens"`
}

type codexStep struct{ reqBody, sseBody string }

// runCodexAgenticTurn streams each step through the real audit path against the
// same agent (so continuation/inheritance runs exactly as in production) and
// returns the final persisted reply.json. respCT selects live vs buffered.
func runCodexAgenticTurn(t *testing.T, provider, agent string, base *url.URL, suffix, respCT string, steps []codexStep) codexTurnResult {
	t.Helper()
	for i, st := range steps {
		hdr := http.Header{"Content-Type": []string{"application/json"}}
		s := newAIGatewayAuditSession(provider, agent, base, suffix, "POST", hdr, []byte(st.reqBody))
		if err := s.writeStartSnapshots(); err != nil {
			t.Fatalf("step %d writeStartSnapshots: %v", i, err)
		}
		rc := newAIGatewayAuditReadCloser(io.NopCloser(bytes.NewReader([]byte(st.sseBody))), s, 200,
			http.Header{"Content-Type": []string{respCT}}, int64(len(st.sseBody)))
		if _, err := io.ReadAll(rc); err != nil {
			t.Fatalf("step %d stream read: %v", i, err)
		}
		_ = rc.Close()
	}
	data, err := os.ReadFile(aiGatewayReplySnapshotPath(agent))
	if err != nil {
		t.Fatalf("read reply.json: %v", err)
	}
	var res codexTurnResult
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("parse reply.json: %v\n%s", err, data)
	}
	return res
}

func evJSON(m map[string]interface{}) string { b, _ := json.Marshal(m); return string(b) }

// codexUsage builds an OpenAI-Responses usage object. input_tokens INCLUDES the
// cached tokens (OpenAI convention); the audit pipeline normalizes it to
// uncached-only + a separate cache_read_input_tokens (Anthropic shape).
func codexUsage(input, cached, output int) map[string]interface{} {
	return map[string]interface{}{
		"input_tokens":         input,
		"input_tokens_details": map[string]interface{}{"cached_tokens": cached},
		"output_tokens":        output,
		"total_tokens":         input + output,
	}
}

// codexExecStepSSE emits one exec_command step: a leading text, then the tool
// keyed BOTH by item id (fc_) via function_call_arguments.done (the nameless
// shadow) and by call id (call_) via output_item.done (the real name) — exactly
// how codex Responses reports a single call.
func codexExecStepSSE(i int, text, cmd string, usage map[string]interface{}) string {
	fc := fmt.Sprintf("fc_%d", i)
	call := fmt.Sprintf("call_%d", i)
	argsBytes, _ := json.Marshal(map[string]interface{}{"cmd": cmd, "login": true, "workdir": "/w"})
	args := string(argsBytes) // arguments is a JSON-encoded string, as codex sends it
	return sseData(
		evJSON(map[string]interface{}{"type": "response.output_text.delta", "delta": text, "item_id": fmt.Sprintf("msg_%d", i), "output_index": 0}),
		evJSON(map[string]interface{}{"type": "response.output_item.added", "item": map[string]interface{}{"id": fc, "type": "function_call", "call_id": call, "name": "exec_command", "arguments": ""}, "output_index": 1}),
		evJSON(map[string]interface{}{"type": "response.function_call_arguments.done", "arguments": args, "item_id": fc, "output_index": 1}),
		evJSON(map[string]interface{}{"type": "response.output_item.done", "item": map[string]interface{}{"id": fc, "type": "function_call", "call_id": call, "name": "exec_command", "arguments": args}, "output_index": 1}),
		evJSON(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"usage": usage}}),
	)
}

// codexTextStepSSE emits a final text-only request (the closing summary codex
// sends after the last tool result returns).
func codexTextStepSSE(i int, text string, usage map[string]interface{}) string {
	return sseData(
		evJSON(map[string]interface{}{"type": "response.output_text.delta", "delta": text, "item_id": fmt.Sprintf("msg_%d", i), "output_index": 0}),
		evJSON(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"usage": usage}}),
	)
}

func codexReqInit(model string) string {
	b, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"input": []interface{}{map[string]interface{}{"type": "message", "role": "user", "content": "分两步执行"}},
	})
	return string(b)
}

// codexReqCont ends with a function_call_output so aiGatewayIsToolContinuation
// recognizes it as a tool continuation (inherit prior items + accumulate tokens).
func codexReqCont(model string, lastCall int) string {
	b, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"input": []interface{}{
			map[string]interface{}{"type": "message", "role": "user", "content": "分两步执行"},
			map[string]interface{}{"type": "function_call_output", "call_id": fmt.Sprintf("call_%d", lastCall), "output": "ok"},
		},
	})
	return string(b)
}

func typeSeq(items []map[string]interface{}) []string {
	seq := make([]string, 0, len(items))
	for _, it := range items {
		seq = append(seq, aiGatewayString(it["type"]))
	}
	return seq
}

func toolCmds(items []map[string]interface{}) []string {
	cmds := []string{}
	for _, it := range toolUseItems(items) {
		if m := aiGatewayMap(it["input"]); len(m) > 0 {
			cmds = append(cmds, aiGatewayString(m["cmd"]))
		} else {
			cmds = append(cmds, "")
		}
	}
	return cmds
}

// codexExecTurnSteps: text→exec(echo a)→text→exec(echo b)→text across 3 requests.
//
// Token semantics: reply.json's input_tokens is the FULL prompt size (cached +
// uncached); cache_read_input_tokens is the cached portion of it. OpenAI sends
// input_tokens already INCLUDING the cache, with the hit in
// input_tokens_details.cached_tokens — the pipeline normalizes that to
// uncached-only internally but reports input_tokens back as the full total
// (uncached + cache_read). input/cache use the LATEST request; output accumulates.
//
//	req1 usage 100/cache60/out10 → input 100, cache 60, out 10
//	req2 usage 120/cache90/out15 → input 120, cache 90, out 15  (output sums: 25)
//	req3 usage 130/cache95/out5  → input 130, cache 95, out 5   (output sums: 30)
//
// Final reply.json: input=130 (latest, full), cache=95 (latest), output=30
// (summed across requests), total=input+output=160.
func codexExecTurnSteps(model string) []codexStep {
	return []codexStep{
		{codexReqInit(model), codexExecStepSSE(1, "先执行第一步。", "echo a > f1", codexUsage(100, 60, 10))},
		{codexReqCont(model, 1), codexExecStepSSE(2, "第一步好了，执行第二步。", "echo b > f2", codexUsage(120, 90, 15))},
		{codexReqCont(model, 2), codexTextStepSSE(3, "两步完成。", codexUsage(130, 95, 5))},
	}
}

func assertExecTurnAligned(t *testing.T, res codexTurnResult, wantModel string) {
	t.Helper()
	// (1) reply items in order: text, tool, text, tool, text.
	want := []string{"text", "tool_use", "text", "tool_use", "text"}
	if got := typeSeq(res.Items); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("item order mismatch:\n got  %v\n want %v\nitems: %s", got, want, mustJSON(res.Items))
	}
	// (3) tools: each exec_command once, real name, no shadow, cmds round-tripped & ordered.
	for _, it := range toolUseItems(res.Items) {
		switch name := aiGatewayString(it["name"]); name {
		case "":
			t.Fatalf("nameless shadow tool_use survived dedup: %s", mustJSON(res.Items))
		case "custom_tool_call":
			t.Fatalf("generic 'custom_tool_call' shadow survived dedup: %s", mustJSON(res.Items))
		case "exec_command":
		default:
			t.Fatalf("unexpected tool name %q", name)
		}
	}
	if got := strings.Join(toolCmds(res.Items), "|"); got != "echo a > f1|echo b > f2" {
		t.Fatalf("tool cmd inputs wrong or out of order: %q\nitems: %s", got, mustJSON(res.Items))
	}
	// (2) token usage captured and accumulated correctly across the requests.
	// input_tokens = full latest prompt (uncached 35 + cache 95 = 130); cache_read
	// = the cached part; output accumulates across all requests; total = in + out.
	if res.InputTokens != 130 {
		t.Fatalf("input_tokens=%d, want 130 (latest request's full input incl. cache)", res.InputTokens)
	}
	if res.CacheReadInputTokens != 95 {
		t.Fatalf("cache_read_input_tokens=%d, want 95 (latest request's cached part)", res.CacheReadInputTokens)
	}
	if res.OutputTokens != 30 {
		t.Fatalf("output_tokens=%d, want 30 (10+15+5 accumulated across requests)", res.OutputTokens)
	}
	if res.TotalTokens != 160 {
		t.Fatalf("total_tokens=%d, want 160 (input 130 + output 30)", res.TotalTokens)
	}
	// model round-trips into reply.json (was lost before lite snapshot gained Model).
	if res.Model != wantModel {
		t.Fatalf("model=%q, want %q", res.Model, wantModel)
	}
}

// NON-GATEWAY codex over MITM: text/event-stream is stripped, so completeFromResponse
// re-parses the buffered body and double-records each exec_command (real call_ +
// nameless fc_ shadow). reply.json must still align after shadow dedup.
func TestCodexNonGatewayExecCommandTurnAligns(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	base, _ := url.Parse("https://chatgpt.com")
	res := runCodexAgenticTurn(t, "openai", "w-19105", base, "/backend-api/codex/responses",
		"application/json", codexExecTurnSteps("gpt-5.5"))
	assertExecTurnAligned(t, res, "gpt-5.5")
}

// GATEWAY codex: the upstream keeps text/event-stream, so the live SSE parse
// correlates fc_/call_ into a single tool item — no shadow is ever produced. The
// same turn must yield byte-for-byte the same aligned reply.json as MITM.
func TestCodexGatewayExecCommandTurnAligns(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	base, _ := url.Parse("https://gateway.cicy-ai.com")
	res := runCodexAgenticTurn(t, "openai", "w-19106", base, "/v1/chat/completions",
		"text/event-stream", codexExecTurnSteps("deepseek-v4-pro"))
	assertExecTurnAligned(t, res, "deepseek-v4-pro")
}
