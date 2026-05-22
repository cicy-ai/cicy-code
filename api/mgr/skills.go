package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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
	Version     string   `json:"version"`
	Category    string   `json:"category"`
	Icon        string   `json:"icon"`
	Tags        []string `json:"tags,omitempty"`
	ConfigFile  string   `json:"config_file,omitempty"`
	// Source distinguishes registry skills (empty string) from user-authored
	// skills under ~/cicy-ai/skills/<name>/ ("user").
	Source string            `json:"source,omitempty"`
	Status marketSkillStatus `json:"status"`
}

const marketRegistryDefaultURL = "https://skills.cicy-ai.com"

var (
	marketCacheMu       sync.Mutex
	marketCacheSkills   []marketSkill
	marketCacheFetched  time.Time
	marketCacheTTL      = 5 * time.Minute
)

func marketRegistryURL() string {
	if v := strings.TrimSpace(os.Getenv("CICY_SKILLS_REGISTRY")); v != "" {
		return v
	}
	return marketRegistryDefaultURL
}

// fetchRegistrySkills GET /v1/skills, returns the per-skill summary list.
// Cached for 5 minutes; refresh on miss / expiry. Network failure returns
// the previous cache (so the UI keeps working briefly when offline).
func fetchRegistrySkills() ([]marketSkill, error) {
	marketCacheMu.Lock()
	defer marketCacheMu.Unlock()

	if marketCacheSkills != nil && time.Since(marketCacheFetched) < marketCacheTTL {
		return marketCacheSkills, nil
	}

	client := &http.Client{Timeout: 8 * time.Second}
	u := strings.TrimRight(marketRegistryURL(), "/") + "/v1/skills"
	resp, err := client.Get(u)
	if err != nil {
		if marketCacheSkills != nil {
			return marketCacheSkills, nil
		}
		return nil, fmt.Errorf("fetch %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if marketCacheSkills != nil {
			return marketCacheSkills, nil
		}
		return nil, fmt.Errorf("registry %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Skills []struct {
				Name        string   `json:"name"`
				Version     string   `json:"version"`
				Title       string   `json:"title"`
				Description string   `json:"description"`
				Category    string   `json:"category"`
				Tags        []string `json:"tags"`
				Config      struct {
					Path string `json:"path"`
				} `json:"config"`
			} `json:"skills"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		if marketCacheSkills != nil {
			return marketCacheSkills, nil
		}
		return nil, fmt.Errorf("decode registry response: %w", err)
	}
	out := make([]marketSkill, 0, len(env.Data.Skills))
	for _, s := range env.Data.Skills {
		out = append(out, marketSkill{
			Name:        s.Name,
			Title:       s.Title,
			Description: s.Description,
			Version:     s.Version,
			Category:    s.Category,
			Tags:        s.Tags,
			ConfigFile:  s.Config.Path,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	marketCacheSkills = out
	marketCacheFetched = time.Now()
	return out, nil
}

func marketSkillsCatalog() []marketSkill {
	skills, err := fetchRegistrySkills()
	if err != nil {
		// Return empty list rather than 500 — UI shows "no skills" cleanly.
		return nil
	}
	return skills
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

func mergedMarketCatalog() []marketSkill {
	registry := marketSkillsCatalog()
	registryNames := make(map[string]struct{}, len(registry))
	for _, s := range registry {
		registryNames[s.Name] = struct{}{}
	}
	user := scanUserSkills(registryNames)
	return append(registry, user...)
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

// loadInstalledNames returns the set of skill names from
// ~/cicy-ai/skills/installed.json (written by `cicy-code skill install`).
func loadInstalledNames() map[string]struct{} {
	out := map[string]struct{}{}
	root := userSkillsRoot()
	if root == "" {
		return out
	}
	data, err := os.ReadFile(filepath.Join(root, "installed.json"))
	if err != nil {
		return out
	}
	var cfg struct {
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return out
	}
	for _, s := range cfg.Skills {
		out[s.Name] = struct{}{}
	}
	return out
}

func computeMarketStatus(skill *marketSkill) {
	installed := loadInstalledNames()
	if _, ok := installed[skill.Name]; ok {
		skill.Status.Installed = true
	} else if skill.Source == "user" {
		// User-authored skills with SKILL.md present count as "installed"
		// for visibility purposes.
		root := userSkillsRoot()
		if root != "" {
			if _, err := os.Stat(filepath.Join(root, skill.Name, "SKILL.md")); err == nil {
				skill.Status.Installed = true
			}
		}
	}
	for u := range uninstalledSkillsSet() {
		if u == skill.Name {
			skill.Status.Installed = false
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

	skills := mergedMarketCatalog()
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
	J(w, M{"skills": out, "total": len(out), "installed": installedCount})
}

func findMarketSkill(name string) *marketSkill {
	catalog := mergedMarketCatalog()
	for i, s := range catalog {
		if s.Name == name {
			skill := catalog[i]
			return &skill
		}
	}
	return nil
}

// readSkillDoc reads SKILL.md / references/help.md / references/tools.md from
// the v2 install location (~/cicy-ai/skills/<name>/).
func readSkillDoc(skill *marketSkill, file string) string {
	root := userSkillsRoot()
	if root == "" {
		return ""
	}
	p := filepath.Join(root, skill.Name, file)
	if data, err := os.ReadFile(p); err == nil {
		return string(data)
	}
	return ""
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

func handleSkillMarketAction(w http.ResponseWriter, r *http.Request) {
	// Routes:
	//   GET  /api/skill-market/<name>                 → detail (includes skill_md, help_md, tools_md)
	//   POST /api/skill-market/<name>/install         → returns advice (run cicy-code skill install)
	//   POST /api/skill-market/<name>/uninstall       → returns advice (run cicy-code skill remove)
	path := strings.TrimPrefix(r.URL.Path, "/api/skill-market/")
	parts := strings.SplitN(path, "/", 2)
	name := parts[0]
	if name == "" {
		httpErr(w, 400, "skill name required")
		return
	}
	skill := findMarketSkill(name)
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
		J(w, M{
			"skill":    skill,
			"skill_md": readSkillDoc(skill, "SKILL.md"),
			"help_md":  readSkillDoc(skill, filepath.Join("references", "help.md")),
			"tools_md": readSkillDoc(skill, filepath.Join("references", "tools.md")),
		})
		return
	}

	if r.Method != "POST" {
		httpErr(w, 405, "method not allowed")
		return
	}
	switch parts[1] {
	case "install":
		J(w, installAdvice{
			OK:      true,
			Action:  "install",
			Command: "cicy-code skill install " + name,
			Hint:    "Run the command above in a terminal (or via an agent pane) to install this skill.",
		})
	case "uninstall":
		J(w, installAdvice{
			OK:      true,
			Action:  "uninstall",
			Command: "cicy-code skill remove " + name,
			Hint:    "Run the command above in a terminal (or via an agent pane) to remove this skill.",
		})
	default:
		httpErr(w, 400, "unknown action: "+parts[1])
	}
}
