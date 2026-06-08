package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: spin up an isolated pipeline + submit n events synchronously,
// returning the per-agent audit.ndjson + state paths.
func setupVerifyFixture(t *testing.T, n int) (ndjsonPath, statePath string) {
	t.Helper()
	root := t.TempDir()
	auditRoot := filepath.Join(root, "audit")
	workersRoot := filepath.Join(root, "workers")
	policy, _ := LoadPolicy("")
	p, err := NewPipeline(auditRoot, workersRoot, NoopScanner{}, policy)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	for i := 0; i < n; i++ {
		p.Submit(context.Background(), Envelope{
			AgentID:       "w-1001",
			AgentType:     "claude",
			SourceChannel: SourceGateway,
			TurnID:        "turn",
			Direction:     DirectionOutbound,
			Payload:       []byte("payload"),
			PayloadRef:    "current.json#turn",
			Inline:        true,
		})
	}
	p.Wait()
	return filepath.Join(workersRoot, "w-1001", ".cicy", "history", "audit.ndjson"),
		filepath.Join(workersRoot, "w-1001", ".cicy", "history", "audit-chain.state")
}

func TestVerifyFile_CleanChain(t *testing.T) {
	ndjson, state := setupVerifyFixture(t, 3)
	report, err := VerifyFile(ndjson, state)
	if err != nil {
		t.Fatalf("VerifyFile: %v", err)
	}
	if !report.OK() {
		t.Errorf("expected clean chain, got errors: %+v", report.Errors)
	}
	if report.EventCount != 3 {
		t.Errorf("EventCount = %d, want 3", report.EventCount)
	}
}

func TestVerifyFile_TamperedEventDetected(t *testing.T) {
	ndjson, state := setupVerifyFixture(t, 2)

	// Mutate event #1: change a non-hash field. self_hash on disk will no
	// longer match the canonical hash of the mutated content.
	data, err := os.ReadFile(ndjson)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	var e Event
	if err := json.Unmarshal(lines[0], &e); err != nil {
		t.Fatal(err)
	}
	e.Subject.PayloadRef = "current.json#TAMPERED"
	tampered, _ := json.Marshal(e)
	lines[0] = tampered
	if err := os.WriteFile(ndjson, append(bytes.Join(lines, []byte{'\n'}), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyFile(ndjson, state)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK() {
		t.Fatal("expected hash_mismatch error, got clean report")
	}
	found := false
	for _, e := range report.Errors {
		if e.Kind == VerifyErrHashMismatch {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one hash_mismatch error, got: %+v", report.Errors)
	}
}

func TestVerifyFile_BrokenChainLink(t *testing.T) {
	ndjson, state := setupVerifyFixture(t, 3)

	// Swap event #2's prev_hash but keep its self_hash recorded: the chain
	// linkage breaks even though the line's own hash would re-verify (we'll
	// recompute it against the mutated content).
	data, err := os.ReadFile(ndjson)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte{'\n'})
	var e Event
	if err := json.Unmarshal(lines[1], &e); err != nil {
		t.Fatal(err)
	}
	e.PrevHash = "sha256:NOT_THE_REAL_PREV"
	// Recompute self_hash so it matches the new content — this isolates the
	// chain_break error from the hash_mismatch one.
	newHash, _ := ComputeSelfHash(e)
	e.SelfHash = newHash
	tampered, _ := json.Marshal(e)
	lines[1] = tampered
	if err := os.WriteFile(ndjson, append(bytes.Join(lines, []byte{'\n'}), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyFile(ndjson, state)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK() {
		t.Fatal("expected chain_break error, got clean report")
	}
	found := false
	for _, e := range report.Errors {
		if e.Kind == VerifyErrChainBreak {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected chain_break error, got: %+v", report.Errors)
	}
}

func TestVerifyFile_StateMismatch(t *testing.T) {
	ndjson, state := setupVerifyFixture(t, 2)

	// Corrupt the state file (lie about count).
	var s ChainState
	data, _ := os.ReadFile(state)
	_ = json.Unmarshal(data, &s)
	s.Count = 999
	corrupt, _ := json.Marshal(s)
	if err := os.WriteFile(state, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyFile(ndjson, state)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK() {
		t.Fatal("expected state_mismatch error, got clean report")
	}
	found := false
	for _, e := range report.Errors {
		if e.Kind == VerifyErrStateMismatch && strings.Contains(e.Detail, "count") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected state_mismatch about count, got: %+v", report.Errors)
	}
}

func TestVerifyAll_EmptyDirs(t *testing.T) {
	root := t.TempDir()
	auditRoot := filepath.Join(root, "audit")
	workersRoot := filepath.Join(root, "workers")
	_ = os.MkdirAll(filepath.Join(auditRoot, "index"), 0o700)
	_ = os.MkdirAll(workersRoot, 0o700)
	reports, err := VerifyAll(auditRoot, workersRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 0 {
		t.Errorf("expected 0 reports on empty dirs, got %d", len(reports))
	}
}

func TestRunCLI_ExitCodes(t *testing.T) {
	// No args -> 2
	if code := RunCLI(nil); code != 2 {
		t.Errorf("RunCLI(nil) = %d, want 2", code)
	}
	// Unknown subcmd -> 2
	if code := RunCLI([]string{"bogus"}); code != 2 {
		t.Errorf("RunCLI(bogus) = %d, want 2", code)
	}
	// Help -> 0
	if code := RunCLI([]string{"help"}); code != 0 {
		t.Errorf("RunCLI(help) = %d, want 0", code)
	}
	// verify of nonexistent file -> 2 (IO error)
	if code := RunCLI([]string{"verify", filepath.Join(t.TempDir(), "no-such.ndjson")}); code != 2 {
		t.Errorf("verify of missing file = %d, want 2", code)
	}
}
