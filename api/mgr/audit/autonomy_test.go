package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withTempHome redirects $HOME to a temp dir so autonomy reads/writes
// touch nothing outside the test sandbox.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestLoadAutonomyConfig_DefaultsWhenMissing(t *testing.T) {
	withTempHome(t)
	cfg, err := LoadAutonomyConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("default should be disabled")
	}
	if time.Duration(cfg.Interval) != 10*time.Minute {
		t.Errorf("interval default = %s, want 10m", time.Duration(cfg.Interval))
	}
	if time.Duration(cfg.Lookback) != 24*time.Hour {
		t.Errorf("lookback default = %s, want 24h", time.Duration(cfg.Lookback))
	}
	if cfg.MaxChangesPerHour != 5 || cfg.MaxChangesPerTick != 3 {
		t.Errorf("rate defaults wrong: %+v", cfg)
	}
	if cfg.LLM.Model != "deepseek-v4-pro" {
		t.Errorf("model default = %q", cfg.LLM.Model)
	}
}

func TestLoadAutonomyConfig_EnvFallback(t *testing.T) {
	withTempHome(t)
	t.Setenv("AUTONOMY_LLM_ENDPOINT", "https://example.test/v1/chat/completions")
	t.Setenv("AUTONOMY_LLM_API_KEY", "sk-test")
	cfg, _ := LoadAutonomyConfig("")
	if cfg.LLM.Endpoint != "https://example.test/v1/chat/completions" {
		t.Errorf("endpoint env fallback: %q", cfg.LLM.Endpoint)
	}
	if cfg.LLM.APIKey != "sk-test" {
		t.Errorf("api key env fallback: %q", cfg.LLM.APIKey)
	}
}

func TestLoadAutonomyConfig_FileOverridesEnv(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("AUTONOMY_LLM_MODEL", "from-env")
	path := filepath.Join(home, "cicy-ai", "autonomy", "autonomy.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"enabled":true,"interval":"30s","llm":{"endpoint":"https://e/","model":"file-model"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAutonomyConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatal("enabled flag lost")
	}
	if time.Duration(cfg.Interval) != 30*time.Second {
		t.Errorf("interval: %s", time.Duration(cfg.Interval))
	}
	if cfg.LLM.Model != "file-model" {
		t.Errorf("file should win over env, got %q", cfg.LLM.Model)
	}
}

func TestParseAutonomyResponse_StrictJSON(t *testing.T) {
	raw := `{"actions":[{"kind":"allow_list","rationale":"r","patch":{"allow_list":{"paths":["/x"]}}}]}`
	got, err := parseAutonomyResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "allow_list" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Patch.AllowList == nil || len(got[0].Patch.AllowList.Paths) != 1 {
		t.Fatalf("patch lost: %+v", got[0].Patch)
	}
}

func TestParseAutonomyResponse_WithSurroundingText(t *testing.T) {
	raw := "Here's my analysis:\n```json\n{\"actions\":[]}\n```\nLet me know."
	got, err := parseAutonomyResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}

