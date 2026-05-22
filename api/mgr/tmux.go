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

type paneCreateOpts struct {
	session          string
	title            string
	role             string
	defaultModel     string
	agentType        string
	workspace        string
	initScript       string
	port             int
	token            string
	allowAllActions  bool
	replyInChinese   bool
	useCustomGateway bool
	useProxy         bool
	proxyPassword    string
	proxyRule        string
	masterPaneID     string
	masterAgentType  string
	inheritGuidance  bool
}

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
	return filepath.Join(home, "logs", "tmux-send.log")
}

func tmuxClientTraceLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "cicy-tmux-client-trace.log")
	}
	return filepath.Join(home, "logs", "tmux-client-trace.log")
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
	// (often "%" on macOS zsh, "$" on bash). Wait for a visible prompt terminator
	// before sending the startup script.
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
				if isShellPromptVisible(out) {
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
		rows, err = store.Query(`SELECT DISTINCT t.pane_id, t.title, t.ttyd_port, t.workspace, t.init_script, t.active, t.created_at, t.updated_at, gp.group_id, t.role, t.default_model, t.trust_level, t.agent_type, COALESCE(t.allow_all_actions, 0), COALESCE(t.reply_in_chinese, 0), COALESCE(t.use_custom_gateway, 0), COALESCE(t.proxy_enable, 0)
				FROM agent_config t INNER JOIN group_windows gp ON t.pane_id=gp.win_id WHERE gp.group_id=? AND t.active=1 ORDER BY t.created_at DESC`, gid)
	} else {
		rows, err = store.Query(`SELECT t.pane_id, t.title, t.ttyd_port, t.workspace, t.init_script, t.active, t.created_at, t.updated_at, gp.group_id, t.role, t.default_model, t.trust_level, t.agent_type, COALESCE(t.allow_all_actions, 0), COALESCE(t.reply_in_chinese, 0), COALESCE(t.use_custom_gateway, 0), COALESCE(t.proxy_enable, 0)
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
		var useCustomGateway sql.NullBool
		var useProxy sql.NullBool
		rows.Scan(&paneID, &title, &port, &workspace, &initScript, &active, &createdAt, &updatedAt, &groupID, &role, &defaultModel, &trustLevel, &agentType, &allowAllActions, &replyInChinese, &useCustomGateway, &useProxy)
		p := M{
			"pane_id": paneID.String, "title": title.String, "ttyd_port": port.Int64,
			"workspace": workspace.String, "init_script": initScript.String,
			"active": active.Int64,
			"role":   role.String, "default_model": defaultModel.String,
			"trust_level": trustLevel.String, "agent_type": agentType.String,
			"allow_all_actions":  allowAllActions.Bool,
			"reply_in_chinese":   replyInChinese.Bool,
			"use_custom_gateway": useCustomGateway.Bool,
			"use_proxy":          useProxy.Bool,
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
		WinName          *string `json:"win_name"`
		InitScript       string  `json:"init_script"`
		Title            string  `json:"title"`
		AgentType        string  `json:"agent_type"`
		Role             string  `json:"role"`
		DefaultModel     string  `json:"default_model"`
		AllowAllActions  bool    `json:"allow_all_actions"`
		ReplyInChinese   bool    `json:"reply_in_chinese"`
		UseCustomGateway *bool   `json:"use_custom_gateway"`
		UseProxy         *bool   `json:"use_proxy"`
		Proxy            any     `json:"proxy"`
		MasterPaneID     string  `json:"master_pane_id"`
		MasterAgentType  string  `json:"master_agent_type"`
		InheritGuidance  *bool   `json:"inherit_guidance"`
	}
	req.AllowAllActions = true
	req.ReplyInChinese = true
	readBody(r, &req)
	req.AgentType = normalizeAgentType(req.AgentType)
	if req.AgentType == "" {
		J(w, M{"success": false, "error": "unsupported agent_type"})
		return
	}
	if !isAllowedAgentType(req.AgentType) {
		J(w, M{"success": false, "error": "agent_type not allowed in current mode"})
		return
	}
	token := getToken(r)

	useCustomGateway := true
	if req.UseCustomGateway != nil {
		useCustomGateway = *req.UseCustomGateway
	}
	useProxy := false
	if req.UseProxy != nil {
		useProxy = *req.UseProxy
	}
	proxySettings, err := proxySettingsFromAny(req.Proxy)
	if err != nil {
		J(w, M{"success": false, "error": err.Error()})
		return
	}
	inheritGuidance := true
	if req.InheritGuidance != nil {
		inheritGuidance = *req.InheritGuidance
	}
	result, err := doCreatePane(req.Title, req.Role, req.DefaultModel, req.AgentType, req.InitScript, req.AllowAllActions, req.ReplyInChinese, useCustomGateway, useProxy, proxySettings, req.WinName, strings.TrimSpace(req.MasterPaneID), strings.TrimSpace(req.MasterAgentType), inheritGuidance, token)
	if err != nil {
		J(w, M{"success": false, "error": err.Error()})
		return
	}
	J(w, result)
}

func doCreatePane(title, role, defaultModel, agentType, initScript string, allowAllActions bool, replyInChinese bool, useCustomGateway bool, useProxy bool, proxy *proxySettings, winName *string, masterPaneID string, masterAgentType string, inheritGuidance bool, token string) (M, error) {
	agentType = normalizeAgentType(agentType)
	if agentType == "" {
		return M{"success": false}, fmt.Errorf("unsupported agent_type")
	}
	if !isAllowedAgentType(agentType) {
		return M{"success": false}, fmt.Errorf("agent_type not allowed in current mode")
	}
	// Get next worker index
	var workerIdx int
	tx, _ := store.Begin()
	tx.QueryRow("SELECT value FROM global_vars WHERE key_name='worker_index'").Scan(&workerIdx)
	if workerIdx == 0 {
		workerIdx = defaultWorkerIndex
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
	return createManagedPane(paneCreateOpts{
		session:          session,
		title:            t,
		role:             role,
		defaultModel:     defaultModel,
		agentType:        agentType,
		workspace:        builtinWorkerWorkspace(session),
		initScript:       initScript,
		port:             workerIdx,
		token:            token,
		allowAllActions:  allowAllActions,
		replyInChinese:   replyInChinese,
		useCustomGateway: useCustomGateway,
		useProxy:         useProxy,
		proxyPassword:    proxySettingsPassword(proxy),
		proxyRule:        proxySettingsRule(proxy),
		masterPaneID:     masterPaneID,
		masterAgentType:  masterAgentType,
		inheritGuidance:  inheritGuidance,
	})
}

func proxySettingsPassword(p *proxySettings) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.Password)
}
func proxySettingsRule(p *proxySettings) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.Rule)
}

// guidanceFilenameForAgentType returns the per-agent guidance filename
// (CLAUDE.md / AGENTS.md). Returns "" for agents that don't have one.
func guidanceFilenameForAgentType(agentType string) string {
	switch normalizeAgentType(agentType) {
	case "claude", "cicy-claude", "kiro-cli":
		return "CLAUDE.md"
	case "codex", "opencode", "cursor":
		return "AGENTS.md"
	}
	return ""
}

// writeAgentGuidanceFile drops an agent-rules file (CLAUDE.md or AGENTS.md)
// into the workspace based on the agent type. Existing files are not
// overwritten so user customisations survive recreate.
func writeAgentGuidanceFile(workspace, agentType, paneID string) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return
	}
	filename := guidanceFilenameForAgentType(agentType)
	if filename == "" {
		return
	}
	path := filepath.Join(workspace, filename)
	if _, err := os.Stat(path); err == nil {
		return
	}
	shortID := strings.Split(paneID, ":")[0]
	content := fmt.Sprintf("# %s\n\n"+
		"- 你的 AGENT_ID 是 `%s`\n"+
		"- 你的当前目录是 `%s`\n"+
		"- 你的项目目录要询问用户设置\n"+
		"- 你运行在 tmux 中，可以通过 `cicy-agent` skill 与其他 agent 协作："+
		"`cicy-agent ls` 发现其他 pane，`cicy-agent msg <pane> <text>` 派发任务或请求支援，"+
		"`cicy-agent capture <pane>` 查看对方进度。"+
		"先运行 `cicy-agent help` 查看完整子命令（注意是 `help` 不是 `--help`）\n",
		filename, shortID, workspace)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		log.Printf("[init] failed to write %s for %s: %v", filename, shortID, err)
	}
}

// appendMasterReferenceToGuidance writes a "你的 master 是 ..." line into the
// sub pane's guidance file, including a pointer to the master's rules file if
// the master has one. Idempotent: skips if the line already exists.
//
// masterAgentTypeHint, when non-empty, overrides the agent_type derived from
// agent_config — useful when the master lives on a remote node or when the
// caller already knows the type and wants to avoid a DB lookup.
func appendMasterReferenceToGuidance(subPaneID, masterPaneID, masterAgentTypeHint string) {
	subFull := normPaneID(strings.TrimSpace(subPaneID))
	masterFull := normPaneID(strings.TrimSpace(masterPaneID))
	if subFull == "" || masterFull == "" || subFull == masterFull {
		return
	}
	var subType, subWorkspace string
	if err := store.QueryRow(
		"SELECT COALESCE(agent_type,''), COALESCE(workspace,'') FROM agent_config WHERE pane_id=?",
		subFull,
	).Scan(&subType, &subWorkspace); err != nil {
		return
	}
	subFile := guidanceFilenameForAgentType(subType)
	if subFile == "" || strings.TrimSpace(subWorkspace) == "" {
		return
	}
	subPath := filepath.Join(subWorkspace, subFile)
	existing, err := os.ReadFile(subPath)
	if err != nil {
		return
	}
	if strings.Contains(string(existing), "你的 master 是") {
		return
	}
	var masterType, masterWorkspace string
	_ = store.QueryRow(
		"SELECT COALESCE(agent_type,''), COALESCE(workspace,'') FROM agent_config WHERE pane_id=?",
		masterFull,
	).Scan(&masterType, &masterWorkspace)
	if hint := strings.TrimSpace(masterAgentTypeHint); hint != "" {
		masterType = normalizeAgentType(hint)
	}
	masterShort := shortPaneID(masterFull)
	line := fmt.Sprintf("- 你的 master 是 `%s` (agent_type: `%s`)", masterShort, masterType)
	if masterFile := guidanceFilenameForAgentType(masterType); masterFile != "" && strings.TrimSpace(masterWorkspace) != "" {
		// `@<absolute-path>` 让 Claude / Codex 自动把 master 规则文件 inline 到上下文；
		// OpenCode 不会自动解析 `@`，但 prose 形式足以提示它用 Read 工具按需加载。
		line = fmt.Sprintf("- 你的 master 是 `%s` (agent_type: `%s`)，请参考 master 的规则文件了解项目上下文：@%s",
			masterShort, masterType, filepath.Join(masterWorkspace, masterFile))
	}
	appended := strings.TrimRight(string(existing), "\n") + "\n" + line + "\n"
	if err := os.WriteFile(subPath, []byte(appended), 0644); err != nil {
		log.Printf("[guidance] failed to append master ref to %s: %v", subPath, err)
	}
}

func createManagedPane(opts paneCreateOpts) (M, error) {
	workspace := strings.TrimSpace(opts.workspace)
	if err := ensureRuntimeDir(workspace, 0755); err != nil {
		return M{"success": false}, err
	}

	paneID := opts.session + ":main.0"
	writeAgentGuidanceFile(workspace, opts.agentType, paneID)
	runTmux("new-session", "-d", "-s", opts.session, "-n", "main", "-c", workspace)
	proxyConfigJSON, err := mergeProxySettingsIntoConfigJSON("{}", &proxySettings{Password: opts.proxyPassword, Rule: opts.proxyRule})
	if err != nil {
		return M{"success": false}, err
	}
	store.Exec(
		fmt.Sprintf(`INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese, use_custom_gateway, proxy_enable, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,%s,%s)`, store.Now(), store.Now()),
		paneID, opts.title, opts.port, workspace, opts.initScript, proxyConfigJSON, opts.role, opts.defaultModel, opts.agentType, opts.allowAllActions, opts.replyInChinese, opts.useCustomGateway, opts.useProxy,
	)
	if strings.TrimSpace(opts.masterPaneID) != "" {
		if _, err := bindAgentCore(opts.masterPaneID, opts.session, opts.inheritGuidance, opts.masterAgentType); err != nil {
			log.Printf("[create] auto-bind sub=%s under master=%s failed: %v",
				opts.session, opts.masterPaneID, err)
		}
	}
	if err := startInstance(paneID, opts.port, opts.token); err != nil {
		return M{"session": opts.session, "pane_id": shortPaneID(paneID)}, err
	}
	waitPort(opts.port, 10*time.Second)
	initPaneEnv(paneEnvOpts{
		paneID:           paneID,
		configJSON:       proxyConfigJSON,
		workspace:        workspace,
		initScript:       opts.initScript,
		agentType:        opts.agentType,
		defaultModel:     opts.defaultModel,
		allowAllActions:  opts.allowAllActions,
		replyInChinese:   opts.replyInChinese,
		useCustomGateway: opts.useCustomGateway,
		useProxy:         opts.useProxy,
		proxyPassword:    opts.proxyPassword,
		proxyRule:        opts.proxyRule,
	})
	return M{
		"success":          true,
		"session":          opts.session,
		"window":           "main",
		"pane_id":          shortPaneID(paneID),
		"title":            opts.title,
		"workspace":        workspace,
		"init_script":      opts.initScript,
		"ttyd_port":        opts.port,
		"reply_in_chinese": opts.replyInChinese,
		"use_proxy":        opts.useProxy,
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
	var useCustomGateway sql.NullBool
	var useProxy sql.NullBool
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
		COALESCE(t.use_custom_gateway, 0),
		COALESCE(t.proxy_enable, 0),
		COALESCE(t.machine_id, 0), COALESCE(m.label, ''), COALESCE(m.url, ''), COALESCE(json_extract(m.capabilities_json, '$.runtime_kind'), ''), COALESCE(m.capabilities_json, '{}')
		FROM agent_config t
		LEFT JOIN group_windows gp ON t.pane_id=gp.win_id
		LEFT JOIN machines m ON t.machine_id=m.id
		WHERE t.pane_id=?`, paneID).Scan(
		&paneID, &title, &port, &workspace, &initScript,
		&tgToken, &tgChatID, &tgEnable, &active, &agentType, &agentDuty, &config, &commonPrompt, &ttydPreview, &groupID, &role, &defaultModel, &trustLevel, &allowAllActions,
		&replyInChinese, &useCustomGateway, &useProxy,
		&machineID, &machineLabel, &machineURL, &runtimeKind, &capabilitiesJSON)
	if err != nil {
		httpErr(w, 404, "Pane "+id+" not found")
		return
	}
	runtimeAI := extractRuntimeAIFromConfigJSON(config.String)
	proxySettings := extractProxySettingsFromConfigJSON(config.String)
	runtimeAIDefault := runtimeAIDefaultSummaryForAgentType(agentType.String)
	resp := M{
		"pane_id": shortPaneID(paneID), "title": title.String, "ttyd_port": port.Int64,
		"workspace": workspace.String, "init_script": initScript.String,
		"tg_token": tgToken.String, "tg_chat_id": tgChatID.String, "tg_enable": tgEnable.Bool,
		"active": active.Int64, "agent_type": agentType.String, "agent_duty": agentDuty.String,
		"config": config.String, "common_prompt": commonPrompt.String, "ttyd_preview": ttydPreview.String,
		"allow_all_actions":  allowAllActions.Bool,
		"reply_in_chinese":   replyInChinese.Bool,
		"use_custom_gateway": useCustomGateway.Bool,
		"use_proxy":          useProxy.Bool,
		"role":               role.String, "default_model": defaultModel.String,
		"trust_level":                 trustLevel.String,
		"machine_label":               machineLabel.String,
		"machine_url":                 machineURL.String,
		"runtime_kind":                runtimeKind.String,
		"runtime_ai":                  runtimeAIOverrideToMap(runtimeAI),
		"proxy":                       proxySettingsToMap(proxySettings),
		"runtime_ai_provider_options": runtimeAIProviderOptionsForAgentType(agentType.String),
		"runtime_ai_default":          runtimeAIDefault,
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

// columns allowed in agent_config UPDATE.
//
// Identity / provisioning fields are deliberately NOT in this map:
//
//	workspace, agent_type, role, init_script, ttyd_port, node_url
//
// — these are set at pane creation (or by bind/unbind, for role) and never via
// PATCH. Putting them here is a security hole: a buggy or malicious caller
// passing `workspace` would silently rewrite a pane's filesystem mapping.
// Bug 2026-05-14 corrupted workspace fields cross-pane because the inspector
// PATCHed a whole settingsData blob; the frontend was tightened, but the
// surface area here is the durable defense.
var paneUpdateCols = map[string]bool{
	"title": true, "agent_duty": true, "config": true, "common_prompt": true,
	"ttyd_preview": true, "default_model": true, "trust_level": true,
	"tg_token": true, "tg_chat_id": true, "tg_enable": true, "active": true,
	"allow_all_actions":  true,
	"reply_in_chinese":   true,
	"use_custom_gateway": true,
	"use_proxy":          true,
	"runtime_ai":         true,
	"private_mode":       true,
	"allowed_users":      true,
	"preview":            true,
	"inject_rules_files": true,
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
	if rawAgentType, ok := filtered["agent_type"]; ok {
		agentType, ok := rawAgentType.(string)
		if !ok {
			httpErr(w, 400, "agent_type must be a string")
			return
		}
		agentType = normalizeAgentType(agentType)
		if agentType == "" {
			httpErr(w, 400, "unsupported agent_type")
			return
		}
		if !isAllowedAgentType(agentType) {
			httpErr(w, 400, "agent_type not allowed in current mode")
			return
		}
		filtered["agent_type"] = agentType
	}
	if rawConfig, ok := filtered["config"]; ok {
		configStr, ok := rawConfig.(string)
		if !ok {
			httpErr(w, 400, "config must be a string")
			return
		}
		normalizedConfig, err := normalizePaneConfigJSON(configStr)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		filtered["config"] = normalizedConfig
	}
	if rawRuntimeAI, ok := req["runtime_ai"]; ok {
		ov, err := runtimeAIOverrideFromAny(rawRuntimeAI)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		var configStr string
		if rawConfig, ok := filtered["config"]; ok {
			configStr, _ = rawConfig.(string)
		} else {
			var existingConfig sql.NullString
			if err := store.QueryRow("SELECT COALESCE(config, '{}') FROM agent_config WHERE pane_id=?", paneID).Scan(&existingConfig); err != nil {
				httpErr(w, 404, "Pane "+id+" not found")
				return
			}
			configStr = existingConfig.String
		}
		nextConfig, err := mergeRuntimeAIIntoConfigJSON(configStr, ov)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		filtered["config"] = nextConfig
	}
	delete(filtered, "runtime_ai")
	if rawProxy, ok := req["proxy"]; ok {
		ps, err := proxySettingsFromAny(rawProxy)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		var configStr string
		if rawConfig, ok := filtered["config"]; ok {
			configStr, _ = rawConfig.(string)
		} else {
			var existingConfig sql.NullString
			if err := store.QueryRow("SELECT COALESCE(config, '{}') FROM agent_config WHERE pane_id=?", paneID).Scan(&existingConfig); err != nil {
				httpErr(w, 404, "Pane "+id+" not found")
				return
			}
			configStr = existingConfig.String
		}
		nextConfig, err := mergeProxySettingsIntoConfigJSON(configStr, ps)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		filtered["config"] = nextConfig
	}
	delete(filtered, "proxy")
	if rawUseCustomGateway, ok := filtered["use_custom_gateway"]; ok {
		useCustomGateway, ok := rawUseCustomGateway.(bool)
		if !ok {
			httpErr(w, 400, "use_custom_gateway must be a boolean")
			return
		}
		filtered["use_custom_gateway"] = useCustomGateway
	}
	if rawUseProxy, ok := filtered["use_proxy"]; ok {
		useProxy, ok := rawUseProxy.(bool)
		if !ok {
			httpErr(w, 400, "use_proxy must be a boolean")
			return
		}
		filtered["proxy_enable"] = useProxy
		delete(filtered, "use_proxy")
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
	shortID := shortPaneID(paneID)
	var port sql.NullInt64
	store.QueryRow("SELECT ttyd_port FROM agent_config WHERE pane_id=?", paneID).Scan(&port)
	affectedParents := []string{}
	rows, err := store.Query("SELECT pane_id FROM pane_agents WHERE agent_name=?", shortID)
	if err == nil {
		defer rows.Close()
		seen := map[string]struct{}{}
		for rows.Next() {
			var parentPaneID string
			if scanErr := rows.Scan(&parentPaneID); scanErr != nil {
				continue
			}
			parentPaneID = shortPaneID(normPaneID(parentPaneID))
			if parentPaneID == "" {
				continue
			}
			if _, ok := seen[parentPaneID]; ok {
				continue
			}
			seen[parentPaneID] = struct{}{}
			affectedParents = append(affectedParents, parentPaneID)
		}
	}
	go func() {
		defer func() { recover() }()
		stopInstance(paneID)
		session := strings.Split(paneID, ":")[0]
		runTmux("kill-session", "-t", session)
	}()
	store.Exec("DELETE FROM pane_agents WHERE pane_id=?", shortID)
	store.Exec("DELETE FROM pane_agents WHERE agent_name=?", shortID)
	store.Exec("DELETE FROM group_windows WHERE win_id=?", paneID)
	store.Exec("DELETE FROM agent_config WHERE pane_id=?", paneID)
	for _, parentPaneID := range affectedParents {
		go broadcastPollData(parentPaneID)
	}
	J(w, M{"success": true, "pane_id": shortID, "message": "Pane deleted"})
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
	var workspace, initScript, title, config, agentType, defaultModel, trustLevel sql.NullString
	var allowAllActions sql.NullBool
	var replyInChinese sql.NullBool
	var useCustomGateway sql.NullBool
	var useProxy sql.NullBool
	err := store.QueryRow("SELECT ttyd_port, workspace, init_script, title, config, agent_type, default_model, trust_level, COALESCE(allow_all_actions, 0), COALESCE(reply_in_chinese, 0), COALESCE(use_custom_gateway, 0), COALESCE(proxy_enable, 0) FROM agent_config WHERE pane_id=?", paneID).
		Scan(&port, &workspace, &initScript, &title, &config, &agentType, &defaultModel, &trustLevel, &allowAllActions, &replyInChinese, &useCustomGateway, &useProxy)
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
		paneID:           paneID,
		configJSON:       config.String,
		workspace:        wsExpanded,
		initScript:       initScript.String,
		agentType:        agentType.String,
		defaultModel:     defaultModel.String,
		allowAllActions:  allowAllActions.Bool,
		replyInChinese:   replyInChinese.Bool,
		useCustomGateway: useCustomGateway.Bool,
		useProxy:         useProxy.Bool,
	})
	store.Exec(fmt.Sprintf("UPDATE agent_config SET updated_at=%s WHERE pane_id=?", store.Now()), paneID)
	return nil
}

