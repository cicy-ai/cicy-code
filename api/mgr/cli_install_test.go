package main

import (
	"strings"
	"testing"
)

func TestCliInstallSpecFor(t *testing.T) {
	if s, ok := cliInstallSpecFor("claude"); !ok || s.cliName != "claude" || s.npmPkg == "" {
		t.Errorf("claude spec = %+v ok=%v", s, ok)
	}
	if s, ok := cliInstallSpecFor("Codex"); !ok || s.cliName != "codex" {
		t.Errorf("codex (case-insensitive) spec = %+v ok=%v", s, ok)
	}
	// cicy is the built-in lite agent — not an installable CLI.
	if _, ok := cliInstallSpecFor("cicy"); ok {
		t.Error("cicy must not be an installable CLI spec")
	}
	if _, ok := cliInstallSpecFor("nonsense"); ok {
		t.Error("unknown agent_type must not resolve")
	}
}

func TestResolveNpmRegistry(t *testing.T) {
	if url, label := resolveNpmRegistry("mirror"); url != npmRegistryMirror || label != "mirror" {
		t.Errorf("forced mirror = %s/%s", url, label)
	}
	if url, label := resolveNpmRegistry("official"); url != npmRegistryOfficial || label != "official" {
		t.Errorf("forced official = %s/%s", url, label)
	}
}

func TestNpmInstallCmdRegistry(t *testing.T) {
	cmd := npmInstallCmdRegistry("@anthropic-ai/claude-code@latest", npmRegistryMirror)
	if !strings.Contains(cmd, "--registry="+npmRegistryMirror) {
		t.Errorf("install cmd missing explicit registry: %s", cmd)
	}
	if !strings.Contains(cmd, "@anthropic-ai/claude-code@latest") {
		t.Errorf("install cmd missing package: %s", cmd)
	}
	if !strings.Contains(cmd, `--prefix "$HOME/.npm-global"`) {
		t.Errorf("install cmd missing prefix (no-sudo): %s", cmd)
	}
}

func TestCliInstallCacheRoundTrip(t *testing.T) {
	prev := cicyRootDir
	cicyRootDir = t.TempDir()
	defer func() { cicyRootDir = prev }()

	setCliInstallRecord("claude", cliInstallRecord{Installed: true, Version: "1.2.3", Path: "/x/claude", CheckedAt: nowRFC3339()})
	m := loadCliInstallCache()
	got, ok := m["claude"]
	if !ok || !got.Installed || got.Version != "1.2.3" {
		t.Errorf("cache round-trip = %+v ok=%v", got, ok)
	}
}
