// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// agentLangFromConfig extracts the agent's stored UI language from its config
// JSON (set at create time). Empty ⇒ English default.
func agentLangFromConfig(config string) string {
	if strings.TrimSpace(config) == "" {
		return ""
	}
	var c struct {
		Lang string `json:"lang"`
	}
	_ = json.Unmarshal([]byte(config), &c)
	return c.Lang
}

// agentRoleTemplatesFS carries the official role templates baked into the binary
// so a fresh install seeds them to ~/cicy-ai/memory/agents/ on first boot. Each
// role is a DIRECTORY: <slug>/role.md (English, the default persona),
// <slug>/role.zh.md (Chinese), <slug>/meta.yaml (profile/tools/name/greeting,
// each with an EN default + _zh variant). The persona file is what lands in the
// agent's CLAUDE.md/AGENTS.md; the greeting lives only in meta.yaml (shown in the
// chat view, never injected into the agent's memory).
//
// memorySeedFS is the ONE dedicated seed directory baked into the binary
// (embed/memory-seed/). It holds everything a fresh install seeds into
// ~/cicy-ai/memory/:
//
//	global.md              → ~/cicy-ai/memory/global.md      (base rules)
//	projects/default.md    → ~/cicy-ai/memory/projects/default.md
//	agents/<slug>/…        → ~/cicy-ai/memory/agents/<slug>/ (role templates)
//
// This is the single source of what ships as the default seed — edit these
// files to change the packaged defaults; nothing is read from ~/cicy-ai at
// build time.
//
//go:embed embed/memory-seed
var memorySeedFS embed.FS

const memorySeedRoot = "embed/memory-seed"

// agentRoleTemplatesFS is retained as an alias so the role readers below keep
// working; it points at the same embedded FS.
var agentRoleTemplatesFS = memorySeedFS

// roleMeta is the parsed <slug>/meta.yaml.
type roleMeta struct {
	Profile    string   `yaml:"profile"`
	Tools      []string `yaml:"tools"`
	Name       string   `yaml:"name"`
	NameZh     string   `yaml:"name_zh"`
	Greeting   string   `yaml:"greeting"`
	GreetingZh string   `yaml:"greeting_zh"`
	// MaxToolRounds overrides the per-turn model→tool→model round cap for this
	// role (0 = use the global default cicyDefaultMaxToolRounds). Tool-heavy roles
	// (知识/审计专员 doing batch governance) can raise it; trivial roles lower it.
	MaxToolRounds int `yaml:"max_tool_rounds"`
}

// langIsZh reports whether a UI/agent language code selects Chinese.
func langIsZh(lang string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "zh")
}

// roleDir is the on-disk directory for a role template.
func roleDir(slug string) string {
	clean := sanitizeTemplateSlug(slug)
	if clean == "" {
		return ""
	}
	return filepath.Join(agentTemplatesDir(), clean)
}

// rolePersonaFile returns the persona filename for a language ("role.md" for the
// English default, "role.zh.md" for Chinese).
func rolePersonaFile(lang string) string {
	if langIsZh(lang) {
		return "role.zh.md"
	}
	return "role.md"
}

// readRoleFile reads a file from a role's dir, preferring the user-editable
// ~/cicy-ai/memory/agents/<slug>/<name> and falling back to the embedded seed.
func readRoleFile(slug, name string) string {
	clean := sanitizeTemplateSlug(slug)
	if clean == "" {
		return ""
	}
	if b, err := os.ReadFile(filepath.Join(agentTemplatesDir(), clean, name)); err == nil && strings.TrimSpace(string(b)) != "" {
		return string(b)
	}
	// Fall back to the packaged seed (embed/memory-seed/agents/…).
	if b, ok := memorySeedRead("agents/" + clean + "/" + name); ok {
		return string(b)
	}
	return ""
}

// cicySystemBase returns THIS role's system prompt — the `system` field sent on
// every cicy turn. Each role owns its own system.md (one per role); the agent's
// specific role/identity still lives in its AGENTS.md, carried as message context.
// Prefers the user-editable ~/cicy-ai/memory/agents/<slug>/system.md, falls back
// to the embedded per-role seed (via readRoleFile).
func cicySystemBase(roleSlug string) string {
	return strings.TrimSpace(readRoleFile(roleSlug, "system.md"))
}

