package audit

import (
	"encoding/json"
	"testing"
)

func msgCount(t *testing.T, payload []byte) int {
	t.Helper()
	if payload == nil {
		return 0
	}
	var req struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal delta: %v", err)
	}
	return len(req.Messages)
}

func TestIncrementalOutbound_OnlyNewMessages(t *testing.T) {
	agent := "w-test-inc"
	// The seen-set is process-global; isolate this test from prior runs (e.g.
	// `go test -count=N`) so the baseline/new-message accounting is deterministic.
	outboundSeen.mu.Lock()
	delete(outboundSeen.perAgt, agent)
	outboundSeen.mu.Unlock()
	turn1 := []byte(`{"model":"m","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}]}`)
	// First encounter: existing history is baselined as known, NOT scanned
	// (never rescan accumulated history, incl. across restarts).
	if d1 := IncrementalOutboundPayload(agent, turn1); d1 != nil {
		t.Fatalf("turn1 (baseline) want nil, got %q", string(d1))
	}

	// Second turn: same history + one NEW message → only the new one scanned.
	turn2 := []byte(`{"model":"m","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"my key is sk-abc"}]}`)
	d2 := IncrementalOutboundPayload(agent, turn2)
	if got := msgCount(t, d2); got != 1 {
		t.Fatalf("turn2 want 1 new msg, got %d", got)
	}

	// Third turn: nothing new → nil (skip scan, no rescan flood).
	d3 := IncrementalOutboundPayload(agent, turn2)
	if d3 != nil {
		t.Fatalf("turn3 want nil (nothing new), got %q", string(d3))
	}
}

func TestIncrementalOutbound_FailOpenOnUnparseable(t *testing.T) {
	body := []byte(`not json`)
	if got := IncrementalOutboundPayload("w-x", body); string(got) != string(body) {
		t.Fatalf("unparseable body should pass through unchanged, got %q", string(got))
	}
}
