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
	return liteConfigFile{
		Profiles: map[string]liteProfileCfg{
			// ONE universal base. Every cicy agent is an "assistant"; its identity
			// and behavior come entirely from its system prompt + selected tools —
			// no dispatcher/liaison profiles, no a2a/external concept anymore.
			"assistant": {Name: "CiCy", SystemBase: "@assistant",
				DefaultGroups:   []string{"coordinate"},
				GrantableGroups: []string{"coordinate", "onboard", "shell", "handoff"}},
		},
		ToolGroups: map[string][]string{
			"coordinate": {"todo_add", "todo_list", "todo_update", "agent_list", "agent_msg", "agent_capture"},
			"handoff":    {"agent_list", "agent_msg", "agent_capture"},
			// HR-only: pull an offline/standalone agent onto the team and bring it
			// online (bind under master; cicy → warm session, CLI → launch pane).
			"onboard": {"agent_online"},
			// A real shell (PowerShell on Windows, bash on unix) — e.g. the team
			// helper installing Docker + cicy-code hands-on.
			"shell": {"shell"},
			// audit: the 审计策略专员 (audit advisor) calls the /api/audit/* API as
			// native tools. Declared here (image default), grantable to every
			// assistant, selected only by the 审计策略专员 charter's `tools:` line.
			"audit": auditGroupToolNames(),
		},
		CustomTools: auditCustomTools(),
		// Grant the audit group to every assistant (the ceiling). Only the
		// 审计策略专员 charter actually selects it, so no other agent gets the
		// audit tools unless its charter opts in.
		Grants: liteGrants{ByAgent: map[string][]string{}, ByProfile: map[string][]string{"assistant": {"audit"}}},
	}
}

// auditGroupToolNames is the ordered tool list of the "audit" group. Kept in
// sync with the keys of auditCustomTools().
func auditGroupToolNames() []string {
	return []string{
		"audit_events", "audit_event_get", "audit_snapshot", "audit_stats", "audit_agents",
		"audit_policy_get", "audit_policy_set", "audit_policy_effective", "audit_rule_test",
		"audit_allowlist_get", "audit_allowlist_add", "audit_allowlist_remove",
	}
}

