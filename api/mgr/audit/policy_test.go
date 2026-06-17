package audit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── P2-T7 AddToAllowList ──────────────────────────────────────────────

func TestAddToAllowList_NewValue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := AddToAllowList(AllowCategoryContentHash, "sha256:abc", "fp from operator")
	if err != nil {
		t.Fatalf("AddToAllowList: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path on first write")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "sha256:abc") {
		t.Errorf("policy.json missing added hash: %s", data)
	}
}

func TestAddToAllowList_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := AddToAllowList(AllowCategoryContentHash, "sha256:xyz", "")
	if err != nil {
		t.Fatal(err)
	}
	// Second call should return "" path (idempotent no-op) and no error.
	path2, err := AddToAllowList(AllowCategoryContentHash, "sha256:xyz", "")
	if err != nil {
		t.Fatal(err)
	}
	if path2 != "" {
		t.Errorf("duplicate add should be no-op, got path=%q", path2)
	}
}

func TestAddToAllowList_UnknownCategory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := AddToAllowList("bogus", "x", "")
	if err == nil || !strings.Contains(err.Error(), "unknown allow_list category") {
		t.Errorf("expected unknown category error, got %v", err)
	}
}

func TestAddToAllowList_PreservesOtherFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Pre-seed policy.json with unrelated fields.
	policyPath := DefaultPolicyPath()
	_ = os.MkdirAll(filepath.Dir(policyPath), 0o700)
	seed := `{
        "version": 1,
        "preventive": {"enabled": true},
        "responsible_persons": {"default": ["a@b"]},
        "custom_rules": [{"id":"custom.x","severity":"low","scan_directions":["outbound"],"match":{"type":"regex","pattern":"x"}}]
    }`
	_ = os.WriteFile(policyPath, []byte(seed), 0o600)

	if _, err := AddToAllowList(AllowCategoryContentHash, "sha256:new", ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(policyPath)
	s := string(data)
	for _, want := range []string{
		`"preventive"`, `"responsible_persons"`, `"custom_rules"`, `"sha256:new"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestPolicy_LoadMissingFile(t *testing.T) {
	p, err := LoadPolicy(filepath.Join(t.TempDir(), "no-such.json"))
	if err != nil {
		t.Fatalf("LoadPolicy missing file should be ok: %v", err)
	}
	if p == nil || p.Hash != "sha256:DEFAULT" || p.FailMode != "open" {
		t.Errorf("default policy wrong: %+v", p)
	}
}

func TestPolicy_LoadValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	_ = os.WriteFile(path, []byte(`{
        "version": 1,
        "enabled": true,
        "fail_mode": "open",
        "rules_override": [{"id": "secret.jwt", "severity": "high"}],
        "custom_rules": [{
            "id": "custom.demo",
            "category": "corp",
            "severity": "medium",
            "scan_directions": ["outbound"],
            "default_action": "log",
            "match": {"type": "regex", "pattern": "DEMO-\\d{4}"}
        }],
        "allow_list": {"agents": ["w-allow"], "paths": [], "content_hashes": []}
    }`), 0o600)
	p, err := LoadPolicy(path)
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	if !strings.HasPrefix(p.Hash, "sha256:") || p.Hash == "sha256:DEFAULT" {
		t.Errorf("expected real hash, got %q", p.Hash)
	}
	if len(p.RulesOverride) != 1 || len(p.CustomRules) != 1 {
		t.Errorf("override/custom counts wrong: %+v", p)
	}
	if len(p.AllowList.Agents) != 1 || p.AllowList.Agents[0] != "w-allow" {
		t.Errorf("allow agent missing: %+v", p.AllowList)
	}
}

func TestPolicy_ValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantSub string
	}{
		{"unknown_override",
			`{"rules_override":[{"id":"does.not.exist","severity":"high"}]}`,
			"unknown builtin rule id"},
		{"bad_severity",
			`{"rules_override":[{"id":"secret.jwt","severity":"plaid"}]}`,
			"invalid severity"},
		{"custom_id_no_prefix",
			`{"custom_rules":[{"id":"corp.x","severity":"low","scan_directions":["outbound"],"match":{"type":"regex","pattern":"x"}}]}`,
			"must start with \"custom.\""},
		{"custom_bad_regex",
			`{"custom_rules":[{"id":"custom.x","severity":"low","scan_directions":["outbound"],"match":{"type":"regex","pattern":"["}}]}`,
			"regex compile"},
		{"custom_unknown_match_type",
			`{"custom_rules":[{"id":"custom.x","severity":"low","scan_directions":["outbound"],"match":{"type":"bogus"}}]}`,
			"unknown match.type"},
		// (custom_no_direction removed: per-rule scan_directions config + its
		// "must list at least one" validation were dropped — rules scan both.)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "policy.json")
			_ = os.WriteFile(path, []byte(tc.body), 0o600)
			_, err := LoadPolicy(path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// setupPipelineWithPolicy spins up an isolated pipeline + applies the given policy.
func setupPipelineWithPolicy(t *testing.T, policy *Policy) (*Pipeline, string) {
	t.Helper()
	root := t.TempDir()
	auditRoot := filepath.Join(root, "audit")
	workersRoot := filepath.Join(root, "workers")
	scanner := NewBuiltinScanner()
	p, err := NewPipeline(auditRoot, workersRoot, scanner, policy)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	return p, workersRoot
}

const bearerPayload = "Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123"

func TestPipeline_RuleOverride_Disabled(t *testing.T) {
	p, _ := setupPipelineWithPolicy(t, &Policy{
		Hash:    "sha256:test",
		RulesOverride: []RuleOverride{
			{ID: "secret.bearer_token", Disabled: true},
		},
	})
	findings := p.scanner.Scan([]byte(bearerPayload), DirectionOutbound, p.CurrentPolicy())
	for _, f := range findings {
		if f.RuleID == "secret.bearer_token" {
			t.Errorf("secret.bearer_token should be disabled, got: %+v", f)
		}
	}
}

func TestPipeline_RuleOverride_SeverityChange(t *testing.T) {
	p, _ := setupPipelineWithPolicy(t, &Policy{
		Hash:    "sha256:test",
		RulesOverride: []RuleOverride{
			{ID: "secret.bearer_token", Severity: SeverityHigh},
		},
	})
	findings := p.scanner.Scan([]byte(bearerPayload), DirectionOutbound, p.CurrentPolicy())
	var hit *Finding
	for i := range findings {
		if findings[i].RuleID == "secret.bearer_token" {
			hit = &findings[i]
		}
	}
	if hit == nil {
		t.Fatal("bearer_token rule didn't fire")
	}
	if hit.Severity != SeverityHigh {
		t.Errorf("override severity not applied: got %s want high", hit.Severity)
	}
}

func TestPipeline_CustomRule_Regex(t *testing.T) {
	p, _ := setupPipelineWithPolicy(t, &Policy{
		Hash:    "sha256:test",
		CustomRules: []CustomRule{{
			ID:             "custom.demo",
			Category:       "corp",
			Severity:       SeverityMedium,
			ScanDirections: []string{DirectionOutbound},
			Match:          RuleMatch{Type: "regex", Pattern: `DEMO-\d{4}`},
		}},
	})
	findings := p.scanner.Scan([]byte("hello DEMO-1234 world"), DirectionOutbound, p.CurrentPolicy())
	found := false
	for _, f := range findings {
		if f.RuleID == "custom.demo" && f.Severity == SeverityMedium && f.Category == "corp" {
			found = true
		}
	}
	if !found {
		t.Errorf("custom regex rule didn't fire on DEMO-1234, got: %+v", findings)
	}
	// (per-rule direction gating removed: a rule now scans both directions, so
	// the former "outbound-only" negative assertion no longer applies.)
}

func TestPipeline_CustomRule_DictFile(t *testing.T) {
	dir := t.TempDir()
	dictPath := filepath.Join(dir, "customers.txt")
	_ = os.WriteFile(dictPath, []byte("# top customers\nAcme Corp\nBeta Industries\n\nCharlie LLC\n"), 0o600)

	p, _ := setupPipelineWithPolicy(t, &Policy{
		Hash:    "sha256:test",
		CustomRules: []CustomRule{{
			ID:             "custom.customers",
			Severity:       SeverityMedium,
			ScanDirections: []string{DirectionOutbound, DirectionInbound},
			Match:          RuleMatch{Type: "dict_file", Path: dictPath},
		}},
	})
	findings := p.scanner.Scan([]byte("please reach out to Acme Corp about the deal"), DirectionOutbound, p.CurrentPolicy())
	var hit *Finding
	for i := range findings {
		if findings[i].RuleID == "custom.customers" {
			hit = &findings[i]
		}
	}
	if hit == nil {
		t.Fatal("dict_file rule didn't fire on Acme Corp")
	}
	if hit.MatchCount != 1 {
		t.Errorf("match_count=%d want 1", hit.MatchCount)
	}
}

func TestPolicy_AllowList_Agent(t *testing.T) {
	pol := &Policy{
		Hash:    "sha256:test",
		AllowList: AllowList{
			Agents: []string{"w-allow"},
		},
	}
	if d := pol.CheckAllowList("w-other", "current.json#t", "sha256:abc"); d.Suppressed {
		t.Error("w-other should not be suppressed")
	}
	if d := pol.CheckAllowList("w-allow", "x", "y"); !d.Suppressed || d.Reason != "agent" {
		t.Errorf("w-allow should be suppressed by agent, got %+v", d)
	}
}

func TestPolicy_AllowList_PathPrefix(t *testing.T) {
	pol := &Policy{
		Hash:    "sha256:test",
		AllowList: AllowList{
			Paths: []string{"mitm:flow-known-false-positive-"},
		},
	}
	d := pol.CheckAllowList("a", "mitm:flow-known-false-positive-abc123", "")
	if !d.Suppressed || d.Reason != "path" {
		t.Errorf("path prefix should match, got %+v", d)
	}
}

func TestPolicy_AllowList_ContentHash(t *testing.T) {
	pol := &Policy{
		Hash:    "sha256:test",
		AllowList: AllowList{
			ContentHashes: []string{"sha256:knownbenign"},
		},
	}
	d := pol.CheckAllowList("a", "x", "sha256:knownbenign")
	if !d.Suppressed || d.Reason != "content_hash" {
		t.Errorf("content hash should match, got %+v", d)
	}
}

func TestPipeline_Allowlist_EventStillRecorded(t *testing.T) {
	pol := &Policy{
		Hash: "sha256:test",
		AllowList: AllowList{
			Agents: []string{"w-eve"},
		},
	}
	p, workersRoot := setupPipelineWithPolicy(t, pol)

	// Payload that would normally trigger 3 rules.
	p.Submit(context.Background(), Envelope{
		AgentID:       "w-eve",
		SourceChannel: SourceGateway,
		Direction:     DirectionOutbound,
		Payload:       []byte("AKIAIOSFODNN7EXAMPLE and phone 13800138000 and 192.168.1.1"),
		PayloadRef:    "current.json#t1",
		Inline:        true,
	})
	p.Wait()

	ndjson := filepath.Join(workersRoot, "w-eve", ".cicy", "history", "audit.ndjson")
	data, err := os.ReadFile(ndjson)
	if err != nil {
		t.Fatalf("expected event still written for allowlisted agent, but file missing: %v", err)
	}
	var e Event
	if err := json.Unmarshal(data[:len(data)-1], &e); err != nil {
		t.Fatalf("parse event: %v", err)
	}
	if len(e.Findings) != 0 {
		t.Errorf("allowlisted findings should be empty, got %d", len(e.Findings))
	}
	if e.Meta.AllowlistedBy != "agent" || e.Meta.AllowlistMatch != "w-eve" {
		t.Errorf("expected allowlisted_by=agent match=w-eve, got %+v", e.Meta)
	}
}

func TestPipeline_HotReload(t *testing.T) {
	root := t.TempDir()
	auditRoot := filepath.Join(root, "audit")
	workersRoot := filepath.Join(root, "workers")
	if err := os.MkdirAll(auditRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(auditRoot, "policy.json")

	scanner := NewBuiltinScanner()
	p, err := NewPipeline(auditRoot, workersRoot, scanner, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.WatchPolicyFile(policyPath); err != nil {
		t.Fatal(err)
	}

	// Initially: builtin secret.bearer_token at its default severity (medium).
	got := p.scanner.Scan([]byte(bearerPayload), DirectionOutbound, p.CurrentPolicy())
	if len(got) == 0 || got[0].Severity != SeverityMedium {
		t.Fatalf("initial severity expected medium, got %+v", got)
	}

	// Write a policy that bumps bearer_token to high.
	body := `{"enabled":true,"rules_override":[{"id":"secret.bearer_token","severity":"high"}]}`
	if err := os.WriteFile(policyPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// fsnotify + 200ms debounce: poll for up to 3 seconds.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f := p.scanner.Scan([]byte(bearerPayload), DirectionOutbound, p.CurrentPolicy())
		if len(f) > 0 && f[0].Severity == SeverityHigh {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("expected severity to become high within 3s after policy write; current = %+v",
		p.scanner.Scan([]byte(bearerPayload), DirectionOutbound, p.CurrentPolicy()))
}
