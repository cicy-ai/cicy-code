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
	"runtime"
	"strconv"
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
const openClawAgentReadyTimeout = 150 * time.Second
const submitConfirmPollInterval = 200 * time.Millisecond
const codexSubmitConfirmPollInterval = 80 * time.Millisecond
const submitConfirmTimeout = 4 * time.Second
const codexSubmitConfirmTimeout = 1200 * time.Millisecond
const submitEnterRetryLimit = 2
const codexSubmitEnterRetryLimit = 0
const startupPromptBatchWindow = 5 * time.Second
const startupPromptCooldown = 15 * time.Second
const directPromptRuneThreshold = 600
const bracketedPasteStart = "\x1b[200~"
const bracketedPasteEnd = "\x1b[201~"
const shellPromptPollInterval = 200 * time.Millisecond
const shellPromptTimeout = 12 * time.Second
const shellPromptTimeoutDarwin = 20 * time.Second

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

type paneSendRequest struct {
	text   string
	result chan error
}

type paneSendWorker struct {
	ch chan paneSendRequest
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
var paneSendWorkersMu sync.Mutex
var paneSendWorkers = map[string]*paneSendWorker{}

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

func tmuxClientTraceLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "cicy-tmux-client-trace.log")
	}
	return filepath.Join(home, ".cicy", "tmux-client-trace.log")
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

func appendTmuxClientTrace(entry map[string]any) {
	if entry == nil {
		return
	}
	entry["ts_server"] = time.Now().UTC().Format(time.RFC3339Nano)
	payload, _ := json.Marshal(entry)
	path := tmuxClientTraceLogPath()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	tmuxSendTraceMu.Lock()
	defer tmuxSendTraceMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("[tmux-client-trace] log-open-error=%v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(payload, '\n')); err != nil {
		log.Printf("[tmux-client-trace] log-write-error=%v", err)
		return
	}
}

func handleTmuxClientTrace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req M
	readBody(r, &req)
	req["remote_addr"] = r.RemoteAddr
	req["user_agent"] = r.UserAgent()
	appendTmuxClientTrace(req)
	J(w, M{"success": true})
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

func isShellPromptVisible(out string) bool {
	lines := normalizeNonEmptyMeaningfulLines(strings.Split(out, "\n"))
	if len(lines) == 0 {
		return false
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return false
	}
	// Before boot.sh runs the pane may show the system's default shell prompt
	// (often "%" on macOS zsh, "$" on bash). After ~/.cicy_tmux.conf loads it
	// becomes "... $". In all cases, wait for a visible prompt terminator before
	// sending the startup script.
	switch {
	case strings.HasSuffix(last, " $"),
		strings.HasSuffix(last, "$"),
		strings.HasSuffix(last, " %"),
		strings.HasSuffix(last, "%"),
		strings.HasSuffix(last, " #"),
		strings.HasSuffix(last, "#"):
		return true
	default:
		return false
	}
}

func shellPromptTimeoutForRuntime() time.Duration {
	if runtime.GOOS == "darwin" {
		return shellPromptTimeoutDarwin
	}
	return shellPromptTimeout
}

func paneCurrentCommand(paneID string) string {
	out, err := runTmux("display-message", "-p", "-t", paneID, "#{pane_current_command}")
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(out))
}

func isDarwinShellDollarPrompt(out string) bool {
	lines := normalizeNonEmptyMeaningfulLines(strings.Split(out, "\n"))
	if len(lines) == 0 {
		return false
	}
	return strings.HasSuffix(strings.TrimSpace(lines[len(lines)-1]), " $")
}

func waitForShellPromptReady(paneID string) bool {
	deadline := time.Now().Add(shellPromptTimeoutForRuntime())
	stableCount := 0
	lastCapture := ""
	lastCommand := ""
	for time.Now().Before(deadline) {
		out, err := runTmux("capture-pane", "-t", paneID, "-p", "-S", "-80")
		if err == nil {
			lastCapture = out
			if runtime.GOOS == "darwin" {
				if isDarwinShellDollarPrompt(out) {
					stableCount++
				} else {
					stableCount = 0
				}
				if stableCount >= 2 {
					return true
				}
			} else {
				if isShellPromptVisible(out) {
					stableCount++
				} else {
					stableCount = 0
				}
			}
		}
		if runtime.GOOS != "darwin" {
			lastCommand = paneCurrentCommand(paneID)
			if stableCount >= 2 && (lastCommand == "bash" || lastCommand == "zsh" || lastCommand == "sh") {
				return true
			}
		}
		time.Sleep(shellPromptPollInterval)
	}
	log.Printf("[init] shell prompt not confirmed for %s within %s; cmd=%q last capture=%q", shortPaneID(paneID), shellPromptTimeoutForRuntime(), lastCommand, promptPreview(lastCapture))
	return false
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
	req.AgentType = normalizeAgentType(req.AgentType)
	if req.AgentType == "" {
		J(w, M{"success": false, "error": "unsupported agent_type"})
		return
	}
	token := getToken(r)

	result, err := doCreatePane(req.Title, req.Role, req.DefaultModel, req.AgentType, req.InitScript, req.AllowAllActions, req.ReplyInChinese, req.WinName, token)
	if err != nil {
		J(w, M{"success": false, "error": err.Error()})
		return
	}
	J(w, result)
}

func doCreatePane(title, role, defaultModel, agentType, initScript string, allowAllActions bool, replyInChinese bool, winName *string, token string) (M, error) {
	agentType = normalizeAgentType(agentType)
	if agentType == "" {
		return M{"success": false}, fmt.Errorf("unsupported agent_type")
	}
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
	go syncTelegramPollers()

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
	case "codex", "openai":
		return "codex"
	case "kiro-cli", "kiro", "kiro-cli chat":
		return "kiro-cli"
	case "copilot", "github-copilot", "github copilot", "ghcopilot":
		return "copilot"
	case "cicy-wechat", "wechat":
		return "cicy-wechat"
	case "cicy-feishu", "feishu", "lark":
		return "cicy-feishu"
	case "claude", "claude code", "claude-code":
		return "claude"
	case "cicy", "cicy-claude":
		return "cicy-claude"
	case "opencode", "open code", "open-code":
		return "opencode"
	case "hermes", "hermes-agent", "hermes agent":
		return "hermes"
	default:
		return ""
	}
}

func normalizedOpenClawPrimaryModel() string {
	model := strings.ToLower(strings.TrimSpace(loadRuntimeAIConfig().OpenClawModel))
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
		port = "8008"
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
	return fmt.Sprintf(`__cicy_require_command %s %s %s %s`, tmuxShellQuote(commandName), tmuxShellQuote(label), tmuxShellQuote(installCmd), tmuxShellQuote(logPath))
}

func visibleAgentInstallLiveLine(commandName, label, installCmd, logPath string) string {
	return fmt.Sprintf(`__cicy_require_command_live %s %s %s %s`, tmuxShellQuote(commandName), tmuxShellQuote(label), tmuxShellQuote(installCmd), tmuxShellQuote(logPath))
}

func ensureAgentCommandLine(commandName, label, installCmd, logPath string) string {
	return visibleAgentInstallLine(commandName, label, installCmd, logPath)
}

func ensureAgentCommandLineLive(commandName, label, installCmd, logPath string) string {
	return visibleAgentInstallLiveLine(commandName, label, installCmd, logPath)
}

func kiroCliBootHelperLines() []string {
	return []string{
		`__cicy_local_install_kiro() {
  local download_dir installer_path manifest_url base_url channel arch_raw arch suffix filename download_url expected_checksum actual_checksum install_script
  download_dir="$(mktemp -d "${TMPDIR:-/tmp}/kiro-cli-install-XXXXXX")" || return 1
  installer_path="$download_dir/install.sh"
  echo '[cicy] 下载安装脚本: https://cli.kiro.dev/install'
  curl -fsSL -o "$installer_path" https://cli.kiro.dev/install || {
    rm -rf "$download_dir"
    return 1
  }
  base_url="$(sed -n 's/^BASE_URL="\([^"]*\)"/\1/p' "$installer_path" | head -n 1)"
  channel="$(sed -n 's/^CHANNEL="\([^"]*\)"/\1/p' "$installer_path" | head -n 1)"
  manifest_url="${base_url%/}/${channel}/latest/manifest.json"
  arch_raw="$(uname -m)"
  case "$arch_raw" in
    x86_64|amd64) arch="x86_64" ;;
    aarch64|arm64) arch="aarch64" ;;
    *)
      echo "[cicy] 不支持的 Kiro CLI 架构: $arch_raw"
      rm -rf "$download_dir"
      return 1
      ;;
  esac
  suffix="-musl"
  filename="kirocli-${arch}-linux${suffix}.zip"
  download_url="${base_url%/}/${channel}/latest/${filename}"
  echo "[cicy] 真实下载地址: $download_url"
  echo "[cicy] 获取校验清单: $manifest_url"
  curl -fsSL -o "$download_dir/manifest.json" "$manifest_url" || {
    rm -rf "$download_dir"
    return 1
  }
  expected_checksum="$(python3 - "$download_dir/manifest.json" "$filename" <<'PY'
import json, sys
manifest_path, filename = sys.argv[1], sys.argv[2]
with open(manifest_path, "r", encoding="utf-8") as fh:
    data = json.load(fh)
for pkg in data.get("packages", []):
    if str(pkg.get("download", "")).endswith(filename):
        print(pkg.get("sha256", ""))
        break
PY
)"
  if [ -z "$expected_checksum" ]; then
    echo "[cicy] 未找到校验和: $filename"
    rm -rf "$download_dir"
    return 1
  fi
  echo "[cicy] 开始下载 Kiro CLI 安装包..."
  curl -L -o "$download_dir/$filename" "$download_url" || {
    rm -rf "$download_dir"
    return 1
  }
  actual_checksum="$(sha256sum "$download_dir/$filename" | awk '{print $1}')"
  if [ "$actual_checksum" != "$expected_checksum" ]; then
    echo "[cicy] Kiro CLI 校验失败"
    echo "[cicy] expected: $expected_checksum"
    echo "[cicy] actual:   $actual_checksum"
    rm -rf "$download_dir"
    return 1
  fi
  unzip -q "$download_dir/$filename" -d "$download_dir" || {
    rm -rf "$download_dir"
    return 1
  }
  install_script="$download_dir/kirocli/install.sh"
  chmod +x "$install_script" || {
    rm -rf "$download_dir"
    return 1
  }
  KIRO_CLI_SKIP_SETUP=1 bash "$install_script"
  local status=$?
  rm -rf "$download_dir"
  return "$status"
}`,
		`__cicy_purge_bad_kiro() {
  local bad=0
  if [ -x "$HOME/.local/bin/kiro-cli-chat" ] && strings "$HOME/.local/bin/kiro-cli-chat" 2>/dev/null | grep -Eq 'GLIBC_2\.(38|39)'; then
    bad=1
  fi
  if [ "$bad" -eq 1 ]; then
    echo '[cicy] 检测到旧版 Kiro 二进制与当前系统 glibc 不兼容，准备重新安装...'
    rm -f "$HOME/.local/bin/kiro-cli" "$HOME/.local/bin/kiro-cli-chat" "$HOME/.local/bin/kiro-cli-term"
    hash -r 2>/dev/null || true
  fi
}`,
		`__cicy_purge_bad_kiro`,
	}
}

