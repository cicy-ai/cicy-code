package main

import (
	"bufio"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"ttyd-go/backend/localcommand"
	"ttyd-go/server"
)

var (
	upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	saasMode    bool
	publicMode  bool
	devMode     bool
	desktopMode bool
	desktopCmd  *exec.Cmd
)

func isSaasMode() bool {
	return os.Getenv("SAAS_MODE") == "1"
}

//go:embed tmux.conf
var embeddedTmuxConf string

// parseTmuxConfVersion extracts version number from "# cicy-code tmux.conf vN" header
func parseTmuxConfVersion(content string) int {
	re := regexp.MustCompile(`# cicy-code tmux\.conf v(\d+)`)
	m := re.FindStringSubmatch(content)
	if len(m) == 2 {
		v := 0
		fmt.Sscanf(m[1], "%d", &v)
		return v
	}
	return 0
}

// ensureTmuxConf checks ~/.tmux.conf version against bundled version, prompts user to update
func ensureTmuxConf() {
	home, _ := os.UserHomeDir()
	confPath := filepath.Join(home, ".tmux.conf")

	bundledVer := parseTmuxConfVersion(embeddedTmuxConf)
	if bundledVer == 0 {
		return
	}

	existing, err := os.ReadFile(confPath)
	if err != nil {
		// No existing file, just write it
		os.WriteFile(confPath, []byte(embeddedTmuxConf), 0644)
		log.Printf("[startup] created ~/.tmux.conf (v%d)", bundledVer)
		reloadTmuxConf(confPath)
		return
	}

	existingVer := parseTmuxConfVersion(string(existing))
	if existingVer >= bundledVer {
		return // up to date
	}

	// Prompt user
	if existingVer == 0 {
		fmt.Printf("⚠️  检测到已有 ~/.tmux.conf（非 cicy-code 版本），内置版本 v%d 修复了终端兼容性问题。\n", bundledVer)
	} else {
		fmt.Printf("⚠️  ~/.tmux.conf 版本 v%d → 内置 v%d，新版修复了终端兼容性问题。\n", existingVer, bundledVer)
	}
	fmt.Print("是否更新？(Y/n): ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	if input == "" || input == "y" || input == "yes" {
		// Backup old config
		backupPath := confPath + ".bak"
		os.WriteFile(backupPath, existing, 0644)
		os.WriteFile(confPath, []byte(embeddedTmuxConf), 0644)
		log.Printf("[startup] updated ~/.tmux.conf v%d → v%d (backup: %s)", existingVer, bundledVer, backupPath)
		reloadTmuxConf(confPath)
	} else {
		log.Printf("[startup] skipped ~/.tmux.conf update (user declined)")
	}
}

func reloadTmuxConf(confPath string) {
	exec.Command("tmux", "source-file", confPath).Run()
}

func checkEnv() {
	// Ensure tmux is in PATH (macOS Homebrew)
	if _, err := exec.LookPath("tmux"); err != nil {
		for _, p := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
			if _, e := os.Stat(p + "/tmux"); e == nil {
				os.Setenv("PATH", p+":"+os.Getenv("PATH"))
				break
			}
		}
	}

	// tmux required
	if _, err := exec.LookPath("tmux"); err != nil {
		log.Fatalf("[startup] missing required dependency: tmux")
	}

	// Ensure ~/.tmux.conf is up to date
	ensureTmuxConf()

	// Check if first run (no agent workers in DB)
	var count int
	store.QueryRow("SELECT COUNT(*) FROM agent_config WHERE agent_type != ''").Scan(&count)
	if count == 0 {
		runSetup()
	}

	// Ensure all builtin agents are running
	ensureBuiltinAgents()

	ensureCodeServer()
}

var csCmd *exec.Cmd

