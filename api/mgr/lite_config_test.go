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

	// No role + no frontmatter → NO tools (employees.yaml + profile-default both
	// retired; tools come only from role meta.yaml / custom AGENT.md / frontmatter).
	cfg := resolveLiteConfig("w-1", writeAgentsMD(t, ""))
	if cfg.profile != "assistant" {
		t.Fatalf("profile=%q want assistant", cfg.profile)
	}
	if len(cfg.enabledTools) != 0 {
		t.Errorf("agent with no role/frontmatter should have NO tools, got %v", cfg.enabledTools)
	}
	if cfg.external {
		t.Error("no agent is external anymore")
	}

	// frontmatter `profile:` is ignored now — every agent is an assistant.
	l := resolveLiteConfig("w-4", writeAgentsMD(t, "---\nprofile: liaison\n---\n"))
	if l.profile != "assistant" {
		t.Errorf("frontmatter profile must be ignored, got %q", l.profile)
	}
	if l.external {
		t.Error("liaison/external is gone")
	}

	// tools: drives the selection.
	a := resolveLiteConfig("w-3", writeAgentsMD(t, "---\ntools: [coordinate]\n---\n"))
	if !a.enabledTools["todo_add"] {
		t.Error("tools:[coordinate] should enable coordinate")
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
	ws := writeAgentsMD(t, "---\ntools: [coordinate]\n---\n")
	a := resolveLiteConfig("w-1", ws)
	b := resolveLiteConfig("w-1", ws)
	if len(a.enabledTools) == 0 || len(a.enabledTools) != len(b.enabledTools) {
		t.Fatalf("toolset must be deterministic across repeated resolution: %v vs %v", a.enabledTools, b.enabledTools)
	}
	for k := range a.enabledTools {
		if !b.enabledTools[k] {
			t.Fatalf("nondeterministic toolset: missing %q on second resolve", k)
		}
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

// ── grants escalate only within config (L1), scoped per agent ─────────────────

func TestLiteGrantsScopedByAgent(t *testing.T) {
	withLiteConfig(t, liteConfigFile{
		Profiles: map[string]liteProfileCfg{
			"assistant": {Name: "a", SystemBase: "@assistant",
				DefaultGroups: []string{"coordinate"}, GrantableGroups: []string{"coordinate"}},
		},
		ToolGroups: map[string][]string{
			"coordinate": {"todo_add"},
			"qa-ui":      {"chrome_eval"},
		},
		CustomTools: map[string]liteCustomTool{
			"chrome_eval": {Description: "x", Argv: []string{"agent-chrome", "eval", "{expr}"},
				Params: map[string]liteToolParam{"expr": {Type: "string", Required: true}}},
		},
		Grants: liteGrants{
			ByAgent: map[string][]string{"w-qa": {"qa-ui"}}, // only this agent may select qa-ui
		},
	})

	// granted agent gets the custom tool, but only when frontmatter selects it.
	wsSel := writeAgentsMD(t, "---\ntools: [coordinate, qa-ui]\n---\n")
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
	if _, ok := cfg.Profiles["assistant"]; !ok {
		t.Error("built-in assistant profile lost after merge")
	}
	if _, ok := cfg.Profiles["analyst"]; !ok {
		t.Error("file-declared analyst profile missing")
	}
	if _, ok := cfg.ToolGroups["coordinate"]; !ok {
		t.Error("built-in coordinate group lost after merge")
	}
}

// agent_online (the onboard group) is HR-only: a dispatcher agent that selects
// `tools: [coordinate, onboard]` in its AGENTS.md gets it, but a default
// dispatcher (coordinate only) must NOT — onboarding/pulling agents online is a
// privileged 组队官 action, not a power every lite agent has.
func TestOnboardToolHRGated(t *testing.T) {
	prev := cicyRootDir
	cicyRootDir = t.TempDir() // no config file → pure defaults
	resetLiteConfigCache()
	defer func() { cicyRootDir = prev; resetLiteConfigCache() }()

	// HR: profile dispatcher + tools:[coordinate, onboard] → agent_online enabled,
	// and the coordinate tools still present (onboard is additive, not replacing).
	hr := resolveLiteConfig("w-997", writeAgentsMD(t, "---\nprofile: dispatcher\ntools: [coordinate, onboard]\nname: HR\n---\n"))
	if !hr.enabledTools["agent_online"] {
		t.Error("HR (tools:[coordinate,onboard]) should enable agent_online")
	}
	if !hr.enabledTools["agent_list"] || !hr.enabledTools["agent_msg"] {
		t.Error("HR should keep its coordinate tools alongside onboard")
	}

	// Default dispatcher (no tools frontmatter → coordinate only) must NOT get it.
	pm := resolveLiteConfig("w-1001", writeAgentsMD(t, ""))
	if pm.enabledTools["agent_online"] {
		t.Error("default dispatcher escalated to agent_online — onboard must stay HR-gated")
	}
	if pm.enabledTools["shell"] {
		t.Error("default dispatcher must NOT have the shell tool")
	}
}

// The shell tool (raw PowerShell/bash) is Team-Helper-only: the 团队助手 template
// selects `tools: [coordinate, shell]` and gets it; no default dispatcher does.
func TestShellToolHelperGated(t *testing.T) {
	prev := cicyRootDir
	cicyRootDir = t.TempDir() // no config file → pure defaults
	resetLiteConfigCache()
	defer func() { cicyRootDir = prev; resetLiteConfigCache() }()

	helper := resolveLiteConfig("w-1001", writeAgentsMD(t, "---\nprofile: dispatcher\ntools: [coordinate, shell]\nname: 团队助手\n---\n"))
	if !helper.enabledTools["shell"] {
		t.Error("团队助手 (tools:[coordinate,shell]) should enable shell")
	}
	plain := resolveLiteConfig("w-1000", writeAgentsMD(t, ""))
	if plain.enabledTools["shell"] {
		t.Error("default dispatcher got shell — must stay helper-gated")
	}
}
