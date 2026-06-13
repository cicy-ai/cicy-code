package main

// Custom agents — "build an agent like you build a skill".
//
// A custom agent is a user-authored cicy (lite) agent persona, stored
// self-contained in one file, mirroring the skill author layout:
//
//	~/cicy-ai/agents/<slug>/AGENT.md
//	---
//	name: 销售助手
//	tools: [coordinate, shell]    # tool-group SELECTION (groups in db/lite-config.json)
//	model: claude-opus-4-8         # optional default model for new instances
//	---
//	你是一个销售助手……             ← body becomes the agent's persona (role charter)
//
// Why this file (and not employees.yaml + memory/agents/*.md split): a custom
// agent is ONE thing the user authored, so it lives as ONE document — name,
// tools, model and persona together — exactly like SKILL.md. It is hot-read
// (no restart) and plugs into the SAME role lookup chain the built-in roles
// use, so creating an instance with agent_type=cicy + role_template=<slug>
// just works:
//   - employeeTemplateFor()  → tools + persona prompt   (see employee_template.go)
//   - composeAgentMemory()   → persona seeded into the new workspace AGENTS.md
//   - doCreatePane()         → default_model applied from the AGENT.md
//
// There is no embedded seed and no migration: the directory only ever holds
// what the user creates here.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// customAgent is one authored agent definition (the parsed AGENT.md).
type customAgent struct {
	Slug  string   `json:"slug"`
	Name  string   `json:"name"`
	Tools []string `json:"tools"`
	Model string   `json:"model"`
	Body  string   `json:"body"`
}

func customAgentsDir() string {
	return filepath.Join(cicyRootDir, "agents")
}

// customAgentPath maps a slug to its AGENT.md file. Returns "" for an invalid
// (empty after sanitising) slug so callers never touch a stray path.
func customAgentPath(slug string) string {
	clean := sanitizeTemplateSlug(slug)
	if clean == "" {
		return ""
	}
	return filepath.Join(customAgentsDir(), clean, "AGENT.md")
}

// parseCustomAgentDoc parses an AGENT.md document into a customAgent (slug set
// by the caller from the directory name). Reuses the shared lite frontmatter
// parser so name/tools/model/body semantics match the rest of the lite system.
func parseCustomAgentDoc(slug, raw string) customAgent {
	fm := parseLiteFrontmatter(raw)
	name := strings.TrimSpace(fm.name)
	if name == "" {
		name = slug
	}
	tools := fm.tools
	if tools == nil {
		tools = []string{}
	}
	return customAgent{
		Slug:  slug,
		Name:  name,
		Tools: tools,
		Model: strings.TrimSpace(fm.model),
		Body:  strings.TrimSpace(fm.body),
	}
}

// customAgentFor returns the authored agent for a slug, plus whether it exists.
// Hot-read straight from disk each call (definitions are small and edited rarely
// — no cache needed, and a fresh read means edits/new agents appear instantly).
func customAgentFor(slug string) (customAgent, bool) {
	path := customAgentPath(slug)
	if path == "" {
		return customAgent{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return customAgent{}, false
	}
	return parseCustomAgentDoc(sanitizeTemplateSlug(slug), string(raw)), true
}

// scanCustomAgents lists every authored agent under ~/cicy-ai/agents/ (sorted by
// directory walk order, i.e. alphabetical). Missing directory → empty list.
func scanCustomAgents() []customAgent {
	entries, err := os.ReadDir(customAgentsDir())
	if err != nil {
		return nil
	}
	out := make([]customAgent, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := sanitizeTemplateSlug(e.Name())
		if slug == "" {
			continue
		}
		if ca, ok := customAgentFor(slug); ok {
			out = append(out, ca)
		}
	}
	return out
}

// renderCustomAgentDoc serialises a customAgent back to AGENT.md text.
func renderCustomAgentDoc(ca customAgent) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + strings.TrimSpace(ca.Name) + "\n")
	b.WriteString("tools: [" + strings.Join(ca.Tools, ", ") + "]\n")
	if m := strings.TrimSpace(ca.Model); m != "" {
		b.WriteString("model: " + m + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(ca.Body))
	b.WriteString("\n")
	return b.String()
}

// writeCustomAgent persists an authored agent to disk, returning its final slug.
func writeCustomAgent(ca customAgent) (string, error) {
	path := customAgentPath(ca.Slug)
	if path == "" {
		return "", os.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(renderCustomAgentDoc(ca)), 0644); err != nil {
		return "", err
	}
	return sanitizeTemplateSlug(ca.Slug), nil
}

// deleteCustomAgent removes an authored agent's directory.
func deleteCustomAgent(slug string) error {
	clean := sanitizeTemplateSlug(slug)
	if clean == "" {
		return os.ErrInvalid
	}
	return os.RemoveAll(filepath.Join(customAgentsDir(), clean))
}

// customAgentToolGroups returns the tool-group names a custom (cicy/dispatcher)
// agent may select from — the dispatcher profile's grantable set, since a custom
// agent runs under the dispatcher profile (no frontmatter profile in its seeded
// AGENTS.md). Selecting a group outside this set would be narrowed away anyway
// (effective = selected ∩ grantable), so the UI only offers what actually works.
func customAgentToolGroups() []string {
	cfg := loadLiteConfig()
	if prof, ok := cfg.Profiles["dispatcher"]; ok && len(prof.GrantableGroups) > 0 {
		return append([]string{}, prof.GrantableGroups...)
	}
	return []string{"coordinate", "onboard", "shell"}
}

// ── HTTP ─────────────────────────────────────────────────────────────────────
//
//	GET    /api/custom-agents        → { agents: [...], toolGroups: [...] }
//	POST   /api/custom-agents        ← { name, tools[], model, body }  (create/overwrite)
//	DELETE /api/custom-agents/<slug>

func handleCustomAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		J(w, M{
			"agents":     scanCustomAgents(),
			"toolGroups": customAgentToolGroups(),
			"dir":        customAgentsDir(),
		})
	case http.MethodPost:
		var body struct {
			Name  string   `json:"name"`
			Tools []string `json:"tools"`
			Model string   `json:"model"`
			Body  string   `json:"body"`
		}
		if err := readBody(r, &body); err != nil {
			httpErr(w, 400, "invalid body")
			return
		}
		slug := sanitizeTemplateSlug(body.Name)
		if slug == "" {
			httpErr(w, 400, "name required")
			return
		}
		if body.Tools == nil {
			body.Tools = []string{}
		}
		saved, err := writeCustomAgent(customAgent{
			Slug:  slug,
			Name:  strings.TrimSpace(body.Name),
			Tools: body.Tools,
			Model: strings.TrimSpace(body.Model),
			Body:  body.Body,
		})
		if err != nil {
			httpErr(w, 500, "save failed: "+err.Error())
			return
		}
		ca, _ := customAgentFor(saved)
		J(w, M{"saved": true, "agent": ca})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleCustomAgentAction(w http.ResponseWriter, r *http.Request) {
	slug := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/custom-agents/"), "/")
	if r.Method != http.MethodDelete {
		httpErr(w, 405, "method not allowed")
		return
	}
	if sanitizeTemplateSlug(slug) == "" {
		httpErr(w, 400, "invalid slug")
		return
	}
	if err := deleteCustomAgent(slug); err != nil {
		httpErr(w, 500, "delete failed: "+err.Error())
		return
	}
	J(w, M{"deleted": true, "slug": sanitizeTemplateSlug(slug)})
}
