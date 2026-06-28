package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// agentRoleTemplatesFS carries the official role templates baked into the binary
// so a fresh install seeds them to ~/cicy-ai/memory/agents/ on first boot — same
// shipping model as the global template, just a directory of them.
//
//go:embed embed/agent-roles/*.md
var agentRoleTemplatesFS embed.FS

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

func roleTemplatePath(slug string) string {
	clean := sanitizeTemplateSlug(slug)
	if clean == "" {
		return ""
	}
	return filepath.Join(agentTemplatesDir(), clean+".md")
}

// reservedTemplateSlugs are system base/default templates that live in
// ~/cicy-ai/memory/agents/ (so there's ONE template source, editable on disk)
// but must NOT show up in the role picker — they're the cicy system-prompt base
// and the no-role default charter, not selectable personas.
var reservedTemplateSlugs = map[string]bool{
	"base-assistant":  true,
	"base-dispatcher": true,
	"default-charter": true,
}

// listTemplateSlugs returns the user-selectable .md slugs in dir (sorted),
// excluding reserved system templates.
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

// roleTemplateRaw returns a role template's RAW markdown (frontmatter intact),
// preferring the user-editable ~/cicy-ai/memory/agents/<slug>.md and falling
// back to the embedded seed so a base/default template is never empty before
// first-boot seeding. This keeps memory/agents/ the single template source.
func roleTemplateRaw(slug string) string {
	clean := sanitizeTemplateSlug(slug)
	if clean == "" {
		return ""
	}
	if raw := loadTemplateFile(roleTemplatePath(clean)); strings.TrimSpace(raw) != "" {
		return raw
	}
	if b, err := agentRoleTemplatesFS.ReadFile("embed/agent-roles/" + clean + ".md"); err == nil {
		return string(b)
	}
	return ""
}

// roleTemplateBody returns a role template's body (frontmatter stripped) via the
// same disk-or-embed source as roleTemplateRaw.
func roleTemplateBody(slug string) string {
	return strings.TrimSpace(parseLiteFrontmatter(roleTemplateRaw(slug)).body)
}

func listProjectTemplates() []string { return listTemplateSlugs(projectTemplatesDir()) }
func listRoleTemplates() []string    { return listTemplateSlugs(agentTemplatesDir()) }

// defaultGlobalMemoryTemplate seeds ~/cicy-ai/memory/global.md the first time
// it is needed.
const defaultGlobalMemoryTemplate = `## Collaboration

You can collaborate with other agents through the ` + "`cicy-agent`" + ` skill:
- ` + "`cicy-agent ls`" + ` — discover other agents
- ` + "`cicy-agent msg <agent> <text>`" + ` — dispatch a task or ask for help
- ` + "`cicy-agent capture <agent>`" + ` — check another agent's progress

Run ` + "`cicy-agent help`" + ` first to see all subcommands (note it is ` + "`help`" + `, not ` + "`--help`" + `).

## Constraints

<!-- Write the mandatory constraints every agent must follow here. -->
`

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
	if err := os.WriteFile(path, []byte(defaultGlobalMemoryTemplate), 0644); err != nil {
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
	const root = "embed/agent-roles"
	entries, err := agentRoleTemplatesFS.ReadDir(root)
	if err != nil {
		log.Printf("[memory-template] read embedded role templates failed: %v", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		dst := filepath.Join(dir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // never clobber a user-edited template
		}
		raw, err := agentRoleTemplatesFS.ReadFile(root + "/" + e.Name())
		if err != nil {
			continue
		}
		if err := os.WriteFile(dst, raw, 0644); err != nil {
			log.Printf("[memory-template] seed role %s failed: %v", e.Name(), err)
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
// history is empty. Resolution: employee-template config (employees.yaml
// `greeting`) → the role .md `## 开场白` (back-compat) → a generic line from the
// title. The first two are hot-read, so editing the config takes effect live.
func agentOpeningGreeting(shortID string) string {
	var roleTemplate, title, agentType, workspace string
	_ = store.QueryRow(
		"SELECT COALESCE(role_template,''), COALESCE(title,''), COALESCE(agent_type,''), COALESCE(workspace,'') FROM agent_config WHERE pane_id=?",
		shortID+":main.0",
	).Scan(&roleTemplate, &title, &agentType, &workspace)
	// Only cicy (lite) agents have an opening greeting; coding agents
	// (claude/codex/…) get none so the chat view stays blank for them.
	if normalizeAgentType(agentType) != "cicy" {
		return ""
	}
	if slug := sanitizeTemplateSlug(roleTemplate); slug != "" {
		// 1) employees.yaml template greeting (the configurable source)
		if g := strings.TrimSpace(employeeTemplateGreeting(slug)); g != "" {
			return substituteTemplatePlaceholders(g, shortID, workspace, agentType)
		}
		// 2) legacy: the role .md `## 开场白` section
		if g := extractOpeningSection(loadTemplateFile(roleTemplatePath(slug))); g != "" {
			return substituteTemplatePlaceholders(g, shortID, workspace, agentType)
		}
	}
	if strings.TrimSpace(title) == "" {
		title = shortID
	}
	return fmt.Sprintf("你好,我是%s。有什么可以帮你的?", title)
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
func composeAgentMemory(agentID, workspace, agentType, projectSlug, roleSlug string) string {
	ensureGlobalMemoryTemplate()
	ensureDefaultProject()

	parts := []string{}
	if global := strings.TrimSpace(loadTemplateFile(globalMemoryTemplatePath())); global != "" {
		parts = append(parts, global)
	}
	// Every agent carries a project's rules; an unassigned agent falls back to the
	// "default" project (projectSlugOrDefault), so default.md reaches everyone.
	// projectRulesBody strips the YAML frontmatter (name) so only the rules body
	// lands in the agent's CLAUDE.md — never the metadata header.
	if project := projectRulesBody(projectSlugOrDefault(projectSlug)); project != "" {
		parts = append(parts, project)
	}
	if slug := sanitizeTemplateSlug(roleSlug); slug != "" {
		if role := strings.TrimSpace(loadTemplateFile(roleTemplatePath(slug))); role != "" {
			parts = append(parts, role)
		} else if ca, ok := customAgentFor(slug); ok && strings.TrimSpace(ca.Body) != "" {
			// User-authored custom agent: persona lives in ~/cicy-ai/agents/<slug>/AGENT.md
			parts = append(parts, strings.TrimSpace(ca.Body))
		}
	}

	composed := strings.Join(parts, "\n\n")
	if strings.TrimSpace(composed) == "" {
		composed = defaultGlobalMemoryTemplate
	}
	return substituteTemplatePlaceholders(composed, agentID, workspace, agentType)
}
