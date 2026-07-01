package main

// Lite-agent configuration layer (todo #103, Barry 2026-06-07).
//
// "Go is only the engine." Profiles, tool groups, custom tools and grants all
// live in DATA (~/cicy-ai/db/lite-config.json), hot-read each turn. The Go side
// keeps only: the built-in tool IMPLEMENTATIONS (cicyRunTool switch +
// a2a_*), the three big system-prompt consts, and the safety logic. Adding a
// profile / group / tool / grant is a config edit, no rebuild, no restart.
//
// Trust model (L1–L4), enforced in resolveLiteConfig + the custom-tool runner:
//   L1  this JSON file ........ declares everything; only Barry/authed write it.
//   L2  role templates ........ may SELECT a profile + groups (frontmatter).
//   L3  workspace AGENTS.md .... `tools:` may only NARROW (effective = ∩ grantable).
//   L4  session / a2a content . changes NOTHING — the tool set is computed from
//                               L1–L3 before the turn, with zero coupling to
//                               message text. Injection cannot grant a tool.
// External profiles (liaison) are hard-capped: grants cannot expand them, and
// the custom-tool runner refuses to execute for an external profile (double gate).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// liteToolParam constrains one fill-in value for a custom tool's argv template.
// v1 supports string values only — they slot into fixed argv positions, never a
// shell, so the worst an LLM controls is a parameter's VALUE, never the command.
type liteToolParam struct {
	Type     string   `json:"type"`     // "string" (only kind honoured in v1)
	Pattern  string   `json:"pattern"`  // full-match regexp the value must satisfy
	Enum     []string `json:"enum"`     // if set, value must be one of these
	MaxLen   int      `json:"maxLen"`   // reject longer values (default 4096)
	Required bool     `json:"required"` // missing → error
}

// liteCustomTool is a data-declared tool: a FIXED argv template whose "{param}"
// slots are filled from schema-checked params, executed without a shell.
type liteCustomTool struct {
	Description string                   `json:"description"`
	Argv        []string                 `json:"argv"` // e.g. ["agent-chrome","eval","{idx}","{expr}"]
	Params      map[string]liteToolParam `json:"params"`
	Risk        string                   `json:"risk"`          // "exec" | "readonly" (informational)
	TimeoutSec  int                      `json:"timeout_sec"`   // default 30
	MaxOutputKB int                      `json:"max_output_kb"` // default 16
}

// liteProfileCfg is a personality preset, now data instead of a Go const.
type liteProfileCfg struct {
	Name string `json:"name"`
	// SystemBase: "@dispatcher" / "@assistant" / "@liaison" reference the Go
	// prompt consts (kept in Go so they stay byte-stable for prompt caching);
	// "@role:<slug>" pulls a role template's body; anything else is literal text.
	SystemBase      string   `json:"systemBase"`
	DefaultGroups   []string `json:"defaultGroups"`   // enabled when AGENTS.md has no `tools:`
	GrantableGroups []string `json:"grantableGroups"` // the max set frontmatter may select from
	External        bool     `json:"external"`        // hard-capped; grants/custom tools never apply
}

type liteGrants struct {
	ByAgent   map[string][]string `json:"by_agent"`   // short id  → group names granted
	ByProfile map[string][]string `json:"by_profile"` // profile   → group names granted
}

type liteConfigFile struct {
	Profiles    map[string]liteProfileCfg `json:"profiles"`
	ToolGroups  map[string][]string       `json:"toolGroups"`  // group → tool/customtool names
	CustomTools map[string]liteCustomTool `json:"customTools"` // name  → declaration
	Grants      liteGrants                `json:"grants"`
}

// ── defaults: an exact reproduction of the former Go consts, so behaviour is
// unchanged when the JSON file is absent or only partially overrides. ─────────

func defaultLiteConfig() liteConfigFile {
	// lite-config.json defines ONLY tools now: tool GROUPS (group → tool names)
	// + the tool DEFINITIONS (customTools). No profiles/grants/grantable — a
	// role's meta.yaml `tools:` is the single source of what it gets.
	//
	// cicy's tools are deliberately tiny: `skill` (discover + read any installed
	// skill's SKILL.md — the cicy-todo / cicy-agent / … ecosystem) and `shell`
	// (run the skill's CLI, or anything else). No per-skill hardcoded tools —
	// 装个 skill 即可用, 改 skill 即更新.
	return liteConfigFile{
		ToolGroups: map[string][]string{
			"core": {"skill", "shell"},
		},
		// No built-in custom tools: every cicy agent works through `skill` + `shell`.
		// (The former `audit` group / audit_* tools were retired when the 审计策略
		// 专员 moved to the cicy-audit-policy skill — install a skill, get its CLI.)
		CustomTools: map[string]liteCustomTool{},
	}
}