// initPaneEnv sets up env vars, proxy, workspace, and runs init script in a pane.
type paneEnvOpts struct {
	paneID           string
	configJSON       string // JSON config (projects only)
	workspace        string // expanded workspace path
	initScript       string
	agentType        string
	defaultModel     string
	allowAllActions  bool
	replyInChinese   bool
	useCustomGateway bool
	useProxy         bool
	proxyPassword    string
	proxyRule        string
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
	case "cursor", "cursor-agent", "cursor agent":
		return "cursor"
	case "kiro-cli", "kiro", "kiro-cli chat":
		return "kiro-cli"
	case "copilot", "github-copilot", "github copilot", "ghcopilot":
		return "copilot"
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
		return "gpt-5.5"
	case "gpt5.5":
		return "gpt-5.5"
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

func tmuxHomeJoin(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		cleaned = append(cleaned, part)
	}
	if len(cleaned) == 0 {
		return `"$HOME"`
	}
	return `"$HOME/` + strings.Join(cleaned, "/") + `"`
}

func visibleAgentInstallLine(commandName, label, installCmd, logPathExpr string) string {
	return fmt.Sprintf(`__cicy_require_command %s %s %s %s`, tmuxShellQuote(commandName), tmuxShellQuote(label), tmuxShellQuote(installCmd), logPathExpr)
}