func TestViolatesConstraints_ForbiddenActions(t *testing.T) {
	cfg := &AutonomyConfig{ForbiddenActions: []string{"enable_preventive_block", "custom_rules_add"}}
	cases := []struct {
		name string
		p    autonomyProposal
		want string
	}{
		{"safe override", autonomyProposal{Patch: PolicyPatch{RulesOverride: []RuleOverride{{ID: "r1", Severity: "low"}}}}, ""},
		{"preventive enable blocked", autonomyProposal{Patch: PolicyPatch{Preventive: &PreventiveConfig{Enabled: true}}}, "forbidden: enable_preventive_block"},
		{"custom rule blocked", autonomyProposal{Patch: PolicyPatch{CustomRules: []CustomRule{{ID: "custom.x", Severity: "medium"}}}}, "forbidden: custom_rules_add"},
		{"empty patch", autonomyProposal{Patch: PolicyPatch{}}, "empty_patch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := violatesConstraints(c.p, cfg)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestMergeAutonomyPatch_RulesOverrideAndAllowList(t *testing.T) {
	current := map[string]interface{}{
		"rules_override": []interface{}{
			map[string]interface{}{"id": "secret.aws_akid", "severity": "high"},
		},
		"allow_list": map[string]interface{}{
			"paths": []interface{}{"/existing"},
		},
	}
	patch := PolicyPatch{
		RulesOverride: []RuleOverride{
			{ID: "secret.aws_akid", Severity: "medium"},      // override
			{ID: "secret.bearer_token", DefaultAction: "log"}, // append
		},
		AllowList: &AllowList{
			Paths:  []string{"/existing", "/new"}, // dedup /existing
			Agents: []string{"w-99999"},
		},
	}
	mergeAutonomyPatch(current, patch)

	ro := current["rules_override"].([]interface{})
	if len(ro) != 2 {
		t.Fatalf("rules_override len = %d", len(ro))
	}
	first := ro[0].(map[string]interface{})
	if first["severity"] != "medium" {
		t.Errorf("override not applied: %v", first)
	}
	al := current["allow_list"].(map[string]interface{})
	paths := al["paths"].([]interface{})
	if len(paths) != 2 {
		t.Errorf("paths dedup broken: %v", paths)
	}
	agents := al["agents"].([]interface{})
	if len(agents) != 1 || agents[0] != "w-99999" {
		t.Errorf("agents not merged: %v", agents)
	}
}

func TestAppendDecisionRotatesWhenLarge(t *testing.T) {
	home := withTempHome(t)
	path := filepath.Join(home, "cicy-ai", "autonomy", "decisions.ndjson")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	// Seed an oversized active file just under threshold-trigger time.
	bigBlob := make([]byte, decisionsLogMaxBytes+10)
	for i := range bigBlob {
		bigBlob[i] = 'x'
	}
	if err := os.WriteFile(path, bigBlob, 0o600); err != nil {
		t.Fatal(err)
	}

	appendDecision(AutonomyDecision{ID: "post-rotate", Timestamp: time.Now().UTC()})

	// Active file should now be small (just the one new decision).
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > 1024 {
		t.Errorf("active file should be small post-rotate, got %d bytes", st.Size())
	}

	// Rotated archive should exist with the original payload.
	entries, _ := os.ReadDir(filepath.Dir(path))
	foundArchive := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "decisions.ndjson.") {
			foundArchive = true
			info, _ := e.Info()
			if info.Size() < int64(decisionsLogMaxBytes) {
				t.Errorf("rotated archive too small: %d bytes", info.Size())
			}
		}
	}
	if !foundArchive {
		t.Fatalf("rotated archive not created; entries: %v", entries)
	}
}

func TestAppendDecisionAndRead(t *testing.T) {
	withTempHome(t)
	d1 := AutonomyDecision{ID: "dec-1", Timestamp: time.Now().UTC(), Trigger: "interval",
		Actions: []AutonomyDecisionAction{{Kind: "allow_list", Applied: true}}}
	d2 := AutonomyDecision{ID: "dec-2", Timestamp: time.Now().UTC(), Trigger: "manual",
		Actions: []AutonomyDecisionAction{{Kind: "rule_override", Applied: false, SkippedReason: "rate"}}}
	appendDecision(d1)
	appendDecision(d2)
	got := ReadDecisions(10)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	// Newest first.
	if got[0].ID != "dec-2" || got[1].ID != "dec-1" {
		t.Errorf("ordering wrong: %v %v", got[0].ID, got[1].ID)
	}
}

func TestRecentDecisionsCount_OnlyAppliedWithinWindow(t *testing.T) {
	withTempHome(t)
	now := time.Now().UTC()
	old := AutonomyDecision{ID: "old", Timestamp: now.Add(-2 * time.Hour),
		Actions: []AutonomyDecisionAction{{Applied: true}}}
	recentApplied := AutonomyDecision{ID: "ra", Timestamp: now.Add(-10 * time.Minute),
		Actions: []AutonomyDecisionAction{{Applied: true}}}
	recentSkipped := AutonomyDecision{ID: "rs", Timestamp: now.Add(-5 * time.Minute),
		Actions: []AutonomyDecisionAction{{Applied: false, SkippedReason: "x"}}}
	appendDecision(old)
	appendDecision(recentApplied)
	appendDecision(recentSkipped)

	n := recentDecisionsCount(time.Hour)
	if n != 1 {
		t.Errorf("expected 1 (only recentApplied counts), got %d", n)
	}
}

