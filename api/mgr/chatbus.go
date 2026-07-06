// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

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
	publicIp       string // egress IP reported by a cicy-desktop client (its own machine, not remoteAddr)
	ipRegion       string // that IP's geo region (country / region / city)
	systemLanguage string // the client machine's OS locale
	deviceId       string // cicy-desktop's stable deviceId
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
	lastReject   map[string]time.Time           // client_id -> last 4409-reject time (for persistent-reconnect supersede)
	// bridges maps a "virtual" client_id (e.g. "<pageClientId>:code-ext") to
	// the real connected page client that owns it. Used by the native-files
	// :code-ext bridge so agent-editor's host.* RPCs still resolve to a
	// connected client without a separate WS. Cleared when the owning client
	// disconnects.
	bridges map[string]*chatClient
}

var hub = &chatHub{
	clients:      make(map[string]map[string]*chatClient),
	waiters:      make(map[string]chan ChatEvent),
	agentClients: make(map[string]map[string]struct{}),
	bridges:      make(map[string]*chatClient),
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
	if h.bridges == nil {
		h.bridges = make(map[string]*chatClient)
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

// isElectronUserAgent returns true when the given UA string looks like an
// Electron client. Electron embeds "Electron/<version>" in its UA; cicy-desktop
// additionally prefixes "ElectronMCP/<version>". Case-insensitive substring
// match keeps the test robust to future UA tweaks.
func isElectronUserAgent(ua string) bool {
	if ua == "" {
		return false
	}
	return strings.Contains(strings.ToLower(ua), "electron")
}

func (h *chatHub) stats() interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()
	type clientInfo struct {
		// NOTE: these two are cosmetic/metadata for display only. Routing to a
		// specific client is done via ClientID alone — never reason about
		// agent ids when targeting a known client. Field names are
		// intentionally long to make that contract obvious to LLM consumers.
		MasterAgentID  string `json:"non_routing_master_agent_id_display_only"`
		ActiveAgentID  string `json:"non_routing_active_agent_id_display_only"`
		ClientID       string `json:"client_id"`
		IsElectron     bool   `json:"isElectron"`
		Platform       string `json:"platform"`
		UserAgent      string `json:"user_agent"`
		RemoteAddr     string `json:"remote_addr"`
		PublicIp       string `json:"public_ip"`
		IpRegion       string `json:"ip_region"`
		SystemLanguage string `json:"system_language"`
		DeviceId       string `json:"device_id"`
		ConnectedAt    string `json:"connected_at"`
		UptimeSec      int    `json:"uptime_sec"`
	}
	out := make([]clientInfo, 0)
	for agentID, m := range h.clients {
		for clientID, c := range m {
			out = append(out, clientInfo{
				MasterAgentID:  agentID,
				ActiveAgentID:  c.activeAgent,
				ClientID:       clientID,
				IsElectron:     c.electron,
				Platform:       c.platform,
				UserAgent:      c.userAgent,
				RemoteAddr:     c.remoteAddr,
				PublicIp:       c.publicIp,
				IpRegion:       c.ipRegion,
				SystemLanguage: c.systemLanguage,
				DeviceId:       c.deviceId,
				ConnectedAt:    c.connectedAt.Format(time.RFC3339),
				UptimeSec:      int(time.Since(c.connectedAt).Seconds()),
			})
		}
	}
	return out
}

func handleWsClients(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(hub.stats())
}

// hasWebpageClientForAgent reports whether at least one non-bridge webpage
// client is currently registered for the given master agent. Used by the
// Team Helper auto-kick so we don't send "start" before the user's desktop
// drawer webview has actually connected — otherwise the agent's
// `agent-webpage clients` call would return empty and the language probe
// would skip straight to the English fallback.
func (h *chatHub) hasWebpageClientForAgent(masterAgentID string) bool {
	if strings.TrimSpace(masterAgentID) == "" {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	bucket := h.clients[masterAgentID]
	for clientID := range bucket {
		// :code-ext is the native-files bridge alias, not a real webpage
		// client — skip it for the readiness check.
		if strings.HasSuffix(clientID, ":code-ext") {
			continue
		}
		return true
	}
	return false
}

func (h *chatHub) lookupClientLocked(clientID string) *chatClient {
	for _, bucket := range h.clients {
		if c := bucket[clientID]; c != nil {
			return c
		}
	}
	// Fall through to bridge aliases — e.g. agent-editor addressing
	// "<page>:code-ext" routes to the native-files bridge running inside the
	// page's existing chat-ws connection.
	if c := h.bridges[clientID]; c != nil {
		return c
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
	// A holder whose send buffer is full is a stuck/dead consumer (its write
	// pump isn't draining — e.g. a half-open conn behind a proxy). Don't let the
	// grace window protect it; let this genuine reconnect supersede it, otherwise
	// it lingers forever and breaks server→client RPC (exec-js, push, etc.).
	existingStuck := existing != nil && len(existing.send) >= cap(existing.send)
	if existing != nil && existing != c && existing.idleFor() < activeClientGrace && !existingStuck {
		if h.lastReject == nil {
			h.lastReject = make(map[string]time.Time)
		}
		last := h.lastReject[c.clientID]
		if last.IsZero() || time.Since(last) > activeClientGrace {
			// First reject for this client_id: protect the current holder so a
			// burst of new instances (iframe reloads, ext-host restarts) can't kick it.
			h.lastReject[c.clientID] = time.Now()
			idle := existing.idleFor()
			h.mu.Unlock()
			log.Printf("[chat-ws] reject (slot held) master_agent_id=%s client_id=%s existing_idle=%s",
				c.agentID, c.clientID, idle.Truncate(time.Millisecond))
			return false
		}
		// Same client_id keeps reconnecting despite a recent 4409 — the holder
		// is almost certainly a stale half-open connection (e.g. left by a
		// proxy). Let this genuine reconnect take over.
		log.Printf("[chat-ws] supersede (persistent reconnect) master_agent_id=%s client_id=%s holder_idle=%s",
			c.agentID, c.clientID, existing.idleFor().Truncate(time.Millisecond))
	}
	if h.lastReject != nil {
		delete(h.lastReject, c.clientID)
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
	// Drop any bridge aliases this client owned.
	for alias, owner := range h.bridges {
		if owner == c {
			delete(h.bridges, alias)
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
	if evt.Type != "system_resources" && evt.Type != "ai_chunk" && evt.Type != "status_change" && evt.Type != "thinking_chunk" {
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

func (h *chatHub) updateClientMetadata(clientID string, platform string, userAgent string) {
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
	if userAgent != "" {
		c.electron = isElectronUserAgent(userAgent)
	}
}

// updateClientDeviceInfo stores the desktop-reported machine info on the client.
// Each field only overwrites when non-empty (so a later register without the
// field doesn't wipe it).
func (h *chatHub) updateClientDeviceInfo(clientID, publicIp, ipRegion, systemLanguage, deviceId string) {
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
	if v := strings.TrimSpace(publicIp); v != "" {
		c.publicIp = v
	}
	if v := strings.TrimSpace(ipRegion); v != "" {
		c.ipRegion = v
	}
	if v := strings.TrimSpace(systemLanguage); v != "" {
		c.systemLanguage = v
	}
	if v := strings.TrimSpace(deviceId); v != "" {
		c.deviceId = v
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
	if evt.Type != "system_resources" && evt.Type != "ai_chunk" && evt.Type != "status_change" && evt.Type != "thinking_chunk" {
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
	// 8MB: exec_js results carry artifact screenshots/PDFs as base64 (a
	// capturePage dataURL is easily 100KB-2MB). The old 64KB cap made gorilla
	// kill the connection on the oversized ack — capture/pdf 504'd and the
	// client got disconnected.
	c.conn.SetReadLimit(8 * 1024 * 1024)
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(35 * time.Second))
		c.markActive()
		return nil
	})
	for {
		c.conn.SetReadDeadline(time.Now().Add(35 * time.Second))
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
		if evt.Type == "register_code_ext_bridge" {
			// Page-side native-files bridge. The page registers an alias
			// "<pageClientId>:code-ext" → its own connection so agent-editor
			// host.* RPCs (which target ":code-ext") reach the page over its
			// existing chat-ws. data may carry an explicit "alias" too.
			data := aiGatewayMap(evt.Data)
			alias := strings.TrimSpace(aiGatewayString(data["alias"]))
			if alias == "" {
				alias = c.clientID + ":code-ext"
			}
			hub.mu.Lock()
			hub.ensureMapsLocked()
			hub.bridges[alias] = c
			hub.mu.Unlock()
			log.Printf("[chat-ws] bridge register alias=%s owner=%s", alias, c.clientID)
			continue
		}
		if evt.Type == "unregister_code_ext_bridge" {
			data := aiGatewayMap(evt.Data)
			alias := strings.TrimSpace(aiGatewayString(data["alias"]))
			if alias == "" {
				alias = c.clientID + ":code-ext"
			}
			hub.mu.Lock()
			if hub.bridges[alias] == c {
				delete(hub.bridges, alias)
			}
			hub.mu.Unlock()
			continue
		}
		if evt.Type == "register_active_channel" {
			data := aiGatewayMap(evt.Data)
			agentID := normalizeChatAgentValue(aiGatewayFirstNonEmpty(aiGatewayString(data["agent_id"]), aiGatewayString(data["pane_id"])))
			platform := aiGatewayFirstNonEmpty(aiGatewayString(data["platform"]), aiGatewayString(data["os"]))
			userAgent := aiGatewayFirstNonEmpty(aiGatewayString(data["user_agent"]), aiGatewayString(data["userAgent"]))
			// isElectron is re-derived from userAgent here; the client no longer
			// supplies it via the message payload.
			hub.updateClientMetadata(c.clientID, platform, userAgent)
			// cicy-desktop clients forward their machine's egress IP / IP region /
			// OS language / deviceId (from the desktop get_device_info RPC). Stored
			// for display in GET /api/chat/clients; only overwrites when non-empty.
			hub.updateClientDeviceInfo(
				c.clientID,
				aiGatewayFirstNonEmpty(aiGatewayString(data["public_ip"]), aiGatewayString(data["publicIp"])),
				aiGatewayFirstNonEmpty(aiGatewayString(data["ip_region"]), aiGatewayString(data["ipRegion"])),
				aiGatewayFirstNonEmpty(aiGatewayString(data["system_language"]), aiGatewayString(data["systemLanguage"])),
				aiGatewayFirstNonEmpty(aiGatewayString(data["device_id"]), aiGatewayString(data["deviceId"])),
			)
			if agentID != "" {
				hub.registerClientAgent(c.clientID, agentID)
			}
			continue
		}
		// Generic waiter routing: any incoming WS message whose data.requestId
		// matches a registered waiter is consumed (not broadcast). This lets
		// /api/chat/push (with wait_ack), /api/chat/exec-js, /api/chat/code-open
		// and /api/chat/ping-client all use the same one-shot RPC primitive
		// without each needing a type allowlist here.
		if requestID := strings.TrimSpace(aiGatewayString(aiGatewayMap(evt.Data)["requestId"])); requestID != "" {
			if hub.resolveWaiter(requestID, evt) {
				continue
			}
		}
		// 广播客户端发来的消息给同 agent 的其他客户端
		hub.broadcastExcept(c.agentID, evt, c)
	}
}

// pollAgentStatuses reads each agent's gateway reply snapshot
// (`<runtime>/history/reply.json`) and returns a map of full pane id →
// {status, updated_at}. reply.json's status is the live turn state written by
// the AI gateway: "thinking" while a turn is in flight, then "completed" or
// "failed". Agents without a snapshot are simply absent from the map.
func pollAgentStatuses(paneID string, agents []M) M {
	statuses := M{}
	seen := map[string]bool{}
	add := func(id string) {
		short := shortPaneID(normPaneID(strings.TrimSpace(id)))
		if short == "" || seen[short] {
			return
		}
		seen[short] = true
		// Rich header metrics (status/model/context/tokens/cost), so a single WS
		// poll_data push drives the whole team panel — no per-agent /current-reply
		// polling. agentInspectorLiteMetrics returns nil when there's no usable
		// reply snapshot (same skip-on-empty as the old {status,updated_at} read).
		if m := agentInspectorLiteMetrics(short); m != nil {
			statuses[short+":main.0"] = m
		}
	}
	add(paneID)
	for _, a := range agents {
		add(aiGatewayString(a["name"]))
	}
	return statuses
}

// buildPollData 构造 poll 响应数据，与 handlePoll 返回格式一致
func buildPollData(paneID string) M {
	agents, _ := listAgentsByPane(paneID)
	if agents == nil {
		agents = []M{}
	}
	snapshot := loadRuntimeMembershipSnapshot()
	// Two global dashboard badge counts ride poll_data so the web doesn't poll
	// /api/knowledge + /api/audit/events on their own timers (TTL-cached).
	knowledgePending, auditOpenIDs := dashboardCounts.get()
	resp := M{
		"success":           true,
		"pane_id":           shortPaneID(normPaneID(paneID)),
		"agents":            agents,
		"statuses":          pollAgentStatuses(paneID, agents),
		"system_resources":  systemResources.getLatest(),
		"server_time":       time.Now().UTC().Format(time.RFC3339),
		"knowledge_pending": knowledgePending,
		"audit_open_ids":    auditOpenIDs,
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
	// isElectron is derived from User-Agent, not a client-supplied query.
	// "Electron"/"ElectronMCP" appears in the UA of every Electron build.
	isElectron := isElectronUserAgent(strings.TrimSpace(r.Header.Get("User-Agent")))
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

// handleChatCodeOpen is the sync wrapper for opening a file in the code-server
// extension. Mirrors handleChatExecJS: register a waiter for `requestId`, push
// `host.open_file` directly to the `:code-ext` client, then block on the waiter
// until we see `code.opened` (success) or `code.open_file_error` (failure) come
// back over that client's own WS — both routed by readPump.
//
// This replaces the older "agent-editor → page → :code-ext" relay where
// the page's second push silently swallowed 404s when the extension wasn't
// connected. Now the 404 surfaces straight back to the caller, who can retry
// (e.g. after asking the user to bring code-server to the foreground) or give
// up cleanly instead of waiting 15s on a ghost ack.
func handleChatCodeOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var req struct {
		ClientID string `json:"client_id"`
		Path     string `json:"path"`
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
	path := strings.TrimSpace(req.Path)
	if path == "" {
		http.Error(w, "path required", 400)
		return
	}
	timeoutMS := req.Timeout
	if timeoutMS <= 0 {
		timeoutMS = 15000
	}
	requestID := "code-open-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	ch := hub.registerWaiter(requestID)
	if !hub.sendToClient(clientID, ChatEvent{Type: "host.open_file", Data: M{"requestId": requestID, "path": path, "code_client_id": clientID}}) {
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
			"success": evt.Type == "code.opened",
			"type":    evt.Type,
			"data":    evt.Data,
		})
	case <-time.After(time.Duration(timeoutMS) * time.Millisecond):
		hub.cancelWaiter(requestID)
		http.Error(w, "code-open timeout", 504)
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
//
// Two modes:
//
//  1. ASYNC / fire-and-forget (default; ` + "`wait_ack: false`" + ` or omitted)
//     Sends the message to target ` + "`client_id`" + ` (or broadcasts to all clients
//     of an agent if no client_id) and returns immediately. The receiver does
//     NOT acknowledge; any error inside the receiver is invisible. Use this
//     only when delivery failure is genuinely tolerable:
//     - UX hints to the page (e.g. ` + "`code.show_files`" + `, ` + "`status_change`" + `)
//     - One-to-many broadcasts (poll responses, ai chunks, current_updated)
//
//  2. SYNC / wait-for-ack (` + "`wait_ack: true`" + `, requires ` + "`client_id`" + `)
//     Injects a server-generated ` + "`requestId`" + ` into the message's data,
//     registers a waiter, sends, and BLOCKS until the target client writes
//     any WS message whose ` + "`data.requestId`" + ` matches. Returns that message
//     as the HTTP response (or 504 on timeout). The receiver-side convention
//     is: process the request, then write back a result message carrying the
//     same ` + "`requestId`" + ` (any type — readPump routes by id, not by type).
//     Use this when the caller needs to know "did it work?" — open file,
//     exec JS, ping, etc. Replaces the older "agent A pushes to page, page
//     relays to extension, no one knows if it landed" pattern that silently
//     swallowed errors.
//
// Either mode returns 404 immediately when the target ` + "`client_id`" + ` isn't
// connected; the caller can retry, surface the error, or fall back as needed.
//
// Companion sync endpoints (` + "`/api/chat/exec-js`" + `, ` + "`/api/chat/ping-client`" + `,
// ` + "`/api/chat/code-open`" + `) are kept for callers that already use them; they
// behave identically to ` + "`/api/chat/push`" + ` with ` + "`wait_ack: true`" + ` and a fixed
// event type, but the unified ` + "`/api/chat/push`" + ` endpoint is now the
// recommended primitive for new code.
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
		// WaitAck switches the handler into sync RPC mode (exec_js style):
		// the server injects a `requestId` into Data, blocks until the target
		// client writes back any message whose `data.requestId` matches, and
		// returns that message as the HTTP response. timeout_ms defaults to
		// 15000 if WaitAck is set without a TimeoutMS.
		WaitAck   bool `json:"wait_ack"`
		TimeoutMS int  `json:"timeout_ms"`
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
		if req.WaitAck {
			// Sync waiter mode. Caller wants confirmation from the target
			// client. Inject requestId, register, push, block on the waiter.
			timeoutMS := req.TimeoutMS
			if timeoutMS <= 0 {
				timeoutMS = 15000
			}
			requestID := "push-" + strconv.FormatInt(time.Now().UnixNano(), 36)
			data, _ := req.Data.(map[string]interface{})
			if data == nil {
				data = map[string]interface{}{}
			}
			data["requestId"] = requestID
			ch := hub.registerWaiter(requestID)
			if !hub.sendToClient(req.ClientID, ChatEvent{Type: req.Type, Data: data}) {
				hub.cancelWaiter(requestID)
				http.Error(w, "client not found", 404)
				return
			}
			log.Printf("[chat-push] client_id=%s type=%s mode=sync rid=%s", req.ClientID, req.Type, requestID)
			select {
			case evt, ok := <-ch:
				if !ok {
					http.Error(w, "waiter canceled", 500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
					"mode":    "sync",
					"type":    evt.Type,
					"data":    evt.Data,
				})
			case <-time.After(time.Duration(timeoutMS) * time.Millisecond):
				hub.cancelWaiter(requestID)
				http.Error(w, "wait_ack timeout", 504)
			}
			return
		}
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
