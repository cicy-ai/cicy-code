package main

import (
	"path/filepath"
	"strings"
)

// Lightweight customizable agent runtime.
//
// The "dispatcher" agent_type is really a generic lite agent: a thin LLM loop
// (agent_dispatcher.go) whose PERSONALITY and CAPABILITIES are data, not code.
// One agent_type, many roles — PM/dispatcher, customer support, sales, company
// PR, … — each instance customized entirely through its workspace AGENTS.md.
//
// AGENTS.md may begin with a YAML-ish frontmatter block selecting a built-in
// profile (personality + default tool groups) and overriding the tool set:
//
//	---
//	profile: assistant        # dispatcher | assistant  (default: dispatcher)
//	tools: [coordinate]       # tool groups; [] = pure chat, no tools
//	name: 客服小美             # display only
//	---
//	# 客服小美
//	你是 XX 公司的客服……        ← this body becomes the system prompt extension
//
// No frontmatter ⇒ dispatcher profile (backward compatible with the original
// dispatcher agents). The body (sans frontmatter) is always appended to the
// profile's compiled base prompt, so users can refine any role inline and it
// takes effect next turn — no restart.

// The system-prompt base is NOT a Go const — it lives in
// ~/cicy-ai/memory/agents/assistant/ (seeded from embed/agent-roles/) and is
// loaded via resolveSystemBase. One universal "assistant" template.

// Profiles / tool groups / custom tools / grants are no longer Go consts — they
// live in ~/cicy-ai/db/lite-config.json (see lite_config.go), with baked
// defaults reproducing the former built-ins. resolveLiteConfig reads them.

// liteConfig is the resolved per-instance configuration.
type liteConfig struct {
	profile      string
	systemPrompt string          // the shared system.md base (the `system` field)
	roleContext  string          // the agent's AGENTS.md body — its role, carried as message context (NOT in system)
	enabledTools map[string]bool // empty ⇒ pure chat
	external      bool            // profile is outward-facing (liaison): exec/custom tools refused
	workspace     string          // for custom-tool cwd
	customTools   map[string]liteCustomTool
	maxToolRounds int             // per-role override of the tool-round cap (0 = global default)
}

// liteFrontmatter is the parsed AGENTS.md header (all optional).
type liteFrontmatter struct {
	profile  string
	name     string
	model    string // optional default model (used by custom agents; ignored elsewhere)
	tools    []string
	hasTools bool // distinguishes "tools: []" (override to none) from absent
	body     string
}

// parseLiteFrontmatter splits an optional leading `---`…`---` YAML-ish block
// from the markdown body. Only profile/name/tools are recognized; unknown keys
// are ignored. A document without a leading `---` is treated as all-body.
func parseLiteFrontmatter(content string) liteFrontmatter {
	fm := liteFrontmatter{body: content}
	s := strings.TrimLeft(content, "\ufeff \t\r\n")
	if !strings.HasPrefix(s, "---") {
		return fm
	}
	rest := s[3:]
	if !strings.HasPrefix(rest, "\n") && !strings.HasPrefix(rest, "\r") {
		return fm // "---foo" is not a fence
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fm
	}
	block := rest[:end]
	after := rest[end+len("\n---"):]
	if i := strings.IndexAny(after, "\r\n"); i >= 0 {
		after = after[i+1:]
	} else {
		after = ""
	}
	fm.body = strings.TrimLeft(after, "\r\n")

	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		switch key {
		case "profile":
			fm.profile = strings.ToLower(strings.Trim(val, `"'`))
		case "name":
			fm.name = strings.Trim(val, `"'`)
		case "model":
			fm.model = strings.Trim(val, `"'`)
		case "tools":
			fm.hasTools = true
			fm.tools = parseLiteList(val)
		}
	}
	return fm
}

// parseLiteList accepts `[a, b]` or `a, b` (or empty) → []string.
func parseLiteList(val string) []string {
	val = strings.TrimSpace(val)
	val = strings.TrimPrefix(val, "[")
	val = strings.TrimSuffix(val, "]")
	out := []string{}
	for _, part := range strings.Split(val, ",") {
		p := strings.TrimSpace(strings.Trim(strings.TrimSpace(part), `"'`))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolveLiteConfig reads the agent's AGENTS.md + the lite config and produces
// its effective profile, system prompt and enabled tool set. Robust to a
// missing/empty file (→ dispatcher profile, backward compatible).
//
// Tool resolution enforces the L1–L4 trust model:
//   grantable = expand(profile.GrantableGroups) ∪ expand(grants for this
//               agent/profile)            — but for EXTERNAL profiles, grants are
//               ignored and grantable is capped to the profile's own groups.
//   selected  = expand(frontmatter.tools) if present, else expand(DefaultGroups)
//   effective = selected ∩ grantable      — frontmatter (L3) can only NARROW;
//               it can never name a tool the config (L1) didn't make grantable.
func resolveLiteConfig(shortID, workspace string) liteConfig {
	cfg := loadLiteConfig()
	raw := loadTemplateFile(filepath.Join(workspace, "AGENTS.md"))
	fm := parseLiteFrontmatter(raw)

	// ONE universal base: every cicy agent is an "assistant". There are no more
	// dispatcher/liaison profiles and no `profile:` frontmatter — identity comes
	// purely from the system prompt + selected tools.
	profileKey := "assistant"

	// Tools come ONLY from the role's own definition — role/meta.yaml `tools:`
	// (library roles) or a custom agent's AGENT.md `tools:` (user-authored). No
	// profiles, no grants, no grantable ceiling, no AGENTS.md-frontmatter override:
	// 以 role 为主. enabled = expand(the role's selected groups).
	roleSlug := employeeRoleSlug(shortID) // this agent's role-template slug
	rm := loadRoleMeta(roleSlug)
	selectGroups := rm.Tools
	if len(selectGroups) == 0 {
		if ca, ok := customAgentFor(roleSlug); ok {
			selectGroups = ca.Tools
		}
	}
	enabled := expandGroups(selectGroups, cfg.ToolGroups)

	// Custom tools available to this instance (subset of enabled that are
	// declared custom).
	custom := map[string]liteCustomTool{}
	for name := range enabled {
		if t, isCustom := cfg.CustomTools[name]; isCustom {
			custom[name] = t
		}
	}

	// Two separate things, like a CLI agent (system prompt vs CLAUDE.md):
	//   - systemPrompt = the role's own system.md → the `system` field (no fallback).
	//   - roleContext  = THIS agent's AGENTS.md body (its role) → carried as a
	//     leading context block in `messages`, NOT concatenated into `system`.
	// Both stay byte-stable across turns (cache prefix) — no timestamps.
	systemBase := cicySystemBase(roleSlug)
	roleContext := strings.TrimSpace(fm.body)

	return liteConfig{
		profile:       profileKey,
		systemPrompt:  systemBase,
		roleContext:   roleContext,
		enabledTools:  enabled,
		external:      false,
		workspace:     workspace,
		customTools:   custom,
		maxToolRounds: rm.MaxToolRounds,
	}
}
