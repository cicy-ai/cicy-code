package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeConfigPersistsSecretAndRemoteWithoutLeakingToken(t *testing.T) {
	withTempCicyRoot(t)
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