// auditCustomTools declares the /api/audit/* endpoints as schema-checked custom
// tools. Each runs `bash -lc <script> audit [args...]`: the script self-reads
// the api_token from ~/cicy-ai/global.json (custom-tool env is washed of tokens,
// liteToolSafeEnv) and pipes the Authorization header to curl via `-K -` so the
// token never appears in argv / ps / the [lite-tool] log. Params arrive as
// positional $1/$2 (never interpolated into the script text) and POST bodies are
// built with python3 json.dumps so values are escaped safely.
func auditCustomTools() map[string]liteCustomTool {
	// auditAPIBase is THIS instance's in-process API origin, derived from the live
	// PORT (not a hardcoded 8008) so audit tools hit their own instance's gateway.
	auditAPIBase := "http://127.0.0.1:" + runtimeAPIBasePort()
	// shared bash prelude: TOK = api_token (read inside the subprocess).
	const tok = `TOK=$(python3 -c "import json,os;print(json.load(open(os.path.expanduser('~/cicy-ai/global.json')))['api_token'])")` + "\n"
	// auth pipe: write the bearer header to curl's stdin config (-K -).
	const hdr = `printf 'header = "Authorization: Bearer %s"\n' "$TOK" | `

	get := func(urlExpr string) []string {
		return []string{"bash", "-c", tok + hdr + `curl -s -K - "` + urlExpr + `"`, "audit"}
	}
	post := func(method, bodyVar, urlExpr string) string {
		return tok + hdr + `curl -s -K - -H "Content-Type: application/json" -X ` + method +
			` --data "` + bodyVar + `" "` + urlExpr + `"`
	}

	return map[string]liteCustomTool{
		// ── read / interpret logs ──────────────────────────────────────────
		"audit_events": {
			Description: "列出审计事件(命中/流量)。query 是原样查询串,如 severity=high,critical&agent=w-12&limit=50&direction=outbound。空=最近全部。",
			Argv:        append(get(auditAPIBase+`/api/audit/events?$1`), "{query}"),
			Params:      map[string]liteToolParam{"query": {Type: "string", MaxLen: 300, Pattern: `[A-Za-z0-9=&_,%.:\-]*`}},
			Risk:        "readonly", TimeoutSec: 20, MaxOutputKB: 64,
		},
		"audit_event_get": {
			Description: "取单条审计事件全量详情。id 形如 evt_xxx。",
			Argv:        append(get(auditAPIBase+`/api/audit/events/$1`), "{id}"),
			Params:      map[string]liteToolParam{"id": {Type: "string", Required: true, MaxLen: 80, Pattern: `[A-Za-z0-9_\-]+`}},
			Risk:        "readonly", TimeoutSec: 20, MaxOutputKB: 64,
		},
		"audit_snapshot": {
			Description: "按 snapshot_ref 取某事件的取证快照(原始未脱敏请求上下文)。ref 取自事件 meta.snapshot_ref。",
			Argv:        append(get(auditAPIBase+`/api/audit/snapshot?ref=$1`), "{ref}"),
			Params:      map[string]liteToolParam{"ref": {Type: "string", Required: true, MaxLen: 160, Pattern: `[A-Za-z0-9_\-./:]+`}},
			Risk:        "readonly", TimeoutSec: 20, MaxOutputKB: 128,
		},
		"audit_stats": {
			Description: "审计聚合统计(按规则/agent/严重度的命中分布)——评估规则吵不吵、谁反复命中。",
			Argv:        get(auditAPIBase + `/api/audit/stats`),
			Risk:        "readonly", TimeoutSec: 20, MaxOutputKB: 64,
		},
		"audit_agents": {
			Description: "列出有审计事件的 agent。",
			Argv:        get(auditAPIBase + `/api/audit/agents`),
			Risk:        "readonly", TimeoutSec: 20, MaxOutputKB: 32,
		},
		// ── configure rules / policy ───────────────────────────────────────
		"audit_policy_get": {
			Description: "读取全局审计策略 policy.json(规则集/严重度/动作/allowlist/阈值)。改策略前先 get 存底。",
			Argv:        get(auditAPIBase + `/api/audit/policy`),
			Risk:        "readonly", TimeoutSec: 20, MaxOutputKB: 128,
		},
		"audit_policy_effective": {
			Description: "读取某 agent 合并后的有效策略(全局 ⊕ per-agent override)。",
			Argv:        append(get(auditAPIBase+`/api/audit/policy/effective/$1`), "{agent}"),
			Params:      map[string]liteToolParam{"agent": {Type: "string", Required: true, MaxLen: 80, Pattern: `[A-Za-z0-9:_.\-]+`}},
			Risk:        "readonly", TimeoutSec: 20, MaxOutputKB: 64,
		},
		"audit_policy_set": {
			Description: "写入全局审计策略(原子写+热加载)。policy_json 必须是完整 policy 对象的 JSON。高破坏(全局 block、批量禁规则)须 Barry 拍。务必先 audit_policy_get 存回滚底。",
			Argv:        []string{"bash", "-c", post("POST", "$1", auditAPIBase+`/api/audit/policy`), "audit", "{policy_json}"},
			Params:      map[string]liteToolParam{"policy_json": {Type: "string", Required: true, MaxLen: 200000}},
			Risk:        "exec", TimeoutSec: 25, MaxOutputKB: 16,
		},
		"audit_rule_test": {
			Description: "试跑一条规则匹配器:match_type=regex|js,pattern 是表达式,text 是待测样本。返回是否命中。加规则前先验。",
			Argv: []string{"bash", "-c",
				`TOK=$(python3 -c "import json,os;print(json.load(open(os.path.expanduser('~/cicy-ai/global.json')))['api_token'])")` + "\n" +
					`BODY=$(python3 -c "import json,sys;print(json.dumps({'match_type':sys.argv[1],'pattern':sys.argv[2],'text':sys.argv[3]}))" "$1" "$2" "$3")` + "\n" +
					hdr + `curl -s -K - -H "Content-Type: application/json" -X POST --data "$BODY" "` + auditAPIBase + `/api/audit/rules/test"`,
				"audit", "{match_type}", "{pattern}", "{text}"},
			Params: map[string]liteToolParam{
				"match_type": {Type: "string", Required: true, Enum: []string{"regex", "js"}},
				"pattern":    {Type: "string", Required: true, MaxLen: 2000},
				"text":       {Type: "string", Required: true, MaxLen: 8000},
			},
			Risk: "readonly", TimeoutSec: 20, MaxOutputKB: 16,
		},
		// ── allowlist (误报治理) ────────────────────────────────────────────
		"audit_allowlist_get": {
			Description: "查看白名单(content_hashes / paths / agents 三类)。",
			Argv:        get(auditAPIBase + `/api/audit/allowlist`),
			Risk:        "readonly", TimeoutSec: 20, MaxOutputKB: 32,
		},
		"audit_allowlist_add": {
			Description: "把某内容哈希加入白名单(标误报,以后相同内容不再告警)。sha256 形如 sha256:<64hex>,reason 写清为什么是误报。",
			Argv: []string{"bash", "-c",
				`TOK=$(python3 -c "import json,os;print(json.load(open(os.path.expanduser('~/cicy-ai/global.json')))['api_token'])")` + "\n" +
					`BODY=$(python3 -c "import json,sys;print(json.dumps({'sha256':sys.argv[1],'reason':sys.argv[2]}))" "$1" "$2")` + "\n" +
					hdr + `curl -s -K - -H "Content-Type: application/json" -X POST --data "$BODY" "` + auditAPIBase + `/api/audit/allowlist/content"`,
				"audit", "{sha256}", "{reason}"},
			Params: map[string]liteToolParam{
				"sha256": {Type: "string", Required: true, MaxLen: 80, Pattern: `sha256:[a-f0-9]{64}`},
				"reason": {Type: "string", Required: true, MaxLen: 200},
			},
			Risk: "exec", TimeoutSec: 20, MaxOutputKB: 8,
		},
		"audit_allowlist_remove": {
			Description: "从白名单移除一条。category=content_hash|path|agent,value 是要移除的具体值。",
			Argv: []string{"bash", "-c",
				`TOK=$(python3 -c "import json,os;print(json.load(open(os.path.expanduser('~/cicy-ai/global.json')))['api_token'])")` + "\n" +
					`BODY=$(python3 -c "import json,sys;print(json.dumps({'category':sys.argv[1],'value':sys.argv[2]}))" "$1" "$2")` + "\n" +
					hdr + `curl -s -K - -H "Content-Type: application/json" -X DELETE --data "$BODY" "` + auditAPIBase + `/api/audit/allowlist"`,
				"audit", "{category}", "{value}"},
			Params: map[string]liteToolParam{
				"category": {Type: "string", Required: true, Enum: []string{"content_hash", "path", "agent"}},
				"value":    {Type: "string", Required: true, MaxLen: 160},
			},
			Risk: "exec", TimeoutSec: 20, MaxOutputKB: 8,
		},
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
