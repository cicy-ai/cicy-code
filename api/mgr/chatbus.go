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

// ── Hub: master-channel pub/sub over WebSocket ──

type chatClient struct {
	conn           *websocket.Conn
	send           chan []byte
	agentID        string
	clientID       string
	activeAgent    string
	electron       bool
	platform       string
	userAgent      string
	connectedAt    time.Time
	remoteAddr     string
	closeOnce      sync.Once
	activityMu     sync.Mutex
	lastActivityAt time.Time
}

func (c *chatClient) markActive() {
	c.activityMu.Lock()
	c.lastActivityAt = time.Now()
	c.activityMu.Unlock()
}

func (c *chatClient) idleFor() time.Duration {
	c.activityMu.Lock()
	t := c.lastActivityAt
	c.activityMu.Unlock()
	if t.IsZero() {
		return time.Since(c.connectedAt)
	}
	return time.Since(t)
}

type chatHub struct {
	mu           sync.RWMutex
	clients      map[string]map[string]*chatClient // master_agent_id -> client_id -> client
	waiters      map[string]chan ChatEvent
	agentClients map[string]map[string]struct{} // agent_id -> client_id set
}

var hub = &chatHub{
	clients:      make(map[string]map[string]*chatClient),
	waiters:      make(map[string]chan ChatEvent),
	agentClients: make(map[string]map[string]struct{}),
}

func (h *chatHub) ensureMapsLocked() {
	if h.clients == nil {
		h.clients = make(map[string]map[string]*chatClient)
	}
	if h.waiters == nil {
		h.waiters = make(map[string]chan ChatEvent)
	}
	if h.agentClients == nil {
		h.agentClients = make(map[string]map[string]struct{})
	}
}

func (c *chatClient) close() {
	c.closeOnce.Do(func() {
		close(c.send)
		_ = c.conn.Close()
	})
}

// closeWithReason sends a WebSocket close frame with the given code/reason and
// then tears down the connection. Used to signal application-level conditions
// (e.g. 4409 "superseded") so the client can decide not to auto-reconnect.
func (c *chatClient) closeWithReason(code int, reason string) {
	c.closeOnce.Do(func() {
		if c.conn != nil {
			_ = c.conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(code, reason),
				time.Now().Add(2*time.Second),
			)
		}
		close(c.send)
		_ = c.conn.Close()
	})
}

func (h *chatHub) stats() interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()
	type clientInfo struct {
		MasterAgentID string `json:"master_agent_id"`
		ActiveAgentID string `json:"active_agent_id"`
		ClientID      string `json:"client_id"`
		IsElectron    bool   `json:"isElectron"`
		Platform      string `json:"platform"`
		UserAgent     string `json:"user_agent"`
		RemoteAddr    string `json:"remote_addr"`
		ConnectedAt   string `json:"connected_at"`
		UptimeSec     int    `json:"uptime_sec"`
	}
	out := make([]clientInfo, 0)
	for agentID, m := range h.clients {
		for clientID, c := range m {
			out = append(out, clientInfo{
				MasterAgentID: agentID,
				ActiveAgentID: c.activeAgent,
				ClientID:      clientID,
				IsElectron:    c.electron,
				Platform:      c.platform,
				UserAgent:     c.userAgent,
				RemoteAddr:    c.remoteAddr,
				ConnectedAt:   c.connectedAt.Format(time.RFC3339),
				UptimeSec:     int(time.Since(c.connectedAt).Seconds()),
			})
		}
	}
	return out
}

func handleWsClients(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(hub.stats())
}

func (h *chatHub) lookupClientLocked(clientID string) *chatClient {
	for _, bucket := range h.clients {
		if c := bucket[clientID]; c != nil {
			return c
		}
	}
	return nil
}

// activeClientGrace is how long an existing connection is considered "alive"
// after its last read/pong. New connections that arrive with the same
// (agentID, clientID) while the existing one is within grace are REJECTED with
// 4409, protecting the current winner from being kicked off the slot. This
// breaks supersede loops where new extension instances appear periodically
// (e.g. iframe reloads, code-server extension host restarts). Past the grace,
// the existing connection is presumed stale and the new one replaces it.
const activeClientGrace = 30 * time.Second

