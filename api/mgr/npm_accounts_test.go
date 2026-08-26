package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestNpmSecretTail(t *testing.T) {
	if got := secretTail("npm_abcdefGPTK"); got != "GPTK" {
		t.Fatalf("tail=%q", got)
	}
	if got := secretTail("abc"); got != "abc" {
		t.Fatalf("short tail=%q", got)
	}
}

func TestNpmAccountNameValidation(t *testing.T) {
	for _, name := range []string{"cicy-ai", "cicy.team", "user_1"} {
		if !npmAccountNameRE.MatchString(name) {
			t.Fatalf("valid name rejected: %s", name)
		}
	}
	for _, name := range []string{"", "../secret", "bad name"} {
		if npmAccountNameRE.MatchString(name) {
			t.Fatalf("invalid name accepted: %s", name)
		}
	}
}

func TestNpmRegistryNormalisation(t *testing.T) {
	cases := map[string]string{
		"":                              npmDefaultRegistry,
		"https://registry.npmjs.org/":   "https://registry.npmjs.org",
		"npm.pkg.github.com":            "https://npm.pkg.github.com",
		"https://npm.example.com/repo/": "https://npm.example.com/repo",
	}
	for in, want := range cases {
		if got := npmRegistryOf(npmAccountConfig{Registry: in}); got != want {
			t.Fatalf("registry %q => %q, want %q", in, got, want)
		}
	}
}

func TestNpmrcKeyFor(t *testing.T) {
	if got := npmrcKeyFor("https://registry.npmjs.org"); got != "//registry.npmjs.org/:_authToken" {
		t.Fatalf("key=%q", got)
	}
	if got := npmrcKeyFor("https://npm.example.com/repo"); got != "//npm.example.com/repo/:_authToken" {
		t.Fatalf("scoped-path key=%q", got)
	}
}

// A bind must replace only its own auth line: unrelated settings (and other
// registries' tokens) survive, so switching accounts never wipes the file.
func TestWriteNpmrcAuthPreservesOtherLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".npmrc")
	seed := "registry=https://registry.npmjs.org\n//npm.pkg.github.com/:_authToken=keep-me\n//registry.npmjs.org/:_authToken=old-token\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeNpmrcAuth(path, "https://registry.npmjs.org", "new-token", "@cicy"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"registry=https://registry.npmjs.org\n",
		"//npm.pkg.github.com/:_authToken=keep-me\n",
		"@cicy:registry=https://registry.npmjs.org\n",
		"//registry.npmjs.org/:_authToken=new-token\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old-token") {
		t.Fatalf("stale token survived:\n%s", got)
	}
	if strings.Count(got, "//registry.npmjs.org/:_authToken=") != 1 {
		t.Fatalf("duplicate auth line:\n%s", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v", info.Mode().Perm())
	}
}

func TestWriteNpmrcAuthCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".npmrc")
	if err := writeNpmrcAuth(path, "https://registry.npmjs.org", "tok", ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "//registry.npmjs.org/:_authToken=tok\n" {
		t.Fatalf("content=%q", string(data))
	}
}

func TestNpmTwoFAMode(t *testing.T) {
	if got := npmTwoFAMode(map[string]any{"mode": "auth-and-writes", "pending": false}); got != "auth-and-writes" {
		t.Fatalf("mode=%q", got)
	}
	if got := npmTwoFAMode(map[string]any{}); got != "enabled" {
		t.Fatalf("mode=%q", got)
	}
	if got := npmTwoFAMode(false); got != "" {
		t.Fatalf("disabled should be empty, got %q", got)
	}
	if got := npmTwoFAMode(nil); got != "" {
		t.Fatalf("missing should be empty, got %q", got)
	}
}

// One pasted token has to yield the whole account: username, email, 2FA mode,
// scopes, package counts and token metadata.
func TestNpmInspectTokenFillsEverything(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer npm_secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/-/whoami":
			_, _ = w.Write([]byte(`{"username":"cicy-ai"}`))
		case r.URL.Path == "/-/npm/v1/user":
			_, _ = w.Write([]byte(`{"name":"cicy-ai","email":"npm@cicy.de5.net","email_verified":true,"fullname":"CiCy","tfa":{"mode":"auth-and-writes","pending":false}}`))
		case r.URL.Path == "/-/user/cicy-ai/package":
			_, _ = w.Write([]byte(`{"cicy-code":"write","@cicy/sdk":"write","@cicy/private-kit":"write"}`))
		case r.URL.Path == "/-/v1/search":
			_, _ = w.Write([]byte(`{"total":2,"objects":[{"package":{"name":"cicy-code","date":"2026-08-20T10:00:00.000Z"}},{"package":{"name":"@cicy/sdk","date":"2026-08-24T10:00:00.000Z"}}]}`))
		case r.URL.Path == "/-/npm/v1/tokens":
			_, _ = w.Write([]byte(`{"objects":[{"token":"secret","key":"k1","readonly":false,"automation":true,"created":"2026-01-02T03:04:05.000Z"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	result, message, err := npmInspectToken("npm_secret", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatalf("inspect refused: %s", message)
	}
	if result.Username != "cicy-ai" || result.Email != "npm@cicy.de5.net" || !result.EmailVerified {
		t.Fatalf("profile=%+v", result)
	}
	if result.TwoFAMode != "auth-and-writes" {
		t.Fatalf("tfa=%q", result.TwoFAMode)
	}
	if result.Packages != 3 || result.PublicPackages != 2 || result.PrivatePkgs != 1 {
		t.Fatalf("packages=%d public=%d private=%d", result.Packages, result.PublicPackages, result.PrivatePkgs)
	}
	if len(result.Scopes) != 1 || result.Scopes[0] != "@cicy" {
		t.Fatalf("scopes=%v", result.Scopes)
	}
	if result.LastPublish != "2026-08-24T10:00:00.000Z" {
		t.Fatalf("last publish=%q", result.LastPublish)
	}
	if !result.TokenAutomatic || result.TokenReadonly || result.TokenCreated == "" {
		t.Fatalf("token meta=%+v", result)
	}
	if len(result.Notes) != 0 {
		t.Fatalf("unexpected degraded notes: %v", result.Notes)
	}
}

// A granular token can read /-/whoami and nothing else; inspection must still
// succeed and simply report which parts were unavailable.
func TestNpmInspectTokenDegradesGracefully(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/whoami" {
			_, _ = w.Write([]byte(`{"username":"granular-bot"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	result, _, err := npmInspectToken("npm_granular", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Username != "granular-bot" {
		t.Fatalf("result=%+v", result)
	}
	if !slices.Contains(result.Notes, "profile") || !slices.Contains(result.Notes, "tokens") {
		t.Fatalf("notes=%v", result.Notes)
	}
}

func TestNpmInspectTokenRejectsBadToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	result, message, err := npmInspectToken("nope", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil || message == "" {
		t.Fatalf("expected refusal, got %+v (%q)", result, message)
	}
}