func agentBootLines(agentType string, allowAllActions bool, shortID string) []string {
	aiCfg := loadRuntimeAIConfig()
	switch normalizeAgentType(agentType) {
	case "openclaw":
		home, _ := os.UserHomeDir()
		baseStateDir := filepath.Join(home, ".openclaw")
		stateDir := filepath.Join(home, ".openclaw-"+shortID)
		baseConfigPath := filepath.Join(baseStateDir, "openclaw.json")
		stateConfigPath := filepath.Join(stateDir, "openclaw.json")
		installLog := filepath.Join(stateDir, "openclaw-install.log")
		sessionName := "main"
		sessionStorePath := filepath.Join(stateDir, "agents", "main", "sessions", "sessions.json")
		lines := []string{
			fmt.Sprintf("export OPENCLAW_BASE_CONFIG_PATH=%s", tmuxShellQuote(baseConfigPath)),
			fmt.Sprintf("export OPENCLAW_CONFIG_PATH=%s", tmuxShellQuote(stateConfigPath)),
			fmt.Sprintf("export OPENCLAW_STATE_DIR=%s", tmuxShellQuote(stateDir)),
			fmt.Sprintf("export OPENCLAW_SESSION_KEY=%s", tmuxShellQuote("agent:main:"+sessionName)),
			fmt.Sprintf("export OPENCLAW_SESSION_STORE=%s", tmuxShellQuote(sessionStorePath)),
			fmt.Sprintf("export OPENAI_API_KEY=%s", tmuxShellQuote("cicy-local-gateway")),
			fmt.Sprintf("export OPENAI_BASE_URL=%s", tmuxShellQuote(openAIRuntimeBaseURL(shortID))),
			fmt.Sprintf("export ANTHROPIC_BASE_URL=%s", tmuxShellQuote(anthropicRuntimeBaseURL(shortID))),
			fmt.Sprintf("mkdir -p %s", tmuxShellQuote(stateDir)),
			fmt.Sprintf("mkdir -p %s %s %s %s %s", tmuxShellQuote(filepath.Join(stateDir, "identity")), tmuxShellQuote(filepath.Join(stateDir, "devices")), tmuxShellQuote(filepath.Join(stateDir, "extensions")), tmuxShellQuote(filepath.Join(stateDir, "agents", "main", "agent")), tmuxShellQuote(filepath.Dir(sessionStorePath))),
			fmt.Sprintf("if [ -d %s ]; then cp -a %s/. %s/; fi", tmuxShellQuote(filepath.Join(baseStateDir, "identity")), tmuxShellQuote(filepath.Join(baseStateDir, "identity")), tmuxShellQuote(filepath.Join(stateDir, "identity"))),
			fmt.Sprintf("if [ -d %s ]; then cp -a %s/. %s/; fi", tmuxShellQuote(filepath.Join(baseStateDir, "devices")), tmuxShellQuote(filepath.Join(baseStateDir, "devices")), tmuxShellQuote(filepath.Join(stateDir, "devices"))),
			fmt.Sprintf("if [ -d %s ]; then cp -a %s/. %s/; fi", tmuxShellQuote(filepath.Join(baseStateDir, "extensions")), tmuxShellQuote(filepath.Join(baseStateDir, "extensions")), tmuxShellQuote(filepath.Join(stateDir, "extensions"))),
			`node - <<'EOF'
const fs = require("fs");
const path = require("path");
const src = process.env.OPENCLAW_BASE_CONFIG_PATH;
const dst = process.env.OPENCLAW_CONFIG_PATH;
const baseStateDir = src ? path.dirname(src) : "";
const stateDir = process.env.OPENCLAW_STATE_DIR || "";
if (!src || !dst) process.exit(0);
const cfg = JSON.parse(fs.readFileSync(src, "utf8"));
let existingCfg = null;
try {
  if (fs.existsSync(dst)) {
    existingCfg = JSON.parse(fs.readFileSync(dst, "utf8"));
  }
} catch (_) {}
function mergeWeixinChannelConfig(targetCfg, sourceCfg) {
  const existingChannel = sourceCfg?.channels?.["openclaw-weixin"];
  if (!existingChannel || typeof existingChannel !== "object") return;
  targetCfg.channels ||= {};
  const currentChannel = targetCfg.channels["openclaw-weixin"];
  if (!currentChannel || typeof currentChannel !== "object") {
    targetCfg.channels["openclaw-weixin"] = existingChannel;
    return;
  }
  const nextChannel = { ...currentChannel, ...existingChannel };
  const currentAccounts = currentChannel.accounts && typeof currentChannel.accounts === "object" ? currentChannel.accounts : {};
  const existingAccounts = existingChannel.accounts && typeof existingChannel.accounts === "object" ? existingChannel.accounts : {};
  if (Object.keys(currentAccounts).length || Object.keys(existingAccounts).length) {
    nextChannel.accounts = { ...currentAccounts, ...existingAccounts };
  }
  targetCfg.channels["openclaw-weixin"] = nextChannel;
}
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
cfg.agents ||= {};
cfg.agents.defaults ||= {};
cfg.agents.defaults.contextTokens = providerApi === "anthropic-messages" ? 200000 : 272000;
cfg.agents.defaults.model = cfg.agents.defaults.model || {};
cfg.agents.defaults.model.primary = "cicy/" + model;
cfg.models.providers.cicy.baseUrl = baseUrl;
cfg.models.providers.cicy.apiKey = "cicy-local-gateway";
	cfg.models.providers.cicy.api = providerApi;
	if (Array.isArray(cfg.models.providers.cicy.models)) {
	  cfg.models.providers.cicy.models = cfg.models.providers.cicy.models.map((entry) => {
	    const next = { ...entry, api: providerApi };
	    if (providerApi === "openai-completions" && (next.id === "gpt-5.4" || next.id === "gpt-5.3-codex")) {
	      next.contextWindow = Math.max(Number(next.contextWindow) || 0, 272000);
	    }
	    delete next.contextTokens;
	    return next;
	  });
	}
if (baseStateDir && stateDir && cfg.plugins && cfg.plugins.installs && typeof cfg.plugins.installs === "object") {
  for (const install of Object.values(cfg.plugins.installs)) {
    if (!install || typeof install !== "object") continue;
    const installPath = typeof install.installPath === "string" ? install.installPath : "";
    if (!installPath) continue;
    if (installPath === baseStateDir) {
      install.installPath = stateDir;
      continue;
    }
    const prefix = baseStateDir + "/";
    if (installPath.startsWith(prefix)) {
      install.installPath = stateDir + "/" + installPath.slice(prefix.length);
    }
  }
}
mergeWeixinChannelConfig(cfg, existingCfg);
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
		lines = append(lines, `openclaw plugins list 2>/dev/null | grep -q openclaw-weixin || OPENCLAW_STATE_DIR="$OPENCLAW_STATE_DIR" openclaw plugins install "@tencent-weixin/openclaw-weixin@latest" 2>/dev/null || true`)
		lines = append(lines, `openclaw plugins list 2>/dev/null | grep -q openclaw-lark || OPENCLAW_STATE_DIR="$OPENCLAW_STATE_DIR" openclaw plugins install "@larksuite/openclaw-lark@latest" 2>/dev/null || true`)
		lines = append(lines, fmt.Sprintf(`openclaw_profile_cmd=(openclaw --profile %s)`, tmuxShellQuote(shortID)))
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
		if normalizeAgentType(agentType) == "openclaw" {
			lines = append(lines,
				fmt.Sprintf(`openclaw_sessions_store=%s`, tmuxShellQuote(filepath.Join(stateDir, "agents", "main", "sessions", "sessions.json"))),
				fmt.Sprintf(`weixin_accounts_path=%s`, tmuxShellQuote(filepath.Join(stateDir, "openclaw-weixin", "accounts.json"))),
				fmt.Sprintf(`weixin_accounts_dir=%s`, tmuxShellQuote(filepath.Join(stateDir, "openclaw-weixin", "accounts"))),
				fmt.Sprintf(`weixin_ready_notify_state=%s`, tmuxShellQuote(filepath.Join(stateDir, "openclaw-weixin", ".last-ready-notify"))),
				fmt.Sprintf(`weixin_welcome_state=%s`, tmuxShellQuote(filepath.Join(stateDir, "openclaw-weixin", ".last-welcome-stamp"))),
				fmt.Sprintf(`feishu_welcome_state=%s`, tmuxShellQuote(filepath.Join(stateDir, "openclaw-lark", ".last-welcome-stamp"))),
			)
		}
		gatewayLog := filepath.Join(stateDir, "openclaw-gateway.log")
		tmpGatewayLog := filepath.Join("/tmp", fmt.Sprintf("openclaw-gateway-%s.log", shortID))
		lines = append(lines,
			strings.Join([]string{
				"resolve_openclaw_tui_session() {",
				`  node - "$OPENCLAW_SESSION_STORE" "$weixin_accounts_dir" <<'EOF'`,
				`const fs = require("fs");`,
				`const path = require("path");`,
				`const storePath = process.argv[2];`,
				`const accountsDir = process.argv[3];`,
				`const fallback = "main";`,
				`const prefix = "agent:main:openclaw-weixin:direct:";`,
				`function latestWeixinAccountSession(dir) {`,
				`  if (!dir || !fs.existsSync(dir)) return "";`,
				`  const files = fs.readdirSync(dir)`,
				`    .filter((name) => name.endsWith(".json") && !name.endsWith(".sync.json") && !name.endsWith(".context-tokens.json"))`,
				`    .sort();`,
				`  if (!files.length) return "";`,
				`  const file = path.join(dir, files[files.length - 1]);`,
				`  const raw = JSON.parse(fs.readFileSync(file, "utf8"));`,
				`  const userId = String((raw && raw.userId) || "").trim().toLowerCase();`,
				`  if (!userId) return "";`,
				`  return "openclaw-weixin:direct:" + userId;`,
				`}`,
				`try {`,
				`  if (!storePath || !fs.existsSync(storePath)) {`,
				`    process.stdout.write(latestWeixinAccountSession(accountsDir) || fallback);`,
				`    process.exit(0);`,
				`  }`,
				`  const raw = JSON.parse(fs.readFileSync(storePath, "utf8"));`,
				`  const matches = Object.entries(raw || {})`,
				`    .filter(([key]) => typeof key === "string" && key.startsWith(prefix))`,
				`    .map(([key, value]) => ({`,
				`      sessionName: key.slice("agent:main:".length),`,
				`      updatedAt: Number((value && value.updatedAt) || 0),`,
				`    }))`,
				`    .filter((entry) => entry.sessionName);`,
				`  matches.sort((a, b) => b.updatedAt - a.updatedAt || a.sessionName.localeCompare(b.sessionName));`,
				`  process.stdout.write((matches[0] && matches[0].sessionName) || latestWeixinAccountSession(accountsDir) || fallback);`,
				`} catch (_) {`,
				`  process.stdout.write(latestWeixinAccountSession(accountsDir) || fallback);`,
				`}`,
				`EOF`,
				`}`,
			}, "\n"),
			`sync_openclaw_session_key() {`,
			`  selected_session="$(resolve_openclaw_tui_session)"`,
			`  [ -n "$selected_session" ] || selected_session="main"`,
			`  export OPENCLAW_SESSION_KEY="agent:main:${selected_session}"`,
			`}`,
			`cicy_ts() {`,
			`  date '+%H:%M:%S'`,
			`}`,
			`cicy_log() {`,
			`  printf '[%s] [cicy] %s\n' "$(cicy_ts)" "$*"`,
			`}`,
			`cicy_sep() {`,
			`  cicy_log '=================================================='`,
			`}`,
			`cicy_pause() {`,
			`  printf '\n'`,
			`  read -r -p '按 Enter 返回管理台...' _cicy_pause || true`,
			`}`,
			strings.Join([]string{
				"latest_channel_session_env() {",
				`  node - "$openclaw_sessions_store" "$1" <<'EOF'`,
				`const fs = require("fs");`,
				`const storePath = process.argv[2];`,
				`const wanted = String(process.argv[3] || "").trim().toLowerCase();`,
				`function shellQuote(value) {`,
				`  return "'" + String(value ?? "").replace(/'/g, "'\"'\"'") + "'";`,
				`}`,
				`function detectChannel(entry) {`,
				`  return String((((entry || {}).deliveryContext || {}).channel) || entry.lastChannel || (((entry || {}).origin || {}).provider) || "").trim().toLowerCase();`,
				`}`,
				`function detectLabel(key, entry) {`,
				`  const origin = (entry && entry.origin) || {};`,
				`  const delivery = (entry && entry.deliveryContext) || {};`,
				`  return String(origin.label || delivery.to || origin.to || key.replace(/^agent:[^:]+:/, "")).trim();`,
				`}`,
				`function shorten(value) {`,
				`  const text = String(value || "").trim();`,
				`  if (!text) return "";`,
				`  if (text.length <= 48) return text;`,
				`  return text.slice(0, 22) + "..." + text.slice(-18);`,
				`}`,
				`function displayName(key, entry) {`,
				`  const channel = detectChannel(entry);`,
				`  const origin = (entry && entry.origin) || {};`,
				`  const chatType = String(origin.chatType || entry.chatType || "").trim().toLowerCase();`,
				`  const channelLabelMap = { "openclaw-weixin": "微信", feishu: "飞书", webchat: "网页", telegram: "Telegram", slack: "Slack" };`,
				`  const chatTypeMap = { direct: "单聊", group: "群聊", channel: "频道", thread: "话题" };`,
				`  const channelLabel = channelLabelMap[channel] || channel || "会话";`,
				`  const chatTypeLabel = chatTypeMap[chatType] || "";`,
				`  const label = shorten(detectLabel(key, entry)) || "未命名会话";`,
				`  return channelLabel + (chatTypeLabel ? " " + chatTypeLabel : "") + " | " + label;`,
				`}`,
				`try {`,
				`  if (!wanted || !storePath || !fs.existsSync(storePath)) process.exit(0);`,
				`  const raw = JSON.parse(fs.readFileSync(storePath, "utf8"));`,
				`  const matches = Object.entries(raw || {})`,
				`    .map(([key, entry]) => ({ key, entry, updatedAt: Number((entry && entry.updatedAt) || 0) }))`,
				`    .filter((item) => detectChannel(item.entry) === wanted)`,
				`    .sort((a, b) => b.updatedAt - a.updatedAt || a.key.localeCompare(b.key));`,
				`  const current = matches[0];`,
				`  if (!current) process.exit(0);`,
				`  const delivery = (current.entry && current.entry.deliveryContext) || {};`,
				`  const origin = (current.entry && current.entry.origin) || {};`,
				`  process.stdout.write("session_key=" + shellQuote(current.key) + "\n");`,
				`  process.stdout.write("session_name=" + shellQuote(current.key.replace(/^agent:[^:]+:/, "")) + "\n");`,
				`  process.stdout.write("session_display=" + shellQuote(displayName(current.key, current.entry)) + "\n");`,
				`  process.stdout.write("session_target=" + shellQuote(String(delivery.to || current.entry.lastTo || origin.to || "")) + "\n");`,
				`  process.stdout.write("session_account_id=" + shellQuote(String(delivery.accountId || current.entry.lastAccountId || origin.accountId || "")) + "\n");`,
				`  process.stdout.write("session_thread_id=" + shellQuote(String(delivery.threadId || current.entry.lastThreadId || origin.threadId || "")) + "\n");`,
				`  process.stdout.write("session_updated_at=" + shellQuote(String(current.updatedAt || "")) + "\n");`,
				`  process.stdout.write("session_label=" + shellQuote(String(origin.label || "")) + "\n");`,
				`} catch (_) {}`,
				`EOF`,
				`}`,
			}, "\n"),
			strings.Join([]string{
				"current_session_display_name() {",
				`  node - "$openclaw_sessions_store" "$selected_session" <<'EOF'`,
				`const fs = require("fs");`,
				`const storePath = process.argv[2];`,
				`const selected = String(process.argv[3] || "").trim();`,
				`function detectChannel(entry) {`,
				`  return String((((entry || {}).deliveryContext || {}).channel) || entry.lastChannel || (((entry || {}).origin || {}).provider) || "").trim().toLowerCase();`,
				`}`,
				`function detectLabel(key, entry) {`,
				`  const origin = (entry && entry.origin) || {};`,
				`  const delivery = (entry && entry.deliveryContext) || {};`,
				`  return String(origin.label || delivery.to || origin.to || key.replace(/^agent:[^:]+:/, "")).trim();`,
				`}`,
				`function shorten(value) {`,
				`  const text = String(value || "").trim();`,
				`  if (!text) return "";`,
				`  if (text.length <= 48) return text;`,
				`  return text.slice(0, 22) + "..." + text.slice(-18);`,
				`}`,
				`function displayName(key, entry) {`,
				`  if (!entry) return selected === "main" ? "主会话" : selected || "主会话";`,
				`  const channel = detectChannel(entry);`,
				`  const origin = (entry && entry.origin) || {};`,
				`  const chatType = String(origin.chatType || entry.chatType || "").trim().toLowerCase();`,
				`  const channelLabelMap = { "openclaw-weixin": "微信", feishu: "飞书", webchat: "网页", telegram: "Telegram", slack: "Slack" };`,
				`  const chatTypeMap = { direct: "单聊", group: "群聊", channel: "频道", thread: "话题" };`,
				`  const channelLabel = channelLabelMap[channel] || channel || "会话";`,
				`  const chatTypeLabel = chatTypeMap[chatType] || "";`,
				`  const label = shorten(detectLabel(key, entry)) || "未命名会话";`,
				`  return channelLabel + (chatTypeLabel ? " " + chatTypeLabel : "") + " | " + label;`,
				`}`,
				`try {`,
				`  if (!storePath || !fs.existsSync(storePath)) {`,
				`    process.stdout.write(selected === "main" ? "主会话" : (selected || "主会话"));`,
				`    process.exit(0);`,
				`  }`,
				`  const raw = JSON.parse(fs.readFileSync(storePath, "utf8"));`,
				`  const key = selected.startsWith("agent:") ? selected : ("agent:main:" + (selected || "main"));`,
				`  process.stdout.write(displayName(key, raw[key] || null));`,
				`} catch (_) {`,
				`  process.stdout.write(selected === "main" ? "主会话" : (selected || "主会话"));`,
				`}`,
				`EOF`,
				`}`,
			}, "\n"),
			strings.Join([]string{
				"openclaw_send_channel_message() {",
				`  send_channel="$1"`,
				`  send_message="$2"`,
				`  eval "$(latest_channel_session_env "$send_channel")"`,
				`  if [ -z "$session_key" ] || [ -z "$session_target" ]; then`,
				`    cicy_log "${send_channel} 当前没有可发送的活跃会话。"`,
				`    return 1`,
				`  fi`,
				`  send_params="$(node - "$send_channel" "$session_target" "$session_account_id" "$session_thread_id" "$session_key" "$send_message" <<'EOF'`,
				`const [channel, to, accountId, threadId, sessionKey, message] = process.argv.slice(2);`,
				`const payload = {`,
				`  channel,`,
				`  to,`,
				`  message,`,
				`  sessionKey,`,
				`  idempotencyKey: "cicy-" + Date.now() + "-" + Math.random().toString(16).slice(2),`,
				`};`,
				`if (accountId) payload.accountId = accountId;`,
				`if (threadId) payload.threadId = threadId;`,
				`process.stdout.write(JSON.stringify(payload));`,
				`EOF`,
				`  )"`,
				`  send_out="$("${openclaw_profile_cmd[@]}" gateway call send --json --params "$send_params" 2>&1 || true)"`,
				`  send_ok="$(printf '%s' "$send_out" | node -e 'let data=""; process.stdin.on("data",(chunk)=>data+=chunk).on("end",()=>{ try { const parsed = JSON.parse(data); process.stdout.write(parsed && parsed.messageId ? "1" : "0"); } catch (_) { process.stdout.write("0"); } });')"`,
				`  if [ "$send_ok" = "1" ]; then`,
				`    return 0`,
				`  fi`,
				`  cicy_log "${send_channel} 消息发送失败: $send_out"`,
				`  return 1`,
				`}`,
			}, "\n"),
			`sync_openclaw_session_key`,
			`cicy_log "已准备 OpenClaw 会话: $(current_session_display_name)"`,
			fmt.Sprintf(`openclaw_gateway_ready() {
  (exec 3<>/dev/tcp/127.0.0.1/%s) >/dev/null 2>&1
}`, openClawPort()),
			fmt.Sprintf(`gateway_log=%s`, tmuxShellQuote(tmpGatewayLog)),
			`openclaw_gateway_running() {
  pgrep -x openclaw-gateway >/dev/null 2>&1
}`,
			`restart_openclaw_gateway_for_session() {
  if ! openclaw_gateway_running; then
    return 0
  fi
  cicy_log "正在按会话重启 OpenClaw gateway: $selected_session"
  pkill -f 'openclaw-gateway|openclaw gateway run' >/dev/null 2>&1 || true
  for i in $(seq 1 30); do
    if ! openclaw_gateway_running; then
      return 0
    fi
    sleep 1
  done
  cicy_log "gateway 在 30 秒后仍未完全退出，继续后续流程。"
}`,
			`ensure_openclaw_gateway() {
  if openclaw_gateway_ready; then
    return 0
  fi
  cicy_log "正在准备 OpenClaw gateway ..."
  if ! openclaw_gateway_running; then
    cicy_log "正在启动 gateway 进程 ..."
    rm -f "$gateway_log"
    : > "$gateway_log"
    rm -f `+tmuxShellQuote(gatewayLog)+`
    nohup env CIAO_DISABLE=1 "${openclaw_profile_cmd[@]}" gateway run --verbose >"$gateway_log" 2>&1 </dev/null &
  else
    cicy_log "检测到已有 gateway 进程，等待就绪 ..."
  fi
  gateway_ready=0
  gateway_conflict_seen=0
  last_log_seen=''
  for i in $(seq 1 120); do
    if openclaw_gateway_ready; then
      gateway_ready=1
      cicy_log "OpenClaw gateway 已就绪。"
      break
    fi
    if [ -s "$gateway_log" ]; then
      last_log=$(tail -1 "$gateway_log" 2>/dev/null | sed 's/[^[:print:]\t]//g')
      if [ -n "$last_log" ] && [ "$last_log" != "$last_log_seen" ]; then
        cicy_log "gateway 日志 :: $last_log"
        last_log_seen="$last_log"
      fi
      if [ "$gateway_conflict_seen" -ne 1 ] && grep -Eq `+tmuxShellQuote(`another gateway instance is already listening on ws://127\.0\.0\.1:`+openClawPort()+`|Port `+openClawPort()+` is already in use`)+` "$gateway_log"; then
        cicy_log "gateway 端口已被占用，等待现有实例就绪 ..."
        gateway_conflict_seen=1
      fi
    fi
    if [ $((i % 5)) -eq 0 ]; then
      cicy_log "等待 gateway 启动 ${i}s/120s ..."
    fi
    sleep 1
  done
  if [ "$gateway_ready" -ne 1 ]; then
    cicy_log "OpenClaw gateway 启动失败，最近日志如下："
    tail -80 "$gateway_log"
    return 1
  fi
}`,
		)
		if shortID != primaryWorkerSession {
			lines = append(lines, `ensure_openclaw_gateway`)
		}
		if normalizeAgentType(agentType) == "openclaw" {
			lines = append(lines,
				`weixin_needs_login() {
  node - "$weixin_accounts_path" <<'EOF'
const fs = require("fs");
const file = process.argv[2];
try {
  if (!file || !fs.existsSync(file)) process.exit(0);
  const raw = fs.readFileSync(file, "utf8").trim();
  if (!raw) process.exit(0);
  const parsed = JSON.parse(raw);
  if (!Array.isArray(parsed) || parsed.length === 0) process.exit(0);
  const hasAccount = parsed.some((item) => typeof item === "string" && item.trim() !== "");
  process.exit(hasAccount ? 1 : 0);
} catch (_) {
  process.exit(0);
}
EOF
}`,
				strings.Join([]string{
					"weixin_latest_account_env() {",
					`  node - "$weixin_accounts_dir" <<'EOF'`,
					`const fs = require("fs");`,
					`const path = require("path");`,
					`const dir = process.argv[2];`,
					`try {`,
					`  if (!dir || !fs.existsSync(dir)) process.exit(0);`,
					`  const files = fs.readdirSync(dir)`,
					`    .filter((name) => name.endsWith(".json") && !name.endsWith(".sync.json") && !name.endsWith(".context-tokens.json"))`,
					`    .sort();`,
					`  if (!files.length) process.exit(0);`,
					`  const file = path.join(dir, files[files.length - 1]);`,
					`  const raw = JSON.parse(fs.readFileSync(file, "utf8"));`,
					`  const accountId = file.split("/").pop().replace(/\.json$/, "");`,
					`  const userId = String((raw && raw.userId) || "").trim();`,
					`  const savedAt = String((raw && raw.savedAt) || "").trim();`,
					`  if (!accountId || !userId || !savedAt) process.exit(0);`,
					`  process.stdout.write("account_id='" + accountId + "'\n");`,
					`  process.stdout.write("user_id='" + userId + "'\n");`,
					`  process.stdout.write("saved_at='" + savedAt + "'\n");`,
					`} catch (_) {}`,
					`EOF`,
					"}",
				}, "\n"),
				strings.Join([]string{
					"weixin_wait_until_ready() {",
					`  eval "$(weixin_latest_account_env)"`,
					`  [ -n "$account_id" ] || return 0`,
					`  cicy_log "正在等待微信进入可交互状态 ..."`,
					`  provider_seen=0`,
					`  for i in $(seq 1 120); do`,
					`    if [ "$provider_seen" -ne 1 ] && grep -Fq "[$account_id] starting weixin provider" "$gateway_log" 2>/dev/null; then`,
					`      cicy_log "微信 provider 正在启动 ..."`,
					`      provider_seen=1`,
					`    fi`,
					`    if grep -Fq "weixin monitor started" "$gateway_log" 2>/dev/null && grep -Fq "account=${account_id}" "$gateway_log" 2>/dev/null; then`,
					`      cicy_log "微信监控已上线。"`,
					`      return 0`,
					`    fi`,
					`    if [ $((i % 5)) -eq 0 ]; then`,
					`      cicy_log "等待微信可交互 ${i}s/120s ..."`,
					`    fi`,
					`    sleep 1`,
					`  done`,
					`  cicy_log "微信在 120 秒后仍未完成预热。"`,
					`  return 1`,
					"}",
				}, "\n"),
				fmt.Sprintf(`weixin_send_welcome() {
  eval "$(weixin_latest_account_env)"
  [ -n "$account_id" ] || return 0
  stamp="${account_id}|${saved_at}"
  if [ -f "$weixin_welcome_state" ] && [ "$(cat "$weixin_welcome_state" 2>/dev/null)" = "$stamp" ]; then
    return 0
  fi
  welcome_text="你好，我是小龙虾管家，有什么可以效劳的吗？"
  send_out="$("${openclaw_profile_cmd[@]}" message send --channel openclaw-weixin --target "$user_id" --message "$welcome_text" 2>&1 || true)"
  if printf '%%s' "$send_out" | grep -Fq "Sent via"; then
    printf '%%s' "$stamp" > "$weixin_welcome_state"
    cicy_log "欢迎消息已发送到 $user_id"
    return 0
  fi
  cicy_log "欢迎消息发送失败: $send_out"
  return 1
}`),
				strings.Join([]string{
					"feishu_has_logged_account() {",
					`  status_json="$("${openclaw_profile_cmd[@]}" channels status --json --probe --timeout 5000 2>/dev/null || true)"`,
					`  node - "$status_json" <<'EOF'`,
					`const raw = process.argv[2] || "{}";`,
					`try {`,
					`  const parsed = JSON.parse(raw);`,
					`  const accounts = parsed?.channelAccounts?.feishu;`,
					`  if (!Array.isArray(accounts) || accounts.length === 0) process.exit(1);`,
					`  const ok = accounts.some((item) => item && item.configured !== false && item.enabled !== false);`,
					`  process.exit(ok ? 0 : 1);`,
					`} catch (_) {`,
					`  process.exit(1);`,
					`}`,
					`EOF`,
					"}",
				}, "\n"),
				strings.Join([]string{
					"feishu_wait_until_ready() {",
					`  if ! feishu_has_logged_account; then`,
					`    return 0`,
					`  fi`,
					`  cicy_log "检测到飞书已登录，正在同时拉起飞书 ..."`,
					`  for i in $(seq 1 60); do`,
					`    status_json="$("${openclaw_profile_cmd[@]}" channels status --json --probe --timeout 5000 2>/dev/null || true)"`,
					`    if node - "$status_json" <<'EOF'`,
					`const raw = process.argv[2] || "{}";`,
					`try {`,
					`  const parsed = JSON.parse(raw);`,
					`  const accounts = parsed?.channelAccounts?.feishu;`,
					`  if (!Array.isArray(accounts) || accounts.length === 0) process.exit(1);`,
					`  const ok = accounts.some((item) => item && item.running === true && item.configured !== false && item.enabled !== false);`,
					`  process.exit(ok ? 0 : 1);`,
					`} catch (_) {`,
					`  process.exit(1);`,
					`}`,
					`EOF`,
					`    then`,
					`      cicy_log "飞书已上线。"`,
					`      return 0`,
					`    fi`,
					`    if [ $((i % 5)) -eq 0 ]; then`,
					`      cicy_log "等待飞书可交互 ${i}s/60s ..."`,
					`    fi`,
					`    sleep 1`,
					`  done`,
					`  cicy_log "飞书尚未完成启动，稍后会继续自动重连。"`,
					`  return 1`,
					"}",
				}, "\n"),
				`boot_completed=0`,
				`just_booted=0`,
				`refresh_openclaw_session() {`,
				`  previous_selected_session="$selected_session"`,
				`  session_changed=0`,
				`  sync_openclaw_session_key`,
				`  if [ "$selected_session" != "$previous_selected_session" ]; then`,
				`    session_changed=1`,
				`    cicy_log "OpenClaw 会话已切换: $previous_selected_session -> $selected_session"`,
				`  fi`,
				`}`,
				`weixin_login_and_start() {`,
				`  login_performed=0`,
				`  if weixin_needs_login; then`,
				`    cicy_log "未检测到微信账号，开始二维码登录 ..."`,
				`    if ! "${openclaw_profile_cmd[@]}" channels login --channel openclaw-weixin; then`,
				`      cicy_log "微信二维码登录未完成。"`,
				`      return 1`,
				`    fi`,
				`    login_performed=1`,
				`  else`,
				`    cicy_log "已检测到现有微信账号，直接启动服务。"`,
				`  fi`,
				`  refresh_openclaw_session`,
				`  if [ "$login_performed" = "1" ] || [ "$session_changed" = "1" ]; then`,
				`    restart_openclaw_gateway_for_session`,
				`  fi`,
				`  ensure_openclaw_gateway || return 1`,
				`  if weixin_wait_until_ready; then`,
				`    feishu_wait_until_ready || true`,
				`    if [ "$login_performed" = "1" ]; then`,
				`      weixin_send_welcome || true`,
				`    fi`,
				`    cicy_log "您的微信已成功连通，请在微信发指令给我！"`,
				`    boot_completed=1`,
				`    just_booted=1`,
				`    return 0`,
				`  fi`,
				`  cicy_log "微信仍在预热，请稍后通过微信重试。"`,
				`  return 1`,
				`}`,
				`show_boot_screen() {`,
				`  clear`,
				`  cicy_sep`,
				`  cicy_log "小龙虾管家"`,
				`  cicy_log "你好，我是小龙虾管家。"`,
				`  cicy_log "点 Enter 扫码登录微信。"`,
				`  cicy_log "登录成功后，我会自动连通微信并进入管理菜单。"`,
				`  cicy_sep`,
				`}`,
				`show_openclaw_status() {`,
				`  cicy_log "正在查看 OpenClaw 状态 ..."`,
				`  "${openclaw_profile_cmd[@]}" status || true`,
				`  printf '\n'`,
				`  cicy_log "正在查看 Gateway 探测 ..."`,
				`  "${openclaw_profile_cmd[@]}" gateway probe || true`,
				`  printf '\n'`,
				`  cicy_log "正在查看 Channel 状态 ..."`,
				`  "${openclaw_profile_cmd[@]}" channels status --probe || true`,
				`}`,
				`restart_gateway_action() {`,
				`  refresh_openclaw_session`,
				`  restart_openclaw_gateway_for_session`,
				`  ensure_openclaw_gateway || return 1`,
				`  weixin_wait_until_ready || true`,
				`  feishu_wait_until_ready || true`,
				`}`,
				`show_dashboard_url() {`,
				`  cicy_log "OpenClaw Dashboard 地址如下："`,
				`  "${openclaw_profile_cmd[@]}" dashboard --no-open || true`,
				`}`,
				`install_plugin_action() {`,
				`  read -r -p '请输入插件 spec: ' plugin_spec || true`,
				`  plugin_spec="$(printf '%s' "$plugin_spec" | xargs)"`,
				`  if [ -z "$plugin_spec" ]; then`,
				`    cicy_log "未输入插件 spec，已取消。"`,
				`    return 0`,
				`  fi`,
				`  cicy_log "正在安装插件: $plugin_spec"`,
				`  if "${openclaw_profile_cmd[@]}" plugins install "$plugin_spec"; then`,
				`    cicy_log "插件安装完成。"`,
				`    restart_gateway_action || true`,
				`  else`,
				`    cicy_log "插件安装失败。"`,
				`    return 1`,
				`  fi`,
				`}`,
				`install_skill_action() {`,
				`  read -r -p '请输入 skill slug: ' skill_slug || true`,
				`  skill_slug="$(printf '%s' "$skill_slug" | xargs)"`,
				`  if [ -z "$skill_slug" ]; then`,
				`    cicy_log "未输入 skill slug，已取消。"`,
				`    return 0`,
				`  fi`,
				`  cicy_log "正在安装 skill: $skill_slug"`,
				`  "${openclaw_profile_cmd[@]}" skills install "$skill_slug" || return 1`,
				`  cicy_log "skill 安装完成。"`,
				`}`,
				`normalize_feishu_config() {`,
				`  node - "$OPENCLAW_CONFIG_PATH" <<'EOF'`,
				`const fs = require("fs");`,
				`const path = process.argv[2];`,
				`if (!path || !fs.existsSync(path)) process.exit(0);`,
				`const cfg = JSON.parse(fs.readFileSync(path, "utf8"));`,
				`cfg.plugins ||= {};`,
				`cfg.plugins.entries ||= {};`,
				`cfg.plugins.entries["openclaw-lark"] = { ...(cfg.plugins.entries["openclaw-lark"] || {}), enabled: true };`,
				`delete cfg.plugins.entries["feishu"];`,
				`delete cfg.plugins.entries["feishu-openclaw-plugin"];`,
				`cfg.channels ||= {};`,
				`cfg.channels.feishu = { ...(cfg.channels.feishu || {}), enabled: true };`,
				`const allow = Array.isArray(cfg.plugins.allow) ? cfg.plugins.allow : [];`,
				`const allowSet = new Set(allow.filter((item) => typeof item === "string" && item.trim()));`,
				`allowSet.delete("feishu");`,
				`allowSet.delete("feishu-openclaw-plugin");`,
				`allowSet.add("openclaw-lark");`,
				`allowSet.add("openclaw-weixin");`,
				`cfg.plugins.allow = Array.from(allowSet);`,
				`fs.writeFileSync(path, JSON.stringify(cfg, null, 2));`,
				`EOF`,
				`}`,
				`install_feishu_action() {`,
				`  cicy_log "即将安装飞书插件，过程中可能需要扫码和确认。"`,
				`  cicy_log "正在执行飞书官方安装命令 ..."`,
				`  if npx -y https://sf3-cn.feishucdn.com/obj/open-platform-opendoc/8ab6e7a04c17db1becfcbda8ca35f091_1rCCFRWlRV.tgz install; then`,
				`    normalize_feishu_config`,
				`    cicy_log "飞书安装完成。"`,
				`    cicy_log "正在重启 gateway 以加载飞书配置 ..."`,
				`    restart_gateway_action || true`,
				`    cicy_log "验证方式：在飞书里发送 /feishu start"`,
				`    cicy_log "如需授权更多飞书能力，可发送 /feishu auth"`,
				fmt.Sprintf(`    cicy_log "如需开启流式输出，可运行：openclaw --profile %s config set channels.feishu.streaming true"`, shortID),
				`  else`,
				`    cicy_log "飞书安装失败。"`,
				`    return 1`,
				`  fi`,
				`}`,
				`add_channel_action() {`,
				`  printf '1. 交互登录型 channel\n'`,
				`  printf '2. 参数添加型 channel\n'`,
				`  read -r -p '请选择添加方式: ' add_mode || true`,
				`  case "$add_mode" in`,
				`    1)`,
				`      read -r -p '请输入 channel 名称: ' channel_name || true`,
				`      channel_name="$(printf '%s' "$channel_name" | xargs)"`,
				`      [ -n "$channel_name" ] || { cicy_log "未输入 channel 名称，已取消。"; return 0; }`,
				`      cicy_log "开始登录 channel: $channel_name"`,
				`      "${openclaw_profile_cmd[@]}" channels login --channel "$channel_name" || return 1`,
				`      refresh_openclaw_session`,
				`      ensure_openclaw_gateway || true`,
				`      [ "$channel_name" = "openclaw-weixin" ] && weixin_wait_until_ready || true`,
				`      ;;`,
				`    2)`,
				`      cicy_log "即将显示 openclaw channels add 帮助。"`,
				`      "${openclaw_profile_cmd[@]}" channels add --help || true`,
				`      printf '\n'`,
				`      read -r -p '请输入附加参数（例如 --channel telegram --token xxx）: ' channel_args || true`,
				`      channel_args="$(printf '%s' "$channel_args" | xargs)"`,
				`      [ -n "$channel_args" ] || { cicy_log "未输入参数，已取消。"; return 0; }`,
				`      eval "set -- $channel_args" || return 1`,
				`      "${openclaw_profile_cmd[@]}" channels add "$@" || return 1`,
				`      refresh_openclaw_session`,
				`      ensure_openclaw_gateway || true`,
				`      ;;`,
				`    *)`,
				`      cicy_log "无效选择，已取消。"`,
				`      ;;`,
				`  esac`,
				`}`,
				`tail_recent_logs_action() {`,
				`  cicy_log "最近 gateway 日志如下："`,
				`  tail -n 120 "$gateway_log" 2>/dev/null || true`,
				`  printf '\n'`,
				`  cicy_log "最近 channel 日志如下："`,
				`  "${openclaw_profile_cmd[@]}" channels logs || true`,
				`}`,
				`send_weixin_test_message_action() {`,
				`  eval "$(weixin_latest_account_env)"`,
				`  if [ -z "$user_id" ]; then`,
				`    cicy_log "未检测到当前微信用户，无法发送测试消息。"`,
				`    return 1`,
				`  fi`,
				`  read -r -p '请输入要发送的消息（留空则发送默认文案）: ' test_message || true`,
				`  if [ -z "$test_message" ]; then`,
				`    test_message='你好，我是 CiCy 管理台测试消息。'`,
				`  fi`,
				`  cicy_log "正在向当前微信发送测试消息 ..."`,
				`  "${openclaw_profile_cmd[@]}" message send --channel openclaw-weixin --target "$user_id" --message "$test_message" || return 1`,
				`  cicy_log "测试消息已发送。"`,
				`}`,
				`show_management_menu() {`,
				`  if [ "$just_booted" = "1" ]; then`,
				`    just_booted=0`,
				`    printf '\n'`,
				`  else`,
				`    clear`,
				`  fi`,
				`  refresh_openclaw_session`,
				`  if openclaw_gateway_ready; then`,
				`    gateway_state='在线'`,
				`  else`,
				`    gateway_state='离线'`,
				`  fi`,
				`  cicy_sep`,
				`  cicy_log "小龙虾管家"`,
				`  cicy_log "当前会话: $selected_session"`,
				`  cicy_log "Gateway 状态: $gateway_state"`,
				`  cicy_log "配置文件: $OPENCLAW_CONFIG_PATH"`,
				`  cicy_sep`,
				`  printf '1. 重新连接微信并启动服务\n'`,
				`  printf '2. 查看 OpenClaw 状态\n'`,
				`  printf '3. 重启 Gateway\n'`,
				`  printf '4. 查看 Dashboard 地址\n'`,
				`  printf '5. 安装插件\n'`,
				`  printf '6. 安装 Skill\n'`,
				`  printf '7. 安装飞书\n'`,
				`  printf '8. 添加 Channel\n'`,
				`  printf '9. 查看最近日志\n'`,
				`  printf '10. 向当前微信发送测试消息\n'`,
				`  printf '0. 刷新管理台\n'`,
				`}`,
				// Split pane: left=TUI, right=weixin login
				`_cicy_tmux_session="$(tmux display-message -p '#{session_name}')"`,
				`sync_openclaw_session_key`,
				`ensure_openclaw_gateway || true`,
				`if weixin_needs_login; then`,
				`  cicy_log "微信未登录，正在右侧打开登录窗口..."`,
				`  tmux split-window -h -t "$_cicy_tmux_session" "OPENCLAW_STATE_DIR='$OPENCLAW_STATE_DIR' OPENCLAW_CONFIG_PATH='$OPENCLAW_CONFIG_PATH' ${openclaw_profile_cmd[*]} channels login --channel openclaw-weixin; sleep 2"`,
				`  _login_pane=$(tmux list-panes -t "$_cicy_tmux_session" -F '#{pane_id}' | tail -1)`,
				`  while tmux list-panes -t "$_cicy_tmux_session" -F '#{pane_id}' 2>/dev/null | grep -q "$_login_pane"; do sleep 2; done`,
				`  refresh_openclaw_session`,
				`  restart_openclaw_gateway_for_session`,
				`  ensure_openclaw_gateway || true`,
				`  weixin_wait_until_ready || true`,
				`  weixin_send_welcome || true`,
				`  cicy_log "微信已连通"`,
				`else`,
				`  cicy_log "已检测到微信已登录"`,
				`  weixin_wait_until_ready || true`,
				`fi`,
				`sync_openclaw_session_key`,
				`cicy_log "正在打开 OpenClaw TUI 会话: $selected_session"`,
				`"${openclaw_profile_cmd[@]}" tui --session "$selected_session" || true`,
				`cicy_log "OpenClaw TUI 已退出。手动重启: source boot.sh"`,
			)
		} else {
			lines = append(lines,
				`previous_selected_session="$selected_session"`,
				`sync_openclaw_session_key`,
				`if [ "$selected_session" != "$previous_selected_session" ]; then`,
				`  cicy_log "OpenClaw 会话已切换: $previous_selected_session -> $selected_session"`,
				`  restart_openclaw_gateway_for_session`,
				`fi`,
				`ensure_openclaw_gateway`,
				`cicy_log "正在打开 OpenClaw TUI 会话: $selected_session"`,
				`"${openclaw_profile_cmd[@]}" tui --session "$selected_session" || true`,
				`cicy_log "OpenClaw TUI 已退出，Shell 仍保持活动。"`,
			)
		}
		return lines
	case "codex":
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("codex-install-%s.log", shortID))
		baseURL := openAIRuntimeBaseURL(shortID)
		providerOverride := tmuxShellQuote(`model_provider="custom"`)
		providerNameOverride := tmuxShellQuote(`model_providers.custom.name="cicy-local"`)
		baseURLOverride := tmuxShellQuote(`model_providers.custom.base_url="` + baseURL + `"`)
		lines := []string{
			"export OPENAI_API_KEY='cicy-local-gateway'",
			ensureAgentCommandLine("codex", "Codex", codexInstallCmd(), installLog),
		}
		if allowAllActions {
			lines = append(lines, fmt.Sprintf("codex -c %s -c %s -c %s --dangerously-bypass-approvals-and-sandbox", providerOverride, providerNameOverride, baseURLOverride))
			return lines
		}
		lines = append(lines, fmt.Sprintf("codex -c %s -c %s -c %s", providerOverride, providerNameOverride, baseURLOverride))
		return lines
	case "claude", "cicy-claude":
		cmdName := "claude"
		label := "Claude Code"
		installCmd := claudeInstallCmd()
		settingsFile := "claude-settings.json"
		if normalizeAgentType(agentType) == "cicy-claude" {
			cmdName = "cicy-claude"
			label = "CiCy"
			installCmd = cicyInstallCmd()
			settingsFile = "cicy-settings.json"
		}
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("%s-install-%s.log", cmdName, shortID))
		baseURL := anthropicRuntimeBaseURL(shortID)
		model := aiCfg.DefaultClaudeModel
		settingsJSON := fmt.Sprintf(`{"env":{"ANTHROPIC_AUTH_TOKEN":"cicy-local-gateway","ANTHROPIC_BASE_URL":"%s"},"model":"%s"}`, baseURL, model)
		lines := []string{
			ensureAgentCommandLine(cmdName, label, installCmd, installLog),
			fmt.Sprintf(`printf '%%s' %s > "$WORKSPACE/%s"`, tmuxShellQuote(settingsJSON), settingsFile),
		}
		if allowAllActions {
			lines = append(lines, fmt.Sprintf(`%s --settings "$WORKSPACE/%s" --permission-mode bypassPermissions`, cmdName, settingsFile))
		} else {
			lines = append(lines, fmt.Sprintf(`%s --settings "$WORKSPACE/%s"`, cmdName, settingsFile))
		}
		return lines
	case "opencode":
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("opencode-install-%s.log", shortID))
		runCmd := "opencode"
		if allowAllActions {
			runCmd = "opencode --dangerously-skip-permissions"
		}
		lines := []string{
			ensureAgentCommandLine("opencode", "OpenCode", opencodeInstallCmd(), installLog),
			fmt.Sprintf("export CICY_OPENAI_BASE_URL=%s", tmuxShellQuote(openAIRuntimeBaseURL(shortID))),
			`export OPENCODE_CONFIG="$WORKSPACE/.opencode/opencode.json"`,
			`export OPENCODE_CONFIG_ROOT="$WORKSPACE/.opencode/xdg"`,
			`export CICY_OPENCODE_MARKER="$WORKSPACE/.opencode/running"`,
			`rm -f "$CICY_OPENCODE_MARKER"`,
			`mkdir -p "$WORKSPACE/.opencode" "$OPENCODE_CONFIG_ROOT"`,
			`cat > "$OPENCODE_CONFIG" <<EOF
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "cicyai": {
      "npm": "@ai-sdk/openai-compatible",
      "api": "openai",
      "name": "cicyAi Gateway",
      "options": {
        "baseURL": "${CICY_OPENAI_BASE_URL}"
      }
    }
  }
}
EOF`,
		}
		lines = append(lines, fmt.Sprintf(`cicy_run_opencode() {
  XDG_CONFIG_HOME="$OPENCODE_CONFIG_ROOT" OPENCODE_CONFIG="$OPENCODE_CONFIG" %s
}`, runCmd))
		lines = append(lines, `cicy_start_opencode() {
  if [ -f "$CICY_OPENCODE_MARKER" ]; then
    echo '[cicy] OpenCode is already starting or running.'
    return 0
  fi
  : > "$CICY_OPENCODE_MARKER"
  cicy_run_opencode
  status=$?
  rm -f "$CICY_OPENCODE_MARKER"
  echo '[cicy] OpenCode exited. Run cicy_start_opencode to relaunch.'
  return "$status"
}`)
		lines = append(lines,
			`cicy_run_opencode`,
		)
		return lines
	case "kiro-cli":
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("kiro-install-%s.log", shortID))
		lines := append(kiroCliBootHelperLines(),
			ensureAgentCommandLineLive("kiro-cli", "Kiro CLI", "__cicy_local_install_kiro", installLog),
			`if kiro-cli whoami 2>/dev/null | grep -q "^Not logged in"; then
  while true; do
    echo ''
    echo '[cicy] Kiro CLI 尚未登录，请选择账号类型：'
    echo '  1. 免费版 (Builder ID / Google / Github)'
    echo '  2. 专业版 (Identity Center)'
    echo '  0. 跳过登录'
    read -r -p '请选择 [0/1/2]: ' kiro_choice
    case "$kiro_choice" in
      1) kiro-cli login --license free --use-device-flow && break ;;
      2) kiro-cli login --license pro --use-device-flow && break ;;
      0) echo '[cicy] 已跳过登录。手动登录: source boot.sh'; break ;;
      *) echo '[cicy] 无效选择，请重新输入' ;;
    esac
    echo '[cicy] 登录失败或已取消，可重新选择'
  done
fi`,
		)
		if allowAllActions {
			lines = append(lines, "kiro-cli chat --trust-all-tools")
		} else {
			lines = append(lines, "kiro-cli chat")
		}
		return lines
	case "copilot":
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("copilot-install-%s.log", shortID))
		lines := []string{
			"mkdir -p ~/.copilot",
			ensureAgentCommandLine("copilot", "GitHub Copilot", copilotInstallCmd(), installLog),
			`node -e 'const fs=require("fs"),f=process.env.HOME+"/.copilot/config.json";let c={};try{c=JSON.parse(fs.readFileSync(f))}catch(_){}c.trustedFolders=c.trustedFolders||[];const w=process.env.WORKSPACE||".";if(!c.trustedFolders.includes(w))c.trustedFolders.push(w);fs.writeFileSync(f,JSON.stringify(c,null,2))'`,
			"copilot --yolo",
		}
		return lines
	case "cicy-wechat":
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("wechat-install-%s.log", shortID))
		lines := []string{
			ensureAgentCommandLine("cicy-wechat", "WeChat", cicyWechatInstallCmd(), installLog),
			`export DATA_DIR="$WORKSPACE/.cicy-wechat"`,
			"cicy-wechat",
		}
		return lines
	case "cicy-feishu":
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("feishu-install-%s.log", shortID))
		lines := []string{
			ensureAgentCommandLine("cicy-feishu", "Feishu", cicyFeishuInstallCmd(), installLog),
			"cicy-feishu",
		}
		return lines
	case "hermes":
		home, _ := os.UserHomeDir()
		installLog := filepath.Join(home, ".cicy", fmt.Sprintf("hermes-install-%s.log", shortID))
		hermesHome := filepath.Join(home, ".hermes-"+shortID)
		hermesInstallDir := filepath.Join(hermesHome, "hermes-agent")
		hermesBin := filepath.Join(hermesInstallDir, "venv", "bin", "hermes")
		configPath := filepath.Join(hermesHome, "config.yaml")
		installScriptPath := fmt.Sprintf("/tmp/hermes-install-%s.sh", shortID)
		modelName := "claude-opus-4-6"
		contextLength := 1000000
		lines := []string{
			fmt.Sprintf("export HERMES_HOME=%s", tmuxShellQuote(hermesHome)),
			fmt.Sprintf("export HERMES_INSTALL_DIR=%s", tmuxShellQuote(hermesInstallDir)),
			fmt.Sprintf("export CICY_HERMES_BIN=%s", tmuxShellQuote(hermesBin)),
			fmt.Sprintf("mkdir -p %s", tmuxShellQuote(hermesHome)),
			fmt.Sprintf(`if [ ! -x %s ]; then
  echo '[cicy] =================================================='
  echo '[cicy] Hermes Agent is not installed. Installing now...'
  echo '[cicy] This may take 1-5 minutes depending on network.'
  echo '[cicy] =================================================='
  install_log=%s
  curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh -o %s && HERMES_HOME="$HERMES_HOME" HERMES_INSTALL_DIR="$HERMES_INSTALL_DIR" bash %s --skip-setup >"$install_log" 2>&1
  install_status=$?
  if [ "$install_status" -ne 0 ]; then
    echo '[cicy] Hermes Agent install failed. Recent log:'
    tail -100 "$install_log"
    return 1
  fi
  echo '[cicy] Hermes Agent install completed.'
fi`, tmuxShellQuote(hermesBin), tmuxShellQuote(installLog), tmuxShellQuote(installScriptPath), tmuxShellQuote(installScriptPath)),
			fmt.Sprintf("export CICY_HERMES_CONFIG=%s", tmuxShellQuote(configPath)),
			fmt.Sprintf("export OPENAI_BASE_URL=%s", tmuxShellQuote(openAIRuntimeBaseURL(shortID))),
			fmt.Sprintf("export OPENAI_API_KEY=%s", tmuxShellQuote("cicy-local-gateway")),
			fmt.Sprintf("export CICY_HERMES_MODEL=%s", tmuxShellQuote(modelName)),
			`python3 - <<'EOF'
from pathlib import Path
import os
config_path = Path((os.environ.get("CICY_HERMES_CONFIG") or "").strip())
if not config_path:
    raise SystemExit(0)
config_path.parent.mkdir(parents=True, exist_ok=True)
model = (os.environ.get("CICY_HERMES_MODEL") or "claude-opus-4-6").strip() or "claude-opus-4-6"
base_url = (os.environ.get("OPENAI_BASE_URL") or "").strip()
api_key = (os.environ.get("OPENAI_API_KEY") or "").strip() or "cicy-local-gateway"
config_path.write_text(
    "model:\n"
    "  provider: custom\n"
    f"  default: {model}\n"
    f"  context_length: ` + strconv.Itoa(contextLength) + `\n"
    f"  base_url: {base_url}\n"
    f"  api_key: {api_key}\n"
    "compression:\n"
    "  enabled: false\n"
    "terminal:\n"
    "  cwd: auto\n",
    encoding="utf-8",
)
EOF`,
		}
		lines = append(lines, `"$CICY_HERMES_BIN"`)
		return lines
	default:
		return nil
	}
}

