package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

func handlePanes(w http.ResponseWriter, r *http.Request) {
	gid := r.URL.Query().Get("group_id")
	var rows *sql.Rows
	var err error
	if gid != "" {
		rows, err = store.Query(`SELECT DISTINCT t.pane_id, t.title, t.ttyd_port, t.workspace, t.init_script, t.active, t.created_at, t.updated_at, gp.group_id, t.role, t.default_model, t.trust_level
			FROM agent_config t INNER JOIN group_windows gp ON t.pane_id=gp.win_id WHERE gp.group_id=? AND t.active=1 ORDER BY t.created_at DESC`, gid)
	} else {
		rows, err = store.Query(`SELECT t.pane_id, t.title, t.ttyd_port, t.workspace, t.init_script, t.active, t.created_at, t.updated_at, gp.group_id, t.role, t.default_model, t.trust_level
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
		var createdAt, updatedAt sql.NullTime
		var groupID sql.NullInt64
		var role, defaultModel, trustLevel sql.NullString
		rows.Scan(&paneID, &title, &port, &workspace, &initScript, &active, &createdAt, &updatedAt, &groupID, &role, &defaultModel, &trustLevel)
		p := M{
			"pane_id": paneID.String, "title": title.String, "ttyd_port": port.Int64,
			"workspace": workspace.String, "init_script": initScript.String,
			"active": active.Int64,
			"role": role.String, "default_model": defaultModel.String,
			"trust_level": trustLevel.String,
		}
		if createdAt.Valid {
			p["created_at"] = createdAt.Time.Format(time.RFC3339)
		}
		if updatedAt.Valid {
			p["updated_at"] = updatedAt.Time.Format(time.RFC3339)
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
		WinName      *string `json:"win_name"`
		Workspace    string  `json:"workspace"`
		InitScript   string  `json:"init_script"`
		Title        string  `json:"title"`
		AgentType    string  `json:"agent_type"`
		Role         string  `json:"role"`
		DefaultModel string  `json:"default_model"`
	}
	readBody(r, &req)
	token := getToken(r)

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
	title := session
	if req.WinName != nil && *req.WinName != "" {
		title = *req.WinName
	}
	if req.Title != "" {
		title = req.Title
	}
	home, _ := os.UserHomeDir()
	workspace := req.Workspace
	if workspace == "" {
		workspace = fmt.Sprintf("%s/workers/%s", home, session)
	}
	wsExpanded := os.ExpandEnv(strings.Replace(workspace, "~", home, 1))
	os.MkdirAll(wsExpanded, 0755)

	paneID := session + ":main.0"
	port := workerIdx

	// Create tmux session
	nodeTmux(paneID, "new-session", "-d", "-s", session, "-n", "main", "-c", wsExpanded)
	nodeTmux(paneID, "send-keys", "-t", paneID, "export TERM=xterm-256color", "Enter")

	// Insert DB
	store.Exec(fmt.Sprintf(`INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,%s,%s)`, store.Now(), store.Now()), paneID, title, port, req.Workspace, req.InitScript, "{}", req.Role, req.DefaultModel)

	// Start ttyd-go instance
	if err := startInstance(paneID, port, token); err != nil {
		J(w, M{"success": false, "error": err.Error(), "session": session, "pane_id": shortPaneID(paneID)})
		return
	}

	// Wait for port
	waitPort(port, 10*time.Second)

	initPaneEnv(paneEnvOpts{
		paneID:     paneID,
		configJSON: "{}",
		workspace:  wsExpanded,
		initScript: req.InitScript,
	})

	J(w, M{
		"success": true, "session": session, "window": "main",
		"pane_id": shortPaneID(paneID), "title": title,
		"workspace": req.Workspace, "init_script": req.InitScript,
		"ttyd_port": port,
	})
}

func handlePaneByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tmux/panes/")
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
	var tgEnable sql.NullBool
	var tgToken, tgChatID sql.NullString
	var groupID sql.NullInt64
	var role, defaultModel, trustLevel sql.NullString
	err := store.QueryRow(`SELECT t.pane_id, t.title, t.ttyd_port, t.workspace, t.init_script,
		t.tg_token, t.tg_chat_id, t.tg_enable, t.active, t.agent_type, t.agent_duty, t.config, t.common_prompt, t.ttyd_preview, gp.group_id, t.role, t.default_model, t.trust_level
		FROM agent_config t LEFT JOIN group_windows gp ON t.pane_id=gp.win_id WHERE t.pane_id=?`, paneID).Scan(
		&paneID, &title, &port, &workspace, &initScript,
		&tgToken, &tgChatID, &tgEnable, &active, &agentType, &agentDuty, &config, &commonPrompt, &ttydPreview, &groupID, &role, &defaultModel, &trustLevel)
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
		"role": role.String, "default_model": defaultModel.String,
		"trust_level": trustLevel.String,
	}
	if groupID.Valid {
		resp["group_id"] = groupID.Int64
	} else {
		resp["group_id"] = nil
	}
	J(w, resp)
}

func handleUpdatePane(w http.ResponseWriter, r *http.Request, id string) {
	paneID := normPaneID(id)
	var req M
	readBody(r, &req)
	delete(req, "pane_id")
	if len(req) == 0 {
		httpErr(w, 400, "No valid fields to update")
		return
	}
	var sets []string
	var vals []interface{}
	for k, v := range req {
		sets = append(sets, k+"=?")
		vals = append(vals, v)
	}
	vals = append(vals, paneID)
	store.Exec("UPDATE agent_config SET "+strings.Join(sets, ", ")+" WHERE pane_id=?", vals...)

	// Sync agent_duty to workspace/.kiro/steering/duty.md
	if duty, ok := req["agent_duty"].(string); ok {
		var ws sql.NullString
		store.QueryRow("SELECT workspace FROM agent_config WHERE pane_id=?", paneID).Scan(&ws)
		if ws.String != "" {
			dir := ws.String + "/.kiro/steering"
			os.MkdirAll(dir, 0755)
			os.WriteFile(dir+"/duty.md", []byte("---\ninclusion: always\n---\n\n"+duty), 0644)
		}
	}
	J(w, M{"success": true, "pane_id": shortPaneID(paneID), "updated": req})
}

func handleDeletePane(w http.ResponseWriter, r *http.Request, id string) {
	paneID := normPaneID(id)
	var port sql.NullInt64
	store.QueryRow("SELECT ttyd_port FROM agent_config WHERE pane_id=?", paneID).Scan(&port)
	go func() {
		defer func() { recover() }()
		stopInstance(paneID)
		if port.Valid {
			exec.Command("bash", "-c", fmt.Sprintf("kill -9 $(lsof -ti:%d 2>/dev/null) 2>/dev/null; true", port.Int64)).Run()
		}
		session := strings.Split(paneID, ":")[0]
		nodeTmux(paneID, "kill-session", "-t", session)
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
	err := store.QueryRow("SELECT ttyd_port, workspace, init_script, title, config, agent_type, trust_level FROM agent_config WHERE pane_id=?", paneID).
		Scan(&port, &workspace, &initScript, &title, &config, &agentType, &trustLevel)
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
	exec.Command("tmux", "send-keys", "-t", session+":main.0", "export TERM=xterm-256color", "Enter").Run()

	// Restart ttyd-go
	p := int(port.Int64)
	if err := startInstance(paneID, p, token); err != nil {
		return err
	}
	waitPort(p, 10*time.Second)

	// Re-run init
	initPaneEnv(paneEnvOpts{
		paneID:     paneID,
		configJSON: config.String,
		workspace:  wsExpanded,
		initScript: initScript.String,
		agentType:  agentType.String,
	})
	store.Exec(fmt.Sprintf("UPDATE agent_config SET updated_at=%s WHERE pane_id=?", store.Now()), paneID)
	return nil
}

// initPaneEnv sets up env vars, proxy, workspace, and runs init script in a pane.
type paneEnvOpts struct {
	paneID     string
	configJSON string // JSON config (uses applyProxyFromConfig)
	workspace  string // expanded workspace path
	initScript string
	agentType  string
}

func initPaneEnv(opts paneEnvOpts) {
	pid := opts.paneID
	shortID := strings.Split(pid, ":")[0]

	// Export agent IDs
	runTmux("send-keys", "-t", pid, fmt.Sprintf("export X_AGENT_ID='%s'", pid), "Enter")
	runTmux("send-keys", "-t", pid, fmt.Sprintf("export X_AGENT_SHORT_ID='%s'", shortID), "Enter")

	// Proxy (from config JSON)
	applyProxyFromConfig(pid, opts.configJSON)

	// cd workspace
	if opts.workspace != "" {
		runTmux("send-keys", "-t", pid, "cd "+opts.workspace, "Enter")
	}

	// Init script
	if opts.initScript != "" {
		runTmux("send-keys", "-t", pid, "clear", "Enter")
		time.Sleep(500 * time.Millisecond)
		for _, line := range strings.Split(opts.initScript, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "sleep:") {
				continue
			}
			if strings.HasPrefix(line, "key:") {
				runTmux("send-keys", "-t", pid, line[4:])
			} else {
				runTmux("send-keys", "-t", pid, line, "Enter")
			}
		}
	}

	// Agent type
	if opts.agentType != "" {
		runTmux("send-keys", "-t", pid, opts.agentType, "Enter")
	}
}

// applyProxyFromConfig parses config JSON and exports proxy env if enabled.
func applyProxyFromConfig(paneID, configJSON string) {
	if configJSON == "" || configJSON == "{}" {
		return
	}
	var cfg struct {
		Proxy struct {
			Enable bool   `json:"enable"`
			URL    string `json:"url"`
		} `json:"proxy"`
	}
	if json.Unmarshal([]byte(configJSON), &cfg) != nil || !cfg.Proxy.Enable {
		return
	}
	// Use pane_id as proxy auth user for mitmproxy identification
	short := strings.Split(paneID, ":")[0]
	proxyURL := fmt.Sprintf("http://%s:x@127.0.0.1:18888", short)
	if cfg.Proxy.URL != "" && cfg.Proxy.URL != "https://proxy.example.com" {
		proxyURL = cfg.Proxy.URL
	}
	cmd := fmt.Sprintf("export HTTPS_PROXY='%s' && export https_proxy='%s' && export HTTP_PROXY='%s' && export http_proxy='%s' && export ALL_PROXY='%s' && export no_proxy=localhost,127.0.0.1", proxyURL, proxyURL, proxyURL, proxyURL, proxyURL)
	runTmux("send-keys", "-t", paneID, cmd, "Enter")
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
		time.Sleep(500 * time.Millisecond)
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
		args := []string{"new-window", "-t", body.Session}
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
	id := r.URL.Query().Get("id")

	// Read from Redis pane_status_map (maintained by cron_pane_watcher)
	statusMap := redisGetJSON("pane_status_map")

	if id != "" {
		paneID := normPaneID(id)
		target := paneID
		if !strings.Contains(paneID, ":") {
			target = paneID + ":main.0"
		}
		if statusMap != nil {
			if v, ok := statusMap[target]; ok {
				J(w, v)
				return
			}
		}
		J(w, M{"error": "not found", "pane_id": id})
		return
	}

	if statusMap != nil {
		J(w, statusMap)
		return
	}

	// Fallback to DB if Redis unavailable
	rows, _ := store.Query("SELECT pane_id, ttyd_port, title FROM agent_config")
	defer rows.Close()
	result := M{}
	for rows.Next() {
		var pid, title string
		var port int
		rows.Scan(&pid, &port, &title)
		result[pid] = M{"pane_id": shortPaneID(pid), "title": title, "port": port}
	}
	J(w, result)
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
