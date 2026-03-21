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
	"strings"
	"syscall"

	"github.com/gorilla/websocket"
)

var (
	upgrader    = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	publicMode  bool
	devMode     bool
	desktopMode bool
	desktopCmd  *exec.Cmd
)

const version = "0.1.0"

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
  --dev                   Development mode
  --public                Listen on 0.0.0.0 (default: 127.0.0.1)
  --agents=LIST           Comma-separated agents to install (skip interactive)
                          e.g. --agents=kiro-cli,claude,copilot
                          Use --agents=all for all agents

Environment:
  PORT          API port (default: 8008)
  SQLITE_PATH   SQLite database file (default: ~/.cicy/data.db)`)
			os.Exit(0)
		case arg == "--dev":
			devMode = true
		case arg == "--public":
			publicMode = true
		case strings.HasPrefix(arg, "--agents="):
			agentsFlag = strings.TrimPrefix(arg, "--agents=")
		}
	}

	initKV()
	initRedis()
	initDB()
	store.Migrate()
	defer store.Close()

	// First-run setup: check env + install agents
	checkEnvAndSetup()

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

	// Tmux
	http.HandleFunc("/api/tmux/send", wa(handleSend))
	http.HandleFunc("/api/tmux/send-keys", wa(handleSendKeys))
	http.HandleFunc("/api/tmux/send_wait", wa(handleSendWait))
	http.HandleFunc("/api/tmux/capture", wa(handleCapture))
	http.HandleFunc("/api/tmux/windows", wa(handleWindows))
	http.HandleFunc("/api/tmux/tree", wa(handleTree))
	http.HandleFunc("/api/tmux/status", wa(handleStatus))
	http.HandleFunc("/api/tmux/mouse", wa(handleMouseToggle))
	http.HandleFunc("/api/tmux/mouse/status", wa(handleMouseStatus))
	http.HandleFunc("/api/tmux/ttyd/status", wa(handleTtydStatus))
	http.HandleFunc("/api/tmux/list", wa(handleTmuxList))
	http.HandleFunc("/api/tmux/clear", wa(handleClear))

	// Chat
	http.HandleFunc("/api/chat/push", wa(handleChatPush))
	http.HandleFunc("/api/chat/ws", handleChatWS)
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

	// Agents
	http.HandleFunc("/api/agents/by-pane", wa(handleAgentsByPane))
	http.HandleFunc("/api/agents/bind", wa(handleAgentBind))
	http.HandleFunc("/api/agents/unbind", wa(handleAgentUnbind))

	// Groups
	http.HandleFunc("/api/groups", wa(handleGroups))
	http.HandleFunc("/api/groups/", wa(handleGroupByID))

	// Nodes
	http.HandleFunc("/api/nodes", wa(handleNodes))
	http.HandleFunc("/api/nodes/exec", wa(handleNodeExec))

	// Settings
	http.HandleFunc("/api/settings", wa(handleSettings))
	http.HandleFunc("/api/file-exists", wa(handleFileExists))
	http.HandleFunc("/api/correctEnglish", wa(handleCorrectEnglish))

	// TTS
	http.HandleFunc("/api/tts", wa(handleTTS))

	// Telegram
	http.HandleFunc("/api/tg/send", wa(handleTGSend))
	http.HandleFunc("/api/tg/photo", wa(handleTGPhoto))

	// Pair
	http.HandleFunc("/api/pair", wa(handlePair))

	// Desktop
	http.HandleFunc("/api/desktop/status", wa(handleDesktopStatus))
	http.HandleFunc("/api/desktop/proxy/", wa(handleDesktopProxy))

	// Code-server proxy
	http.HandleFunc("/code/", handleCodeServer)
	http.HandleFunc("/code/auth", handleCodeServerAuth)

	// WebSocket terminal proxy
	http.HandleFunc("/ws", handleWSProxy)
	http.HandleFunc("/ws/ttyd/", handleTtydProxy)

	// UI (SPA)
	http.Handle("/ui/", http.StripPrefix("/ui", serveUI()))

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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[shutdown] stopping...")
		os.Exit(0)
	}()

	log.Fatal(http.ListenAndServe(bind+":"+port, nil))
}

// checkEnvAndSetup runs first-time setup if no agents exist in DB.
func checkEnvAndSetup() {
	var count int
	store.QueryRow("SELECT COUNT(*) FROM agent_config").Scan(&count)
	if count > 0 {
		// Already set up — ensure existing agents are running
		ensureBuiltinAgents()
		return
	}
	// First run
	if agentsFlag != "" {
		// Non-interactive: --agents=kiro-cli,claude or --agents=all
		runSetupWithAgents(agentsFlag)
	} else {
		// Interactive prompt
		runSetup()
	}
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

func w(h http.HandlerFunc) http.HandlerFunc  { return corsM(h) }
func wa(h http.HandlerFunc) http.HandlerFunc { return corsM(authM(h)) }

func handleHealth(w http.ResponseWriter, r *http.Request) {
	J(w, M{"status": "ok", "source": "cicy-code"})
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	J(w, M{"status": "ok", "version": version, "source": "cicy-code"})
}
