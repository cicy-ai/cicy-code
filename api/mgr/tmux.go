package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// enterDelay is the pause between sending text and pressing Enter.
// Heavy TUIs (Copilot, OpenCode) need time to render text in the input buffer.
// TODO: make this per-agent-type once agent detection is reliable.
const enterDelay = 600 * time.Millisecond

func handlePanes(w http.ResponseWriter, r *http.Request) {
	gid := r.URL.Query().Get("group_id")
	var rows *sql.Rows
	var err error
	if gid != "" {
		rows, err = store.Query(`SELECT DISTINCT t.pane_id, t.title, t.ttyd_port, t.workspace, t.init_script, t.active, t.created_at, t.updated_at, gp.group_id, t.role, t.default_model, t.trust_level, t.agent_type, COALESCE(t.allow_all_actions, 0)
			FROM agent_config t INNER JOIN group_windows gp ON t.pane_id=gp.win_id WHERE gp.group_id=? AND t.active=1 ORDER BY t.created_at DESC`, gid)
	} else {
		rows, err = store.Query(`SELECT t.pane_id, t.title, t.ttyd_port, t.workspace, t.init_script, t.active, t.created_at, t.updated_at, gp.group_id, t.role, t.default_model, t.trust_level, t.agent_type, COALESCE(t.allow_all_actions, 0)
			FROM agent_config t LEFT JOIN group_windows gp ON t.pane_id=gp.win_id WHERE t.active=1 ORDER BY t.created_at DESC`)
	}
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	var panes []M
	for rows.Next() {
		var paneID, title, workspace sql.NullString
		var initScript sql.NullString
		var port sql.NullInt64
		var active sql.NullInt64
		var createdAt, updatedAt sql.NullString
		var groupID sql.NullInt64
		var role, defaultModel, trustLevel sql.NullString
		var agentType sql.NullString
		var allowAllActions sql.NullBool
		rows.Scan(&paneID, &title, &port, &workspace, &initScript, &active, &createdAt, &updatedAt, &groupID, &role, &defaultModel, &trustLevel, &agentType, &allowAllActions)
		p := M{
			"pane_id": paneID.String, "title": title.String, "ttyd_port": port.Int64,
			"workspace": workspace.String, "init_script": initScript.String,
			"active": active.Int64,
			"role":   role.String, "default_model": defaultModel.String,
			"trust_level": trustLevel.String, "agent_type": agentType.String,
			"allow_all_actions": allowAllActions.Bool,
		}
		if createdAt.Valid {
			p["created_at"] = createdAt.String
		}
		if updatedAt.Valid {
			p["updated_at"] = updatedAt.String
		}
		if groupID.Valid {
			p["group_id"] = groupID.Int64
		} else {
			p["group_id"] = nil
		}
		panes = append(panes, p)
	}
	if panes == nil {
		panes = []M{}
	}
	J(w, M{"panes": panes})
}

func handleCreatePane(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WinName         *string `json:"win_name"`
		InitScript      string  `json:"init_script"`
		Title           string  `json:"title"`
		AgentType       string  `json:"agent_type"`
		Role            string  `json:"role"`
		DefaultModel    string  `json:"default_model"`
		AllowAllActions bool    `json:"allow_all_actions"`
	}
	readBody(r, &req)
	token := getToken(r)

	result, err := doCreatePane(req.Title, req.Role, req.DefaultModel, req.AgentType, req.InitScript, req.AllowAllActions, req.WinName, token)
	if err != nil {
		J(w, M{"success": false, "error": err.Error()})
		return
	}
	J(w, result)
}

func doCreatePane(title, role, defaultModel, agentType, initScript string, allowAllActions bool, winName *string, token string) (M, error) {
	// Get next worker index
	var workerIdx int
	tx, _ := store.Begin()
	tx.QueryRow("SELECT value FROM global_vars WHERE key_name='worker_index'").Scan(&workerIdx)
	if workerIdx == 0 {
		workerIdx = 20000
	}
	workerIdx++
	tx.Exec(store.Upsert("global_vars", "key_name", []string{"key_name", "value"}, []string{"value"}), "worker_index", workerIdx)
	tx.Commit()

	session := fmt.Sprintf("w-%d", workerIdx)
	t := session
	if winName != nil && *winName != "" {
		t = *winName
	}
	if title != "" {
		t = title
	}
	home, _ := os.UserHomeDir()
	workspace := fmt.Sprintf("%s/workers/%s", home, session)
	os.MkdirAll(workspace, 0755)

	paneID := session + ":main.0"
	port := workerIdx

	// Create tmux session
	runTmux("new-session", "-d", "-s", session, "-n", "main", "-c", workspace)
	// Insert DB
	store.Exec(fmt.Sprintf(`INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,%s,%s)`, store.Now(), store.Now()), paneID, t, port, workspace, initScript, "{}", role, defaultModel, agentType, allowAllActions)

	// Start ttyd-go instance
	if err := startInstance(paneID, port, token); err != nil {
		return M{"session": session, "pane_id": shortPaneID(paneID)}, err
	}

	// Wait for port
	waitPort(port, 10*time.Second)

	initPaneEnv(paneEnvOpts{
		paneID:          paneID,
		configJSON:      "{}",
		workspace:       workspace,
		initScript:      initScript,
		agentType:       agentType,
		allowAllActions: allowAllActions,
	})

	return M{
		"success": true, "session": session, "window": "main",
		"pane_id": shortPaneID(paneID), "title": t,
		"workspace": workspace, "init_script": initScript,
		"ttyd_port": port,
	}, nil
}

