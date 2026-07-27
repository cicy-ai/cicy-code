// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ttyd-go/skillcmd"
)

// ── workflow skills (review / qa / ship / investigate / office-hours / document-release) ──
// These are step-creation skills surfaced by the agent_queue UI. They have
// nothing to do with the marketplace skill registry below.

type skillDefinition struct {
	ID            string    `json:"id"`
	Label         string    `json:"label"`
	Description   string    `json:"description"`
	Icon          string    `json:"icon"`
	Mode          string    `json:"mode"`
	DefaultTarget string    `json:"default_target"`
	Template      string    `json:"template"`
	StepKind      string    `json:"step_kind,omitempty"`
	WorkflowSteps []stepDef `json:"workflow_steps,omitempty"`
}

type stepDef struct {
	Title    string `json:"title"`
	StepKind string `json:"step_kind"`
	Type     string `json:"type"`
	Template string `json:"template"`
}

func builtinSkills() []skillDefinition {
	return []skillDefinition{
		{ID: "review", Label: "代码审查", Description: "创建 review step", Icon: "🔍", Mode: "create_step", DefaultTarget: "bound_worker", Template: "Use the /review skill from gstack to do a pre-landing code review on the current branch", StepKind: "review"},
		{ID: "qa", Label: "QA 测试", Description: "创建 QA step", Icon: "🧪", Mode: "create_step", DefaultTarget: "bound_worker", Template: "Use the /qa skill from gstack to QA test the app", StepKind: "qa"},
		{ID: "ship", Label: "发布", Description: "创建 review → qa → ship workflow", Icon: "🚀", Mode: "create_workflow", DefaultTarget: "bound_worker", Template: "Use the /ship skill from gstack to run tests, review, and create a PR", WorkflowSteps: []stepDef{
			{Title: "Review", StepKind: "review", Type: "message", Template: "Use the /review skill from gstack to do a pre-landing code review on the current branch"},
			{Title: "QA", StepKind: "qa", Type: "message", Template: "Use the /qa skill from gstack to QA test the app"},
			{Title: "Ship", StepKind: "ship", Type: "message", Template: "Use the /ship skill from gstack to run tests, review, and create a PR"},
		}},
		{ID: "investigate", Label: "调试", Description: "创建 investigate step", Icon: "🔧", Mode: "create_step", DefaultTarget: "current_pane", Template: "Use the /investigate skill from gstack to systematically debug the current issue", StepKind: "task"},
		{ID: "office-hours", Label: "CEO 顾问", Description: "直接发送顾问 prompt", Icon: "🧠", Mode: "direct_prompt", DefaultTarget: "current_pane", Template: "Use the /office-hours skill from gstack"},
		{ID: "document-release", Label: "更新文档", Description: "创建文档更新 step", Icon: "📄", Mode: "create_step", DefaultTarget: "bound_worker", Template: "Use the /document-release skill from gstack to update all docs after shipping", StepKind: "task"},
	}
}

func handleSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	J(w, M{"skills": builtinSkills()})
}

// handleInstalledSkills lists the LOCALLY installed cicy-skills (name + version),
// read from the local installed manifest — NO remote registry fetch. The web uses
// this for cheap install checks (e.g. "is cicy-todo installed?") instead of the
// ~2s /api/skill-market remote catalog, which it was calling on every page load
// just to read one skill's install flag.
func handleInstalledSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	installed := loadInstalledFull()
	out := make([]M, 0, len(installed))
	for name, s := range installed {
		out = append(out, M{"name": name, "version": s.Version})
	}
	J(w, M{"skills": out})
}

func handleSkillRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpErr(w, 405, "method not allowed")
		return
	}
	var req struct {
		SkillID         string `json:"skill_id"`
		TargetPaneID    string `json:"target_pane_id"`
		TargetMachineID int    `json:"target_machine_id"`
		CurrentPaneID   string `json:"current_pane_id"`
		CreatedBy       string `json:"created_by"`
		Title           string `json:"title"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	var skill *skillDefinition
	for _, item := range builtinSkills() {
		if item.ID == req.SkillID {
			copy := item
			skill = &copy
			break
		}
	}
	if skill == nil {
		httpErr(w, 404, "skill not found")
		return
	}
	targetPaneID := firstNonEmpty(req.TargetPaneID, req.CurrentPaneID)
	if targetPaneID == "" {
		httpErr(w, 400, "target_pane_id required")
		return
	}
	switch skill.Mode {
	case "direct_prompt":
		res, err := store.Exec(`INSERT INTO agent_queue (
			pane_id, message, type, priority, status, step_kind, title, target_machine_id, target_pane_id, created_by
		) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			normPaneID(targetPaneID), skill.Template, "message", 0, "pending", "message", firstNonEmpty(req.Title, skill.Label), nullableInt(req.TargetMachineID), shortPaneID(normPaneID(targetPaneID)), req.CreatedBy)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		step, _ := getQueueItem(int(id))
		J(w, M{"success": true, "mode": skill.Mode, "step": step})
	case "create_step":
		stepReq := M{
			"pane_id":           targetPaneID,
			"target_pane_id":    targetPaneID,
			"message":           skill.Template,
			"type":              "message",
			"step_kind":         skill.StepKind,
			"title":             firstNonEmpty(req.Title, skill.Label),
			"target_machine_id": req.TargetMachineID,
			"created_by":        req.CreatedBy,
		}
		res, err := store.Exec(`INSERT INTO agent_queue (
			pane_id, message, type, priority, status, step_kind, title, target_machine_id, target_pane_id, created_by
		) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			normPaneID(targetPaneID), skill.Template, "message", 0, "pending", skill.StepKind, firstNonEmpty(req.Title, skill.Label), nullableInt(req.TargetMachineID), shortPaneID(normPaneID(targetPaneID)), req.CreatedBy)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		step, _ := getQueueItem(int(id))
		J(w, M{"success": true, "mode": skill.Mode, "step": step, "request": stepReq})
	case "create_workflow":
		workflowRes, err := store.Exec("INSERT INTO workflows (title, description, status, created_by, created_at, updated_at) VALUES (?,?,?,?,datetime('now'),datetime('now'))",
			firstNonEmpty(req.Title, skill.Label), skill.Description, "pending", req.CreatedBy)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		workflowID, _ := workflowRes.LastInsertId()
		steps := []M{}
		for i, step := range skill.WorkflowSteps {
			res, err := store.Exec(`INSERT INTO agent_queue (
				pane_id, message, type, priority, status, step_kind, workflow_id, step_index, title, target_machine_id, target_pane_id, created_by
			) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				normPaneID(targetPaneID), step.Template, step.Type, 0, "pending", step.StepKind, workflowID, i+1, step.Title, nullableInt(req.TargetMachineID), shortPaneID(normPaneID(targetPaneID)), req.CreatedBy)
			if err != nil {
				continue
			}
			id, _ := res.LastInsertId()
			item, _ := getQueueItem(int(id))
			steps = append(steps, item)
		}
		J(w, M{"success": true, "mode": skill.Mode, "workflow_id": workflowID, "steps": steps})
	default:
		httpErr(w, 400, "unsupported skill mode")
	}
}

// ── Skill Marketplace (v2 worker registry) ──────────────────────────────
//
// All install / uninstall now flows through the `cicy-code skill` subcommand
// (api/skillcmd/). The HTTP marketplace UI here is read-only proxy of the
// public registry at https://skills.cicy-ai.com/v1/skills, with installed
// status looked up from ~/cicy-ai/skills/installed.json.

type marketSkillStatus struct {
	Installed     bool              `json:"installed"`
	ConfigPresent bool              `json:"config_present"`
	RequiresMet   map[string]bool   `json:"requires_met,omitempty"`
	LastError     string            `json:"last_error,omitempty"`
	Detail        map[string]string `json:"detail,omitempty"`
}

type marketSkill struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Version     string   `json:"version"`           // latest version available in registry
	Category    string   `json:"category"`
	Icon        string   `json:"icon"`
	Tags        []string `json:"tags,omitempty"`
	ConfigFile  string   `json:"config_file,omitempty"`
	// Source distinguishes registry skills (empty string) from user-authored
	// skills under ~/cicy-ai/skills/<name>/ ("user").
	Source string `json:"source,omitempty"`
	// RegistrySource names the private registry a skill came from (the source
	// label in registries.json, e.g. "team-a"). Empty for the public registry.
	// The UI renders a badge when this is set.
	RegistrySource string            `json:"registry_source,omitempty"`
	Status         marketSkillStatus `json:"status"`
	// Installed-state fields. InstalledVersion is the version recorded in
	// ~/cicy-ai/skills/installed.json (empty when not installed). HasUpdate
	// is true when InstalledVersion is non-empty AND lower than Version.
	InstalledVersion string `json:"installed_version,omitempty"`
	HasUpdate        bool   `json:"has_update,omitempty"`
}

// single source of truth for the public registry URL (shared with skillcmd)
const marketRegistryDefaultURL = skillcmd.DefaultRegistry

var (
	marketCacheMu       sync.Mutex
	// keyed by lang ("" = no Accept-Language → server default)
	marketCacheByLang   = map[string][]marketSkill{}
	marketCacheFetched  = map[string]time.Time{}
	marketCacheTTL      = 5 * time.Minute
)

func marketRegistryURL() string {
	if v := strings.TrimSpace(os.Getenv("CICY_SKILLS_REGISTRY")); v != "" {
		return v
	}
	return marketRegistryDefaultURL
}