func ensureCodeServer() {
	// Check if installed
	path, err := exec.LookPath("code-server")
	if err != nil {
		log.Println("[code-server] not found, installing...")
		var cmd *exec.Cmd
		if _, err := exec.LookPath("brew"); err == nil {
			cmd = exec.Command("brew", "install", "code-server")
		} else {
			cmd = exec.Command("sh", "-c", "curl -fsSL https://code-server.dev/install.sh | sh")
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("[code-server] install failed: %v", err)
		}
		path, _ = exec.LookPath("code-server")
	}

	// Check if running
	csPort := "18080" // code-server fixed port

	out, _ := exec.Command("sh", "-c", fmt.Sprintf("lsof -i:%s -t 2>/dev/null || pgrep -f 'code-server.*%s'", csPort, csPort)).Output()
	if len(strings.TrimSpace(string(out))) == 0 {
		log.Printf("[code-server] starting on port %s...", csPort)
		csCmd = exec.Command(path, "--bind-addr", "127.0.0.1:"+csPort, "--auth", "none")
		csCmd.Stdout = nil
		csCmd.Stderr = nil
		csCmd.Start()
		time.Sleep(2 * time.Second)
	}
	log.Printf("[code-server] ready on port %s", csPort)
}

const version = "0.2.3"

func main() {
	// Parse flags
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--version", "-v":
			fmt.Println("cicy-code " + version)
			os.Exit(0)
		case "--help", "-h":
			fmt.Println(`cicy-code - AI-powered development environment

Usage: cicy-code [options]

Options:
  --help, -h    Show this help
  --cn          Use Chinese mirrors (npm + GitHub proxy)
  --dev         Development mode (load resources from filesystem, use COS)
  --desktop     Desktop mode (Electron + RPC/MCP on port 18101)
  --saas        Enable SaaS mode (or SAAS_MODE=1)
  --public      Listen on 0.0.0.0 (default: 127.0.0.1)
  --audit       Enable mitmproxy audit mode

Environment:
  PORT          API port (default: 18008 local, 8008 saas)
  SQLITE_PATH   SQLite database path (default: ~/.cicy/data.db)
  KV_PATH       KV cache path (default: ~/.cicy/kv.json)
  MYSQL_DSN     MySQL connection string

Data directory: ~/.cicy/`)
			os.Exit(0)
		case "--cn":
			os.Setenv("CN_MIRROR", "1")
		case "--dev":
			devMode = true
		case "--saas":
			os.Setenv("SAAS_MODE", "1")
		case "--public":
			publicMode = true
		case "--audit":
			auditMode = true
		case "--desktop":
			desktopMode = true
		}
	}

	saasMode = isSaasMode()

	initKV()
	initRedis()
	initDB()
	store.Migrate()
	defer store.Close()

	if !saasMode {
		checkEnv()
		if auditMode {
			initAudit()
		}
		// checkEnv 完成后才启动 watcher，避免和 setup 竞争创建 worker
		go startWatcher()
		go startTmuxHealth()
		ensureDesktop()
	} else {
		// SaaS 模式直接启动
		if auditMode {
			initAudit()
		}
		go startWatcher()
		go startTmuxHealth()
	}
	http.HandleFunc("/health", w(handleHealth))
	http.HandleFunc("/api/health", w(handleHealth))
	http.HandleFunc("/api/ping", w(handlePing))
	http.HandleFunc("/api/mode", w(handleMode))

	// Chat
	http.HandleFunc("/api/chat/push", wa(handleChatPush))

	// Auth
	http.HandleFunc("/api/auth/verify", w(handleAuthVerify))
	http.HandleFunc("/api/auth/verify-token", w(handleAuthVerifyToken))
	http.HandleFunc("/api/auth/tokens", wa(handleAuthTokens))
	http.HandleFunc("/api/auth/tokens/", wa(handleAuthTokenDelete))

	// Provision SSE
	http.HandleFunc("/api/provision/stream", corsM(handleProvisionStream))

	// Resolve slug → backend_url (for CF Worker)
	http.HandleFunc("/api/resolve", w(handleResolve))
	http.HandleFunc("/api/vm-token", w(handleVMToken))
	http.HandleFunc("/api/auth/exchange", corsM(handleAuthExchange))

	// SaaS OAuth
	if githubEnabled() {
		http.HandleFunc("/api/auth/github", w(handleGithubAuth))
		http.HandleFunc("/api/auth/github/callback", w(handleGithubCallback))
	}
	if googleEnabled() {
		http.HandleFunc("/api/auth/google", w(handleGoogleAuth))
		http.HandleFunc("/api/auth/google/callback", w(handleGoogleCallback))
	}
	http.HandleFunc("/api/auth/saas/verify", w(handleSaasVerify))
	http.HandleFunc("/api/auth/saas/me", w(handleSaasMe))

	// Tmux panes
	http.HandleFunc("/api/tmux/panes", wa(handlePanes))
	http.HandleFunc("/api/tmux/list", wa(handleTmuxList))
	http.HandleFunc("/api/tmux/create", wa(handleCreatePane))
	http.HandleFunc("/api/tmux/send", wa(handleSend))
	http.HandleFunc("/api/tmux/send-keys", wa(handleSendKeys))
	http.HandleFunc("/api/tmux/send_wait", wa(handleSendWait))
	http.HandleFunc("/api/tmux/capture_pane", wa(handleCapture))
	http.HandleFunc("/api/tmux/tree", wa(handleTree))
	http.HandleFunc("/api/tmux/windows", wa(handleWindows))
	http.HandleFunc("/api/tmux/status", wa(handleStatus))
	http.HandleFunc("/api/tmux/restart_all", wa(handleRestartAll))
	http.HandleFunc("/api/tmux/clear", wa(handleClear))

	// Mouse
	http.HandleFunc("/api/tmux/mouse/on", wa(handleMouseToggle))
	http.HandleFunc("/api/tmux/mouse/off", wa(handleMouseToggle))
	http.HandleFunc("/api/tmux/mouse/status", wa(handleMouseStatus))

	// TTYD status
	http.HandleFunc("/api/tmux/ttyd/status/", wa(handleTtydStatus))

	// Pane CRUD
	http.HandleFunc("/api/tmux/panes/", wa(handlePaneByID))
	http.HandleFunc("/api/tmux/pair", wa(handlePair))

	// Agents
	http.HandleFunc("/api/agents/pane/", wa(handleAgentsByPane))
	http.HandleFunc("/api/agents/bind", wa(handleAgentBind))
	http.HandleFunc("/api/agents/unbind/", wa(handleAgentUnbind))

	// Queue
	http.HandleFunc("/api/workers/queue", wa(handleQueue))
	http.HandleFunc("/api/workers/queue/", wa(handleQueueByID))

	// Groups
	http.HandleFunc("/api/groups", wa(handleGroups))
	http.HandleFunc("/api/groups/", wa(handleGroupByID))

	// Nodes
	http.HandleFunc("/api/nodes", wa(handleNodes))
	http.HandleFunc("/api/nodes/exec", wa(handleNodeExec))

	// Settings
	http.HandleFunc("/api/settings/global", wa(handleSettings))

	// Audit (mitmproxy management)
	http.HandleFunc("/api/audit/status", wa(handleAuditStatus))
	http.HandleFunc("/api/audit/start", wa(handleAuditStart))
	http.HandleFunc("/api/audit/stop", wa(handleAuditStop))
	http.HandleFunc("/api/audit/restart", wa(handleAuditRestart))
	http.HandleFunc("/api/audit/addons", wa(handleAuditAddons))
	http.HandleFunc("/api/audit/rules", wa(handleAuditRules))

	// Audit Dashboard (SaaS multi-tenant)
	http.HandleFunc("/api/audit/register", wa(handleAuditRegister))
	http.HandleFunc("/api/audit/dashboard", wa(handleAuditDashboard))
	http.HandleFunc("/api/audit/usage", wa(handleAuditUsage))
	http.HandleFunc("/api/audit/admin/overview", wa(handleAuditAdminOverview))
	http.HandleFunc("/api/audit/live", corsM(func(w http.ResponseWriter, r *http.Request) {
		t := r.URL.Query().Get("token")
		if t == "" || !verifyToken(t) {
			httpErr(w, 401, "Not authenticated")
			return
		}
		handleAuditLive(w, r)
	}))

	// Audit Public (CA cert, install script, setup guide)
	http.HandleFunc("/ca.pem", corsM(handleCACert))
	http.HandleFunc("/install-ca", corsM(handleInstallCA))
	http.HandleFunc("/setup", corsM(handleSetupGuide))

	// Stats
	http.HandleFunc("/api/stats/traffic", wa(handleStatsTraffic))
	http.HandleFunc("/api/stats/traffic/raw", wa(handleStatsTrafficRaw))
	http.HandleFunc("/api/stats/chat", wa(handleChatHistory))
	http.HandleFunc("/api/stats/chat/stream", wa(handleChatStream))

	// Chat V2 — WebSocket
	http.HandleFunc("/api/chat/ws", handleChatWS)
	http.HandleFunc("/api/chat/debug", wa(handleChatDebug))
	http.HandleFunc("/api/chat/webhook", corsM(handleChatWebhook))
	http.HandleFunc("/api/stats/traffic/live", corsM(func(w http.ResponseWriter, r *http.Request) {
		// SSE needs query token since EventSource can't set headers
		t := r.URL.Query().Get("token")
		if t == "" || !verifyToken(t) {
			httpErr(w, 401, "Not authenticated")
			return
		}
		handleTrafficLive(w, r)
	}))
	http.HandleFunc("/api/notify", wa(handleNotify))
	http.HandleFunc("/api/cicy/files", wa(handleCicyFiles))
	http.HandleFunc("/api/cicy/file", wa(handleCicyFile))
	http.HandleFunc("/api/notify/stream", corsM(func(w http.ResponseWriter, r *http.Request) {
		t := r.URL.Query().Get("token")
		if t == "" || !verifyToken(t) {
			httpErr(w, 401, "Not authenticated")
			return
		}
		handleNotifyStream(w, r)
	}))

	// TTS
	http.HandleFunc("/api/tts", wa(handleTTS))

	// Utils
	http.HandleFunc("/api/utils/file/exists", wa(handleFileExists))
	http.HandleFunc("/api/correctEnglish", wa(handleAICorrect))

	// AI
	http.HandleFunc("/api/ai/chat", wa(handleAIChat))
	http.HandleFunc("/api/ai/chat/stream", corsM(handleAIChatStream))
	http.HandleFunc("/v1/chat/completions", handleV1ChatCompletions)
	http.HandleFunc("/v1/models", handleV1Models)
	http.HandleFunc("/stt", wa(handleSTT))
	http.HandleFunc("/api/apps/create", corsM(handleCreateApp))
	http.HandleFunc("/api/apps", corsM(handleListApps))
	http.HandleFunc("/api/apps/", corsM(handleServeApp))
	http.HandleFunc("/api/ai/correct", wa(handleAICorrect))

	// Telegram
	http.HandleFunc("/api/tg/send", wa(handleTGSend))
	http.HandleFunc("/api/tg/photo", wa(handleTGPhoto))

	// Code-server proxy (token auth only for root, assets bypass)
	http.HandleFunc("/code/", corsM(handleCodeServerAuth))

	// Mitmproxy Web UI proxy
	http.HandleFunc("/mitm/", corsM(handleMitmproxyAuth))

	// phpMyAdmin proxy
	http.HandleFunc("/pma/", corsM(handlePmaAuth))

	// Desktop (electron-mcp) RPC proxy: /api/desktop/* → 127.0.0.1:18101
	http.HandleFunc("/api/desktop/", wa(handleDesktopProxy))
	http.HandleFunc("/api/desktop/status", wa(handleDesktopStatus))

	// XUI proxy: /api/xui/{pane_id}/... → node xui
	http.HandleFunc("/api/xui/", wa(handleXuiProxy))

	// OAuth Google
	http.HandleFunc("/oauth/start", handleOAuthStart)
	http.HandleFunc("/oauth/callback", handleOAuthCallback)

	// WebSocket
	http.HandleFunc("/api/ws/", wa(handleWSProxy))

	// Static files (custom gotty-bundle.js etc)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// TTYD proxy - serve ttyd-go instances at /ttyd/{pane_id}/
	http.HandleFunc("/ttyd/", handleTtydProxy)

	// Embedded UI (SPA fallback)
	uiHandler := serveUI()
	defaultHandler := http.DefaultServeMux
	http.DefaultServeMux = http.NewServeMux()
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// API/特殊路径走原有 handler
		for _, prefix := range []string{"/api/", "/ttyd/", "/code/", "/mitm/", "/pma/", "/static/", "/v1/", "/oauth/", "/stt", "/health", "/ca.pem", "/install-ca", "/setup"} {
			if strings.HasPrefix(r.URL.Path, prefix) || r.URL.Path == prefix {
				defaultHandler.ServeHTTP(w, r)
				return
			}
		}
		// audit.cicy-ai.com → audit SPA
		if strings.Contains(r.Host, "audit") {
			handleAuditSPA(w, r)
			return
		}
		// 其他走嵌入 UI
		uiHandler.ServeHTTP(w, r)
	})

	port := os.Getenv("PORT")
	if port == "" {
		if saasMode {
			port = "8008"
		} else {
			port = "18008"
		}
	}

	mode := "local"
	if saasMode {
		mode = "saas"
	}
	kvMode := "memory"
	if useRedis {
		kvMode = "redis"
	} else if kv.file != "" {
		kvMode = "file:" + kv.file
	}
	log.Printf("[startup] mode=%s port=%s db=%s kv=%s", mode, port, store.Driver, kvMode)

	// 注册默认 hook：thinking→idle 时通知 master
	RegisterHook(func(paneID string, old, new paneSt) {
		if old.Status != nil && *old.Status == "thinking" && new.Status != nil && *new.Status == "idle" {
			// Auto-dispatch queued messages
			go dispatchQueue(paneID)

			shortPane := shortPaneID(paneID)

			// Find master panes that have this worker bound
			rows, err := store.Query(`SELECT pa.pane_id FROM pane_agents pa WHERE pa.agent_name=? AND pa.status='active'`, shortPane)
			if err != nil {
				return
			}
			defer rows.Close()
			for rows.Next() {
				var masterPane string
				rows.Scan(&masterPane)

				// 1. Send to master CLI via tmux (core: master CLI handles dispatch/review)
				tmuxCmd("send-keys", "-t", masterPane+":main.0", "-l", fmt.Sprintf("pane_idle:%s", shortPane))
				tmuxCmd("send-keys", "-t", masterPane+":main.0", "Enter")

				// 2. Also notify ChatView UI
				hub.broadcast(masterPane, ChatEvent{
					Type: "worker_idle",
					Data: M{
						"protocol": "cicy/v1",
						"from":     shortPane,
						"type":     "task_result",
						"data": M{
							"worker":  shortPane,
							"status":  "idle",
							"message": fmt.Sprintf("Worker %s finished (thinking → idle)", shortPane),
						},
					},
				})
				log.Printf("[hook] notified master %s (tmux+chatbus): worker %s idle", masterPane, shortPane)
			}
		}
	})

	initHTTPLogConsumer()
	bind := "127.0.0.1"
	if publicMode {
		bind = "0.0.0.0"
	}
	log.Printf("cicy-code-api starting on %s:%s", bind, port)

	// Graceful shutdown: stop code-server on exit
	if !saasMode {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			log.Println("[shutdown] stopping...")
			if csCmd != nil && csCmd.Process != nil {
				log.Println("[shutdown] stopping code-server")
				csCmd.Process.Kill()
			}
			if auditMode {
				stopMitmproxy()
			}
			os.Exit(0)
		}()
	}

	// Auto-open browser with token
	if !saasMode {
		go func() {
			time.Sleep(500 * time.Millisecond)
			token := getFirstToken()
			host := "localhost"
			if publicMode {
				host = "0.0.0.0"
			}
			url := fmt.Sprintf("http://%s:%s", host, port)
			if token != "" {
				url += "/?token=" + token
				fmt.Printf("\n🔑 Token: %s\n", token)
			}
			fmt.Printf("🌐 URL: %s\n\n", url)
			exec.Command("open", url).Start()           // macOS
			exec.Command("xdg-open", url).Start()       // Linux
		}()
	}

	log.Fatal(http.ListenAndServe(bind+":"+port, nil))
}