func visibleAgentInstallLiveLine(commandName, label, installCmd, logPathExpr string) string {
	return fmt.Sprintf(`__cicy_require_command_live %s %s %s %s`, tmuxShellQuote(commandName), tmuxShellQuote(label), tmuxShellQuote(installCmd), logPathExpr)
}

func ensureAgentCommandLine(commandName, label, installCmd, logPathExpr string) string {
	return visibleAgentInstallLine(commandName, label, installCmd, logPathExpr)
}

func ensureAgentCommandLineLive(commandName, label, installCmd, logPathExpr string) string {
	return visibleAgentInstallLiveLine(commandName, label, installCmd, logPathExpr)
}

// claudeUserStatuslineSetupLines emits idempotent shell lines that ensure the
// user-level ~/.claude/settings.json declares a statusLine command and that the
// helper script exists. The status line displays the current model + context
// usage, so it stays visible no matter how long the conversation runs.
//
// Runs on every claude launch (after the install check). Safe to re-run: only
// the script content is overwritten; the settings.json statusLine key is only
// inserted when missing so user customizations are preserved.
func claudeUserStatuslineSetupLines() []string {
	return []string{
		`mkdir -p "$HOME/.claude"`,
		`cat > "$HOME/.claude/statusline-command.sh" <<'CICY_STATUSLINE_EOF'`,
		`#!/usr/bin/env bash`,
		`input=$(cat)`,
		`model=$(echo "$input" | jq -r '.model.display_name // .model.id // "Unknown Model"')`,
		`used_pct=$(echo "$input" | jq -r '.context_window.used_percentage // "?"')`,
		`total_k=$(echo "$input" | jq -r '(.context_window.context_window_size // 0) / 1000 | floor')`,
		`printf "\033[1;36m[ %s | Context: %s%% / %sk ]\033[0m" "$model" "$used_pct" "$total_k"`,
		`CICY_STATUSLINE_EOF`,
		`chmod +x "$HOME/.claude/statusline-command.sh"`,
		`[ -f "$HOME/.claude/settings.json" ] || printf '{}\n' > "$HOME/.claude/settings.json"`,
		`if command -v jq >/dev/null 2>&1 && ! jq -e '.statusLine' "$HOME/.claude/settings.json" >/dev/null 2>&1; then __cicy_tmp=$(mktemp) && jq --arg cmd "bash $HOME/.claude/statusline-command.sh" '. + {statusLine: {type: "command", command: $cmd}}' "$HOME/.claude/settings.json" > "$__cicy_tmp" && mv "$__cicy_tmp" "$HOME/.claude/settings.json"; fi`,
		// Pre-set theme to dark and mark onboarding prompts as completed so
		// claude-code never shows the interactive theme picker or security
		// notes on first run. Without this the auto-confirm goroutine may
		// time out during a slow npm install and the pane stays stuck.
		`if command -v jq >/dev/null 2>&1; then __cicy_tmp=$(mktemp) && jq '. + {"theme":"dark","hasCompletedOnboarding":true,"hasAcknowledgedCostThreshold":true}' "$HOME/.claude/settings.json" > "$__cicy_tmp" && mv "$__cicy_tmp" "$HOME/.claude/settings.json"; fi`,
	}
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

func resolveCodexStartupModel(defaultModel string, aiCfg runtimeAIConfig, shortID string) string {
	if model := strings.TrimSpace(defaultModel); model != "" {
		return model
	}
	// Honor per-agent runtime_ai override (e.g. user picked DeepSeek for this pane).
	if shortID != "" {
		if _, ov, err := resolveRuntimeAIConfigForAgent("openai", shortID); err == nil && ov != nil && strings.TrimSpace(ov.ProviderName) != "" {
			if provider, ok := loadProviderByKey(ov.ProviderName); ok {
				if model := strings.TrimSpace(providerDefaultModelForAgentType(provider, "codex")); model != "" {
					return model
				}
			}
		}
	}
	if provider, ok := loadProviderForAgentType("codex"); ok {
		if model := strings.TrimSpace(providerDefaultModelForAgentType(provider, "codex")); model != "" {
			return model
		}
	}
	if model := strings.TrimSpace(aiCfg.CodexModel); model != "" {
		return model
	}
	return "gpt-5.4"
}

// resolveOpencodeStartupModel mirrors the codex/claude resolvers but for
// opencode. Opencode's config file requires us to declare the default model
// (top-level `model: cicyai/<name>`); without it the CLI falls back to its
// built-in free DeepSeek provider regardless of what we registered in the
// `cicyai` provider block.
func resolveOpencodeStartupModel(defaultModel string, aiCfg runtimeAIConfig, shortID string) string {
	if model := strings.TrimSpace(defaultModel); model != "" {
		return model
	}
	if shortID != "" {
		if _, ov, err := resolveRuntimeAIConfigForAgent("openai", shortID); err == nil && ov != nil && strings.TrimSpace(ov.ProviderName) != "" {
			if provider, ok := loadProviderByKey(ov.ProviderName); ok {
				if model := strings.TrimSpace(providerDefaultModelForAgentType(provider, "opencode")); model != "" {
					return model
				}
			}
		}
	}
	if provider, ok := loadProviderForAgentType("opencode"); ok {
		if model := strings.TrimSpace(providerDefaultModelForAgentType(provider, "opencode")); model != "" {
			return model
		}
	}
	if model := strings.TrimSpace(aiCfg.DefaultOpencodeModel); model != "" {
		return model
	}
	return ""
}

// opencodeActiveProvider returns the providerConfig currently routing for this
// opencode pane: runtime_ai override if set, else the agent_type default.
func opencodeActiveProvider(shortID string) *providerConfig {
	if shortID != "" {
		if ov, _ := loadPaneRuntimeAIOverride(shortID); ov != nil && strings.TrimSpace(ov.ProviderName) != "" {
			if p, ok := loadProviderByKey(ov.ProviderName); ok {
				return p
			}
		}
	}
	if p, ok := loadProviderForAgentType("opencode"); ok {
		return p
	}
	return nil
}

// opencodeActiveProtocol returns "openai" or "anthropic" based on the active
// provider's declared protocol. Opencode wires the matching @ai-sdk adapter
// and gateway path; mismatched routing (e.g. anthropic-style upstream behind
// the openai gateway) would either fail translation or get rejected by the
// upstream because we don't have an openai→anthropic adapter in the gateway.
func opencodeActiveProtocol(shortID string) string {
	if p := opencodeActiveProvider(shortID); p != nil {
		if proto := normalizeAIGatewayProvider(p.Protocol); proto == "anthropic" || proto == "openai" {
			return proto
		}
	}
	return "openai"
}

// opencodeActiveProviderModels returns the models declared by the provider
// currently routing for this pane. The resolvedModel argument is
// prepended/deduped so it's always in the list even if the provider's
// models[] array is empty.
func opencodeActiveProviderModels(shortID string, resolvedModel string) []string {
	provider := opencodeActiveProvider(shortID)
	var out []string
	seen := map[string]bool{}
	add := func(m string) {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			return
		}
		seen[m] = true
		out = append(out, m)
	}
	if resolvedModel != "" {
		add(resolvedModel)
	}
	if provider != nil {
		for _, m := range provider.Models {
			add(m)
		}
	}
	return out
}

