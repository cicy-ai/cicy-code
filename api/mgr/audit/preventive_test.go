package audit

import (
	"os"
	"path/filepath"
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

func TestPreventive_AWS_AKID_Blocks(t *testing.T) {
	pol := DefaultPolicy()
	pol.Preventive.Enabled = true
	p, _ := preventiveFixture(t, pol)

	// secret.aws_akid is inline=true with DefaultAction=redact. NOT block.
	// Verify that a NON-block inline action does NOT trigger preventive
	// block (preventive cut 1 is block-only; redact is cut 2).
	dec := p.PreventiveCheck(Envelope{
		AgentID:       "w-z",
		SourceChannel: SourceGateway,
		Direction:     DirectionOutbound,
		Payload:       []byte("AKIAIOSFODNN7EXAMPLE"),
	})
	if dec.Action != ActionNone {
		t.Errorf("aws_akid (redact-default) must not block in cut 1, got %+v", dec)
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