// register tries to install c as the active client for (agentID, clientID).
// Returns true on success. Returns false if an active winner already holds
// the slot — the caller is expected to close c with a 4409 close frame so the
// browser-side client knows not to auto-reconnect.
func (h *chatHub) register(c *chatClient) bool {
	c.markActive()
	h.mu.Lock()
	h.ensureMapsLocked()
	if h.clients[c.agentID] == nil {
		h.clients[c.agentID] = make(map[string]*chatClient)
	}
	existing := h.clients[c.agentID][c.clientID]
	if existing != nil && existing != c && existing.idleFor() < activeClientGrace {
		idle := existing.idleFor()
		h.mu.Unlock()
		log.Printf("[chat-ws] reject (slot held) master_agent_id=%s client_id=%s existing_idle=%s",
			c.agentID, c.clientID, idle.Truncate(time.Millisecond))
		return false
	}
	var replaced *chatClient
	if existing != nil && existing != c {
		replaced = existing
	}
	h.clients[c.agentID][c.clientID] = c
	count := len(h.clients[c.agentID])
	h.mu.Unlock()
	if replaced != nil {
		replaced.closeWithReason(4409, "superseded (existing stale)")
		log.Printf("[chat-ws] supersede master_agent_id=%s client_id=%s", c.agentID, c.clientID)
	}
	log.Printf("[chat-ws] connect master_agent_id=%s client_id=%s clients=%d", c.agentID, c.clientID, count)
	return true
}

func (h *chatHub) unregister(c *chatClient) {
	h.mu.Lock()
	h.ensureMapsLocked()
	if m, ok := h.clients[c.agentID]; ok {
		if current, ok := m[c.clientID]; ok && current == c {
			delete(m, c.clientID)
		}
		if len(m) == 0 {
			delete(h.clients, c.agentID)
		}
	}
	for agentID, clients := range h.agentClients {
		if clients == nil {
			continue
		}
		delete(clients, c.clientID)
		if len(clients) == 0 {
			delete(h.agentClients, agentID)
		}
	}
	h.mu.Unlock()
	c.close()
	log.Printf("[chat-ws] disconnect master_agent_id=%s client_id=%s", c.agentID, c.clientID)
}

// Deprecated: broadcast routes by the websocket's master agent bucket, not the
// page's currently selected agent. Prefer direct client_id delivery for new work.
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
	if evt.Type != "system_resources" && evt.Type != "ai_chunk" && evt.Type != "status_change" {
		log.Printf("[chat-ws] broadcast master_agent_id=%s type=%s clients=%d", agentID, evt.Type, n)
	}
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

func (h *chatHub) sendToClient(clientID string, evt ChatEvent) bool {
	b, _ := json.Marshal(evt)
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.lookupClientLocked(clientID)
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

func (h *chatHub) registerClientAgent(clientID, agentID string) {
	clientID = strings.TrimSpace(clientID)
	agentID = normalizeChatAgentValue(agentID)
	if clientID == "" || agentID == "" {
		return
	}
	h.mu.Lock()
	h.ensureMapsLocked()
	defer h.mu.Unlock()
	for existingAgentID, clients := range h.agentClients {
		if clients == nil {
			continue
		}
		delete(clients, clientID)
		if len(clients) == 0 {
			delete(h.agentClients, existingAgentID)
		}
	}
	if h.agentClients[agentID] == nil {
		h.agentClients[agentID] = make(map[string]struct{})
	}
	h.agentClients[agentID][clientID] = struct{}{}
	if c := h.lookupClientLocked(clientID); c != nil {
		c.activeAgent = agentID
	}
	log.Printf("[chat-ws] register agent client agent_id=%s client_id=%s", agentID, clientID)
}

func normalizeChatClientPlatform(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "win", "windows":
		return "win"
	case "darwin", "mac", "macos", "osx":
		return "darwin"
	case "linux":
		return "linux"
	default:
		return ""
	}
}

func parseChatClientElectronValue(value interface{}) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "y", "on":
			return true, true
		case "0", "false", "no", "n", "off":
			return false, true
		}
	}
	return false, false
}

func (h *chatHub) updateClientMetadata(clientID string, platform string, userAgent string, electron *bool) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.lookupClientLocked(clientID)
	if c == nil {
		return
	}
	if normalized := normalizeChatClientPlatform(platform); normalized != "" {
		c.platform = normalized
	}
	if ua := strings.TrimSpace(userAgent); ua != "" {
		c.userAgent = ua
	}
	if electron != nil {
		c.electron = *electron
	}
}

func (h *chatHub) publishAgent(agentID string, evt ChatEvent) {
	agentID = normalizeChatAgentValue(agentID)
	if agentID == "" {
		return
	}
	appendRuntimeEvent(agentID, evt.Type, evt.Data)
	b, _ := json.Marshal(evt)
	h.mu.RLock()
	clientIDs := make([]string, 0, len(h.agentClients[agentID]))
	for clientID := range h.agentClients[agentID] {
		clientIDs = append(clientIDs, clientID)
	}
	h.mu.RUnlock()
	if evt.Type != "system_resources" && evt.Type != "ai_chunk" && evt.Type != "status_change" {
		log.Printf("[chat-ws] publish agent_id=%s type=%s clients=%d", agentID, evt.Type, len(clientIDs))
	}
	for _, clientID := range clientIDs {
		h.mu.RLock()
		c := h.lookupClientLocked(clientID)
		h.mu.RUnlock()
		if c == nil {
			continue
		}
		select {
		case c.send <- b:
		default:
		}
	}
}