// authedGet issues a GET with an optional bearer token (for private
// registries). Token is scoped by the caller (same-origin as the source URL).
func authedGet(client *http.Client, rawURL, token string) (*http.Response, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return client.Do(req)
}

// preferredLang reads the Accept-Language header (or ?lang= query) from the
// request and normalizes it to the canonical form we forward to the worker.
// Returns "" when no preference is supplied.
func preferredLang(r *http.Request) string {
	if r == nil {
		return ""
	}
	if q := strings.TrimSpace(r.URL.Query().Get("lang")); q != "" {
		return q
	}
	h := strings.TrimSpace(r.Header.Get("Accept-Language"))
	if h == "" {
		return ""
	}
	// Accept-Language can be a list ("zh-CN,zh;q=0.9,en;q=0.8"). Take the
	// first weighted entry.
	first := strings.SplitN(h, ",", 2)[0]
	first = strings.SplitN(first, ";", 2)[0]
	return strings.TrimSpace(first)
}

// fetchRegistrySkills GET /v1/skills?lang=<lang>, returns the per-skill
// summary list. Cached per-lang for 5 minutes; refresh on miss / expiry.
// Network failure returns the previous cache (so the UI keeps working
// briefly when offline).
func fetchRegistrySkills(lang string) ([]marketSkill, error) {
	marketCacheMu.Lock()
	defer marketCacheMu.Unlock()

	if cached, ok := marketCacheByLang[lang]; ok && time.Since(marketCacheFetched[lang]) < marketCacheTTL {
		return cached, nil
	}

	client := &http.Client{Timeout: 8 * time.Second}
	// Query every configured source (public + private). Merge by name in
	// precedence order (later source wins), matching the CLI.
	byName := map[string]marketSkill{}
	anyOK := false
	var lastErr error
	for _, src := range skillcmd.ClientSources() {
		u := strings.TrimRight(src.URL, "/") + "/v1/skills"
		if lang != "" {
			u += "?lang=" + url.QueryEscape(lang)
		}
		resp, err := authedGet(client, u, src.Token)
		if err != nil {
			lastErr = fmt.Errorf("fetch %s: %w", u, err)
			continue
		}
		func() {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				lastErr = fmt.Errorf("registry %q %d: %s", src.Name, resp.StatusCode, strings.TrimSpace(string(body)))
				return
			}
			var env struct {
				OK   bool `json:"ok"`
				Data struct {
					Skills []struct {
						Name                 string   `json:"name"`
						Version              string   `json:"version"`
						Title                string   `json:"title"`
						Description          string   `json:"description"`
						TitleLocalized       string   `json:"title_localized"`
						DescriptionLocalized string   `json:"description_localized"`
						Category             string   `json:"category"`
						Tags                 []string `json:"tags"`
						Config               struct {
							Path string `json:"path"`
						} `json:"config"`
					} `json:"skills"`
				} `json:"data"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				lastErr = fmt.Errorf("decode %q: %w", src.Name, err)
				return
			}
			anyOK = true
			// private = any source other than the public registry
			regSource := ""
			if src.URL != marketRegistryDefaultURL {
				regSource = src.Name
			}
			for _, s := range env.Data.Skills {
				title := s.Title
				if s.TitleLocalized != "" {
					title = s.TitleLocalized
				}
				desc := s.Description
				if s.DescriptionLocalized != "" {
					desc = s.DescriptionLocalized
				}
				byName[s.Name] = marketSkill{ // later source wins
					Name:           s.Name,
					Title:          title,
					Description:    desc,
					Version:        s.Version,
					Category:       s.Category,
					Tags:           s.Tags,
					ConfigFile:     s.Config.Path,
					RegistrySource: regSource,
				}
			}
		}()
	}
	if !anyOK {
		if cached, ok := marketCacheByLang[lang]; ok {
			return cached, nil
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no registry sources reachable")
	}
	out := make([]marketSkill, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	marketCacheByLang[lang] = out
	marketCacheFetched[lang] = time.Now()
	return out, nil
}

func marketSkillsCatalog(lang string) ([]marketSkill, error) {
	return fetchRegistrySkills(lang)
}

// userSkillsRoot returns ~/cicy-ai/skills (the v2 install root).
// Override with CICY_SKILLS_ROOT for tests.
func userSkillsRoot() string {
	if v := strings.TrimSpace(os.Getenv("CICY_SKILLS_ROOT")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "cicy-ai", "skills")
}

// reservedUserSkillDirs are top-level entries in ~/cicy-ai/skills/ that are
// NOT user-authored skills (they're metadata or installed-via-registry copies).
var reservedUserSkillDirs = map[string]struct{}{
	".cache":         {},
	"installed.json": {},
	"agents.json":    {},
	"private":        {}, // own local-registry installs (source-based layout)
	"team":           {}, // other teams' private-registry installs
}

func parseSkillFrontmatter(path string) (name, description string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return "", ""
	}
	// Find the closing --- of the frontmatter block.
	rest := text[3:]
	idx := strings.Index(rest, "---")
	if idx < 0 {
		return "", ""
	}
	front := rest[:idx]
	for _, raw := range strings.Split(front, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		eq := strings.Index(line, ":")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		val = strings.Trim(val, `"'`)
		switch key {
		case "name":
			name = val
		case "description":
			description = val
		}
	}
	return name, description
}

func titleizeSkillName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(string(p[0])) + p[1:]
	}
	return strings.Join(parts, " ")
}

