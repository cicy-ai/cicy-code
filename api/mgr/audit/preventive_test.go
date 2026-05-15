package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// preventiveFixture builds an isolated pipeline + ensures the global
// singleton points at it so package-level audit.PreventiveCheck works.
// Returns the pipeline, the workers root, and a teardown that restores
// the previous singleton.
func preventiveFixture(t *testing.T, policy *Policy) (*Pipeline, string) {
	t.Helper()
	root := t.TempDir()
	auditRoot := filepath.Join(root, "audit")
	workersRoot := filepath.Join(root, "workers")
	scanner := NewBuiltinScanner()
	p, err := NewPipeline(auditRoot, workersRoot, scanner, policy)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	prev := globalPipeline
	globalPipeline = p
	t.Cleanup(func() { globalPipeline = prev })
	return p, workersRoot
}

func TestPreventive_Disabled_PassThrough(t *testing.T) {
	pol := DefaultPolicy()
	pol.Preventive.Enabled = false
	p, _ := preventiveFixture(t, pol)

	dec := p.PreventiveCheck(Envelope{
		AgentID:       "w-x",
		SourceChannel: SourceGateway,
		Direction:     DirectionOutbound,
		Payload:       []byte("AKIAIOSFODNN7EXAMPLE"),
	})
	if dec.Action != ActionNone || dec.Reason != "preventive_disabled" {
		t.Errorf("expected none/preventive_disabled, got %+v", dec)
	}
	if dec.EventID != "" {
		t.Errorf("no event should be written when preventive disabled, got %s", dec.EventID)
	}
}

func TestPreventive_BlocksHighInlineRule(t *testing.T) {
	pol := DefaultPolicy()
	pol.Preventive.Enabled = true
	p, workersRoot := preventiveFixture(t, pol)

	dec := p.PreventiveCheck(Envelope{
		AgentID:       "w-x",
		SourceChannel: SourceGateway,
		Direction:     DirectionOutbound,
		Payload:       []byte("-----BEGIN RSA PRIVATE KEY-----"),
	})
	if dec.Action != ActionBlock {
		t.Fatalf("expected block, got %+v", dec)
	}
	if dec.EventID == "" {
		t.Errorf("expected non-empty event_id")
	}
	if len(dec.Findings) == 0 {
		t.Errorf("expected at least one finding")
	}

	// Confirm the preventive event was actually persisted with action=block,
	// applied=true, evaluated_inline=true.
	events := readEvents(t, filepath.Join(workersRoot, "w-x", ".cicy", "history", "audit.ndjson"))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Decision.Action != ActionBlock {
		t.Errorf("event action = %s, want block", events[0].Decision.Action)
	}
	if !events[0].Decision.Applied {
		t.Errorf("event applied = false, want true")
	}
	if !events[0].Decision.EvaluatedInline {
		t.Errorf("event evaluated_inline = false, want true")
	}
}

func TestPreventive_NoInlineMatch_PassThrough(t *testing.T) {
	pol := DefaultPolicy()
	pol.Preventive.Enabled = true
	p, workersRoot := preventiveFixture(t, pol)

	// pii.phone_cn is low severity, inline=false; does NOT trigger preventive.
	dec := p.PreventiveCheck(Envelope{
		AgentID:       "w-y",
		SourceChannel: SourceGateway,
		Direction:     DirectionOutbound,
		Payload:       []byte("call 13800138000"),
	})
	if dec.Action != ActionNone {
		t.Errorf("phone alone should not block, got %+v", dec)
	}
	if dec.Reason != "no_inline_match" {
		t.Errorf("expected reason no_inline_match, got %s", dec.Reason)
	}
	// No preventive event for pass-through.
	if _, err := os.Stat(filepath.Join(workersRoot, "w-y", ".cicy", "history", "audit.ndjson")); !os.IsNotExist(err) {
		t.Errorf("expected no event file, got err=%v", err)
	}
}

func TestPreventive_AWS_AKID_Redacts(t *testing.T) {
	pol := DefaultPolicy()
	pol.Preventive.Enabled = true
	p, workersRoot := preventiveFixture(t, pol)

	// secret.aws_akid is inline=true with DefaultAction=redact. Phase 3 cut 2
	// applies the redaction: the body returned to the caller has the AKID
	// replaced with [REDACTED:secret.aws_akid] and the original is saved
	// to the encrypted pre-redact archive.
	original := []byte("token=AKIAIOSFODNN7EXAMPLE end")
	dec := p.PreventiveCheck(Envelope{
		AgentID:       "w-z",
		SourceChannel: SourceGateway,
		Direction:     DirectionOutbound,
		Payload:       original,
	})
	if dec.Action != ActionRedact {
		t.Fatalf("aws_akid must redact in cut 2, got %+v", dec)
	}
	if !strings.Contains(string(dec.ModifiedPayload), "[REDACTED:secret.aws_akid]") {
		t.Errorf("modified payload missing REDACTED token: %q", dec.ModifiedPayload)
	}
	if strings.Contains(string(dec.ModifiedPayload), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("original AKID leaked into modified payload: %q", dec.ModifiedPayload)
	}
	if dec.PreRedactRef == "" {
		t.Error("expected non-empty PreRedactRef")
	}
	if dec.EventID == "" {
		t.Error("expected non-empty EventID")
	}

	// Pre-redact file should decrypt back to the original.
	encPath := filepath.Join(workersRoot, "w-z", ".cicy", "history", "pre-redact", dec.EventID+".enc")
	data, err := os.ReadFile(encPath)
	if err != nil {
		t.Fatalf("read pre-redact file: %v", err)
	}
	plaintext, err := DecryptPreRedact(filepath.Join(filepath.Dir(workersRoot), "audit"), data)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(plaintext) != string(original) {
		t.Errorf("pre-redact decrypted %q != original %q", plaintext, original)
	}
}