func isClaudeInputReady(out string) bool {
	if isClaudeThemePrompt(out) || isClaudeSecurityNotesPrompt(out) || isClaudeTrustPrompt(out) || isClaudeBypassChoicePrompt(out) || isClaudeBypassConfirmPrompt(out) {
		return false
	}
	if strings.Contains(out, "? for shortcuts") ||
		strings.Contains(out, " /effort") ||
		strings.Contains(out, "Welcome back!") ||
		strings.Contains(out, "Recent activity") {
		return true
	}
	for _, line := range normalizeNonEmptyMeaningfulLines(strings.Split(out, "\n")) {
		if strings.HasPrefix(strings.TrimSpace(line), "❯") {
			return true
		}
	}
	return false
}

func isClaudeThemePrompt(out string) bool {
	return (strings.Contains(out, "Choose the text style that looks best with your terminal") ||
		(strings.Contains(out, "Let's get started.") &&
			strings.Contains(out, "/theme") &&
			strings.Contains(out, "Dark mode"))) &&
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
		strings.Contains(out, "No, exit") &&
		strings.Contains(out, "Yes, I accept")
}

func isClaudeBypassConfirmPrompt(out string) bool {
	return strings.Contains(out, "Bypass Permissions mode") &&
		strings.Contains(out, "Enter to confirm") &&
		!strings.Contains(out, "No, exit")
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
	case allowAllActions && isClaudeBypassConfirmPrompt(out):
		return claudeStageBypassConfirm
	case allowAllActions && isClaudeBypassChoicePrompt(out):
		return claudeStageBypassChoice
	default:
		return claudeStageNone
	}
}