// loadRoleMeta parses a role's meta.yaml (disk-or-embed). Empty struct if absent.
func loadRoleMeta(slug string) roleMeta {
	var m roleMeta
	if raw := readRoleFile(slug, "meta.yaml"); raw != "" {
		_ = yaml.Unmarshal([]byte(raw), &m)
	}
	return m
}

// roleMetaName / roleMetaGreeting pick the language variant (English default).
func roleMetaName(slug, lang string) string {
	m := loadRoleMeta(slug)
	if langIsZh(lang) && strings.TrimSpace(m.NameZh) != "" {
		return m.NameZh
	}
	if strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	return sanitizeTemplateSlug(slug)
}

func roleMetaGreeting(slug, lang string) string {
	m := loadRoleMeta(slug)
	if langIsZh(lang) && strings.TrimSpace(m.GreetingZh) != "" {
		return strings.TrimSpace(m.GreetingZh)
	}
	return strings.TrimSpace(m.Greeting)
}

// Layered, user-editable memory templates. On agent creation the composed
// content is copied verbatim into the new agent's native guidance file
// (CLAUDE.md / AGENTS.md / .kiro/steering/memory.md). There is NO inheritance
// and NO gateway injection: every agent owns a self-contained file on disk that
// both gateway and non-gateway CLIs read natively, keeping the two paths
// consistent.
//
// Layers (composed in order), mapping to AI-COMPANY-ARCHITECTURE.md:
//   global   (always)         ~/cicy-ai/memory/global.md       — base rules
//   project  (optional)       ~/cicy-ai/memory/projects/<slug>.md — project rules (§4.3)
//   role     (optional, TODO) ~/cicy-ai/memory/agents/<slug>.md   — role charter (§5 HR 手册)
//
// Placeholders {{AGENT_ID}}, {{WORKSPACE}}, {{AGENT_TYPE}} are substituted per
// agent at copy time. The `agents/` (role) layer is scaffolded here for
// extension; no UI wires it yet.

func cicyMemoryDir() string {
	return filepath.Join(cicyRootDir, "memory")
}

func globalMemoryTemplatePath() string {
	return filepath.Join(cicyMemoryDir(), "global.md")
}

func projectTemplatesDir() string {
	return filepath.Join(cicyMemoryDir(), "projects")
}

func agentTemplatesDir() string {
	return filepath.Join(cicyMemoryDir(), "agents")
}

// templateSlugRE strips only the characters that are unsafe in a filename or
// could escape the templates directory (path separators, control chars, and
// the Windows-reserved set). Unicode letters — including Chinese — are kept so
// templates can carry human-readable names.
var templateSlugRE = regexp.MustCompile(`[\x00-\x1f/\\:*?"<>|]+`)

func sanitizeTemplateSlug(slug string) string {
	s := strings.TrimSpace(slug)
	s = strings.TrimSuffix(s, ".md")
	s = templateSlugRE.ReplaceAllString(s, "-")
	// Drop leading/trailing dots/dashes/spaces so a slug can't become a hidden
	// file ("."), a traversal token (".."), or carry stray padding.
	s = strings.Trim(s, "-. ")
	return s
}

func projectTemplatePath(slug string) string {
	clean := sanitizeTemplateSlug(slug)
	if clean == "" {
		return ""
	}
	return filepath.Join(projectTemplatesDir(), clean+".md")
}

// roleTemplatePath returns the editable English persona file for a role
// (<slug>/role.md) — used by the template-editor API.
func roleTemplatePath(slug string) string {
	d := roleDir(slug)
	if d == "" {
		return ""
	}
	return filepath.Join(d, "role.md")
}

// reservedTemplateSlugs are system base/default templates that live in
// ~/cicy-ai/memory/agents/ (so there's ONE template source, editable on disk)
// but must NOT show up in the role picker — they're the cicy system-prompt base
// and the no-role default charter, not selectable personas.
// reservedTemplateSlugs hides system base templates from the create-agent role
// picker. Empty now: "assistant" is a normal, selectable role (the default), so
// nothing is hidden.
var reservedTemplateSlugs = map[string]bool{}

// listTemplateSlugs returns the user-selectable .md slugs in dir (sorted),
// excluding reserved system templates. Used for PROJECT templates (flat .md).
func listTemplateSlugs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		if reservedTemplateSlugs[slug] {
			continue
		}
		out = append(out, slug)
	}
	sort.Strings(out)
	return out
}