func TestRunOneTick_FullPath_WithStubLLM(t *testing.T) {
	home := withTempHome(t)

	// Fresh audit pipeline pointing at the temp dirs.
	auditRoot := filepath.Join(home, "cicy-ai", "audit")
	workersRoot := filepath.Join(home, "cicy-ai", "workers")
	_ = os.MkdirAll(auditRoot, 0o700)
	_ = os.MkdirAll(workersRoot, 0o700)
	pol := DefaultPolicy()
	p, err := NewPipeline(auditRoot, workersRoot, NoopScanner{}, pol)
	if err != nil {
		t.Fatal(err)
	}
	prev := globalPipeline
	globalPipeline = p
	defer func() { globalPipeline = prev }()

	// Seed one synthetic event so EventsConsidered > 0.
	p.Submit(context.Background(), Envelope{
		AgentID:   "w-test",
		Provider:  "anthropic",
		Direction: DirectionOutbound,
		Payload:   []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	})
	p.Wait()

	// Stub LLM returns one safe allow_list addition.
	llmResp := `{"actions":[{"kind":"allow_list","rationale":"frequent internal traffic","patch":{"allow_list":{"paths":["/internal/seeded"]}}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": llmResp}},
			},
		})
	}))
	defer srv.Close()

	cfg := &AutonomyConfig{
		Enabled:           true,
		Interval:          JSONDuration(1 * time.Hour),
		Lookback:          JSONDuration(1 * time.Hour),
		MaxChangesPerHour: 10,
		MaxChangesPerTick: 5,
		LLM:               AutonomyLLM{Endpoint: srv.URL, Model: "stub", APIKey: "x"},
	}
	// Install into autonomyCfg so RunOneTickNow uses it.
	autonomyCfg = cfg
	defer func() { autonomyCfg = nil }()

	dec := RunOneTickNow(context.Background(), "manual")
	if dec.Error != "" {
		t.Fatalf("tick error: %s", dec.Error)
	}
	if dec.EventsConsidered < 1 {
		t.Errorf("EventsConsidered = %d", dec.EventsConsidered)
	}
	if len(dec.Actions) != 1 || !dec.Actions[0].Applied {
		t.Fatalf("expected 1 applied action, got %+v", dec.Actions)
	}

	// Verify policy.json on disk now contains the new allow_list path.
	polBody, err := os.ReadFile(DefaultPolicyPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(polBody), "/internal/seeded") {
		t.Errorf("policy.json missing applied path:\n%s", polBody)
	}

	// Decision was persisted with policy_hash transitions.
	persisted := ReadDecisions(10)
	if len(persisted) == 0 || persisted[0].PolicyHashAfter == "" {
		t.Errorf("decision not persisted with after-hash: %+v", persisted)
	}
}

func TestRunOneTick_RateLimitDropsTick(t *testing.T) {
	withTempHome(t)

	// Seed 5 already-applied recent decisions (= cap).
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		appendDecision(AutonomyDecision{
			ID:        "seed-" + string(rune('a'+i)),
			Timestamp: now.Add(-1 * time.Minute),
			Actions:   []AutonomyDecisionAction{{Applied: true}},
		})
	}

	cfg := &AutonomyConfig{
		Enabled:           true,
		Interval:          JSONDuration(time.Hour),
		Lookback:          JSONDuration(time.Hour),
		MaxChangesPerHour: 5,
		MaxChangesPerTick: 3,
		LLM:               AutonomyLLM{Endpoint: "http://unreachable", Model: "x"},
	}
	autonomyCfg = cfg
	defer func() { autonomyCfg = nil }()

	dec := RunOneTickNow(context.Background(), "interval")
	if !strings.Contains(dec.Error, "rate limited") {
		t.Errorf("expected rate-limit error, got: %q", dec.Error)
	}
}
