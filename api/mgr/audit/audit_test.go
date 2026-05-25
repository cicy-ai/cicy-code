package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPipelineWalkingSkeleton verifies the end-to-end audit pipeline in
// isolation (no global singleton, no real gateway): submit one outbound
// envelope plus one inbound envelope, then prove that:
//   - per-agent audit.ndjson appears with two lines
//   - both events have well-formed identity / subject / payload_sha256
//   - the second event's prev_hash equals the first event's self_hash
//   - global index file mirrors the same events on its own chain
//   - chain state files match the on-file tail
func TestPipelineWalkingSkeleton(t *testing.T) {
	root := t.TempDir()
	auditRoot := filepath.Join(root, "audit")
	workersRoot := filepath.Join(root, "workers")

	policy, err := LoadPolicy(filepath.Join(auditRoot, "policy.json"))
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	p, err := NewPipeline(auditRoot, workersRoot, NoopScanner{}, policy)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	outboundPayload := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	inboundPayload := []byte(`{"items":[{"type":"text","text":"hello"}]}`)

	// Use Inline=true for deterministic file order in the test. Production
	// uses Inline=false (fire-and-forget); ts is captured at Submit, so
	// async submits still carry correct timestamps even if file order races.
	p.Submit(context.Background(), Envelope{
		AgentID:        "w-10001",
		AgentType:      "claude",
		UserID:         "u-test",
		SessionID:      "sess-test",
		SourceChannel:  SourceGateway,
		TurnID:         "turn_test_001",
		ConversationID: "conv_test_001",
		Provider:       "anthropic",
		Model:          "claude-opus-4-7",
		Direction:      DirectionOutbound,
		Payload:        outboundPayload,
		PayloadRef:     "current.json#turn_test_001",
		Inline:         true,
	})
	p.Submit(context.Background(), Envelope{
		AgentID:        "w-10001",
		AgentType:      "claude",
		UserID:         "u-test",
		SessionID:      "sess-test",
		SourceChannel:  SourceGateway,
		TurnID:         "turn_test_001",
		ConversationID: "conv_test_001",
		Provider:       "anthropic",
		Model:          "claude-opus-4-7",
		Direction:      DirectionInbound,
		Payload:        inboundPayload,
		PayloadRef:     "reply.json#turn_test_001",
		Inline:         true,
	})
	p.Wait()

	// Per-agent file
	agentPath := filepath.Join(workersRoot, "w-10001", ".cicy", "history", "audit.ndjson")
	lines := readNDJSON(t, agentPath)
	if len(lines) != 2 {
		t.Fatalf("agent ndjson: want 2 lines, got %d", len(lines))
	}

	outbound := parseEvent(t, lines[0])
	inbound := parseEvent(t, lines[1])

	// Required fields
	if outbound.Identity.AgentID != "w-10001" || outbound.Identity.SourceChannel != SourceGateway {
		t.Errorf("outbound identity wrong: %+v", outbound.Identity)
	}
	if outbound.Subject.Direction != DirectionOutbound || inbound.Subject.Direction != DirectionInbound {
		t.Errorf("directions wrong: outbound=%s inbound=%s", outbound.Subject.Direction, inbound.Subject.Direction)
	}
	if outbound.SchemaVersion != SchemaVersion || outbound.RulesVersion != RulesVersion {
		t.Errorf("schema/rules version: %s / %s", outbound.SchemaVersion, outbound.RulesVersion)
	}
	if outbound.Subject.PayloadSize != len(outboundPayload) {
		t.Errorf("outbound payload_size: want %d got %d", len(outboundPayload), outbound.Subject.PayloadSize)
	}
	if got := computeSHA256(outboundPayload); outbound.Subject.PayloadSHA256 != got {
		t.Errorf("outbound payload_sha256 mismatch: want %s got %s", got, outbound.Subject.PayloadSHA256)
	}

	// Chain: outbound's prev_hash is genesis; inbound's prev_hash equals outbound's self_hash.
	if outbound.PrevHash != ChainGenesis {
		t.Errorf("outbound prev_hash: want %s got %s", ChainGenesis, outbound.PrevHash)
	}
	if !strings.HasPrefix(outbound.SelfHash, "sha256:") {
		t.Errorf("outbound self_hash format wrong: %s", outbound.SelfHash)
	}
	if inbound.PrevHash != outbound.SelfHash {
		t.Errorf("chain break: inbound.prev_hash=%s outbound.self_hash=%s", inbound.PrevHash, outbound.SelfHash)
	}

	// Chain state file matches the tail.
	stateBytes, err := os.ReadFile(filepath.Join(workersRoot, "w-10001", ".cicy", "history", "audit-chain.state"))
	if err != nil {
		t.Fatalf("read agent chain state: %v", err)
	}
	var state ChainState
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatalf("parse chain state: %v", err)
	}
	if state.LastHash != inbound.SelfHash {
		t.Errorf("agent chain state last_hash %s != inbound.self_hash %s", state.LastHash, inbound.SelfHash)
	}
	if state.Count != 2 {
		t.Errorf("agent chain state count = %d, want 2", state.Count)
	}

	// Recompute the canonical hash for outbound and confirm it matches self_hash.
	recomputed, err := ComputeSelfHash(outbound)
	if err != nil {
		t.Fatalf("ComputeSelfHash: %v", err)
	}
	if recomputed != outbound.SelfHash {
		t.Errorf("canonical hash drift: want %s got %s", outbound.SelfHash, recomputed)
	}

	// Global index file: today's NDJSON exists and has 2 lines.
	indexDir := filepath.Join(auditRoot, "index")
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		t.Fatalf("read index dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("index dir: want 1 file, got %d", len(entries))
	}
	indexLines := readNDJSON(t, filepath.Join(indexDir, entries[0].Name()))
	if len(indexLines) != 2 {
		t.Fatalf("index ndjson: want 2 lines, got %d", len(indexLines))
	}

	// Index chain links its own line 2 to line 1 (independent of agent chain).
	idxOut := parseEvent(t, indexLines[0])
	idxIn := parseEvent(t, indexLines[1])
	if idxOut.PrevHash != ChainGenesis {
		t.Errorf("index outbound prev_hash: want %s got %s", ChainGenesis, idxOut.PrevHash)
	}
	if idxIn.PrevHash != idxOut.SelfHash {
		t.Errorf("index chain break: idxIn.prev=%s idxOut.self=%s", idxIn.PrevHash, idxOut.SelfHash)
	}

	// machine_id file persists with consistent value
	if outbound.Identity.MachineID == "" || !strings.HasPrefix(outbound.Identity.MachineID, "host_") {
		t.Errorf("machine_id format: %s", outbound.Identity.MachineID)
	}
	if inbound.Identity.MachineID != outbound.Identity.MachineID {
		t.Errorf("machine_id should be stable across events: out=%s in=%s",
			outbound.Identity.MachineID, inbound.Identity.MachineID)
	}
}

