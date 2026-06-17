package main

import (
	"os"
	"os/exec"
	"path/filepath"
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

// gitInitKnowledge sets up the (temp) knowledge root as a git repo. ensureRoot
// itself git-inits + makes the baseline "知识库初始化" commit on a fresh store.
func gitInitKnowledge(t *testing.T) {
	t.Helper()
	if err := knowledgeEnsureRoot(); err != nil {
		t.Fatalf("ensure root: %v", err)
	}
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

// P8a: a fresh install auto-git-inits the knowledge store — seeding the embedded
// README + .gitignore and a "知识库初始化" commit — and is idempotent (a second
// ensure neither re-commits nor clobbers user content).
func TestKnowledgeEnsureGitRepoFreshInstall(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	withTempCicyRoot(t)

	if err := knowledgeEnsureRoot(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !fileExists(filepath.Join(knowledgeRootDir(), ".git")) {
		t.Fatalf("fresh install did not git-init the store")
	}
	if !fileExists(filepath.Join(knowledgeRootDir(), "README.md")) || !fileExists(filepath.Join(knowledgeRootDir(), ".gitignore")) {
		t.Fatalf("README/.gitignore not seeded")
	}
	if c := knowledgeCommitCount(t); c != 1 {
		t.Fatalf("init commits = %d, want 1", c)
	}
	if subj := gitKnowledge(t, "log", "-1", "--format=%s"); subj != "知识库初始化" {
		t.Fatalf("init commit subject = %q, want 知识库初始化", subj)
	}

	// Idempotent: edit + commit, then a second ensure must NOT add a commit nor
	// clobber the user's README.
	custom := "# my edited knowledge readme\n"
	if err := os.WriteFile(filepath.Join(knowledgeRootDir(), "README.md"), []byte(custom), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitKnowledge(t, "commit", "-q", "-am", "user edit")
	base := knowledgeCommitCount(t)
	if err := knowledgeEnsureRoot(); err != nil {
		t.Fatalf("ensure#2: %v", err)
	}
	if c := knowledgeCommitCount(t); c != base {
		t.Fatalf("second ensure was not a no-op: %d → %d", base, c)
	}
	if got, _ := os.ReadFile(filepath.Join(knowledgeRootDir(), "README.md")); string(got) != custom {
		t.Fatalf("second ensure clobbered the user README")
	}
}

// A non-git knowledge root: knowledgeGitCommit is a graceful no-op (never panics,
// never creates a repo).
func TestKnowledgeGitNonRepoSkip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	withTempCicyRoot(t)
	if err := os.MkdirAll(knowledgeRootDir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	knowledgeGitCommit("knowledge: x (by w-1)", "w-1") // no .git → skip
	if fileExists(filepath.Join(knowledgeRootDir(), ".git")) {
		t.Fatalf("knowledgeGitCommit must not create a repo")
	}
}