func TestPreventive_BlockBeatsRedact(t *testing.T) {
	pol := DefaultPolicy()
	pol.Preventive.Enabled = true
	p, _ := preventiveFixture(t, pol)

	// Payload with BOTH a private-key block trigger and an AKID redact
	// trigger. Block must win — the redact is moot once the request is
	// refused.
	dec := p.PreventiveCheck(Envelope{
		AgentID:       "w-mix",
		SourceChannel: SourceGateway,
		Direction:     DirectionOutbound,
		Payload:       []byte("-----BEGIN RSA PRIVATE KEY-----\nAKIAIOSFODNN7EXAMPLE\n"),
	})
	if dec.Action != ActionBlock {
		t.Errorf("block must beat redact, got %s", dec.Action)
	}
	// Findings should only carry the block-default rule(s).
	for _, f := range dec.Findings {
		if f.RuleID == "secret.aws_akid" {
			t.Errorf("redact-default findings should not appear when block wins, got: %+v", f)
		}
	}
}

func TestRedactPayload(t *testing.T) {
	payload := []byte("AAAA xxx BBBB yyy CCCC")
	findings := []Finding{
		{
			RuleID: "r1",
			Spans: []Span{
				{Start: 0, End: 4},   // AAAA
				{Start: 9, End: 13},  // BBBB
				{Start: 18, End: 22}, // CCCC
			},
		},
	}
	got := string(RedactPayload(payload, findings))
	want := "[REDACTED:r1] xxx [REDACTED:r1] yyy [REDACTED:r1]"
	if got != want {
		t.Errorf("redact wrong:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestPreRedactRoundTrip(t *testing.T) {
	root := t.TempDir()
	auditRoot := filepath.Join(root, "audit")
	workersRoot := filepath.Join(root, "workers")
	original := []byte("sensitive prompt with AKID=AKIAIOSFODNN7EXAMPLE")

	ref, err := SavePreRedact(auditRoot, workersRoot, "w-x", "evt_test", original)
	if err != nil {
		t.Fatal(err)
	}
	if ref != "pre-redact:w-x/evt_test.enc" {
		t.Errorf("ref = %q", ref)
	}

	encPath := filepath.Join(workersRoot, "w-x", ".cicy", "history", "pre-redact", "evt_test.enc")
	data, err := os.ReadFile(encPath)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := DecryptPreRedact(auditRoot, data)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != string(original) {
		t.Errorf("decrypted %q != original %q", plaintext, original)
	}

	// Encrypted bytes must NOT contain the plaintext.
	if strings.Contains(string(data), "AKIAIOSFODNN7EXAMPLE") {
		t.Error("plaintext leaked into encrypted blob")
	}
}

func TestPreventive_AllowlistAgent_Bypass(t *testing.T) {
	pol := DefaultPolicy()
	pol.Preventive.Enabled = true
	pol.AllowList.Agents = []string{"w-trusted"}
	p, _ := preventiveFixture(t, pol)

	dec := p.PreventiveCheck(Envelope{
		AgentID:       "w-trusted",
		SourceChannel: SourceGateway,
		Direction:     DirectionOutbound,
		Payload:       []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIE..."),
	})
	if dec.Action != ActionNone {
		t.Errorf("allow-listed agent must bypass preventive, got %+v", dec)
	}
	if dec.Reason != "allowlisted_agent" {
		t.Errorf("expected reason allowlisted_agent, got %q", dec.Reason)
	}
}

func TestPreventive_PrivateKey_BlocksAndStampsFindings(t *testing.T) {
	pol := DefaultPolicy()
	pol.Preventive.Enabled = true
	p, workersRoot := preventiveFixture(t, pol)

	dec := p.PreventiveCheck(Envelope{
		AgentID:       "w-w",
		SourceChannel: SourceGateway,
		Direction:     DirectionOutbound,
		Payload:       []byte("here is the key: -----BEGIN OPENSSH PRIVATE KEY-----\nMIIE..."),
	})
	if dec.Action != ActionBlock {
		t.Fatalf("private key must block, got %+v", dec)
	}
	// Check only the secret.private_key finding made it through (other rules
	// have non-block defaults).
	for _, f := range dec.Findings {
		if f.RuleID != "secret.private_key" {
			t.Errorf("unexpected non-block-default finding leaked through filter: %s", f.RuleID)
		}
	}
	// Verify on disk.
	events := readEvents(t, filepath.Join(workersRoot, "w-w", ".cicy", "history", "audit.ndjson"))
	if len(events) != 1 || events[0].Decision.Action != ActionBlock {
		t.Errorf("expected single block event, got %+v", events)
	}
}
