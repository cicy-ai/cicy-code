package main

// Dead-stream guard (found 2026-07-09, w-10036 history_id 1503): a 2xx SSE that
// ends having delivered ZERO bytes/events — the client hung up during a long
// prefill, or the upstream stalled and the connection was torn down — must NOT
// be sealed status "completed" with empty items. That combination leaves a
// permanent phantom blank last round in reply.json which every client renders
// as an empty answer. It must land "failed" with an explanatory item instead.
// A stream that produced real content keeps sealing "completed".

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
)

type deadStreamReplyLite struct {
	Status string                   `json:"status"`
	Items  []map[string]interface{} `json:"items"`
}

// runRawStreamTurn drives the exact production path the proxy/MITM use
// (newAIGatewayAuditSession → newAIGatewayAuditReadCloser → completeFromResponse)
// with an arbitrary SSE body, then returns the persisted reply.json.
func runRawStreamTurn(t *testing.T, agent, sseBody string) deadStreamReplyLite {
	t.Helper()
	return runRawStreamTurnQ(t, agent, "hi", sseBody)
}

func runRawStreamTurnQ(t *testing.T, agent, question, sseBody string) deadStreamReplyLite {
	t.Helper()
	base, _ := url.Parse("https://chatgpt.com")
	q, _ := json.Marshal(question)
	reqBody := `{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":` + string(q) + `}]}`
	hdr := http.Header{"Content-Type": []string{"application/json"}}
	s := newAIGatewayAuditSession("openai", agent, base, "/backend-api/codex/responses", "POST", hdr, []byte(reqBody))
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
	var lite deadStreamReplyLite
	if err := json.Unmarshal(data, &lite); err != nil {
		t.Fatalf("parse reply.json: %v\n%s", err, data)
	}
	return lite
}

func TestDeadStreamSealsFailedNotCompleted(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	reply := runRawStreamTurn(t, "w-19201", "" /* zero bytes, zero events */)

	if reply.Status != "failed" {
		t.Fatalf("dead stream sealed status %q, want \"failed\"", reply.Status)
	}
	// The persisted lite snapshot carries no `answer` field by design — the UI
	// reads content off items. The guard must leave a visible explanation there.
	if len(reply.Items) == 0 {
		t.Fatalf("dead stream must carry an explanatory item, got none")
	}
	if text := aiGatewayString(reply.Items[0]["text"]); !bytes.Contains([]byte(text), []byte("生成中断")) {
		t.Fatalf("explanatory item should mention 生成中断, got %q", text)
	}
}

// Recap wakeups are auxiliary side questions, not conversation turns. Treated
// as mainline they reset reply.json (destroying the real last round) and then
// park themselves as the "current reply" (w-10122 #528) — or a dead-stream
// empty shell (w-10036 #1503).
func TestRecapClassifiedAuxiliary(t *testing.T) {
	q := "The user stepped away and is coming back. Recap in under 40 words, 1-2 plain sentences, no markdown."
	if got := aiGatewayAuxiliaryKind(q, nil); got != "recap" {
		t.Fatalf("recap question classified %q, want \"recap\"", got)
	}
	// A leading system-reminder block must not defeat the ^ anchor.
	wrapped := "<system-reminder>ctx</system-reminder>\n" + q
	if got := aiGatewayAuxiliaryKind(wrapped, nil); got != "recap" {
		t.Fatalf("reminder-wrapped recap classified %q, want \"recap\"", got)
	}
	if got := aiGatewayAuxiliaryKind("The user asked about recaps in general", nil); got != "" {
		t.Fatalf("ordinary question misclassified %q, want mainline", got)
	}
}

func TestRecapRoundLeavesReplyJSONUntouched(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-19203"

	// Round 1: a real turn seals its answer into reply.json.
	sse := sseData(
		`{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant","content":[]},"output_index":0}`,
		`{"type":"response.output_text.delta","delta":"真实回答。","item_id":"msg_1","output_index":0}`,
		`{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"真实回答。"}]},"output_index":0}`,
		`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}`,
	)
	first := runRawStreamTurn(t, agent, sse)
	if first.Status != "completed" || len(first.Items) == 0 {
		t.Fatalf("setup turn not sealed: %+v", first)
	}

	// Round 2: a recap side question — even one that dies as an empty stream —
	// must neither reset nor overwrite the real round's reply.json.
	after := runRawStreamTurnQ(t, agent,
		"The user stepped away and is coming back. Recap in under 40 words, 1-2 plain sentences, no markdown.",
		"" /* dead stream, worst case */)
	if after.Status != "completed" || len(after.Items) != len(first.Items) {
		t.Fatalf("recap round disturbed reply.json: before=%+v after=%+v", first, after)
	}
	if text := aiGatewayString(after.Items[0]["text"]); text != "真实回答。" {
		t.Fatalf("reply.json content replaced by recap round: %q", text)
	}
}

func TestContentStreamStillSealsCompleted(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	sse := sseData(
		`{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant","content":[]},"output_index":0}`,
		`{"type":"response.output_text.delta","delta":"好的。","item_id":"msg_1","output_index":0}`,
		`{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"好的。"}]},"output_index":0}`,
		`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}`,
	)
	reply := runRawStreamTurn(t, "w-19202", sse)

	if reply.Status != "completed" {
		t.Fatalf("content stream sealed status %q, want \"completed\"", reply.Status)
	}
}
