package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

var (
	upgrader    = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	publicMode  bool
	devMode     bool
	desktopMode bool
	auditMode   bool
	cnMirror    bool
	desktopCmd  *exec.Cmd
)

const version = "0.2.12"

// agentsFlag holds --agents=kiro-cli,claude,... for non-interactive setup
var agentsFlag string

func main() {
	for _, arg := range os.Args[1:] {
		switch {
		case arg == "--version" || arg == "-v":
			fmt.Println("cicy-code " + version)
			os.Exit(0)
		case arg == "--help" || arg == "-h":
			fmt.Println(`cicy-code - AI agent collaboration tool (local, SQLite)

Usage: cicy-code [options]

Options:
  --help, -h              Show this help
  --version, -v           Show version
  --desktop               Start in desktop mode
  --dev                   Development mode
  --public                Listen on 0.0.0.0 (default: 127.0.0.1)
  --audit                 Enable audit mode
  --cn                    Use Chinese mirrors
  --agents=LIST           Comma-separated agents to install (skip interactive)
                          e.g. --agents=kiro-cli,claude,copilot
                          Use --agents=all for all agents

Environment:
  PORT          API port (default: 8008)
  CS_PORT       code-server port (default: 8002)
  SQLITE_PATH   SQLite database file (default: ~/.cicy/data.db)`)
			os.Exit(0)
		case arg == "--desktop":
			desktopMode = true
		case arg == "--dev":
			devMode = true
		case arg == "--public":
			publicMode = true
		case arg == "--audit":
			auditMode = true
			os.Setenv("AUDIT_MODE", "1")
		case arg == "--cn":
			cnMirror = true
			os.Setenv("CN_MIRROR", "1")
		case strings.HasPrefix(arg, "--agents="):
			agentsFlag = strings.TrimPrefix(arg, "--agents=")
		}
	}

	// --dev without explicit --agents defaults to core set
	if devMode && agentsFlag == "" {
		agentsFlag = "kiro-cli,opencode,copilot"
	}

	initKV()
	initRedis()
	initDB()
	store.Migrate()
	defer store.Close()

	checkEnv()

	go startWatcher()
	go startTmuxHealth()

	// Health
	http.HandleFunc("/health", w(handleHealth))
	http.HandleFunc("/api/health", w(handleHealth))
	http.HandleFunc("/api/ping", w(handlePing))

	// Auth — local token management
	http.HandleFunc("/api/auth/verify", w(handleAuthVerify))
	http.HandleFunc("/api/auth/verify-token", w(handleAuthVerifyToken))
	http.HandleFunc("/api/auth/tokens", wa(handleAuthTokens))
	http.HandleFunc("/api/auth/tokens/", wa(handleAuthTokenDelete))

	// Panes
	http.HandleFunc("/api/panes", wa(handlePanes))
	http.HandleFunc("/api/panes/create", wa(handleCreatePane))
	http.HandleFunc("/api/panes/", wa(handlePaneByID))
	http.HandleFunc("/api/panes/restart-all", wa(handleRestartAll))
	// Legacy panes routes (frontend compatibility)
	http.HandleFunc("/api/tmux/panes", wa(handlePanes))
	http.HandleFunc("/api/tmux/panes/", wa(handlePaneByID))
	http.HandleFunc("/api/tmux/create", wa(handleCreatePane))
	http.HandleFunc("/api/tmux/restart_all", wa(handleRestartAll))

	// Tmux
	http.HandleFunc("/api/tmux/send", wa(handleSend))
	http.HandleFunc("/api/tmux/send-keys", wa(handleSendKeys))
	http.HandleFunc("/api/tmux/send_wait", wa(handleSendWait))
	http.HandleFunc("/api/tmux/capture", wa(handleCapture))
	http.HandleFunc("/api/tmux/windows", wa(handleWindows))
	http.HandleFunc("/api/tmux/tree", wa(handleTree))
	http.HandleFunc("/api/tmux/status", wa(handleStatus))
	http.HandleFunc("/api/tmux/mouse", wa(handleMouseToggle))
	http.HandleFunc("/api/tmux/mouse/on", wa(handleMouseToggle))
	http.HandleFunc("/api/tmux/mouse/off", wa(handleMouseToggle))
	http.HandleFunc("/api/tmux/mouse/status", wa(handleMouseStatus))
	http.HandleFunc("/api/tmux/ttyd/status", wa(handleTtydStatus))
	http.HandleFunc("/api/tmux/ttyd/status/", wa(handleTtydStatus))
	http.HandleFunc("/api/tmux/list", wa(handleTmuxList))
	http.HandleFunc("/api/tmux/clear", wa(handleClear))
	http.HandleFunc("/api/tmux/capture_pane", wa(handleCapture))

	// Chat
	http.HandleFunc("/api/chat/push", wa(handleChatPush))
	http.HandleFunc("/api/chat/ws", handleChatWS)
	http.HandleFunc("/api/chat/clients", wa(handleWsClients))
	http.HandleFunc("/api/chat/debug", wa(handleChatDebug))
	http.HandleFunc("/api/chat/webhook", corsM(handleChatWebhook))

	// Stats
	http.HandleFunc("/api/stats/traffic", wa(handleStatsTraffic))
	http.HandleFunc("/api/stats/traffic/raw", wa(handleStatsTrafficRaw))
	http.HandleFunc("/api/stats/chat", wa(handleChatHistory))
	http.HandleFunc("/api/stats/chat/stream", wa(handleChatStream))
	http.HandleFunc("/api/stats/traffic/live", corsM(func(w http.ResponseWriter, r *http.Request) {
		t := r.URL.Query().Get("token")
		if t == "" || !verifyToken(t) {
			httpErr(w, 401, "Not authenticated")
			return
		}
		handleTrafficLive(w, r)
	}))

	// Notifications
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

	// Queue
	http.HandleFunc("/api/queue", wa(handleQueue))
	http.HandleFunc("/api/queue/push", wa(handleQueuePush))
	http.HandleFunc("/api/queue/list", wa(handleQueueList))
	http.HandleFunc("/api/queue/", wa(handleQueueByID))
	// Legacy queue routes
	http.HandleFunc("/api/workers/queue", wa(handleQueue))
	http.HandleFunc("/api/workers/queue/", wa(handleQueueByID))

	// Agents
	http.HandleFunc("/api/agents/by-pane", wa(handleAgentsByPane))
	http.HandleFunc("/api/agents/by-pane/", wa(handleAgentsByPane))
	http.HandleFunc("/api/agents/pane/", wa(handleAgentsByPane))
	http.HandleFunc("/api/agents/bind", wa(handleAgentBind))
	http.HandleFunc("/api/agents/unbind", wa(handleAgentUnbind))
	http.HandleFunc("/api/agents/unbind/", wa(handleAgentUnbind))

	// Groups
	http.HandleFunc("/api/groups", wa(handleGroups))
	http.HandleFunc("/api/groups/", wa(handleGroupByID))

	// Nodes
	http.HandleFunc("/api/nodes", wa(handleNodes))
	http.HandleFunc("/api/nodes/exec", wa(handleNodeExec))

	// Settings
	http.HandleFunc("/api/settings", wa(handleSettings))
	http.HandleFunc("/api/settings/global", wa(handleSettings))
	http.HandleFunc("/api/file-exists", wa(handleFileExists))
	http.HandleFunc("/api/utils/file/exists", wa(handleFileExists))
	http.HandleFunc("/api/correctEnglish", wa(handleCorrectEnglish))

	// TTS
	http.HandleFunc("/api/tts", wa(handleTTS))

	// Telegram
	http.HandleFunc("/api/tg/send", wa(handleTGSend))
	http.HandleFunc("/api/tg/photo", wa(handleTGPhoto))

	// Pair
	http.HandleFunc("/api/pair", wa(handlePair))
	http.HandleFunc("/api/tmux/pair", wa(handlePair))

	// Desktop
	http.HandleFunc("/api/desktop/status", wa(handleDesktopStatus))
	http.HandleFunc("/api/desktop/proxy/", wa(handleDesktopProxy))

	// Code-server proxy
	http.HandleFunc("/code/", handleCodeServer)
	http.HandleFunc("/code/auth", handleCodeServerAuth)
	http.HandleFunc("/mitm/", handleMitmproxyAuth)
	http.HandleFunc("/mitm", handleMitmproxyAuth)

	// WebSocket terminal proxy
	http.HandleFunc("/ws", handleWSProxy)
	http.HandleFunc("/ttyd/", handleTtydProxy)

	// UI (SPA)
	http.Handle("/", serveUI())

	port := os.Getenv("PORT")
	if port == "" {
		port = "8008"
	}

	kvMode := "memory"
	if useRedis {
		kvMode = "redis"
	} else if kv.file != "" {
		kvMode = "file:" + kv.file
	}
	log.Printf("[startup] mode=local port=%s db=%s kv=%s", port, store.Driver, kvMode)

	// Hook: thinking → idle
	RegisterHook(func(paneID string, old, new paneSt) {
		if old.Status != nil && *old.Status == "thinking" && new.Status != nil && *new.Status == "idle" {
			go dispatchQueue(paneID)

			shortPane := shortPaneID(paneID)
			rows, err := store.Query(`SELECT pa.pane_id FROM pane_agents pa WHERE pa.agent_name=? AND pa.status='active'`, shortPane)
			if err != nil {
				return
			}
			defer rows.Close()
			for rows.Next() {
				var masterPane string
				rows.Scan(&masterPane)
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
				log.Printf("[hook] notified master %s (chatbus): worker %s idle", masterPane, shortPane)
			}
		}
	})

	initHTTPLogConsumer()

	bind := "127.0.0.1"
	if publicMode {
		bind = "0.0.0.0"
	}
	log.Printf("cicy-code starting on %s:%s", bind, port)
	token := getFirstToken()
	openHost := bind
	if openHost == "0.0.0.0" {
		openHost = "127.0.0.1"
	}
	openURL := fmt.Sprintf("http://%s:%s/?token=%s", openHost, port, token)
	log.Printf("")
	log.Printf("============================================================")
	log.Printf("")
	log.Printf("  >>> CICY CODE <<<")
	log.Printf("============================================================")
	log.Printf("  Token: %s", token)
	log.Printf("  URL:   %s", openURL)
	log.Printf("============================================================")
	log.Printf("")
	go func() {
		if os.Getenv("CICY_NO_BROWSER") == "1" {
			return
		}
		if err := openDefaultBrowser(openURL); err != nil {
			log.Printf("[startup] open browser failed: %v", err)
		} else {
			log.Printf("[startup] browser opened")
		}
	}()
	if auditMode {
		log.Printf("[startup] audit mode enabled")
	}
	if cnMirror {
		log.Printf("[startup] CN mirror enabled")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	signal.Ignore(syscall.SIGHUP) // ignore SIGHUP when parent terminal closes
	go func() {
		<-sigCh
		log.Println("[shutdown] stopping...")
		os.Exit(0)
	}()

	if desktopMode {
		go func() {
			time.Sleep(2 * time.Second)
			ensureDesktop()
		}()
	}

	log.Fatal(http.ListenAndServe(bind+":"+port, globalCORS(http.DefaultServeMux)))
}

func getFirstToken() string {
	home, _ := os.UserHomeDir()
	gpath := home + "/global.json"
	cfg := map[string]interface{}{}
	if data, err := os.ReadFile(gpath); err == nil {
		json.Unmarshal(data, &cfg)
	}
	if t, ok := cfg["api_token"].(string); ok && t != "" {
		return t
	}
	b := make([]byte, 16)
	rand.Read(b)
	token := "cicy_" + hex.EncodeToString(b)
	cfg["api_token"] = token
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(gpath, data, 0644)
	return token
}

func globalCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func w(h http.HandlerFunc) http.HandlerFunc  { return corsM(h) }
func wa(h http.HandlerFunc) http.HandlerFunc { return corsM(authM(h)) }

func handleHealth(w http.ResponseWriter, r *http.Request) {
	J(w, M{"status": "ok", "source": "cicy-code"})
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	J(w, M{"status": "ok", "version": version, "source": "cicy-code"})
}

func openDefaultBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
