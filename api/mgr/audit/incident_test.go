package audit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResponsiblePersons_Resolve_AllTiers(t *testing.T) {
	r := ResponsiblePersonsConfig{
		Default:    []string{"sec@corp"},
		BySeverity: map[string][]string{"high": {"oncall@corp"}, "critical": {"ciso@corp"}},
		ByAgent:    map[string][]string{"w-1001": {"alice@corp"}, "w-1*": {"team-platform@corp"}},
		ByUser:     map[string][]string{"u-abc": {"alice@corp"}},
		ByRule:     map[string][]string{"secret.aws_akid": {"devops@corp"}},
	}

	// All tiers fire simultaneously — dedup expected (alice@corp appears
	// twice: by_agent w-1001 + by_user u-abc).
	got := r.Resolve(SeverityHigh, "w-1001", "u-abc", []string{"secret.aws_akid"})
	want := []string{"alice@corp", "devops@corp", "oncall@corp", "team-platform@corp"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("all-tiers: got %v want %v", got, want)
	}
}

func TestResponsiblePersons_DefaultFallback(t *testing.T) {
	r := ResponsiblePersonsConfig{Default: []string{"sec@corp"}}
	got := r.Resolve(SeverityHigh, "w-99", "u-99", []string{"unknown.rule"})
	if !reflect.DeepEqual(got, []string{"sec@corp"}) {
		t.Errorf("default fallback: got %v want [sec@corp]", got)
	}
}

func TestResponsiblePersons_NoMatchNoDefault(t *testing.T) {
	r := ResponsiblePersonsConfig{}
	got := r.Resolve(SeverityHigh, "w-99", "", nil)
	if len(got) != 0 {
		t.Errorf("empty config: got %v want []", got)
	}
}

func TestResponsiblePersons_AgentWildcard(t *testing.T) {
	r := ResponsiblePersonsConfig{
		ByAgent: map[string][]string{
			"w-100*":  {"a@corp"},
			"w-1001": {"b@corp"},
		},
	}
	// w-1001 matches both — both included, dedup.
	got := r.Resolve("", "w-1001", "", nil)
	if !reflect.DeepEqual(got, []string{"a@corp", "b@corp"}) {
		t.Errorf("wildcard merge: got %v", got)
	}
	// w-10042 matches only the wildcard.
	got = r.Resolve("", "w-10042", "", nil)
	if !reflect.DeepEqual(got, []string{"a@corp"}) {
		t.Errorf("wildcard only: got %v", got)
	}
}

func TestSeverityMeetsTrigger(t *testing.T) {
	mk := func(sev Severity) Event {
		return Event{Findings: []Finding{{Severity: sev}}}
	}
	if !severityMeetsTrigger(mk(SeverityHigh), SeverityHigh) {
		t.Error("high meets high")
	}
	if !severityMeetsTrigger(mk(SeverityCritical), SeverityHigh) {
		t.Error("critical meets high")
	}
	if severityMeetsTrigger(mk(SeverityMedium), SeverityHigh) {
		t.Error("medium must not meet high")
	}
	if severityMeetsTrigger(Event{}, SeverityHigh) {
		t.Error("no findings must not meet trigger")
	}
}

// ── dispatchIncident: auto-SMTP + forward to 审计策略专员 (audit-v2 contract) ──
//
// On a qualifying hit dispatchIncident now does BOTH: ① auto-emails the owner
// via the active mailer (no longer waiting for an agent to trigger it), and
// ② forwards a masked finding brief to the live 审计策略专员 agent for triage.
// These tests assert the forward (link ②); TestDispatchIncident_HighSeverity*
// also asserts the auto-email (link ①). The gates (enabled / trigger severity
// / cooldown) suppress BOTH channels.

func TestDispatchIncident_DisabledNoop(t *testing.T) {
	pol := DefaultPolicy()
	pol.IncidentResponse.Enabled = false
	p, _ := preventiveFixture(t, pol)
	fc := captureForwarder(t)

	p.dispatchIncident(Event{
		ID:       "evt_x",
		Identity: Identity{AgentID: "w-x"},
		Findings: []Finding{{RuleID: "r", Severity: SeverityHigh, Spans: []Span{{Preview: "p"}}}},
	})
	if fc.count() != 0 {
		t.Errorf("disabled: should not forward, got %d", fc.count())
	}
}

