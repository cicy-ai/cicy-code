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

	// Health
	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/health", w(handleHealth))
	http.HandleFunc("/api/health", w(handleHealth))
	http.HandleFunc("/ping", w(handlePing))

	// Auth
	http.HandleFunc("/api/auth/verify", w(handleAuthVerify))
	http.HandleFunc("/api/auth/verify-token", w(handleAuthVerifyToken))
	http.HandleFunc("/api/auth/tokens", wa(handleAuthTokens))
	http.HandleFunc("/api/auth/tokens/", wa(handleAuthTokenDelete))

	// Tmux panes
	http.HandleFunc("/api/tmux/panes", wa(handlePanes))
	http.HandleFunc("/api/tmux/list", wa(handleTmuxList))
	http.HandleFunc("/api/tmux/create", wa(handleCreatePane))
	http.HandleFunc("/api/tmux/send", wa(handleSend))
	http.HandleFunc("/api/tmux/send-keys", wa(handleSendKeys))
	http.HandleFunc("/api/tmux/send_wait", wa(handleSendWait))
	http.HandleFunc("/api/tmux/capture_pane", wa(handleCapture))
	http.HandleFunc("/api/tmux/tree", wa(handleTree))
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

	// Utils
	http.HandleFunc("/api/utils/file/exists", wa(handleFileExists))
	http.HandleFunc("/api/correctEnglish", wa(handleCorrectEnglish))

	// Code-server proxy (token auth only for root, assets bypass)
	http.HandleFunc("/code/", corsM(handleCodeServerAuth))

	// Mitmproxy Web UI proxy
	http.HandleFunc("/mitm/", corsM(handleMitmproxyAuth))

	// XUI proxy: /api/xui/{pane_id}/... → node xui
	http.HandleFunc("/api/xui/", wa(handleXuiProxy))

	// WebSocket
	http.HandleFunc("/api/ws/", wa(handleWSProxy))

	// Static files (custom gotty-bundle.js etc)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// TTYD proxy - serve ttyd-go instances at /ttyd/{pane_id}/
	http.HandleFunc("/ttyd/", handleTtydProxy)

	port := os.Getenv("PORT")
	if port == "" {
		port = "14444"
	}

	// 注册默认 hook：thinking→idle 时通知 master
	RegisterHook(func(paneID string, old, new paneSt) {
		if old.Status != nil && *old.Status == "thinking" && new.Status != nil && *new.Status == "idle" {
			// Auto-dispatch queued messages
			go dispatchQueue(paneID)

			rows, err := db.Query("SELECT agent_name FROM pane_agents WHERE pane_id=? AND status='active'", paneID)
			if err != nil {
				return
			}
			defer rows.Close()
			for rows.Next() {
				var agent string
				rows.Scan(&agent)
				if strings.HasPrefix(agent, "master-") {
					target := agent
					if !strings.Contains(target, ":") {
						target += ":main.0"
					}
					msg := fmt.Sprintf("pane_idle:%s", paneID)
					nodeTmux(target, "send-keys", "-t", target, "-l", msg)
					nodeTmux(target, "send-keys", "-t", target, "Enter")
					log.Printf("[hook] notified %s: %s", agent, msg)
				}
			}
		}
	})

	go startWatcher()
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
	J(w, M{"pong": "ok", "version": "1.0", "server_datetime": time.Now().Format(time.RFC3339)})
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
