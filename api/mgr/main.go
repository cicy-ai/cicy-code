package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/websocket"

	"ttyd-go/backend/localcommand"
	"ttyd-go/server"
)

var (
	db       *sql.DB
	upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
)

func main() {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = "root:cicy-code@tcp(localhost:3306)/cicy_code"
	}
	if !strings.Contains(dsn, "parseTime") {
		if strings.Contains(dsn, "?") {
			dsn += "&parseTime=true"
		} else {
			dsn += "?parseTime=true"
		}
	}
	var err error
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	defer db.Close()

	// SaaS users table
	db.Exec(`CREATE TABLE IF NOT EXISTS saas_users (
		id VARCHAR(36) PRIMARY KEY,
		email VARCHAR(255) UNIQUE NOT NULL,
		plan VARCHAR(20) DEFAULT 'free',
		backend_url VARCHAR(255) DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)

	// Health
	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/health", w(handleHealth))
	http.HandleFunc("/api/health", w(handleHealth))
	http.HandleFunc("/api/ping", w(handlePing))

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

	port := os.Getenv("PORT")
	if port == "" {
		port = "8008"
	}

	// 注册默认 hook：thinking→idle 时通知 master
	RegisterHook(func(paneID string, old, new paneSt) {
		if old.Status != nil && *old.Status == "thinking" && new.Status != nil && *new.Status == "idle" {
			// Auto-dispatch queued messages
			go dispatchQueue(paneID)

			shortPane := shortPaneID(paneID)

			// Find master panes that have this worker bound
			rows, err := db.Query(`SELECT pa.pane_id FROM pane_agents pa WHERE pa.agent_name=? AND pa.status='active'`, shortPane)
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

	go startWatcher()
	go startTmuxHealth()
	initHTTPLogConsumer()
	log.Printf("cicy-code-api starting on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
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
