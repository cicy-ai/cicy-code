package main

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

var (
	pipeLogDir       = ""
	compactThreshold = 70
	fullSyncInterval = 3 * time.Second
	actionCooldown   = 3 * time.Second
	watcherMu        sync.Mutex
	cooldownMap      = map[string]time.Time{}
	cfgCache         = map[string]map[string]string{}

	ansiRe = regexp.MustCompile(`\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])`)
	ctrlRe = regexp.MustCompile(`[\x00-\x08\x0B-\x0C\x0E-\x1F\x7F-\x9F]`)
	kiroRe = []*regexp.Regexp{
		regexp.MustCompile(`I will run the following command`),
		regexp.MustCompile(`Purpose:`),
		regexp.MustCompile(`\(using tool:`),
		regexp.MustCompile(`Looking up symbols`),
		regexp.MustCompile(`Found.*symbols`),
		regexp.MustCompile(`Completed in.*s`),
	}
	ocRe = []*regexp.Regexp{
		regexp.MustCompile(`(?i)▣.*Build.*trinity`),
		regexp.MustCompile(`(?i)Build.*Trinity.*OpenCode`),
		regexp.MustCompile(`█▀▀█ █▀▀█ █▀▀█`),
		regexp.MustCompile(`(?i)Ask anything`),
	}
	spinnerRe     = regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]`)
	idlePromptRe  = regexp.MustCompile(`\d+%?\s*!?>\s`)
	ctxRe         = regexp.MustCompile(`(\d+)%?\s*!?>\s*$`)
	credRe    = regexp.MustCompile(`Credits:\s*([\d.]+)`)
	elapRe    = regexp.MustCompile(`Time:\s*(\d+)s`)
)

func initWatcher() {
	pipeLogDir = os.Getenv("PIPE_LOG_DIR")
	if pipeLogDir == "" {
		home, _ := os.UserHomeDir()
		pipeLogDir = filepath.Join(home, "logs")
	}
	os.MkdirAll(pipeLogDir, 0755)
	if v := os.Getenv("COMPACT_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			compactThreshold = n
		}
	}
}

func paneFromFile(name string) string {
	if !strings.HasPrefix(name, "pipe-") || !strings.HasSuffix(name, ".log") {
		return ""
	}
	raw := name[5 : len(name)-4]
	parts := strings.Split(raw, "_")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[:len(parts)-2], "_") + ":" + parts[len(parts)-2] + "." + parts[len(parts)-1]
}

func readPipeLog(paneID string) string {
	if !strings.Contains(paneID, ":") {
		paneID += ":main.0"
	}
	f := filepath.Join(pipeLogDir, "pipe-"+strings.NewReplacer(":", "_", ".", "_").Replace(paneID)+".log")
	out, err := exec.Command("tail", "-c", "32768", f).Output()
	if err != nil {
		return ""
	}
	s := ansiRe.ReplaceAllString(string(out), "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	// Simulate terminal: \r overwrites current line, but keep prompt lines
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		parts := strings.Split(line, "\r")
		// Check if any part contains idle prompt pattern
		best := parts[len(parts)-1]
		for _, p := range parts {
			if strings.Contains(p, "% >") || strings.Contains(p, "% λ") {
				best = p
				break
			}
		}
		lines = append(lines, best)
	}
	s = strings.Join(lines, "\n")
	return ctrlRe.ReplaceAllString(s, "")
}

func pipeMtime(paneID string) *int64 {
	if !strings.Contains(paneID, ":") {
		paneID += ":main.0"
	}
	f := filepath.Join(pipeLogDir, "pipe-"+strings.NewReplacer(":", "_", ".", "_").Replace(paneID)+".log")
	info, err := os.Stat(f)
	if err != nil {
		return nil
	}
	t := info.ModTime().Unix()
	return &t
}

func tmuxCmd(args ...string) string {
	paneID := extractPaneID(args)
	if paneID != "" {
		out, _ := nodeTmux(paneID, args...)
		return out
	}
	out, _ := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out))
}

func sessExists(paneID, sess string) bool {
	out, _ := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	for _, s := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if s == sess {
			return true
		}
	}
	return false
}

func guessAgent(text string, lines []string) string {
	sample := text
	if len(sample) > 2000 {
		sample = sample[len(sample)-2000:]
	}
	for _, r := range kiroRe {
		if r.MatchString(sample) {
			return "kiro-cli"
		}
	}
	for _, r := range ocRe {
		if r.MatchString(sample) {
			return "opencode"
		}
	}
	if len(lines) > 0 {
		l := strings.TrimRight(lines[len(lines)-1], " ")
		if strings.HasSuffix(l, "$") || strings.HasSuffix(l, "#") {
			return "shell"
		}
	}
	return "unknown"
}

type paneSt struct {
	PaneID    string      `json:"pane_id"`
	Active    bool        `json:"active"`
	AgentType *string     `json:"agent_type"`
	Status    *string     `json:"status"`
	IsThink   *bool       `json:"isThinking"`
	IsWait    *bool       `json:"isWaitingAuth"`
	IsCompact *bool       `json:"isCompacting"`
	IsWaitSt  *bool       `json:"isWaitStartup"`
	IsIdle    *bool       `json:"isIdle"`
	CtxUsage  *int        `json:"contextUsage"`
	Credits   *float64    `json:"credits"`
	Elapsed   *int        `json:"elapsedTime"`
	Raw       *string     `json:"raw"`
	CurTask   *string     `json:"currentTask"`
	Guess     *string     `json:"guess"`
	LogEx     *bool       `json:"log_exists,omitempty"`
	LastUpd   *int64      `json:"lastUpdateAt"`
	CheckT    int64       `json:"checkTime"`
	TimeAgo   interface{} `json:"timeAgo"`
	Title     *string     `json:"title"`
	Role      *string     `json:"role,omitempty"`
	DefModel  *string     `json:"default_model,omitempty"`
	TrustLvl  *string     `json:"trust_level,omitempty"`
}

func sp(s string) *string   { return &s }
func bp(b bool) *bool       { return &b }
func ip(i int) *int         { return &i }
func fp(f float64) *float64 { return &f }

func checkPane(paneID string, cfg map[string]string) paneSt {
	clean := strings.Replace(paneID, ":main.0", "", 1)
	now := time.Now().Unix()
	sess := strings.Split(paneID, ":")[0]

	if !sessExists(paneID, sess) {
		return paneSt{PaneID: clean, Active: false, CheckT: now}
	}

	// 分布式: 直接通过 xui capture-pane 获取内容
	t := paneID
	if !strings.Contains(t, ":") {
		t += ":main.0"
	}
	// 优先读 pipe-pane log（实时），fallback 到 capture-pane
	raw := readPipeLog(paneID)
	ts := now
	mtime := pipeMtime(paneID)
	if mtime == nil {
		mtime = &ts
	}
	if raw == "" {
		out, _ := exec.Command("tmux", "capture-pane", "-t", t, "-p").Output()
		raw = strings.TrimSpace(string(out))
		mtime = &ts
	}
	if raw == "" {
		return paneSt{PaneID: clean, Active: true, LogEx: bp(false), CheckT: now, LastUpd: mtime}
	}

	var nonEmpty []string
	for _, l := range strings.Split(raw, "\n") {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	n := 4
	if len(nonEmpty) < n {
		n = len(nonEmpty)
	}
	text := strings.Join(nonEmpty[len(nonEmpty)-n:], "\n")
	last2start := len(nonEmpty) - 2
	if last2start < 0 {
		last2start = 0
	}
	last2 := strings.Join(nonEmpty[last2start:], " ")

	guess := guessAgent(raw, nonEmpty)
	det := guess
	if det == "unknown" && cfg != nil && cfg["agent_type"] != "" {
		det = cfg["agent_type"]
	}

	st := parsePane(clean, det, text, last2, nonEmpty)
	st.Active = true
	st.LogEx = bp(true)
	st.Guess = sp(guess)
	st.LastUpd = mtime
	st.CheckT = now
	if cfg != nil {
		if v := cfg["agent_type"]; v != "" {
			st.AgentType = &v
		}
		if v := cfg["title"]; v != "" {
			st.Title = &v
		}
		if v := cfg["role"]; v != "" {
			st.Role = &v
		}
		if v := cfg["default_model"]; v != "" {
			st.DefModel = &v
		}
		if v := cfg["trust_level"]; v != "" {
			st.TrustLvl = &v
		}
	}
	if mtime != nil {
		st.TimeAgo = now - *mtime
	}
	return st
}

func parsePane(pid, atype, text, last2 string, lines []string) paneSt {
	st := paneSt{PaneID: pid}
	last := ""
	if len(lines) > 0 {
		last = strings.TrimRight(lines[len(lines)-1], " ")
	}
	wa := strings.Contains(last2, "Allow this action") || strings.Contains(last2, "[y/n/t]")
	co := strings.Contains(last2, "Creating summary") || strings.Contains(last2, "/compact")
	th := spinnerRe.MatchString(last2)
	idle := strings.HasSuffix(last, ">") || strings.HasSuffix(last, "$") || idlePromptRe.MatchString(last) || strings.Contains(last, "% >")

	s := ""
	if wa {
		s = "wait_auth"
	} else if co {
		s = "compacting"
	} else if th && !idle {
		s = "thinking"
	} else if idle {
		s = "idle"
	} else if atype == "kiro-cli" && !idle {
		// kiro-cli running but spinner not captured → still thinking
		s = "thinking"
	}
	st.Status = &s
	st.IsThink = bp(th || co)
	st.IsWait = bp(wa)
	st.IsCompact = bp(co)
	st.IsIdle = bp(idle)
	st.Raw = &text

	if atype == "kiro-cli" || atype == "" {
		if m := ctxRe.FindAllStringSubmatch(text, -1); len(m) > 0 {
			if v, err := strconv.Atoi(m[len(m)-1][1]); err == nil {
				st.CtxUsage = ip(v)
			}
		}
		if m := credRe.FindStringSubmatch(last2); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				st.Credits = fp(v)
			}
		}
		if m := elapRe.FindStringSubmatch(last2); m != nil {
			if v, err := strconv.Atoi(m[1]); err == nil {
				st.Elapsed = ip(v)
			}
		}
	}
	return st
}

func autoAction(pid string, st paneSt) {
	if st.Status == nil || *st.Status == "" {
		return
	}
	now := time.Now()
	watcherMu.Lock()
	last := cooldownMap[pid]
	watcherMu.Unlock()
	if now.Sub(last) < actionCooldown {
		return
	}
	target := pid
	if !strings.Contains(pid, ":") {
		target += ":main.0"
	}
	switch *st.Status {
	case "wait_auth":
		log.Printf("[watcher] %s: wait_auth → t", pid)
		watcherMu.Lock()
		cooldownMap[pid] = now
		watcherMu.Unlock()
		tmuxCmd("send-keys", "-t", target, "t")
		time.Sleep(500 * time.Millisecond)
		tmuxCmd("send-keys", "-t", target, "Enter")
	case "idle":
		if st.CtxUsage != nil && *st.CtxUsage > compactThreshold {
			log.Printf("[watcher] %s: ctx=%d%% → /compact", pid, *st.CtxUsage)
			watcherMu.Lock()
			cooldownMap[pid] = now
			watcherMu.Unlock()
			tmuxCmd("send-keys", "-t", target, "-l", "/compact")
			time.Sleep(300 * time.Millisecond)
			tmuxCmd("send-keys", "-t", target, "Enter")
		}
	}
}

func ensurePipe(paneID string) bool {
	target := paneID
	if !strings.Contains(target, ":") {
		target += ":main.0"
	}
	logFile := filepath.Join(pipeLogDir, "pipe-"+strings.NewReplacer(":", "_", ".", "_").Replace(target)+".log")
	if info, err := os.Stat(logFile); err == nil && time.Since(info.ModTime()) < 2*fullSyncInterval {
		return false
	}
	exec.Command("tmux", "pipe-pane", "-t", target, "-o", "cat >> "+logFile).Run()
	return true
}

func refreshCfgCache() {
	rows, err := store.Query("SELECT pane_id, title, COALESCE(agent_type,''), COALESCE(role,''), COALESCE(default_model,''), COALESCE(trust_level,''), COALESCE(ttyd_port,0), COALESCE(workspace,'') FROM agent_config WHERE active=1")
	if err != nil {
		return
	}
	defer rows.Close()
	m := map[string]map[string]string{}
	for rows.Next() {
		var pid, title, at, role, model, trust, ws string
		var port int
		rows.Scan(&pid, &title, &at, &role, &model, &trust, &port, &ws)
		m[pid] = map[string]string{"title": title, "agent_type": at, "role": role, "default_model": model, "trust_level": trust, "ttyd_port": strconv.Itoa(port), "workspace": ws}
	}
	watcherMu.Lock()
	cfgCache = m
	watcherMu.Unlock()
}

func fullSyncOnce() {
	refreshCfgCache()
	watcherMu.Lock()
	cache := cfgCache
	watcherMu.Unlock()

	prevRaw := redisDo("GET", "pane_status_map")
	prevMap := map[string]json.RawMessage{}
	if prevRaw != "" {
		json.Unmarshal([]byte(prevRaw), &prevMap)
	}

	token := getFirstToken()
	statusMap := map[string]paneSt{}
	restored := 0
	for pid, cfg := range cache {
		// Ensure tmux session exists
		session := strings.Split(pid, ":")[0]
		if exec.Command("tmux", "has-session", "-t", session).Run() != nil {
			log.Printf("[watcher] session %s missing, creating locally", session)
			ws := cfg["workspace"]
			if ws == "" { ws = os.Getenv("HOME") }
			ws = strings.Replace(ws, "~", os.Getenv("HOME"), 1)
			exec.Command("tmux", "new-session", "-d", "-s", session, "-n", "main", "-c", ws).Run()
		}
		// Ensure ttyd instance is running
		if port, _ := strconv.Atoi(cfg["ttyd_port"]); port > 0 {
			if !isPortListening(port) {
				log.Printf("[watcher] ttyd port %d not listening for %s, starting instance", port, pid)
				startInstance(pid, port, token)
			}
		}
		if ensurePipe(pid) {
			restored++
		}
		st := checkPane(pid, cfg)
		// thinking protection: only keep thinking if new status is non-empty and not a terminal state
		if prev, ok := prevMap[pid]; ok {
			var p struct {
				Status string `json:"status"`
			}
			json.Unmarshal(prev, &p)
			if p.Status == "thinking" && st.Status != nil && *st.Status != "idle" && *st.Status != "wait_auth" && *st.Status != "compacting" && *st.Status != "" {
				st.Status = sp("thinking")
			}
		}
		statusMap[pid] = st
		autoAction(pid, st)
		// trigger hook
		b, _ := json.Marshal(st)
		diffStatus(pid, prevMap[pid], b)
	}
	data, _ := json.Marshal(statusMap)
	redisDo("SET", "pane_status_map", string(data))
	if restored > 0 {
		if os.Getenv("DEBUG") != "" {
			log.Printf("[watcher] full sync: %d panes, restored %d pipe-pane", len(statusMap), restored)
		}
	}
}

func processOne(paneID string) {
	watcherMu.Lock()
	cfg := cfgCache[paneID]
	watcherMu.Unlock()

	st := checkPane(paneID, cfg)

	raw := redisDo("GET", "pane_status_map")
	m := map[string]json.RawMessage{}
	if raw != "" {
		json.Unmarshal([]byte(raw), &m)
	}
	oldRaw := m[paneID]
	if prev, ok := m[paneID]; ok {
		var p struct {
			Status string `json:"status"`
		}
		json.Unmarshal(prev, &p)
		// thinking protection: only keep thinking if new status is non-empty and not a terminal state
		if p.Status == "thinking" && st.Status != nil && *st.Status != "idle" && *st.Status != "wait_auth" && *st.Status != "compacting" && *st.Status != "" {
			st.Status = sp("thinking")
		}
	}
	b, _ := json.Marshal(st)
	m[paneID] = b
	data, _ := json.Marshal(m)
	redisDo("SET", "pane_status_map", string(data))

	// Only log on status change to avoid log spam
	if oldRaw != nil {
		var prev struct{ Status string `json:"status"` }
		json.Unmarshal(oldRaw, &prev)
		if st.Status != nil && *st.Status != prev.Status {
			log.Printf("[watcher] %s: %s → %s", paneID, prev.Status, *st.Status)
		}
	} else if st.Status != nil && *st.Status != "" {
		log.Printf("[watcher] %s: %s", paneID, *st.Status)
	}
	autoAction(paneID, st)
	diffStatus(paneID, oldRaw, b)
}

func startWatcher() {
	initWatcher()
	log.Printf("[watcher] started | log_dir=%s interval=%s", pipeLogDir, fullSyncInterval)

	fullSyncOnce()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[watcher] fsnotify error: %v", err)
		return
	}
	w.Add(pipeLogDir)

	debounce := map[string]time.Time{}
	var dmu sync.Mutex

	go func() {
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Op&fsnotify.Write == 0 {
					continue
				}
				pid := paneFromFile(filepath.Base(ev.Name))
				if pid == "" {
					continue
				}
				dmu.Lock()
				if time.Since(debounce[pid]) < 500*time.Millisecond {
					dmu.Unlock()
					continue
				}
				debounce[pid] = time.Now()
				dmu.Unlock()
				go processOne(pid)
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Printf("[watcher] error: %v", err)
			}
		}
	}()

	for {
		time.Sleep(fullSyncInterval)
		fullSyncOnce()
	}
}