func liteConfigPath() string {
	return filepath.Join(cicyRootDir, "db", "lite-config.json")
}

var (
	liteCfgMu     sync.Mutex
	liteCfgCache  *liteConfigFile
	liteCfgMtime  int64
	liteCfgLoaded bool
)

// loadLiteConfig returns defaults merged with ~/cicy-ai/db/lite-config.json
// (file entries win per map key). Hot-read: re-parsed only when the file's
// mtime changes, so every turn sees the latest without a restart.
func loadLiteConfig() liteConfigFile {
	liteCfgMu.Lock()
	defer liteCfgMu.Unlock()

	path := liteConfigPath()
	var mtime int64
	if fi, err := os.Stat(path); err == nil {
		mtime = fi.ModTime().UnixNano()
	}
	if liteCfgLoaded && liteCfgCache != nil && mtime == liteCfgMtime {
		return *liteCfgCache
	}

	merged := defaultLiteConfig()
	if raw, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
		var fromFile liteConfigFile
		if jsonErr := json.Unmarshal(raw, &fromFile); jsonErr == nil {
			mergeLiteConfig(&merged, fromFile)
		}
		// A malformed file falls through to pure defaults — never a crash, never
		// a silent privilege change. (The loader stays permissive; the safety
		// invariants live in resolveLiteConfig, not here.)
	}

	liteCfgCache = &merged
	liteCfgMtime = mtime
	liteCfgLoaded = true
	return merged
}

// mergeLiteConfig folds file-declared entries over the defaults at map-key
// granularity (so Barry can add one profile/group/tool without re-declaring the
// built-ins). Grants are merged per sub-map.
func mergeLiteConfig(base *liteConfigFile, f liteConfigFile) {
	for k, v := range f.Profiles {
		base.Profiles[k] = v
	}
	for k, v := range f.ToolGroups {
		base.ToolGroups[k] = v
	}
	for k, v := range f.CustomTools {
		base.CustomTools[k] = v
	}
	if f.Grants.ByAgent != nil {
		if base.Grants.ByAgent == nil {
			base.Grants.ByAgent = map[string][]string{}
		}
		for k, v := range f.Grants.ByAgent {
			base.Grants.ByAgent[k] = v
		}
	}
	if f.Grants.ByProfile != nil {
		if base.Grants.ByProfile == nil {
			base.Grants.ByProfile = map[string][]string{}
		}
		for k, v := range f.Grants.ByProfile {
			base.Grants.ByProfile[k] = v
		}
	}
}

// resetLiteConfigCache forces the next loadLiteConfig to re-read (tests use it).
func resetLiteConfigCache() {
	liteCfgMu.Lock()
	liteCfgLoaded = false
	liteCfgCache = nil
	liteCfgMtime = 0
	liteCfgMu.Unlock()
}

// resolveSystemBase turns a profile's SystemBase reference into prompt text.
// All persona/base text lives in ~/cicy-ai/memory/agents/ (seeded from
// embed/agent-roles/), so the system base is loaded from there too — there is
// no hardcoded prompt text in Go.
func resolveSystemBase(ref string) string {
	switch ref {
	// One universal base. @dispatcher is a legacy alias kept only so any old
	// config still resolves — every cicy agent now shares "assistant".
	case "@assistant", "@dispatcher":
		return roleTemplateBody("assistant")
	}
	if slug := strings.TrimPrefix(ref, "@role:"); slug != ref {
		return roleTemplateBody(slug)
	}
	return ref // literal prompt text
}

// expandGroups turns a list of group-or-bare-tool names into the concrete set of
// tool names, using the config's tool-group table. Unknown names are treated as
// bare tool names (so a grant/frontmatter can name a single tool directly).
func expandGroups(groups []string, table map[string][]string) map[string]bool {
	out := map[string]bool{}
	for _, g := range groups {
		if names, ok := table[g]; ok {
			for _, n := range names {
				out[n] = true
			}
		} else if g = strings.TrimSpace(g); g != "" {
			out[g] = true
		}
	}
	return out
}
