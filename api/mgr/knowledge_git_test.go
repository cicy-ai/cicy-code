package main

import (
	"os/exec"
	"strings"
	"testing"
)

func gitKnowledge(t *testing.T, args ...string) string {
	t.Helper()
	full := append([]string{"-C", knowledgeRootDir(),
		"-c", "user.name=base", "-c", "user.email=base@cicy.local"}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitInitKnowledge turns the (temp) knowledge root into a git repo with a
// baseline commit, mirroring the real ~/cicy-ai/knowledge setup.
func gitInitKnowledge(t *testing.T) {
	t.Helper()
	if err := knowledgeEnsureRoot(); err != nil {
		t.Fatalf("ensure root: %v", err)
	}
	gitKnowledge(t, "init", "-q")
	gitKnowledge(t, "commit", "--allow-empty", "-q", "-m", "baseline")
}

func knowledgeCommitCount(t *testing.T) int {
	return strings.Count(gitKnowledge(t, "log", "--format=%h"), "\n") + 1
}

// Each governance action (add / promote / reject) and a UI edit produces exactly
// one commit, carrying the acting pane as git author, and is revertable.
func TestKnowledgeGitCommitPerAction(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	withTempCicyRoot(t)
	gitInitKnowledge(t)
	base := knowledgeCommitCount(t)

	// add → +1, authored by the source pane.
	id, err := insertKnowledge(knowledgeRow{Title: "Deploy note", Body: "run dev.py", SourcePane: "w-10001"})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if got := knowledgeCommitCount(t); got != base+1 {
		t.Fatalf("after add: commits=%d, want %d", got, base+1)
	}
	if subj := gitKnowledge(t, "log", "-1", "--format=%s"); !strings.Contains(subj, "add "+id) {
		t.Fatalf("add commit subject = %q", subj)
	}
	if author := gitKnowledge(t, "log", "-1", "--format=%an"); author != "w-10001" {
		t.Fatalf("add commit author = %q, want w-10001", author)
	}

	// promote → +1, into the ops domain, authored by the reviewing pane.
	if err := promoteKnowledge(id, "ops", "w-10131"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if got := knowledgeCommitCount(t); got != base+2 {
		t.Fatalf("after promote: commits=%d, want %d", got, base+2)
	}
	if subj := gitKnowledge(t, "log", "-1", "--format=%s"); !strings.Contains(subj, "promote "+id) || !strings.Contains(subj, "ops") {
		t.Fatalf("promote commit subject = %q", subj)
	}
	if author := gitKnowledge(t, "log", "-1", "--format=%an"); author != "w-10131" {
		t.Fatalf("promote commit author = %q, want w-10131", author)
	}

	// the promote commit is revertable (audit/rollback requirement).
	gitKnowledge(t, "revert", "--no-edit", "HEAD")
	if got := knowledgeCommitCount(t); got != base+3 {
		t.Fatalf("after revert: commits=%d, want %d", got, base+3)
	}
}

// A no-op (no file change) does not create an empty commit, and a non-git root
// is skipped gracefully.
func TestKnowledgeGitNoopAndNonRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	withTempCicyRoot(t)

	// Non-repo: commit is a graceful no-op (must not panic / create anything).
	if err := knowledgeEnsureRoot(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	knowledgeGitCommit("knowledge: noop", "w-1") // no .git → skip

	gitInitKnowledge(t)
	base := knowledgeCommitCount(t)
	knowledgeGitCommit("knowledge: still noop (nothing staged)", "w-1")
	if got := knowledgeCommitCount(t); got != base {
		t.Fatalf("no-op produced a commit: %d → %d", base, got)
	}
}