// TestChainTamperDetected proves a manually mutated event no longer hashes
// to its recorded self_hash — the core integrity guarantee.
func TestChainTamperDetected(t *testing.T) {
	root := t.TempDir()
	policy, _ := LoadPolicy("")
	p, err := NewPipeline(filepath.Join(root, "audit"), filepath.Join(root, "workers"), NoopScanner{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	p.Submit(context.Background(), Envelope{
		AgentID:       "w-10001",
		SourceChannel: SourceGateway,
		TurnID:        "t1",
		Direction:     DirectionOutbound,
		Payload:       []byte("original"),
		PayloadRef:    "current.json#t1",
		Inline:        true,
	})
	p.Wait()

	lines := readNDJSON(t, filepath.Join(root, "workers", "w-10001", ".cicy", "history", "audit.ndjson"))
	original := parseEvent(t, lines[0])

	// Mutate one field as if an attacker edited the file
	tampered := original
	tampered.Subject.PayloadRef = "current.json#hacked"

	recomputed, err := ComputeSelfHash(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if recomputed == original.SelfHash {
		t.Errorf("tampering not detected: hash unchanged")
	}
}

// helpers

func readNDJSON(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	raw := strings.TrimRight(string(data), "\n")
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}

func parseEvent(t *testing.T, line string) Event {
	t.Helper()
	var e Event
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		t.Fatalf("parse event: %v\nline: %s", err, line)
	}
	return e
}

func computeSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