// listRoleSlugs returns the user-selectable role slugs (subdirs of
// ~/cicy-ai/memory/agents/ that contain a role.md), excluding reserved templates.
func listRoleSlugs() []string {
	entries, err := os.ReadDir(agentTemplatesDir())
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || reservedTemplateSlugs[e.Name()] {
			continue
		}
		if _, err := os.Stat(filepath.Join(agentTemplatesDir(), e.Name(), "role.md")); err != nil {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// roleFileSortKey orders the files inside a role dir for display: the English
// persona first, then the Chinese persona, then meta.yaml, then anything else.
func roleFileSortKey(name string) int {
	switch name {
	case "role.md":
		return 0
	case "role.zh.md":
		return 1
	case "meta.yaml":
		return 2
	}
	return 3
}

// listRoleFiles returns the files inside a role's dir (union of the on-disk
// ~/cicy-ai/memory/agents/<slug>/ and the embedded seed), ordered role.md,
// role.zh.md, meta.yaml, then the rest alphabetically. Sub-dirs are skipped.
func listRoleFiles(slug string) []string {
	clean := sanitizeTemplateSlug(slug)
	if clean == "" {
		return nil
	}
	set := map[string]bool{}
	if entries, err := os.ReadDir(filepath.Join(agentTemplatesDir(), clean)); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				set[e.Name()] = true
			}
		}
	}
	if entries, err := agentRoleTemplatesFS.ReadDir("embed/memory-seed/agents/" + clean); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				set[e.Name()] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if ki, kj := roleFileSortKey(out[i]), roleFileSortKey(out[j]); ki != kj {
			return ki < kj
		}
		return out[i] < out[j]
	})
	return out
}

// roleDirInfo is one role folder plus the files under it, for the memory editor.
type roleDirInfo struct {
	Slug  string   `json:"slug"`
	Files []string `json:"files"`
}

// listAllRoleDirs returns EVERY role folder under ~/cicy-ai/memory/agents/
// (including the reserved base/default templates, which the editor may edit even
// though they're hidden from the create-agent picker), merged with embedded
// seeds, each with its file list. Backs the memory editor's role tree.
func listAllRoleDirs() []roleDirInfo {
	names := map[string]bool{}
	if entries, err := os.ReadDir(agentTemplatesDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				names[e.Name()] = true
			}
		}
	}
	if entries, err := agentRoleTemplatesFS.ReadDir("embed/memory-seed/agents"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				names[e.Name()] = true
			}
		}
	}
	slugs := make([]string, 0, len(names))
	for n := range names {
		slugs = append(slugs, n)
	}
	sort.Strings(slugs)
	out := make([]roleDirInfo, 0, len(slugs))
	for _, s := range slugs {
		out = append(out, roleDirInfo{Slug: s, Files: listRoleFiles(s)})
	}
	return out
}

