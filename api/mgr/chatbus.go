package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ── Event types ──

type ChatEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data,omitempty"`
}

// ── Hub: per-pane pub/sub over WebSocket ──

type chatClient struct {
	conn        *websocket.Conn
	send        chan []byte
	pane        string
	electron    bool
	connectedAt time.Time
	remoteAddr  string
}

type chatHub struct {
	mu      sync.RWMutex
	clients map[string]map[*chatClient]struct{} // pane -> clients
}

var hub = &chatHub{clients: make(map[string]map[*chatClient]struct{})}

func (h *chatHub) stats() interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()
	type clientInfo struct {
		Electron    bool   `json:"electron"`
		RemoteAddr  string `json:"remote_addr"`
		ConnectedAt string `json:"connected_at"`
		UptimeSec   int    `json:"uptime_sec"`
	}
	out := map[string][]clientInfo{}
	for pane, m := range h.clients {
		for c := range m {
			out[pane] = append(out[pane], clientInfo{
				Electron:    c.electron,
				RemoteAddr:  c.remoteAddr,
				ConnectedAt: c.connectedAt.Format(time.RFC3339),
				UptimeSec:   int(time.Since(c.connectedAt).Seconds()),
			})
		}
	}
	return out
}

func handleWsClients(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(hub.stats())
}

func (h *chatHub) register(c *chatClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[c.pane] == nil {
		h.clients[c.pane] = make(map[*chatClient]struct{})
	}
	h.clients[c.pane][c] = struct{}{}
	log.Printf("[chat-ws] connect pane=%s clients=%d", c.pane, len(h.clients[c.pane]))
}

func (h *chatHub) unregister(c *chatClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m, ok := h.clients[c.pane]; ok {
		delete(m, c)
		if len(m) == 0 {
			delete(h.clients, c.pane)
		}
	}
	close(c.send)
	c.conn.Close()
	log.Printf("[chat-ws] disconnect pane=%s", c.pane)
}

func (h *chatHub) broadcast(pane string, evt ChatEvent) {
	appendRuntimeEvent(pane, evt.Type, evt.Data)
	h.broadcastExcept(pane, evt, nil)
}

func (h *chatHub) broadcastExcept(pane string, evt ChatEvent, except *chatClient) {
	b, _ := json.Marshal(evt)
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := len(h.clients[pane])
	log.Printf("[chat-ws] broadcast pane=%s type=%s clients=%d", pane, evt.Type, n)
	for c := range h.clients[pane] {
		if c == except {
			continue
		}
		select {
		case c.send <- b:
		default:
		}
	}
}

func (h *chatHub) broadcastElectron(pane string, evt ChatEvent) {
	b, _ := json.Marshal(evt)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients[pane] {
		if !c.electron {
			continue
		}
		select {
		case c.send <- b:
		default:
		}
	}
}

// ── Client read/write pumps ──

func (c *chatClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *chatClient) readPump() {
	defer hub.unregister(c)
	c.conn.SetReadLimit(64 * 1024)
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})
	for {
		c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		// 广播客户端发来的消息给同 pane 的所有客户端
		var evt ChatEvent
		if json.Unmarshal(msg, &evt) == nil && evt.Type != "" {
			hub.broadcastExcept(c.pane, evt, c)
		}
	}
}

// ── HTTP handlers ──

// GET /api/chat/ws?pane=xxx&token=xxx — WebSocket upgrade
func handleChatWS(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	t := r.URL.Query().Get("token")
	if pane == "" || t == "" || !verifyToken(t) {
		httpErr(w, 401, "unauthorized")
		return
	}
	pane = strings.Replace(pane, ":main.0", "", 1)

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	remoteAddr := r.Header.Get("CF-Connecting-IP")
	if remoteAddr == "" {
		remoteAddr = r.Header.Get("X-Real-IP")
	}
	if remoteAddr == "" {
		remoteAddr = r.Header.Get("X-Forwarded-For")
	}
	if remoteAddr == "" {
		remoteAddr = r.RemoteAddr
	}
	c := &chatClient{conn: conn, send: make(chan []byte, 64), pane: pane, electron: r.URL.Query().Get("electron") == "1", connectedAt: time.Now(), remoteAddr: remoteAddr}
	hub.register(c)
	go c.writePump()
	c.readPump()
}

// POST /api/chat/webhook — mitmproxy pushes events
func handleChatWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pane  string      `json:"pane"`
		Event string      `json:"event"`
		Data  interface{} `json:"data"`
	}
	if readBody(r, &req) != nil || req.Pane == "" || req.Event == "" {
		httpErr(w, 400, "pane and event required")
		return
	}
	hub.broadcast(req.Pane, ChatEvent{Type: req.Event, Data: req.Data})
	if req.Event == "user_q" {
		hub.broadcast(req.Pane, ChatEvent{Type: "status_change", Data: M{"status": "thinking"}})
	}
	if req.Event == "ai_done" {
		hub.broadcast(req.Pane, ChatEvent{Type: "status_change", Data: M{"status": "idle"}})
	}
	w.WriteHeader(204)
}

// ── HTTP handler: push event to pane ──

func handleChatPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var req struct {
		Pane string      `json:"pane"`
		Type string      `json:"type"`
		Data interface{} `json:"data"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	if req.Pane == "" || req.Type == "" {
		http.Error(w, "pane and type required", 400)
		return
	}

	// desktop_event with ipc/gemini types → only electron clients
	if req.Type == "desktop_event" {
		if dm, ok := req.Data.(map[string]interface{}); ok {
			if dt, _ := dm["type"].(string); dt == "gemini_ask" || dt == "gemini_vision_request" || dt == "ipc_ping" {
				hub.broadcastElectron(req.Pane, ChatEvent{Type: req.Type, Data: req.Data})
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
				return
			}
		}
	}

	hub.broadcast(req.Pane, ChatEvent{Type: req.Type, Data: req.Data})
	log.Printf("[chat-push] pane=%s type=%s", req.Pane, req.Type)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func handleChatDebug(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(r.Header)
}
