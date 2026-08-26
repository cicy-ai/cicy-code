package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerRegistryNormalisation(t *testing.T) {
	cases := map[string]string{
		"":                             dockerDefaultRegistry,
		"docker.io":                    "docker.io",
		"https://index.docker.io/v1/":  "docker.io",
		"registry-1.docker.io":         "docker.io",
		"ghcr.io":                      "ghcr.io",
		"https://registry.example.com": "registry.example.com",
	}
	for in, want := range cases {
		if got := dockerRegistryOf(dockerAccountConfig{Registry: in}); got != want {
			t.Fatalf("registry %q => %q, want %q", in, got, want)
		}
	}
	if got := dockerAuthKeyFor("docker.io"); got != dockerHubAuthKey {
		t.Fatalf("hub auth key=%q", got)
	}
	if got := dockerAuthKeyFor("ghcr.io"); got != "ghcr.io" {
		t.Fatalf("registry auth key=%q", got)
	}
}

func TestDockerRateLimitValue(t *testing.T) {
	if got := dockerRateLimitValue("100;w=21600"); got != 100 {
		t.Fatalf("limit=%d", got)
	}
	if got := dockerRateLimitValue("76"); got != 76 {
		t.Fatalf("limit=%d", got)
	}
	if got := dockerRateLimitValue(""); got != -1 {
		t.Fatalf("missing header should be -1, got %d", got)
	}
}

// A bind writes exactly what `docker login` writes, and other registries'
// credentials plus unrelated top-level keys survive.
func TestWriteDockerConfigAuthMerges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := `{"auths":{"ghcr.io":{"auth":"a2VlcDprZWVw"},"https://index.docker.io/v1/":{"auth":"b2xkOm9sZA=="}},"credsStore":"pass"}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDockerConfigAuth(path, dockerHubAuthKey, "cicy", "dckr_pat_x", "npm@cicy.de5.net"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Auths map[string]struct {
			Auth  string `json:"auth"`
			Email string `json:"email"`
		} `json:"auths"`
		CredsStore string `json:"credsStore"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.CredsStore != "pass" {
		t.Fatalf("credsStore lost: %s", data)
	}
	if config.Auths["ghcr.io"].Auth != "a2VlcDprZWVw" {
		t.Fatalf("other registry lost: %s", data)
	}
	decoded, _ := base64.StdEncoding.DecodeString(config.Auths[dockerHubAuthKey].Auth)
	if string(decoded) != "cicy:dckr_pat_x" {
		t.Fatalf("auth=%q", string(decoded))
	}
	if config.Auths[dockerHubAuthKey].Email != "npm@cicy.de5.net" {
		t.Fatalf("email=%q", config.Auths[dockerHubAuthKey].Email)
	}
	if strings.Contains(string(data), "b2xkOm9sZA==") {
		t.Fatalf("stale credential survived: %s", data)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v", info.Mode().Perm())
	}
}

func TestDockerLoginNameFallsBackToAccountName(t *testing.T) {
	if got := dockerLoginName("cicy-ai", dockerAccountConfig{}); got != "cicy-ai" {
		t.Fatalf("login=%q", got)
	}
	if got := dockerLoginName("cicy-ai", dockerAccountConfig{Username: " robot "}); got != "robot" {
		t.Fatalf("login=%q", got)
	}
}

// A Docker credential is meaningless without the login name it pairs with, so
// inspection refuses instead of silently probing as an anonymous user.
func TestDockerInspectRequiresUsername(t *testing.T) {
	result, message, err := dockerInspectToken("", "dckr_pat_x", "docker.io")
	if err != nil {
		t.Fatal(err)
	}
	if result != nil || message == "" {
		t.Fatalf("expected refusal, got %+v (%q)", result, message)
	}
	result, message, err = dockerInspectToken("", "tok", "ghcr.io")
	if err != nil {
		t.Fatal(err)
	}
	if result != nil || message == "" {
		t.Fatalf("expected refusal for private registry, got %+v (%q)", result, message)
	}
}
