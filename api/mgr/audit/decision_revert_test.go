package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRevertDecision_RoundTrip(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git binary not in PATH")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	auditDir := filepath.Join(home, "cicy-ai", "audit")
	if err := os.MkdirAll(auditDir, 0o700); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(auditDir, "policy.json")

	// 1. Initial policy + first commit (simulating autonomy applying an action).
	if err := os.WriteFile(policyPath, []byte(`{"v":1,"allow_list":{"paths":["/old"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sha1 := GitAutoCommitDecisionReturningSHA("autonomy dec-1: initial allow_list /old")
	if sha1 == "" {
		t.Fatal("first commit returned empty SHA")
	}

	// 2. Second policy change + commit — this is what we'll revert.
	if err := os.WriteFile(policyPath, []byte(`{"v":2,"allow_list":{"paths":["/old","/risky"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sha2 := GitAutoCommitDecisionReturningSHA("autonomy dec-2: added /risky path")
	if sha2 == "" || sha2 == sha1 {
		t.Fatalf("second commit unexpected: %s vs %s", sha2, sha1)
	}

	// 3. Seed a matching decision in decisions.ndjson so RevertDecision finds it.
	appendDecision(AutonomyDecision{
		ID:               "dec-risky-added",
		Timestamp:        time.Now().UTC(),
		Trigger:          "interval",
		Actions:          []AutonomyDecisionAction{{Kind: "allow_list", Applied: true}},
		PolicyHashBefore: "sha256:before",
		PolicyHashAfter:  "sha256:after",
		GitSHA:           sha2,
	})

	// 4. Call revert.
	result, err := RevertDecision("dec-risky-added")
	if err != nil {
		t.Fatalf("RevertDecision: %v", err)
	}
	if result.OriginalGitSHA != sha2 || result.NewGitSHA == "" || result.NewGitSHA == sha2 {
		t.Fatalf("unexpected revert result: %+v", result)
	}

	// 5. policy.json should now be back to {"v":1,"allow_list":{"paths":["/old"]}}.
	body, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "/risky") {
		t.Errorf("revert did not remove /risky: %s", body)
	}
	if !strings.Contains(string(body), "/old") {
		t.Errorf("revert clobbered /old: %s", body)
	}

	// 6. New revert decision is in the log with trigger="revert".
	all := ReadDecisions(10)
	found := false
	for _, d := range all {
		if d.ID == result.NewDecisionID && d.Trigger == "revert" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("revert decision not persisted: %+v", all)
	}
}

func TestRevertDecision_ErrorsForMissingOrNoSHA(t *testing.T) {
	withTempHome(t)
	// 1. Not found.
	if _, err := RevertDecision("does-not-exist"); err == nil {
		t.Error("expected error for missing decision")
	}
	// 2. Decision with no GitSHA (e.g. rate-limited tick).
	appendDecision(AutonomyDecision{
		ID:        "dec-empty",
		Timestamp: time.Now().UTC(),
		// No GitSHA — nothing to revert.
	})
	if _, err := RevertDecision("dec-empty"); err == nil {
		t.Error("expected error for decision with no GitSHA")
	} else if !strings.Contains(err.Error(), "no git_sha") {
		t.Errorf("error should mention no git_sha, got %v", err)
	}
}
