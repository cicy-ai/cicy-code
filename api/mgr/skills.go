package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	Status        marketSkillStatus `json:"status"`
}

func marketSkillsCatalog() []marketSkill {
	return []marketSkill{
		{Name: "cf-tunnel", Title: "Cloudflare Tunnel", Description: "Manage Cloudflare Tunnel routes and DNS records on this host.", Version: "1.0.0", Category: "network", Icon: "globe", Tags: []string{"cloudflare", "tunnel", "dns"}, BinaryAliases: []string{"cf-tunnel"}, ConfigFile: "~/cicy-ai/db/cf.json"},
		{Name: "cping", Title: "cping", Description: "Quick network latency check for a domain or IP.", Version: "1.0.0", Category: "network", Icon: "activity", BinaryAliases: []string{"cping"}},
		{Name: "frp-server", Title: "FRP Server", Description: "Run frps in the background with status, reload, connections.", Version: "1.0.0", Category: "network", Icon: "server", BinaryAliases: []string{"frp-server"}, ConfigFile: "~/data/frp/frps.toml"},
		{Name: "frp-client", Title: "FRP Client", Description: "Run frpc, with remote management over SSH.", Version: "1.0.0", Category: "network", Icon: "plug", BinaryAliases: []string{"frp-client"}},
		// google: temporarily delisted — OAuth flow needs a central cicy-ai.com
		// redirect to support localhost/no-domain workers. Re-add once that's done.
		// {Name: "google", Title: "Google Workspace", Description: "Gmail / Sheets / Drive / Calendar via Google APIs.", Version: "1.0.0", Category: "ai", Icon: "mail", Tags: []string{"gmail", "sheets", "drive", "calendar"}, BinaryAliases: []string{"google"}, ConfigFile: "~/cicy-ai/db/google.json"},
		{Name: "agent-summary", Title: "Agent Summary", Description: "Generate conversation summaries and handoff documents.", Version: "1.0.0", Category: "ai", Icon: "file-text", BinaryAliases: []string{}},
		{Name: "agent-webpage", Title: "Agent Webpage", Description: "Talk to the live webpage client for an agent.", Version: "1.0.0", Category: "ai", Icon: "globe", BinaryAliases: []string{"agent-webpage"}},
		{Name: "agent-code-server", Title: "Code Server", Description: "Open files in the page-bound code-server.", Version: "1.0.0", Category: "ai", Icon: "code", BinaryAliases: []string{"agent-code-server"}},
		{Name: "cicy-agent", Title: "cicy-agent", Description: "Operate tmux panes and windows on this host.", Version: "1.0.0", Category: "dev", Icon: "terminal", BinaryAliases: []string{"cicy-agent"}, ConfigFile: "~/cicy-ai/db/cicy-agent.json"},
		{Name: "cicy-ssh", Title: "cicy-ssh", Description: "Manage SSH hosts via ~/.ssh/config.", Version: "1.0.0", Category: "ops", Icon: "key", BinaryAliases: []string{}},
		{Name: "globalApiToken", Title: "Global API Token", Description: "Show or refresh ~/cicy-ai/global.json api_token.", Version: "1.0.0", Category: "ops", Icon: "shield", BinaryAliases: []string{"globalApiToken"}, ConfigFile: "~/cicy-ai/global.json"},
		{Name: "docker-build-github-action", Title: "Docker Build (GHCR)", Description: "Build base images on GitHub Actions and push to GHCR.", Version: "1.0.0", Category: "infra", Icon: "package", BinaryAliases: []string{}, ConfigFile: "~/cicy-ai/db/docker-build-ghcr.json"},
		{Name: "us-spot-proxy", Title: "US Spot Proxy", Description: "Manage Aliyun spot proxy nodes.", Version: "1.0.0", Category: "infra", Icon: "cloud", BinaryAliases: []string{"us-spot-proxy"}, ConfigFile: "~/cicy-ai/db/us-spot-proxy.json"},
		{Name: "cicy-mihomo", Title: "Cicy Mihomo Proxy", Description: "Run a local Cicy Mihomo (mihomo / clash-meta fork) proxy with start/stop/reload/logs, node speed testing, and per-worker auth + routing.", Version: "1.0.0", Category: "network", Icon: "shield", Tags: []string{"proxy", "mihomo", "clash"}, BinaryAliases: []string{"cicy-mihomo"}, ConfigFile: "~/cicy-ai/db/mihomo.yaml"},
		{Name: "proxy_ssh", Title: "SSH SOCKS Proxy", Description: "Manage local autossh-based SOCKS proxy profiles (start/stop/restart/test).", Version: "1.0.0", Category: "network", Icon: "plug", BinaryAliases: []string{"proxy_ssh"}, ConfigFile: "~/cicy-ai/db/proxy_ssh.json"},
		{Name: "us-spot-dev", Title: "US Spot Dev", Description: "Provision a US Aliyun spot dev container on a persistent ESSD disk.", Version: "1.0.0", Category: "infra", Icon: "cloud", BinaryAliases: []string{"us-spot-dev"}},
		{Name: "cicy-master", Title: "CiCy Master", Description: "Manage and sync the multi-node CiCy machine registry from the master CLI.", Version: "1.0.0", Category: "dev", Icon: "server", BinaryAliases: []string{"cicy-master"}, ConfigFile: "~/cicy-ai/db/cicy-master.json"},
		{Name: "hk-spot-dev", Title: "HK Spot Dev", Description: "Provision an HK Aliyun spot dev container (companion to us-spot-dev).", Version: "1.0.0", Category: "infra", Icon: "cloud", BinaryAliases: []string{"hk-spot-dev"}},
	}
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
	if _, ok := agentgenApprovedMarketSkills[skill.Name]; ok {
		for _, profile := range []string{"claude", "codex", "opencode"} {
			doc := filepath.Join(home, "."+profile, "skills", skill.Name, "SKILL.md")
			if _, err := os.Stat(doc); err != nil {
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

	skills := marketSkillsCatalog()
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
	for i, s := range marketSkillsCatalog() {
		if s.Name == name {
			skill := marketSkillsCatalog()[i]
			return &skill
		}
	}
	return nil
}

func readSkillDoc(name, file string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, profile := range []string{".claude", ".codex", ".opencode"} {
		p := filepath.Join(home, profile, "skills", name, file)
		if data, err := os.ReadFile(p); err == nil {
			return string(data)
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
			"skill_md": readSkillDoc(skill.Name, "SKILL.md"),
			"help_md":  readSkillDoc(skill.Name, filepath.Join("references", "help.md")),
			"tools_md": readSkillDoc(skill.Name, filepath.Join("references", "tools.md")),
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
	"agent-code-server":         {},
	"agent-summary":             {},
	"agent-webpage":             {},
	"cf-tunnel":                 {},
	"cping":                     {},
	"docker-build-github-action": {},
	"frp-client":                {},
	"frp-server":                {},
	"globalApiToken":            {},
	// "google":                    {}, // delisted with the marketplace entry above
	"cicy-ssh":                  {},
	"cicy-agent":                {},
	"cicy-mihomo":               {},
	"us-spot-proxy":             {},
	"proxy_ssh":                 {},
}

// hosttool aliases — symlink target is dist/cicy-hosttools. Must stay in sync
// with skills/internal/bundle/bundle.go HosttoolAliases.
var hosttoolAliasSet = map[string]struct{}{
	"agent-code-server": {}, "agent-webpage": {}, "cf-tunnel": {},
	"cf-tunnel-py": {}, "cf-tunnel.py": {}, "cping": {}, "eng": {},
	"gemini-ask": {}, "gemini-vision": {}, "globalApiToken": {},
	"google":   {}, // pure-Go google skill (was Node provider; migrated)
	"gpt":      {}, "gpt-chat": {}, "frp-client": {}, "frp-server": {}, "cicy-mihomo": {},
	"mysql-exec": {}, "tg": {}, "cicy-agent": {}, "todo": {},
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
	case "proxy_ssh", "us-spot-dev", "us-spot-proxy", "cicy-master", "hk-spot-dev":
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

func uninstallMarketSkill(skill *marketSkill) ([]string, error) {
	logs := []string{}
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
