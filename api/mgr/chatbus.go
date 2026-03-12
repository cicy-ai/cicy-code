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
	conn *websocket.Conn
	send chan []byte
	pane string
}

type chatHub struct {
	mu      sync.RWMutex
	clients map[string]map[*chatClient]struct{} // pane -> clients
}

var hub = &chatHub{clients: make(map[string]map[*chatClient]struct{})}

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
	b, _ := json.Marshal(evt)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients[pane] {
		select {
		case c.send <- b:
		default:
			// slow client, skip
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
	c.conn.SetReadLimit(4096)
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})
	for {
		c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			return
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

	c := &chatClient{conn: conn, send: make(chan []byte, 64), pane: pane}
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
	if req.Event == "ai_done" {
		hub.broadcast(req.Pane, ChatEvent{Type: "status_change", Data: M{"status": "idle"}})
	}
	w.WriteHeader(204)
}