func TestDispatchIncident_BelowTriggerNoop(t *testing.T) {
	pol := DefaultPolicy()
	pol.IncidentResponse.Enabled = true
	pol.IncidentResponse.TriggerMinSeverity = SeverityHigh
	pol.ResponsiblePersons.Default = []string{"sec@corp"}
	p, _ := preventiveFixture(t, pol)
	fc := captureForwarder(t)

	p.dispatchIncident(Event{
		ID:       "evt_low",
		Identity: Identity{AgentID: "w-x"},
		Findings: []Finding{{RuleID: "pii.phone_cn", Severity: SeverityLow, MatchCount: 1, Spans: []Span{{Preview: "1380****0000"}}}},
	})
	if fc.count() != 0 {
		t.Errorf("below-trigger: should not forward, got %d", fc.count())
	}
}

// ── SendOwnerIncident: the owner-email path (advisor-triggered) ──
//
// This is what the EML-generation logic became after dispatchIncident stopped
// emailing directly. w-6001 calls it via POST /api/audit/notify.

func TestSendOwnerIncident_WritesEML(t *testing.T) {
	pol := DefaultPolicy()
	pol.IncidentResponse.Enabled = true
	pol.ResponsiblePersons.Default = []string{"sec@corp"}
	p, _ := preventiveFixture(t, pol)
	emailDir := dispatchEmailDir(t, p)

	err := p.SendOwnerIncident(Event{
		ID:        "evt_owner1",
		Timestamp: "2026-05-15T08:00:00Z",
		Identity:  Identity{AgentID: "w-x", AgentType: "claude"},
		Findings:  []Finding{{RuleID: "secret.aws_akid", Severity: SeverityHigh, Category: "secret", MatchCount: 1, Spans: []Span{{Preview: "AKIA****MPLE"}}}},
		Decision:  Decision{Action: ActionBlock, Applied: true},
	}, "GitHub token leaked — revoke + rotate now")
	if err != nil {
		t.Fatalf("SendOwnerIncident: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(emailDir, "evt_owner1.eml"))
	if err != nil {
		t.Fatalf("expected .eml file: %v", err)
	}
	s := string(data)
	for _, want := range []string{
		"To: sec@corp",
		"[CICY-AUDIT][HIGH]",
		"AKIA****MPLE",                  // masked finding preview
		"X-Cicy-Audit-Event: evt_owner1",
		"审计顾问研判",                  // advisor note header
		"GitHub token leaked",          // advisor note body prepended
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestSendOwnerIncident_NoRecipients(t *testing.T) {
	pol := DefaultPolicy()
	pol.IncidentResponse.Enabled = true
	// no responsible_persons configured → nothing resolves
	p, _ := preventiveFixture(t, pol)

	err := p.SendOwnerIncident(Event{
		ID:       "evt_norcpt",
		Identity: Identity{AgentID: "w-x"},
		Findings: []Finding{{RuleID: "secret.aws_akid", Severity: SeverityHigh, MatchCount: 1}},
	}, "")
	if err == nil {
		t.Error("expected error when no responsible person resolves")
	}
}

func TestFileMailer_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := &FileMailer{OutputDir: dir}
	err := m.Send(EmailMessage{
		To:       []string{"alice@corp", "bob@corp"},
		Subject:  "[CICY-AUDIT][HIGH] test — w-x",
		Body:     "hello",
		EventID:  "evt_test",
		AgentID:  "w-x",
		Severity: SeverityHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "evt_test.eml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"To: alice@corp, bob@corp",
		"Subject: [CICY-AUDIT][HIGH] test — w-x",
		"X-Cicy-Audit-Event: evt_test",
		"X-Cicy-Audit-Severity: high",
		"hello",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

// ── helpers ──

// forwardCapture is a thread-safe stub for the advisor-forward channel.
type forwardCapture struct {
	mu     sync.Mutex
	briefs []string
}

func (fc *forwardCapture) count() int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return len(fc.briefs)
}

func (fc *forwardCapture) last() string {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.briefs) == 0 {
		return ""
	}
	return fc.briefs[len(fc.briefs)-1]
}

// captureForwarder installs a capturing advisor-forward channel and restores
// the previous one on cleanup.
func captureForwarder(t *testing.T) *forwardCapture {
	t.Helper()
	fc := &forwardCapture{}
	prev := findingForwarder
	findingForwarder = func(brief string) error {
		fc.mu.Lock()
		fc.briefs = append(fc.briefs, brief)
		fc.mu.Unlock()
		return nil
	}
	t.Cleanup(func() { findingForwarder = prev })
	return fc
}

func waitForForwardCount(t *testing.T, fc *forwardCapture, want int) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if fc.count() >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("advisor forwards: want >=%d got %d", want, fc.count())
}

func dispatchEmailDir(t *testing.T, p *Pipeline) string {
	t.Helper()
	return filepath.Join(p.store.auditRoot, "email-out")
}