func handlePaneByID(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/api/tmux/panes/"):
		path = strings.TrimPrefix(path, "/api/tmux/panes/")
	case strings.HasPrefix(path, "/api/panes/"):
		path = strings.TrimPrefix(path, "/api/panes/")
	}
	switch {
	case strings.HasSuffix(path, "/restart") && r.Method == "POST":
		handleRestartPane(w, r, strings.TrimSuffix(path, "/restart"))
	case strings.HasSuffix(path, "/split") && r.Method == "POST":
		handleSplitPane(w, r, strings.TrimSuffix(path, "/split"))
	case strings.HasSuffix(path, "/unsplit") && r.Method == "POST":
		handleUnsplitPane(w, r, strings.TrimSuffix(path, "/unsplit"))
	case strings.HasSuffix(path, "/choose-session") && r.Method == "POST":
		paneID := normPaneID(strings.TrimSuffix(path, "/choose-session"))
		runTmux("choose-tree", "-Zs", "-t", paneID)
		J(w, M{"success": true})
	case r.Method == "GET":
		handleGetPane(w, r, path)
	case r.Method == "PATCH":
		handleUpdatePane(w, r, path)
	case r.Method == "DELETE":
		handleDeletePane(w, r, path)
	default:
		httpErr(w, 404, "not found")
	}
}

func handleGetPane(w http.ResponseWriter, r *http.Request, id string) {
	paneID := normPaneID(id)
	var title, workspace, initScript, agentType, agentDuty, config, commonPrompt, ttydPreview sql.NullString
	var port sql.NullInt64
	var active sql.NullInt64
	var allowAllActions sql.NullBool
	var tgEnable sql.NullBool
	var tgToken, tgChatID sql.NullString
	var groupID sql.NullInt64
	var role, defaultModel, trustLevel sql.NullString
	var machineID sql.NullInt64
	var machineLabel, machineURL, runtimeKind, capabilitiesJSON sql.NullString
	err := store.QueryRow(`SELECT t.pane_id, t.title, t.ttyd_port, t.workspace, t.init_script,
		t.tg_token, t.tg_chat_id, t.tg_enable, t.active, t.agent_type, t.agent_duty, t.config, t.common_prompt, t.ttyd_preview, gp.group_id, t.role, t.default_model, t.trust_level,
		COALESCE(t.allow_all_actions, 0),
		COALESCE(t.machine_id, 0), COALESCE(m.label, ''), COALESCE(m.url, ''), COALESCE(json_extract(m.capabilities_json, '$.runtime_kind'), ''), COALESCE(m.capabilities_json, '{}')
		FROM agent_config t
		LEFT JOIN group_windows gp ON t.pane_id=gp.win_id
		LEFT JOIN machines m ON t.machine_id=m.id
		WHERE t.pane_id=?`, paneID).Scan(
		&paneID, &title, &port, &workspace, &initScript,
		&tgToken, &tgChatID, &tgEnable, &active, &agentType, &agentDuty, &config, &commonPrompt, &ttydPreview, &groupID, &role, &defaultModel, &trustLevel, &allowAllActions,
		&machineID, &machineLabel, &machineURL, &runtimeKind, &capabilitiesJSON)
	if err != nil {
		httpErr(w, 404, "Pane "+id+" not found")
		return
	}
	resp := M{
		"pane_id": shortPaneID(paneID), "title": title.String, "ttyd_port": port.Int64,
		"workspace": workspace.String, "init_script": initScript.String,
		"tg_token": tgToken.String, "tg_chat_id": tgChatID.String, "tg_enable": tgEnable.Bool,
		"active": active.Int64, "agent_type": agentType.String, "agent_duty": agentDuty.String,
		"config": config.String, "common_prompt": commonPrompt.String, "ttyd_preview": ttydPreview.String,
		"allow_all_actions": allowAllActions.Bool,
		"role":              role.String, "default_model": defaultModel.String,
		"trust_level":   trustLevel.String,
		"machine_label": machineLabel.String,
		"machine_url":   machineURL.String,
		"runtime_kind":  runtimeKind.String,
	}
	if machineID.Valid && machineID.Int64 > 0 {
		resp["machine_id"] = machineID.Int64
	} else {
		resp["machine_id"] = nil
	}
	capabilities := M{}
	if strings.TrimSpace(capabilitiesJSON.String) != "" {
		_ = json.Unmarshal([]byte(capabilitiesJSON.String), &capabilities)
	}
	resp["capabilities"] = capabilities
	if groupID.Valid {
		resp["group_id"] = groupID.Int64
	} else {
		resp["group_id"] = nil
	}
	J(w, resp)
}

// columns allowed in agent_config UPDATE
var paneUpdateCols = map[string]bool{
	"title": true, "ttyd_port": true, "workspace": true, "init_script": true,
	"tg_token": true, "tg_chat_id": true, "tg_enable": true, "active": true,
	"agent_type": true, "agent_duty": true, "config": true, "common_prompt": true,
	"ttyd_preview": true, "role": true, "default_model": true, "trust_level": true,
	"allow_all_actions": true,
	"private_mode":      true, "allowed_users": true,
	"node_url": true, "preview": true,
}

