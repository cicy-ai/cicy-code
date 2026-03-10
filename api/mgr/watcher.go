package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
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
	fullSyncInterval = 30 * time.Second
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
	spinnerRe = regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]`)
	ctxRe     = regexp.MustCompile(`(\d+)%?\s*!?>\s*$`)
	credRe    = regexp.MustCompile(`Credits:\s*([\d.]+)`)
	elapRe    = regexp.MustCompile(`Time:\s*(\d+)s`)
)

func initWatcher() {
	pipeLogDir = os.Getenv("PIPE_LOG_DIR")
	if pipeLogDir == "" {
		pipeLogDir = filepath.Join(os.Getenv("HOME"), "projects/ai-workers/fast-api/logs")
	}
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
	s = strings.ReplaceAll(s, "\r", "")
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
	out, _ := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out))
}

func sessExists(sess string) bool {
	for _, s := range strings.Split(tmuxCmd("list-sessions", "-F", "#{session_name}"), "\n") {
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

	if !sessExists(sess) {
		return paneSt{PaneID: clean, Active: false, CheckT: now}
	}

	mtime := pipeMtime(paneID)
	raw := readPipeLog(paneID)
	if raw == "" {
		t := paneID
		if !strings.Contains(t, ":") {
			t += ":main.0"
		}
		raw = tmuxCmd("capture-pane", "-t", t, "-p")
		n := time.Now().Unix()
		mtime = &n
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
	idle := strings.HasSuffix(last, ">") || strings.HasSuffix(last, "$")

	s := ""
	if wa {
		s = "wait_auth"
	} else if co {
		s = "compacting"
	} else if th && !idle {
		s = "thinking"
	} else if idle {
		s = "idle"
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
	t := paneID
	if !strings.Contains(t, ":") {
		t += ":main.0"
	}
	if tmuxCmd("display-message", "-t", t, "-p", "#{pane_pipe}") == "0" {
		f := filepath.Join(pipeLogDir, "pipe-"+strings.NewReplacer(":", "_", ".", "_").Replace(t)+".log")
		tmuxCmd("pipe-pane", "-t", t, "cat >> "+f)
		return true
	}
	return false
}

func refreshCfgCache() {
	rows, err := db.Query("SELECT pane_id, title, COALESCE(agent_type,''), COALESCE(role,''), COALESCE(default_model,''), COALESCE(trust_level,'') FROM ttyd_config WHERE active=1")
	if err != nil {
		return
	}
	defer rows.Close()
	m := map[string]map[string]string{}
	for rows.Next() {
		var pid, title, at, role, model, trust string
		rows.Scan(&pid, &title, &at, &role, &model, &trust)
		m[pid] = map[string]string{"title": title, "agent_type": at, "role": role, "default_model": model, "trust_level": trust}
	}
	watcherMu.Lock()
	cfgCache = m
	watcherMu.Unlock()
}

// raw TCP redis GET/SET
func redisDo(cmds ...string) string {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	conn, err := net.DialTimeout("tcp", host+":"+port, 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := fmt.Sprintf("*%d\r\n", len(cmds))
	for _, c := range cmds {
		req += fmt.Sprintf("$%d\r\n%s\r\n", len(c), c)
	}
	conn.Write([]byte(req))

	// read full response
	var all []byte
	buf := make([]byte, 64*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			all = append(all, buf[:n]...)
		}
		// check if we have complete RESP response
		resp := string(all)
		if strings.HasPrefix(resp, "$") {
			idx := strings.Index(resp, "\r\n")
			if idx >= 0 {
				sz, _ := strconv.Atoi(resp[1:idx])
				if sz < 0 {
					return "" // $-1 = nil
				}
				need := idx + 2 + sz + 2 // $N\r\n<data>\r\n
				if len(all) >= need {
					return resp[idx+2 : idx+2+sz]
				}
			}
		} else if strings.HasPrefix(resp, "+") || strings.HasPrefix(resp, "-") {
			if strings.Contains(resp, "\r\n") {
				idx := strings.Index(resp, "\r\n")
				return resp[1:idx]
			}
		}
		if err != nil {
			break
		}
	}
	return string(all)
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

	statusMap := map[string]paneSt{}
	restored := 0
	for pid, cfg := range cache {
		if ensurePipe(pid) {
			restored++
		}
		st := checkPane(pid, cfg)
		// thinking protection
		if prev, ok := prevMap[pid]; ok {
			var p struct {
				Status string `json:"status"`
			}
			json.Unmarshal(prev, &p)
			if p.Status == "thinking" && st.Status != nil && *st.Status != "idle" && *st.Status != "wait_auth" && *st.Status != "compacting" {
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
		log.Printf("[watcher] full sync: %d panes, restored %d pipe-pane", len(statusMap), restored)
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
		if p.Status == "thinking" && st.Status != nil && *st.Status != "idle" && *st.Status != "wait_auth" && *st.Status != "compacting" {
			st.Status = sp("thinking")
		}
	}
	b, _ := json.Marshal(st)
	m[paneID] = b
	data, _ := json.Marshal(m)
	redisDo("SET", "pane_status_map", string(data))

	if st.Status != nil && *st.Status != "idle" && *st.Status != "" {
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
