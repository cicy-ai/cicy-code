package main

import (
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Baked-in seed docs for a fresh knowledge store (content tracks the live
// ~/cicy-ai/knowledge/{README.md,.gitignore}). Written only when the store is
// first git-initialized, never overwriting user content.
//
//go:embed embed/knowledge/README.md
var knowledgeReadmeEmbed string

//go:embed embed/knowledge/gitignore
var knowledgeGitignoreEmbed string

// The team knowledge store (~/cicy-ai/knowledge) is a git repo: every governance
// action (add / promote / reject / supersede) and every UI edit is auto-committed
// so the history is auditable and any change can be `git revert`-ed. docs/ is
// gitignored, so uploaded enterprise documents do not produce commits.

var knowledgeGitMu sync.Mutex

// knowledgeEnsureGitRepo makes a fresh knowledge store self-contained on a brand
// new install: if knowledgeRootDir is not yet a git repo, it seeds the embedded
// README.md + .gitignore (only when absent — never clobbering user files),
// `git init`s, and records the first commit. Idempotent: an existing repo is a
// fast no-op (one stat). Best-effort — git absence / errors just leave the store
// ungit'd (it still works), so this never blocks startup or writes.
func knowledgeEnsureGitRepo() {
	dir := knowledgeRootDir()
	knowledgeGitMu.Lock()
	defer knowledgeGitMu.Unlock()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return // already a git repo
	}
	// Seed the README / .gitignore only when missing.
	if readme := filepath.Join(dir, "README.md"); !fileExists(readme) {
		_ = os.WriteFile(readme, []byte(knowledgeReadmeEmbed), 0o644)
	}
	if gi := filepath.Join(dir, ".gitignore"); !fileExists(gi) {
		_ = os.WriteFile(gi, []byte(knowledgeGitignoreEmbed), 0o644)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		log.Printf("[knowledge-git] init failed: %v: %s", err, strings.TrimSpace(string(out)))
		return
	}
	if err := exec.Command("git", "-C", dir, "add", "-A").Run(); err != nil {
		log.Printf("[knowledge-git] init add failed: %v", err)
		return
	}
	if exec.Command("git", "-C", dir, "diff", "--cached", "--quiet").Run() == nil {
		return // nothing to commit (empty store)
	}
	cmd := exec.Command("git", "-C", dir,
		"-c", "user.name=cicy", "-c", "user.email=cicy@cicy-agent.local",
		"commit", "-q", "-m", "知识库初始化", "--author", "cicy <cicy@cicy-agent.local>")
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[knowledge-git] initial commit failed: %v: %s", err, strings.TrimSpace(string(out)))
		return
	}
	log.Printf("[knowledge-git] initialized knowledge store git repo at %s", dir)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// knowledgeGitCommit stages all changes under the knowledge root and, if there
// is anything to commit, records one commit authored by the caller (an agent
// pane or a human). It is a no-op when there are no changes, and gracefully
// skips when the root is not a git repo. Serialized by knowledgeGitMu (the
// /api/knowledge mutations are already serialized, this guards concurrent
// fs writes + the memory hook).
func knowledgeGitCommit(message, author string) {
	dir := knowledgeRootDir()
	knowledgeGitMu.Lock()
	defer knowledgeGitMu.Unlock()

	// Not a git repo → skip silently (the store still works without git).
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "true" {
		return
	}
	if err := exec.Command("git", "-C", dir, "add", "-A").Run(); err != nil {
		log.Printf("[knowledge-git] add failed: %v", err)
		return
	}
	// `git diff --cached --quiet` exits 0 when nothing is staged → no-op.
	if exec.Command("git", "-C", dir, "diff", "--cached", "--quiet").Run() == nil {
		return
	}

	name := knowledgeGitAuthor(author)
	email := name + "@cicy-agent.local"
	// Set committer identity inline so the commit succeeds even when the repo
	// has no configured user; --author carries the originating pane/person.
	cmd := exec.Command("git", "-C", dir,
		"-c", "user.name="+name, "-c", "user.email="+email,
		"commit", "-m", message, "--author", name+" <"+email+">")
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[knowledge-git] commit failed: %v: %s", err, strings.TrimSpace(string(out)))
		return
	}
	log.Printf("[knowledge-git] %s", message)
}

// knowledgeGitAuthor normalizes a caller id into a git-author-safe name.
func knowledgeGitAuthor(author string) string {
	a := shortPaneID(strings.TrimSpace(author))
	a = strings.Map(func(r rune) rune {
		if r == '<' || r == '>' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, a)
	a = strings.TrimSpace(a)
	if a == "" {
		return "cicy"
	}
	return a
}

// knowledgeMaybeCommitFsWrite auto-commits a successful /api/fs mutation when it
// targeted the knowledge fs root. Author is the requesting agent (or "ui").
func knowledgeMaybeCommitFsWrite(r *http.Request, op, relPath string) {
	if r.URL.Query().Get("root") != "knowledge" {
		return
	}
	author := firstNonEmpty(
		strings.TrimSpace(r.Header.Get("X-Agent-Show-Id")),
		strings.TrimSpace(r.URL.Query().Get("agent_id")),
		"ui",
	)
	rel := strings.TrimSpace(relPath)
	knowledgeGitCommit(fmt.Sprintf("knowledge: %s %s (by %s)", op, rel, knowledgeGitAuthor(author)), author)
}
