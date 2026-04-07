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
	"sync"
	"time"
	"unicode/utf8"
)

// enterDelay is the pause between sending text and pressing Enter.
// Heavy TUIs (Copilot, OpenCode) need time to render text in the input buffer.
// TODO: make this per-agent-type once agent detection is reliable.
const enterDelay = 600 * time.Millisecond
const chunkedPromptRuneSize = 80
const chunkedPromptChunkDelay = 30 * time.Millisecond
const chunkedPromptEnterDelay = 1 * time.Second
const promptConfirmPollInterval = 150 * time.Millisecond
const codexPromptConfirmPollInterval = 60 * time.Millisecond
const codexPromptConfirmStabilizeDelay = 40 * time.Millisecond
const promptConfirmTimeout = 5 * time.Second
const promptConfirmCaptureStart = "-160"
const agentReadyPollInterval = 500 * time.Millisecond
const codexAgentReadyPollInterval = 120 * time.Millisecond
const agentReadyTimeout = 90 * time.Second
const submitConfirmPollInterval = 200 * time.Millisecond
const codexSubmitConfirmPollInterval = 80 * time.Millisecond
const submitConfirmTimeout = 4 * time.Second
const submitEnterRetryLimit = 2
const startupPromptBatchWindow = 5 * time.Second
const startupPromptCooldown = 15 * time.Second
const directPromptRuneThreshold = 600

type startupPromptTask struct {
	paneID    string
	agentType string
	seq       int64
}

type startupPromptQueue struct {
	mu        sync.Mutex
	cond      *sync.Cond
	once      sync.Once
	pending   map[string]startupPromptTask
	seqByPane map[string]int64
}

type tmuxSendTrace struct {
	ID        string
	PaneID    string
	AgentType string
}

var replyInChineseStartupQueue = func() *startupPromptQueue {
	q := &startupPromptQueue{
		pending:   map[string]startupPromptTask{},
		seqByPane: map[string]int64{},
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}()

var tmuxSendTraceMu sync.Mutex

func newTmuxSendTrace(paneID, agentType string) *tmuxSendTrace {
	return &tmuxSendTrace{
		ID:        fmt.Sprintf("%d-%s", time.Now().UTC().UnixNano(), strings.ReplaceAll(shortPaneID(paneID), ":", "_")),
		PaneID:    normPaneID(paneID),
		AgentType: normalizeAgentType(agentType),
	}
}

func tmuxSendTraceLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "cicy-tmux-send.log")
	}
	return filepath.Join(home, ".cicy", "tmux-send.log")
}

func (t *tmuxSendTrace) logStep(step string, meta map[string]any, body string) {
	if t == nil {
		return
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["trace_id"] = t.ID
	meta["pane_id"] = shortPaneID(t.PaneID)
	meta["agent_type"] = t.AgentType
	meta["step"] = step
	meta["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	metaJSON, _ := json.Marshal(meta)

	var b strings.Builder
	b.Write(metaJSON)
	b.WriteByte('\n')
	if body != "" {
		b.WriteString("<<<\n")
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString(">>>\n")
	}

	path := tmuxSendTraceLogPath()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	tmuxSendTraceMu.Lock()
	defer tmuxSendTraceMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("[tmux-send-trace] id=%s step=%s log-open-error=%v", t.ID, step, err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		log.Printf("[tmux-send-trace] id=%s step=%s log-write-error=%v", t.ID, step, err)
		return
	}
	log.Printf("[tmux-send-trace] id=%s pane=%s step=%s", t.ID, shortPaneID(t.PaneID), step)
}

func paneStartupPromptOrder(paneID string) int {
	shortID := shortPaneID(paneID)
	var n int
	if _, err := fmt.Sscanf(shortID, "w-%d", &n); err == nil {
		return n
	}
	return 1 << 30
}

func startupPromptLess(a, b startupPromptTask) bool {
	ao := paneStartupPromptOrder(a.paneID)
	bo := paneStartupPromptOrder(b.paneID)
	if ao != bo {
		return ao < bo
	}
	return a.paneID < b.paneID
}

func (q *startupPromptQueue) start() {
	q.once.Do(func() {
		go q.run()
	})
}

func (q *startupPromptQueue) enqueue(paneID, agentType string) {
	q.start()
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seqByPane[paneID]++
	q.pending[paneID] = startupPromptTask{
		paneID:    paneID,
		agentType: agentType,
		seq:       q.seqByPane[paneID],
	}
	q.cond.Signal()
}

func (q *startupPromptQueue) isCurrent(task startupPromptTask) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.seqByPane[task.paneID] == task.seq
}

func (q *startupPromptQueue) waitForPending() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.pending) == 0 {
		q.cond.Wait()
	}
}

func (q *startupPromptQueue) popNext() (startupPromptTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return startupPromptTask{}, false
	}
	var chosen startupPromptTask
	first := true
	for _, task := range q.pending {
		if first || startupPromptLess(task, chosen) {
			chosen = task
			first = false
		}
	}
	delete(q.pending, chosen.paneID)
	return chosen, true
}

func (q *startupPromptQueue) run() {
	for {
		q.waitForPending()
		log.Printf("[startup-prompt] batch window %s collecting pending panes", startupPromptBatchWindow)
		time.Sleep(startupPromptBatchWindow)
		for {
			task, ok := q.popNext()
			if !ok {
				break
			}
			if q.process(task) {
				log.Printf("[startup-prompt] %s cooldown %s before next pane", task.paneID, startupPromptCooldown)
				time.Sleep(startupPromptCooldown)
			}
		}
	}
}

func (q *startupPromptQueue) process(task startupPromptTask) bool {
	for i := 0; i < 180; i++ {
		if !q.isCurrent(task) {
			log.Printf("[startup-prompt] %s superseded before ready", task.paneID)
			return false
		}
		time.Sleep(500 * time.Millisecond)
		out, err := runTmux("capture-pane", "-t", task.paneID, "-p")
		if err != nil {
			continue
		}
		if !isAgentInputReady(task.agentType, out) {
			continue
		}
		if !q.isCurrent(task) {
			log.Printf("[startup-prompt] %s superseded before send", task.paneID)
			return false
		}
		time.Sleep(800 * time.Millisecond)
		if !q.isCurrent(task) {
			log.Printf("[startup-prompt] %s superseded during final wait", task.paneID)
			return false
		}
		log.Printf("[startup-prompt] %s send reply_in_chinese", task.paneID)
		sendPaneText(task.paneID, "reply in chinese")
		return true
	}
	if q.isCurrent(task) {
		log.Printf("[startup-prompt] %s timeout waiting for %s", task.paneID, normalizeAgentType(task.agentType))
	}
	return false
}