func handleUpdatePane(w http.ResponseWriter, r *http.Request, id string) {
	paneID := normPaneID(id)
	var req M
	readBody(r, &req)
	// filter to allowed columns only
	filtered := M{}
	for k, v := range req {
		if paneUpdateCols[k] {
			filtered[k] = v
		}
	}
	if len(filtered) == 0 {
		httpErr(w, 400, "No valid fields to update")
		return
	}
	var sets []string
	var vals []interface{}
	for k, v := range filtered {
		sets = append(sets, k+"=?")
		vals = append(vals, v)
	}
	vals = append(vals, paneID)
	res, err := store.Exec("UPDATE agent_config SET "+strings.Join(sets, ", ")+" WHERE pane_id=?", vals...)
	if err != nil {
		httpErr(w, 500, "update failed: "+err.Error())
		return
	}
	_ = res

	// Sync agent_duty to workspace/.kiro/steering/duty.md
	if duty, ok := filtered["agent_duty"].(string); ok {
		var ws sql.NullString
		store.QueryRow("SELECT workspace FROM agent_config WHERE pane_id=?", paneID).Scan(&ws)
		if ws.String != "" {
			dir := ws.String + "/.kiro/steering"
			os.MkdirAll(dir, 0755)
			os.WriteFile(dir+"/duty.md", []byte("---\ninclusion: always\n---\n\n"+duty), 0644)
		}
	}
	J(w, M{"success": true, "pane_id": shortPaneID(paneID), "updated": filtered})
}

func handleDeletePane(w http.ResponseWriter, r *http.Request, id string) {
	paneID := normPaneID(id)
	var port sql.NullInt64
	store.QueryRow("SELECT ttyd_port FROM agent_config WHERE pane_id=?", paneID).Scan(&port)
	go func() {
		defer func() { recover() }()
		stopInstance(paneID)
		session := strings.Split(paneID, ":")[0]
		runTmux("kill-session", "-t", session)
	}()
	store.Exec("DELETE FROM group_windows WHERE win_id=?", paneID)
	store.Exec("DELETE FROM agent_config WHERE pane_id=?", paneID)
	J(w, M{"success": true, "pane_id": shortPaneID(paneID), "message": "Pane deleted"})
}

func handleRestartPane(w http.ResponseWriter, r *http.Request, id string) {
	paneID := normPaneID(id)
	token := getToken(r)
	if err := restartPaneCore(paneID, token); err != nil {
		J(w, M{"success": false, "error": err.Error()})
		return
	}
	J(w, M{"success": true, "message": "Pane 软重启完成"})
}

func restartPaneCore(paneID, token string) error {
	var port sql.NullInt64
	var workspace, initScript, title, config, agentType, trustLevel sql.NullString
	var allowAllActions sql.NullBool
	err := store.QueryRow("SELECT ttyd_port, workspace, init_script, title, config, agent_type, trust_level, COALESCE(allow_all_actions, 0) FROM agent_config WHERE pane_id=?", paneID).
		Scan(&port, &workspace, &initScript, &title, &config, &agentType, &trustLevel, &allowAllActions)
	if err != nil {
		return fmt.Errorf("pane %s not found in db", paneID)
	}

	// Kill old ttyd
	stopInstance(paneID)
	if port.Valid {
		exec.Command("bash", "-c", fmt.Sprintf("pkill -f 'ttyd.*-p %d '", port.Int64)).Run()
	}
	time.Sleep(500 * time.Millisecond)

	// Kill and recreate tmux session
	session := strings.Split(paneID, ":")[0]
	exec.Command("tmux", "kill-session", "-t", session).Run()
	time.Sleep(300 * time.Millisecond)
	home, _ := os.UserHomeDir()
	ws := workspace.String
	if ws == "" {
		ws = "~"
	}
	wsExpanded := strings.Replace(ws, "~", home, 1)
	exec.Command("tmux", "new-session", "-d", "-s", session, "-n", "main", "-c", wsExpanded).Run()

	// Restart ttyd-go
	p := int(port.Int64)
	if err := startInstance(paneID, p, token); err != nil {
		return err
	}
	waitPort(p, 10*time.Second)

	// Re-run init
	initPaneEnv(paneEnvOpts{
		paneID:          paneID,
		configJSON:      config.String,
		workspace:       wsExpanded,
		initScript:      initScript.String,
		agentType:       agentType.String,
		allowAllActions: allowAllActions.Bool,
	})
	store.Exec(fmt.Sprintf("UPDATE agent_config SET updated_at=%s WHERE pane_id=?", store.Now()), paneID)
	return nil
}

// initPaneEnv sets up env vars, proxy, workspace, and runs init script in a pane.
type paneEnvOpts struct {
	paneID          string
	configJSON      string // JSON config (projects only)
	workspace       string // expanded workspace path
	initScript      string
	agentType       string
	allowAllActions bool
}

func tmuxShellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'"
}

func normalizeAgentType(agentType string) string {
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "openclaw", "opencraw":
		return "openclaw"
	case "codex", "openai", "kiro-cli", "kiro-cli chat", "gemini", "copilot":
		return "codex"
	case "claude", "claude code", "claude-code":
		return "claude"
	case "opencode", "open code", "open-code":
		return "opencode"
	default:
		return ""
	}
}

func visibleAgentInstallLine(commandName, label, installCmd, logPath string) string {
	return fmt.Sprintf(`if ! command -v %s >/dev/null 2>&1; then
  echo '[cicy] =================================================='
  echo '[cicy] %s is not installed. Installing now...'
  echo '[cicy] This may take 1-5 minutes depending on network.'
  echo '[cicy] =================================================='
  install_log=%s
  ( %s ) >"$install_log" 2>&1 &
  install_pid=$!
  elapsed=0
  while kill -0 "$install_pid" 2>/dev/null; do
    elapsed=$((elapsed + 1))
    filled=$(((elapsed %% 20) + 1))
    bar=$(printf '%%-*s' "$filled" '' | tr ' ' '#')
    pad=$(printf '%%-*s' $((20 - filled)) '')
    printf '\r[cicy] [%%s%%s] installing %s... %%3ss' "$bar" "$pad" "$elapsed"
    sleep 1
  done
  wait "$install_pid"
  install_status=$?
  printf '\n'
  if [ "$install_status" -ne 0 ]; then
    echo '[cicy] %s install failed. Recent log:'
    tail -100 "$install_log"
    return 1
  fi
  echo '[cicy] %s install completed.'
fi`, commandName, label, tmuxShellQuote(logPath), installCmd, strings.ToLower(label), strings.ToLower(label), strings.ToLower(label))
}

