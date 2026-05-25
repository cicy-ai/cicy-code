package audit

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestAgentOverride_LoadSaveRoundTrip(t *testing.T) {
	root := t.TempDir()
	workers := filepath.Join(root, "workers")

	ov := &AgentOverride{
		RulesOverride: []RuleOverride{{ID: "pii.phone_cn", Disabled: true}},
		AllowList: AgentAllowListSubset{
			Paths:         []string{"mitm:flow-x"},
			ContentHashes: []string{"sha256:abc"},
		},
	}
	if err := SaveAgentOverride(workers, "w-x", ov); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := LoadAgentOverride(workers, "w-x")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil {
		t.Fatal("load returned nil")
	}
	if !reflect.DeepEqual(got.RulesOverride, ov.RulesOverride) {
		t.Errorf("rules_override mismatch:\n  got:  %+v\n  want: %+v", got.RulesOverride, ov.RulesOverride)
	}
	if !reflect.DeepEqual(got.AllowList, ov.AllowList) {
		t.Errorf("allow_list mismatch:\n  got:  %+v\n  want: %+v", got.AllowList, ov.AllowList)
	}
}

func TestAgentOverride_EmptyDeletesFile(t *testing.T) {
	root := t.TempDir()
	workers := filepath.Join(root, "workers")

	// First save some content.
	if err := SaveAgentOverride(workers, "w-x", &AgentOverride{
		AllowList: AgentAllowListSubset{ContentHashes: []string{"sha256:abc"}},
	}); err != nil {
		t.Fatal(err)
	}
	path := agentOverridePath(workers, "w-x")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("file missing after save")
	}

	// Save empty → file deleted.
	if err := SaveAgentOverride(workers, "w-x", &AgentOverride{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("empty save should delete file, got err=%v", err)
	}
}

func TestAgentOverride_MissingFileReturnsNil(t *testing.T) {
	root := t.TempDir()
	workers := filepath.Join(root, "workers")
	got, err := LoadAgentOverride(workers, "no-such-agent")
	if err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
	if got != nil {
		t.Errorf("missing file should return nil, got %+v", got)
	}
}

func TestAgentOverride_ValidationRejectsUnknownRule(t *testing.T) {
	root := t.TempDir()
	workers := filepath.Join(root, "workers")
	ov := &AgentOverride{
		RulesOverride: []RuleOverride{{ID: "made.up.rule", Disabled: true}},
	}
	if err := SaveAgentOverride(workers, "w-x", ov); err == nil {
		t.Error("expected validation error on unknown rule id")
	}
}

func TestMergeIntoEffective_NilOverride(t *testing.T) {
	g := DefaultPolicy()
	g.AllowList.Paths = []string{"global-path"}
	out := MergeIntoEffective(g, nil)
	if out == g {
		t.Error("must return a fresh policy, not the same pointer")
	}
	if out.AllowList.Paths[0] != "global-path" {
		t.Errorf("global paths lost: %+v", out.AllowList.Paths)
	}
}

func TestMergeIntoEffective_RulesOverrideAgentWinsOnSameID(t *testing.T) {
	g := DefaultPolicy()
	g.RulesOverride = []RuleOverride{
		{ID: "pii.phone_cn", Severity: SeverityMedium},
		{ID: "secret.jwt", Disabled: true},
	}
	ov := &AgentOverride{
		RulesOverride: []RuleOverride{
			{ID: "pii.phone_cn", Severity: SeverityHigh}, // override the medium
		},
	}
	out := MergeIntoEffective(g, ov)

	byID := map[string]RuleOverride{}
	for _, r := range out.RulesOverride {
		byID[r.ID] = r
	}
	if got := byID["pii.phone_cn"].Severity; got != SeverityHigh {
		t.Errorf("agent should win, got severity=%s want high", got)
	}
	if got, ok := byID["secret.jwt"]; !ok || !got.Disabled {
		t.Errorf("global secret.jwt should be preserved, got %+v ok=%v", got, ok)
	}
}

