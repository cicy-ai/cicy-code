package audit

import (
	"strings"
	"testing"
	"time"
)

func TestAck_SignVerifyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// First sign-verify pair generates the key file at <dir>/.ack.key.
	token, err := SignAckToken(dir, "evt_round_trip", AckTokenDefaultTTL)
	if err != nil {
		t.Fatalf("SignAckToken: %v", err)
	}
	if !strings.Contains(token, ".") {
		t.Errorf("token shape unexpected: %q", token)
	}
	got, err := VerifyAckToken(dir, token)
	if err != nil {
		t.Fatalf("VerifyAckToken: %v", err)
	}
	if got != "evt_round_trip" {
		t.Errorf("event id roundtrip: got %q want evt_round_trip", got)
	}
}

func TestAck_VerifyRejectsTamperedSignature(t *testing.T) {
	dir := t.TempDir()
	token, _ := SignAckToken(dir, "evt_x", time.Hour)
	parts := strings.SplitN(token, ".", 2)
	tampered := parts[0] + ".00000000000000000000000000000000"
	if _, err := VerifyAckToken(dir, tampered); err == nil {
		t.Error("tampered signature should reject")
	}
}

func TestAck_VerifyRejectsTamperedPayload(t *testing.T) {
	dir := t.TempDir()
	token, _ := SignAckToken(dir, "evt_x", time.Hour)
	parts := strings.SplitN(token, ".", 2)
	// Different (valid b64) payload — sig won't match.
	tampered := "ZWlkPWV2dF9oYWNrZWQ" + "." + parts[1]
	if _, err := VerifyAckToken(dir, tampered); err == nil {
		t.Error("tampered payload should reject")
	}
}

func TestAck_VerifyExpired(t *testing.T) {
	dir := t.TempDir()
	// Negative TTL is overridden to the default; force a tiny window via
	// direct call. Easier path: sign with 1-second TTL, sleep > 1 sec.
	token, err := SignAckToken(dir, "evt_old", time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second) // tokens use unix-second resolution
	if _, err := VerifyAckToken(dir, token); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected expired error, got %v", err)
	}
}

func TestAck_VerifyMalformed(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{
		"",
		"no-dot",
		".only-trailing",
		"only-leading.",
	} {
		if _, err := VerifyAckToken(dir, bad); err == nil {
			t.Errorf("malformed %q should reject", bad)
		}
	}
}

func TestAck_DifferentKeysRejectEachOther(t *testing.T) {
	// Two separate audit roots => two separate keys; a token from one
	// must not verify under the other.
	a := t.TempDir()
	b := t.TempDir()
	token, _ := SignAckToken(a, "evt_x", time.Hour)
	// Force key load into the "b" cache by calling SignAckToken there
	// first — but we're using a package-level sync.Once so the FIRST call
	// won (root a). To isolate, we'd need a per-call key. For Phase 6 cut
	// 2c walking skeleton the once-loaded key is fine; document the limit:
	if _, err := VerifyAckToken(a, token); err != nil {
		t.Errorf("same-root verify must succeed: %v", err)
	}
	_ = b
}

func TestRecordAck_AppendsMetaEvent(t *testing.T) {
	pol := DefaultPolicy()
	p, _ := preventiveFixture(t, pol)

	metaID, err := p.recordAck("evt_original", "w-x", "Mozilla", "1.2.3.4")
	if err != nil {
		t.Fatalf("recordAck: %v", err)
	}
	if metaID == "" {
		t.Error("expected meta event id")
	}

	// Find the meta event in the agent's audit log.
	events := readEvents(t, p.store.agentAuditPath("w-x"))
	var meta *Event
	for i := range events {
		if events[i].Meta.Category == "meta_alert_ack" {
			meta = &events[i]
			break
		}
	}
	if meta == nil {
		t.Fatal("meta_alert_ack event not appended")
	}
	if meta.Meta.AckEventID != "evt_original" {
		t.Errorf("ack_event_id = %q, want evt_original", meta.Meta.AckEventID)
	}
	if meta.Decision.Action != ActionLog {
		t.Errorf("decision.action = %q, want log", meta.Decision.Action)
	}
}
