package audit

// policy_git — automatic git commits over ~/cicy-ai/audit/ so every
// autonomous policy mutation is traceable + reversible.
//
// Each successful WriteGlobalPolicy fires `gitAutoCommit` which:
//   1. Initializes ~/cicy-ai/audit/.git if missing (one-time bootstrap).
//   2. Stages policy.json + the decision file that triggered the change.
//   3. Commits with the decision ID + agent rationale as the message.
//
// All git operations are best-effort: a missing `git` binary or a
// permission problem logs a WARN but does not block the policy write.
// The policy is canonical on disk; git is just a free safety net.
//
// Rollback path: `cd ~/cicy-ai/audit && git revert <sha>` then the
// autonomy fsnotify watcher reloads within 200ms.

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	gitMu       sync.Mutex
	gitDisabled bool // flipped to true on first detection of missing `git`
)

// GitAutoCommitDecision is called by the autonomy tick AFTER applying a
// decision's patches. msg is a short reason string (decision ID + summary).
// Safe to call with an empty msg or from any goroutine — internally
// serialized under gitMu.
//
// Returns the new HEAD SHA on success ("" if no commit was made / git
// disabled). Callers should store the SHA on the decision record so a
// later revert can target the right commit.
func GitAutoCommitDecisionReturningSHA(commitMsg string) string {
	GitAutoCommitDecision(commitMsg)
	repo := auditRepoDir()
	if repo == "" {
		return ""
	}
	sha, err := gitOutput(repo, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(sha)
}

// GitRevertCommit runs `git revert --no-edit <sha>` in the audit repo.
// Used by the /api/audit/decisions/revert/<id> endpoint. Returns the
// new HEAD SHA on success.
func GitRevertCommit(targetSHA string) (string, error) {
	if gitDisabled {
		return "", fmt.Errorf("git auto-commit is disabled (git binary missing)")
	}
	if targetSHA == "" {
		return "", fmt.Errorf("empty target sha")
	}
	gitMu.Lock()
	defer gitMu.Unlock()
	repo := auditRepoDir()
	if repo == "" {
		return "", fmt.Errorf("cannot resolve audit repo dir")
	}
	if !isGitRepo(repo) {
		return "", fmt.Errorf("audit dir is not a git repo (no policy mutations to revert)")
	}
	if err := runGit(repo, "revert", "--no-edit", targetSHA); err != nil {
		return "", err
	}
	sha, err := gitOutput(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

// gitOutput runs git and returns stdout. Used for query-style ops
// (rev-parse, log, etc.) that need the output, not just status.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

func GitAutoCommitDecision(commitMsg string) {
	if gitDisabled {
		return
	}
	gitMu.Lock()
	defer gitMu.Unlock()

	repoDir := auditRepoDir()
	if repoDir == "" {
		return
	}

	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		log.Printf("[autonomy git] mkdir: %v", err)
		return
	}

	// Bootstrap on first call: git init + initial commit so future
	// `git add` works even if policy.json is the first tracked file.
	if !isGitRepo(repoDir) {
		if err := runGit(repoDir, "init", "-q", "-b", "main"); err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				gitDisabled = true
				log.Printf("[autonomy git] git binary not found — disabling auto-commit")
				return
			}
			log.Printf("[autonomy git] init: %v", err)
			return
		}
		// Local identity — match the cicybot convention used elsewhere.
		_ = runGit(repoDir, "config", "user.name", "cicy-autonomy")
		_ = runGit(repoDir, "config", "user.email", "autonomy@cicy.local")
		// Initial empty commit so HEAD exists.
		_ = runGit(repoDir, "commit", "--allow-empty", "-q", "-m", "init: autonomy git tracking bootstrap")
	}

	// Stage the policy and any nearby autonomy artifacts that exist.
	for _, rel := range []string{"policy.json"} {
		path := filepath.Join(repoDir, rel)
		if _, err := os.Stat(path); err == nil {
			_ = runGit(repoDir, "add", rel)
		}
	}

	// Detect "nothing to commit" so we don't make empty commits on
	// no-op ticks. `git diff --cached --quiet` exits 0 on no changes.
	if err := runGit(repoDir, "diff", "--cached", "--quiet"); err == nil {
		return // nothing staged that differs
	}

	msg := strings.TrimSpace(commitMsg)
	if msg == "" {
		msg = "policy: autonomous update"
	}
	if err := runGit(repoDir, "commit", "-q", "-m", msg); err != nil {
		log.Printf("[autonomy git] commit: %v", err)
	}
}

// auditRepoDir returns the directory holding policy.json (= ~/cicy-ai/audit/).
// Returns "" if HOME is unresolvable.
func auditRepoDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "cicy-ai", "audit")
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Suppress stdout; surface stderr only on failure.
	out, err := cmd.CombinedOutput()
	if err != nil {
		// "diff --cached --quiet" uses non-zero to signal "differences",
		// which is normal. Bubble up so caller can treat exit-1 as "yes
		// there are changes".
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 && len(args) >= 2 && args[0] == "diff" {
				return exitErr
			}
		}
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
