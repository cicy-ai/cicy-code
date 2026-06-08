package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"
	"regexp"
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

// listTemplateSlugs returns the .md slugs in dir (sorted).
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
		out = append(out, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(out)
	return out
}

func listProjectTemplates() []string { return listTemplateSlugs(projectTemplatesDir()) }
func listRoleTemplates() []string    { return listTemplateSlugs(agentTemplatesDir()) }

// defaultGlobalMemoryTemplate seeds ~/cicy-ai/memory/global.md the first time
// it is needed.
const defaultGlobalMemoryTemplate = `# Agent Memory

- Your AGENT_ID is ` + "`{{AGENT_ID}}`" + `
- Your current directory is ` + "`{{WORKSPACE}}`" + `
- Your agent_type is ` + "`{{AGENT_TYPE}}`" + `
- Ask the user to set your project directory

## Collaboration

You run inside tmux and can collaborate with other agents through the ` + "`cicy-agent`" + ` skill:
- ` + "`cicy-agent ls`" + ` — discover other panes
- ` + "`cicy-agent msg <pane> <text>`" + ` — dispatch a task or ask for help
- ` + "`cicy-agent capture <pane>`" + ` — check another agent's progress

Run ` + "`cicy-agent help`" + ` first to see all subcommands (note it is ` + "`help`" + `, not ` + "`--help`" + `).

## Tool routing (intent → which tool to use)

- todo / task list / "what's left" → ` + "`cicy-todo`" + ` (` + "`cicy-todo add/start/done/drop`" + `); don't write these into scratch notes
- message another agent → ` + "`cicy-agent msg`" + `
- fork / inherit an agent → ` + "`cicy-agent fork`" + `
- read the raw conversation / feed context to a fork → ` + "`agent-summary`" + `

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
	)
	return rep.Replace(content)
}

// composeAgentMemory builds the seed content for a new agent's guidance file:
// global (always) + project (when projectSlug set) + role (when roleSlug set),
// placeholder-substituted. Returns the default global template when nothing is
// on disk so creation never produces an empty file.
func composeAgentMemory(agentID, workspace, agentType, projectSlug, roleSlug string) string {
	ensureGlobalMemoryTemplate()

	parts := []string{}
	if global := strings.TrimSpace(loadTemplateFile(globalMemoryTemplatePath())); global != "" {
		parts = append(parts, global)
	}
	if slug := sanitizeTemplateSlug(projectSlug); slug != "" {
		if project := strings.TrimSpace(loadTemplateFile(projectTemplatePath(slug))); project != "" {
			parts = append(parts, project)
		}
	}
	if slug := sanitizeTemplateSlug(roleSlug); slug != "" {
		if role := strings.TrimSpace(loadTemplateFile(roleTemplatePath(slug))); role != "" {
			parts = append(parts, role)
		}
	}

	composed := strings.Join(parts, "\n\n")
	if strings.TrimSpace(composed) == "" {
		composed = defaultGlobalMemoryTemplate
	}
	return substituteTemplatePlaceholders(composed, agentID, workspace, agentType)
}
