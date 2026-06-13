package main

import (
	"fmt"
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

// assistantSystemPromptBase — a neutral, customer/audience-facing conversational
// agent with NO internal coordination tools. The intended base for support,
// sales, PR and similar roles; the concrete persona comes from AGENTS.md body.
const assistantSystemPromptBase = `你是一个轻量对话助理。具体身份、语气和职责由下面的角色说明决定。

通用准则:
- 紧扣角色说明设定的身份与边界,不要自称 AI 或跳出人设,除非角色说明要求。
- 默认中文;用户用其他语言则跟随。
- 简洁、直接、礼貌;先给结论或答案,必要时再展开。
- 只在角色说明授权的范围内承诺或回答;不确定或超出范围时如实说明并给出下一步建议。
- 不编造事实、价格、政策或承诺。`

// Profiles / tool groups / custom tools / grants are no longer Go consts — they
// live in ~/cicy-ai/db/lite-config.json (see lite_config.go), with baked
// defaults reproducing the former built-ins. resolveLiteConfig reads them.

// liteConfig is the resolved per-instance configuration.
type liteConfig struct {
	profile      string
	systemPrompt string          // base + body + identity line, cache-stable
	enabledTools map[string]bool // empty ⇒ pure chat
	external     bool            // profile is outward-facing (liaison): exec/custom tools refused
	workspace    string          // for custom-tool cwd
	customTools  map[string]liteCustomTool
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

	profileKey := fm.profile
	prof, ok := cfg.Profiles[profileKey]
	if !ok {
		profileKey = "dispatcher"
		prof = cfg.Profiles["dispatcher"]
	}

	// grantable: the ceiling of what this instance may use.
	grantGroups := append([]string{}, prof.GrantableGroups...)
	if !prof.External {
		grantGroups = append(grantGroups, cfg.Grants.ByProfile[profileKey]...)
		grantGroups = append(grantGroups, cfg.Grants.ByAgent[shortID]...)
	}
	grantable := expandGroups(grantGroups, cfg.ToolGroups)

	// selected groups, in priority order:
	//   employees.yaml template `tools:` (this employee's role) >
	//   workspace AGENTS.md frontmatter `tools:` > profile default.
	// Still narrowed by `grantable` below — the security model (effective =
	// selected ∩ grantable, L3 narrow-only) is unchanged regardless of source.
	roleSlug := employeeRoleSlug(shortID) // this employee's role-template slug (for employees.yaml lookups)
	selectGroups := prof.DefaultGroups
	if fm.hasTools {
		selectGroups = fm.tools
	}
	if tools := employeeTemplateTools(roleSlug); len(tools) > 0 {
		selectGroups = tools
	}
	selected := expandGroups(selectGroups, cfg.ToolGroups)

	// effective = selected ∩ grantable (narrow-only).
	enabled := map[string]bool{}
	for name := range selected {
		if grantable[name] {
			enabled[name] = true
		}
	}

	// Custom tools available to this instance (subset of enabled that are
	// declared custom). External profiles get none (defense in depth — the
	// runner also refuses, and grantable already excludes them).
	custom := map[string]liteCustomTool{}
	if !prof.External {
		for name := range enabled {
			if t, isCustom := cfg.CustomTools[name]; isCustom {
				custom[name] = t
			}
		}
	}

	// System prompt = profile base + AGENTS.md body + identity line. Stays
	// byte-stable across turns (cache prefix) — no timestamps.
	prompt := resolveSystemBase(prof.SystemBase)
	// persona: employees.yaml template `prompt:` (if set) overrides the AGENTS.md
	// body; otherwise the role .md body is used as before.
	body := strings.TrimSpace(fm.body)
	if p := strings.TrimSpace(employeeTemplatePrompt(roleSlug)); p != "" {
		body = p
	}
	if body != "" {
		prompt += "\n\n# 角色说明\n" + body
	}
	prompt += fmt.Sprintf("\n\n你自己的 AGENT_ID 是 %s。", shortID)

	return liteConfig{
		profile:      profileKey,
		systemPrompt: prompt,
		enabledTools: enabled,
		external:     prof.External,
		workspace:    workspace,
		customTools:  custom,
	}
}