func getFirstToken() string {
	home, _ := os.UserHomeDir()
	gpath := filepath.Join(home, "global.json")

	// 读取或创建 global.json
	cfg := map[string]interface{}{}
	if data, err := os.ReadFile(gpath); err == nil {
		json.Unmarshal(data, &cfg)
	}

	// 有 token 直接返回
	if t, ok := cfg["api_token"].(string); ok && t != "" {
		return t
	}

	// 生成 token 并写入
	b := make([]byte, 16)
	rand.Read(b)
	token := "cicy_" + hex.EncodeToString(b)
	cfg["api_token"] = token
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(gpath, data, 0644)
	return token
}

func ensureBuiltinAgents() {
	token := getFirstToken()
	// Ensure all builtin agents in DB have tmux sessions and ttyd running
	rows, err := store.Query("SELECT pane_id, ttyd_port, workspace FROM agent_config WHERE ttyd_port >= 10001 AND ttyd_port <= 10006")
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var paneID, workspace string
		var port int
		rows.Scan(&paneID, &port, &workspace)
		session := strings.Split(paneID, ":")[0]

		// Ensure tmux session
		if exec.Command("tmux", "has-session", "-t", session).Run() != nil {
			if workspace == "" {
				home, _ := os.UserHomeDir()
				workspace = filepath.Join(home, "workers", session)
			}
			os.MkdirAll(workspace, 0755)
			exec.Command("tmux", "new-session", "-d", "-s", session, "-n", "main", "-c", workspace).Run()
			log.Printf("[startup] recreated tmux session %s", session)
		}

		// Ensure ttyd
		if !isPortListening(port) {
			if err := startInstance(paneID, port, token); err != nil {
				log.Printf("[startup] ttyd start error for %s: %v", paneID, err)
				continue
			}
			waitPort(port, 10*time.Second)
		}
		log.Printf("[startup] agent %s ready (ttyd :%d)", paneID, port)
	}
}

// w = cors only, wa = cors + auth
func w(h http.HandlerFunc) http.HandlerFunc  { return corsM(h) }
func wa(h http.HandlerFunc) http.HandlerFunc { return corsM(authM(h)) }

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	J(w, M{
		"ping":    "Pong",
		"data":    "cicy-code-api",
		"version": "1.0.0",
	})
}
func handleHealth(w http.ResponseWriter, r *http.Request) {
	J(w, M{"status": "ok", "source": "cicy-code-api"})
}
func handleMode(w http.ResponseWriter, r *http.Request) {
	mode := "local"
	if saasMode { mode = "saas" }
	J(w, M{"mode": mode})
}
func handlePing(w http.ResponseWriter, r *http.Request) {
	J(w, M{"pong": "ok", "version": "2026.0316.1", "server_datetime": time.Now().Format(time.RFC3339)})
}

// Placeholder to avoid unused import
var _ = context.Background
var _ = fmt.Sprintf
var _ = exec.Command
var _ = filepath.Join
var _ = strings.TrimSpace
var _ = json.Marshal
var _ server.Options
var _ localcommand.Options