func handlePanes(w http.ResponseWriter, r *http.Request) {
	gid := r.URL.Query().Get("group_id")
	var rows *sql.Rows
	var err error
	if gid != "" {
		rows, err = store.Query(`SELECT DISTINCT t.pane_id, t.title, t.ttyd_port, t.workspace, t.init_script, t.active, t.created_at, t.updated_at, gp.group_id, t.role, t.default_model, t.trust_level, t.agent_type, COALESCE(t.allow_all_actions, 0), COALESCE(t.reply_in_chinese, 0)
			FROM agent_config t INNER JOIN group_windows gp ON t.pane_id=gp.win_id WHERE gp.group_id=? AND t.active=1 ORDER BY t.created_at DESC`, gid)
	} else {
		rows, err = store.Query(`SELECT t.pane_id, t.title, t.ttyd_port, t.workspace, t.init_script, t.active, t.created_at, t.updated_at, gp.group_id, t.role, t.default_model, t.trust_level, t.agent_type, COALESCE(t.allow_all_actions, 0), COALESCE(t.reply_in_chinese, 0)
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
		var replyInChinese sql.NullBool
		rows.Scan(&paneID, &title, &port, &workspace, &initScript, &active, &createdAt, &updatedAt, &groupID, &role, &defaultModel, &trustLevel, &agentType, &allowAllActions, &replyInChinese)
		p := M{
			"pane_id": paneID.String, "title": title.String, "ttyd_port": port.Int64,
			"workspace": workspace.String, "init_script": initScript.String,
			"active": active.Int64,
			"role":   role.String, "default_model": defaultModel.String,
			"trust_level": trustLevel.String, "agent_type": agentType.String,
			"allow_all_actions": allowAllActions.Bool,
			"reply_in_chinese":  replyInChinese.Bool,
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
		ReplyInChinese  bool    `json:"reply_in_chinese"`
	}
	req.AllowAllActions = true
	req.ReplyInChinese = true
	readBody(r, &req)
	token := getToken(r)

	result, err := doCreatePane(req.Title, req.Role, req.DefaultModel, req.AgentType, req.InitScript, req.AllowAllActions, req.ReplyInChinese, req.WinName, token)
	if err != nil {
		J(w, M{"success": false, "error": err.Error()})
		return
	}
	J(w, result)
}

func doCreatePane(title, role, defaultModel, agentType, initScript string, allowAllActions bool, replyInChinese bool, winName *string, token string) (M, error) {
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
	store.Exec(fmt.Sprintf(`INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,%s,%s)`, store.Now(), store.Now()), paneID, t, port, workspace, initScript, "{}", role, defaultModel, agentType, allowAllActions, replyInChinese)

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
		replyInChinese:  replyInChinese,
	})

	return M{
		"success": true, "session": session, "window": "main",
		"pane_id": shortPaneID(paneID), "title": t,
		"workspace": workspace, "init_script": initScript,
		"ttyd_port":        port,
		"reply_in_chinese": replyInChinese,
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
	var replyInChinese sql.NullBool
	var tgEnable sql.NullBool
	var tgToken, tgChatID sql.NullString
	var groupID sql.NullInt64
	var role, defaultModel, trustLevel sql.NullString
	var machineID sql.NullInt64
	var machineLabel, machineURL, runtimeKind, capabilitiesJSON sql.NullString
	err := store.QueryRow(`SELECT t.pane_id, t.title, t.ttyd_port, t.workspace, t.init_script,
		t.tg_token, t.tg_chat_id, t.tg_enable, t.active, t.agent_type, t.agent_duty, t.config, t.common_prompt, t.ttyd_preview, gp.group_id, t.role, t.default_model, t.trust_level,
		COALESCE(t.allow_all_actions, 0),
		COALESCE(t.reply_in_chinese, 0),
		COALESCE(t.machine_id, 0), COALESCE(m.label, ''), COALESCE(m.url, ''), COALESCE(json_extract(m.capabilities_json, '$.runtime_kind'), ''), COALESCE(m.capabilities_json, '{}')
		FROM agent_config t
		LEFT JOIN group_windows gp ON t.pane_id=gp.win_id
		LEFT JOIN machines m ON t.machine_id=m.id
		WHERE t.pane_id=?`, paneID).Scan(
		&paneID, &title, &port, &workspace, &initScript,
		&tgToken, &tgChatID, &tgEnable, &active, &agentType, &agentDuty, &config, &commonPrompt, &ttydPreview, &groupID, &role, &defaultModel, &trustLevel, &allowAllActions,
		&replyInChinese,
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
		"reply_in_chinese":  replyInChinese.Bool,
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
	"reply_in_chinese":  true,
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
	// if duty, ok := filtered["agent_duty"].(string); ok {
	// 	var ws sql.NullString
	// 	store.QueryRow("SELECT workspace FROM agent_config WHERE pane_id=?", paneID).Scan(&ws)
	// 	if ws.String != "" {
	// 		dir := ws.String + "/.kiro/steering"
	// 		os.MkdirAll(dir, 0755)
	// 		os.WriteFile(dir+"/duty.md", []byte("---\ninclusion: always\n---\n\n"+duty), 0644)
	// 	}
	// }
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
	var replyInChinese sql.NullBool
	err := store.QueryRow("SELECT ttyd_port, workspace, init_script, title, config, agent_type, trust_level, COALESCE(allow_all_actions, 0), COALESCE(reply_in_chinese, 0) FROM agent_config WHERE pane_id=?", paneID).
		Scan(&port, &workspace, &initScript, &title, &config, &agentType, &trustLevel, &allowAllActions, &replyInChinese)
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
		replyInChinese:  replyInChinese.Bool,
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
	replyInChinese  bool
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

func normalizedOpenClawPrimaryModel() string {
	model := strings.ToLower(strings.TrimSpace(os.Getenv("CICY_OPENCLAW_MODEL")))
	switch model {
	case "":
		return "claude-sonnet-4-6"
	case "gpt5.4":
		return "gpt-5.4"
	case "cicyai/claude-opus-4-6":
		return "claude-opus-4-6"
	case "cicyai/claude-sonnet-4-6":
		return "claude-sonnet-4-6"
	case "cicyai/claude-haiku-4-5-20251001":
		return "claude-haiku-4-5-20251001"
	case "shibacc/claude-opus-4-6":
		return "claude-opus-4-6"
	case "shibacc/claude-sonnet-4-6":
		return "claude-sonnet-4-6"
	case "shibacc/claude-haiku-4-5-20251001":
		return "claude-haiku-4-5-20251001"
	}
	model = strings.TrimPrefix(model, "cicyai/")
	return strings.TrimPrefix(model, "shibacc/")
}

func runtimeAPIBasePort() string {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8021"
	}
	return port
}

func localAIGatewayBaseURL(provider string, agentID string) string {
	return "http://127.0.0.1:" + runtimeAPIBasePort() + "/api/ai-gateway/" + provider + "/" + agentID
}

func openAIRuntimeBaseURL(agentID string) string {
	return localAIGatewayBaseURL("openai", agentID)
}

func anthropicRuntimeBaseURL(agentID string) string {
	return localAIGatewayBaseURL("anthropic", agentID)
}

func openClawRuntimeBaseURL(agentID string) string {
	if strings.HasPrefix(normalizedOpenClawPrimaryModel(), "claude-") {
		return anthropicRuntimeBaseURL(agentID)
	}
	return openAIRuntimeBaseURL(agentID)
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

func ensureAgentCommandLine(commandName, label, installCmd, logPath string) string {
	if strings.TrimSpace(installCmd) == "" {
		return fmt.Sprintf(`if ! command -v %s >/dev/null 2>&1; then
  echo '[cicy] =================================================='
  echo '[cicy] %s is missing from the preinstalled runtime image.'
  echo '[cicy] Rebuild or update the base image, then restart this pane.'
  echo '[cicy] =================================================='
  return 1
fi`, commandName, label)
	}
	return visibleAgentInstallLine(commandName, label, installCmd, logPath)
}

func agentBootLines(agentType string, allowAllActions bool, shortID string) []string {
	switch normalizeAgentType(agentType) {
	case "openclaw":
		home, _ := os.UserHomeDir()
		baseStateDir := filepath.Join(home, ".openclaw")
		stateDir := filepath.Join(home, ".openclaw-"+shortID)
		baseConfigPath := filepath.Join(baseStateDir, "openclaw.json")
		stateConfigPath := filepath.Join(stateDir, "openclaw.json")
		installLog := filepath.Join(stateDir, "openclaw-install.log")
		sessionName := strings.ReplaceAll(shortID, "-", "")
		lines := []string{
			fmt.Sprintf("export OPENCLAW_BASE_CONFIG_PATH=%s", tmuxShellQuote(baseConfigPath)),
			fmt.Sprintf("export OPENCLAW_CONFIG_PATH=%s", tmuxShellQuote(stateConfigPath)),
			fmt.Sprintf("export OPENCLAW_STATE_DIR=%s", tmuxShellQuote(stateDir)),
			fmt.Sprintf("export OPENCLAW_SESSION_KEY=%s", tmuxShellQuote("agent:main:"+sessionName)),
			fmt.Sprintf("export OPENCLAW_SESSION_STORE=%s", tmuxShellQuote(filepath.Join(baseStateDir, "agents", "main", "sessions", "sessions.json"))),
			fmt.Sprintf("export OPENAI_API_KEY=%s", tmuxShellQuote(os.Getenv("CICY_API_KEY"))),
			fmt.Sprintf("export OPENAI_BASE_URL=%s", tmuxShellQuote(openAIRuntimeBaseURL(shortID))),
			fmt.Sprintf("export ANTHROPIC_BASE_URL=%s", tmuxShellQuote(anthropicRuntimeBaseURL(shortID))),
			fmt.Sprintf("mkdir -p %s", tmuxShellQuote(stateDir)),
			fmt.Sprintf("mkdir -p %s %s %s", tmuxShellQuote(filepath.Join(stateDir, "identity")), tmuxShellQuote(filepath.Join(stateDir, "devices")), tmuxShellQuote(filepath.Join(stateDir, "agents", "main", "agent"))),
			fmt.Sprintf("if [ -d %s ]; then cp -a %s/. %s/; fi", tmuxShellQuote(filepath.Join(baseStateDir, "identity")), tmuxShellQuote(filepath.Join(baseStateDir, "identity")), tmuxShellQuote(filepath.Join(stateDir, "identity"))),
			fmt.Sprintf("if [ -d %s ]; then cp -a %s/. %s/; fi", tmuxShellQuote(filepath.Join(baseStateDir, "devices")), tmuxShellQuote(filepath.Join(baseStateDir, "devices")), tmuxShellQuote(filepath.Join(stateDir, "devices"))),
			`node - <<'EOF'
const fs = require("fs");
const src = process.env.OPENCLAW_BASE_CONFIG_PATH;
const dst = process.env.OPENCLAW_CONFIG_PATH;
if (!src || !dst) process.exit(0);
const cfg = JSON.parse(fs.readFileSync(src, "utf8"));
const rawModel = String(process.env.CICY_OPENCLAW_MODEL || "").trim().toLowerCase();
let model = rawModel || "claude-sonnet-4-6";
switch (model) {
  case "gpt5.4":
    model = "gpt-5.4";
    break;
  case "cicyai/claude-opus-4-6":
    model = "claude-opus-4-6";
    break;
  case "cicyai/claude-sonnet-4-6":
    model = "claude-sonnet-4-6";
    break;
  case "cicyai/claude-haiku-4-5-20251001":
    model = "claude-haiku-4-5-20251001";
    break;
  case "shibacc/claude-opus-4-6":
    model = "claude-opus-4-6";
    break;
  case "shibacc/claude-sonnet-4-6":
    model = "claude-sonnet-4-6";
    break;
  case "shibacc/claude-haiku-4-5-20251001":
    model = "claude-haiku-4-5-20251001";
    break;
  default:
    model = model.replace(/^cicyai\//, "");
    model = model.replace(/^shibacc\//, "");
    break;
}
const providerApi = model.startsWith("claude-") ? "anthropic-messages" : "openai-completions";
const baseUrl = providerApi === "anthropic-messages" ? process.env.ANTHROPIC_BASE_URL : process.env.OPENAI_BASE_URL;
cfg.models ||= {};
cfg.models.providers ||= {};
cfg.models.providers.cicy ||= {};
cfg.models.providers.cicy.baseUrl = baseUrl;
cfg.models.providers.cicy.api = providerApi;
if (Array.isArray(cfg.models.providers.cicy.models)) {
  cfg.models.providers.cicy.models = cfg.models.providers.cicy.models.map((entry) => ({ ...entry, api: providerApi }));
}
fs.writeFileSync(dst, JSON.stringify(cfg, null, 2));
EOF`,
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
		lines = append(lines, ensureAgentCommandLine("openclaw", "OpenClaw", openClawInstallCmd(), installLog))
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
		baseURL := openAIRuntimeBaseURL(shortID)
		baseURLOverride := tmuxShellQuote(`model_providers.custom.base_url="` + baseURL + `"`)
		lines := []string{
			"mkdir -p ~/.cicy",
			ensureAgentCommandLine("codex", "Codex", codexInstallCmd(), installLog),
		}
		if allowAllActions {
			lines = append(lines, fmt.Sprintf("codex -c %s --dangerously-bypass-approvals-and-sandbox", baseURLOverride))
			return lines
		}
		lines = append(lines, fmt.Sprintf("codex -c %s", baseURLOverride))
		return lines
	case "claude":
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("claude-install-%s.log", shortID))
		settingsPath := fmt.Sprintf("/tmp/claude-settings-%s.json", shortID)
		lines := []string{
			"mkdir -p ~/.cicy",
			ensureAgentCommandLine("claude", "Claude Code", claudeInstallCmd(), installLog),
			fmt.Sprintf("export ANTHROPIC_BASE_URL=%s", tmuxShellQuote(anthropicRuntimeBaseURL(shortID))),
			fmt.Sprintf("export CLAUDE_SETTINGS_PATH=%s", tmuxShellQuote(settingsPath)),
			fmt.Sprintf("export CICY_DEFAULT_CLAUDE_MODEL=%s", tmuxShellQuote(strings.TrimSpace(os.Getenv("CICY_DEFAULT_CLAUDE_MODEL")))),
			`node - <<'EOF'
const fs = require("fs");
const path = process.env.CLAUDE_SETTINGS_PATH;
if (!path) process.exit(0);
const config = {
  env: {
    ANTHROPIC_AUTH_TOKEN: process.env.CICY_API_KEY || "",
    ANTHROPIC_BASE_URL: process.env.ANTHROPIC_BASE_URL || "",
  },
};
const model = String(process.env.CICY_DEFAULT_CLAUDE_MODEL || "").trim();
if (model) {
  config.model = model;
}
fs.writeFileSync(path, JSON.stringify(config, null, 2));
EOF`,
		}
		if allowAllActions {
			lines = append(lines, `resolve_claude_bypass_user() {
  if [ "$(id -u)" -ne 0 ]; then
    export CLAUDE_BYPASS_USER="$(id -un)"
    export CLAUDE_BYPASS_HOME="${HOME:-$(getent passwd "$(id -u)" | cut -d: -f6)}"
    return 0
  fi
  for candidate in cicy node; do
    if id -u "$candidate" >/dev/null 2>&1; then
      export CLAUDE_BYPASS_USER="$candidate"
      export CLAUDE_BYPASS_HOME="$(getent passwd "$candidate" | cut -d: -f6)"
      chmod 711 /root 2>/dev/null || true
      mkdir -p "$CLAUDE_BYPASS_HOME/.claude" "$CLAUDE_BYPASS_HOME/.config" "$CLAUDE_BYPASS_HOME/.cache"
      chown -R "$candidate:$candidate" "$WORKSPACE" "$CLAUDE_BYPASS_HOME/.claude" "$CLAUDE_BYPASS_HOME/.config" "$CLAUDE_BYPASS_HOME/.cache"
      return 0
    fi
  done
  export CLAUDE_BYPASS_USER=root
  export CLAUDE_BYPASS_HOME=/root
}`)
			lines = append(lines,
				"resolve_claude_bypass_user || return 1",
				`if [ "$CLAUDE_BYPASS_USER" = "$(id -un)" ]; then
  env HOME="$CLAUDE_BYPASS_HOME" USER="$CLAUDE_BYPASS_USER" LOGNAME="$CLAUDE_BYPASS_USER" SHELL=/bin/bash PATH="$PATH" TERM="$TERM" WORKSPACE="$WORKSPACE" CLAUDE_SETTINGS_PATH="$CLAUDE_SETTINGS_PATH" ANTHROPIC_BASE_URL="$ANTHROPIC_BASE_URL" CICY_API_KEY="$CICY_API_KEY" sh -lc 'cd "$WORKSPACE" && exec claude --settings "$CLAUDE_SETTINGS_PATH" --dangerously-skip-permissions'
else
  runuser -u "$CLAUDE_BYPASS_USER" -- env HOME="$CLAUDE_BYPASS_HOME" USER="$CLAUDE_BYPASS_USER" LOGNAME="$CLAUDE_BYPASS_USER" SHELL=/bin/bash PATH="$PATH" TERM="$TERM" WORKSPACE="$WORKSPACE" CLAUDE_SETTINGS_PATH="$CLAUDE_SETTINGS_PATH" ANTHROPIC_BASE_URL="$ANTHROPIC_BASE_URL" CICY_API_KEY="$CICY_API_KEY" sh -lc 'cd "$WORKSPACE" && exec claude --settings "$CLAUDE_SETTINGS_PATH" --dangerously-skip-permissions'
fi`,
			)
			return lines
		}
		lines = append(lines, "claude --settings \"$CLAUDE_SETTINGS_PATH\"")
		return lines
	case "opencode":
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("opencode-install-%s.log", shortID))
		baseConfigPath := filepath.Join(home, ".config", "opencode", "opencode.json")
		configPath := fmt.Sprintf("/tmp/opencode-%s.json", shortID)
		markerPath := lazyAgentMarkerPath("opencode", shortID)
		lines := []string{
			"mkdir -p ~/.cicy",
			ensureAgentCommandLine("opencode", "OpenCode", opencodeInstallCmd(), installLog),
			fmt.Sprintf("export OPENAI_BASE_URL=%s", tmuxShellQuote(openAIRuntimeBaseURL(shortID))),
			fmt.Sprintf("export OPENCODE_BASE_CONFIG=%s", tmuxShellQuote(baseConfigPath)),
			fmt.Sprintf("export OPENCODE_CONFIG=%s", tmuxShellQuote(configPath)),
			fmt.Sprintf("export CICY_OPENCODE_MARKER=%s", tmuxShellQuote(markerPath)),
			`rm -f "$CICY_OPENCODE_MARKER"`,
		}
		if allowAllActions {
			lines = append(lines, `node - <<'EOF'
const fs = require("fs");
const src = process.env.OPENCODE_BASE_CONFIG;
const dst = process.env.OPENCODE_CONFIG;
if (!src || !dst) process.exit(0);
const cfg = JSON.parse(fs.readFileSync(src, "utf8"));
cfg.provider ||= {};
const provider = cfg.provider.cicyai || cfg.provider.shibacc || {};
provider.npm ||= "@ai-sdk/openai-compatible";
provider.options ||= {};
provider.options.baseURL = process.env.OPENAI_BASE_URL || provider.options.baseURL;
cfg.provider.cicyai = provider;
delete cfg.provider.shibacc;
	if (typeof cfg.model === "string") {
	  cfg.model = cfg.model.replace(/^shibacc\//, "cicyai/");
	  if (!cfg.model.includes("/")) cfg.model = "cicyai/" + cfg.model;
	}
	if (typeof cfg.small_model === "string") {
	  cfg.small_model = cfg.small_model.replace(/^shibacc\//, "cicyai/");
	  if (!cfg.small_model.includes("/")) cfg.small_model = "cicyai/" + cfg.small_model;
	}
cfg.permission = "allow";
fs.writeFileSync(dst, JSON.stringify(cfg, null, 2));
EOF`)
			lines = append(lines, `cicy_start_opencode() {
  if [ -f "$CICY_OPENCODE_MARKER" ]; then
    echo '[cicy] OpenCode is already starting or running.'
    return 0
  fi
  : > "$CICY_OPENCODE_MARKER"
  opencode
  status=$?
  rm -f "$CICY_OPENCODE_MARKER"
  echo '[cicy] OpenCode exited. Run cicy_start_opencode to relaunch.'
  return "$status"
}`)
			lines = append(lines,
				`echo '[cicy] OpenCode is installed but not auto-started to save resources.'`,
				`echo '[cicy] Send a message from the UI or run cicy_start_opencode to launch it.'`,
			)
			return lines
		}
		lines = append(lines, `node - <<'EOF'
const fs = require("fs");
const src = process.env.OPENCODE_BASE_CONFIG;
const dst = process.env.OPENCODE_CONFIG;
if (!src || !dst) process.exit(0);
const cfg = JSON.parse(fs.readFileSync(src, "utf8"));
cfg.provider ||= {};
const provider = cfg.provider.cicyai || cfg.provider.shibacc || {};
provider.npm ||= "@ai-sdk/openai-compatible";
provider.options ||= {};
provider.options.baseURL = process.env.OPENAI_BASE_URL || provider.options.baseURL;
cfg.provider.cicyai = provider;
delete cfg.provider.shibacc;
	if (typeof cfg.model === "string") {
	  cfg.model = cfg.model.replace(/^shibacc\//, "cicyai/");
	  if (!cfg.model.includes("/")) cfg.model = "cicyai/" + cfg.model;
	}
	if (typeof cfg.small_model === "string") {
	  cfg.small_model = cfg.small_model.replace(/^shibacc\//, "cicyai/");
	  if (!cfg.small_model.includes("/")) cfg.small_model = "cicyai/" + cfg.small_model;
	}
fs.writeFileSync(dst, JSON.stringify(cfg, null, 2));
EOF`)
		lines = append(lines, `cicy_start_opencode() {
  if [ -f "$CICY_OPENCODE_MARKER" ]; then
    echo '[cicy] OpenCode is already starting or running.'
    return 0
  fi
  : > "$CICY_OPENCODE_MARKER"
  opencode
  status=$?
  rm -f "$CICY_OPENCODE_MARKER"
  echo '[cicy] OpenCode exited. Run cicy_start_opencode to relaunch.'
  return "$status"
}`)
		lines = append(lines,
			`echo '[cicy] OpenCode is installed but not auto-started to save resources.'`,
			`echo '[cicy] Send a message from the UI or run cicy_start_opencode to launch it.'`,
		)
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

func isClaudeThemePrompt(out string) bool {
	return strings.Contains(out, "Choose the text style that looks best with your terminal") &&
		strings.Contains(out, "Dark mode")
}

func isClaudeSecurityNotesPrompt(out string) bool {
	return strings.Contains(out, "Security notes:") &&
		strings.Contains(out, "Press Enter to continue")
}

func isClaudeTrustPrompt(out string) bool {
	return strings.Contains(out, "Quick safety check") &&
		strings.Contains(out, "Yes, I trust this folder") &&
		strings.Contains(out, "Enter to confirm")
}

func isClaudeBypassChoicePrompt(out string) bool {
	return strings.Contains(out, "Bypass Permissions mode") &&
		strings.Contains(out, "Yes, I accept")
}

func isClaudeBypassConfirmPrompt(out string) bool {
	return strings.Contains(out, "Bypass Permissions mode") &&
		strings.Contains(out, "Enter to confirm")
}

type claudePromptStage string

const (
	claudeStageNone          claudePromptStage = ""
	claudeStageTheme         claudePromptStage = "theme"
	claudeStageSecurityNotes claudePromptStage = "security_notes"
	claudeStageTrust         claudePromptStage = "trust"
	claudeStageBypassChoice  claudePromptStage = "bypass_choice"
	claudeStageBypassConfirm claudePromptStage = "bypass_confirm"
)

func detectClaudePromptStage(out string, allowAllActions bool) claudePromptStage {
	switch {
	case isClaudeThemePrompt(out):
		return claudeStageTheme
	case isClaudeSecurityNotesPrompt(out):
		return claudeStageSecurityNotes
	case isClaudeTrustPrompt(out):
		return claudeStageTrust
	case allowAllActions && isClaudeBypassChoicePrompt(out):
		return claudeStageBypassChoice
	case allowAllActions && isClaudeBypassConfirmPrompt(out):
		return claudeStageBypassConfirm
	default:
		return claudeStageNone
	}
}

func isCodexTrustPrompt(out string) bool {
	return strings.Contains(out, "Do you trust the contents of this directory?") &&
		strings.Contains(out, "1. Yes, continue") &&
		strings.Contains(out, "Press enter to continue")
}

func isCodexInputReady(out string) bool {
	// Newer Codex UI often no longer keeps the initial "OpenAI Codex (v...)"
	// header in the visible capture. Treat the active prompt + status footer as ready.
	if strings.Contains(out, "› ") &&
		strings.Contains(out, "· ") &&
		strings.Contains(out, "% left") &&
		(strings.Contains(out, "~/") || strings.Contains(out, "/workers/")) {
		return true
	}
	if strings.Contains(out, "OpenAI Codex (v") &&
		(strings.Contains(out, "directory:") ||
			strings.Contains(out, "~/workers/") ||
			strings.Contains(out, "model:")) &&
		(strings.Contains(out, "/model to change") ||
			strings.Contains(out, "Use /skills to list available skills") ||
			strings.Contains(out, "100% left") ||
			strings.Contains(out, "› ")) {
		return true
	}
	if isCodexTrustPrompt(out) {
		return false
	}
	return false
}

func isOpenCodeInputReady(out string) bool {
	return strings.Contains(out, "OpenCode ") &&
		(strings.Contains(out, "ctrl+p commands") ||
			strings.Contains(out, "Ask anything") ||
			strings.Contains(out, "Build  "))
}

func isOpenClawInputReady(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "openclaw tui") &&
		(strings.Contains(lower, "session agent:main:") ||
			strings.Contains(lower, "agent main | session ")) &&
		(strings.Contains(lower, "connected |") ||
			strings.Contains(lower, "connecting |"))
}

func isAgentInputReady(agentType, out string) bool {
	switch normalizeAgentType(agentType) {
	case "claude":
		return isClaudeInputReady(out)
	case "codex":
		return isCodexInputReady(out)
	case "opencode":
		return isOpenCodeInputReady(out)
	case "openclaw":
		return isOpenClawInputReady(out)
	default:
		return false
	}
}

func hasLazyStartup(agentType string) bool {
	switch normalizeAgentType(agentType) {
	case "opencode":
		return true
	default:
		return false
	}
}

func lazyAgentMarkerPath(agentType, paneID string) string {
	shortID := strings.Split(normPaneID(paneID), ":")[0]
	switch normalizeAgentType(agentType) {
	case "opencode":
		return filepath.Join(os.TempDir(), fmt.Sprintf("opencode-running-%s", shortID))
	default:
		return ""
	}
}

func ensurePaneReadyForSend(paneID string, trace *tmuxSendTrace) error {
	paneID = normPaneID(paneID)
	var agentType string
	var replyInChinese int
	if err := store.QueryRow("SELECT COALESCE(agent_type,''), COALESCE(reply_in_chinese,0) FROM agent_config WHERE pane_id=?", paneID).
		Scan(&agentType, &replyInChinese); err != nil {
		log.Printf("[lazy-agent] %s lookup skipped: %v", paneID, err)
		if trace != nil {
			trace.logStep("lookup-skipped", map[string]any{"error": err.Error()}, "")
		}
		return nil
	}
	if trace != nil && trace.AgentType == "" {
		trace.AgentType = normalizeAgentType(agentType)
	}
	if err := ensureLazyAgentReady(paneID, agentType, replyInChinese != 0); err != nil {
		if trace != nil {
			trace.logStep("lazy-ready-error", map[string]any{"error": err.Error()}, "")
		}
		return err
	}
	return waitForAgentInputReady(paneID, agentType, trace)
}

func ensureLazyAgentReady(paneID, agentType string, replyInChinese bool) error {
	switch normalizeAgentType(agentType) {
	case "opencode":
		return ensureLazyOpenCodeReady(paneID, replyInChinese)
	default:
		return nil
	}
}

func ensureLazyOpenCodeReady(paneID string, replyInChinese bool) error {
	out, err := runTmux("capture-pane", "-t", paneID, "-p", "-S", "-160")
	if err == nil && isOpenCodeInputReady(out) {
		return nil
	}

	markerPath := lazyAgentMarkerPath("opencode", paneID)
	startedNow := false
	if markerPath != "" {
		if _, statErr := os.Stat(markerPath); os.IsNotExist(statErr) {
			log.Printf("[lazy-agent] %s starting opencode", paneID)
			sendPaneText(paneID, "cicy_start_opencode")
			startedNow = true
		}
	}

	for i := 0; i < 120; i++ {
		time.Sleep(500 * time.Millisecond)
		out, err = runTmux("capture-pane", "-t", paneID, "-p", "-S", "-160")
		if err != nil {
			continue
		}
		if !isOpenCodeInputReady(out) {
			continue
		}
		if startedNow && replyInChinese {
			log.Printf("[lazy-agent] %s send reply_in_chinese after opencode start", paneID)
			sendPaneText(paneID, "reply in chinese")
			time.Sleep(800 * time.Millisecond)
		}
		return nil
	}

	return fmt.Errorf("pane %s opencode did not become ready in time", shortPaneID(paneID))
}

func waitForAgentInputReady(paneID, agentType string, trace *tmuxSendTrace) error {
	agentType = normalizeAgentType(agentType)
	if agentType == "" || agentType == "opencode" {
		return nil
	}
	if trace != nil {
		trace.logStep("ready-wait-start", map[string]any{}, "")
	}
	deadline := time.Now().Add(agentReadyTimeout)
	var lastCapture string
	for time.Now().Before(deadline) {
		out, err := runTmux("capture-pane", "-t", paneID, "-p", "-S", promptConfirmCaptureStart)
		if err == nil {
			lastCapture = out
		}
		if err == nil && isAgentInputReady(agentType, out) {
			if trace != nil {
				trace.logStep("ready-wait-confirmed", map[string]any{}, out)
			}
			return nil
		}
		time.Sleep(agentReadyPollIntervalForAgent(agentType))
	}
	if trace != nil {
		trace.logStep("ready-wait-timeout", map[string]any{}, lastCapture)
	}
	return fmt.Errorf("pane %s %s not ready for send", shortPaneID(paneID), agentType)
}

func agentReadyPollIntervalForAgent(agentType string) time.Duration {
	switch normalizeAgentType(agentType) {
	case "codex":
		return codexAgentReadyPollInterval
	default:
		return agentReadyPollInterval
	}
}

func promptConfirmPollIntervalForAgent(agentType string) time.Duration {
	switch normalizeAgentType(agentType) {
	case "codex":
		return codexPromptConfirmPollInterval
	default:
		return promptConfirmPollInterval
	}
}

func promptConfirmStabilizeDelayForAgent(agentType string) time.Duration {
	switch normalizeAgentType(agentType) {
	case "codex":
		return codexPromptConfirmStabilizeDelay
	default:
		return promptConfirmPollIntervalForAgent(agentType)
	}
}

func submitConfirmPollIntervalForAgent(agentType string) time.Duration {
	switch normalizeAgentType(agentType) {
	case "codex":
		return codexSubmitConfirmPollInterval
	default:
		return submitConfirmPollInterval
	}
}

func sendPaneText(paneID, text string) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		runTmux("send-keys", "-t", paneID, "-l", line)
		if i < len(lines)-1 {
			time.Sleep(100 * time.Millisecond)
			runTmux("send-keys", "-t", paneID, "Enter")
		}
	}
	time.Sleep(enterDelay)
	runTmux("send-keys", "-t", paneID, "Enter")
}

func promptPreview(text string) string {
	line := text
	if idx := strings.Index(line, "\n"); idx >= 0 {
		line = line[:idx]
	}
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) > 80 {
		return string(runes[:80]) + "..."
	}
	return line
}

func promptLineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func logPromptSend(paneID, mode, text string) {
	log.Printf("[tmux-send] pane=%s mode=%s lines=%d runes=%d preview=%q",
		shortPaneID(paneID),
		mode,
		promptLineCount(text),
		utf8.RuneCountInString(text),
		promptPreview(text),
	)
}

func shouldPastePromptText(text string) bool {
	return utf8.RuneCountInString(text) > directPromptRuneThreshold
}

func splitTextByRunes(text string, size int) []string {
	if size <= 0 || text == "" {
		return []string{text}
	}
	runes := []rune(text)
	if len(runes) <= size {
		return []string{text}
	}
	parts := make([]string, 0, (len(runes)+size-1)/size)
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		parts = append(parts, string(runes[start:end]))
	}
	return parts
}

func sendPromptChunked(paneID, text string) error {
	parts := splitTextByRunes(text, chunkedPromptRuneSize)
	for i, part := range parts {
		if part == "" {
			continue
		}
		if _, err := runTmux("send-keys", "-t", paneID, "-l", part); err != nil {
			return err
		}
		if i < len(parts)-1 {
			time.Sleep(chunkedPromptChunkDelay)
		}
	}
	return nil
}

func sendPromptText(paneID, text string, trace *tmuxSendTrace) (string, error) {
	mode := "literal"
	if shouldPastePromptText(text) {
		mode = "chunked"
	}
	logPromptSend(paneID, mode, text)
	if trace != nil {
		trace.logStep("send-text", map[string]any{
			"mode":       mode,
			"line_count": promptLineCount(text),
			"rune_count": utf8.RuneCountInString(text),
			"chunks":     len(splitTextByRunes(text, chunkedPromptRuneSize)),
		}, text)
	}
	if mode == "chunked" {
		return mode, sendPromptChunked(paneID, text)
	}
	_, err := runTmux("send-keys", "-t", paneID, "-l", text)
	return mode, err
}

func sendPromptEnter(paneID string) error {
	_, err := runTmux("send-keys", "-t", paneID, "Enter")
	return err
}

func paneAgentType(paneID string) string {
	var agentType string
	if err := store.QueryRow("SELECT COALESCE(agent_type,'') FROM agent_config WHERE pane_id=?", normPaneID(paneID)).Scan(&agentType); err != nil {
		return ""
	}
	return normalizeAgentType(agentType)
}

func shouldConfirmPromptBeforeEnter(paneID, agentType string) bool {
	if !strings.HasSuffix(normPaneID(paneID), ":main.0") {
		return false
	}
	switch normalizeAgentType(agentType) {
	case "codex", "claude", "openclaw":
		return true
	default:
		return false
	}
}

func capturePromptConfirmPane(paneID string) (string, error) {
	return runTmux("capture-pane", "-t", paneID, "-p", "-J", "-S", promptConfirmCaptureStart)
}

func normalizePromptEchoText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

func trimPromptFragmentRunes(s string, max int, fromEnd bool) string {
	runes := []rune(strings.TrimSpace(s))
	if max <= 0 || len(runes) <= max {
		return string(runes)
	}
	if fromEnd {
		return string(runes[len(runes)-max:])
	}
	return string(runes[:max])
}

func promptEchoFragments(text string) []string {
	add := func(dst []string, seen map[string]struct{}, s string) []string {
		s = normalizePromptEchoText(s)
		if s == "" {
			return dst
		}
		if _, ok := seen[s]; ok {
			return dst
		}
		seen[s] = struct{}{}
		return append(dst, s)
	}

	seen := map[string]struct{}{}
	var fragments []string
	normalized := normalizePromptEchoText(text)
	if normalized == "" {
		return nil
	}

	fragments = add(fragments, seen, normalized)
	fragments = add(fragments, seen, trimPromptFragmentRunes(normalized, 96, false))
	fragments = add(fragments, seen, trimPromptFragmentRunes(normalized, 96, true))

	var nonEmptyLines []string
	for _, line := range strings.Split(text, "\n") {
		line = normalizePromptEchoText(line)
		if line != "" {
			nonEmptyLines = append(nonEmptyLines, line)
		}
	}
	if len(nonEmptyLines) > 0 {
		fragments = add(fragments, seen, nonEmptyLines[0])
		fragments = add(fragments, seen, trimPromptFragmentRunes(nonEmptyLines[0], 96, false))
	}
	if len(nonEmptyLines) > 1 {
		last := nonEmptyLines[len(nonEmptyLines)-1]
		fragments = add(fragments, seen, last)
		fragments = add(fragments, seen, trimPromptFragmentRunes(last, 96, true))
	}
	return fragments
}

func promptEchoVisible(before, after, text string) bool {
	normBefore := normalizePromptEchoText(before)
	normAfter := normalizePromptEchoText(after)
	if normAfter == "" {
		return false
	}
	for _, fragment := range promptEchoFragments(text) {
		if fragment == "" {
			continue
		}
		if strings.Count(normAfter, fragment) > strings.Count(normBefore, fragment) {
			return true
		}
	}
	return false
}

func pasteIndicatorCount(out, agentType string) int {
	switch normalizeAgentType(agentType) {
	case "codex":
		return strings.Count(out, "[Pasted Content ")
	case "claude", "openclaw":
		return strings.Count(strings.ToLower(out), "pasted content")
	default:
		return 0
	}
}

func promptEchoConfirmed(before, after, text, mode, agentType string) (string, bool) {
	if promptEchoVisible(before, after, text) {
		return "matched-text", true
	}
	if mode == "chunked" {
		beforePasteCount := pasteIndicatorCount(before, agentType)
		afterPasteCount := pasteIndicatorCount(after, agentType)
		if afterPasteCount > beforePasteCount {
			return "matched-paste-indicator", true
		}
	}
	return "", false
}

func normalizeNonEmptyMeaningfulLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Trim(trimmed, "─━═- ") == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func openClawInputBoxContainsText(out, text string) bool {
	lines := strings.Split(out, "\n")
	var separators []int
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Trim(trimmed, "─━═- ") == "" {
			separators = append(separators, i)
		}
	}
	if len(separators) < 2 {
		return false
	}
	start := separators[len(separators)-2] + 1
	end := separators[len(separators)-1]
	if start >= end || start < 0 || end > len(lines) {
		return false
	}
	box := normalizePromptEchoText(strings.Join(lines[start:end], "\n"))
	if box == "" {
		return false
	}
	for _, fragment := range promptEchoFragments(text) {
		if fragment != "" && strings.Contains(box, fragment) {
			return true
		}
	}
	return false
}

func lineContainsPromptFragment(line string, text string) bool {
	normLine := normalizePromptEchoText(line)
	if normLine == "" {
		return false
	}
	for _, fragment := range promptEchoFragments(text) {
		if fragment != "" && strings.Contains(normLine, fragment) {
			return true
		}
	}
	return false
}

func codexSubmitConfirmed(out, text string) bool {
	lines := strings.Split(out, "\n")
	lastPromptLine := -1
	for i, line := range lines {
		if lineContainsPromptFragment(line, text) {
			lastPromptLine = i
		}
	}
	if lastPromptLine < 0 {
		return false
	}
	for _, line := range lines[lastPromptLine+1:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "• ") {
			return true
		}
		if strings.HasPrefix(trimmed, "›") && !lineContainsPromptFragment(line, text) {
			return true
		}
	}
	return false
}

func claudeSubmitConfirmed(out, text string) bool {
	lines := strings.Split(out, "\n")
	lastPromptLine := -1
	for i, line := range lines {
		if lineContainsPromptFragment(line, text) {
			lastPromptLine = i
		}
	}
	if lastPromptLine < 0 {
		return false
	}
	for _, line := range lines[lastPromptLine+1:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "● ") {
			return true
		}
		if strings.HasPrefix(trimmed, "❯") && !lineContainsPromptFragment(line, text) {
			return true
		}
	}
	return false
}

func openClawIdleStatusVisible(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "connected | idle") &&
		strings.Contains(lower, "agent main | session")
}

func openClawSubmitConfirmed(out, text string) bool {
	if openClawInputBoxContainsText(out, text) {
		return false
	}
	if openClawIdleStatusVisible(out) {
		return true
	}
	lines := normalizeNonEmptyMeaningfulLines(strings.Split(out, "\n"))
	lastPromptLine := -1
	for i, line := range lines {
		if lineContainsPromptFragment(line, text) {
			lastPromptLine = i
		}
	}
	return lastPromptLine >= 0 && lastPromptLine < len(lines)-1
}

func promptSubmitConfirmed(after, text, agentType string) bool {
	switch normalizeAgentType(agentType) {
	case "codex":
		return codexSubmitConfirmed(after, text)
	case "claude":
		return claudeSubmitConfirmed(after, text)
	case "openclaw":
		return openClawSubmitConfirmed(after, text)
	default:
		return false
	}
}

func waitForPromptEchoBeforeEnter(paneID, agentType, text, mode, baseline string, trace *tmuxSendTrace) (string, error) {
	pollInterval := promptConfirmPollIntervalForAgent(agentType)
	stabilizeDelay := promptConfirmStabilizeDelayForAgent(agentType)
	deadline := time.Now().Add(promptConfirmTimeout)
	var lastCapture string
	var lastErr error
	attempt := 0
	for time.Now().Before(deadline) {
		attempt++
		out, err := capturePromptConfirmPane(paneID)
		if err != nil {
			lastErr = err
			if trace != nil {
				trace.logStep("pre-submit-capture-error", map[string]any{"attempt": attempt, "error": err.Error()}, "")
			}
			time.Sleep(pollInterval)
			continue
		}
		lastCapture = out
		if trace != nil {
			match, ok := promptEchoConfirmed(baseline, out, text, mode, agentType)
			trace.logStep("pre-submit-capture", map[string]any{"attempt": attempt, "matched": ok, "match_kind": match}, out)
		}
		if match, ok := promptEchoConfirmed(baseline, out, text, mode, agentType); ok {
			time.Sleep(stabilizeDelay)
			out2, err := capturePromptConfirmPane(paneID)
			if err != nil {
				lastErr = err
				if trace != nil {
					trace.logStep("pre-submit-capture2-error", map[string]any{"attempt": attempt, "error": err.Error()}, "")
				}
				continue
			}
			lastCapture = out2
			if trace != nil {
				_, ok2 := promptEchoConfirmed(baseline, out2, text, mode, agentType)
				trace.logStep("pre-submit-capture2", map[string]any{"attempt": attempt, "matched": ok2, "match_kind": match}, out2)
			}
			if _, ok := promptEchoConfirmed(baseline, out2, text, mode, agentType); ok {
				log.Printf("[tmux-send] pane=%s confirm=%s confirm2=matched agent=%s mode=%s preview=%q",
					shortPaneID(paneID), match, normalizeAgentType(agentType), mode, promptPreview(text))
				return match, nil
			}
			log.Printf("[tmux-send] pane=%s confirm=%s confirm2=miss agent=%s mode=%s preview=%q",
				shortPaneID(paneID), match, normalizeAgentType(agentType), mode, promptPreview(text))
		}
		time.Sleep(pollInterval)
	}
	if lastErr != nil {
		return "", fmt.Errorf("prompt echo confirm failed for %s: %w", shortPaneID(paneID), lastErr)
	}
	if lastCapture != "" {
		log.Printf("[tmux-send] pane=%s confirm=timeout agent=%s mode=%s preview=%q tail=%q",
			shortPaneID(paneID), normalizeAgentType(agentType), mode, promptPreview(text), promptPreview(lastCapture))
	}
	return "", fmt.Errorf("prompt echo not confirmed for %s", shortPaneID(paneID))
}

func submitPromptWithConfirmation(paneID, agentType, text string, trace *tmuxSendTrace) error {
	pollInterval := submitConfirmPollIntervalForAgent(agentType)
	var lastCapture string
	var lastErr error
	for attempt := 0; attempt <= submitEnterRetryLimit; attempt++ {
		if attempt > 0 {
			log.Printf("[tmux-send] pane=%s submit=retry attempt=%d agent=%s preview=%q",
				shortPaneID(paneID), attempt+1, normalizeAgentType(agentType), promptPreview(text))
		}
		if trace != nil {
			trace.logStep("submit-enter", map[string]any{"attempt": attempt + 1}, "")
		}
		if err := sendPromptEnter(paneID); err != nil {
			if trace != nil {
				trace.logStep("submit-enter-error", map[string]any{"attempt": attempt + 1, "error": err.Error()}, "")
			}
			return fmt.Errorf("failed to submit text: %w", err)
		}
		deadline := time.Now().Add(submitConfirmTimeout)
		captureAttempt := 0
		for time.Now().Before(deadline) {
			captureAttempt++
			out, err := capturePromptConfirmPane(paneID)
			if err != nil {
				lastErr = err
				if trace != nil {
					trace.logStep("post-submit-capture-error", map[string]any{"attempt": attempt + 1, "capture_attempt": captureAttempt, "error": err.Error()}, "")
				}
				time.Sleep(pollInterval)
				continue
			}
			lastCapture = out
			if trace != nil {
				trace.logStep("post-submit-capture", map[string]any{
					"attempt":         attempt + 1,
					"capture_attempt": captureAttempt,
					"confirmed":       promptSubmitConfirmed(out, text, agentType),
				}, out)
			}
			if promptSubmitConfirmed(out, text, agentType) {
				log.Printf("[tmux-send] pane=%s submit=confirmed agent=%s preview=%q",
					shortPaneID(paneID), normalizeAgentType(agentType), promptPreview(text))
				return nil
			}
			time.Sleep(pollInterval)
		}
	}
	if lastErr != nil {
		return fmt.Errorf("submit confirm failed for %s: %w", shortPaneID(paneID), lastErr)
	}
	if lastCapture != "" {
		log.Printf("[tmux-send] pane=%s submit=timeout agent=%s preview=%q tail=%q",
			shortPaneID(paneID), normalizeAgentType(agentType), promptPreview(text), promptPreview(lastCapture))
	}
	return fmt.Errorf("submit not confirmed for %s", shortPaneID(paneID))
}

func autoSendReplyInChinese(paneID, agentType string, enabled bool) {
	if !enabled {
		return
	}
	if hasLazyStartup(agentType) {
		return
	}
	replyInChineseStartupQueue.enqueue(paneID, agentType)
}

func autoConfirmClaudeStartup(paneID string, allowAllActions bool) {
	go func() {
		var lastAction time.Time
		var currentStage claudePromptStage
		var stageSince time.Time
		stageAttempts := 0
		for i := 0; i < 120; i++ {
			time.Sleep(500 * time.Millisecond)
			out, err := runTmux("capture-pane", "-t", paneID, "-p", "-S", "-160")
			if err != nil {
				continue
			}
			if isClaudeInputReady(out) {
				log.Printf("[claude-auto-confirm] %s ready", paneID)
				return
			}
			stage := detectClaudePromptStage(out, allowAllActions)
			if stage == claudeStageNone {
				currentStage = claudeStageNone
				stageAttempts = 0
				stageSince = time.Time{}
				continue
			}
			if stage != currentStage {
				currentStage = stage
				stageSince = time.Now()
				stageAttempts = 0
			}
			if time.Since(lastAction) < 1200*time.Millisecond {
				continue
			}
			if stageAttempts >= 1 && time.Since(stageSince) < 4*time.Second {
				continue
			}
			if stageAttempts >= 2 {
				continue
			}
			switch stage {
			case claudeStageTheme:
				log.Printf("[claude-auto-confirm] %s theme selected", paneID)
				runTmux("send-keys", "-t", paneID, "Enter")
			case claudeStageSecurityNotes:
				log.Printf("[claude-auto-confirm] %s security notes continue", paneID)
				runTmux("send-keys", "-t", paneID, "Enter")
			case claudeStageTrust:
				log.Printf("[claude-auto-confirm] %s trust workspace", paneID)
				runTmux("send-keys", "-t", paneID, "Enter")
			case claudeStageBypassChoice:
				log.Printf("[claude-auto-confirm] %s accept bypass mode", paneID)
				runTmux("send-keys", "-t", paneID, "Down")
				time.Sleep(150 * time.Millisecond)
				runTmux("send-keys", "-t", paneID, "Enter")
			case claudeStageBypassConfirm:
				log.Printf("[claude-auto-confirm] %s confirm bypass mode", paneID)
				runTmux("send-keys", "-t", paneID, "Enter")
			}
			lastAction = time.Now()
			stageAttempts++
		}
		log.Printf("[claude-auto-confirm] %s timeout", paneID)
	}()
}

func autoConfirmCodexTrust(paneID string) {
	go func() {
		var lastAction time.Time
		enterCount := 0
		for i := 0; i < 120; i++ {
			time.Sleep(500 * time.Millisecond)
			out, err := runTmux("capture-pane", "-t", paneID, "-p")
			if err != nil {
				continue
			}
			if isCodexInputReady(out) {
				log.Printf("[codex-auto-confirm] %s ready", paneID)
				return
			}
			if !isCodexTrustPrompt(out) {
				continue
			}
			if enterCount >= 3 || time.Since(lastAction) < 1500*time.Millisecond {
				continue
			}
			log.Printf("[codex-auto-confirm] %s trust workspace enter #%d", paneID, enterCount+1)
			runTmux("send-keys", "-t", paneID, "Enter")
			lastAction = time.Now()
			enterCount++
		}
		log.Printf("[codex-auto-confirm] %s timeout", paneID)
	}()
}

func initPaneEnv(opts paneEnvOpts) {
	pid := opts.paneID
	shortID := strings.Split(pid, ":")[0]
	// proxyURL := fmt.Sprintf("http://%s:x@127.0.0.1:17080", shortID)

	// tmux panes inherit environment from the tmux server, not from the current
	// cicy-code process. Sync runtime auth/config into the target session before
	// sourcing the init script so agent boot code can see current values.
	sessionEnv := map[string]string{
		"CICY_API_KEY":                strings.TrimSpace(os.Getenv("CICY_API_KEY")),
		"CICY_API_URL":                strings.TrimSpace(os.Getenv("CICY_API_URL")),
		"CICY_ANTHROPIC_URL":          strings.TrimSpace(os.Getenv("CICY_ANTHROPIC_URL")),
		"CICY_DEFAULT_CLAUDE_MODEL":   strings.TrimSpace(os.Getenv("CICY_DEFAULT_CLAUDE_MODEL")),
		"CICY_DEFAULT_OPENCODE_MODEL": strings.TrimSpace(os.Getenv("CICY_DEFAULT_OPENCODE_MODEL")),
		"CICY_CODEX_MODEL":            strings.TrimSpace(os.Getenv("CICY_CODEX_MODEL")),
		"CICY_OPENCLAW_MODEL":         strings.TrimSpace(os.Getenv("CICY_OPENCLAW_MODEL")),
	}
	for key, value := range sessionEnv {
		if value == "" {
			_, _ = runTmux("set-environment", "-r", "-t", shortID, key)
			continue
		}
		_, _ = runTmux("set-environment", "-t", shortID, key, value)
	}

	lines := []string{
		"touch ~/.cicy_tmux.conf",
		"source ~/.cicy_tmux.conf",
		`export PATH="$HOME/.local/bin:$HOME/.opencode/bin:$PATH"`,
		fmt.Sprintf("export X_AGENT_ID=%s", tmuxShellQuote(pid)),
		fmt.Sprintf("export X_AGENT_SHORT_ID=%s", tmuxShellQuote(shortID)),
		fmt.Sprintf("export CICY_API_KEY=%s", tmuxShellQuote(strings.TrimSpace(os.Getenv("CICY_API_KEY")))),
		fmt.Sprintf("export CICY_API_URL=%s", tmuxShellQuote(strings.TrimSpace(os.Getenv("CICY_API_URL")))),
		fmt.Sprintf("export CICY_ANTHROPIC_URL=%s", tmuxShellQuote(strings.TrimSpace(os.Getenv("CICY_ANTHROPIC_URL")))),
		fmt.Sprintf("export CICY_DEFAULT_CLAUDE_MODEL=%s", tmuxShellQuote(strings.TrimSpace(os.Getenv("CICY_DEFAULT_CLAUDE_MODEL")))),
		fmt.Sprintf("export CICY_DEFAULT_OPENCODE_MODEL=%s", tmuxShellQuote(strings.TrimSpace(os.Getenv("CICY_DEFAULT_OPENCODE_MODEL")))),
		fmt.Sprintf("export CICY_CODEX_MODEL=%s", tmuxShellQuote(strings.TrimSpace(os.Getenv("CICY_CODEX_MODEL")))),
		fmt.Sprintf("export CICY_OPENCLAW_MODEL=%s", tmuxShellQuote(strings.TrimSpace(os.Getenv("CICY_OPENCLAW_MODEL")))),
		fmt.Sprintf("export CICY_OPENAI_BASE_URL=%s", tmuxShellQuote(openAIRuntimeBaseURL(shortID))),
		fmt.Sprintf("export CICY_ANTHROPIC_BASE_URL=%s", tmuxShellQuote(anthropicRuntimeBaseURL(shortID))),
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
			// "mkdir -p ./skills ./projects",
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
	if normalizeAgentType(opts.agentType) == "claude" {
		autoConfirmClaudeStartup(pid, opts.allowAllActions)
	} else if normalizeAgentType(opts.agentType) == "codex" {
		autoConfirmCodexTrust(pid)
	}
	autoSendReplyInChinese(pid, opts.agentType, opts.replyInChinese)
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
		text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
		if text == "" {
			J(w, M{"error": "text required"})
			return
		}
		agentType := paneAgentType(winID)
		trace := newTmuxSendTrace(winID, agentType)
		trace.logStep("request", map[string]any{
			"line_count": promptLineCount(text),
			"rune_count": utf8.RuneCountInString(text),
		}, text)
		if err := ensurePaneReadyForSend(winID, trace); err != nil {
			trace.logStep("request-error", map[string]any{"error": err.Error()}, "")
			J(w, M{"error": err.Error()})
			return
		}
		confirmBeforeEnter := shouldConfirmPromptBeforeEnter(winID, agentType)
		baseline := ""
		if confirmBeforeEnter {
			out, err := capturePromptConfirmPane(winID)
			if err != nil {
				log.Printf("[tmux-send] pane=%s confirm=baseline-capture-failed agent=%s err=%v",
					shortPaneID(winID), agentType, err)
				trace.logStep("baseline-capture-error", map[string]any{"error": err.Error()}, "")
			} else {
				baseline = out
				trace.logStep("baseline-capture", map[string]any{}, out)
			}
		}
		mode, err := sendPromptText(winID, text, trace)
		if err != nil {
			trace.logStep("send-text-error", map[string]any{"error": err.Error()}, "")
			J(w, M{"error": "failed to send text: " + err.Error()})
			return
		}
		if confirmBeforeEnter {
			if _, err := waitForPromptEchoBeforeEnter(winID, agentType, text, mode, baseline, trace); err != nil {
				trace.logStep("pre-submit-failed", map[string]any{"error": err.Error()}, "")
				J(w, M{"error": "failed to confirm text before submit: " + err.Error()})
				return
			}
			if err := submitPromptWithConfirmation(winID, agentType, text, trace); err != nil {
				trace.logStep("submit-failed", map[string]any{"error": err.Error()}, "")
				J(w, M{"error": err.Error()})
				return
			}
		} else {
			delay := enterDelay
			if mode == "chunked" {
				delay = chunkedPromptEnterDelay
			}
			time.Sleep(delay)
			if err := sendPromptEnter(winID); err != nil {
				trace.logStep("submit-enter-error", map[string]any{"error": err.Error()}, "")
				J(w, M{"error": "failed to submit text: " + err.Error()})
				return
			}
		}
		trace.logStep("request-complete", map[string]any{"mode": mode, "confirm_before_enter": confirmBeforeEnter}, "")
	} else if keys, ok := req["keys"].(string); ok && keys != "" {
		log.Printf("[tmux-send] pane=%s mode=keys keys=%q", shortPaneID(winID), keys)
		if _, err := runTmux("send-keys", "-t", winID, keys); err != nil {
			J(w, M{"error": "failed to send keys: " + err.Error()})
			return
		}
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