func agentBootLines(agentType string, allowAllActions bool, shortID string) []string {
	switch normalizeAgentType(agentType) {
	case "openclaw":
		home, _ := os.UserHomeDir()
		baseStateDir := filepath.Join(home, ".openclaw")
		stateDir := filepath.Join(home, ".openclaw-"+shortID)
		installLog := filepath.Join(stateDir, "openclaw-install.log")
		sessionName := strings.ReplaceAll(shortID, "-", "")
		lines := []string{
			fmt.Sprintf("export OPENCLAW_CONFIG_PATH=%s", tmuxShellQuote(filepath.Join(baseStateDir, "openclaw.json"))),
			fmt.Sprintf("export OPENCLAW_STATE_DIR=%s", tmuxShellQuote(stateDir)),
			fmt.Sprintf("export OPENCLAW_SESSION_KEY=%s", tmuxShellQuote("agent:main:"+sessionName)),
			fmt.Sprintf("export OPENCLAW_SESSION_STORE=%s", tmuxShellQuote(filepath.Join(baseStateDir, "agents", "main", "sessions", "sessions.json"))),
			fmt.Sprintf("export OPENAI_API_KEY=%s", tmuxShellQuote(os.Getenv("CICY_API_KEY"))),
			fmt.Sprintf("export OPENAI_BASE_URL=%s", tmuxShellQuote(strings.TrimSpace(os.Getenv("CICY_API_URL")))),
			fmt.Sprintf("mkdir -p %s", tmuxShellQuote(stateDir)),
			fmt.Sprintf("mkdir -p %s %s %s", tmuxShellQuote(filepath.Join(stateDir, "identity")), tmuxShellQuote(filepath.Join(stateDir, "devices")), tmuxShellQuote(filepath.Join(stateDir, "agents", "main", "agent"))),
			fmt.Sprintf("if [ -d %s ]; then cp -a %s/. %s/; fi", tmuxShellQuote(filepath.Join(baseStateDir, "identity")), tmuxShellQuote(filepath.Join(baseStateDir, "identity")), tmuxShellQuote(filepath.Join(stateDir, "identity"))),
			fmt.Sprintf("if [ -d %s ]; then cp -a %s/. %s/; fi", tmuxShellQuote(filepath.Join(baseStateDir, "devices")), tmuxShellQuote(filepath.Join(baseStateDir, "devices")), tmuxShellQuote(filepath.Join(stateDir, "devices"))),
			fmt.Sprintf("rm -f %s", tmuxShellQuote(filepath.Join(stateDir, "agents", "main", "agent", "auth-profiles.json"))),
			`node - <<'EOF'
const fs = require("fs");
const storePath = process.env.OPENCLAW_SESSION_STORE;
const sessionKey = process.env.OPENCLAW_SESSION_KEY;
if (!storePath || !sessionKey) process.exit(0);
try {
  const raw = JSON.parse(fs.readFileSync(storePath, "utf8"));
  const entry = raw[sessionKey];
  if (!entry) process.exit(0);
  if (entry.sessionFile) {
    try { fs.rmSync(entry.sessionFile, { force: true }); } catch (_) {}
  }
  delete raw[sessionKey];
  fs.writeFileSync(storePath, JSON.stringify(raw, null, 2));
} catch (_) {}
EOF`,
			`export OPENCLAW_GATEWAY_TOKEN="$(node -e 'const fs=require("fs"); const p=process.env.OPENCLAW_CONFIG_PATH; try { const data=JSON.parse(fs.readFileSync(p, "utf8")); process.stdout.write((((data.gateway || {}).auth || {}).token || "")); } catch (_) {}')"`,
		}
		if installCmd := openClawInstallCmd(); installCmd != "" {
			lines = append(lines, visibleAgentInstallLine("openclaw", "OpenClaw", installCmd, installLog))
		}
		if allowAllActions {
			approvalsPath := fmt.Sprintf("%s/exec-approvals.json", stateDir)
			lines = append(lines, fmt.Sprintf(`cat > %s <<'EOF'
{
  "version": 1,
  "defaults": {
    "security": "full",
    "ask": "off",
    "askFallback": "full",
    "autoAllowSkills": true
  },
  "agents": {
    "main": {
      "security": "full",
      "ask": "off",
      "askFallback": "full",
      "autoAllowSkills": true
    }
  }
}
EOF`, tmuxShellQuote(approvalsPath)))
		}
		gatewayLog := filepath.Join(stateDir, "openclaw-gateway.log")
		tmpGatewayLog := filepath.Join("/tmp", fmt.Sprintf("openclaw-gateway-%s.log", shortID))
		lines = append(lines,
			fmt.Sprintf(`openclaw_gateway_ready() {
  curl -fsS --max-time 2 http://127.0.0.1:%s/ >/dev/null 2>&1
}`, openClawPort()),
			fmt.Sprintf(`gateway_log=%s`, tmuxShellQuote(tmpGatewayLog)),
			`openclaw_gateway_running() {
  pidof openclaw-gateway >/dev/null 2>&1 || pgrep -A -f 'openclaw-gateway|openclaw gateway run' >/dev/null 2>&1
}`,
			`if ! openclaw_gateway_ready; then`,
			fmt.Sprintf("  echo %s", tmuxShellQuote("[cicy] preparing OpenClaw gateway ...")),
			"  if ! openclaw_gateway_running; then",
			fmt.Sprintf("    echo %s", tmuxShellQuote("[cicy] starting gateway process ...")),
			"    rm -f \"$gateway_log\"",
			"    : > \"$gateway_log\"",
			"    rm -f "+tmuxShellQuote(gatewayLog),
			"    nohup openclaw gateway run --verbose >\"$gateway_log\" 2>&1 </dev/null &",
			"  else",
			fmt.Sprintf("    echo %s", tmuxShellQuote("[cicy] gateway process already exists, waiting for readiness ...")),
			"  fi",
			"  gateway_ready=0",
			"  gateway_conflict_seen=0",
			"  last_log_seen=''",
			"  for i in $(seq 1 60); do",
			"    if openclaw_gateway_ready; then",
			"      gateway_ready=1",
			fmt.Sprintf("      echo %s", tmuxShellQuote("[cicy] OpenClaw gateway ready.")),
			"      break",
			"    fi",
			"    if [ -s \"$gateway_log\" ]; then",
			"      last_log=$(tail -1 \"$gateway_log\" 2>/dev/null | sed 's/[^[:print:]\t]//g')",
			"      if [ -n \"$last_log\" ] && [ \"$last_log\" != \"$last_log_seen\" ]; then",
			"        echo \"[cicy] gateway log :: $last_log\"",
			"        last_log_seen=\"$last_log\"",
			"      fi",
			fmt.Sprintf("      if [ \"$gateway_conflict_seen\" -ne 1 ] && grep -Eq %s \"$gateway_log\"; then", tmuxShellQuote(`another gateway instance is already listening on ws://127\.0\.0\.1:`+openClawPort()+`|Port `+openClawPort()+` is already in use`)),
			fmt.Sprintf("        echo %s", tmuxShellQuote("[cicy] gateway port already in use; waiting for the existing instance ...")),
			"        gateway_conflict_seen=1",
			"      fi",
			"    fi",
			"    if [ $((i % 5)) -eq 0 ]; then",
			"      echo \"[cicy] waiting for gateway startup ${i}s/60s ...\"",
			"    fi",
			"    sleep 1",
			"  done",
			"  if [ \"$gateway_ready\" -ne 1 ]; then",
			fmt.Sprintf("    echo %s", tmuxShellQuote("[cicy] OpenClaw gateway failed to start. Recent log:")),
			"    tail -80 \"$gateway_log\"",
			"    return 1",
			"  fi",
			"fi",
			fmt.Sprintf("openclaw tui --session %s || true", tmuxShellQuote(sessionName)),
			"echo '[cicy] OpenClaw TUI exited. Shell is still active.'",
		)
		return lines
	case "codex":
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("codex-install-%s.log", shortID))
		lines := []string{
			"mkdir -p ~/.cicy",
			visibleAgentInstallLine("codex", "Codex", npmGlobalInstallCmd("@openai/codex"), installLog),
		}
		if allowAllActions {
			lines = append(lines, "codex --dangerously-bypass-approvals-and-sandbox")
			return lines
		}
		lines = append(lines, "codex")
		return lines
	case "claude":
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("claude-install-%s.log", shortID))
		lines := []string{
			"mkdir -p ~/.cicy",
			visibleAgentInstallLine("claude", "Claude Code", npmGlobalInstallCmd("@anthropic-ai/claude-code"), installLog),
		}
		if allowAllActions {
			lines = append(lines, "claude --dangerously-skip-permissions")
			return lines
		}
		lines = append(lines, "claude")
		return lines
	case "opencode":
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("opencode-install-%s.log", shortID))
		lines := []string{
			"mkdir -p ~/.cicy",
			visibleAgentInstallLine("opencode", "OpenCode", "curl -fsSL https://opencode.ai/install | bash", installLog),
		}
		if allowAllActions {
			configPath := fmt.Sprintf("/tmp/opencode_allow_%s.json", shortID)
			lines = append(lines,
				fmt.Sprintf("cat > %s <<'EOF'\n{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"permission\": \"allow\"\n}\nEOF", tmuxShellQuote(configPath)),
				fmt.Sprintf("export OPENCODE_CONFIG=%s", tmuxShellQuote(configPath)),
				"opencode",
			)
			return lines
		}
		lines = append(lines, "opencode")
		return lines
	default:
		return nil
	}
}

