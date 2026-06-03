package audit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoise_Suspended(t *testing.T) {
	n := newNoiseTracker()
	cfg := DefaultNotifyConfig()
	cfg.Suspended = true
	got := n.EvaluateNotify("a", "r", "h", 1, cfg, SeverityMedium)
	if got != "suspended" {
		t.Errorf("suspended: got %q want suspended", got)
	}
}

func TestNoise_RateLimit(t *testing.T) {
	n := newNoiseTracker()
	cfg := DefaultNotifyConfig()
	cfg.RateLimit.WindowSeconds = 60
	cfg.RateLimit.MaxPerAgentPerRule = 3
	cfg.Cooldown.Seconds = 0 // disable cooldown for this test

	for i := 1; i <= 3; i++ {
		if reason := n.EvaluateNotify("agent-1", "rule.x", "h"+string(rune(i)), int64(i), cfg, SeverityMedium); reason != "" {
			t.Errorf("call %d: unexpected suppress %q", i, reason)
		}
	}
	// 4th call within window must be rate-limited.
	if reason := n.EvaluateNotify("agent-1", "rule.x", "h4", 4, cfg, SeverityMedium); reason != "rate_limit" {
		t.Errorf("4th call: got %q want rate_limit", reason)
	}
}

func TestNoise_RateLimit_DistinctAgentsIndependent(t *testing.T) {
	n := newNoiseTracker()
	cfg := DefaultNotifyConfig()
	cfg.RateLimit.WindowSeconds = 60
	cfg.RateLimit.MaxPerAgentPerRule = 1
	cfg.Cooldown.Seconds = 0

	if reason := n.EvaluateNotify("agent-A", "rule.x", "h1", 1, cfg, SeverityMedium); reason != "" {
		t.Errorf("A: %q want \"\"", reason)
	}
	if reason := n.EvaluateNotify("agent-B", "rule.x", "h2", 2, cfg, SeverityMedium); reason != "" {
		t.Errorf("B same rule should be independent: %q", reason)
	}
	// Same agent again should hit limit.
	if reason := n.EvaluateNotify("agent-A", "rule.x", "h3", 3, cfg, SeverityMedium); reason != "rate_limit" {
		t.Errorf("A second: %q want rate_limit", reason)
	}
}

func TestNoise_Cooldown(t *testing.T) {
	n := newNoiseTracker()
	cfg := DefaultNotifyConfig()
	cfg.Cooldown.Seconds = 100
	cfg.RateLimit.MaxPerAgentPerRule = 1000 // effectively disabled

	if reason := n.EvaluateNotify("a", "r", "fh-1", 0, cfg, SeverityMedium); reason != "" {
		t.Errorf("first: %q want \"\"", reason)
	}
	// 50s later — within 100s cooldown.
	if reason := n.EvaluateNotify("a", "r", "fh-1", 50*int64(1e9), cfg, SeverityMedium); reason != "cooldown" {
		t.Errorf("within cooldown: %q want cooldown", reason)
	}
	// Different finding hash — must NOT be cooled down.
	if reason := n.EvaluateNotify("a", "r", "fh-2", 50*int64(1e9), cfg, SeverityMedium); reason != "" {
		t.Errorf("different hash within window: %q want \"\"", reason)
	}
	// 101s later — cooldown expired.
	if reason := n.EvaluateNotify("a", "r", "fh-1", 101*int64(1e9), cfg, SeverityMedium); reason != "" {
		t.Errorf("after cooldown: %q want \"\"", reason)
	}
}