// sanitizeRoleFileName validates a filename addressed within a role dir
// (role.md, role.zh.md, meta.yaml, …): a bare basename with no separators,
// no traversal, and not hidden. Returns "" when unsafe.
func sanitizeRoleFileName(name string) string {
	name = strings.TrimSpace(strings.Trim(name, "/"))
	if name == "" || strings.ContainsAny(name, `/\`) {
		return ""
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return ""
	}
	if filepath.Base(name) != name {
		return ""
	}
	return name
}

// roleTemplateRaw returns a role's persona markdown for a language
// (<slug>/role.md for English, <slug>/role.zh.md for Chinese), disk-or-embed.
// The persona carries NO frontmatter (metadata is in meta.yaml) and NO greeting.
func roleTemplateRaw(slug, lang string) string {
	if raw := readRoleFile(slug, rolePersonaFile(lang)); raw != "" {
		return raw
	}
	// Chinese requested but no zh persona → fall back to the English default.
	if langIsZh(lang) {
		return readRoleFile(slug, "role.md")
	}
	return ""
}

// roleTemplateBody returns a role's English persona body, trimmed. (Personas
// carry no frontmatter now, so this is just the trimmed file.)
func roleTemplateBody(slug string) string {
	return strings.TrimSpace(roleTemplateRaw(slug, ""))
}

func listProjectTemplates() []string { return listTemplateSlugs(projectTemplatesDir()) }
func listRoleTemplates() []string    { return listRoleSlugs() }

// defaultGlobalMemoryTemplate returns the text seeded into
// ~/cicy-ai/memory/global.md on first boot — read from the packaged seed dir
// (embed/memory-seed/global.md), with a tiny literal fallback.
func defaultGlobalMemoryTemplate() string {
	return memorySeedFile("global.md",
		"## Constraints\n\n- Projects: always create and clone into `~/projects`.\n")
}

// memorySeedRead reads a seed file (path relative to the seed root) from the
// dedicated seed dir baked into the binary (embed/memory-seed/). ok=false if
// absent.
func memorySeedRead(rel string) ([]byte, bool) {
	if b, err := memorySeedFS.ReadFile(memorySeedRoot + "/" + rel); err == nil {
		return b, true
	}
	return nil, false
}

// memorySeedFile is memorySeedRead as a string with a fallback for missing/empty.
func memorySeedFile(rel, fallback string) string {
	if b, ok := memorySeedRead(rel); ok && strings.TrimSpace(string(b)) != "" {
		return string(b)
	}
	return fallback
}

// ensureGlobalMemoryTemplate writes the default template if the file is missing.
// Existing user edits are never overwritten.
func ensureGlobalMemoryTemplate() string {
	path := globalMemoryTemplatePath()
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		log.Printf("[memory-template] mkdir failed: %v", err)
		return path
	}
	if err := os.WriteFile(path, []byte(defaultGlobalMemoryTemplate()), 0644); err != nil {
		log.Printf("[memory-template] seed write failed: %v", err)
	}
	return path
}

// ensureRoleMemoryTemplates seeds the official role templates into
// ~/cicy-ai/memory/agents/<slug>.md on first boot — same contract as
// ensureGlobalMemoryTemplate: write when missing, never overwrite user edits.
// The official roster's cicy role agents (项目经理 / 产品经理 / …) compose their
// AGENTS.md from these, so they must be on disk before the roster is created.
func ensureRoleMemoryTemplates() {
	dir := agentTemplatesDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[memory-template] mkdir agents failed: %v", err)
		return
	}
	const root = memorySeedRoot + "/agents"
	roleDirs, err := memorySeedFS.ReadDir(root)
	if err != nil {
		log.Printf("[memory-template] read embedded role templates failed: %v", err)
		return
	}
	for _, rd := range roleDirs {
		if !rd.IsDir() {
			continue
		}
		dstDir := filepath.Join(dir, rd.Name())
		_ = os.MkdirAll(dstDir, 0755)
		files, _ := memorySeedFS.ReadDir(root + "/" + rd.Name())
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			dst := filepath.Join(dstDir, f.Name())
			if _, err := os.Stat(dst); err == nil {
				continue // never clobber a user-edited file
			}
			raw, err := memorySeedFS.ReadFile(root + "/" + rd.Name() + "/" + f.Name())
			if err != nil {
				continue
			}
			if err := os.WriteFile(dst, raw, 0644); err != nil {
				log.Printf("[memory-template] seed role %s/%s failed: %v", rd.Name(), f.Name(), err)
			}
		}
	}
}

// extractOpeningSection pulls the text under a role template's `## 开场白`
// heading (until the next heading or trailing HTML comment). Returns "" if the
// section is absent.
func extractOpeningSection(md string) string {
	lines := strings.Split(md, "\n")
	var out []string
	in := false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if !in {
			if strings.HasPrefix(t, "## 开场白") {
				in = true
			}
			continue
		}
		if strings.HasPrefix(t, "## ") || strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "<!--") {
			break
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// agentOpeningGreeting returns the opening line shown when an employee's chat
// history is empty. It comes from the role's meta.yaml (`greeting` / `greeting_zh`),
// picked by the agent's stored language (default English).
func agentOpeningGreeting(shortID string) string {
	var roleTemplate, title, agentType, workspace, config string
	_ = store.QueryRow(
		"SELECT COALESCE(role_template,''), COALESCE(title,''), COALESCE(agent_type,''), COALESCE(workspace,''), COALESCE(config,'') FROM agent_config WHERE pane_id=?",
		shortID+":main.0",
	).Scan(&roleTemplate, &title, &agentType, &workspace, &config)
	// Only cicy (lite) agents have an opening greeting; coding agents
	// (claude/codex/…) get none so the chat view stays blank for them.
	if normalizeAgentType(agentType) != "cicy" {
		return ""
	}
	lang := agentLangFromConfig(config)
	if slug := sanitizeTemplateSlug(roleTemplate); slug != "" {
		if g := roleMetaGreeting(slug, lang); g != "" {
			return substituteTemplatePlaceholders(g, shortID, workspace, agentType)
		}
	}
	if strings.TrimSpace(title) == "" {
		title = shortID
	}
	if langIsZh(lang) {
		return fmt.Sprintf("你好,我是%s。有什么可以帮你的?", title)
	}
	return fmt.Sprintf("Hi, I'm %s. How can I help?", title)
}

func loadTemplateFile(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func substituteTemplatePlaceholders(content, agentID, workspace, agentType string) string {
	rep := strings.NewReplacer(
		"{{AGENT_ID}}", strings.Split(agentID, ":")[0],
		"{{WORKSPACE}}", workspace,
		"{{AGENT_TYPE}}", normalizeAgentType(agentType),
		"{{PLATFORM}}", platformDisplayName(),
		"{{PLATFORM_SETUP}}", platformSetupGuidance(),
	)
	return rep.Replace(content)
}

// platformDisplayName is the daemon host's OS, for charters that adapt by platform.
func platformDisplayName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	default:
		return runtime.GOOS
	}
}

// platformSetupGuidance is the platform-specific install path for the 团队专员
// charter ({{PLATFORM_SETUP}}): Windows can use WSL or Docker; macOS/Linux use
// Docker. Substituted at seed time so the prompt genuinely differs per platform.
func platformSetupGuidance() string {
	if runtime.GOOS == "windows" {
		return "你在 **Windows**:托管你的这台 cicy-code 是**原生(非 Docker)直接跑在系统上、占着本机 8008** 的——它能跑 cicy 类 agent(项目经理/各专员/你),但 **Windows 没 tmux,编排不了 claude / codex / gemini / opencode 这些 CLI 编程 agent**。\n" +
			"所以要一整支带编程 agent 的团队,**统一走 Docker**(给一个有 tmux 的 Linux 运行时):装 **Docker Desktop**(它内部用 WSL2 后端,但你只管 Docker,不单独碰 WSL),按下面命令起 cicy-code 容器(主机端口**从 8009 起**,8008 被原生那台占着)。\n" +
			"探测 `docker version`;没有就引导装 Docker Desktop。"
	}
	return "你在 **" + platformDisplayName() + "**:本机原生 cicy-code(占 8008,tmux 可用,能跑整支含编程 agent 的团队)已在跑。你的活是用 **Docker 无限扩编**——需要更多算力/并行/隔离不同项目时,起额外 cicy-code 容器(主机端口 **8009 起**,8008 被原生那台占着),各自一套独立团队。\n" +
		"探测 `docker version` 确认 Docker 在跑;没装就引导装;就绪后按下面命令起容器。"
}

// composeAgentMemory builds the seed content for a new agent's guidance file:
// global (always) + project (when projectSlug set) + role (when roleSlug set),
// placeholder-substituted. Returns the default global template when nothing is
// on disk so creation never produces an empty file.
func composeAgentMemory(agentID, workspace, agentType, projectSlug, roleSlug, lang string) string {
	ensureGlobalMemoryTemplate()
	ensureDefaultProject()

	parts := []string{}
	if global := strings.TrimSpace(loadTemplateFile(globalMemoryTemplatePath())); global != "" {
		parts = append(parts, global)
	}
	// Every agent carries a project's rules; an unassigned agent falls back to the
	// "default" project (projectSlugOrDefault), so default.md reaches everyone.
	if project := projectRulesBody(projectSlugOrDefault(projectSlug)); project != "" {
		parts = append(parts, project)
	}
	if slug := sanitizeTemplateSlug(roleSlug); slug != "" {
		if role := strings.TrimSpace(roleTemplateRaw(slug, lang)); role != "" {
			parts = append(parts, role)
		} else if ca, ok := customAgentFor(slug); ok && strings.TrimSpace(ca.Body) != "" {
			// User-authored custom agent: persona lives in ~/cicy-ai/agents/<slug>/AGENT.md
			parts = append(parts, strings.TrimSpace(ca.Body))
		}
	}

	composed := strings.Join(parts, "\n\n")
	if strings.TrimSpace(composed) == "" {
		composed = defaultGlobalMemoryTemplate()
	}
	return substituteTemplatePlaceholders(composed, agentID, workspace, agentType)
}
