package audit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func TestGitAutoCommit_BootstrapsAndCommits(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git binary not in PATH")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	auditDir := filepath.Join(home, "cicy-ai", "audit")
	if err := os.MkdirAll(auditDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Seed a policy.json so commit has something to stage.
	if err := os.WriteFile(filepath.Join(auditDir, "policy.json"),
		[]byte(`{"version":1,"enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	GitAutoCommitDecision("autonomy test-1: applied 2 / proposed 2")

	// .git must exist after first call.
	if _, err := os.Stat(filepath.Join(auditDir, ".git")); err != nil {
		t.Fatalf("repo not bootstrapped: %v", err)
	}
	// Latest commit message must contain our msg.
	out, err := exec.Command("git", "-C", auditDir, "log", "-1", "--format=%s").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "test-1") {
		t.Errorf("commit msg = %q, want substring 'test-1'", string(out))
	}
}

func TestGitAutoCommit_NoOpWhenPolicyUnchanged(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git binary not in PATH")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	auditDir := filepath.Join(home, "cicy-ai", "audit")
	_ = os.MkdirAll(auditDir, 0o700)
	_ = os.WriteFile(filepath.Join(auditDir, "policy.json"), []byte(`{"v":1}`), 0o600)

	GitAutoCommitDecision("first")
	GitAutoCommitDecision("second-with-same-content")

	out, _ := exec.Command("git", "-C", auditDir, "log", "--oneline").CombinedOutput()
	lines := strings.Count(string(out), "\n")
	// Bootstrap (empty init commit) + 1 real commit = 2 lines max.
	// Should NOT be 3 — the second "no-op" call must skip.
	if lines > 2 {
		t.Errorf("expected ≤2 commits, got %d:\n%s", lines, out)
	}
}

func TestGitAutoCommit_CommitsAcrossMutations(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git binary not in PATH")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	auditDir := filepath.Join(home, "cicy-ai", "audit")
	_ = os.MkdirAll(auditDir, 0o700)

	for i, content := range []string{`{"v":1}`, `{"v":2}`, `{"v":3}`} {
		_ = os.WriteFile(filepath.Join(auditDir, "policy.json"), []byte(content), 0o600)
		GitAutoCommitDecision("change-" + string(rune('a'+i)))
	}

	out, _ := exec.Command("git", "-C", auditDir, "log", "--oneline").CombinedOutput()
	commits := strings.Count(string(out), "\n")
	// init + 3 changes = 4 commits.
	if commits != 4 {
		t.Errorf("expected 4 commits, got %d:\n%s", commits, out)
	}
	if !strings.Contains(string(out), "change-c") {
		t.Errorf("missing latest commit: %s", out)
	}
}
