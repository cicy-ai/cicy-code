package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeConfigPersistsSecretAndRemoteWithoutLeakingToken(t *testing.T) {
	withTempCicyRoot(t)
	previousValidator := validateKnowledgeGitHubAccessFn
	validateKnowledgeGitHubAccessFn = func(_, _ string) error { return nil }
	t.Cleanup(func() { validateKnowledgeGitHubAccessFn = previousValidator })
	if err := knowledgeEnsureRoot(); err != nil {
		t.Fatalf("ensure knowledge root: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(knowledgeGitTokenEnv) })

	code, body := knowledgeReq(t, handleKnowledgeConfig, "POST", "/api/knowledge/config", M{
		"pane":   "w-102",
		"origin": "https://github.com/cicy-ai/private-knowledge.git",
		"token":  "github_pat_secret-value",
	})
	if code != 200 {
		t.Fatalf("POST config code=%d body=%v", code, body)
	}
	if body["pane"] != "w-102:main.0" || body["origin"] != "https://github.com/cicy-ai/private-knowledge.git" {
		t.Fatalf("unexpected config response: %v", body)
	}
	if body["token_set"] != true || body["token_tail"] != "alue" {
		t.Fatalf("masked token metadata missing: %v", body)
	}
	rawRemote, err := exec.Command("git", "-C", knowledgeRootDir(), "remote", "get-url", "origin").Output()
	if err != nil || !strings.Contains(string(rawRemote), "x-access-token:github_pat_secret-value@github.com") {
		t.Fatalf("authenticated origin was not saved to .git/config: %q err=%v", rawRemote, err)
	}
	gitConfigInfo, err := os.Stat(filepath.Join(knowledgeRootDir(), ".git", "config"))
	if err != nil {
		t.Fatalf("stat .git/config: %v", err)
	}
	if gitConfigInfo.Mode().Perm() != 0o600 {
		t.Fatalf(".git/config must be private: mode=%v", gitConfigInfo.Mode().Perm())
	}
	for key, want := range map[string]string{
		"branch.main.remote": "origin",
		"branch.main.merge":  "refs/heads/main",
		"user.name":          "cicy-knowledge-sync-gh",
		"user.email":         "cicybot@qq.com",
	} {
		out, err := exec.Command("git", "-C", knowledgeRootDir(), "config", "--local", "--get", key).Output()
		if err != nil || strings.TrimSpace(string(out)) != want {
			t.Fatalf("git config %s = %q, want %q (err=%v)", key, strings.TrimSpace(string(out)), want, err)
		}
	}
	if strings.Contains(strings.TrimSpace(anyString(body["token"])), "secret") {
		t.Fatalf("response leaked token: %v", body)
	}
	if got := os.Getenv(knowledgeGitTokenEnv); got != "github_pat_secret-value" {
		t.Fatalf("runtime token env = %q", got)
	}
	info, err := os.Stat(filepath.Join(cicyRootDir, "db", "knowledge-git.json"))
	if err != nil {
		t.Fatalf("stat private config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private config mode = %v", info.Mode().Perm())
	}

	code, get := knowledgeReq(t, handleKnowledgeConfig, "GET", "/api/knowledge/config", nil)
	if code != 200 || get["token_set"] != true || get["token_tail"] != "alue" {
		t.Fatalf("GET config code=%d body=%v", code, get)
	}
	if _, exists := get["token"]; exists {
		t.Fatalf("GET response must not include token: %v", get)
	}
	code, revealed := knowledgeReq(t, handleKnowledgeConfig, "GET", "/api/knowledge/config?reveal_token=1", nil)
	if code != 200 || revealed["token"] != "github_pat_secret-value" {
		t.Fatalf("explicit token reveal code=%d body=%v", code, revealed)
	}
}

func TestKnowledgeConfigRejectsCredentialBearingRemote(t *testing.T) {
	withTempCicyRoot(t)
	if err := knowledgeEnsureRoot(); err != nil {
		t.Fatalf("ensure knowledge root: %v", err)
	}
	code, _ := knowledgeReq(t, handleKnowledgeConfig, "POST", "/api/knowledge/config", M{
		"pane": "w-1001", "origin": "https://token@github.com/org/repo.git",
	})
	if code != 400 {
		t.Fatalf("credential-bearing origin code=%d, want 400", code)
	}
}

func TestKnowledgeConfigRequiresOriginAndToken(t *testing.T) {
	withTempCicyRoot(t)
	if err := knowledgeEnsureRoot(); err != nil {
		t.Fatalf("ensure knowledge root: %v", err)
	}
	code, _ := knowledgeReq(t, handleKnowledgeConfig, "POST", "/api/knowledge/config", M{"pane": "w-1001", "token": "token"})
	if code != 400 {
		t.Fatalf("missing origin code=%d, want 400", code)
	}
	code, _ = knowledgeReq(t, handleKnowledgeConfig, "POST", "/api/knowledge/config", M{"pane": "w-1001", "origin": "https://github.com/org/repo.git"})
	if code != 400 {
		t.Fatalf("missing token code=%d, want 400", code)
	}
}

func TestKnowledgeConfigFallsBackToEnvironmentToken(t *testing.T) {
	withTempCicyRoot(t)
	t.Setenv(knowledgeGitTokenEnv, "github_pat_from-environment")
	code, body := knowledgeReq(t, handleKnowledgeConfig, "GET", "/api/knowledge/config", nil)
	if code != 200 || body["token_set"] != true || body["token_tail"] != "ment" {
		t.Fatalf("environment token metadata code=%d body=%v", code, body)
	}
	code, revealed := knowledgeReq(t, handleKnowledgeConfig, "GET", "/api/knowledge/config?reveal_token=1", nil)
	if code != 200 || revealed["token"] != "github_pat_from-environment" {
		t.Fatalf("environment token reveal code=%d body=%v", code, revealed)
	}
}
