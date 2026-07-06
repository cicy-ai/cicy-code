// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Memory-template management API. Backs the create-agent dialog's project
// template dropdown + inline edit/new, the role template library, and the
// inspector's Memory tab editor.
//
//	GET  /api/memory/templates                 → { global, projects: [...], roles: [...] }
//	GET  /api/memory/templates/global          → { scope, name, content, path }
//	PUT  /api/memory/templates/global          ← { content }
//	GET  /api/memory/templates/project/<slug>  → { scope, name, content, path }
//	PUT  /api/memory/templates/project/<slug>  ← { content }   (creates if absent)
//	GET  /api/memory/templates/role/<slug>     → { scope, name, content, path }
//	GET  /api/memory/templates/agent/<paneID>  → { scope, filename, content, path }
//	PUT  /api/memory/templates/agent/<paneID>  ← { content }   (the agent's own
//	                                              CLAUDE.md / AGENTS.md in-place)

func handleMemoryTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, 405, "method not allowed")
		return
	}
	J(w, M{
		"global":   M{"name": "global", "path": globalMemoryTemplatePath()},
		"projects": listProjectTemplates(),
		"roles":    listRoleTemplates(),
		"roleDirs": listAllRoleDirs(),
		"dir":      cicyMemoryDir(),
	})
}

// resolveTemplatePath maps a "<scope>[/<slug>]" suffix to an absolute path.
// Returns ("", "", "") when the scope/slug is invalid.
func resolveTemplatePath(suffix string) (scope, name, path string) {
	suffix = strings.Trim(strings.TrimSpace(suffix), "/")
	if suffix == "" {
		return "", "", ""
	}
	parts := strings.SplitN(suffix, "/", 2)
	switch parts[0] {
	case "config":
		// The tool/agent config (~/cicy-ai/db/lite-config.json) — a single fixed
		// file edited from the memory view's Tools section.
		return "config", "lite-config.json", liteConfigPath()
	case "global":
		return "global", "global", globalMemoryTemplatePath()
	case "project":
		if len(parts) < 2 {
			return "", "", ""
		}
		slug := sanitizeTemplateSlug(parts[1])
		if slug == "" {
			return "", "", ""
		}
		return "project", slug, projectTemplatePath(slug)
	case "role":
		if len(parts) < 2 {
			return "", "", ""
		}
		// role/<slug>[/<file>]: a bare slug edits the English persona (role.md);
		// an explicit file addresses any file in the role dir (role.zh.md,
		// meta.yaml, …).
		rest := strings.SplitN(parts[1], "/", 2)
		slug := sanitizeTemplateSlug(rest[0])
		if slug == "" {
			return "", "", ""
		}
		if len(rest) == 2 {
			file := sanitizeRoleFileName(rest[1])
			if file == "" {
				return "", "", ""
			}
			return "role", slug + "/" + file, filepath.Join(roleDir(slug), file)
		}
		return "role", slug, roleTemplatePath(slug)
	}
	return "", "", ""
}

// agentGuidancePath resolves an agent's own native guidance file
// (CLAUDE.md / AGENTS.md / .kiro/steering/memory.md) from its pane id, by
// looking up its workspace + agent_type in agent_config. Returns the absolute
// path plus the workspace-relative filename. ok is false when the agent has no
// guidance file (unknown pane, empty workspace, or an agent_type that doesn't
// get one).
func agentGuidancePath(paneID string) (path, filename string, ok bool) {
	pane := normPaneID(strings.TrimSpace(paneID))
	if pane == "" {
		return "", "", false
	}
	var workspace, agentType sql.NullString
	if err := store.QueryRow("SELECT workspace, agent_type FROM agent_config WHERE pane_id=?", pane).Scan(&workspace, &agentType); err != nil {
		return "", "", false
	}
	ws := strings.TrimSpace(workspace.String)
	rel := guidanceFilenameForAgentType(agentType.String)
	if ws == "" || rel == "" {
		return "", "", false
	}
	return filepath.Join(ws, rel), rel, true
}

// handleAgentMemoryFile serves GET/PUT for the agent's own in-workspace
// guidance file. DELETE is intentionally unsupported — it is the agent's live
// memory, not a reusable template.
func handleAgentMemoryFile(w http.ResponseWriter, r *http.Request, paneID string) {
	path, filename, ok := agentGuidancePath(paneID)
	if !ok {
		httpErr(w, 404, "agent guidance file not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		J(w, M{"scope": "agent", "name": shortPaneID(normPaneID(paneID)), "filename": filename, "content": loadTemplateFile(path), "path": path, "exists": fileExistsPlain(path)})
	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		if err := readBody(r, &body); err != nil {
			httpErr(w, 400, "invalid body")
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			httpErr(w, 500, "mkdir failed: "+err.Error())
			return
		}
		if err := os.WriteFile(path, []byte(body.Content), 0644); err != nil {
			httpErr(w, 500, "write failed: "+err.Error())
			return
		}
		J(w, M{"scope": "agent", "filename": filename, "path": path, "saved": true})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleMemoryTemplateByName(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/api/memory/templates/")
	// agent/<paneID>: the agent's own guidance file (DB-resolved, not a slug).
	if trimmed := strings.Trim(strings.TrimSpace(suffix), "/"); strings.HasPrefix(trimmed, "agent/") {
		handleAgentMemoryFile(w, r, strings.TrimPrefix(trimmed, "agent/"))
		return
	}
	scope, name, path := resolveTemplatePath(suffix)
	if path == "" {
		httpErr(w, 400, "invalid template name")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// global seeds itself; projects may legitimately not exist yet (new).
		if scope == "global" {
			ensureGlobalMemoryTemplate()
		}
		content := loadTemplateFile(path)
		// role files fall back to their embedded seed when no on-disk override
		// exists, so the editor always shows the effective content (and deleting
		// a seeded file cleanly reverts to seed rather than showing blank).
		if scope == "role" && content == "" {
			rslug, rfile := name, "role.md"
			if i := strings.IndexByte(name, '/'); i >= 0 {
				rslug, rfile = name[:i], name[i+1:]
			}
			content = readRoleFile(rslug, rfile)
		}
		// lite-config falls back to the effective DEFAULT config (pretty JSON) when
		// no file exists yet, so the editor shows what's actually in effect and a
		// save materialises it for editing.
		if scope == "config" && strings.TrimSpace(content) == "" {
			if b, err := json.MarshalIndent(defaultLiteConfig(), "", "  "); err == nil {
				content = string(b) + "\n"
			}
		}
		J(w, M{"scope": scope, "name": name, "content": content, "path": path, "exists": fileExistsPlain(path)})
	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		if err := readBody(r, &body); err != nil {
			httpErr(w, 400, "invalid body")
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			httpErr(w, 500, "mkdir failed: "+err.Error())
			return
		}
		if err := os.WriteFile(path, []byte(body.Content), 0644); err != nil {
			httpErr(w, 500, "write failed: "+err.Error())
			return
		}
		J(w, M{"scope": scope, "name": name, "path": path, "saved": true})
	case http.MethodDelete:
		if scope == "global" {
			httpErr(w, 400, "cannot delete the global template")
			return
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			httpErr(w, 500, "delete failed: "+err.Error())
			return
		}
		J(w, M{"scope": scope, "name": name, "deleted": true})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func fileExistsPlain(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