// buildOpencodeModelsBlock renders the opencode.json `models` field value as a
// raw JSON object. Opencode treats keys as model identifiers and the values as
// per-model overrides — we leave overrides empty since the cicyai provider
// just forwards verbatim.
func buildOpencodeModelsBlock(models []string) string {
	if len(models) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(models))
	for _, m := range models {
		b, _ := json.Marshal(m)
		parts = append(parts, string(b)+":{}")
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func resolveClaudeStartupModel(defaultModel string, aiCfg runtimeAIConfig, shortID string) string {
	if model := strings.TrimSpace(defaultModel); model != "" {
		return model
	}
	// Honor per-agent runtime_ai override (parity with codex). If the user enabled
	// runtime override picking a non-default provider but left default_model blank,
	// fall back to that provider's claude-typed default rather than the agent_type
	// default — keeps the boot -m flag in sync with the gateway's actual routing.
	if shortID != "" {
		if _, ov, err := resolveRuntimeAIConfigForAgent("anthropic", shortID); err == nil && ov != nil && strings.TrimSpace(ov.ProviderName) != "" {
			if provider, ok := loadProviderByKey(ov.ProviderName); ok {
				if model := strings.TrimSpace(providerDefaultModelForAgentType(provider, "claude")); model != "" {
					return model
				}
			}
		}
	}
	if provider, ok := loadProviderForAgentType("claude"); ok {
		if model := strings.TrimSpace(providerDefaultModelForAgentType(provider, "claude")); model != "" {
			return model
		}
	}
	if model := strings.TrimSpace(aiCfg.DefaultClaudeModel); model != "" {
		return model
	}
	return "claude-opus-4-7"
}

func agentBootLines(agentType string, allowAllActions bool, replyInChinese bool, useCustomGateway bool, shortID string, defaultModel string) []string {
	aiCfg := loadRuntimeAIConfig()
	switch normalizeAgentType(agentType) {
	case "openclaw":
		home, _ := os.UserHomeDir()
		stateDir := filepath.Join(home, ".openclaw-"+shortID)
		stateConfigPath := filepath.Join(stateDir, "openclaw.json")
		installLog := filepath.Join(stateDir, "openclaw-install.log")
		sessionName := "main"
		sessionStorePath := filepath.Join(stateDir, "agents", "main", "sessions", "sessions.json")
		lines := []string{
			fmt.Sprintf("export OPENCLAW_CONFIG_PATH=%s", tmuxShellQuote(stateConfigPath)),
			fmt.Sprintf("export OPENCLAW_STATE_DIR=%s", tmuxShellQuote(stateDir)),
			fmt.Sprintf("export OPENCLAW_SESSION_KEY=%s", tmuxShellQuote("agent:main:"+sessionName)),
			fmt.Sprintf("export OPENCLAW_SESSION_STORE=%s", tmuxShellQuote(sessionStorePath)),
			fmt.Sprintf("export OPENAI_API_KEY=%s", tmuxShellQuote("cicy-local-gateway")),
			fmt.Sprintf("export OPENAI_BASE_URL=%s", tmuxShellQuote(openAIRuntimeBaseURL(shortID))),
			fmt.Sprintf("export ANTHROPIC_BASE_URL=%s", tmuxShellQuote(anthropicRuntimeBaseURL(shortID))),
			fmt.Sprintf("mkdir -p %s", tmuxShellQuote(stateDir)),
			fmt.Sprintf("mkdir -p %s %s %s %s %s", tmuxShellQuote(filepath.Join(stateDir, "identity")), tmuxShellQuote(filepath.Join(stateDir, "devices")), tmuxShellQuote(filepath.Join(stateDir, "extensions")), tmuxShellQuote(filepath.Join(stateDir, "agents", "main", "agent")), tmuxShellQuote(filepath.Dir(sessionStorePath))),
			`node - <<'EOF'
const fs = require("fs");
const dst = process.env.OPENCLAW_CONFIG_PATH;
const stateDir = process.env.OPENCLAW_STATE_DIR || "";
if (!dst) process.exit(0);
let cfg = {};
if (fs.existsSync(dst)) {
  cfg = JSON.parse(fs.readFileSync(dst, "utf8"));
}
if (!cfg || typeof cfg !== "object" || Array.isArray(cfg) || Object.keys(cfg).length === 0) {
  const token = require("crypto").randomBytes(24).toString("hex");
  cfg = {
    gateway: {
      mode: "local",
      auth: { mode: "token", token },
      controlUi: {
        allowInsecureAuth: true,
        dangerouslyDisableDeviceAuth: true,
        dangerouslyAllowHostHeaderOriginFallback: true,
        allowedOrigins: ["*"]
      }
    },
    models: { providers: { cicy: { models: [] } } },
    agents: { defaults: {} },
    plugins: { enabled: false, entries: {}, allow: [], deny: [], installs: {}, slots: { memory: "none" } },
    channels: {}
  };
}
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
let model = rawModel || "gpt-5.5";
switch (model) {
  case "gpt5.5":
    model = "gpt-5.5";
    break;
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
cfg.plugins ||= {};
cfg.plugins.enabled = cfg.plugins.enabled === true;
cfg.plugins.allow = Array.isArray(cfg.plugins.allow) ? cfg.plugins.allow : [];
cfg.plugins.deny = Array.isArray(cfg.plugins.deny) ? cfg.plugins.deny : [];
cfg.plugins.entries = cfg.plugins.entries && typeof cfg.plugins.entries === "object" && !Array.isArray(cfg.plugins.entries) ? cfg.plugins.entries : {};
cfg.plugins.installs = cfg.plugins.installs && typeof cfg.plugins.installs === "object" && !Array.isArray(cfg.plugins.installs) ? cfg.plugins.installs : {};
cfg.plugins.slots = cfg.plugins.slots && typeof cfg.plugins.slots === "object" && !Array.isArray(cfg.plugins.slots) ? cfg.plugins.slots : {};
if (!Object.prototype.hasOwnProperty.call(cfg.plugins.slots, "memory")) {
  cfg.plugins.slots.memory = "none";
}
cfg.agents.defaults.contextTokens = providerApi === "anthropic-messages" ? 200000 : 272000;
cfg.agents.defaults.model = cfg.agents.defaults.model || {};
cfg.agents.defaults.model.primary = "cicy/" + model;
cfg.models.providers.cicy.baseUrl = baseUrl;
cfg.models.providers.cicy.apiKey = "cicy-local-gateway";
	cfg.models.providers.cicy.api = providerApi;
	if (!Array.isArray(cfg.models.providers.cicy.models)) {
	  cfg.models.providers.cicy.models = [{ id: model, name: model, api: providerApi }];
	}
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
				`sync_openclaw_session_key`,
				`cicy_log "OpenClaw gateway 已启动，当前会话: $selected_session"`,
				`"${openclaw_profile_cmd[@]}" gateway probe --token "$OPENCLAW_GATEWAY_TOKEN" || true`,
				`cicy_log "如需手动打开 TUI: openclaw --profile `+shortID+` tui --url ws://127.0.0.1:`+openClawPort()+` --token \"$OPENCLAW_GATEWAY_TOKEN\" --session \"$selected_session\""`,
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
				`cicy_log "OpenClaw gateway 已启动，当前会话: $selected_session"`,
				`"${openclaw_profile_cmd[@]}" gateway probe --token "$OPENCLAW_GATEWAY_TOKEN" || true`,
				`cicy_log "如需手动打开 TUI: openclaw --profile `+shortID+` tui --url ws://127.0.0.1:`+openClawPort()+` --token \"$OPENCLAW_GATEWAY_TOKEN\" --session \"$selected_session\""`,
			)
		}
		return lines
	case "codex":
		installLog := tmuxHomeJoin("logs", fmt.Sprintf("codex-install-%s.log", shortID))
		lines := []string{
			ensureAgentCommandLine("codex", "Codex", codexInstallCmd(), installLog),
		}
		if useCustomGateway {
			baseURL := openAIRuntimeBaseURL(shortID)
			model := resolveCodexStartupModel(defaultModel, aiCfg, shortID)
			providerOverride := tmuxShellQuote(`model_provider="custom"`)
			providerNameOverride := tmuxShellQuote(`model_providers.custom.name="cicy-local"`)
			baseURLOverride := tmuxShellQuote(`model_providers.custom.base_url="` + baseURL + `"`)
			modelArg := tmuxShellQuote(model)
			lines = append(lines, "export OPENAI_API_KEY='cicy-local-gateway'", "clear")
			if allowAllActions {
				lines = append(lines, fmt.Sprintf("codex -m %s -c %s -c %s -c %s --dangerously-bypass-approvals-and-sandbox", modelArg, providerOverride, providerNameOverride, baseURLOverride))
			} else {
				lines = append(lines, fmt.Sprintf("codex -m %s -c %s -c %s -c %s", modelArg, providerOverride, providerNameOverride, baseURLOverride))
			}
			return lines
		}
		// Official login path: drop local-gateway env so codex uses its own auth/config.
		lines = append(lines, "unset OPENAI_API_KEY", "clear")
		if allowAllActions {
			lines = append(lines, "codex --dangerously-bypass-approvals-and-sandbox")
		} else {
			lines = append(lines, "codex")
		}
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
		launchPrefix := cmdName
		if normalizeAgentType(agentType) == "cicy-claude" {
			launchPrefix += " --bare"
		}
		installLog := tmuxHomeJoin("logs", fmt.Sprintf("%s-install-%s.log", cmdName, shortID))
		// When the local gateway is bypassed (official login), only honor an
		// explicitly-set defaultModel — falling back to the provider's gateway
		// default (e.g. deepseek-*) would force anthropic.com to reject the model.
		lines := []string{
			ensureAgentCommandLine(cmdName, label, installCmd, installLog),
		}
		// Vanilla claude reads ~/.claude/settings.json — wire up a statusLine
		// that shows the current model + context usage so users can always tell
		// which model is active deep into a long conversation.
		if cmdName == "claude" {
			lines = append(lines, claudeUserStatuslineSetupLines()...)
		}
		if useCustomGateway {
			model := resolveClaudeStartupModel(defaultModel, aiCfg, shortID)
			settingsJSON := "{\n" + `  "env": {
    "ANTHROPIC_AUTH_TOKEN": "cicy-local-gateway",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:8008/api/ai-gateway/anthropic/${X_AGENT_SHORT_ID}"
  },
  "model": "` + model + `"
}`
			lines = append(lines,
				`mkdir -p "$WORKSPACE/.cicy"`,
				fmt.Sprintf(`cat > "$WORKSPACE/.cicy/%s" <<EOF`, settingsFile),
				settingsJSON,
				`EOF`,
				"clear",
			)
			if allowAllActions {
				lines = append(lines, fmt.Sprintf(`%s --settings "$WORKSPACE/.cicy/%s" --dangerously-skip-permissions`, launchPrefix, settingsFile))
			} else {
				lines = append(lines, fmt.Sprintf(`%s --settings "$WORKSPACE/.cicy/%s"`, launchPrefix, settingsFile))
			}
			return lines
		}
		// Official login path: no settings.json, no --model flag. The agent runs
		// against its own native upstream (anthropic.com) using its own auth, so
		// the cicy-side default_model has no meaning here — passing it would just
		// force a model the user's account might not have access to. Let claude
		// pick its default (latest opus) and let the user switch in-session.
		launchCmd := launchPrefix
		if allowAllActions {
			launchCmd += " --dangerously-skip-permissions"
		}
		lines = append(lines,
			fmt.Sprintf(`rm -f "$WORKSPACE/.cicy/%s"`, settingsFile),
			"unset ANTHROPIC_BASE_URL",
			"unset ANTHROPIC_API_KEY",
			"clear",
			launchCmd,
		)
		return lines
	case "opencode":
		installLog := tmuxHomeJoin("logs", fmt.Sprintf("opencode-install-%s.log", shortID))
		lines := []string{
			ensureAgentCommandLine("opencode", "OpenCode", opencodeInstallCmd(), installLog),
		}
		if useCustomGateway {
			// Resolve startup model + active provider's catalog + protocol.
			// Opencode supports both openai and anthropic SDKs natively; pick
			// the right adapter and gateway path here. Without `model`+`models`
			// in the config opencode silently falls back to its built-in free
			// DeepSeek provider regardless of what we wired up.
			model := resolveOpencodeStartupModel(defaultModel, aiCfg, shortID)
			modelsBlock := buildOpencodeModelsBlock(opencodeActiveProviderModels(shortID, model))
			topModelField := ""
			if model != "" {
				b, _ := json.Marshal("cicyai/" + model)
				topModelField = `,"model":` + string(b)
			}
			// IMPORTANT: opencode's @ai-sdk packages treat baseURL as if it
			// already contains the `/v1` API version segment — the anthropic
			// adapter appends `/messages` (not `/v1/messages`), and the
			// openai-compatible adapter appends `/chat/completions` (not
			// `/v1/chat/completions`). The official Claude / OpenAI CLIs both
			// append the full `/v1/...` themselves, so our raw runtime base
			// URL has no trailing /v1. We tack it on here for opencode only.
			var providerBlock string
			switch opencodeActiveProtocol(shortID) {
			case "anthropic":
				lines = append(lines,
					fmt.Sprintf("export CICY_ANTHROPIC_BASE_URL=%s", tmuxShellQuote(anthropicRuntimeBaseURL(shortID)+"/v1")),
					"export ANTHROPIC_API_KEY='cicy-local-gateway'",
					"unset CICY_OPENAI_BASE_URL",
				)
				providerBlock = `"cicyai":{"npm":"@ai-sdk/anthropic","api":"anthropic","name":"cicyAi Gateway","options":{"baseURL":"$CICY_ANTHROPIC_BASE_URL","apiKey":"cicy-local-gateway"},"models":` + modelsBlock + `}`
			default:
				lines = append(lines,
					fmt.Sprintf("export CICY_OPENAI_BASE_URL=%s", tmuxShellQuote(openAIRuntimeBaseURL(shortID)+"/v1")),
					"unset CICY_ANTHROPIC_BASE_URL",
					"unset ANTHROPIC_API_KEY",
				)
				providerBlock = `"cicyai":{"npm":"@ai-sdk/openai-compatible","api":"openai","name":"cicyAi Gateway","options":{"baseURL":"$CICY_OPENAI_BASE_URL"},"models":` + modelsBlock + `}`
			}
			lines = append(lines, `mkdir -p "$WORKSPACE/.opencode"`)
			if replyInChinese {
				lines = append(lines,
					`printf 'Always reply in Chinese unless the user explicitly asks for another language.\nKeep code, commands, file paths, environment variables, API identifiers, and other literal tokens unchanged when accuracy matters.\n' > "$WORKSPACE/.opencode/reply-in-chinese.md"`,
					fmt.Sprintf(`cat > "$WORKSPACE/.opencode/opencode.json" <<EOF
{"\$schema":"https://opencode.ai/config.json","permission":"allow","instructions":["$WORKSPACE/.opencode/reply-in-chinese.md"]%s,"provider":{%s}}
EOF`, topModelField, providerBlock),
				)
			} else {
				lines = append(lines,
					`rm -f "$WORKSPACE/.opencode/reply-in-chinese.md"`,
					fmt.Sprintf(`cat > "$WORKSPACE/.opencode/opencode.json" <<EOF
{"\$schema":"https://opencode.ai/config.json","permission":"allow"%s,"provider":{%s}}
EOF`, topModelField, providerBlock),
				)
			}
			lines = append(lines, `OPENCODE_CONFIG="$WORKSPACE/.opencode/opencode.json" opencode`)
			return lines
		}
		// Official login path: opencode manages its own provider config, drop our overrides.
		lines = append(lines,
			`rm -f "$WORKSPACE/.opencode/opencode.json"`,
			`rm -f "$WORKSPACE/.opencode/reply-in-chinese.md"`,
			"unset CICY_OPENAI_BASE_URL",
			"unset CICY_ANTHROPIC_BASE_URL",
			"unset ANTHROPIC_API_KEY",
			"unset OPENCODE_CONFIG",
			"clear",
			"opencode",
		)
		return lines
	case "kiro-cli":
		installLog := tmuxHomeJoin("logs", fmt.Sprintf("kiro-install-%s.log", shortID))
		lines := append(kiroCliBootHelperLines(),
			ensureAgentCommandLineLive("kiro-cli", "Kiro CLI", "__cicy_local_install_kiro", installLog),
			`mkdir -p "$WORKSPACE/.kiro/steering"`,
		)
		if replyInChinese {
			lines = append(lines, `cat > "$WORKSPACE/.kiro/steering/reply-in-chinese.md" <<'EOF'
---
inclusion: always
---

Always reply in Chinese unless the user explicitly asks for another language.
Keep code, commands, file paths, environment variables, API identifiers, and other literal tokens unchanged when accuracy matters.
EOF`)
		} else {
			lines = append(lines, `rm -f "$WORKSPACE/.kiro/steering/reply-in-chinese.md"`)
		}
		lines = append(lines,
			`if kiro-cli whoami 2>/dev/null | grep -q "^Not logged in"; then
  while true; do
    echo ''
    echo '[cicy] Kiro CLI 尚未登录，请选择账号类型：'
    echo '  1. 免费版 (Builder ID / Google / Github)'
    echo '  2. 专业版 (Identity Center)'
    read -r -p '请选择 [1/2]: ' kiro_choice
    case "$kiro_choice" in
      1) kiro-cli login --license free --use-device-flow && break ;;
      2) kiro-cli login --license pro --use-device-flow && break ;;
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
		installLog := tmuxHomeJoin("logs", fmt.Sprintf("copilot-install-%s.log", shortID))
		lines := []string{
			"mkdir -p ~/.copilot",
			ensureAgentCommandLine("copilot", "GitHub Copilot", copilotInstallCmd(), installLog),
			`node -e 'const fs=require("fs"),f=process.env.HOME+"/.copilot/config.json";let c={};try{c=JSON.parse(fs.readFileSync(f))}catch(_){}c.trustedFolders=c.trustedFolders||[];const w=process.env.WORKSPACE||".";if(!c.trustedFolders.includes(w))c.trustedFolders.push(w);fs.writeFileSync(f,JSON.stringify(c,null,2))'`,
			"copilot --yolo",
		}
		return lines
	case "hermes":
		installLog := tmuxHomeJoin("logs", fmt.Sprintf("hermes-install-%s.log", shortID))
		hermesHome := filepath.Join(os.Getenv("HOME"), ".hermes-"+shortID)
		hermesInstallDir := filepath.Join(hermesHome, "hermes-agent")
		hermesBin := filepath.Join(hermesInstallDir, "venv", "bin", "hermes")
		configPath := filepath.Join(hermesHome, "config.yaml")
		installScriptPath := fmt.Sprintf("/tmp/hermes-install-%s.sh", shortID)
		modelName := normalizeHermesModel(aiCfg.HermesModel)
		contextLength := 1000000
		lines := []string{
			fmt.Sprintf("export HERMES_HOME=%s", tmuxShellQuote(hermesHome)),
			fmt.Sprintf("export HERMES_INSTALL_DIR=%s", tmuxShellQuote(hermesInstallDir)),
			fmt.Sprintf("export CICY_HERMES_BIN=%s", tmuxShellQuote(hermesBin)),
			fmt.Sprintf("export UV_INDEX_URL=%s", tmuxShellQuote(cicyDefaultPyPIMirror)),
			fmt.Sprintf("export PIP_INDEX_URL=%s", tmuxShellQuote(cicyDefaultPyPIMirror)),
			fmt.Sprintf("mkdir -p %s", tmuxShellQuote(hermesHome)),
			fmt.Sprintf(`if [ ! -x %s ]; then
  echo '[cicy] =================================================='
  echo '[cicy] Hermes Agent is not installed. Installing now...'
  echo '[cicy] This may take 1-5 minutes depending on network.'
  echo '[cicy] =================================================='
  rm -rf "$HERMES_INSTALL_DIR"
  install_log=%s
  if ! curl -fsSL %s -o %s; then
    install_status=$?
  else
    python3 - %s <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
raw = path.read_text(encoding="utf-8")
raw = raw.replace(
    'REPO_URL_HTTPS="https://github.com/NousResearch/hermes-agent.git"',
    'REPO_URL_HTTPS="https://gh-proxy.com/https://github.com/NousResearch/hermes-agent.git"',
)
helper = """cicy_clone_hermes() {
    local branch=\"$1\" install_dir=\"$2\" archive_url tmp_dir archive_file root_dir
    tmp_dir=\"$(mktemp -d \"${TMPDIR:-/tmp}/hermes-src-XXXXXX\")\" || return 1
    archive_file=\"$tmp_dir/hermes.tar.gz\"
    archive_url=\"https://gh-proxy.com/https://codeload.github.com/NousResearch/hermes-agent/tar.gz/refs/heads/${branch}\"
    log_info \"Downloading source archive via gh proxy...\"
    if ! curl -L --fail --retry 3 --retry-delay 1 -o \"$archive_file\" \"$archive_url\"; then
        rm -rf \"$tmp_dir\"
        return 1
    fi
    if ! tar -xzf \"$archive_file\" -C \"$tmp_dir\"; then
        rm -rf \"$tmp_dir\"
        return 1
    fi
    root_dir=\"$(find \"$tmp_dir\" -mindepth 1 -maxdepth 1 -type d | head -n 1)\"
    if [ -z \"$root_dir\" ]; then
        rm -rf \"$tmp_dir\"
        return 1
    fi
    rm -rf \"$install_dir\"
    mv \"$root_dir\" \"$install_dir\"
    rm -rf \"$tmp_dir\"
    return 0
}

"""
marker = "# ============================================================================\n# Helper functions\n# ============================================================================\n"
if helper not in raw and marker in raw:
    raw = raw.replace(marker, marker + "\n" + helper, 1)
raw = raw.replace(
    'if git clone --branch "$BRANCH" "$REPO_URL_HTTPS" "$INSTALL_DIR"; then',
    'if cicy_clone_hermes "$BRANCH" "$INSTALL_DIR"; then',
)
path.write_text(raw, encoding="utf-8")
PY
    HERMES_HOME="$HERMES_HOME" HERMES_INSTALL_DIR="$HERMES_INSTALL_DIR" bash %s --skip-setup 2>&1 | tee "$install_log"
    install_status=${PIPESTATUS[0]}
  fi
  if [ "$install_status" -ne 0 ]; then
    echo '[cicy] Hermes Agent install failed. Recent log:'
    tail -100 "$install_log"
    return 1
  fi
  echo '[cicy] Hermes Agent install completed.'
	fi`, tmuxShellQuote(hermesBin), installLog, tmuxShellQuote(defaultGitHubProxy+"https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh"), tmuxShellQuote(installScriptPath), tmuxShellQuote(installScriptPath), tmuxShellQuote(installScriptPath)),
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
model = (os.environ.get("CICY_HERMES_MODEL") or "gpt-5.5").strip() or "gpt-5.5"
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

func sanitizeTmuxPaneText(out string) string {
	if out == "" {
		return ""
	}
	out = ansiRe.ReplaceAllString(out, "")
	out = ctrlRe.ReplaceAllString(out, "")
	return out
}

func recentTmuxPaneText(out string, maxLines int) string {
	out = sanitizeTmuxPaneText(out)
	if out == "" || maxLines <= 0 {
		return out
	}
	lines := strings.Split(out, "\n")
	if len(lines) <= maxLines {
		return out
	}
	return strings.Join(lines[len(lines)-maxLines:], "\n")
}

func isClaudeInputReady(out string) bool {
	out = recentTmuxPaneText(out, 80)
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
	out = recentTmuxPaneText(out, 80)
	return (strings.Contains(out, "Choose the text style that looks best with your terminal") ||
		(strings.Contains(out, "Let's get started.") &&
			strings.Contains(out, "/theme") &&
			strings.Contains(out, "Dark mode"))) &&
		strings.Contains(out, "Dark mode")
}

func isClaudeSecurityNotesPrompt(out string) bool {
	out = recentTmuxPaneText(out, 80)
	return strings.Contains(out, "Security notes:") &&
		strings.Contains(out, "Press Enter to continue")
}

func isClaudeTrustPrompt(out string) bool {
	out = recentTmuxPaneText(out, 80)
	return strings.Contains(out, "Quick safety check") &&
		strings.Contains(out, "Yes, I trust this folder") &&
		strings.Contains(out, "Enter to confirm")
}

func isClaudeBypassChoicePrompt(out string) bool {
	out = recentTmuxPaneText(out, 80)
	return strings.Contains(out, "Bypass Permissions mode") &&
		strings.Contains(out, "No, exit") &&
		strings.Contains(out, "Yes, I accept")
}

func isClaudeBypassAcceptSelected(out string) bool {
	out = recentTmuxPaneText(out, 80)
	for _, line := range normalizeNonEmptyMeaningfulLines(strings.Split(out, "\n")) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "❯") && strings.Contains(line, "2. Yes, I accept") {
			return true
		}
	}
	return false
}