// scanUserSkills walks ~/cicy-ai/skills/ and surfaces user-authored skills
// (those with a SKILL.md in the directory and NOT already in the registry
// catalog). registryNames is the set of names from fetchRegistrySkills.
// A directory without SKILL.md is skipped (e.g. v1 leftover source trees
// like cicy-skills/, .cache/, plus anything without a manifest yet).
func scanUserSkills(registryNames map[string]struct{}) []marketSkill {
	root := userSkillsRoot()
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := []marketSkill{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if _, reserved := reservedUserSkillDirs[name]; reserved {
			continue
		}
		if _, registered := registryNames[name]; registered {
			continue // registered skills override user copies
		}
		skillMD := filepath.Join(root, name, "SKILL.md")
		// require an actual SKILL.md — directories without a manifest are
		// not user skills (avoids surfacing v1 mgr's extracted source tree
		// or other random subdirs).
		if _, err := os.Stat(skillMD); err != nil {
			continue
		}
		fmName, fmDesc := parseSkillFrontmatter(skillMD)
		if fmName == "" {
			fmName = name
		}
		title := titleizeSkillName(fmName)
		out = append(out, marketSkill{
			Name:        fmName,
			Title:       title,
			Description: fmDesc,
			Version:     "user",
			Category:    "user",
			Source:      "user",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func mergedMarketCatalog(lang string) (skills []marketSkill, registryErr error) {
	registry, err := marketSkillsCatalog(lang)
	registryErr = err
	registryNames := make(map[string]struct{}, len(registry))
	for _, s := range registry {
		registryNames[s.Name] = struct{}{}
	}
	user := scanUserSkills(registryNames)
	skills = append(registry, user...)
	return
}

func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return h
	}
	return filepath.Join(h, strings.TrimPrefix(p, "~/"))
}

// loadInstalledNames returns the map name → version from
// ~/cicy-ai/skills/installed.json (written by `cicy-code skill install`).
func loadInstalledNames() map[string]string {
	out := map[string]string{}
	for name, s := range loadInstalledFull() {
		out[name] = s.Version
	}
	return out
}

// loadInstalledFull is the richer variant: name → full InstalledSkill entry,
// so callers can read source.type (registry vs "local" override / `skill dev`)
// and other metadata. Returns an empty map on missing/corrupt installed.json.
func loadInstalledFull() map[string]skillcmd.InstalledSkill {
	out := map[string]skillcmd.InstalledSkill{}
	cfg, err := skillcmd.InstalledSkills()
	if err != nil || cfg == nil {
		return out
	}
	for _, s := range cfg.Skills {
		out[s.Name] = s
	}
	return out
}

// versionLess: true if a < b using simple semver comparison. Falls back to
// string compare for non-numeric segments.
func versionLess(a, b string) bool {
	if a == b {
		return false
	}
	splitV := func(v string) []int {
		v = strings.TrimPrefix(v, "v")
		// drop pre-release / build suffix (-foo, +foo)
		if i := strings.IndexAny(v, "-+"); i >= 0 {
			v = v[:i]
		}
		parts := strings.Split(v, ".")
		out := make([]int, 0, 3)
		for _, p := range parts {
			n := 0
			fmt.Sscanf(p, "%d", &n)
			out = append(out, n)
		}
		return out
	}
	pa, pb := splitV(a), splitV(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var ai, bi int
		if i < len(pa) {
			ai = pa[i]
		}
		if i < len(pb) {
			bi = pb[i]
		}
		if ai != bi {
			return ai < bi
		}
	}
	return false
}

func computeMarketStatus(skill *marketSkill) {
	// Reset installed-state before recomputing. The catalog slice handed in may
	// be the CACHED one (mergedMarketCatalog aliases it when there are no user
	// skills), and the logic below only ever SETS HasUpdate/Installed — never
	// clears them. Without this reset, a skill that was just updated (or
	// uninstalled) via the CLI would keep a stale HasUpdate=true in the cached
	// struct until the 5-min catalog cache expires, so the UI's update badge
	// never clears. Recompute everything fresh from installed.json each call.
	skill.Status = marketSkillStatus{}
	skill.InstalledVersion = ""
	skill.HasUpdate = false
	if skill.Source == "local" {
		skill.Source = "" // re-derived from installed.json below
	}
	installed := loadInstalledFull()
	if entry, ok := installed[skill.Name]; ok {
		skill.Status.Installed = true
		skill.InstalledVersion = entry.Version
		// installed.json source.type == "local" means the user has either
		// `skill dev`'d this name or ejected the registry copy. Surface that
		// so the UI shows a "local" tag and we suppress update prompts below.
		if skill.Source == "" && entry.Source.Type == "local" {
			skill.Source = "local"
		}
		// HasUpdate when registry version (skill.Version) is newer than the
		// recorded install. Skip for "user"-authored AND "local"-ejected
		// skills — both are user-managed and shouldn't be auto-updated.
		if skill.Source == "" && skill.Version != "" && skill.Version != entry.Version && versionLess(entry.Version, skill.Version) {
			skill.HasUpdate = true
		}
	} else if skill.Source == "user" {
		// User-authored skills with SKILL.md present count as "installed"
		// for visibility purposes.
		root := userSkillsRoot()
		if root != "" {
			if _, err := os.Stat(filepath.Join(root, skill.Name, "SKILL.md")); err == nil {
				skill.Status.Installed = true
				skill.InstalledVersion = "user"
			}
		}
	}
	for u := range uninstalledSkillsSet() {
		if u == skill.Name {
			skill.Status.Installed = false
			skill.HasUpdate = false
			break
		}
	}
	if skill.ConfigFile != "" {
		if _, err := os.Stat(expandHome(skill.ConfigFile)); err == nil {
			skill.Status.ConfigPresent = true
		}
	} else {
		skill.Status.ConfigPresent = true
	}
}

func handleSkillMarketList(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	categoryFilter := strings.TrimSpace(r.URL.Query().Get("category"))
	installedFilter := r.URL.Query().Get("installed")
	lang := preferredLang(r)

	skills, registryErr := mergedMarketCatalog(lang)
	out := make([]marketSkill, 0, len(skills))
	installedCount := 0
	for i := range skills {
		computeMarketStatus(&skills[i])
		s := skills[i]
		if categoryFilter != "" && s.Category != categoryFilter {
			continue
		}
		if installedFilter == "true" && !s.Status.Installed {
			continue
		}
		if installedFilter == "false" && s.Status.Installed {
			continue
		}
		if q != "" {
			if !strings.Contains(strings.ToLower(s.Name), q) &&
				!strings.Contains(strings.ToLower(s.Title), q) &&
				!strings.Contains(strings.ToLower(s.Description), q) {
				continue
			}
		}
		if s.Status.Installed {
			installedCount++
		}
		out = append(out, s)
	}
	registryStatus := "ok"
	registryErrorMsg := ""
	if registryErr != nil {
		registryStatus = "unavailable"
		registryErrorMsg = registryErr.Error()
	}
	J(w, M{
		"skills":          out,
		"total":           len(out),
		"installed":       installedCount,
		"registry_status": registryStatus,
		"registry_error":  registryErrorMsg,
		"registry_url":    marketRegistryURL(),
		"lang":            lang,
	})
}

func findMarketSkill(name, lang string) *marketSkill {
	catalog, _ := mergedMarketCatalog(lang)
	for i, s := range catalog {
		if s.Name == name {
			skill := catalog[i]
			return &skill
		}
	}
	// Fall back to a private skill (~/cicy-ai/skills/private/<name>): these are
	// excluded from the merged catalog (they're not for public discovery) but
	// must still be viewable from the "我的库" tab.
	return privateMarketSkill(name)
}

// privateMarketSkill builds a marketSkill from a skill dir under PrivateSkillsDir.
func privateMarketSkill(name string) *marketSkill {
	data, err := os.ReadFile(filepath.Join(skillcmd.PrivateSkillsDir(), name, "manifest.json"))
	if err != nil {
		return nil
	}
	var m struct {
		Name        string   `json:"name"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Category    string   `json:"category"`
		Version     string   `json:"version"`
		Tags        []string `json:"tags"`
		Config      struct {
			Path string `json:"path"`
		} `json:"config"`
	}
	if json.Unmarshal(data, &m) != nil || m.Name == "" {
		return nil
	}
	title := m.Title
	if title == "" {
		title = titleizeSkillName(m.Name)
	}
	return &marketSkill{
		Name:        m.Name,
		Title:       title,
		Description: m.Description,
		Version:     m.Version,
		Category:    m.Category,
		Tags:        m.Tags,
		ConfigFile:  m.Config.Path,
		Source:      "private",
	}
}

// readPrivateManifest returns the raw manifest map for a private skill (for the
// detail sidebar), or nil if absent.
func readPrivateManifest(name string) map[string]any {
	data, err := os.ReadFile(filepath.Join(skillcmd.PrivateSkillsDir(), name, "manifest.json"))
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}

// readSkillDoc reads SKILL.md / references/help.md / references/tools.md.
// Order:
//   1. Local install at ~/cicy-ai/skills/<name>/<file> (always English here).
//   2. Worker /v1/skills/<name>/<version>/files/<file_key> (the v2 publish
//      flow uploads the doc into KV alongside the manifest, so any registry
//      skill has these even before install).
// Worker file keys are: skill_md, help_md, tools_md, readme.
func readSkillDoc(skill *marketSkill, file, fileKey string) string {
	root := userSkillsRoot()
	if root != "" {
		p := filepath.Join(root, skill.Name, file)
		if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
			return string(data)
		}
	}
	// Private skills live under ~/cicy-ai/skills/private/<name>/ (excluded from
	// the flat root above) — read their docs from there.
	if pp := filepath.Join(skillcmd.PrivateSkillsDir(), skill.Name, file); pp != "" {
		if data, err := os.ReadFile(pp); err == nil && len(data) > 0 {
			return string(data)
		}
	}
	// Fallback: pull from worker. Use latest if version is empty.
	version := skill.Version
	if version == "" || version == "user" {
		return ""
	}
	client := &http.Client{Timeout: 6 * time.Second}
	// Try each source (reverse precedence: private first) until one serves it.
	srcs := skillcmd.ClientSources()
	for i := len(srcs) - 1; i >= 0; i-- {
		src := srcs[i]
		u := fmt.Sprintf("%s/v1/skills/%s/%s/files/%s",
			strings.TrimRight(src.URL, "/"),
			url.PathEscape(skill.Name),
			url.PathEscape(version),
			url.PathEscape(fileKey),
		)
		resp, err := authedGet(client, u, src.Token)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err == nil && len(body) > 0 {
			return string(body)
		}
	}
	return ""
}

func readLocalizedSkillDoc(skill *marketSkill, stem, lang string) string {
	suffix := "en"
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "zh") {
		suffix = "cn"
	}
	localizedFile := filepath.Join("references", stem+"."+suffix+".md")
	localizedKey := stem + "_" + suffix + "_md"
	if content := readSkillDoc(skill, localizedFile, localizedKey); content != "" {
		return content
	}
	// The public registry's legacy file endpoint only accepts the original
	// skill_md/help_md/tools_md keys. Localized documents are still packaged in
	// each immutable GitHub tag, so read that static source as a compatibility
	// fallback until every registry deployment accepts the new keys.
	if skill.Source != "private" && skill.Source != "user" && skill.Version != "" {
		tag := skill.Name + "-v" + skill.Version
		rawURL := fmt.Sprintf(
			"https://raw.githubusercontent.com/cicy-ai/cicy-skills/%s/skills/%s/references/%s.%s.md",
			url.PathEscape(tag), url.PathEscape(skill.Name), url.PathEscape(stem), url.PathEscape(suffix),
		)
		client := &http.Client{Timeout: 6 * time.Second}
		if resp, err := client.Get(rawURL); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				if body, readErr := io.ReadAll(resp.Body); readErr == nil && len(body) > 0 {
					return string(body)
				}
			}
		}
	}
	return readSkillDoc(skill, filepath.Join("references", stem+".md"), stem+"_md")
}

// fetchRegistryManifest GET /v1/skills/<name> from the worker and returns
// the raw manifest as a generic map (so we can pass through new fields the
// worker adds without recompiling). Used by the detail handler to surface
// publisher / homepage / license / size / etc. in the marketplace UI.
//
// Returns nil on any error — callers must treat manifest as optional.
func fetchRegistryManifest(name, lang string) map[string]any {
	if name == "" {
		return nil
	}
	client := &http.Client{Timeout: 6 * time.Second}
	// Try each source (reverse precedence: private first).
	srcs := skillcmd.ClientSources()
	for i := len(srcs) - 1; i >= 0; i-- {
		src := srcs[i]
		u := strings.TrimRight(src.URL, "/") + "/v1/skills/" + url.PathEscape(name)
		if lang != "" {
			u += "?lang=" + url.QueryEscape(lang)
		}
		resp, err := authedGet(client, u, src.Token)
		if err != nil {
			continue
		}
		body, rerr := io.ReadAll(resp.Body)
		status := resp.StatusCode
		resp.Body.Close()
		if status != http.StatusOK || rerr != nil {
			continue
		}
		var env struct {
			OK   bool `json:"ok"`
			Data struct {
				Manifest map[string]any `json:"manifest"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			continue
		}
		if env.OK && env.Data.Manifest != nil {
			return env.Data.Manifest
		}
	}
	return nil
}

// installAdvice describes the v2 self-service install path. Marketplace
// install/uninstall buttons return this so the agent / user can run the
// command themselves. Phase 4 will replace this with an agent-driven flow.
type installAdvice struct {
	OK      bool   `json:"ok"`
	Action  string `json:"action"`
	Command string `json:"command"`
	Hint    string `json:"hint"`
}

// handleSkillRegistries manages the client-side private registry source list
// (~/cicy-ai/registries.json) so the marketplace UI has an entry point to add
// a team's private registry by pasting its address + token.
//
// Routes:
//   GET    /api/skill-registries            → list sources (tokens redacted)
//   POST   /api/skill-registries            → {name,url,token} add/upsert
//   DELETE /api/skill-registries/<name>     → remove by name or url
func handleSkillRegistries(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		type srcView struct {
			Name     string `json:"name"`
			URL      string `json:"url"`
			HasToken bool   `json:"has_token"`
			Public   bool   `json:"public"`
		}
		srcs := skillcmd.ClientSources()
		out := make([]srcView, 0, len(srcs))
		for _, s := range srcs {
			out = append(out, srcView{
				Name:     s.Name,
				URL:      s.URL,
				HasToken: s.Token != "",
				Public:   s.URL == marketRegistryDefaultURL,
			})
		}
		J(w, M{"sources": out})

	case "POST":
		var body struct {
			Name  string `json:"name"`
			URL   string `json:"url"`
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, 400, "invalid JSON body")
			return
		}
		if strings.TrimSpace(body.URL) == "" {
			httpErr(w, 400, "url required")
			return
		}
		if _, err := skillcmd.AddSource(body.Name, body.URL, body.Token); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		invalidateMarketCache()
		J(w, M{"ok": true})

	case "DELETE":
		key := strings.TrimPrefix(r.URL.Path, "/api/skill-registries/")
		key = strings.Trim(key, "/")
		if key == "" {
			httpErr(w, 400, "source name required")
			return
		}
		removed, err := skillcmd.RemoveSource(key)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		if !removed {
			httpErr(w, 404, "no registry source matching "+key)
			return
		}
		invalidateMarketCache()
		J(w, M{"ok": true})

	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleSkillMarketAction(w http.ResponseWriter, r *http.Request) {
	// Routes:
	//   GET  /api/skill-market/<name>                  → detail (includes skill_md, help_md, tools_md)
	//   POST /api/skill-market/<name>/install          → run skillcmd install in-process; return logs+status
	//   POST /api/skill-market/<name>/uninstall        → run skillcmd remove in-process; return logs+status
	//   POST /api/skill-market/<name>/update           → run skillcmd update in-process; return logs+status
	path := strings.TrimPrefix(r.URL.Path, "/api/skill-market/")
	parts := strings.SplitN(path, "/", 2)
	name := parts[0]
	if name == "" {
		httpErr(w, 400, "skill name required")
		return
	}
	lang := preferredLang(r)
	skill := findMarketSkill(name, lang)
	if skill == nil {
		httpErr(w, 404, "skill not found")
		return
	}
	computeMarketStatus(skill)

	if len(parts) == 1 {
		if r.Method != "GET" {
			httpErr(w, 405, "method not allowed")
			return
		}
		// Fetch the full manifest from the worker so the UI can render
		// publisher / homepage / license / size / runtime / permissions
		// etc. in the detail sidebar. This is best-effort: registry-side
		// failures degrade gracefully (UI just won't show the sidebar).
		var manifest map[string]any
		if skill.Source == "private" {
			manifest = readPrivateManifest(skill.Name)
		} else if skill.Source != "user" {
			manifest = fetchRegistryManifest(skill.Name, lang)
		}
		J(w, M{
			"skill":    skill,
			"skill_md": readSkillDoc(skill, "SKILL.md", "skill_md"),
			"help_md":  readLocalizedSkillDoc(skill, "help", lang),
			"tools_md": readLocalizedSkillDoc(skill, "tools", lang),
			"manifest": manifest,
		})
		return
	}

	if r.Method != "POST" {
		httpErr(w, 405, "method not allowed")
		return
	}

	switch parts[1] {
	case "install":
		// User skills (~/cicy-ai/skills/<name> with SKILL.md) are not in the
		// registry — installing them is a no-op (they're already discovered
		// by scanUserSkills). Surface a friendly hint for now.
		if skill.Source == "user" {
			J(w, M{"ok": true, "info": "user skill is already discovered locally; no install needed."})
			return
		}
		var logBuf strings.Builder
		res, err := skillcmd.InstallSkill(name, &logBuf)
		if err != nil {
			J(w, M{"ok": false, "error": err.Error(), "log": logBuf.String()})
			return
		}
		// drop registry cache so /api/skill-market re-fetch picks up
		// latest installed version status.
		invalidateMarketCache()
		// re-compute status after install — fetch fresh from merged catalog
		// (cache was just cleared so this fetches new data from registry).
		// Fall back to the pre-install skill object if lookup fails.
		updated := findMarketSkill(name, lang)
		if updated != nil {
			computeMarketStatus(updated)
			// Explicitly clear HasUpdate: after a successful install the
			// installed_version equals the latest, so has_update must be false.
			updated.HasUpdate = false
			skill = updated
		} else {
			// cache miss — patch the snapshot we already have
			skill.Status.Installed = true
			skill.InstalledVersion = skill.Version
			skill.HasUpdate = false
		}
		J(w, M{
			"ok":          true,
			"name":        res.Name,
			"version":     res.Version,
			"path":        res.Path,
			"sha256":      res.SHA256,
			"agents":      res.AgentsSynced,
			"log":         res.LogText,
			"skill":       skill,
		})
	case "uninstall":
		if skill.Source == "user" {
			J(w, M{"ok": false, "error": "user skill — delete the directory under ~/cicy-ai/skills/ manually"})
			return
		}
		var logBuf strings.Builder
		removed, err := skillcmd.RemoveSkill(name, &logBuf)
		if err != nil {
			J(w, M{"ok": false, "error": err.Error(), "log": logBuf.String()})
			return
		}
		invalidateMarketCache()
		updated := findMarketSkill(name, lang)
		if updated != nil {
			computeMarketStatus(updated)
			skill = updated
		}
		J(w, M{
			"ok":      true,
			"name":    removed.Name,
			"version": removed.Version,
			"log":     logBuf.String(),
			"skill":   skill,
		})
	case "update":
		if skill.Source == "user" {
			J(w, M{"ok": false, "error": "user skill — edit files directly under ~/cicy-ai/skills/"})
			return
		}
		var logBuf strings.Builder
		res, err := skillcmd.UpdateSkill(name, &logBuf)
		if err != nil {
			J(w, M{"ok": false, "error": err.Error(), "log": logBuf.String()})
			return
		}
		invalidateMarketCache()
		updated := findMarketSkill(name, lang)
		if updated != nil {
			computeMarketStatus(updated)
			updated.HasUpdate = false
			skill = updated
		} else {
			skill.Status.Installed = true
			skill.InstalledVersion = skill.Version
			skill.HasUpdate = false
		}
		J(w, M{
			"ok":      true,
			"name":    res.Name,
			"from":    res.From,
			"to":      res.To,
			"updated": res.Updated,
			"log":     logBuf.String(),
			"skill":   skill,
		})
	case "eject":
		// Convert an installed registry skill into a local-source one: files
		// stay in place under ~/cicy-ai/skills/<name>/, but installed.json
		// gets source.type="local" so the skill is treated as user-managed
		// (no auto-update, surfaced as Source="local" in this catalog).
		if skill.Source == "user" {
			J(w, M{"ok": false, "error": "user-authored skill — already local, nothing to eject"})
			return
		}
		if !skill.Status.Installed {
			J(w, M{"ok": false, "error": "skill not installed — install first, then eject"})
			return
		}
		var logBuf strings.Builder
		ejected, err := skillcmd.PublicEject(name, &logBuf)
		if err != nil {
			J(w, M{"ok": false, "error": err.Error(), "log": logBuf.String()})
			return
		}
		invalidateMarketCache()
		updated := findMarketSkill(name, lang)
		if updated != nil {
			computeMarketStatus(updated)
			skill = updated
		} else {
			skill.Source = "local"
			skill.HasUpdate = false
		}
		J(w, M{
			"ok":      true,
			"name":    name,
			"version": ejected.Version,
			"source":  "local",
			"path":    ejected.Source.Path,
			"log":     logBuf.String(),
			"skill":   skill,
		})
	case "advice":
		// Legacy path — kept so any old client polling `/install` action
		// pre-binary-update still gets a sensible response. Returns the
		// CLI command string instead of running anything.
		J(w, installAdvice{
			OK:      true,
			Action:  "install",
			Command: "cicy-code skill install " + name,
			Hint:    "Run the command above in a terminal (or via an agent pane) to install this skill.",
		})
	default:
		httpErr(w, 400, "unknown action: "+parts[1])
	}
}

// invalidateMarketCache forces the next list/detail fetch to bypass the
// per-language cache. Called after install/uninstall/update so the UI sees
// fresh status (and any newly installed_version) immediately.
func invalidateMarketCache() {
	marketCacheMu.Lock()
	defer marketCacheMu.Unlock()
	marketCacheByLang = map[string][]marketSkill{}
	marketCacheFetched = map[string]time.Time{}
}