func isCodexTrustPrompt(out string) bool {
	return strings.Contains(out, "Do you trust the contents of this directory?") &&
		strings.Contains(out, "1. Yes, continue") &&
		strings.Contains(out, "Press enter to continue")
}

func isCodexUpdatePrompt(out string) bool {
	return (strings.Contains(out, "Update available!") || strings.Contains(out, "Update available:")) &&
		strings.Contains(out, "Skip until next version") &&
		strings.Contains(out, "Press enter to continue")
}

func isCodexBusyStateVisible(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "esc to interrupt") ||
		strings.Contains(lower, "working (") ||
		strings.Contains(lower, "messages to be submitted after next tool call")
}

func isCodexPromptVisible(out string) bool {
	return strings.Contains(out, "› ")
}

func isCodexStatusFooterVisible(out string) bool {
	return strings.Contains(out, "· ") &&
		strings.Contains(out, "% left") &&
		(strings.Contains(out, "~/") || strings.Contains(out, "/workers/"))
}

func isCodexInputReady(out string) bool {
	if isCodexTrustPrompt(out) {
		return false
	}
	if isCodexUpdatePrompt(out) {
		return false
	}
	// Newer Codex UI often no longer keeps the initial "OpenAI Codex (v...)"
	// header in the visible capture. Treat the active prompt + status footer as ready.
	if isCodexPromptVisible(out) && isCodexStatusFooterVisible(out) {
		return true
	}
	// Codex can accept queued prompts while it is still working. As long as the
	// prompt is visible and the terminal shows Codex's working/queue affordance,
	// allow send so the built-in queue can handle it.
	if isCodexPromptVisible(out) && isCodexBusyStateVisible(out) {
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
	return (strings.Contains(lower, "session agent:main:") ||
		strings.Contains(lower, "agent main | session ")) &&
		strings.Contains(lower, "connected |") &&
		!strings.Contains(lower, "connecting |")
}

func isAgentInputReady(agentType, out string) bool {
	switch normalizeAgentType(agentType) {
	case "claude":
		return isClaudeInputReady(out)
	case "cicy-claude":
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
		var workspace string
		if err := store.QueryRow("SELECT COALESCE(workspace, '') FROM agent_config WHERE pane_id=?", normPaneID(paneID)).Scan(&workspace); err == nil {
			workspace = strings.TrimSpace(workspace)
			if workspace != "" {
				return filepath.Join(workspace, ".opencode", "running")
			}
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, "workers", shortID, ".opencode", "running")
		}
		return filepath.Join(os.TempDir(), fmt.Sprintf("opencode-running-%s", shortID))
	default:
		return ""
	}
}

