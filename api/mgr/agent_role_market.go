// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Use the same Cloud-owned catalog flow as the public Skill Market. The Cloud
// sync workflow validates and snapshots the upstream immutable role registry,
// so desktop clients do not depend on GitHub's raw endpoint directly.
const publicAgentRoleRegistry = "https://cicy-ai.com/agent-role-catalog.json"

const publicAgentRoleRepository = "https://github.com/cicy-ai/cicy-agent-roles"

// defaultAgentRoleSlugs are installed from the public Role Market after the
// cicy-agent-role skill is available. Installation is additive: an existing
// directory is always treated as user-owned and is never updated or replaced.
var defaultAgentRoleSlugs = []string{
	"assistant",
	"audit-policy-specialist",
	"desktop-assist",
	"knowledge-specialist",
	"koubo",
}

var agentRoleSlugRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

type agentRoleMarketEntry struct {
	Slug          string   `json:"slug"`
	Version       string   `json:"version"`
	Name          string   `json:"name"`
	NameZH        string   `json:"name_zh"`
	Description   string   `json:"description"`
	DescriptionZH string   `json:"description_zh"`
	Tags          []string `json:"tags"`
	Installed     bool     `json:"installed"`
	InstalledVer  string   `json:"installed_version,omitempty"`
	HasUpdate     bool     `json:"has_update"`
	Modified      bool     `json:"modified"`
	Conflicts     []string `json:"conflicts,omitempty"`
	RepositoryURL string   `json:"repository_url"`
	SourceURL     string   `json:"source_url"`
	ReleaseURL    string   `json:"release_url"`
}

func loadAgentRoleMarket() ([]agentRoleMarketEntry, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(publicAgentRoleRegistry)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Roles []agentRoleMarketEntry `json:"roles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	for i := range payload.Roles {
		payload.Roles[i].RepositoryURL = publicAgentRoleRepository
		payload.Roles[i].SourceURL = publicAgentRoleRepository + "/tree/main/roles/" + payload.Roles[i].Slug
		payload.Roles[i].ReleaseURL = publicAgentRoleRepository + "/releases/tag/" + payload.Roles[i].Slug + "-v" + payload.Roles[i].Version
		dir := filepath.Join(cicyRootDir, "memory", "agents", payload.Roles[i].Slug)
		data, err := os.ReadFile(filepath.Join(dir, ".cicy-role.json"))
		if err != nil {
			// Roles may predate the marketplace or be maintained directly in the
			// runtime role directory. A complete standard role directory is already
			// installed; the marketplace marker is version metadata, not the source
			// of truth for existence. Never ask the user to reinstall or overwrite it.
			if runtimeAgentRoleInstalled(dir) {
				payload.Roles[i].Installed = true
				payload.Roles[i].InstalledVer = "local"
			}
			continue
		}
		var meta struct {
			Version   string   `json:"version"`
			Conflicts []string `json:"conflicts"`
		}
		if json.Unmarshal(data, &meta) == nil {
			payload.Roles[i].Installed = true
			payload.Roles[i].InstalledVer = meta.Version
			payload.Roles[i].HasUpdate = meta.Version != payload.Roles[i].Version
			payload.Roles[i].Conflicts = meta.Conflicts
			payload.Roles[i].Modified = len(meta.Conflicts) > 0
		}
	}
	return payload.Roles, nil
}

func runtimeAgentRoleInstalled(dir string) bool {
	for _, name := range []string{"meta.yaml", "role.md", "system.md"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.IsDir() || info.Size() == 0 {
			return false
		}
	}
	return true
}

func ensureDefaultAgentRoles() {
	for _, slug := range defaultAgentRoleSlugs {
		dir := filepath.Join(cicyRootDir, "memory", "agents", slug)
		if _, err := os.Stat(dir); err == nil || !os.IsNotExist(err) {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		output, err := exec.CommandContext(ctx, "cicy-agent-role", "install", slug).CombinedOutput()
		cancel()
		if err != nil {
			log.Printf("[startup] default agent role %s install failed: %v (%s)", slug, err, strings.TrimSpace(string(output)))
			continue
		}
		log.Printf("[startup] default agent role %s installed", slug)
	}
}

func handleAgentRoleMarket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, 405, "method not allowed")
		return
	}
	roles, err := loadAgentRoleMarket()
	if err != nil {
		httpErr(w, 502, "role market unavailable: "+err.Error())
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	out := make([]agentRoleMarketEntry, 0, len(roles))
	for _, role := range roles {
		haystack := strings.ToLower(strings.Join(append([]string{role.Slug, role.Name, role.NameZH, role.Description, role.DescriptionZH}, role.Tags...), " "))
		if q == "" || strings.Contains(haystack, q) {
			out = append(out, role)
		}
	}
	J(w, M{"roles": out, "total": len(out), "registry": publicAgentRoleRegistry})
}

func handleAgentRoleMarketAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, 405, "method not allowed")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/agent-role-market/"), "/"), "/")
	if len(parts) != 2 || !agentRoleSlugRE.MatchString(parts[0]) || (parts[1] != "install" && parts[1] != "update") {
		httpErr(w, 404, "not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cicy-agent-role", parts[1], parts[0])
	output, err := cmd.CombinedOutput()
	if err != nil {
		httpErr(w, 409, strings.TrimSpace(string(output)))
		return
	}
	J(w, M{"ok": true, "slug": parts[0], "action": parts[1], "output": strings.TrimSpace(string(output))})
}
