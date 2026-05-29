package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderIMIncident(t *testing.T) {
	e := Event{
		ID:       "evt_im1",
		Identity: Identity{AgentID: "w-x", AgentType: "codex"},
		Findings: []Finding{{RuleID: "secret.aws_akid", Severity: SeverityHigh, Spans: []Span{{Preview: "AKIA****MPLE"}}}},
	}
	out := renderIMIncident(e, "立即吊销", "https://h/api/audit/ack?token=t")
	for _, want := range []string{"HIGH", "w-x", "secret.aws_akid", "AKIA****MPLE", "立即吊销", "/api/audit/ack?token=t"} {
		if !strings.Contains(out, want) {
			t.Errorf("IM text missing %q in:\n%s", want, out)
		}
	}
}

// WeChat fires additively: with no email recipients but a bound IM channel,
// the IM delivery alone makes SendOwnerIncident succeed.
func TestSendOwnerIncident_WeChatOnlyDelivers(t *testing.T) {
	pol := DefaultPolicy()
	pol.IncidentResponse.Enabled = true // no responsible_persons → email can't go
	p, _ := preventiveFixture(t, pol)

	var gotIM string
	SetIMBoundCheck(func() bool { return true })
	SetIMNotifier(func(text string) (bool, error) { gotIM = text; return true, nil })
	t.Cleanup(func() { SetIMBoundCheck(nil); SetIMNotifier(nil) })

	err := p.SendOwnerIncident(Event{
		ID:       "evt_imonly",
		Identity: Identity{AgentID: "w-x"},
		Findings: []Finding{{RuleID: "secret.aws_akid", Severity: SeverityHigh, MatchCount: 1, Spans: []Span{{Preview: "AKIA****MPLE"}}}},
	}, "note")
	if err != nil {
		t.Fatalf("WeChat-only delivery should succeed, got %v", err)
	}
	if !strings.Contains(gotIM, "secret.aws_akid") {
		t.Errorf("IM text not built: %q", gotIM)
	}
}

// Email AND WeChat both fire when both are available (additive).
func TestSendOwnerIncident_BothChannels(t *testing.T) {
	pol := DefaultPolicy()
	pol.IncidentResponse.Enabled = true
	pol.ResponsiblePersons.Default = []string{"sec@corp"}
	p, _ := preventiveFixture(t, pol)
	emailDir := dispatchEmailDir(t, p)

	imCalls := 0
	SetIMBoundCheck(func() bool { return true })
	SetIMNotifier(func(string) (bool, error) { imCalls++; return true, nil })
	t.Cleanup(func() { SetIMBoundCheck(nil); SetIMNotifier(nil) })

	if err := p.SendOwnerIncident(Event{
		ID:       "evt_both",
		Identity: Identity{AgentID: "w-x"},
		Findings: []Finding{{RuleID: "secret.aws_akid", Severity: SeverityHigh, MatchCount: 1, Spans: []Span{{Preview: "AKIA****MPLE"}}}},
	}, ""); err != nil {
		t.Fatalf("both channels: %v", err)
	}
	if imCalls != 1 {
		t.Errorf("WeChat should fire once, got %d", imCalls)
	}
	if _, err := os.Stat(filepath.Join(emailDir, "evt_both.eml")); err != nil {
		t.Errorf("email channel should also have written .eml: %v", err)
	}
}

// SendTestNotification reports per-channel outcome (email via mailer + WeChat
// when bound) without needing a real finding.
func TestSendTestNotification(t *testing.T) {
	p, _ := preventiveFixture(t, DefaultPolicy())
	SetIMBoundCheck(func() bool { return false })
	t.Cleanup(func() { SetIMBoundCheck(nil) })
	summary, err := p.SendTestNotification("officer@corp")
	if err != nil {
		t.Fatalf("test notify: %v", err)
	}
	if !strings.Contains(summary, "已发") || !strings.Contains(summary, "officer@corp") {
		t.Errorf("email not reported sent: %q", summary)
	}
	if !strings.Contains(summary, "微信: 未绑定") {
		t.Errorf("should note wechat unbound: %q", summary)
	}
}

// 2.1.8: TestSendOwnerIncident_SecurityOfficerEscalation removed — the audit
// advisor (w-6001) now owns human coordination, so SendOwnerIncident has only
// two channels (email + WeChat); there's no security-officer agent to escalate
// to. The merge is exercised by TestSendOwnerIncident_BothChannels above.

// With neither email recipients nor a bound IM channel, it still errors.
func TestSendOwnerIncident_NoChannelErrors(t *testing.T) {
	pol := DefaultPolicy()
	pol.IncidentResponse.Enabled = true
	p, _ := preventiveFixture(t, pol)
	SetIMBoundCheck(func() bool { return false })
	t.Cleanup(func() { SetIMBoundCheck(nil) })
	if err := p.SendOwnerIncident(Event{ID: "evt_none", Identity: Identity{AgentID: "w-x"},
		Findings: []Finding{{RuleID: "secret.aws_akid", Severity: SeverityHigh}}}, ""); err == nil {
		t.Error("expected error when no channel delivers")
	}
}