func TestEventFindingHash(t *testing.T) {
	mk := func(agent, ruleID, preview string, sev Severity) Event {
		return Event{
			Identity: Identity{AgentID: agent},
			Findings: []Finding{{RuleID: ruleID, Severity: sev, Spans: []Span{{Preview: preview}}}},
		}
	}
	// Empty
	if h := EventFindingHash(Event{}); h != "" {
		t.Errorf("empty event: %q want \"\"", h)
	}
	// Identity yields identity
	h1 := EventFindingHash(mk("a", "r", "pv", SeverityHigh))
	h2 := EventFindingHash(mk("a", "r", "pv", SeverityHigh))
	if h1 != h2 {
		t.Errorf("same inputs should hash same: %s vs %s", h1, h2)
	}
	// Differs on agent
	if EventFindingHash(mk("b", "r", "pv", SeverityHigh)) == h1 {
		t.Errorf("agent should affect hash")
	}
	// Differs on preview
	if EventFindingHash(mk("a", "r", "pv2", SeverityHigh)) == h1 {
		t.Errorf("preview should affect hash")
	}
	// Differs on rule
	if EventFindingHash(mk("a", "r2", "pv", SeverityHigh)) == h1 {
		t.Errorf("rule_id should affect hash")
	}
}

// End-to-end: feed two events with the same payload (same finding identity)
// through the pipeline and verify the second is stamped cooldown.
func TestPipeline_Cooldown_EndToEnd(t *testing.T) {
	pol := DefaultPolicy()
	pol.Notify.Cooldown.Seconds = 3600
	pol.Notify.RateLimit.MaxPerAgentPerRule = 1000 // generous so it's only cooldown
	p, workersRoot := setupPipelineWithPolicy(t, pol)

	payload := []byte("call 13800138000")
	// pii.phone_cn defaults to LOW → action=log. Promote to MEDIUM so the
	// action becomes notify AND the finding stays cooldown-suppressible
	// (high/critical deliberately pierce cooldown so real leaks aren't muted).
	pol.RulesOverride = []RuleOverride{{ID: "pii.phone_cn", Severity: SeverityMedium}}
	if err := p.ApplyPolicy(pol); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		p.Submit(context.Background(), Envelope{
			AgentID:       "w-cd",
			SourceChannel: SourceGateway,
			Direction:     DirectionOutbound,
			Payload:       payload,
			PayloadRef:    "current.json#t1",
			Inline:        true,
		})
	}
	p.Wait()

	events := readEvents(t, filepath.Join(workersRoot, "w-cd", ".cicy", "history", "audit.ndjson"))
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	// Chronological order: events[0] first, events[1] second.
	if events[0].Meta.NotifySuppressedBy != "" {
		t.Errorf("first event should not be suppressed, got %q", events[0].Meta.NotifySuppressedBy)
	}
	if events[1].Meta.NotifySuppressedBy != "cooldown" {
		t.Errorf("second event should be cooldown-suppressed, got %q", events[1].Meta.NotifySuppressedBy)
	}
	// Both should still have action=notify (intent preserved).
	if events[0].Decision.Action != ActionNotify || events[1].Decision.Action != ActionNotify {
		t.Errorf("both events should keep action=notify, got %s / %s",
			events[0].Decision.Action, events[1].Decision.Action)
	}
}

func TestPipeline_RateLimit_EndToEnd(t *testing.T) {
	pol := DefaultPolicy()
	pol.Notify.Cooldown.Seconds = 0 // disable so we isolate rate_limit
	pol.Notify.RateLimit.WindowSeconds = 3600
	pol.Notify.RateLimit.MaxPerAgentPerRule = 2
	pol.RulesOverride = []RuleOverride{{ID: "pii.phone_cn", Severity: SeverityHigh}}

	p, workersRoot := setupPipelineWithPolicy(t, pol)

	// 3 events, each with a DIFFERENT phone number so the cooldown wouldn't
	// suppress them — only rate_limit applies.
	for i, phone := range []string{"13800138001", "13800138002", "13800138003"} {
		p.Submit(context.Background(), Envelope{
			AgentID:       "w-rl",
			SourceChannel: SourceGateway,
			Direction:     DirectionOutbound,
			Payload:       []byte("call " + phone),
			PayloadRef:    "current.json#t" + string(rune('0'+i)),
			Inline:        true,
		})
	}
	p.Wait()

	events := readEvents(t, filepath.Join(workersRoot, "w-rl", ".cicy", "history", "audit.ndjson"))
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Meta.NotifySuppressedBy != "" || events[1].Meta.NotifySuppressedBy != "" {
		t.Errorf("first two should pass: %q / %q",
			events[0].Meta.NotifySuppressedBy, events[1].Meta.NotifySuppressedBy)
	}
	if events[2].Meta.NotifySuppressedBy != "rate_limit" {
		t.Errorf("third should be rate-limited, got %q", events[2].Meta.NotifySuppressedBy)
	}
}