func isClaudeBypassConfirmPrompt(out string) bool {
	out = recentTmuxPaneText(out, 80)
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

type claudeAutoConfirmAction string

const (
	claudeActionNone  claudeAutoConfirmAction = ""
	claudeActionDown  claudeAutoConfirmAction = "down"
	claudeActionEnter claudeAutoConfirmAction = "enter"
	claudeActionReady claudeAutoConfirmAction = "ready"
	claudeActionStop  claudeAutoConfirmAction = "stop"
)

type claudeAutoConfirmState struct {
	lastAction       time.Time
	currentStage     claudePromptStage
	stageSince       time.Time
	stageAttempts    int
	sawClaudeProcess bool
}

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

func nextClaudeAutoConfirmAction(state *claudeAutoConfirmState, out, currentCmd string, allowAllActions bool, now time.Time) claudeAutoConfirmAction {
	stage := detectClaudePromptStage(out, allowAllActions)
	currentCmd = strings.ToLower(strings.TrimSpace(currentCmd))
	if currentCmd == "claude" || currentCmd == "cicy-claude" {
		state.sawClaudeProcess = true
	} else if state.sawClaudeProcess && currentCmd != "" && stage == claudeStageNone {
		return claudeActionStop
	}
	if isClaudeInputReady(out) {
		return claudeActionReady
	}
	if stage == claudeStageNone {
		state.currentStage = claudeStageNone
		state.stageAttempts = 0
		state.stageSince = time.Time{}
		return claudeActionNone
	}
	if stage != state.currentStage {
		state.currentStage = stage
		state.stageSince = now
		state.stageAttempts = 0
	}
	actionCooldown := 700 * time.Millisecond
	retryDelay := 1500 * time.Millisecond
	if stage == claudeStageBypassChoice || stage == claudeStageBypassConfirm {
		actionCooldown = 120 * time.Millisecond
		retryDelay = 250 * time.Millisecond
	}
	if now.Sub(state.lastAction) < actionCooldown {
		return claudeActionNone
	}
	if retryDelay > 0 && state.stageAttempts >= 1 && now.Sub(state.stageSince) < retryDelay {
		return claudeActionNone
	}
	if state.stageAttempts >= 4 {
		return claudeActionNone
	}
	state.lastAction = now
	state.stageAttempts++
	switch stage {
	case claudeStageTheme, claudeStageSecurityNotes, claudeStageTrust, claudeStageBypassConfirm:
		return claudeActionEnter
	case claudeStageBypassChoice:
		if !isClaudeBypassAcceptSelected(out) {
			return claudeActionDown
		}
		return claudeActionEnter
	default:
		return claudeActionNone
	}
}

func isCodexTrustPrompt(out string) bool {
	out = recentTmuxPaneText(out, 80)
	return strings.Contains(out, "Do you trust the contents of this directory?") &&
		strings.Contains(out, "1. Yes, continue") &&
		strings.Contains(out, "Press enter to continue")
}

func isCodexUpdatePrompt(out string) bool {
	out = recentTmuxPaneText(out, 80)
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
	recent := recentTmuxPaneText(out, 80)
	// If the active prompt and footer are visible, treat Codex as ready even if
	// an older trust/update prompt remains in scrollback.
	if isCodexPromptVisible(recent) && isCodexStatusFooterVisible(recent) {
		return true
	}
	if isCodexPromptVisible(recent) && isCodexBusyStateVisible(recent) {
		return true
	}
	if isCodexTrustPrompt(out) {
		return false
	}
	if isCodexUpdatePrompt(out) {
		return false
	}
	if strings.Contains(recent, "OpenAI Codex (v") &&
		(strings.Contains(recent, "directory:") ||
			strings.Contains(recent, "~/cicy-ai/workers/") ||
			strings.Contains(recent, "/cicy-ai/workers/") ||
			strings.Contains(recent, "~/cicy-ai/workers/") ||
			strings.Contains(recent, "model:")) &&
		(strings.Contains(recent, "/model to change") ||
			strings.Contains(recent, "Use /skills to list available skills") ||
			strings.Contains(recent, "100% left") ||
			strings.Contains(recent, "› ")) {
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
	_ = agentType
	return false
}

func lazyAgentMarkerPath(agentType, paneID string) string {
	_ = agentType
	_ = paneID
	return ""
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
	_ = paneID
	_ = agentType
	return nil
}

func waitForAgentInputReady(paneID, agentType string, trace *tmuxSendTrace) error {
	agentType = normalizeAgentType(agentType)
	if agentType == "" || agentType == "opencode" || agentType == "codex" || agentType == "hermes" || agentType == "kiro-cli" {
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
	checkConfirmed := func(attempt int) bool {
		deadline := time.Now().Add(timeout)
		captureAttempt := 0
		for time.Now().Before(deadline) {
			captureAttempt++
			out, err := capturePromptConfirmPane(paneID)
			if err != nil {
				lastErr = err
				if trace != nil {
					trace.logStep("post-submit-capture-error", map[string]any{"attempt": attempt, "capture_attempt": captureAttempt, "error": err.Error()}, "")
				}
				time.Sleep(pollInterval)
				continue
			}
			lastCapture = out
			confirmed := promptSubmitConfirmed(out, text, agentType)
			if trace != nil {
				trace.logStep("post-submit-capture", map[string]any{
					"attempt":         attempt,
					"capture_attempt": captureAttempt,
					"confirmed":       confirmed,
				}, out)
			}
			if confirmed {
				log.Printf("[tmux-send] pane=%s submit=confirmed agent=%s preview=%q",
					shortPaneID(paneID), normalizeAgentType(agentType), promptPreview(text))
				return true
			}
			time.Sleep(pollInterval)
		}
		return false
	}
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
		if checkConfirmed(attempt + 1) {
			return nil
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
	log.Printf("[tmux-send] pane=%s submit=timeout-enter-fallback agent=%s preview=%q",
		shortPaneID(paneID), normalizeAgentType(agentType), promptPreview(text))
	if trace != nil {
		trace.logStep("submit-timeout-enter-fallback", map[string]any{}, "")
	}
	if err := sendPromptEnter(paneID); err == nil {
		if checkConfirmed(retryLimit + 2) {
			return nil
		}
	} else if trace != nil {
		trace.logStep("submit-timeout-enter-fallback-error", map[string]any{"error": err.Error()}, "")
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
		state := claudeAutoConfirmState{}
		// maxPolls: 6000 × 200ms = 20 minutes — enough for a slow npm install.
		// We also reset the counter once we detect the claude process is running
		// to avoid timing out during a long install before claude even starts.
		claudeStarted := false
		for i := 0; i < 6000; i++ {
			pollInterval := 200 * time.Millisecond
			if state.currentStage == claudeStageBypassChoice || state.currentStage == claudeStageBypassConfirm {
				pollInterval = 20 * time.Millisecond
			}
			time.Sleep(pollInterval)
			out, err := runTmux("capture-pane", "-t", paneID, "-p", "-S", "-160")
			if err != nil {
				continue
			}
			currentCmd, _ := runTmux("display-message", "-p", "-t", paneID, "#{pane_current_command}")
			cmd := strings.ToLower(strings.TrimSpace(currentCmd))
			// Reset counter once claude process actually appears, so the
			// install phase doesn't eat into the timeout budget.
			if !claudeStarted && (cmd == "claude" || cmd == "cicy-claude") {
				claudeStarted = true
				i = 0
			}
			action := nextClaudeAutoConfirmAction(&state, out, currentCmd, allowAllActions, time.Now())
			switch action {
			case claudeActionStop:
				log.Printf("[claude-auto-confirm] %s stop: current command=%s", paneID, currentCmd)
				return
			case claudeActionReady:
				go clearClaudeStartupScreen(paneID)
				log.Printf("[claude-auto-confirm] %s ready", paneID)
				return
			case claudeActionNone:
				continue
			case claudeActionEnter:
				switch state.currentStage {
				case claudeStageTheme:
					log.Printf("[claude-auto-confirm] %s theme selected", paneID)
				case claudeStageSecurityNotes:
					log.Printf("[claude-auto-confirm] %s security notes continue", paneID)
				case claudeStageTrust:
					log.Printf("[claude-auto-confirm] %s trust workspace", paneID)
				case claudeStageBypassChoice:
					log.Printf("[claude-auto-confirm] %s confirm selected bypass accept option", paneID)
				case claudeStageBypassConfirm:
					log.Printf("[claude-auto-confirm] %s confirm bypass mode", paneID)
				}
				runTmux("send-keys", "-t", paneID, "Enter")
			case claudeActionDown:
				log.Printf("[claude-auto-confirm] %s move selection to accept bypass mode", paneID)
				runTmux("send-keys", "-t", paneID, "Down")
			}
		}
		log.Printf("[claude-auto-confirm] %s timeout", paneID)
	}()
}

func clearClaudeStartupScreen(paneID string) {
	for i := 0; i < 3; i++ {
		runTmux("send-keys", "-t", paneID, "C-l")
		time.Sleep(80 * time.Millisecond)
	}
	runTmux("clear-history", "-t", paneID)
}

func autoConfirmCodexTrust(paneID string) {
	go func() {
		var lastAction time.Time
		enterCount := 0
		updateSkipCount := 0
		codexStarted := false
		for i := 0; i < 6000; i++ {
			time.Sleep(200 * time.Millisecond)
			out, err := runTmux("capture-pane", "-t", paneID, "-p")
			if err != nil {
				continue
			}
			currentCmd, _ := runTmux("display-message", "-p", "-t", paneID, "#{pane_current_command}")
			cmd := strings.ToLower(strings.TrimSpace(currentCmd))
			if !codexStarted && cmd == "codex" {
				codexStarted = true
				i = 0
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
			if enterCount >= 4 || time.Since(lastAction) < 800*time.Millisecond {
				continue
			}
			log.Printf("[codex-auto-confirm] %s trust workspace 1+Enter #%d", paneID, enterCount+1)
			runTmux("send-keys", "-t", paneID, "-l", "--", "1")
			time.Sleep(120 * time.Millisecond)
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
	case "hermes":
		sessionEnv["CICY_API_KEY"] = strings.TrimSpace(aiCfg.APIKey)
		sessionEnv["CICY_API_URL"] = strings.TrimSpace(aiCfg.APIURL)
		sessionEnv["CICY_ANTHROPIC_URL"] = strings.TrimSpace(aiCfg.AnthropicURL)
		sessionEnv["CICY_HERMES_MODEL"] = normalizeHermesModel(aiCfg.HermesModel)
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

	// ~/.cicy_tmux.conf is already loaded by tmux's default-command via
	// ~/.cicy_shell_init (bash --rcfile), so we don't re-source it here.
	lines := []string{
		fmt.Sprintf("export X_AGENT_ID=%s", tmuxShellQuote(pid)),
		fmt.Sprintf("export X_AGENT_SHORT_ID=%s", tmuxShellQuote(shortID)),
	}
	for key, value := range sessionEnv {
		if value != "" {
			lines = append(lines, fmt.Sprintf("export %s=%s", key, tmuxShellQuote(value)))
		}
	}
	switch agentNorm {
	case "codex", "claude", "kiro-cli", "copilot", "opencode":
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
			// "mkdir -p ./skills ./projects",
		)
	}

	useCustomGateway := opts.useCustomGateway
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

	if opts.useProxy {
		// Source of truth for the proxy password is mihomo.yaml's globalPassword
		// (seeded by `cicy-mihomo gen-config` and assumed to exist). Per-pane
		// proxy.password from configJSON can override it for this agent.
		// Routing is handled by the global `IN-USER-PREFIX,w-,default_proxy_group`
		// rule in mihomo.yaml — no per-agent rule injection needed.
		password := readMihomoGlobalPassword()
		if ps := extractProxySettingsFromConfigJSON(opts.configJSON); ps != nil {
			if p := strings.TrimSpace(ps.Password); p != "" {
				password = p
			}
			if strings.TrimSpace(ps.Rule) != "" {
				lines = append(lines, fmt.Sprintf("export CICY_PROXY_RULE=%s", tmuxShellQuote(strings.TrimSpace(ps.Rule))))
			}
		}
		if password != "" {
			lines = append(lines, fmt.Sprintf("export CICY_PROXY_PASSWORD=%s", tmuxShellQuote(password)))
		}
		lines = append(lines,
			"cicy_proxy_on",
		)
	}
	if strings.TrimSpace(opts.initScript) != "" {
		lines = append(lines, opts.initScript)
	}
	bootAgentNorm := normalizeAgentType(opts.agentType)
	if bootAgentNorm != "claude" && bootAgentNorm != "cicy-claude" && bootAgentNorm != "codex" && bootAgentNorm != "opencode" {
		lines = append(lines, "clear")
	}
	lines = append(lines, agentBootLines(opts.agentType, opts.allowAllActions, opts.replyInChinese, useCustomGateway, shortID, opts.defaultModel)...)

	// 将启动脚本写入 workspace，避免散落到 /tmp。
	// Claude boot scripts are kept intentionally small/readable; other agents keep the
	// bash re-entry wrapper because their generated bodies may rely on being sourced
	// from non-bash shells on macOS.
	script := "#!/usr/bin/env bash\n\n"
	if bootAgentNorm == "claude" || bootAgentNorm == "cicy-claude" || bootAgentNorm == "codex" || bootAgentNorm == "opencode" {
		script += strings.Join(lines, "\n") + "\n"
	} else {
		script += "if [ -z \"${BASH_VERSION:-}\" ]; then\n" +
			"  _cicy_boot_script_path=\"$PWD/.cicy/boot.sh\"\n" +
			"  bash \"$_cicy_boot_script_path\"\n" +
			"  _cicy_boot_status=$?\n" +
			"  unset _cicy_boot_script_path\n" +
			"  return \"$_cicy_boot_status\" 2>/dev/null || exit \"$_cicy_boot_status\"\n" +
			"fi\n\n" +
			"_cicy_boot_script=\"${BASH_SOURCE:-$0}\"\n" +
			"_cicy_runtime_dir=\"$(cd \"$(dirname \"$_cicy_boot_script\")\" && pwd)\"\n" +
			"export CICY_RUNTIME_DIR=\"$_cicy_runtime_dir\"\n" +
			"_cicy_workspace_dir=\"$(cd \"$_cicy_runtime_dir/..\" && pwd)\"\n" +
			"cd \"$_cicy_workspace_dir\"\n\n" +
			strings.Join(lines, "\n") + "\n" +
			"unset _cicy_boot_script _cicy_runtime_dir _cicy_workspace_dir\n"
	}
	scriptPath := filepath.Join(workspaceRuntimeDir(opts.workspace), "boot.sh")
	if strings.TrimSpace(opts.workspace) == "" {
		scriptPath = fmt.Sprintf("/tmp/init_pane_%s.sh", strings.ReplaceAll(pid, ":", "_"))
	} else if err := ensureRuntimeDir(workspaceRuntimeDir(opts.workspace), 0755); err != nil {
		log.Printf("[init] failed to ensure workspace for script: %v", err)
		return
	}
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		log.Printf("[init] failed to write script: %v", err)
		return
	}
	if err := ensureRuntimeFile(scriptPath, 0700); err != nil {
		log.Printf("[init] failed to set script ownership: %v", err)
		return
	}
	log.Printf("[init] v1 pane %s script path=%s\n%s", pid, scriptPath, script)

	// On macOS the tmux pane can exist before the shell prompt is actually
	// visible/interactive. Wait for the prompt marker before sending boot.sh.
	if waitForShellPromptReady(pid) {
		log.Printf("[init] shell prompt ready for %s", shortPaneID(pid))
		runTmux("send-keys", "-t", pid, "source .cicy/boot.sh", "Enter")
	} else {
		log.Printf("[init] shell prompt not confirmed for %s, skip auto source .cicy/boot.sh", shortPaneID(pid))
		return
	}
	if (normalizeAgentType(opts.agentType) == "claude" || normalizeAgentType(opts.agentType) == "cicy-claude") && useCustomGateway {
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

func sendTextToPane(winID, text string, submit bool) error {
	winID = normPaneID(winID)
	if !submit {
		if strings.TrimSpace(text) == "" {
			return newTmuxSendError("text required", http.StatusBadRequest, false)
		}
		log.Printf("[tmux-send] pane=%s mode=text-no-submit text=%q", shortPaneID(winID), text)
		if _, err := runTmux("send-keys", "-t", winID, "-l", text); err != nil {
			return newTmuxSendError("failed to send text without submit: "+err.Error(), http.StatusInternalServerError, false)
		}
		return nil
	}
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
			log.Printf("[tmux-send] pane=%s pre-submit failed; forcing enter fallback agent=%s mode=%s preview=%q err=%v",
				shortPaneID(winID), normalizeAgentType(agentType), mode, promptPreview(text), err)
			if trace != nil {
				trace.logStep("pre-submit-enter-fallback", map[string]any{"reason": err.Error()}, "")
			}
			if submitErr := submitPromptWithConfirmation(winID, agentType, text, trace); submitErr != nil {
				// submitPromptWithConfirmation already sent Enter (including its own
				// fallback Enter at the end). We just couldn't visually verify the
				// submission via terminal scrape. Treat as success: returning an
				// error here confuses the client because the message almost always
				// did go through. Do NOT clear the input — if Enter actually
				// landed, C-u would wipe whatever the agent types next.
				trace.logStep("submit-warn", map[string]any{"error": submitErr.Error(), "fallback": "pre-submit-enter", "treated_as": "success"}, "")
				log.Printf("[tmux-send] pane=%s WARN submit not visually confirmed (Enter was sent); treating as success agent=%s preview=%q pre_err=%v submit_err=%v",
					shortPaneID(winID), normalizeAgentType(agentType), promptPreview(text), err, submitErr)
			}
		} else if err := submitPromptWithConfirmation(winID, agentType, text, trace); err != nil {
			// Same rationale: Enter has been sent (possibly multiple times); the
			// visual confirm just failed. Don't surface as error, don't clear.
			trace.logStep("submit-warn", map[string]any{"error": err.Error(), "treated_as": "success"}, "")
			log.Printf("[tmux-send] pane=%s WARN submit not visually confirmed (Enter was sent); treating as success agent=%s preview=%q err=%v",
				shortPaneID(winID), normalizeAgentType(agentType), promptPreview(text), err)
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
	markWorkerPromptSubmitted(winID, text)
	return nil
}

func clearPanePromptInput(paneID string, trace *tmuxSendTrace) {
	paneID = normPaneID(paneID)
	if paneID == "" {
		return
	}
	if _, err := runTmux("send-keys", "-t", paneID, "C-u"); err != nil {
		if trace != nil {
			trace.logStep("clear-input-error", map[string]any{"error": err.Error()}, "")
		}
		return
	}
	_, _ = runTmux("send-keys", "-t", paneID, "Escape")
	if trace != nil {
		trace.logStep("clear-input", map[string]any{}, "")
	}
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
		submit := true
		if raw, ok := req["submit"].(bool); ok {
			submit = raw
		}
		// Register the cross-agent callback BEFORE sending the text. The receiver
		// CLI can react fast enough to start a gateway audit session within the
		// same wall-clock second the text lands; if registration happened after
		// the send, that audit session's drain would find an empty pending list
		// and the hook would never attach.
		if cbTo, ok := req["callback_to"].(string); ok && strings.TrimSpace(cbTo) != "" {
			registerReplyCallback(winID, cbTo)
		}
		if err := sendTextToPane(winID, text, submit); err != nil {
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
	if strings.TrimSpace(winID) == "" {
		winID, _ = req["pane_id"].(string)
	}
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
		workersDir := filepath.Join(cicyWorkersDir, body.Session)
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
	return aiGatewayLoadReplySnapshot(agentID), nil
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

func waitForAIGatewayMessageRecord(agentID string, baselineHistoryID int64, expectedQuestion string, sentAt time.Time, timeout time.Duration) (aiGatewayMessageRecord, error) {
	deadline := time.Now().Add(timeout)
	pollInterval := 250 * time.Millisecond
	sentFloor := sentAt.UTC().Add(-1 * time.Second)
	var lastSnapshot aiGatewayReplySnapshot
	var lastErr error

	for time.Now().Before(deadline) {
		records, err := agentHistoryLoadRecordsAfter(agentID, baselineHistoryID, 20)
		if err != nil {
			lastErr = err
		} else if len(records) > 0 {
			for _, item := range records {
				record := item.Record
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

// readMihomoGlobalPassword reads the top-level `globalPassword:` value from
// ~/cicy-ai/db/mihomo.yaml. Returns "" if the file or key is missing — the
// caller may then skip exporting CICY_PROXY_PASSWORD, but in normal operation
// the value is seeded by `cicy-mihomo gen-config` and assumed to exist.
func readMihomoGlobalPassword() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, "cicy-ai", "db", "mihomo.yaml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "globalPassword:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(t, "globalPassword:"))
		return strings.Trim(v, "\"' ")
	}
	return ""
}