func TestMergeIntoEffective_AllowListUnionDedup(t *testing.T) {
	g := DefaultPolicy()
	g.AllowList = AllowList{
		Paths:         []string{"global-a", "shared-b"},
		ContentHashes: []string{"sha256:g1"},
		Agents:        []string{"global-agent"},
	}
	ov := &AgentOverride{
		AllowList: AgentAllowListSubset{
			Paths:         []string{"shared-b", "agent-c"},
			ContentHashes: []string{"sha256:a1"},
		},
	}
	out := MergeIntoEffective(g, ov)

	wantPaths := []string{"agent-c", "global-a", "shared-b"}
	got := append([]string(nil), out.AllowList.Paths...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, wantPaths) {
		t.Errorf("paths union wrong:\n  got:  %v\n  want: %v", got, wantPaths)
	}

	wantHashes := []string{"sha256:a1", "sha256:g1"}
	gotH := append([]string(nil), out.AllowList.ContentHashes...)
	sort.Strings(gotH)
	if !reflect.DeepEqual(gotH, wantHashes) {
		t.Errorf("content_hashes union wrong: %v", gotH)
	}

	// agents stays exactly as global.
	if !reflect.DeepEqual(out.AllowList.Agents, []string{"global-agent"}) {
		t.Errorf("agents should be global-only, got %v", out.AllowList.Agents)
	}
}

func TestMergeIntoEffective_ResponsiblePersonsTierReplace(t *testing.T) {
	g := DefaultPolicy()
	g.ResponsiblePersons = ResponsiblePersonsConfig{
		Default:    []string{"global-default@x"},
		BySeverity: map[string][]string{"critical": {"global-ciso@x"}},
		ByRule:     map[string][]string{"secret.aws_akid": {"global-devops@x"}},
	}
	ov := &AgentOverride{
		ResponsiblePersons: &ResponsiblePersonsConfig{
			Default: []string{"agent-on-call@x"},
			ByRule:  map[string][]string{"secret.aws_akid": {"agent-team@x"}},
			// BySeverity NOT set — should fall through to global.
		},
	}
	out := MergeIntoEffective(g, ov)

	if got := out.ResponsiblePersons.Default; !reflect.DeepEqual(got, []string{"agent-on-call@x"}) {
		t.Errorf("default override failed: %v", got)
	}
	if got := out.ResponsiblePersons.ByRule["secret.aws_akid"]; !reflect.DeepEqual(got, []string{"agent-team@x"}) {
		t.Errorf("by_rule override failed: %v", got)
	}
	if got := out.ResponsiblePersons.BySeverity["critical"]; !reflect.DeepEqual(got, []string{"global-ciso@x"}) {
		t.Errorf("by_severity should fall through to global: %v", got)
	}
}

func TestApplyRulesOverrideToFindings_Disable(t *testing.T) {
	in := []Finding{
		{RuleID: "pii.phone_cn", Severity: SeverityLow},
		{RuleID: "secret.aws_akid", Severity: SeverityHigh},
	}
	out := ApplyRulesOverrideToFindings(in, []RuleOverride{
		{ID: "pii.phone_cn", Disabled: true},
	})
	if len(out) != 1 || out[0].RuleID != "secret.aws_akid" {
		t.Errorf("disable should drop pii.phone_cn, got %+v", out)
	}
}

func TestApplyRulesOverrideToFindings_SeverityRegrade(t *testing.T) {
	in := []Finding{{RuleID: "pii.phone_cn", Severity: SeverityLow}}
	out := ApplyRulesOverrideToFindings(in, []RuleOverride{
		{ID: "pii.phone_cn", Severity: SeverityHigh},
	})
	if len(out) != 1 || out[0].Severity != SeverityHigh {
		t.Errorf("severity not re-graded: %+v", out)
	}
}

// End-to-end: agent override applies on top of global without touching it.
func TestPipeline_AgentOverride_DisablesRuleForOneAgent(t *testing.T) {
	pol := DefaultPolicy()
	p, workersRoot := preventiveFixture(t, pol)

	// Save per-agent override disabling pii.phone_cn for w-quiet only.
	ov := &AgentOverride{RulesOverride: []RuleOverride{{ID: "pii.phone_cn", Disabled: true}}}
	if err := SaveAgentOverride(workersRoot, "w-quiet", ov); err != nil {
		t.Fatal(err)
	}

	// Same payload triggers phone rule normally for w-loud.
	res := p.scanner.Scan([]byte("call 13800138000"), DirectionOutbound, p.CurrentPolicy())
	hasPhone := false
	for _, f := range res {
		if f.RuleID == "pii.phone_cn" {
			hasPhone = true
		}
	}
	if !hasPhone {
		t.Fatal("baseline: phone rule should fire")
	}

	// Now simulate the pipeline's effective merge for w-quiet.
	merged := MergeIntoEffective(p.CurrentPolicy(), ov)
	filtered := ApplyRulesOverrideToFindings(res, merged.RulesOverride)
	for _, f := range filtered {
		if f.RuleID == "pii.phone_cn" {
			t.Errorf("phone rule should have been disabled for w-quiet, got %+v", f)
		}
	}
}
