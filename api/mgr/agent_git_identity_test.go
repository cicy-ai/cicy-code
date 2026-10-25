package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHubSlugifyMatchesHub(t *testing.T) {
	for in, want := range map[string]string{"colab_limeng": "colab-limeng", "Limeng": "limeng", "  My Box!! ": "my-box", "": ""} {
		if got := hubSlugify(in); got != want {
			t.Errorf("hubSlugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgentGitIdentityEnv(t *testing.T) {
	oldDB := cicyDBDir
	cicyDBDir = t.TempDir()
	defer func() { cicyDBDir = oldDB }()
	write := func(cred cicyCloudCredential) {
		data, _ := json.Marshal(cred)
		if err := os.WriteFile(filepath.Join(cicyDBDir, "cloud-device.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if env := agentGitIdentityEnv("w-1002"); env != nil {
		t.Fatalf("no credential should give no identity, got %v", env)
	}
	write(cicyCloudCredential{TeamID: "colab_limeng", Token: "x", Mode: "cloud"})
	if env := agentGitIdentityEnv("w-1002"); env != nil {
		t.Fatalf("cicy-cloud login has no mailbox, got %v", env)
	}
	write(cicyCloudCredential{TeamID: "colab_limeng", Token: "x", Mode: cicyCloudModeHub})
	env := agentGitIdentityEnv("W-1002")
	if env["GIT_AUTHOR_EMAIL"] != "w-1002.colab-limeng@cicy-ai.com" || env["GIT_COMMITTER_NAME"] != "w-1002.colab-limeng" {
		t.Fatalf("unexpected identity %v", env)
	}
	t.Setenv("CICY_AGENT_GIT_IDENTITY", "0")
	if env := agentGitIdentityEnv("w-1002"); env != nil {
		t.Fatalf("opt-out ignored: %v", env)
	}
}
