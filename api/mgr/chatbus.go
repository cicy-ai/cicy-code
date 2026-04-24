package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
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

// ── Hub: per-agent pub/sub over WebSocket ──

type chatClient struct {
	conn        *websocket.Conn
	send        chan []byte
	agentID     string
	clientID    string
	electron    bool
	connectedAt time.Time
	remoteAddr  string
	closeOnce   sync.Once
}

type chatHub struct {
	mu      sync.RWMutex
	clients map[string]map[string]*chatClient // agent_id -> client_id -> client
}

var hub = &chatHub{clients: make(map[string]map[string]*chatClient)}

func (c *chatClient) close() {
	c.closeOnce.Do(func() {
		close(c.send)
		_ = c.conn.Close()
	})
}

func (h *chatHub) stats() interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()
	type clientInfo struct {
		ClientID    string `json:"client_id"`
		Electron    bool   `json:"electron"`
		RemoteAddr  string `json:"remote_addr"`
		ConnectedAt string `json:"connected_at"`
		UptimeSec   int    `json:"uptime_sec"`
	}
	out := map[string]map[string]clientInfo{}
	for agentID, m := range h.clients {
		if out[agentID] == nil {
			out[agentID] = make(map[string]clientInfo)
		}
		for clientID, c := range m {
			out[agentID][clientID] = clientInfo{
				ClientID:    clientID,
				Electron:    c.electron,
				RemoteAddr:  c.remoteAddr,
				ConnectedAt: c.connectedAt.Format(time.RFC3339),
				UptimeSec:   int(time.Since(c.connectedAt).Seconds()),
			}
		}
	}
	return out
}

func handleWsClients(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(hub.stats())
}

func (h *chatHub) register(c *chatClient) {
	h.mu.Lock()
	var replaced *chatClient
	if h.clients[c.agentID] == nil {
		h.clients[c.agentID] = make(map[string]*chatClient)
	}
	if existing := h.clients[c.agentID][c.clientID]; existing != nil && existing != c {
		replaced = existing
	}
	h.clients[c.agentID][c.clientID] = c
	count := len(h.clients[c.agentID])
	h.mu.Unlock()
	if replaced != nil {
		replaced.close()
	}
	log.Printf("[chat-ws] connect agent_id=%s client_id=%s clients=%d", c.agentID, c.clientID, count)
}

func (h *chatHub) unregister(c *chatClient) {
	h.mu.Lock()
	if m, ok := h.clients[c.agentID]; ok {
		if current, ok := m[c.clientID]; ok && current == c {
			delete(m, c.clientID)
		}
		if len(m) == 0 {
			delete(h.clients, c.agentID)
		}
	}
	h.mu.Unlock()
	c.close()
	log.Printf("[chat-ws] disconnect agent_id=%s client_id=%s", c.agentID, c.clientID)
}

func (h *chatHub) broadcast(agentID string, evt ChatEvent) {
	appendRuntimeEvent(agentID, evt.Type, evt.Data)
	h.broadcastExcept(agentID, evt, nil)
}

func (h *chatHub) broadcastAll(evt ChatEvent) {
	h.mu.RLock()
	agentIDs := make([]string, 0, len(h.clients))
	for agentID := range h.clients {
		agentIDs = append(agentIDs, agentID)
	}
	h.mu.RUnlock()
	for _, agentID := range agentIDs {
		h.broadcastExcept(agentID, evt, nil)
	}
}

func (h *chatHub) broadcastExcept(agentID string, evt ChatEvent, except *chatClient) {
	b, _ := json.Marshal(evt)
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := len(h.clients[agentID])
	log.Printf("[chat-ws] broadcast agent_id=%s type=%s clients=%d", agentID, evt.Type, n)
	for _, c := range h.clients[agentID] {
		if c == except {
			continue
		}
		select {
		case c.send <- b:
		default:
		}
	}
}

func (h *chatHub) broadcastElectron(agentID string, evt ChatEvent) {
	b, _ := json.Marshal(evt)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.clients[agentID] {
		if !c.electron {
			continue
		}
		select {
		case c.send <- b:
		default:
		}
	}
}

func (h *chatHub) sendToClient(agentID, clientID string, evt ChatEvent) bool {
	b, _ := json.Marshal(evt)
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.clients[agentID][clientID]
	if c == nil {
		return false
	}
	select {
	case c.send <- b:
		return true
	default:
		return false
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
		var evt ChatEvent
		if json.Unmarshal(msg, &evt) != nil || evt.Type == "" {
			continue
		}
		// poll_request: 回复当前 poll 数据给请求方
		if evt.Type == "poll_request" {
			data := buildPollData(c.agentID)
			reply := ChatEvent{Type: "poll_data", Data: data}
			if b, err := json.Marshal(reply); err == nil {
				select {
				case c.send <- b:
				default:
				}
			}
			continue
		}
		// 广播客户端发来的消息给同 agent 的其他客户端
		hub.broadcastExcept(c.agentID, evt, c)
	}
}