func isClaudeInputReady(out string) bool {
	return strings.Contains(out, "Claude Code v") &&
		(strings.Contains(out, "? for shortcuts") ||
			strings.Contains(out, " /effort") ||
			strings.Contains(out, "Welcome back!") ||
			strings.Contains(out, "Recent activity"))
}

func autoConfirmClaudeDanger(paneID string) {
	go func() {
		var lastAction time.Time
		for i := 0; i < 60; i++ {
			time.Sleep(500 * time.Millisecond)
			out, err := runTmux("capture-pane", "-t", paneID, "-p", "-S", "-160")
			if err != nil {
				continue
			}
			if isClaudeInputReady(out) {
				log.Printf("[claude-auto-confirm] %s ready", paneID)
				return
			}
			if time.Since(lastAction) < 900*time.Millisecond {
				continue
			}
			switch {
			case strings.Contains(out, "Quick safety check") && strings.Contains(out, "Yes, I trust this folder"):
				log.Printf("[claude-auto-confirm] %s trust workspace", paneID)
				runTmux("send-keys", "-t", paneID, "Enter")
				lastAction = time.Now()
			case strings.Contains(out, "Bypass Permissions mode") && strings.Contains(out, "Yes, I accept"):
				log.Printf("[claude-auto-confirm] %s accept bypass mode", paneID)
				runTmux("send-keys", "-t", paneID, "Down")
				time.Sleep(150 * time.Millisecond)
				runTmux("send-keys", "-t", paneID, "Enter")
				lastAction = time.Now()
			case strings.Contains(out, "Bypass Permissions mode") && strings.Contains(out, "Enter to confirm"):
				log.Printf("[claude-auto-confirm] %s confirm bypass mode", paneID)
				runTmux("send-keys", "-t", paneID, "Enter")
				lastAction = time.Now()
			}
		}
		log.Printf("[claude-auto-confirm] %s timeout", paneID)
	}()
}

