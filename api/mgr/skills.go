package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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

// ── Skill Marketplace (registry-driven; see skills/migrations/SKILL_API.md) ──
// TODO: this is mock data until internal/registry is built. Replace
// marketSkills() with registry.List() once registry is in cicy-skills repo.

type marketSkillStatus struct {
	Installed     bool              `json:"installed"`
	ConfigPresent bool              `json:"config_present"`
	RequiresMet   map[string]bool   `json:"requires_met,omitempty"`
	LastError     string            `json:"last_error,omitempty"`
	Detail        map[string]string `json:"detail,omitempty"`
}

type marketSkill struct {
	Name          string            `json:"name"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Version       string            `json:"version"`
	Category      string            `json:"category"`
	Icon          string            `json:"icon"`
	Tags          []string          `json:"tags,omitempty"`
	BinaryAliases []string          `json:"binary_aliases"`
	ConfigFile    string            `json:"config_file,omitempty"`
	// Source distinguishes built-in marketplace entries (empty string) from
	// user-authored skills discovered under ~/cicy-ai/skills/<name>/ ("user").
	// User skills are surfaced for view + delete only — install is done out of
	// band via the `skill-author` CLI, so the marketplace UI hides their
	// install/uninstall buttons and shows a "Delete" action instead.
	Source string            `json:"source,omitempty"`
	Status marketSkillStatus `json:"status"`

	// legacyAgentSkillDirs lists historical on-disk SKILL.md directory names
	// that older cicy-skills installers may have produced for this market
	// entry. computeMarketStatus treats SKILL.md presence in EITHER the
	// canonical Name directory OR any of these aliases as "installed", so
	// hosts running a stale embedded cicy-skills binary still report a
	// truthful status. Fresh installs always write to the canonical Name.
	legacyAgentSkillDirs []string
}

func marketSkillsCatalog() []marketSkill {
	return []marketSkill{
		{Name: "cf", Title: "Cloudflare API", Description: "Secure Cloudflare API wrapper: manage DNS, zones, and more via `cf curl`. Token stored in ~/cicy-ai/db/cf.json and never exposed to the agent.", Version: "1.0.0", Category: "network", Icon: "globe", Tags: []string{"cloudflare", "dns", "api"}, BinaryAliases: []string{"cf"}, ConfigFile: "~/cicy-ai/db/cf.json"},
		{Name: "cf-tunnel", Title: "Cloudflare Tunnel", Description: "Manage Cloudflare Tunnel routes and DNS records on this host.", Version: "1.0.0", Category: "network", Icon: "globe", Tags: []string{"cloudflare", "tunnel", "dns"}, BinaryAliases: []string{"cf-tunnel"}, ConfigFile: "~/cicy-ai/db/cf.json"},
		{Name: "cping", Title: "cping", Description: "Quick network latency check for a domain or IP.", Version: "1.0.0", Category: "network", Icon: "activity", BinaryAliases: []string{"cping"}},
		{Name: "frp-server", Title: "FRP Server", Description: "Run frps in the background with status, reload, connections.", Version: "1.0.0", Category: "network", Icon: "server", BinaryAliases: []string{"frp-server"}, ConfigFile: "~/data/frp/frps.toml"},
		{Name: "frp-client", Title: "FRP Client", Description: "Run frpc, with remote management over SSH.", Version: "1.0.0", Category: "network", Icon: "plug", BinaryAliases: []string{"frp-client"}},
		{Name: "google", Title: "Google Workspace", Description: "Gmail / Sheets / Drive / Calendar via Google APIs. OAuth flow uses oauth-flow.cicy-ai.com as a code relay (Worker never sees client_secret or tokens).", Version: "1.0.0", Category: "ai", Icon: "mail", Tags: []string{"gmail", "sheets", "drive", "calendar"}, BinaryAliases: []string{"google"}, ConfigFile: "~/cicy-ai/db/google.json"},
		{Name: "agent-summary", Title: "Agent Summary", Description: "Generate conversation summaries and handoff documents.", Version: "1.0.0", Category: "ai", Icon: "file-text", BinaryAliases: []string{}},
		{Name: "agent-webpage", Title: "Agent Webpage", Description: "Talk to the live webpage client for an agent.", Version: "1.0.0", Category: "ai", Icon: "globe", BinaryAliases: []string{"agent-webpage"}},
		{Name: "agent-desktop", Title: "Agent Desktop", Description: "Control a connected cicy-desktop (Electron) client: screenshot, clipboard, exec shell, list windows, raw RPC.", Version: "1.0.0", Category: "ai", Icon: "monitor", BinaryAliases: []string{"agent-desktop"}},
		{Name: "agent-chrome", Title: "Agent Chrome", Description: "Control system Chrome on a connected cicy-desktop host with per-profile proxy. Auto-discovers Chrome on macOS / Windows / Linux; errors with install hint if not found.", Version: "1.0.0", Category: "ai", Icon: "chrome", BinaryAliases: []string{"agent-chrome"}},
		{Name: "agent-code-server", Title: "Code Server", Description: "Open files in the page-bound code-server.", Version: "1.0.0", Category: "ai", Icon: "code", BinaryAliases: []string{"agent-code-server"}},
		{Name: "cicy-agent", Title: "CiCy Agent", Description: "Operate tmux panes and windows on this host.", Version: "1.0.0", Category: "dev", Icon: "terminal", BinaryAliases: []string{"cicy-agent"}},
		{Name: "cicy-ssh", Title: "CiCy SSH", Description: "Manage SSH hosts via ~/.ssh/config. Use ssh-list to enumerate configured hosts.", Version: "1.1.0", Category: "ops", Icon: "key", BinaryAliases: []string{"ssh-list"}},
		{Name: "globalApiToken", Title: "Global API Token", Description: "Show or refresh ~/cicy-ai/global.json api_token.", Version: "1.0.0", Category: "ops", Icon: "shield", BinaryAliases: []string{"globalApiToken"}, ConfigFile: "~/cicy-ai/global.json"},
		{Name: "us-spot-proxy", Title: "US Spot Proxy", Description: "Manage Aliyun spot proxy nodes.", Version: "1.0.0", Category: "infra", Icon: "cloud", BinaryAliases: []string{"us-spot-proxy"}, ConfigFile: "~/cicy-ai/db/us-spot-proxy.json"},
		{Name: "aliyun-cli", Title: "Aliyun CLI", Description: "Bootstrap the official Aliyun CLI on this host. `aliyun-cli` installs the binary and applies a JSON config; after that, use the native `aliyun` command directly for ECS / VPC / RAM / OSS / etc.", Version: "1.0.0", Category: "infra", Icon: "cloud", Tags: []string{"aliyun", "ecs", "cli"}, BinaryAliases: []string{"aliyun-cli"}},
		{Name: "email", Title: "Email Sender", Description: "Send transactional email via Resend. Bootstrap an api_key + verified from_address (config / status), then `email send --to ... --subject ... --body ...`.", Version: "1.0.0", Category: "infra", Icon: "mail", Tags: []string{"email", "resend", "smtp", "transactional"}, BinaryAliases: []string{"email"}},
		{Name: "cicy-mihomo", Title: "Cicy Mihomo Proxy", Description: "Run a local Cicy Mihomo (mihomo / clash-meta fork) proxy with start/stop/reload/logs, node speed testing, and per-worker auth + routing.", Version: "1.0.0", Category: "network", Icon: "shield", Tags: []string{"proxy", "mihomo", "clash"}, BinaryAliases: []string{"cicy-mihomo"}, ConfigFile: "~/cicy-ai/db/mihomo.yaml", legacyAgentSkillDirs: []string{"mihomo"}},
		{Name: "proxy_ssh", Title: "SSH SOCKS Proxy", Description: "Manage local autossh-based SOCKS proxy profiles (start/stop/restart/test).", Version: "1.0.0", Category: "network", Icon: "plug", BinaryAliases: []string{"proxy_ssh"}, ConfigFile: "~/cicy-ai/db/proxy_ssh.json"},
		{Name: "us-spot-dev", Title: "US Spot Dev", Description: "Provision a US Aliyun spot dev container on a persistent ESSD disk.", Version: "1.0.0", Category: "infra", Icon: "cloud", BinaryAliases: []string{"us-spot-dev"}},
		{Name: "hk-spot-dev", Title: "HK Spot Dev", Description: "Provision an HK Aliyun spot dev container (companion to us-spot-dev).", Version: "1.0.0", Category: "infra", Icon: "cloud", BinaryAliases: []string{"hk-spot-dev"}},
		{Name: "skill-author", Title: "Skill Author", Description: "Author and publish custom Claude/Codex/OpenCode skills under ~/cicy-ai/skills/. Auto-installed; cannot be uninstalled.", Version: "1.0.0", Category: "dev", Icon: "package", Tags: []string{"meta", "scaffold", "author"}, BinaryAliases: []string{"skill-author"}},
		{Name: "cicy-todo", Title: "Cicy Todo", Description: "Per-workspace todo list (todo/doing/done/dropped) backed by YAML. CLI (cicy-todo) and the Workspace Todo tab share one file at <workspace>/.cicy/todos.yaml.", Version: "1.0.0", Category: "productivity", Icon: "check-square", Tags: []string{"todo", "task", "workspace"}, BinaryAliases: []string{"cicy-todo"}},
	}
}

// userSkillsRoot is where users author skills via the `skill-author` CLI.
// Each subdirectory (other than the reserved `cicy-skills` source dir and
// any name that collides with a built-in catalog entry) is treated as a
// user skill and surfaced in the marketplace.
func userSkillsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "cicy-ai", "skills")
}

// reservedUserSkillDirs are subdirectories of userSkillsRoot() that must
// never be treated as user skills. cicy-skills itself lives here.
var reservedUserSkillDirs = map[string]struct{}{
	"cicy-skills": {},
}

// parseSkillFrontmatter extracts `name:` and `description:` from the YAML
// frontmatter block at the top of a SKILL.md file. Returns empty strings
// if the file is missing or malformed — callers fall back to the directory
// name and a placeholder description.
func parseSkillFrontmatter(path string) (name, description string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		} else if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	return name, description
}

func titleizeSkillName(name string) string {
	parts := strings.Split(name, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// scanUserSkills walks userSkillsRoot() and returns a marketSkill entry
// for each user-authored skill (Source="user"). Built-in names and
// reserved dirs are skipped — the built-in catalog is authoritative.
func scanUserSkills(builtinNames map[string]struct{}) []marketSkill {
	root := userSkillsRoot()
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []marketSkill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, reserved := reservedUserSkillDirs[name]; reserved {
			continue
		}
		if _, isBuiltin := builtinNames[name]; isBuiltin {
			continue
		}
		skillMd := filepath.Join(root, name, "SKILL.md")
		if _, err := os.Stat(skillMd); err != nil {
			continue
		}
		fmName, fmDesc := parseSkillFrontmatter(skillMd)
		if fmName == "" {
			fmName = name
		}
		if fmDesc == "" {
			fmDesc = "User-authored skill (no description provided in SKILL.md frontmatter)."
		}
		out = append(out, marketSkill{
			Name:        fmName,
			Title:       titleizeSkillName(fmName),
			Description: fmDesc,
			Version:     "user",
			Category:    "user",
			Icon:        "user",
			Source:      "user",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// mergedMarketCatalog returns the built-in catalog with discovered user
// skills appended. Built-in names win on collisions.
func mergedMarketCatalog() []marketSkill {
	builtin := marketSkillsCatalog()
	names := make(map[string]struct{}, len(builtin))
	for _, s := range builtin {
		names[s.Name] = struct{}{}
	}
	user := scanUserSkills(names)
	return append(builtin, user...)
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

func computeMarketStatus(skill *marketSkill) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	binDir := filepath.Join(home, ".local", "bin")

	symlinkInstalled := false
	for _, alias := range skill.BinaryAliases {
		if _, err := os.Lstat(filepath.Join(binDir, alias)); err == nil {
			symlinkInstalled = true
			break
		}
	}
	if len(skill.BinaryAliases) == 0 {
		symlinkInstalled = true
	}

	skillDocInstalled := true
	_, isAgentgen := agentgenApprovedMarketSkills[skill.Name]
	// User skills follow the same per-profile-doc check as agentgen skills:
	// they are "installed" only when SKILL.md exists in all three profiles.
	if isAgentgen || skill.Source == "user" {
		candidateDirs := append([]string{skill.Name}, skill.legacyAgentSkillDirs...)
		for _, profile := range []string{"claude", "codex", "opencode"} {
			profileDocOK := false
			for _, dir := range candidateDirs {
				doc := filepath.Join(home, "."+profile, "skills", dir, "SKILL.md")
				if _, err := os.Stat(doc); err == nil {
					profileDocOK = true
					break
				}
			}
			if !profileDocOK {
				skillDocInstalled = false
				break
			}
		}
	}

	skill.Status.Installed = symlinkInstalled && skillDocInstalled

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

	ensureSkillAuthorInstalled()
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

func readSkillDoc(skill *marketSkill, file string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	candidateDirs := append([]string{skill.Name}, skill.legacyAgentSkillDirs...)
	for _, profile := range []string{".claude", ".codex", ".opencode"} {
		for _, dir := range candidateDirs {
			p := filepath.Join(home, profile, "skills", dir, file)
			if data, err := os.ReadFile(p); err == nil {
				return string(data)
			}
		}
	}
	return ""
}

func handleSkillMarketAction(w http.ResponseWriter, r *http.Request) {
	// Routes:
	//   GET  /api/skill-market/<name>                 → detail (includes skill_md, help_md, tools_md)
	//   POST /api/skill-market/<name>/install
	//   POST /api/skill-market/<name>/uninstall
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
		logs, err := installMarketSkill(skill)
		if err != nil {
			J(w, M{"ok": false, "error": err.Error(), "log": logs})
			return
		}
		computeMarketStatus(skill)
		J(w, M{"ok": true, "status": skill.Status, "log": logs})
	case "uninstall":
		logs, err := uninstallMarketSkill(skill)
		if err != nil {
			J(w, M{"ok": false, "error": err.Error(), "log": logs})
			return
		}
		computeMarketStatus(skill)
		J(w, M{"ok": true, "status": skill.Status, "log": logs})
	default:
		httpErr(w, 400, "unknown action: "+parts[1])
	}
}

// agentgen-approved skills — must stay in sync with
// skills/internal/agentgen/generate.go ApprovedCodexSkills(). Catalog entries
// outside this set are symlink-only (us-spot-dev, cicy-master, hk-spot-dev)
// and have no per-profile SKILL.md handling.
var agentgenApprovedMarketSkills = map[string]struct{}{
	"agent-chrome":              {},
	"agent-code-server":         {},
	"agent-desktop":             {},
	"agent-summary":             {},
	"agent-webpage":             {},
	"cf":                        {},
	"cf-tunnel":                 {},
	"cping":                     {},
	"frp-client":                {},
	"frp-server":                {},
	"globalApiToken":            {},
	"google":                    {},
	"cicy-ssh":                  {},
	"cicy-agent":                {},
	"cicy-mihomo":               {},
	"us-spot-proxy":             {},
	"proxy_ssh":                 {},
	"aliyun-cli":                {},
	"email":                     {},
	"us-spot-dev":               {},
	"hk-spot-dev":               {},
	"cicy-todo":                 {},
}

// hosttool aliases — symlink target is dist/cicy-hosttools. Must stay in sync
// with skills/internal/bundle/bundle.go HosttoolAliases.
var hosttoolAliasSet = map[string]struct{}{
	"agent-chrome": {}, "agent-code-server": {}, "agent-desktop": {}, "agent-webpage": {}, "cf": {}, "cf-tunnel": {},
	"cf-tunnel-py": {}, "cf-tunnel.py": {}, "cping": {}, "eng": {},
	"gemini-ask": {}, "gemini-vision": {}, "globalApiToken": {},
	"google":   {}, // pure-Go google skill (was Node provider; migrated)
	"gpt":      {}, "gpt-chat": {}, "frp-client": {}, "frp-server": {}, "cicy-mihomo": {},
	"mysql-exec": {}, "tg": {}, "cicy-agent": {}, "todo": {}, "aliyun-cli": {}, "email": {},
	"ssh-list": {},
}

// resolveSymlinkSource maps an alias name to the file it should symlink to
// under the extracted cicy-skills project tree. Returns "" if the alias has
// no known source (caller should skip without error — some skills like
// docker-build-github-action ship as SKILL.md only with no binary).
func resolveSymlinkSource(alias string) string {
	projectRoot := cicySkillsProjectDir()
	if _, ok := hosttoolAliasSet[alias]; ok {
		return filepath.Join(projectRoot, "dist", "cicy-hosttools")
	}
	switch alias {
	case "proxy_ssh", "us-spot-dev", "us-spot-proxy", "hk-spot-dev":
		return filepath.Join(projectRoot, alias)
	}
	return ""
}

func userBinDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "bin")
}

func runCicySkillsAgent(action, profile, skillName string, sink *[]string) error {
	cli := cicySkillsCommandPath("cicy-skills")
	if cli == "" {
		return fmt.Errorf("cicy-skills CLI not found")
	}
	if info, err := os.Stat(cli); err != nil || info.Mode()&0o111 == 0 {
		return fmt.Errorf("cicy-skills CLI not executable at %s", cli)
	}
	cmd := exec.Command(cli, "agent", action, profile, skillName)
	cmd.Env = append(os.Environ(), "CICY_SKILLS_ROOT="+cicySkillsProjectDir())
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	line := fmt.Sprintf("[%s] cicy-skills agent %s %s %s",
		time.Now().Format(time.RFC3339), action, profile, skillName)
	if trim := strings.TrimSpace(buf.String()); trim != "" {
		line += " — " + trim
	}
	if err != nil {
		line += fmt.Sprintf(" (error: %v)", err)
	}
	*sink = append(*sink, line)
	if err != nil {
		return fmt.Errorf("cicy-skills agent %s %s %s: %w", action, profile, skillName, err)
	}
	return nil
}

func installMarketSkill(skill *marketSkill) ([]string, error) {
	logs := []string{}
	if skill.Source == "user" {
		return installUserSkill(skill)
	}
	if _, ok := agentgenApprovedMarketSkills[skill.Name]; ok {
		for _, profile := range []string{"codex", "claude", "opencode"} {
			if err := runCicySkillsAgent("install", profile, skill.Name, &logs); err != nil {
				return logs, err
			}
		}
	} else {
		logs = append(logs, fmt.Sprintf("skill %q has no per-profile SKILL.md (symlink-only)", skill.Name))
	}

	binDir := userBinDir()
	if binDir == "" {
		return logs, fmt.Errorf("could not resolve user bin directory")
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return logs, fmt.Errorf("mkdir %s: %w", binDir, err)
	}
	for _, alias := range skill.BinaryAliases {
		source := resolveSymlinkSource(alias)
		if source == "" {
			logs = append(logs, fmt.Sprintf("alias %q has no known symlink source — skipped", alias))
			continue
		}
		if _, err := os.Stat(source); err != nil {
			logs = append(logs, fmt.Sprintf("source missing for alias %q: %s — skipped", alias, source))
			continue
		}
		link := filepath.Join(binDir, alias)
		_ = os.Remove(link)
		if err := os.Symlink(source, link); err != nil {
			return logs, fmt.Errorf("symlink %s -> %s: %w", link, source, err)
		}
		logs = append(logs, fmt.Sprintf("linked %s -> %s", link, source))
	}

	if err := markSkillInstalled(skill.Name); err != nil {
		log.Printf("[skill-market] markSkillInstalled(%s): %v", skill.Name, err)
	}
	return logs, nil
}

// ensureSkillAuthorInstalled auto-installs the skill-author meta-skill into all
// three agent profiles if it is not already present. Called on every skill-market
// list request so new installs get it without a manual install step.
func ensureSkillAuthorInstalled() {
	src := filepath.Join(userSkillsRoot(), "cicy-skills", "skill-author")
	if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
		return // source not present — nothing to install
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, profile := range []string{"claude", "codex", "opencode"} {
		dst := filepath.Join(home, "."+profile, "skills", "skill-author")
		if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err == nil {
			continue // already installed in this profile
		}
		_ = os.RemoveAll(dst)
		_ = copyTree(src, dst)
	}
}

func uninstallMarketSkill(skill *marketSkill) ([]string, error) {
	logs := []string{}
	if skill.Name == "skill-author" {
		return nil, fmt.Errorf("skill-author cannot be uninstalled; use 'skill-author install skill-author' to reinstall")
	}
	if skill.Source == "user" {
		return uninstallUserSkill(skill)
	}
	if _, ok := agentgenApprovedMarketSkills[skill.Name]; ok {
		for _, profile := range []string{"codex", "claude", "opencode"} {
			if err := runCicySkillsAgent("remove", profile, skill.Name, &logs); err != nil {
				return logs, err
			}
		}
	} else {
		logs = append(logs, fmt.Sprintf("skill %q has no per-profile SKILL.md (symlink-only)", skill.Name))
	}

	binDir := userBinDir()
	for _, alias := range skill.BinaryAliases {
		link := filepath.Join(binDir, alias)
		if err := os.Remove(link); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return logs, fmt.Errorf("remove %s: %w", link, err)
		}
		logs = append(logs, fmt.Sprintf("removed %s", link))
	}

	if err := markSkillUninstalled(skill.Name); err != nil {
		log.Printf("[skill-market] markSkillUninstalled(%s): %v", skill.Name, err)
	}
	return logs, nil
}

// installUserSkill copies ~/cicy-ai/skills/<name>/ into all three agent
// profile directories (.claude / .codex / .opencode). Idempotent — any
// existing per-profile copy is removed first so edits to the source dir
// take effect.
func installUserSkill(skill *marketSkill) ([]string, error) {
	logs := []string{}
	root := userSkillsRoot()
	if root == "" {
		return logs, fmt.Errorf("could not resolve user skills root")
	}
	src := filepath.Join(root, skill.Name)
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return logs, fmt.Errorf("user skill source missing: %s", src)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return logs, fmt.Errorf("resolve home: %w", err)
	}
	for _, profile := range []string{"claude", "codex", "opencode"} {
		dst := filepath.Join(home, "."+profile, "skills", skill.Name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return logs, fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
		}
		if err := os.RemoveAll(dst); err != nil {
			return logs, fmt.Errorf("rm %s: %w", dst, err)
		}
		if err := copyTree(src, dst); err != nil {
			return logs, fmt.Errorf("copy %s -> %s: %w", src, dst, err)
		}
		logs = append(logs, fmt.Sprintf("installed user skill %s -> %s", skill.Name, dst))
	}
	return logs, nil
}

// uninstallUserSkill deletes the user skill source AND every per-profile
// copy. This is the marketplace UI's "Delete" action — the user explicitly
// asked for view + delete only, so uninstall on a user skill is destructive.
func uninstallUserSkill(skill *marketSkill) ([]string, error) {
	logs := []string{}
	root := userSkillsRoot()
	home, err := os.UserHomeDir()
	if err != nil {
		return logs, fmt.Errorf("resolve home: %w", err)
	}
	// Profile copies first — the source is the source of truth, so removing
	// it first would leave orphans on disk if a profile rm later fails.
	for _, profile := range []string{"claude", "codex", "opencode"} {
		dst := filepath.Join(home, "."+profile, "skills", skill.Name)
		if err := os.RemoveAll(dst); err != nil {
			return logs, fmt.Errorf("rm %s: %w", dst, err)
		}
		logs = append(logs, fmt.Sprintf("removed %s", dst))
	}
	if root != "" {
		src := filepath.Join(root, skill.Name)
		if err := os.RemoveAll(src); err != nil {
			return logs, fmt.Errorf("rm source %s: %w", src, err)
		}
		logs = append(logs, fmt.Sprintf("removed source %s", src))
	}
	return logs, nil
}

// copyTree recursively copies src to dst, preserving file modes. Symlinks
// are recreated as symlinks (cp -a behavior). Used for user-skill install.
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyTree(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