func TestNoise_SeverityPiercing(t *testing.T) {
	// Critical pierces an explicit suspend; high still respects it.
	{
		n := newNoiseTracker()
		cfg := DefaultNotifyConfig()
		cfg.Suspended = true
		if r := n.EvaluateNotify("a", "r", "h", 1, cfg, SeverityCritical); r != "" {
			t.Errorf("critical should pierce suspend, got %q", r)
		}
		if r := n.EvaluateNotify("a", "r", "h", 2, cfg, SeverityHigh); r != "suspended" {
			t.Errorf("high should respect suspend, got %q", r)
		}
	}

	// High pierces cooldown; medium is muted by it.
	{
		n := newNoiseTracker()
		cfg := DefaultNotifyConfig()
		cfg.Cooldown.Seconds = 100
		cfg.RateLimit.WindowSeconds = 0 // isolate cooldown
		n.EvaluateNotify("a", "r", "fh", 0, cfg, SeverityMedium) // anchor
		if r := n.EvaluateNotify("a", "r", "fh", 10*int64(1e9), cfg, SeverityMedium); r != "cooldown" {
			t.Errorf("medium should cooldown, got %q", r)
		}
		if r := n.EvaluateNotify("a", "r", "fh", 11*int64(1e9), cfg, SeverityHigh); r != "" {
			t.Errorf("high should pierce cooldown, got %q", r)
		}
		if r := n.EvaluateNotify("a", "r", "fh", 12*int64(1e9), cfg, SeverityCritical); r != "" {
			t.Errorf("critical should pierce cooldown, got %q", r)
		}
	}

	// High is still flood-limited; critical pierces rate limit.
	{
		n := newNoiseTracker()
		cfg := DefaultNotifyConfig()
		cfg.Cooldown.Seconds = 0 // isolate rate limit
		cfg.RateLimit.WindowSeconds = 3600
		cfg.RateLimit.MaxPerAgentPerRule = 1
		if r := n.EvaluateNotify("a", "rr", "h1", 1, cfg, SeverityHigh); r != "" {
			t.Errorf("first high should pass, got %q", r)
		}
		if r := n.EvaluateNotify("a", "rr", "h2", 2, cfg, SeverityHigh); r != "rate_limit" {
			t.Errorf("second high should rate_limit, got %q", r)
		}
		if r := n.EvaluateNotify("a", "rr", "h3", 3, cfg, SeverityCritical); r != "" {
			t.Errorf("critical should pierce rate_limit, got %q", r)
		}
	}
}

func TestPipeline_Suspended_EndToEnd(t *testing.T) {
	pol := DefaultPolicy()
	pol.Notify.Suspended = true
	pol.RulesOverride = []RuleOverride{{ID: "pii.phone_cn", Severity: SeverityHigh}}
	p, workersRoot := setupPipelineWithPolicy(t, pol)

	p.Submit(context.Background(), Envelope{
		AgentID:       "w-sus",
		SourceChannel: SourceGateway,
		Direction:     DirectionOutbound,
		Payload:       []byte("call 13800138000"),
		PayloadRef:    "current.json#t1",
		Inline:        true,
	})
	p.Wait()

	events := readEvents(t, filepath.Join(workersRoot, "w-sus", ".cicy", "history", "audit.ndjson"))
	if len(events) != 1 || events[0].Meta.NotifySuppressedBy != "suspended" {
		t.Errorf("expected suspended event, got %+v", events)
	}
}

func readEvents(t *testing.T, path string) []Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []Event
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("parse: %v", err)
		}
		out = append(out, e)
	}
	return out
}