func initPaneEnv(opts paneEnvOpts) {
	pid := opts.paneID
	shortID := strings.Split(pid, ":")[0]
	// proxyURL := fmt.Sprintf("http://%s:x@127.0.0.1:17080", shortID)

	lines := []string{
		"touch ~/.cicy_tmux.conf",
		"source ~/.cicy_tmux.conf",
		`export PATH="$HOME/.local/bin:$HOME/.opencode/bin:$PATH"`,
		fmt.Sprintf("export X_AGENT_ID=%s", tmuxShellQuote(pid)),
		fmt.Sprintf("export X_AGENT_SHORT_ID=%s", tmuxShellQuote(shortID)),
		//fmt.Sprintf("export HTTP_PROXY=%s", tmuxShellQuote(proxyURL)),
		// "export HTTPS_PROXY=\"$HTTP_PROXY\"",
		// "export ALL_PROXY=\"$HTTP_PROXY\"",
		// "export http_proxy=\"$HTTP_PROXY\"",
		// "export https_proxy=\"$HTTP_PROXY\"",
		// "export all_proxy=\"$HTTP_PROXY\"",
		// "export NO_PROXY='localhost,127.0.0.1'",
		// "export no_proxy=\"$NO_PROXY\"",
	}

	if opts.workspace != "" {
		lines = append(lines,
			"cd "+tmuxShellQuote(opts.workspace),
			fmt.Sprintf("export WORKSPACE=%s", tmuxShellQuote(opts.workspace)),
			"mkdir -p ./skills ./projects",
		)
	}

	if opts.configJSON != "" && opts.configJSON != "{}" {
		var cfg struct {
			Projects []string `json:"projects"`
		}
		if json.Unmarshal([]byte(opts.configJSON), &cfg) == nil {
			for _, p := range cfg.Projects {
				p = strings.TrimSpace(strings.TrimRight(p, "/"))
				if p == "" {
					continue
				}
				lines = append(lines, fmt.Sprintf("ln -sfn -- %s ./projects/", tmuxShellQuote(p)))
			}
		}
	}

	if strings.TrimSpace(opts.initScript) != "" {
		lines = append(lines, opts.initScript)
	}
	lines = append(lines, agentBootLines(opts.agentType, opts.allowAllActions, shortID)...)

	// 写临时脚本，执行后删除
	script := "#!/usr/bin/env bash\n\n" + strings.Join(lines, "\n") + "\n"
	tmpFile := fmt.Sprintf("/tmp/init_pane_%s.sh", strings.ReplaceAll(pid, ":", "_"))
	if err := os.WriteFile(tmpFile, []byte(script), 0700); err != nil {
		log.Printf("[init] failed to write script: %v", err)
		return
	}
	log.Printf("[init] v1 pane %s script:\n%s", pid, script)

	// 轮询等待 shell 就绪（发 echo 检测响应）
	ready := false
	for i := 0; i < 20; i++ {
		time.Sleep(200 * time.Millisecond)
		if _, err := exec.Command("tmux", "capture-pane", "-t", pid, "-p", "-S", "-1").Output(); err == nil {
			log.Printf("[init] shell ready after %d attempts", i+1)
			ready = true
			break
		}
	}
	if !ready {
		log.Printf("[init] shell not confirmed, continue anyway")
	}

	runTmux("send-keys", "-t", pid, fmt.Sprintf("source %s", tmuxShellQuote(tmpFile)), "Enter")
	if normalizeAgentType(opts.agentType) == "claude" && opts.allowAllActions {
		autoConfirmClaudeDanger(pid)
	}
}

func handleRestartAll(w http.ResponseWriter, r *http.Request) {
	rows, _ := store.Query("SELECT pane_id FROM agent_config WHERE active=1")
	defer rows.Close()
	var results []M
	for rows.Next() {
		var pid string
		rows.Scan(&pid)
		// Simplified: just mark as restarted
		results = append(results, M{"pane_id": pid, "success": true})
	}
	J(w, M{"success": true, "results": results, "total": len(results)})
}

func handleSend(w http.ResponseWriter, r *http.Request) {
	var req M
	readBody(r, &req)
	winID, _ := req["win_id"].(string)
	if winID == "" {
		winID, _ = req["pane_id"].(string)
	}
	winID = normPaneID(winID)
	if winID == "" {
		J(w, M{"error": "win_id required"})
		return
	}
	if text, ok := req["text"].(string); ok && text != "" {
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			line = strings.ReplaceAll(line, "'", "'\\''")
			runTmux("send-keys", "-t", winID, "-l", line)
			if i < len(lines)-1 {
				time.Sleep(100 * time.Millisecond)
				runTmux("send-keys", "-t", winID, "Enter")
			}
		}
		time.Sleep(enterDelay)
		runTmux("send-keys", "-t", winID, "Enter")
	} else if keys, ok := req["keys"].(string); ok && keys != "" {
		runTmux("send-keys", "-t", winID, keys)
	}
	J(w, M{"success": true, "win_id": shortPaneID(winID)})
}

