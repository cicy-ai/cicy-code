package audit

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestResponsiblePersons_Resolve_AllTiers(t *testing.T) {
	r := ResponsiblePersonsConfig{
		Default:    []string{"sec@corp"},
		BySeverity: map[string][]string{"high": {"oncall@corp"}, "critical": {"ciso@corp"}},
		ByAgent:    map[string][]string{"w-10001": {"alice@corp"}, "w-1*": {"team-platform@corp"}},
		ByUser:     map[string][]string{"u-abc": {"alice@corp"}},
		ByRule:     map[string][]string{"secret.aws_akid": {"devops@corp"}},
	}

	// All tiers fire simultaneously — dedup expected (alice@corp appears
	// twice: by_agent w-10001 + by_user u-abc).
	got := r.Resolve(SeverityHigh, "w-10001", "u-abc", []string{"secret.aws_akid"})
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
			"w-10001": {"b@corp"},
		},
	}
	// w-10001 matches both — both included, dedup.
	got := r.Resolve("", "w-10001", "", nil)
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

func TestDispatchIncident_DisabledNoop(t *testing.T) {
	pol := DefaultPolicy()
	pol.IncidentResponse.Enabled = false
	p, _ := preventiveFixture(t, pol)
	emailDir := dispatchEmailDir(t, p)

	p.dispatchIncident(Event{
		ID:       "evt_x",
		Identity: Identity{AgentID: "w-x"},
		Findings: []Finding{{RuleID: "r", Severity: SeverityHigh, Spans: []Span{{Preview: "p"}}}},
	})
	files := listFiles(t, emailDir)
	if len(files) != 0 {
		t.Errorf("disabled: should not write any .eml, got %v", files)
	}
}

func TestDispatchIncident_HighSeverityWritesEML(t *testing.T) {
	pol := DefaultPolicy()
	pol.IncidentResponse.Enabled = true
	pol.IncidentResponse.TriggerMinSeverity = SeverityHigh
	pol.ResponsiblePersons.Default = []string{"sec@corp"}
	p, _ := preventiveFixture(t, pol)
	emailDir := dispatchEmailDir(t, p)

	p.dispatchIncident(Event{
		ID:        "evt_x123",
		Timestamp: "2026-05-15T08:00:00Z",
		Identity:  Identity{AgentID: "w-x", AgentType: "claude"},
		Findings:  []Finding{{RuleID: "secret.aws_akid", Severity: SeverityHigh, MatchCount: 1, Spans: []Span{{Preview: "AKIA****MPLE"}}}},
		Decision:  Decision{Action: ActionRedact, Applied: true},
	})
	path := filepath.Join(emailDir, "evt_x123.eml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected .eml file at %s: %v", path, err)
	}
	s := string(data)
	if !strings.Contains(s, "To: sec@corp") {
		t.Error("missing default recipient")
	}
	if !strings.Contains(s, "[CICY-AUDIT][HIGH]") {
		t.Errorf("subject wrong, body: %s", s[:200])
	}
	if !strings.Contains(s, "AKIA****MPLE") {
		t.Error("missing finding preview in body")
	}
	if !strings.Contains(s, "English summary") {
		t.Error("missing bilingual section")
	}
	if !strings.Contains(s, "X-Cicy-Audit-Event: evt_x123") {
		t.Error("missing X-Cicy-Audit-Event header")
	}
}

func TestDispatchIncident_BelowTriggerNoop(t *testing.T) {
	pol := DefaultPolicy()
	pol.IncidentResponse.Enabled = true
	pol.IncidentResponse.TriggerMinSeverity = SeverityHigh
	pol.ResponsiblePersons.Default = []string{"sec@corp"}
	p, _ := preventiveFixture(t, pol)
	emailDir := dispatchEmailDir(t, p)

	p.dispatchIncident(Event{
		ID:       "evt_low",
		Identity: Identity{AgentID: "w-x"},
		Findings: []Finding{{RuleID: "pii.phone_cn", Severity: SeverityLow, MatchCount: 1, Spans: []Span{{Preview: "1380****0000"}}}},
	})
	files := listFiles(t, emailDir)
	if len(files) != 0 {
		t.Errorf("below-trigger: should not write any .eml, got %v", files)
	}
}

func TestDispatchIncident_CooldownDedupes(t *testing.T) {
	pol := DefaultPolicy()
	pol.IncidentResponse.Enabled = true
	pol.IncidentResponse.TriggerMinSeverity = SeverityHigh
	pol.IncidentResponse.CooldownSeconds = 3600
	pol.ResponsiblePersons.Default = []string{"sec@corp"}
	p, _ := preventiveFixture(t, pol)
	emailDir := dispatchEmailDir(t, p)

	finding := Finding{RuleID: "secret.aws_akid", Severity: SeverityHigh, MatchCount: 1, Spans: []Span{{Preview: "AKIA****MPLE"}}}
	// First event — should write .eml
	p.dispatchIncident(Event{
		ID:       "evt_a",
		Identity: Identity{AgentID: "w-x"},
		Findings: []Finding{finding},
	})
	// Second event with SAME finding hash — should NOT write (cooldown)
	p.dispatchIncident(Event{
		ID:       "evt_b",
		Identity: Identity{AgentID: "w-x"},
		Findings: []Finding{finding},
	})
	files := listFiles(t, emailDir)
	if len(files) != 1 {
		t.Errorf("cooldown: want 1 .eml, got %d (%v)", len(files), files)
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

func TestPipeline_PreventiveBlock_TriggersIncidentEmail(t *testing.T) {
	pol := DefaultPolicy()
	pol.Preventive.Enabled = true
	pol.IncidentResponse.Enabled = true
	pol.IncidentResponse.TriggerMinSeverity = SeverityHigh
	pol.ResponsiblePersons.Default = []string{"sec@corp"}
	p, _ := preventiveFixture(t, pol)
	emailDir := dispatchEmailDir(t, p)

	dec := p.PreventiveCheck(Envelope{
		AgentID:       "w-block",
		SourceChannel: SourceGateway,
		Direction:     DirectionOutbound,
		Payload:       []byte("-----BEGIN RSA PRIVATE KEY-----"),
	})
	if dec.Action != ActionBlock {
		t.Fatalf("expected block, got %v", dec.Action)
	}

	// dispatchIncident runs in a goroutine. Wait briefly.
	waitForFile(t, filepath.Join(emailDir, dec.EventID+".eml"))
}

// ── helpers ──

func dispatchEmailDir(t *testing.T, p *Pipeline) string {
	t.Helper()
	dir := filepath.Join(p.store.auditRoot, "email-out")
	return dir
}

func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	for i := 0; i < 30; i++ {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("file never appeared: %s", path)
}

// Stub: avoid an extra import in this test file alone.
var _ = context.Background