func (h *chatHub) registerWaiter(requestID string) chan ChatEvent {
	ch := make(chan ChatEvent, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.waiters[requestID] = ch
	return ch
}

func (h *chatHub) resolveWaiter(requestID string, evt ChatEvent) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := h.waiters[requestID]
	if ch == nil {
		return false
	}
	delete(h.waiters, requestID)
	select {
	case ch <- evt:
	default:
	}
	close(ch)
	return true
}

func (h *chatHub) cancelWaiter(requestID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := h.waiters[requestID]
	if ch == nil {
		return
	}
	delete(h.waiters, requestID)
	close(ch)
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
		c.markActive()
		return nil
	})
	for {
		c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		c.markActive()
		var evt ChatEvent
		if json.Unmarshal(msg, &evt) != nil || evt.Type == "" {
			continue
		}
		// poll_request: 回复当前 poll 数据给请求方
		if evt.Type == "poll_request" {
			if workspace := paneWorkspace(c.agentID); strings.TrimSpace(workspace) != "" {
				if err := ensureWorkspaceHomeLink(workspace); err != nil {
					log.Printf("[poll_request] ensure workspace home link failed master_agent_id=%s workspace=%s err=%v", c.agentID, workspace, err)
				}
			}
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
		if evt.Type == "ping" {
			reply := ChatEvent{Type: "pong", Data: evt.Data}
			if b, err := json.Marshal(reply); err == nil {
				select {
				case c.send <- b:
				default:
				}
			}
			continue
		}
		if evt.Type == "register_active_channel" {
			data := aiGatewayMap(evt.Data)
			agentID := normalizeChatAgentValue(aiGatewayFirstNonEmpty(aiGatewayString(data["agent_id"]), aiGatewayString(data["pane_id"])))
			platform := aiGatewayFirstNonEmpty(aiGatewayString(data["platform"]), aiGatewayString(data["os"]))
			userAgent := aiGatewayFirstNonEmpty(aiGatewayString(data["user_agent"]), aiGatewayString(data["userAgent"]))
			var electronPtr *bool
			if value, ok := parseChatClientElectronValue(data["isElectron"]); ok {
				electronPtr = &value
			} else if value, ok := parseChatClientElectronValue(data["electron"]); ok {
				electronPtr = &value
			}
			hub.updateClientMetadata(c.clientID, platform, userAgent, electronPtr)
			if agentID != "" {
				hub.registerClientAgent(c.clientID, agentID)
			}
			continue
		}
		if evt.Type == "webpage_pong" || evt.Type == "exec_js_result" {
			requestID := strings.TrimSpace(aiGatewayString(aiGatewayMap(evt.Data)["requestId"]))
			if requestID != "" && hub.resolveWaiter(requestID, evt) {
				continue
			}
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
		"success":          true,
		"pane_id":          shortPaneID(normPaneID(paneID)),
		"agents":           agents,
		"statuses":         M{},
		"system_resources": systemResources.getLatest(),
		"server_time":      time.Now().UTC().Format(time.RFC3339),
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
	value := strings.TrimSpace(r.URL.Query().Get("master_agent_id"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("agent_id"))
	}
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("pane"))
	}
	return normalizeChatAgentValue(value)
}

// GET /api/chat/ws?master_agent_id=xxx&token=xxx — WebSocket upgrade
// master_agent_id here is the master websocket channel id, not the page's current agent.
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
	isElectron := r.URL.Query().Get("electron") == "1"
	c := &chatClient{
		conn:        conn,
		send:        make(chan []byte, 64),
		agentID:     agentID,
		clientID:    clientID,
		electron:    isElectron,
		platform:    normalizeChatClientPlatform(r.URL.Query().Get("platform")),
		userAgent:   strings.TrimSpace(r.Header.Get("User-Agent")),
		connectedAt: time.Now(),
		remoteAddr:  remoteAddr,
	}
	if !hub.register(c) {
		// Slot is held by an active winner; tell this client to give up.
		c.closeWithReason(4409, "slot held by active connection")
		return
	}
	go c.writePump()
	c.readPump()
}