func handleSendKeys(w http.ResponseWriter, r *http.Request) {
	var req M
	readBody(r, &req)
	winID, _ := req["win_id"].(string)
	winID = normPaneID(winID)
	if winID == "" {
		J(w, M{"error": "win_id required"})
		return
	}
	keys, _ := req["keys"].(string)
	if keys == "" {
		J(w, M{"error": "keys required"})
		return
	}
	runTmux("send-keys", "-t", winID, keys)
	J(w, M{"success": true, "win_id": shortPaneID(winID)})
}

func handleCapture(w http.ResponseWriter, r *http.Request) {
	var req M
	readBody(r, &req)
	paneID, _ := req["pane_id"].(string)
	paneID = normPaneID(paneID)
	if paneID == "" {
		J(w, M{"error": "pane_id required"})
		return
	}
	lines := 100
	if l, ok := req["lines"].(float64); ok && l > 0 {
		lines = int(l)
	}
	if nodeURL := nodeURL(paneID); nodeURL != "" {
		out, err := remoteCapture(nodeURL, nodeToken(paneID), shortPaneID(paneID), lines)
		if err == nil {
			J(w, M{"pane_id": shortPaneID(paneID), "output": out})
			return
		}
	}
	out, _ := runTmux("capture-pane", "-t", paneID, "-p", "-S", fmt.Sprintf("-%d", lines))
	J(w, M{"pane_id": shortPaneID(paneID), "output": out + "\n"})
}

// handleWindows — CRUD for tmux windows within a session
// GET    ?session=xxx           → list windows
// POST   {session, name}        → new-window
// PATCH  {session, index, name} → rename-window
// DELETE {session, index}       → kill-window
func handleWindows(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		s := r.URL.Query().Get("session")
		if s == "" {
			httpErr(w, 400, "session required")
			return
		}
		wo, err := runTmux("list-windows", "-t", s, "-F", "#{window_index}|#{window_name}|#{window_active}")
		if err != nil {
			J(w, M{"windows": []M{}})
			return
		}
		var wins []M
		for _, line := range strings.Split(wo, "\n") {
			parts := strings.SplitN(line, "|", 3)
			if len(parts) < 3 {
				continue
			}
			wins = append(wins, M{"index": parts[0], "name": parts[1], "active": parts[2] == "1"})
		}
		J(w, M{"windows": wins})
	case "POST":
		var body struct {
			Session string `json:"session"`
			Name    string `json:"name"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Session == "" {
			httpErr(w, 400, "session required")
			return
		}
		home, _ := os.UserHomeDir()
		workersDir := filepath.Join(home, "workers", body.Session)
		_ = os.MkdirAll(workersDir, 0755)
		args := []string{"new-window", "-c", workersDir, "-t", body.Session}
		if body.Name != "" {
			args = append(args, "-n", body.Name)
		}
		_, err := runTmux(args...)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true})
	case "PATCH":
		var body struct {
			Session string `json:"session"`
			Index   string `json:"index"`
			Name    string `json:"name"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Session == "" || body.Index == "" || body.Name == "" {
			httpErr(w, 400, "session, index, name required")
			return
		}
		_, err := runTmux("rename-window", "-t", body.Session+":"+body.Index, body.Name)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true})
	case "DELETE":
		var body struct {
			Session string `json:"session"`
			Index   string `json:"index"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Session == "" || body.Index == "" {
			httpErr(w, 400, "session, index required")
			return
		}
		_, err := runTmux("kill-window", "-t", body.Session+":"+body.Index)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true})
	case "PUT":
		var body struct {
			Session string `json:"session"`
			Index   string `json:"index"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Session == "" || body.Index == "" {
			httpErr(w, 400, "session, index required")
			return
		}
		_, err := runTmux("select-window", "-t", body.Session+":"+body.Index)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleTree(w http.ResponseWriter, r *http.Request) {
	out, err := runTmux("list-sessions", "-F", "#{session_name}")
	if err != nil {
		J(w, M{"tree": []M{}})
		return
	}
	var tree []M
	for _, s := range strings.Split(out, "\n") {
		if s == "" {
			continue
		}
		wo, err := runTmux("list-windows", "-t", s, "-F", "#{window_index}|#{window_name}|#{window_active}")
		var windows []M
		if err == nil {
			for _, line := range strings.Split(wo, "\n") {
				parts := strings.SplitN(line, "|", 3)
				if len(parts) < 3 {
					continue
				}
				windows = append(windows, M{"index": parts[0], "name": parts[1], "active": parts[2] == "1", "pane": s + ":" + parts[1] + ".0"})
			}
		}
		tree = append(tree, M{"session": s, "windows": windows})
	}
	J(w, M{"tree": tree})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	J(w, M{})
}

func handleSendWait(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target     string `json:"target"`
		Text       string `json:"text"`
		PromptType string `json:"prompt_type"`
		Timeout    int    `json:"timeout"`
	}
	readBody(r, &req)
	if req.PromptType == "" {
		req.PromptType = "kiro-cli"
	}
	if req.Timeout == 0 {
		req.Timeout = 60
	}
	if req.Timeout > 120 {
		req.Timeout = 120
	}

	paneID := req.Target
	// Resolve @title
	if strings.HasPrefix(paneID, "@") {
		store.QueryRow("SELECT pane_id FROM agent_config WHERE title=? LIMIT 1", paneID[1:]).Scan(&paneID)
		if paneID == "" {
			J(w, M{"success": false, "error": fmt.Sprintf("No pane found with title '%s'", req.Target[1:])})
			return
		}
	} else {
		paneID = normPaneID(paneID)
	}

	var promptRe *regexp.Regexp
	if req.PromptType == "kiro-cli" {
		promptRe = regexp.MustCompile(`\d+%\s*>\s*$`)
	} else if req.PromptType == "bash" {
		promptRe = regexp.MustCompile(`w-\d+\s+\$\s*$`)
	} else {
		J(w, M{"success": false, "error": "Invalid prompt_type: " + req.PromptType})
		return
	}

	// Capture baseline
	baseline, _ := runTmux("capture-pane", "-t", paneID, "-p")
	baselineLen := len(strings.Split(baseline, "\n"))

	// Send
	text := strings.ReplaceAll(req.Text, "'", "'\\''")
	runTmux("send-keys", "-t", paneID, "-l", text)
	time.Sleep(enterDelay)
	runTmux("send-keys", "-t", paneID, "Enter")

	// Poll
	start := time.Now()
	for time.Since(start) < time.Duration(req.Timeout)*time.Second {
		time.Sleep(time.Second)
		cur, _ := runTmux("capture-pane", "-t", paneID, "-p")
		lines := strings.Split(cur, "\n")
		if len(lines) > 0 && promptRe.MatchString(strings.TrimRight(lines[len(lines)-1], " ")) {
			newLines := lines[baselineLen:]
			answer := strings.TrimSpace(strings.Join(newLines, "\n"))
			J(w, M{"success": true, "pane_id": shortPaneID(paneID), "question": req.Text, "answer": answer})
			return
		}
	}
	J(w, M{"success": false, "pane_id": shortPaneID(paneID), "question": req.Text, "error": fmt.Sprintf("Timeout after %ds waiting for prompt", req.Timeout)})
}

