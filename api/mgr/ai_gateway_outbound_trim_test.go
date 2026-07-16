package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func outboundRounds(sizes ...int) []aiGatewayOutboundRequestSnap {
	out := make([]aiGatewayOutboundRequestSnap, 0, len(sizes))
	for i, n := range sizes {
		out = append(out, aiGatewayOutboundRequestSnap{Seq: i + 1, bytes: n})
	}
	return out
}

func retainedSeqs(reqs []aiGatewayOutboundRequestSnap) []int {
	out := make([]int, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.Seq)
	}
	return out
}

func retainedBytes(reqs []aiGatewayOutboundRequestSnap) int {
	total := 0
	for _, r := range reqs {
		total += r.bytes
	}
	return total
}

// The bug this guards: a body is the WHOLE conversation sent that round, so on a
// large context one body is tens of MB. Bounding by round COUNT alone let a single
// agent retain 587 MB — in memory, and rewritten to disk in full on every request.
func TestOutboundTrimBoundsByBytesNotJustRounds(t *testing.T) {
	big := 20 << 20 // 20 MB per round — 10 of these would be 200 MB
	reqs := outboundRounds(big, big, big, big, big)
	got := aiGatewayOutboundTrim(reqs)

	if n := retainedBytes(got); n > aiGatewayOutboundMaxBytes {
		t.Fatalf("retained %d bytes, budget is %d — the byte cap did not bind (a "+
			"count-only cap is what let one agent hold 587 MB)", n, aiGatewayOutboundMaxBytes)
	}
	if len(got) >= len(reqs) {
		t.Fatalf("nothing was trimmed: kept %d of %d rounds", len(got), len(reqs))
	}
	// Trimming is oldest-first: the newest round must be the last one retained.
	if got[len(got)-1].Seq != reqs[len(reqs)-1].Seq {
		t.Fatalf("newest round (seq %d) was dropped; retained %v",
			reqs[len(reqs)-1].Seq, retainedSeqs(got))
	}
}

// A body larger than the whole budget must still be kept — a truncated or empty
// outbound.json is worse than a large one. The point is that it costs 1× its size,
// never 10×.
func TestOutboundTrimAlwaysKeepsTheNewestRound(t *testing.T) {
	huge := aiGatewayOutboundMaxBytes * 3
	got := aiGatewayOutboundTrim(outboundRounds(1024, 1024, huge))
	if len(got) != 1 || got[0].bytes != huge {
		t.Fatalf("retained %v (%d bytes) — the newest round must survive whatever its size",
			retainedSeqs(got), retainedBytes(got))
	}
}

// Small conversations must be untouched: the byte cap is a backstop for giant
// contexts, not a behaviour change for everyone.
func TestOutboundTrimLeavesSmallConversationsAlone(t *testing.T) {
	reqs := outboundRounds(1024, 2048, 4096)
	got := aiGatewayOutboundTrim(reqs)
	if len(got) != 3 {
		t.Fatalf("kept %d of 3 small rounds — the cap must not bind here", len(got))
	}
}

func TestOutboundTrimStillHonoursTheRoundCap(t *testing.T) {
	sizes := make([]int, aiGatewayOutboundMaxRounds+5)
	for i := range sizes {
		sizes[i] = 1024
	}
	got := aiGatewayOutboundTrim(outboundRounds(sizes...))
	if len(got) != aiGatewayOutboundMaxRounds {
		t.Fatalf("kept %d rounds, want %d", len(got), aiGatewayOutboundMaxRounds)
	}
	if got[0].Seq != 6 {
		t.Fatalf("kept from seq %d — trimming must drop the OLDEST", got[0].Seq)
	}
}

// outbound.json is rewritten in full on every gateway round-trip. Pretty-printing
// it is pure CPU and allocation on the hot path, and it is machine-read only.
func TestOutboundIsWrittenCompact(t *testing.T) {
	withTempCicyRoot(t)
	dropOutboundSnapshot("w-9101")

	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	aiGatewayAppendOutbound("w-9101", "conv-a", "t1", "r1", body, time.Now())

	raw, err := os.ReadFile(aiGatewayOutboundSnapshotPath("w-9101"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\n  \"") {
		t.Fatal("outbound.json is pretty-printed — indenting a file that is rewritten " +
			"in full on every request is wasted CPU on the hot path")
	}
	// Still valid JSON, and the body is on disk BYTE-IDENTICAL to what went out —
	// that verbatim wire copy is the entire point of the file. Decode into
	// RawMessage: decoding into interface{} would reorder keys and prove nothing.
	var snap struct {
		Requests []struct {
			Body json.RawMessage `json:"body"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("outbound.json is not valid JSON: %v", err)
	}
	if len(snap.Requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(snap.Requests))
	}
	if string(snap.Requests[0].Body) != string(body) {
		t.Fatalf("body is not byte-identical to the wire request:\n got %s\nwant %s",
			snap.Requests[0].Body, body)
	}
}

func TestDropOutboundSnapshotReleasesTheAgent(t *testing.T) {
	withTempCicyRoot(t)
	aiGatewayAppendOutbound("w-9102", "conv-a", "t1", "r1", []byte(`{"a":1}`), time.Now())

	aiGatewayOutboundSnapshots.mu.Lock()
	_, before := aiGatewayOutboundSnapshots.items["w-9102"]
	aiGatewayOutboundSnapshots.mu.Unlock()
	if !before {
		t.Fatal("test setup: nothing was retained")
	}

	dropOutboundSnapshot("w-9102")

	aiGatewayOutboundSnapshots.mu.Lock()
	_, after := aiGatewayOutboundSnapshots.items["w-9102"]
	aiGatewayOutboundSnapshots.mu.Unlock()
	if after {
		t.Fatal("deleted pane still pins its outbound bodies — the map is never otherwise pruned")
	}
}