func ensurePaneReadyForSend(paneID string, trace *tmuxSendTrace) error {
	paneID = normPaneID(paneID)
	var agentType string
	if err := store.QueryRow("SELECT COALESCE(agent_type,'') FROM agent_config WHERE pane_id=?", paneID).
		Scan(&agentType); err != nil {
		log.Printf("[lazy-agent] %s lookup skipped: %v", paneID, err)
		if trace != nil {
			trace.logStep("lookup-skipped", map[string]any{"error": err.Error()}, "")
		}
		return nil
	}
	if trace != nil && trace.AgentType == "" {
		trace.AgentType = normalizeAgentType(agentType)
	}
	if err := ensureLazyAgentReady(paneID, agentType); err != nil {
		if trace != nil {
			trace.logStep("lazy-ready-error", map[string]any{"error": err.Error()}, "")
		}
		return err
	}
	return waitForAgentInputReady(paneID, agentType, trace)
}

func ensureLazyAgentReady(paneID, agentType string) error {
	switch normalizeAgentType(agentType) {
	case "opencode":
		return ensureLazyOpenCodeReady(paneID)
	default:
		return nil
	}
}

func ensureLazyOpenCodeReady(paneID string) error {
	out, err := runTmux("capture-pane", "-t", paneID, "-p", "-S", "-160")
	if err == nil && isOpenCodeInputReady(out) {
		return nil
	}

	markerPath := lazyAgentMarkerPath("opencode", paneID)
	if markerPath != "" {
		if _, statErr := os.Stat(markerPath); os.IsNotExist(statErr) {
			log.Printf("[lazy-agent] %s starting opencode", paneID)
			sendPaneText(paneID, "cicy_start_opencode")
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
		return nil
	}

	return fmt.Errorf("pane %s opencode did not become ready in time", shortPaneID(paneID))
}

func waitForAgentInputReady(paneID, agentType string, trace *tmuxSendTrace) error {
	agentType = normalizeAgentType(agentType)
	if agentType == "" || agentType == "opencode" || agentType == "codex" || agentType == "hermes" {
		return nil
	}
	if trace != nil {
		trace.logStep("ready-wait-start", map[string]any{}, "")
	}
	deadline := time.Now().Add(agentReadyTimeoutForAgent(agentType))
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

func agentReadyTimeoutForAgent(agentType string) time.Duration {
	switch normalizeAgentType(agentType) {
	case "openclaw":
		return openClawAgentReadyTimeout
	default:
		return agentReadyTimeout
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

func submitConfirmTimeoutForAgent(agentType string) time.Duration {
	switch normalizeAgentType(agentType) {
	case "codex":
		return codexSubmitConfirmTimeout
	default:
		return submitConfirmTimeout
	}
}

func submitEnterRetryLimitForAgent(agentType string) int {
	switch normalizeAgentType(agentType) {
	case "codex":
		return codexSubmitEnterRetryLimit
	default:
		return submitEnterRetryLimit
	}
}

func sendPaneText(paneID, text string) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		runTmux("send-keys", "-t", paneID, "-l", "--", line)
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

func shouldBracketedPastePromptText(text string) bool {
	return strings.Contains(text, "\n")
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
		if _, err := runTmux("send-keys", "-t", paneID, "-l", "--", part); err != nil {
			return err
		}
		if i < len(parts)-1 {
			time.Sleep(chunkedPromptChunkDelay)
		}
	}
	return nil
}

func sendPromptBracketedPaste(paneID, text string) error {
	if _, err := runTmux("send-keys", "-t", paneID, "-l", "--", bracketedPasteStart); err != nil {
		return err
	}
	parts := splitTextByRunes(text, chunkedPromptRuneSize)
	for i, part := range parts {
		if part == "" {
			continue
		}
		if _, err := runTmux("send-keys", "-t", paneID, "-l", "--", part); err != nil {
			return err
		}
		if i < len(parts)-1 {
			time.Sleep(chunkedPromptChunkDelay)
		}
	}
	if _, err := runTmux("send-keys", "-t", paneID, "-l", "--", bracketedPasteEnd); err != nil {
		return err
	}
	return nil
}

func sendPromptText(paneID, text string, trace *tmuxSendTrace) (string, error) {
	mode := "literal"
	if shouldBracketedPastePromptText(text) {
		mode = "bracketed-paste"
	} else if shouldPastePromptText(text) {
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
	if mode == "bracketed-paste" {
		return mode, sendPromptBracketedPaste(paneID, text)
	}
	if mode == "chunked" {
		return mode, sendPromptChunked(paneID, text)
	}
	_, err := runTmux("send-keys", "-t", paneID, "-l", "--", text)
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
	case "codex", "claude", "cicy-claude", "openclaw":
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

func codexQueuedPromptVisible(before, after string) bool {
	lowerAfter := strings.ToLower(after)
	if !(strings.Contains(lowerAfter, "queue message") ||
		strings.Contains(lowerAfter, "messages to be submitted after next tool call")) {
		return false
	}
	lowerBefore := strings.ToLower(before)
	if strings.Contains(lowerBefore, "queue message") ||
		strings.Contains(lowerBefore, "messages to be submitted after next tool call") {
		return false
	}
	return isCodexPromptVisible(after)
}

func promptEchoConfirmed(before, after, text, mode, agentType string) (string, bool) {
	if promptEchoVisible(before, after, text) {
		return "matched-text", true
	}
	if normalizeAgentType(agentType) == "codex" && codexQueuedPromptVisible(before, after) {
		return "matched-codex-queue-hint", true
	}
	if mode == "chunked" || mode == "bracketed-paste" {
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
	lower := strings.ToLower(out)
	if (strings.Contains(lower, "queue message") ||
		strings.Contains(lower, "messages to be submitted after next tool call")) &&
		lineContainsPromptFragment(out, text) {
		return true
	}
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
	case "cicy-claude":
		return claudeSubmitConfirmed(after, text)
	case "openclaw":
		return openClawSubmitConfirmed(after, text)
	default:
		return false
	}
}

func shouldRequireStablePreSubmitConfirm(agentType string) bool {
	switch normalizeAgentType(agentType) {
	case "codex":
		return false
	default:
		return true
	}
}

func waitForPromptEchoBeforeEnter(paneID, agentType, text, mode, baseline string, trace *tmuxSendTrace) (string, error) {
	pollInterval := promptConfirmPollIntervalForAgent(agentType)
	stabilizeDelay := promptConfirmStabilizeDelayForAgent(agentType)
	requireStableConfirm := shouldRequireStablePreSubmitConfirm(agentType)
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
			if !requireStableConfirm {
				log.Printf("[tmux-send] pane=%s confirm=%s confirm2=skipped agent=%s mode=%s preview=%q",
					shortPaneID(paneID), match, normalizeAgentType(agentType), mode, promptPreview(text))
				return match, nil
			}
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
	if normalizeAgentType(agentType) == "codex" && isCodexBusyStateVisible(lastCapture) {
		return "", fmt.Errorf("pane %s codex not ready for send", shortPaneID(paneID))
	}
	if lastCapture != "" {
		log.Printf("[tmux-send] pane=%s confirm=timeout agent=%s mode=%s preview=%q tail=%q",
			shortPaneID(paneID), normalizeAgentType(agentType), mode, promptPreview(text), promptPreview(lastCapture))
	}
	return "", fmt.Errorf("prompt echo not confirmed for %s", shortPaneID(paneID))
}

func submitPromptWithConfirmation(paneID, agentType, text string, trace *tmuxSendTrace) error {
	pollInterval := submitConfirmPollIntervalForAgent(agentType)
	timeout := submitConfirmTimeoutForAgent(agentType)
	retryLimit := submitEnterRetryLimitForAgent(agentType)
	var lastCapture string
	var lastErr error
	for attempt := 0; attempt <= retryLimit; attempt++ {
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
		deadline := time.Now().Add(timeout)
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
	if normalizeAgentType(agentType) == "codex" {
		log.Printf("[tmux-send] pane=%s submit=soft-timeout agent=%s preview=%q tail=%q",
			shortPaneID(paneID), normalizeAgentType(agentType), promptPreview(text), promptPreview(lastCapture))
		if trace != nil {
			trace.logStep("submit-soft-timeout", map[string]any{}, lastCapture)
		}
		return nil
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
				runTmux("send-keys", "-t", paneID, "2")
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
		updateSkipCount := 0
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
			if isCodexUpdatePrompt(out) {
				if updateSkipCount >= 2 || time.Since(lastAction) < 1500*time.Millisecond {
					continue
				}
				log.Printf("[codex-auto-confirm] %s dismiss update prompt #%d", paneID, updateSkipCount+1)
				runTmux("send-keys", "-t", paneID, "2")
				time.Sleep(150 * time.Millisecond)
				runTmux("send-keys", "-t", paneID, "Enter")
				lastAction = time.Now()
				updateSkipCount++
				continue
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
	aiCfg := loadRuntimeAIConfig()
	// proxyURL := fmt.Sprintf("http://%s:x@127.0.0.1:17080", shortID)

	// tmux panes inherit environment from the tmux server, not from the current
	// cicy-code process. Sync runtime auth/config into the target session before
	// sourcing the init script so agent boot code can see current values.
	// Per-agent-type env: only set what each agent actually needs.
	agentNorm := normalizeAgentType(opts.agentType)
	sessionEnv := map[string]string{
		"CICY_AGENT_TYPE": agentNorm,
	}
	switch agentNorm {
	case "openclaw":
		sessionEnv["CICY_API_KEY"] = strings.TrimSpace(aiCfg.APIKey)
		sessionEnv["CICY_API_URL"] = strings.TrimSpace(aiCfg.APIURL)
		sessionEnv["CICY_ANTHROPIC_URL"] = strings.TrimSpace(aiCfg.AnthropicURL)
		sessionEnv["CICY_OPENCLAW_MODEL"] = strings.TrimSpace(aiCfg.OpenClawModel)
	case "claude":
		// claude uses ANTHROPIC_BASE_URL and settings.json directly in boot lines
	case "opencode":
	case "codex":
		// codex uses -c flags directly, no env needed
	case "kiro-cli":
		// kiro-cli uses ANTHROPIC_BASE_URL directly in boot lines
	case "copilot":
		// copilot uses GitHub auth, no gateway env needed
	case "cicy-wechat":
		// cicy-wechat handles its own auth
	case "cicy-feishu":
		// cicy-feishu uses FEISHU_APP_ID/FEISHU_APP_SECRET env vars
	case "hermes":
		sessionEnv["CICY_API_KEY"] = strings.TrimSpace(aiCfg.APIKey)
		sessionEnv["CICY_API_URL"] = strings.TrimSpace(aiCfg.APIURL)
		sessionEnv["CICY_ANTHROPIC_URL"] = strings.TrimSpace(aiCfg.AnthropicURL)
	default:
		sessionEnv["CICY_API_KEY"] = strings.TrimSpace(aiCfg.APIKey)
		sessionEnv["CICY_API_URL"] = strings.TrimSpace(aiCfg.APIURL)
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
		fmt.Sprintf("export X_AGENT_ID=%s", tmuxShellQuote(pid)),
		fmt.Sprintf("export X_AGENT_SHORT_ID=%s", tmuxShellQuote(shortID)),
	}
	for key, value := range sessionEnv {
		if value != "" {
			lines = append(lines, fmt.Sprintf("export %s=%s", key, tmuxShellQuote(value)))
		}
	}
	switch agentNorm {
	case "codex", "claude", "kiro-cli", "copilot", "cicy-wechat", "cicy-feishu", "opencode":
		// boot lines handle gateway URLs directly
	default:
		lines = append(lines,
			fmt.Sprintf("export CICY_OPENAI_BASE_URL=%s", tmuxShellQuote(openAIRuntimeBaseURL(shortID))),
			fmt.Sprintf("export CICY_ANTHROPIC_BASE_URL=%s", tmuxShellQuote(anthropicRuntimeBaseURL(shortID))),
		)
	}

	if opts.workspace != "" {
		lines = append(lines,
			fmt.Sprintf("export WORKSPACE=%s", tmuxShellQuote(opts.workspace)),
			`if [ ! -e ./home ] || [ -L ./home ]; then ln -sfn -- "${HOME:-$(getent passwd "$(id -u)" | cut -d: -f6)}" ./home; fi`,
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

	// 将启动脚本写入 workspace，避免散落到 /tmp。
	// source boot.sh may happen inside zsh on macOS, but the generated body relies
	// on bash semantics. Re-enter through bash so manual/source startup still works.
	script := "#!/usr/bin/env bash\n\n" +
		"if [ -z \"${BASH_VERSION:-}\" ]; then\n" +
		"  _cicy_boot_dir=\"$PWD\"\n" +
		"  bash -lc 'cd \"$1\" && source ./boot.sh' bash \"$_cicy_boot_dir\"\n" +
		"  _cicy_boot_status=$?\n" +
		"  unset _cicy_boot_dir\n" +
		"  return \"$_cicy_boot_status\" 2>/dev/null || exit \"$_cicy_boot_status\"\n" +
		"fi\n\n" +
		"_cicy_boot_script=\"${BASH_SOURCE:-$0}\"\n" +
		"_cicy_boot_dir=\"$(cd \"$(dirname \"$_cicy_boot_script\")\" && pwd)\"\n" +
		"cd \"$_cicy_boot_dir\"\n\n" +
		strings.Join(lines, "\n") + "\n" +
		"unset _cicy_boot_script _cicy_boot_dir\n"
	scriptPath := filepath.Join(opts.workspace, "boot.sh")
	if strings.TrimSpace(opts.workspace) == "" {
		scriptPath = fmt.Sprintf("/tmp/init_pane_%s.sh", strings.ReplaceAll(pid, ":", "_"))
	} else if err := os.MkdirAll(opts.workspace, 0755); err != nil {
		log.Printf("[init] failed to ensure workspace for script: %v", err)
		return
	}
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		log.Printf("[init] failed to write script: %v", err)
		return
	}
	log.Printf("[init] v1 pane %s script path=%s\n%s", pid, scriptPath, script)

	// On macOS the tmux pane can exist before the shell prompt is actually
	// visible/interactive. Wait for the prompt marker before sending boot.sh.
	if waitForShellPromptReady(pid) {
		log.Printf("[init] shell prompt ready for %s", shortPaneID(pid))
		runTmux("send-keys", "-t", pid, "source boot.sh", "Enter")
	} else {
		log.Printf("[init] shell prompt not confirmed for %s, skip auto source boot.sh", shortPaneID(pid))
		return
	}
	if normalizeAgentType(opts.agentType) == "claude" || normalizeAgentType(opts.agentType) == "cicy-claude" {
		autoConfirmClaudeStartup(pid, opts.allowAllActions)
	} else if normalizeAgentType(opts.agentType) == "codex" {
		autoConfirmCodexTrust(pid)
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

type tmuxSendError struct {
	Message      string
	StatusCode   int
	PaneUpdated  bool
	RestoreInput bool
}

func (e *tmuxSendError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func newTmuxSendError(message string, statusCode int, paneUpdated bool) *tmuxSendError {
	return &tmuxSendError{
		Message:      message,
		StatusCode:   statusCode,
		PaneUpdated:  paneUpdated,
		RestoreInput: !paneUpdated,
	}
}

func getPaneSendWorker(paneID string) *paneSendWorker {
	paneID = normPaneID(paneID)
	paneSendWorkersMu.Lock()
	defer paneSendWorkersMu.Unlock()
	if worker, ok := paneSendWorkers[paneID]; ok {
		return worker
	}
	worker := &paneSendWorker{ch: make(chan paneSendRequest, 128)}
	paneSendWorkers[paneID] = worker
	go runPaneSendWorker(paneID, worker)
	return worker
}

func mergePaneSendBatch(batch []paneSendRequest) string {
	if len(batch) == 0 {
		return ""
	}
	parts := make([]string, 0, len(batch))
	for _, req := range batch {
		parts = append(parts, req.text)
	}
	return strings.Join(parts, "\n")
}

func runPaneSendWorker(paneID string, worker *paneSendWorker) {
	for req := range worker.ch {
		batch := []paneSendRequest{req}
	collect:
		for {
			select {
			case next := <-worker.ch:
				batch = append(batch, next)
			default:
				break collect
			}
		}
		text := mergePaneSendBatch(batch)
		if len(batch) > 1 {
			log.Printf("[tmux-send-queue] pane=%s merged=%d line_count=%d rune_count=%d",
				shortPaneID(paneID), len(batch), promptLineCount(text), utf8.RuneCountInString(text))
		}
		err := sendTextToPaneDirect(paneID, text)
		for _, item := range batch {
			item.result <- err
			close(item.result)
		}
	}
}

func sendTextToPane(winID, text string) error {
	winID = normPaneID(winID)
	req := paneSendRequest{
		text:   text,
		result: make(chan error, 1),
	}
	getPaneSendWorker(winID).ch <- req
	return <-req.result
}

func sendTextToPaneDirect(winID, text string) error {
	winID = normPaneID(winID)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if winID == "" {
		return newTmuxSendError("win_id required", http.StatusBadRequest, false)
	}
	if strings.TrimSpace(text) == "" {
		return newTmuxSendError("text required", http.StatusBadRequest, false)
	}
	agentType := paneAgentType(winID)
	trace := newTmuxSendTrace(winID, agentType)
	trace.logStep("request", map[string]any{
		"line_count": promptLineCount(text),
		"rune_count": utf8.RuneCountInString(text),
	}, text)
	if err := ensurePaneReadyForSend(winID, trace); err != nil {
		trace.logStep("request-error", map[string]any{"error": err.Error()}, "")
		statusCode := http.StatusInternalServerError
		if strings.Contains(strings.ToLower(err.Error()), "not ready for send") {
			statusCode = http.StatusConflict
		}
		return newTmuxSendError(err.Error(), statusCode, false)
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
		return newTmuxSendError("failed to send text: "+err.Error(), http.StatusInternalServerError, false)
	}
	if confirmBeforeEnter {
		if _, err := waitForPromptEchoBeforeEnter(winID, agentType, text, mode, baseline, trace); err != nil {
			trace.logStep("pre-submit-failed", map[string]any{"error": err.Error()}, "")
			return newTmuxSendError("failed to confirm text before submit: "+err.Error(), http.StatusConflict, true)
		}
		if err := submitPromptWithConfirmation(winID, agentType, text, trace); err != nil {
			trace.logStep("submit-failed", map[string]any{"error": err.Error()}, "")
			return newTmuxSendError(err.Error(), http.StatusConflict, true)
		}
	} else {
		delay := enterDelay
		if mode == "chunked" {
			delay = chunkedPromptEnterDelay
		}
		time.Sleep(delay)
		if err := sendPromptEnter(winID); err != nil {
			trace.logStep("submit-enter-error", map[string]any{"error": err.Error()}, "")
			return newTmuxSendError("failed to submit text: "+err.Error(), http.StatusConflict, true)
		}
	}
	trace.logStep("request-complete", map[string]any{"mode": mode, "confirm_before_enter": confirmBeforeEnter}, "")
	return nil
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
		httpErr(w, http.StatusBadRequest, "win_id required")
		return
	}
	if text, ok := req["text"].(string); ok && text != "" {
		if err := sendTextToPane(winID, text); err != nil {
			if sendErr, ok := err.(*tmuxSendError); ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(sendErr.StatusCode)
				_ = json.NewEncoder(w).Encode(M{
					"detail":        sendErr.Message,
					"pane_updated":  sendErr.PaneUpdated,
					"restore_input": sendErr.RestoreInput,
				})
				return
			}
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else if keys, ok := req["keys"].(string); ok && keys != "" {
		log.Printf("[tmux-send] pane=%s mode=keys keys=%q", shortPaneID(winID), keys)
		if _, err := runTmux("send-keys", "-t", winID, keys); err != nil {
			httpErr(w, http.StatusInternalServerError, "failed to send keys: "+err.Error())
			return
		}
	} else {
		httpErr(w, http.StatusBadRequest, "text or keys required")
		return
	}
	J(w, M{"success": true, "win_id": shortPaneID(winID)})
}

func handleSendKeys(w http.ResponseWriter, r *http.Request) {
	var req M
	readBody(r, &req)
	winID, _ := req["win_id"].(string)
	winID = normPaneID(winID)
	if winID == "" {
		httpErr(w, http.StatusBadRequest, "win_id required")
		return
	}
	keys, _ := req["keys"].(string)
	if keys == "" {
		httpErr(w, http.StatusBadRequest, "keys required")
		return
	}
	if _, err := runTmux("send-keys", "-t", winID, keys); err != nil {
		httpErr(w, http.StatusInternalServerError, "failed to send keys: "+err.Error())
		return
	}
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

func readAIGatewayReplySnapshot(agentID string) (aiGatewayReplySnapshot, error) {
	path := filepath.Join(aiGatewayHistoryDir(agentID), "reply.json")
	body, err := os.ReadFile(path)
	if err != nil {
		return aiGatewayReplySnapshot{}, err
	}
	var snapshot aiGatewayReplySnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return aiGatewayReplySnapshot{}, err
	}
	return snapshot, nil
}

func isAIGatewayReplyTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed":
		return true
	default:
		return false
	}
}

func aiGatewayReplyTimestamp(snapshot aiGatewayReplySnapshot) time.Time {
	for _, raw := range []string{snapshot.StartedAt, snapshot.UpdatedAt} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if ts, err := time.Parse(time.RFC3339, raw); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func readAIGatewayMessagesFile(agentID string) (aiGatewayMessagesFile, error) {
	return aiGatewayLoadMessagesFile(agentID)
}

func aiGatewayMessageRecordTimestamp(record aiGatewayMessageRecord) time.Time {
	for _, raw := range []string{record.ATime, record.QTime} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if ts, err := time.Parse(time.RFC3339, raw); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func aiGatewayMessageMatchesSend(record aiGatewayMessageRecord, expectedQuestion string, sentFloor time.Time) bool {
	if strings.TrimSpace(aiGatewaySanitizeMessageAnswer(record.A)) == "" {
		return false
	}
	recordQuestion := aiGatewaySanitizeUserQuestion(record.Q)
	if recordQuestion == "" {
		return false
	}
	expectedQuestion = aiGatewaySanitizeUserQuestion(expectedQuestion)
	if expectedQuestion != "" && recordQuestion == expectedQuestion {
		return true
	}
	ts := aiGatewayMessageRecordTimestamp(record)
	return !ts.IsZero() && !ts.Before(sentFloor)
}

func waitForAIGatewayMessageRecord(agentID string, baselineCount int, expectedQuestion string, sentAt time.Time, timeout time.Duration) (aiGatewayMessageRecord, error) {
	deadline := time.Now().Add(timeout)
	pollInterval := 250 * time.Millisecond
	sentFloor := sentAt.UTC().Add(-1 * time.Second)
	var lastSnapshot aiGatewayReplySnapshot
	var lastErr error

	for time.Now().Before(deadline) {
		file, err := readAIGatewayMessagesFile(agentID)
		if err != nil {
			if !os.IsNotExist(err) {
				lastErr = err
			}
		} else if len(file.Messages) > baselineCount {
			limit := len(file.Messages)
			if baselineCount < 0 {
				baselineCount = 0
			}
			for i := baselineCount; i < limit; i++ {
				record := file.Messages[i]
				if aiGatewayMessageMatchesSend(record, expectedQuestion, sentFloor) {
					log.Printf("[send-wait] pane=%s matched message q_len=%d a_len=%d model=%s tools=%d",
						agentID, len([]rune(record.Q)), len([]rune(record.A)), record.Model, len(record.ToolCalls))
					return record, nil
				}
			}
		}
		snapshot, err := readAIGatewayReplySnapshot(agentID)
		if err == nil {
			lastSnapshot = snapshot
			if isAIGatewayReplyTerminal(snapshot.Status) && strings.EqualFold(snapshot.Status, "failed") {
				current, currentErr := aiGatewayReadCurrentSnapshot(agentID)
				if currentErr == nil && aiGatewayMessageMatchesSend(aiGatewayMessageRecord{
					Q:     aiGatewayCurrentQuestion(current),
					A:     snapshot.Answer,
					QTime: current.Timestamp,
					ATime: snapshot.UpdatedAt,
				}, expectedQuestion, sentFloor) {
					return aiGatewayMessageRecord{
						Q:         aiGatewayCurrentQuestion(current),
						A:         aiGatewaySanitizeMessageAnswer(snapshot.Answer),
						QTime:     aiGatewayMessageQuestionTime(current, snapshot),
						ATime:     aiGatewayMessageAnswerTime(snapshot),
						Model:     aiGatewayFirstNonEmpty(aiGatewayReplyPrimaryModel(snapshot), strings.TrimSpace(current.Model)),
						Thinking:  strings.TrimSpace(snapshot.Thinking),
						ToolCalls: aiGatewayBuildMessageToolCalls(nil, snapshot),
					}, nil
				}
			}
		} else if !os.IsNotExist(err) {
			lastErr = err
		}
		time.Sleep(pollInterval)
	}

	if lastErr != nil {
		log.Printf("[send-wait] pane=%s timeout waiting message read_err=%v last_turn=%s status=%s",
			agentID, lastErr, lastSnapshot.TurnID, lastSnapshot.Status)
		return aiGatewayMessageRecord{}, fmt.Errorf("timeout after %ds waiting for gateway message: %w", int(timeout/time.Second), lastErr)
	}
	log.Printf("[send-wait] pane=%s timeout waiting message last_turn=%s status=%s", agentID, lastSnapshot.TurnID, lastSnapshot.Status)
	return aiGatewayMessageRecord{}, fmt.Errorf("timeout after %ds waiting for gateway message", int(timeout/time.Second))
}

func handleSendWait(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target  string `json:"target"`
		Text    string `json:"text"`
		Timeout int    `json:"timeout"`
	}
	readBody(r, &req)
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
	if paneID == "" {
		J(w, M{"success": false, "error": "target required"})
		return
	}

	agentID := shortPaneID(paneID)
	baseline, baselineErr := readAIGatewayReplySnapshot(agentID)
	if baselineErr != nil && !os.IsNotExist(baselineErr) {
		log.Printf("[send-wait] pane=%s baseline read error: %v", agentID, baselineErr)
	}
	baselineMessages, baselineMessagesErr := readAIGatewayMessagesFile(agentID)
	if baselineMessagesErr != nil && !os.IsNotExist(baselineMessagesErr) {
		log.Printf("[send-wait] pane=%s baseline messages read error: %v", agentID, baselineMessagesErr)
	}
	log.Printf("[send-wait] pane=%s send start timeout=%ds baseline_turn=%s baseline_status=%s",
		agentID, req.Timeout, baseline.TurnID, baseline.Status)

	sentAt := time.Now().UTC()
	if err := sendTextToPane(paneID, req.Text); err != nil {
		log.Printf("[send-wait] pane=%s send failed: %v", agentID, err)
		J(w, M{"success": false, "pane_id": agentID, "question": req.Text, "error": err.Error()})
		return
	}

	record, err := waitForAIGatewayMessageRecord(agentID, len(baselineMessages.Messages), req.Text, sentAt, time.Duration(req.Timeout)*time.Second)
	if err != nil {
		reply, _ := readAIGatewayReplySnapshot(agentID)
		J(w, M{
			"success":  false,
			"pane_id":  agentID,
			"question": req.Text,
			"turn_id":  reply.TurnID,
			"status":   reply.Status,
			"answer":   reply.Answer,
			"error":    err.Error(),
		})
		return
	}
	J(w, M{
		"success":    true,
		"pane_id":    agentID,
		"question":   record.Q,
		"status":     "completed",
		"answer":     record.A,
		"thinking":   record.Thinking,
		"tool_calls": record.ToolCalls,
		"qTime":      record.QTime,
		"aTime":      record.ATime,
		"model":      record.Model,
	})
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