func handleMouseToggle(w http.ResponseWriter, r *http.Request) {
	action := "on"
	if strings.HasSuffix(r.URL.Path, "/off") {
		action = "off"
	}
	paneID := r.URL.Query().Get("pane_id")
	runTmux("set", "-g", "mouse", action)
	J(w, M{"success": true, "mouse_mode": action, "pane_id": paneID, "message": fmt.Sprintf("Mouse mode turned %s for pane %s", action, paneID)})
}

func handleMouseStatus(w http.ResponseWriter, r *http.Request) {
	out, _ := runTmux("show-options", "-g", "mouse")
	mode := "off"
	if strings.Contains(out, "on") {
		mode = "on"
	}
	J(w, M{"success": true, "mouse_mode": mode})
}

func handleTtydStatus(w http.ResponseWriter, r *http.Request) {
	paneID := normPaneID(strings.TrimPrefix(r.URL.Path, "/api/tmux/ttyd/status/"))
	var port sql.NullInt64
	err := store.QueryRow("SELECT ttyd_port FROM agent_config WHERE pane_id=?", paneID).Scan(&port)
	if err != nil {
		httpErr(w, 404, "pane_id not found")
		return
	}
	// Check if port is listening
	listening := false
	if inst := getInstance(paneID); inst != nil {
		listening = true
	}
	status := "stopped"
	if listening {
		status = "running"
	}
	J(w, M{"pane_id": paneID, "port": port.Int64, "status": status})
}

func handleSplitPane(w http.ResponseWriter, r *http.Request, id string) {
	paneID := normPaneID(id)
	dir := r.URL.Query().Get("direction")
	if dir == "" {
		dir = "v"
	}
	session := strings.Split(paneID, ":")[0]
	out, _ := runTmux("list-panes", "-t", session+":main")
	if len(strings.Split(strings.TrimSpace(out), "\n")) >= 2 {
		J(w, M{"success": false, "error": "Already split"})
		return
	}
	runTmux("split-window", "-t", session+":main", "-"+dir)
	J(w, M{"success": true, "message": "Split " + dir})
}

func handleUnsplitPane(w http.ResponseWriter, r *http.Request, id string) {
	paneID := normPaneID(id)
	session := strings.Split(paneID, ":")[0]
	out, _ := runTmux("list-panes", "-t", session+":main")
	if len(strings.Split(strings.TrimSpace(out), "\n")) <= 1 {
		J(w, M{"success": false, "error": "No split to close"})
		return
	}
	runTmux("kill-pane", "-t", session+":main.1")
	J(w, M{"success": true, "message": "Split closed"})
}

func handleClear(w http.ResponseWriter, r *http.Request) {
	// kill tmux on all active nodes
	rows, _ := store.Query("SELECT DISTINCT node_url FROM agent_config WHERE active=1")
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var u string
			rows.Scan(&u)
			nodeExec(u, "tmux kill-server")
		}
	}
	J(w, M{"success": true, "message": "All sessions cleared"})
}

func handleTmuxList(w http.ResponseWriter, r *http.Request) {
	out, err := runTmux("list-sessions", "-F", "#{session_name}")
	if err != nil {
		J(w, M{"success": true, "output": "没有运行中的 session"})
		return
	}
	sessions := strings.Split(strings.TrimSpace(out), "\n")
	var lines []string
	for i, s := range sessions {
		if s == "" {
			continue
		}
		ls := i == len(sessions)-1
		pre := "├──"
		if ls {
			pre = "└──"
		}
		lines = append(lines, pre+" "+s)
		wo, err := runTmux("list-windows", "-t", s, "-F", "#{window_index} #{window_name}")
		if err != nil {
			continue
		}
		ws := strings.Split(strings.TrimSpace(wo), "\n")
		for j, wl := range ws {
			parts := strings.SplitN(wl, " ", 2)
			if len(parts) < 2 {
				continue
			}
			lw := j == len(ws)-1
			ind := "│   "
			if ls {
				ind = "    "
			}
			wp := "├──"
			if lw {
				wp = "└──"
			}
			lines = append(lines, ind+wp+" "+parts[0]+" "+parts[1])
		}
	}
	J(w, M{"success": true, "output": strings.Join(lines, "\n")})
}
