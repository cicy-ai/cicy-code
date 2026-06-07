package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeLiteConfig points the loader at a temp config file and resets the cache.
// Returns a cleanup that restores the default path resolution.
func withLiteConfig(t *testing.T, cfg liteConfigFile) {
	t.Helper()
	dir := t.TempDir()
	// cicyRootDir/db/lite-config.json is the path; override cicyRootDir.
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

// ── default behaviour unchanged (migration safety) ───────────────────────────

func TestLiteDefaultsReproduceBuiltins(t *testing.T) {
	prev := cicyRootDir
	cicyRootDir = t.TempDir() // no config file → pure defaults
	resetLiteConfigCache()
	defer func() { cicyRootDir = prev; resetLiteConfigCache() }()

	// dispatcher: no frontmatter → coordinate group, internal.
	cfg := resolveLiteConfig("w-1", writeAgentsMD(t, ""))
	if cfg.profile != "dispatcher" {
		t.Fatalf("profile=%q want dispatcher", cfg.profile)
	}
	for _, n := range []string{"todo_add", "agent_msg", "agent_capture"} {
		if !cfg.enabledTools[n] {
			t.Errorf("dispatcher missing %q", n)
		}
	}
	if cfg.external {
		t.Error("dispatcher must not be external")
	}

	// assistant: no frontmatter → pure chat.
	a := resolveLiteConfig("w-2", writeAgentsMD(t, "---\nprofile: assistant\n---\n"))
	if len(a.enabledTools) != 0 {
		t.Errorf("assistant default should be pure chat, got %v", a.enabledTools)
	}
	// assistant + tools:[coordinate] → coordinate (grantable allows it).
	a2 := resolveLiteConfig("w-3", writeAgentsMD(t, "---\nprofile: assistant\ntools: [coordinate]\n---\n"))
	if !a2.enabledTools["todo_add"] {
		t.Error("assistant tools:[coordinate] should enable coordinate")
	}

	// liaison: external, a2a+handoff.
	l := resolveLiteConfig("w-4", writeAgentsMD(t, "---\nprofile: liaison\n---\n"))
	if !l.external {
		t.Error("liaison must be external")
	}
	if !l.enabledTools["a2a_status"] {
		t.Error("liaison missing a2a tools")
	}
}

// ── L4: session/injection cannot grant tools (tools computed pre-turn) ────────
// (Structural: resolveLiteConfig takes only shortID+workspace, never message
//  text. This test documents that the enabled set is independent of any chat
//  content by resolving twice and asserting determinism.)
func TestLiteToolsetIndependentOfSession(t *testing.T) {
	prev := cicyRootDir
	cicyRootDir = t.TempDir()
	resetLiteConfigCache()
	defer func() { cicyRootDir = prev; resetLiteConfigCache() }()
	ws := writeAgentsMD(t, "---\nprofile: assistant\n---\n")
	a := resolveLiteConfig("w-1", ws)
	b := resolveLiteConfig("w-1", ws)
	if len(a.enabledTools) != 0 || len(b.enabledTools) != 0 {
		t.Fatal("assistant must stay pure-chat regardless of repeated resolution")
	}
}

// ── L3: frontmatter can only NARROW, never escalate ──────────────────────────

func TestLiteFrontmatterCannotEscalate(t *testing.T) {
	withLiteConfig(t, liteConfigFile{
		Profiles: map[string]liteProfileCfg{
			"assistant": {Name: "a", SystemBase: "@assistant",
				DefaultGroups: nil, GrantableGroups: []string{"coordinate"}},
		},
		ToolGroups: map[string][]string{
			"coordinate": {"todo_add", "agent_msg"},
			"danger":     {"shell_exec"},
		},
		CustomTools: map[string]liteCustomTool{
			"shell_exec": {Description: "x", Argv: []string{"echo"}},
		},
		Grants: liteGrants{},
	})
	// assistant tries to grab a non-grantable group via frontmatter.
	ws := writeAgentsMD(t, "---\nprofile: assistant\ntools: [coordinate, danger, shell_exec]\n---\n")
	cfg := resolveLiteConfig("w-1", ws)
	if cfg.enabledTools["shell_exec"] {
		t.Error("frontmatter escalated to a non-grantable custom tool — SECURITY HOLE")
	}
	if !cfg.enabledTools["todo_add"] {
		t.Error("grantable coordinate should still be enabled")
	}
	if len(cfg.customTools) != 0 {
		t.Errorf("no custom tool should be active, got %v", cfg.customTools)
	}
}

// ── grants escalate only within config (L1), and external is hard-capped ──────

func TestLiteGrantsAndExternalCap(t *testing.T) {
	withLiteConfig(t, liteConfigFile{
		Profiles: map[string]liteProfileCfg{
			"dispatcher": {Name: "d", SystemBase: "@dispatcher",
				DefaultGroups: []string{"coordinate"}, GrantableGroups: []string{"coordinate"}},
			"liaison": {Name: "l", SystemBase: "@liaison",
				DefaultGroups: []string{"a2a"}, GrantableGroups: []string{"a2a"}, External: true},
		},
		ToolGroups: map[string][]string{
			"coordinate": {"todo_add"},
			"a2a":        {"a2a_status"},
			"qa-ui":      {"chrome_eval"},
		},
		CustomTools: map[string]liteCustomTool{
			"chrome_eval": {Description: "x", Argv: []string{"agent-chrome", "eval", "{expr}"},
				Params: map[string]liteToolParam{"expr": {Type: "string", Required: true}}},
		},
		Grants: liteGrants{
			ByAgent:   map[string][]string{"w-qa": {"qa-ui"}},
			ByProfile: map[string][]string{"liaison": {"qa-ui"}}, // must be IGNORED (external)
		},
	})

	// granted dispatcher agent gets the custom tool, but only when frontmatter selects it.
	wsSel := writeAgentsMD(t, "---\nprofile: dispatcher\ntools: [coordinate, qa-ui]\n---\n")
	qa := resolveLiteConfig("w-qa", wsSel)
	if !qa.enabledTools["chrome_eval"] {
		t.Error("granted agent selecting qa-ui should enable chrome_eval")
	}
	if _, ok := qa.customTools["chrome_eval"]; !ok {
		t.Error("chrome_eval should be in resolved customTools")
	}
	// a DIFFERENT agent (no grant) selecting qa-ui gets nothing.
	other := resolveLiteConfig("w-other", wsSel)
	if other.enabledTools["chrome_eval"] {
		t.Error("ungranted agent escalated via frontmatter — SECURITY HOLE")
	}
	// liaison (external) even with a by-profile grant + frontmatter cannot get it.
	wsLia := writeAgentsMD(t, "---\nprofile: liaison\ntools: [a2a, qa-ui]\n---\n")
	lia := resolveLiteConfig("w-qa", wsLia) // same agent id that HAS the by_agent grant
	if lia.enabledTools["chrome_eval"] {
		t.Error("external liaison obtained a custom tool — SECURITY HOLE")
	}
	if len(lia.customTools) != 0 {
		t.Error("external profile must resolve zero custom tools")
	}
	if !lia.enabledTools["a2a_status"] {
		t.Error("liaison should keep its a2a tools")
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
	out := runLiteCustomTool(cfg, "w-1", "echoer", map[string]interface{}{
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
	if out := runLiteCustomTool(cfg, "w-1", "t", map[string]interface{}{"idx": "abc"}); out == "abc" || out[:5] != "error" {
		t.Errorf("pattern violation should error, got %q", out)
	}
	if out := runLiteCustomTool(cfg, "w-1", "t", map[string]interface{}{}); out[:5] != "error" {
		t.Errorf("missing required should error, got %q", out)
	}
}

func TestLiteCustomToolExternalRefused(t *testing.T) {
	cfg := liteConfig{
		external:     true,
		enabledTools: map[string]bool{"t": true},
		customTools:  map[string]liteCustomTool{"t": {Argv: []string{"echo"}}},
	}
	if out := runLiteCustomTool(cfg, "w-1", "t", nil); out[:5] != "error" {
		t.Errorf("external profile must refuse custom tools, got %q", out)
	}
}

func TestLiteCustomToolNotEnabledRefused(t *testing.T) {
	cfg := liteConfig{
		enabledTools: map[string]bool{}, // not enabled
		customTools:  map[string]liteCustomTool{"t": {Argv: []string{"echo"}}},
	}
	if out := runLiteCustomTool(cfg, "w-1", "t", nil); out[:5] != "error" {
		t.Errorf("disabled tool must be refused, got %q", out)
	}
}

// ── config hot-merge: file adds without redeclaring built-ins ─────────────────

func TestLiteConfigMergeKeepsBuiltins(t *testing.T) {
	withLiteConfig(t, liteConfigFile{
		// only declare a NEW profile; built-ins must survive the merge.
		Profiles: map[string]liteProfileCfg{
			"analyst": {Name: "分析", SystemBase: "literal base", GrantableGroups: []string{"coordinate"}},
		},
	})
	cfg := loadLiteConfig()
	if _, ok := cfg.Profiles["dispatcher"]; !ok {
		t.Error("built-in dispatcher profile lost after merge")
	}
	if _, ok := cfg.Profiles["analyst"]; !ok {
		t.Error("file-declared analyst profile missing")
	}
	if _, ok := cfg.ToolGroups["coordinate"]; !ok {
		t.Error("built-in coordinate group lost after merge")
	}
}