// POST /api/chat/webhook — mitmproxy pushes events
func handleChatPingClient(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var req struct {
		ClientID      string `json:"client_id"`
		MasterAgentID string `json:"master_agent_id"`
		AgentID       string `json:"agent_id"`
		Pane          string `json:"pane"`
		Timeout       int    `json:"timeout_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" {
		http.Error(w, "client_id required", 400)
		return
	}
	timeoutMS := req.Timeout
	if timeoutMS <= 0 {
		timeoutMS = 5000
	}
	requestID := "ping-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	ch := hub.registerWaiter(requestID)
	if !hub.sendToClient(clientID, ChatEvent{Type: "webpage_ping", Data: M{"requestId": requestID}}) {
		hub.cancelWaiter(requestID)
		http.Error(w, "client not found", 404)
		return
	}
	select {
	case evt, ok := <-ch:
		if !ok {
			http.Error(w, "waiter canceled", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"mode":    "direct",
			"type":    evt.Type,
			"data":    evt.Data,
		})
	case <-time.After(time.Duration(timeoutMS) * time.Millisecond):
		hub.cancelWaiter(requestID)
		http.Error(w, "ping timeout", 504)
	}
}

func handleChatExecJS(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var req struct {
		ClientID string `json:"client_id"`
		Code     string `json:"code"`
		Timeout  int    `json:"timeout_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" {
		http.Error(w, "client_id required", 400)
		return
	}
	code := req.Code
	if strings.TrimSpace(code) == "" {
		http.Error(w, "code required", 400)
		return
	}
	timeoutMS := req.Timeout
	if timeoutMS <= 0 {
		timeoutMS = 5000
	}
	requestID := "exec-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	ch := hub.registerWaiter(requestID)
	if !hub.sendToClient(clientID, ChatEvent{Type: "exec_js", Data: M{"requestId": requestID, "code": code}}) {
		hub.cancelWaiter(requestID)
		http.Error(w, "client not found", 404)
		return
	}
	select {
	case evt, ok := <-ch:
		if !ok {
			http.Error(w, "waiter canceled", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"mode":    "direct",
			"type":    evt.Type,
			"data":    evt.Data,
		})
	case <-time.After(time.Duration(timeoutMS) * time.Millisecond):
		hub.cancelWaiter(requestID)
		http.Error(w, "exec_js timeout", 504)
	}
}

func handleChatWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID      string      `json:"client_id"`
		MasterAgentID string      `json:"master_agent_id"`
		AgentID       string      `json:"agent_id"`
		Pane          string      `json:"pane"`
		Event         string      `json:"event"`
		Data          interface{} `json:"data"`
	}
	if readBody(r, &req) != nil || req.Event == "" {
		httpErr(w, 400, "event required")
		return
	}
	evt := ChatEvent{Type: req.Event, Data: req.Data}
	if req.ClientID != "" {
		if !hub.sendToClient(req.ClientID, evt) {
			httpErr(w, 404, "client not found")
			return
		}
		log.Printf("[chat-webhook] client_id=%s type=%s mode=direct", req.ClientID, req.Event)
		w.WriteHeader(204)
		return
	}
	agentID := normalizeChatAgentValue(req.MasterAgentID)
	if agentID == "" {
		agentID = normalizeChatAgentValue(req.AgentID)
	}
	if agentID == "" {
		agentID = normalizeChatAgentValue(req.Pane)
	}
	if agentID == "" {
		httpErr(w, 400, "master_agent_id required for broadcast")
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
		ClientID      string      `json:"client_id"`
		MasterAgentID string      `json:"master_agent_id"`
		AgentID       string      `json:"agent_id"`
		Pane          string      `json:"pane"`
		Type          string      `json:"type"`
		Data          interface{} `json:"data"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}
	if req.Type == "" {
		http.Error(w, "type required", 400)
		return
	}

	if req.ClientID != "" {
		ok := hub.sendToClient(req.ClientID, ChatEvent{Type: req.Type, Data: req.Data})
		if !ok {
			http.Error(w, "client not found", 404)
			return
		}
		log.Printf("[chat-push] client_id=%s type=%s mode=direct", req.ClientID, req.Type)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "mode": "direct"})
		return
	}
	agentID := normalizeChatAgentValue(req.MasterAgentID)
	if agentID == "" {
		agentID = normalizeChatAgentValue(req.AgentID)
	}
	if agentID == "" {
		agentID = normalizeChatAgentValue(req.Pane)
	}
	if agentID == "" {
		http.Error(w, "master_agent_id required for broadcast", 400)
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
	log.Printf("[chat-push] master_agent_id=%s type=%s", agentID, req.Type)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "mode": "broadcast"})
}

func handleChatDebug(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(r.Header)
}
