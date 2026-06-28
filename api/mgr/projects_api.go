package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Project = a first-class, user-created project: name + rules.
// Stored as ~/cicy-ai/memory/projects/<slug>.md with a YAML frontmatter
// (name) and the body as the project rules (composed into agents' CLAUDE.md).
// Each project also owns a shared claude memory pool at
// ~/cicy-ai/memory/project-mem/<slug>/ — claude workers whose project_template
// is <slug> get CLAUDE_COWORK_MEMORY_PATH_OVERRIDE pointed there, so the same
// project's claude agents share auto-memory (A writes → B recalls).

type projectMeta struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// defaultProjectSlug is the out-of-the-box project every agent belongs to until
// it's assigned a real one. Makes A→B shared memory work from first boot.
const defaultProjectSlug = "default"

// projectSlugOrDefault normalizes a (possibly empty) project_template to a slug,
// falling back to the default project so unassigned claude agents still share.
func projectSlugOrDefault(projectTemplate string) string {
	if slug := sanitizeTemplateSlug(projectTemplate); slug != "" {
		return slug
	}
	return defaultProjectSlug
}

// ensureDefaultProject seeds projects/default.md + its memory pool on first boot
// (idempotent). Called from setup.
func ensureDefaultProject() {
	path := projectTemplatePath(defaultProjectSlug)
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			body := "---\nname: " + yamlScalar("default") + "\n---\n" +
				"# Default Project\n\n<!-- Out-of-the-box default project: every agent not assigned to a real project shares this one memory pool. -->\n"
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			_ = os.WriteFile(path, []byte(body), 0o644)
		}
	}
	ensureProjectMemDir(defaultProjectSlug)
}

// projectMemBaseDir is the parent of all per-project shared memory pools.
func projectMemBaseDir() string {
	return filepath.Join(cicyMemoryDir(), "project-mem")
}

// projectMemDir is the shared claude auto-memory pool for one project.
func projectMemDir(slug string) string {
	clean := sanitizeTemplateSlug(slug)
	if clean == "" {
		return ""
	}
	return filepath.Join(projectMemBaseDir(), clean)
}

// ensureProjectMemDir creates the pool dir (idempotent). Returns the path, or
// "" for an empty/invalid slug.
func ensureProjectMemDir(slug string) string {
	dir := projectMemDir(slug)
	if dir == "" {
		return ""
	}
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// projectFrontmatter is the YAML header of a project .md.
type projectFrontmatter struct {
	Name string `yaml:"name"`
}

// splitFrontmatter separates a leading `---\n…\n---\n` YAML block from the body.
// Returns (frontmatterYAML, body). When there is no frontmatter, fm is "" and
// body is the whole content.
func splitFrontmatter(content string) (fm, body string) {
	s := strings.TrimLeft(content, "\ufeff") // tolerate BOM
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return "", content
	}
	rest := s[strings.IndexByte(s, '\n')+1:]
	// find the closing fence at the start of a line
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return "", content
	}
	fm = rest[:idx]
	after := rest[idx+1:] // starts at "---"
	if nl := strings.IndexByte(after, '\n'); nl >= 0 {
		body = after[nl+1:]
	} else {
		body = ""
	}
	return fm, body
}

// projectRulesBody returns a project's rules text with any frontmatter stripped
// (so it can be composed into an agent's CLAUDE.md without leaking YAML).
func projectRulesBody(slug string) string {
	path := projectTemplatePath(slug)
	if path == "" {
		return ""
	}
	_, body := splitFrontmatter(loadTemplateFile(path))
	return strings.TrimSpace(body)
}

// readProjectMeta parses a project's {slug, name} from its .md frontmatter.
// Missing name falls back to the slug.
func readProjectMeta(slug string) projectMeta {
	clean := sanitizeTemplateSlug(slug)
	meta := projectMeta{Slug: clean, Name: clean}
	path := projectTemplatePath(clean)
	if path == "" {
		return meta
	}
	fm, _ := splitFrontmatter(loadTemplateFile(path))
	if strings.TrimSpace(fm) != "" {
		var pf projectFrontmatter
		if yaml.Unmarshal([]byte(fm), &pf) == nil {
			if n := strings.TrimSpace(pf.Name); n != "" {
				meta.Name = n
			}
		}
	}
	return meta
}

// listProjectsWithMeta returns every registered project with its metadata.
func listProjectsWithMeta() []projectMeta {
	slugs := listProjectTemplates()
	out := make([]projectMeta, 0, len(slugs))
	for _, s := range slugs {
		out = append(out, readProjectMeta(s))
	}
	return out
}

// handleProjects backs the create-agent dialog's project picker.
//
//	GET  /api/projects               → [{slug,name}]
//	POST /api/projects {name,rules?} → creates projects/<slug>.md + pool dir
func handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		J(w, M{"projects": listProjectsWithMeta()})
	case http.MethodPost:
		var req struct {
			Name  string `json:"name"`
			Rules string `json:"rules"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid_json")
			return
		}
		name := strings.TrimSpace(req.Name)
		slug := sanitizeTemplateSlug(name)
		if slug == "" {
			httpErr(w, http.StatusBadRequest, "name_required")
			return
		}
		path := projectTemplatePath(slug)
		if path == "" {
			httpErr(w, http.StatusBadRequest, "bad_name")
			return
		}
		rules := strings.TrimSpace(req.Rules)
		if rules == "" {
			rules = "# " + name + "\n\n<!-- 项目规则:agents 选了本项目会把下面内容并入 CLAUDE.md。 -->\n"
		}
		var b strings.Builder
		b.WriteString("---\n")
		b.WriteString("name: " + yamlScalar(name) + "\n")
		b.WriteString("---\n")
		b.WriteString(rules)
		if !strings.HasSuffix(rules, "\n") {
			b.WriteString("\n")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			httpErr(w, http.StatusInternalServerError, "mkdir_failed")
			return
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			httpErr(w, http.StatusInternalServerError, "write_failed")
			return
		}
		ensureProjectMemDir(slug)
		J(w, projectMeta{Slug: slug, Name: name})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

// yamlScalar quotes a scalar for a frontmatter value when needed (paths with
// special chars, leading ~, etc.). Double-quote + escape is always safe.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
