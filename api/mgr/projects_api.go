// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func ensureGroupProjectDefinition(groupID int64, name string, isDefault bool) (string, string, error) {
	slug := fmt.Sprintf("project-%d", groupID)
	if isDefault {
		slug = defaultProjectSlug
	}
	path := projectTemplatePath(slug)
	if path == "" {
		return "", "", fmt.Errorf("invalid project template path")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", "", err
		}
		seed := fmt.Sprintf("## 项目角色\n\n项目：%s\n", strings.TrimSpace(name))
		if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
			return "", "", err
		}
	} else if err != nil {
		return "", "", err
	}
	return slug, loadTemplateFile(path), nil
}

func groupProjectDefinitionPath(groupID int64, isDefault bool) string {
	slug := fmt.Sprintf("project-%d", groupID)
	if isDefault {
		slug = defaultProjectSlug
	}
	return projectTemplatePath(slug)
}

func assignPaneToProjectTemplate(paneID, projectTemplate string) error {
	slug := projectSlugOrDefault(projectTemplate)
	var groupID int64
	if slug == defaultProjectSlug {
		if err := store.QueryRow("SELECT id FROM agent_groups WHERE COALESCE(is_default,0)=1 LIMIT 1").Scan(&groupID); err != nil {
			return err
		}
	} else {
		var id int64
		if _, err := fmt.Sscanf(slug, "project-%d", &id); err != nil || id <= 0 || slug != fmt.Sprintf("project-%d", id) {
			return nil
		}
		if err := store.QueryRow("SELECT id FROM agent_groups WHERE id=?", id).Scan(&groupID); err != nil {
			return err
		}
	}
	_, err := store.Exec(store.InsertIgnore("group_windows", []string{"group_id", "win_id", "win_type", "ref_id"}), groupID, paneID, "agent_ttyd", paneID)
	return err
}

func writeGroupProjectDefinition(groupID int64, name string, isDefault bool, content string) (string, error) {
	slug, _, err := ensureGroupProjectDefinition(groupID, name, isDefault)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(projectTemplatePath(slug), []byte(content), 0o644); err != nil {
		return "", err
	}
	return slug, nil
}

// Project = a first-class, user-created project: name + rules.
// Stored as ~/cicy-ai/memory/projects/<slug>.md with a YAML frontmatter
// (name) and the body as the project rules (composed into agents' CLAUDE.md).

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
			// Seed from the dedicated seed dir (embed/memory-seed/projects/
			// default.md); tiny literal fallback if the embed read ever fails.
			// No frontmatter — the project name IS the file slug.
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			_ = os.WriteFile(path, []byte(memorySeedFile("projects/default.md", "## Project\n")), 0o644)
		}
	}
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

// readProjectMeta returns a project's {slug, name}. The display name IS the file
// slug — project .md files carry no frontmatter; the filename is the name.
func readProjectMeta(slug string) projectMeta {
	clean := sanitizeTemplateSlug(slug)
	return projectMeta{Slug: clean, Name: clean}
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
		// No frontmatter — the project name IS the file slug. The .md holds just
		// the rules body (composed into agents' CLAUDE.md).
		rules := strings.TrimSpace(req.Rules)
		if rules == "" {
			rules = "# " + name
		}
		if !strings.HasSuffix(rules, "\n") {
			rules += "\n"
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			httpErr(w, http.StatusInternalServerError, "mkdir_failed")
			return
		}
		if err := os.WriteFile(path, []byte(rules), 0o644); err != nil {
			httpErr(w, http.StatusInternalServerError, "write_failed")
			return
		}
		J(w, projectMeta{Slug: slug, Name: slug})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}
