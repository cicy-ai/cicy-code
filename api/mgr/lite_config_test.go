package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// withLiteConfig points the loader at a temp config file and resets the cache.
func withLiteConfig(t *testing.T, cfg liteConfigFile) {
	t.Helper()
	dir := t.TempDir()
	prev := cicyRootDir
	cicyRootDir = dir
	if err := os.MkdirAll(filepath.Join(dir, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "db", "lite-config.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	resetLiteConfigCache()
	t.Cleanup(func() { cicyRootDir = prev; resetLiteConfigCache() })
}

func writeAgentsMD(t *testing.T, frontmatter string) string {
	t.Helper()
	ws := t.TempDir()
	if frontmatter != "" {
		if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte(frontmatter), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

// ── tools come ONLY from the role (meta.yaml / custom AGENT.md) ───────────────
// No profiles, no grants, no grantable, no AGENTS.md-frontmatter, no
// profile-default — an agent with no role gets NO tools.

func TestLiteNoRoleNoTools(t *testing.T) {
	prev := cicyRootDir
	cicyRootDir = t.TempDir() // no config file → pure defaults
	resetLiteConfigCache()
	defer func() { cicyRootDir = prev; resetLiteConfigCache() }()

	cfg := resolveLiteConfig("w-1", writeAgentsMD(t, ""))
	if cfg.profile != "assistant" {
		t.Fatalf("profile=%q want assistant", cfg.profile)
	}
	if len(cfg.enabledTools) != 0 {
		t.Errorf("agent with no role should have NO tools, got %v", cfg.enabledTools)
	}
	if cfg.external {
		t.Error("no agent is external anymore")
	}
}

// ── custom-tool runner: argv fill-in safety + guardrails ─────────────────────

func TestLiteCustomToolArgvNoShellInjection(t *testing.T) {
	cfg := liteConfig{
		external:     false,
		workspace:    t.TempDir(),
		enabledTools: map[string]bool{"echoer": true},
		customTools: map[string]liteCustomTool{
			"echoer": {Description: "x", Argv: []string{"printf", "%s", "{msg}"},
				Params: map[string]liteToolParam{"msg": {Type: "string", Required: true, MaxLen: 100}}},
		},
	}
	// A value loaded with shell metacharacters must be passed as ONE literal arg,
	// never interpreted (printf prints it verbatim, no command runs).
	out := runLiteCustomTool(nil, cfg, "w-1", "echoer", map[string]interface{}{
		"msg": "; rm -rf / | whoami && echo pwned $(id)",
	})
	if out != "; rm -rf / | whoami && echo pwned $(id)" {
		t.Errorf("argv injection not contained, got %q", out)
	}
}

func TestLiteCustomToolSchemaRejects(t *testing.T) {
	cfg := liteConfig{
		workspace:    t.TempDir(),
		enabledTools: map[string]bool{"t": true},
		customTools: map[string]liteCustomTool{
			"t": {Argv: []string{"printf", "%s", "{idx}"},
				Params: map[string]liteToolParam{"idx": {Type: "string", Pattern: `[0-9]{1,3}`, Required: true}}},
		},
	}
	if out := runLiteCustomTool(nil, cfg, "w-1", "t", map[string]interface{}{"idx": "abc"}); out == "abc" || out[:5] != "error" {
		t.Errorf("pattern violation should error, got %q", out)
	}
	if out := runLiteCustomTool(nil, cfg, "w-1", "t", map[string]interface{}{}); out[:5] != "error" {
		t.Errorf("missing required should error, got %q", out)
	}
}

func TestLiteCustomToolExternalRefused(t *testing.T) {
	cfg := liteConfig{
		external:     true,
		enabledTools: map[string]bool{"t": true},
		customTools:  map[string]liteCustomTool{"t": {Argv: []string{"echo"}}},
	}
	if out := runLiteCustomTool(nil, cfg, "w-1", "t", nil); out[:5] != "error" {
		t.Errorf("external profile must refuse custom tools, got %q", out)
	}
}

func TestLiteCustomToolNotEnabledRefused(t *testing.T) {
	cfg := liteConfig{
		enabledTools: map[string]bool{}, // not enabled
		customTools:  map[string]liteCustomTool{"t": {Argv: []string{"echo"}}},
	}
	if out := runLiteCustomTool(nil, cfg, "w-1", "t", nil); out[:5] != "error" {
		t.Errorf("disabled tool must be refused, got %q", out)
	}
}

// ── config hot-merge: a file adds tool groups without losing the built-ins ─────

func TestLiteConfigMergeKeepsBuiltins(t *testing.T) {
	withLiteConfig(t, liteConfigFile{
		ToolGroups: map[string][]string{"extra": {"shell"}},
	})
	cfg := loadLiteConfig()
	if _, ok := cfg.ToolGroups["core"]; !ok {
		t.Error("built-in core group lost after merge")
	}
	if _, ok := cfg.ToolGroups["extra"]; !ok {
		t.Error("file-declared extra group missing")
	}
}