// buildPollData 构造 poll 响应数据，与 handlePoll 返回格式一致
func buildPollData(paneID string) M {
	agents, _ := listAgentsByPane(paneID)
	if agents == nil {
		agents = []M{}
	}
	snapshot := loadRuntimeMembershipSnapshot()
	resp := M{
		"success":     true,
		"pane_id":     shortPaneID(normPaneID(paneID)),
		"agents":      agents,
		"statuses":    M{},
		"system_resources": systemResources.getLatest(),
		"server_time": time.Now().UTC().Format(time.RFC3339),
	}
	if snapshot.TrialExpiresAt != "" {
		resp["trial_expires_at"] = snapshot.TrialExpiresAt
		if snapshot.TrialExpiresEpoch != "" {
			resp["trial_expires_at_epoch"] = snapshot.TrialExpiresEpoch
		}
	}
	if snapshot.IsPro != nil {
		resp["is_pro"] = *snapshot.IsPro
	}
	if snapshot.Kind != "" {
		resp["membership_kind"] = snapshot.Kind
	}
	if snapshot.Tag != "" {
		resp["membership_tag"] = snapshot.Tag
	}
	if snapshot.ExpiresAt != "" {
		resp["membership_expires_at"] = snapshot.ExpiresAt
	}
	if snapshot.RenewURL != "" {
		resp["renew_url"] = snapshot.RenewURL
	}
	if snapshot.UpgradeURL != "" {
		resp["upgrade_url"] = snapshot.UpgradeURL
	}
	if snapshot.ShowRenew != nil {
		resp["show_renew"] = *snapshot.ShowRenew
	}
	if snapshot.ShowUpgrade != nil {
		resp["show_upgrade"] = *snapshot.ShowUpgrade
	}
	if snapshot.SyncedAt != "" {
		resp["membership_synced_at"] = snapshot.SyncedAt
	}
	return resp
}

// broadcastPollData 向指定 agent 的所有 WS 客户端推送最新 poll 数据
func broadcastPollData(paneID string) {
	data := buildPollData(paneID)
	hub.broadcast(paneID, ChatEvent{Type: "poll_data", Data: data})
}

// ── HTTP handlers ──

func normalizeChatAgentValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return shortPaneID(normPaneID(value))
}

func normalizeChatAgentID(r *http.Request) string {
	value := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("pane"))
	}
	return normalizeChatAgentValue(value)
}

// GET /api/chat/ws?agent_id=xxx&token=xxx — WebSocket upgrade
func handleChatWS(w http.ResponseWriter, r *http.Request) {
	agentID := normalizeChatAgentID(r)
	t := r.URL.Query().Get("token")
	if agentID == "" || t == "" || !verifyToken(t) {
		httpErr(w, 401, "unauthorized")
		return
	}
	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	if clientID == "" {
		clientID = "ws-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}

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
	c := &chatClient{conn: conn, send: make(chan []byte, 64), agentID: agentID, clientID: clientID, electron: r.URL.Query().Get("electron") == "1", connectedAt: time.Now(), remoteAddr: remoteAddr}
	hub.register(c)
	go c.writePump()
	c.readPump()
}

// POST /api/chat/webhook — mitmproxy pushes events
func handleChatWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID string      `json:"client_id"`
		AgentID  string      `json:"agent_id"`
		Pane     string      `json:"pane"`
		Event    string      `json:"event"`
		Data     interface{} `json:"data"`
	}
	if readBody(r, &req) != nil || (req.AgentID == "" && req.Pane == "") || req.Event == "" {
		httpErr(w, 400, "agent_id and event required")
		return
	}
	agentID := normalizeChatAgentValue(req.AgentID)
	if agentID == "" {
		agentID = normalizeChatAgentValue(req.Pane)
	}
	evt := ChatEvent{Type: req.Event, Data: req.Data}
	if req.ClientID != "" {
		if !hub.sendToClient(agentID, req.ClientID, evt) {
			httpErr(w, 404, "client not found")
			return
		}
		log.Printf("[chat-webhook] agent_id=%s client_id=%s type=%s mode=direct", agentID, req.ClientID, req.Event)
		w.WriteHeader(204)
		return
	}
	hub.broadcast(agentID, evt)
	if req.Event == "user_q" {
		hub.broadcast(agentID, ChatEvent{Type: "status_change", Data: M{"status": "thinking"}})
	}
	if req.Event == "ai_done" {
		hub.broadcast(agentID, ChatEvent{Type: "status_change", Data: M{"status": "idle"}})
	}
	w.WriteHeader(204)
}

// ── HTTP handler: push event to agent/client ──

func handleChatPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var req struct {
		ClientID string      `json:"client_id"`
		AgentID  string      `json:"agent_id"`
		Pane     string      `json:"pane"`
		Type     string      `json:"type"`
		Data     interface{} `json:"data"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	if (req.AgentID == "" && req.Pane == "") || req.Type == "" {
		http.Error(w, "agent_id and type required", 400)
		return
	}
	agentID := normalizeChatAgentValue(req.AgentID)
	if agentID == "" {
		agentID = normalizeChatAgentValue(req.Pane)
	}

	if req.ClientID != "" {
		ok := hub.sendToClient(agentID, req.ClientID, ChatEvent{Type: req.Type, Data: req.Data})
		if !ok {
			http.Error(w, "client not found", 404)
			return
		}
		log.Printf("[chat-push] agent_id=%s client_id=%s type=%s mode=direct", agentID, req.ClientID, req.Type)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "mode": "direct"})
		return
	}

	// desktop_event with ipc/gemini types → only electron clients
	if req.Type == "desktop_event" {
		if dm, ok := req.Data.(map[string]interface{}); ok {
			if dt, _ := dm["type"].(string); dt == "gemini_ask" || dt == "gemini_vision_request" || dt == "ipc_ping" {
				hub.broadcastElectron(agentID, ChatEvent{Type: req.Type, Data: req.Data})
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
				return
			}
		}
	}

	hub.broadcast(agentID, ChatEvent{Type: req.Type, Data: req.Data})
	log.Printf("[chat-push] agent_id=%s type=%s", agentID, req.Type)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "mode": "broadcast"})
}

func handleChatDebug(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(r.Header)
}
