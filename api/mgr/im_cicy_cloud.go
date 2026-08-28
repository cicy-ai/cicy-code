// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const defaultCiCyCloudOrigin = "https://cicy-ai.com"

var cicyCloudEmailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
var cicyCloudTeamRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var cicyCloudMessageRE = regexp.MustCompile(`^msg-[A-Za-z0-9_-]{8,96}$`)
var cicyCloudPendingLogins sync.Map // state -> cicyCloudPendingLogin

var hubLoginStates sync.Map // local state -> hub login state

type cicyCloudPendingLogin struct {
	Team       string
	HubOrigin  string // non-empty → login was started against a cicy-ws-hub
	InstanceID string
}

func cicyCloudTraceID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

type cicyCloudCredential struct {
	Email      string `json:"email"`
	InstanceID string `json:"instance_id"`
	TeamID     string `json:"team_id"`
	Token      string `json:"token"`
	Origin     string `json:"cloud_origin"`
	// Mode "hub" means Origin is a cicy-ws-hub and the instance talks to it
	// directly (email login, tickets, directory) with no cicy-cloud worker.
	Mode      string `json:"mode,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

const cicyCloudModeHub = "hub"
const defaultCiCyHubOrigin = "https://ws.cicy-ai.com"

func cicyHubOrigin() string {
	origin := strings.TrimRight(strings.TrimSpace(os.Getenv("CICY_HUB_ORIGIN")), "/")
	if origin == "" {
		origin = defaultCiCyHubOrigin
	}
	return origin
}

func loadCiCyCloudCredential() (cicyCloudCredential, bool) {
	var cred cicyCloudCredential
	data, err := os.ReadFile(cicyCloudCredentialPath())
	if err != nil || json.Unmarshal(data, &cred) != nil {
		return cicyCloudCredential{}, false
	}
	return cred, true
}

// hubOriginForToken reports the hub origin when the given instance token was
// issued by a cicy-ws-hub (credential mode "hub"); "" means cicy-cloud.
func hubOriginForToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	cred, ok := loadCiCyCloudCredential()
	if !ok || cred.Mode != cicyCloudModeHub || strings.TrimSpace(cred.Token) != token {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(cred.Origin), "/")
}

func cicyCloudOrigin() string {
	origin := strings.TrimRight(strings.TrimSpace(os.Getenv("CICY_CLOUD_ORIGIN")), "/")
	if origin == "" {
		origin = defaultCiCyCloudOrigin
	}
	return origin
}

func cicyCloudCredentialPath() string { return filepath.Join(cicyDBDir, "cloud-device.json") }

func randomCodeInstanceID() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "code-" + hex.EncodeToString(b), nil
}

func randomLoginState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// cloudJSON talks to the control plane the token belongs to: the cicy-cloud
// worker, or — for a hub-issued token — the cicy-ws-hub with the worker
// routes mapped onto the hub API (see hubJSON).
func cloudJSON(method, route, token string, requestBody any, responseBody any) error {
	if hub := hubOriginForToken(token); hub != "" {
		return hubJSON(hub, method, route, token, requestBody, responseBody)
	}
	return cloudJSONAt(cicyCloudOrigin(), method, route, token, requestBody, responseBody)
}

var errHubWebSocketOnly = fmt.Errorf("hub mode: websocket not connected")

// hubJSON maps the worker routes used by the transport onto cicy-ws-hub.
// Anything the hub has no equivalent for (D1-backed HTTP message fallback,
// agent-config push, heartbeat telemetry) is a no-op: messages ride the
// websocket only, and the hub learns liveness from /api/register.
func hubJSON(origin, method, route, token string, requestBody any, responseBody any) error {
	path := route
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	switch {
	case method == http.MethodPost && path == "/api/code/ws-ticket":
		// Registering doubles as a heartbeat: telemetry, tunnel and live
		// resources go along so the directory and gateway are current.
		return cloudJSONAt(origin, http.MethodPost, "/api/register", token, hubHeartbeatBody(nil), responseBody)
	case method == http.MethodPost && path == "/api/code/gateway-grant":
		return cloudJSONAt(origin, http.MethodPost, "/api/gateway/grant", token, requestBody, responseBody)
	case method == http.MethodPost && path == "/api/code/instances/heartbeat":
		body, _ := requestBody.(M)
		return cloudJSONAt(origin, http.MethodPost, "/api/heartbeat", token, hubHeartbeatBody(body), nil)
	case method == http.MethodGet && path == "/api/code/instances":
		instances, err := hubInstances(origin, token)
		if err != nil {
			return err
		}
		return hubAssign(responseBody, M{"success": true, "instances": hubInstancesToCloud(instances)})
	case method == http.MethodGet && path == "/api/code/agents":
		instances, err := hubInstances(origin, token)
		if err != nil {
			return err
		}
		return hubAssign(responseBody, M{"success": true, "agents": hubAgentsToCloud(instances)})
	case path == "/api/code/messages" && method == http.MethodPost:
		return errHubWebSocketOnly
	case path == "/api/code/messages/poll":
		return hubAssign(responseBody, M{"success": true, "messages": []any{}})
	case path == "/api/code/agent-configs" && method == http.MethodGet:
		return hubAssign(responseBody, M{"success": true, "configs": []any{}})
	case path == "/api/code/agent-configs", path == "/api/code/agents":
		return nil
	}
	return cloudJSONAt(origin, method, route, token, requestBody, responseBody)
}

// hubHeartbeatBody merges the worker heartbeat payload (tunnelUrl /
// tunnelToken / ports) with host telemetry and the live resource sample the
// local /api/system/resources monitor already keeps.
func hubHeartbeatBody(base M) M {
	tele := collectCiCyCodeTelemetry()
	body := M{"platform": tele.Platform, "arch": tele.Arch, "runtime": tele.Runtime, "cpuModel": tele.CPUModel,
		"cpuCores": tele.CPUCores, "memoryTotalMB": tele.MemoryTotalMB, "gpu": tele.GPU, "version": version}
	for k, v := range base {
		body[k] = v
	}
	if systemResources != nil {
		if snap := systemResources.getLatest(); snap.UpdatedAt != "" {
			body["resources"] = snap
		}
	}
	if _, ok := body["tunnelUrl"]; !ok {
		if tunnelURL := strings.TrimSpace(cftCurrentURL()); tunnelURL != "" {
			body["tunnelUrl"] = tunnelURL
			body["tunnelToken"] = loadAPIToken()
		}
	}
	return body
}

func hubAssign(out any, value M) error {
	if out == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

type hubInstance struct {
	InstanceID     string          `json:"instanceId"`
	Name           string          `json:"name"`
	Platform       string          `json:"platform"`
	CreatedAt      string          `json:"createdAt"`
	LastLoginAt    string          `json:"lastLoginAt"`
	LastSeenAt     string          `json:"lastSeenAt"`
	Online         bool            `json:"online"`
	Self           bool            `json:"self"`
	Agents         json.RawMessage `json:"agents"`
	Arch           string          `json:"arch"`
	Runtime        string          `json:"runtime"`
	CPUModel       string          `json:"cpuModel"`
	CPUCores       int             `json:"cpuCores"`
	MemoryTotalMB  int             `json:"memoryTotalMB"`
	GPU            string          `json:"gpu"`
	PublicIP       string          `json:"publicIp"`
	Version        string          `json:"version"`
	ProxyHost      string          `json:"proxyHost"`
	ProxyAvailable bool            `json:"proxyAvailable"`
	Ports          json.RawMessage `json:"ports"`
	Resources      json.RawMessage `json:"resources"`
}

func hubInstances(origin, token string) ([]hubInstance, error) {
	var out struct {
		Owner     string        `json:"owner"`
		Instances []hubInstance `json:"instances"`
	}
	if err := cloudJSONAt(origin, http.MethodGet, "/api/instances", token, nil, &out); err != nil {
		return nil, err
	}
	return out.Instances, nil
}

// hubTeamID gives an instance the team-like label the workspace UI shows:
// the name chosen at login, else a short form of the instance id.
func hubTeamID(inst hubInstance) string {
	if name := strings.TrimSpace(inst.Name); name != "" {
		return name
	}
	id := strings.TrimPrefix(inst.InstanceID, "code-")
	if len(id) > 8 {
		id = id[:8]
	}
	return "code-" + id
}

func hubInstancesToCloud(instances []hubInstance) []M {
	out := make([]M, 0, len(instances))
	for _, inst := range instances {
		status := "offline"
		if inst.Online {
			status = "online"
		}
		rt := inst.Runtime
		if rt == "" {
			rt = "native"
		}
		proxyAvailable := 0
		if inst.ProxyAvailable {
			proxyAvailable = 1
		}
		row := M{"instanceId": inst.InstanceID, "teamId": hubTeamID(inst), "status": status,
			"platform": inst.Platform, "arch": inst.Arch, "runtime": rt, "createdAt": inst.CreatedAt,
			"lastSeenAt": inst.LastSeenAt, "cpuModel": inst.CPUModel, "cpuCores": inst.CPUCores,
			"memoryTotalMB": inst.MemoryTotalMB, "gpu": inst.GPU, "publicIp": inst.PublicIP, "version": inst.Version,
			"proxyHost": inst.ProxyHost, "proxyAvailable": proxyAvailable, "hub": true, "self": inst.Self}
		if len(inst.Ports) > 0 {
			row["ports"] = inst.Ports
		}
		if len(inst.Resources) > 0 {
			row["resources"] = inst.Resources
		}
		out = append(out, row)
	}
	return out
}

func hubAgentsToCloud(instances []hubInstance) []M {
	out := []M{}
	for _, inst := range instances {
		if len(inst.Agents) == 0 {
			continue
		}
		var agents []M
		if json.Unmarshal(inst.Agents, &agents) != nil {
			continue
		}
		team := hubTeamID(inst)
		for _, a := range agents {
			agentID := strings.TrimSpace(fmt.Sprint(a["agentId"]))
			if agentID == "" || agentID == "<nil>" {
				agentID = strings.TrimSpace(fmt.Sprint(a["id"]))
			}
			if agentID == "" || agentID == "<nil>" {
				continue
			}
			row := M{}
			for k, v := range a {
				row[k] = v
			}
			row["instanceId"] = inst.InstanceID
			row["agentId"] = agentID
			row["teamId"] = team
			if _, ok := row["agentType"]; !ok {
				row["agentType"] = a["agent_type"]
			}
			row["instanceOnline"] = inst.Online
			out = append(out, row)
		}
	}
	return out
}

func cloudJSONAt(origin, method, route, token string, requestBody any, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		data, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, origin+route, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("cicy-code/%s (%s; %s)", version, runtime.GOOS, runtime.GOARCH))
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var out struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &out)
		if out.Error == "" {
			out.Error = fmt.Sprintf("cloud HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("%s", out.Error)
	}
	if responseBody != nil {
		return json.Unmarshal(data, responseBody)
	}
	return nil
}

func saveCiCyCloudCredential(cred cicyCloudCredential) error {
	if err := os.MkdirAll(cicyDBDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cred, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(cicyDBDir, ".cloud-device.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, cicyCloudCredentialPath())
}

func upsertCiCyCloudIMAccount(cred cicyCloudCredential) (*imAccount, error) {
	cfg, _ := json.Marshal(M{"email": cred.Email, "instance_id": cred.InstanceID, "team_id": cred.TeamID, "cloud_origin": cred.Origin, "mode": cred.Mode})
	var id int64
	err := store.QueryRow("SELECT id FROM im_accounts WHERE platform=? ORDER BY id LIMIT 1", imPlatformCiCyCloud).Scan(&id)
	if err == nil {
		_, err = store.Exec("UPDATE im_accounts SET name=?,secret=?,config=?,enabled=1,state='connected',state_detail='',updated_at="+store.Now()+" WHERE id=?",
			cred.Email, cred.Token, string(cfg), id)
	} else {
		res, insertErr := store.Exec("INSERT INTO im_accounts (platform,name,secret,config,enabled,state,state_detail,inbound_to_agent,bound_pane_id) VALUES (?,?,?,?,1,'connected','',1,'w-1001')",
			imPlatformCiCyCloud, cred.Email, cred.Token, string(cfg))
		if insertErr != nil {
			return nil, insertErr
		}
		id, _ = res.LastInsertId()
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return imGetAccount(id)
}

func ensureCiCyCloudAccountFromEnvironment() error {
	if store == nil {
		return nil
	}
	cred := cicyCloudCredential{
		Email:      strings.TrimSpace(os.Getenv("CICY_CLOUD_EMAIL")),
		InstanceID: strings.TrimSpace(os.Getenv("CICY_CLOUD_INSTANCE_ID")),
		TeamID:     strings.TrimSpace(os.Getenv("CICY_CLOUD_TEAM_ID")),
		Token:      strings.TrimSpace(os.Getenv("CICY_CLOUD_TOKEN")),
		Origin:     cicyCloudOrigin(),
	}
	if data, err := os.ReadFile(cicyCloudCredentialPath()); err == nil {
		var saved cicyCloudCredential
		if json.Unmarshal(data, &saved) == nil {
			if cred.Email == "" {
				cred.Email = saved.Email
			}
			if cred.InstanceID == "" {
				cred.InstanceID = saved.InstanceID
			}
			if cred.Token == "" {
				cred.Token = saved.Token
			}
			if cred.TeamID == "" {
				cred.TeamID = saved.TeamID
			}
			if saved.Origin != "" {
				cred.Origin = saved.Origin
			}
			cred.Mode = saved.Mode
		}
	}
	if cred.Email == "" || cred.InstanceID == "" || cred.Token == "" {
		return nil
	}
	_, err := upsertCiCyCloudIMAccount(cred)
	return err
}

type cicyCloudTransport struct {
	accountID     int64
	token         string
	presenceMu    sync.Mutex
	stateMu       sync.Mutex
	lastHeartbeat time.Time
	lastPresence  time.Time
	pollMu        sync.Mutex
	idlePollDelay time.Duration
	streamOnce    sync.Once
	streamWake    chan struct{}
	streamStop    chan struct{}
	streamClose   sync.Once
	streamConnMu  sync.Mutex
	streamConn    *websocket.Conn
	streamEpoch   uint64
	streamWriteMu sync.Mutex
	streamInbox   chan cicyCloudStreamMessage
	streamWaiters map[string]chan cicyCloudServerFrame
	streamCursor  int64
	streamLogMu   sync.Mutex
	streamLogKey  string
	streamLogAt   time.Time
	messageMu     sync.Mutex
	sentMessages  map[string]cicyCloudLocalMessageState
}

type cicyCloudLocalMessageState struct {
	Transport  string
	SentAtMS   int64
	Reply      cicyCloudStreamMessage
	ReceivedMS int64
}

type cicyCloudStreamMessage struct {
	ID               string `json:"id"`
	SenderInstanceID string `json:"senderInstanceId"`
	SenderAgentID    string `json:"senderAgentId"`
	TargetAgentID    string `json:"targetAgentId"`
	Kind             string `json:"kind"`
	Text             string `json:"text"`
	ReplyTo          string `json:"replyTo"`
	EnqueuedAtMS     int64  `json:"enqueuedAtMs"`
}

type cicyCloudServerFrame struct {
	Type      string                 `json:"type"`
	RequestID string                 `json:"requestId"`
	Cursor    int64                  `json:"cursor"`
	ID        string                 `json:"id"`
	Error     string                 `json:"error"`
	Revision  uint64                 `json:"revision"`
	Message   cicyCloudStreamMessage `json:"message"`
}

const (
	cicyCloudHeartbeatInterval = 30 * time.Second
	cicyCloudPresenceInterval  = 120 * time.Second
	cicyCloudPollMinDelay      = 2 * time.Second
	cicyCloudPollMaxDelay      = 15 * time.Second
	cicyCloudStreamMinBackoff  = time.Second
	cicyCloudStreamMaxBackoff  = 30 * time.Second
	cicyCloudStreamLogInterval = 5 * time.Minute
)

func (t *cicyCloudTransport) logStreamFailure(stage string, status int, err error) {
	key := fmt.Sprintf("%s:%d:%T", stage, status, err)
	now := time.Now()
	t.streamLogMu.Lock()
	if key == t.streamLogKey && now.Sub(t.streamLogAt) < cicyCloudStreamLogInterval {
		t.streamLogMu.Unlock()
		return
	}
	t.streamLogKey = key
	t.streamLogAt = now
	t.streamLogMu.Unlock()
	// Do not include err.Error() for websocket dial/read failures: library
	// errors may embed the request URL, whose query contains the signed ticket.
	if status > 0 {
		log.Printf("[im-cloud] websocket %s failed account=%d status=%d error_type=%T", stage, t.accountID, status, err)
		return
	}
	log.Printf("[im-cloud] websocket %s failed account=%d error_type=%T", stage, t.accountID, err)
}

func (t *cicyCloudTransport) logStreamConnected(host string) {
	t.streamLogMu.Lock()
	t.streamLogKey = ""
	t.streamLogAt = time.Time{}
	t.streamLogMu.Unlock()
	log.Printf("[im-cloud] websocket connected account=%d host=%s", t.accountID, host)
}

func (t *cicyCloudTransport) initStream() {
	t.streamOnce.Do(func() {
		t.streamWake = make(chan struct{}, 1)
		t.streamStop = make(chan struct{})
		t.streamInbox = make(chan cicyCloudStreamMessage, 256)
		t.streamWaiters = make(map[string]chan cicyCloudServerFrame)
		if os.Getenv("CICY_CLOUD_DISABLE_WS") != "1" {
			go t.runMessageStream()
		}
	})
}

func (t *cicyCloudTransport) signalStreamWake() {
	select {
	case t.streamWake <- struct{}{}:
	default:
	}
}

func (t *cicyCloudTransport) waitForNextPoll(delay time.Duration) {
	t.initStream()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-t.streamWake:
	case <-timer.C:
	case <-t.streamStop:
	}
}

func (t *cicyCloudTransport) cicyCloudStreamURL() (string, error) {
	var ticket struct {
		Ticket string `json:"ticket"`
		WSURL  string `json:"wsUrl"`
	}
	if err := cloudJSON(http.MethodPost, "/api/code/ws-ticket", t.token, M{}, &ticket); err != nil {
		return "", err
	}
	u, err := url.Parse(strings.TrimSpace(ticket.WSURL))
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || strings.TrimSpace(ticket.Ticket) == "" {
		return "", fmt.Errorf("invalid cloud websocket ticket")
	}
	q := u.Query()
	q.Set("ticket", ticket.Ticket)
	t.streamConnMu.Lock()
	q.Set("cursor", strconv.FormatInt(t.streamCursor, 10))
	t.streamConnMu.Unlock()
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func nextCiCyCloudStreamBackoff(current time.Duration) time.Duration {
	if current < cicyCloudStreamMinBackoff {
		return cicyCloudStreamMinBackoff
	}
	next := current * 2
	if next > cicyCloudStreamMaxBackoff {
		return cicyCloudStreamMaxBackoff
	}
	return next
}

func (t *cicyCloudTransport) runMessageStream() {
	backoff := time.Duration(0)
	for {
		select {
		case <-t.streamStop:
			return
		default:
		}

		streamURL, err := t.cicyCloudStreamURL()
		if err != nil {
			t.logStreamFailure("ticket", 0, err)
		} else {
			headers := http.Header{}
			headers.Set("User-Agent", fmt.Sprintf("cicy-code/%s (%s; %s)", version, runtime.GOOS, runtime.GOARCH))
			connectedAt := time.Now()
			conn, resp, dialErr := websocket.DefaultDialer.Dial(streamURL, headers)
			if dialErr == nil {
				host := "unknown"
				if parsed, parseErr := url.Parse(streamURL); parseErr == nil && parsed.Host != "" {
					host = parsed.Host
				}
				t.logStreamConnected(host)
				t.streamConnMu.Lock()
				t.streamConn = conn
				t.streamEpoch++
				epoch := t.streamEpoch
				t.streamConnMu.Unlock()
				t.signalStreamWake()
				go t.reportAgentDirectoryAndState(epoch)
				heartbeatStop := make(chan struct{})
				go t.runStreamHeartbeat(conn, heartbeatStop)
				for {
					var frame cicyCloudServerFrame
					if readErr := conn.ReadJSON(&frame); readErr != nil {
						t.logStreamFailure("read", 0, readErr)
						close(heartbeatStop)
						_ = conn.Close()
						t.streamConnMu.Lock()
						if t.streamConn == conn {
							t.streamConn = nil
						}
						for id, waiter := range t.streamWaiters {
							delete(t.streamWaiters, id)
							close(waiter)
						}
						t.streamConnMu.Unlock()
						break
					}
					t.handleStreamFrame(frame)
				}
				if time.Since(connectedAt) >= cicyCloudStreamMaxBackoff {
					backoff = 0
				}
			} else {
				status := 0
				if resp != nil {
					status = resp.StatusCode
					if resp.Body != nil {
						_ = resp.Body.Close()
					}
				}
				t.logStreamFailure("dial", status, dialErr)
			}
		}

		backoff = nextCiCyCloudStreamBackoff(backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-t.streamStop:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
}

func (t *cicyCloudTransport) runStreamHeartbeat(conn *websocket.Conn, stop <-chan struct{}) {
	ticker := time.NewTicker(cicyCloudHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.streamWriteMu.Lock()
			_ = conn.WriteJSON(M{"type": "heartbeat"})
			t.streamWriteMu.Unlock()
		case <-stop:
			return
		case <-t.streamStop:
			return
		}
	}
}

func (t *cicyCloudTransport) handleStreamFrame(frame cicyCloudServerFrame) {
	if frame.Type == "message" && frame.Message.ID != "" {
		log.Printf("[im-cloud] deliver.received transport=ws account=%d id=%s kind=%s src_hash=%s target_agent=%s cursor=%d",
			t.accountID, frame.Message.ID, frame.Message.Kind, cicyCloudTraceID(frame.Message.SenderInstanceID), frame.Message.TargetAgentID, frame.Cursor)
		t.streamConnMu.Lock()
		if frame.Cursor > t.streamCursor {
			t.streamCursor = frame.Cursor
		}
		t.streamConnMu.Unlock()
		select {
		case t.streamInbox <- frame.Message:
		case <-t.streamStop:
		}
		return
	}
	if frame.RequestID != "" {
		t.streamConnMu.Lock()
		waiter := t.streamWaiters[frame.RequestID]
		if waiter != nil {
			delete(t.streamWaiters, frame.RequestID)
		}
		t.streamConnMu.Unlock()
		if waiter != nil {
			select {
			case waiter <- frame:
			default:
			}
		}
	}
}

func (t *cicyCloudTransport) writeStream(frame M) error {
	t.initStream()
	t.streamConnMu.Lock()
	conn := t.streamConn
	t.streamConnMu.Unlock()
	if conn == nil {
		return fmt.Errorf("cloud websocket disconnected")
	}
	t.streamWriteMu.Lock()
	err := conn.WriteJSON(frame)
	t.streamWriteMu.Unlock()
	return err
}

func (t *cicyCloudTransport) requestStream(frame M) (cicyCloudServerFrame, error) {
	return t.requestStreamAtEpoch(frame, 0)
}

func (t *cicyCloudTransport) requestStreamAtEpoch(frame M, expectedEpoch uint64) (cicyCloudServerFrame, error) {
	t.initStream()
	if t.streamWaiters == nil {
		return cicyCloudServerFrame{}, fmt.Errorf("cloud websocket disconnected")
	}
	requestID, err := randomCodeInstanceID()
	if err != nil {
		return cicyCloudServerFrame{}, err
	}
	frame["requestId"] = requestID
	waiter := make(chan cicyCloudServerFrame, 1)
	t.streamConnMu.Lock()
	if t.streamConn == nil || expectedEpoch != 0 && t.streamEpoch != expectedEpoch {
		t.streamConnMu.Unlock()
		return cicyCloudServerFrame{}, fmt.Errorf("cloud websocket changed")
	}
	conn := t.streamConn
	t.streamWaiters[requestID] = waiter
	t.streamConnMu.Unlock()
	t.streamWriteMu.Lock()
	err = conn.WriteJSON(frame)
	t.streamWriteMu.Unlock()
	if err != nil {
		t.streamConnMu.Lock()
		delete(t.streamWaiters, requestID)
		t.streamConnMu.Unlock()
		return cicyCloudServerFrame{}, err
	}
	select {
	case response, ok := <-waiter:
		if !ok {
			return cicyCloudServerFrame{}, fmt.Errorf("cloud websocket disconnected")
		}
		if response.Type == "error" {
			return response, fmt.Errorf("cloud websocket: %s", response.Error)
		}
		return response, nil
	case <-time.After(10 * time.Second):
		t.streamConnMu.Lock()
		delete(t.streamWaiters, requestID)
		t.streamConnMu.Unlock()
		return cicyCloudServerFrame{}, fmt.Errorf("cloud websocket timeout")
	}
}

func (t *cicyCloudTransport) Close() error {
	t.streamClose.Do(func() {
		t.initStream()
		close(t.streamStop)
		t.streamConnMu.Lock()
		if t.streamConn != nil {
			_ = t.streamConn.Close()
			t.streamConn = nil
		}
		t.streamConnMu.Unlock()
	})
	return nil
}

func (t *cicyCloudTransport) nextIdlePollDelay() time.Duration {
	t.pollMu.Lock()
	defer t.pollMu.Unlock()
	if t.idlePollDelay <= 0 {
		t.idlePollDelay = cicyCloudPollMinDelay
	} else {
		t.idlePollDelay *= 2
		if t.idlePollDelay > cicyCloudPollMaxDelay {
			t.idlePollDelay = cicyCloudPollMaxDelay
		}
	}
	return t.idlePollDelay
}

func (t *cicyCloudTransport) resetIdlePollDelay() {
	t.pollMu.Lock()
	t.idlePollDelay = 0
	t.pollMu.Unlock()
}

type cicyCodeTelemetry struct {
	Platform      string `json:"platform"`
	Arch          string `json:"arch"`
	Runtime       string `json:"runtime"`
	CPUModel      string `json:"cpuModel"`
	CPUCores      int    `json:"cpuCores"`
	MemoryTotalMB int64  `json:"memoryTotalMB"`
	GPU           string `json:"gpu"`
}

var cicyCodeTelemetryOnce sync.Once
var cicyCodeTelemetryValue cicyCodeTelemetry

func collectCiCyCodeTelemetry() cicyCodeTelemetry {
	cicyCodeTelemetryOnce.Do(func() {
		t := cicyCodeTelemetry{Platform: runtime.GOOS, Arch: runtime.GOARCH,
			Runtime: "native", CPUCores: runtime.NumCPU()}
		if os.Getenv("COLAB_RELEASE_TAG") != "" || os.Getenv("COLAB_GPU") != "" {
			t.Runtime = "colab"
		} else if info, err := os.Stat("/content"); err == nil && info.IsDir() {
			t.Runtime = "colab"
		}
		if raw, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if p := strings.SplitN(line, ":", 2); len(p) == 2 && strings.TrimSpace(p[0]) == "model name" {
					t.CPUModel = strings.TrimSpace(p[1])
					break
				}
			}
		}
		if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
					kb, _ := strconv.ParseInt(f[1], 10, 64)
					t.MemoryTotalMB = kb / 1024
					break
				}
			}
		}
		if runtime.GOOS == "darwin" {
			if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
				t.CPUModel = strings.TrimSpace(string(out))
			}
			if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
				b, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
				t.MemoryTotalMB = b / 1024 / 1024
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader").Output(); err == nil {
			t.GPU = strings.TrimSpace(string(out))
		}
		cicyCodeTelemetryValue = t
	})
	return cicyCodeTelemetryValue
}

type cicyCloudAgentConfig struct {
	AgentID      string  `json:"agentId"`
	Title        *string `json:"title"`
	Guidance     *string `json:"guidance"`
	SystemPrompt *string `json:"systemPrompt"`
	Meta         *string `json:"meta"`
	Version      int64   `json:"version"`
}

func (t *cicyCloudTransport) syncAgentConfigs() {
	var out struct {
		Configs []cicyCloudAgentConfig `json:"configs"`
	}
	if err := cloudJSON(http.MethodGet, "/api/code/agent-configs", t.token, nil, &out); err != nil {
		log.Printf("[im] cicy cloud config poll failed: %v", err)
		return
	}
	results := make([]M, 0, len(out.Configs))
	for _, cfg := range out.Configs {
		errText := ""
		paneID := normPaneID(cfg.AgentID)
		var workspace string
		if err := store.QueryRow("SELECT COALESCE(workspace,'') FROM agent_config WHERE pane_id=? AND active=1", paneID).Scan(&workspace); err != nil || strings.TrimSpace(workspace) == "" {
			errText = "agent_not_found"
		} else {
			if cfg.Title != nil {
				if title := strings.TrimSpace(*cfg.Title); title == "" {
					errText = "title_required"
				} else {
					_, err := store.Exec(fmt.Sprintf("UPDATE agent_config SET title=?,updated_at=%s WHERE pane_id=?", store.Now()), title, paneID)
					if err != nil {
						errText = "rename_failed"
					}
				}
			}
			if errText == "" && cfg.Guidance != nil {
				path, _, ok := agentGuidancePath(paneID)
				if !ok {
					errText = "guidance_not_available"
				} else if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					errText = "guidance_mkdir_failed"
				} else if err := os.WriteFile(path, []byte(*cfg.Guidance), 0644); err != nil {
					errText = "guidance_write_failed"
				}
			}
			if errText == "" && cfg.SystemPrompt != nil {
				if err := writeAgentRoleFile(paneID, "system.md", *cfg.SystemPrompt); err != nil {
					errText = "system_write_failed"
				}
			}
			if errText == "" && cfg.Meta != nil {
				if err := writeAgentRoleFile(paneID, "meta.yaml", *cfg.Meta); err != nil {
					errText = "meta_write_failed"
				}
			}
		}
		results = append(results, M{"agentId": shortPaneID(paneID), "version": cfg.Version, "error": errText})
	}
	if len(results) > 0 {
		if err := cloudJSON(http.MethodPost, "/api/code/agent-configs", t.token, M{"results": results}, nil); err != nil {
			log.Printf("[im] cicy cloud config ack failed: %v", err)
		}
	}
}

func newCiCyCloudTransport(acc *imAccount) (botTransport, error) {
	if strings.TrimSpace(acc.Secret) == "" {
		return nil, fmt.Errorf("CiCy Cloud token missing")
	}
	return &cicyCloudTransport{accountID: acc.ID, token: strings.TrimSpace(acc.Secret)}, nil
}

func (t *cicyCloudTransport) Kind() string                       { return imPlatformCiCyCloud }
func (t *cicyCloudTransport) CanEdit() bool                      { return false }
func (t *cicyCloudTransport) Edit(botPeer, string, string) error { return errBotEditUnsupported }
func (t *cicyCloudTransport) Typing(botPeer) error               { return nil }

func (t *cicyCloudTransport) Poll(cursor string) ([]botMsg, string, error) {
	t.initStream()
	t.reportAllAgents()
	t.streamConnMu.Lock()
	connected := t.streamConn != nil
	t.streamConnMu.Unlock()
	if connected {
		select {
		case item := <-t.streamInbox:
			return t.processCloudMessages([]cicyCloudStreamMessage{item}, "ws")
		case <-time.After(cicyCloudPollMaxDelay):
			// Compatibility compensation: old Worker/web clients still enqueue in
			// D1. Check it infrequently while the Go Hub stream is healthy.
		case <-t.streamStop:
			return nil, "", nil
		}
	}
	if hubOriginForToken(t.token) != "" {
		// Hub mode has no HTTP inbox: wait for the websocket to (re)connect.
		t.waitForNextPoll(t.nextIdlePollDelay())
		return nil, "", nil
	}
	route := "/api/code/messages/poll"
	if strings.TrimSpace(cursor) != "" {
		route += "?ack=" + strings.TrimSpace(cursor)
	}
	var out struct {
		Messages []cicyCloudStreamMessage `json:"messages"`
	}
	if err := cloudJSON(http.MethodGet, route, t.token, nil, &out); err != nil {
		// The outer IM loop adds a fixed 3s delay on errors. Apply the same
		// bounded adaptive delay used for an empty inbox as well, otherwise a
		// Cloud outage makes every instance retry every three seconds and can
		// exhaust the daily Worker quota before the service recovers.
		t.waitForNextPoll(t.nextIdlePollDelay())
		return nil, cursor, err
	}
	if len(out.Messages) == 0 {
		t.waitForNextPoll(t.nextIdlePollDelay())
		return nil, "", nil
	}
	t.resetIdlePollDelay()
	for _, item := range out.Messages {
		log.Printf("[im-cloud] deliver.received transport=http account=%d id=%s kind=%s src_hash=%s target_agent=%s",
			t.accountID, item.ID, item.Kind, cicyCloudTraceID(item.SenderInstanceID), item.TargetAgentID)
	}
	return t.processCloudMessages(out.Messages, "http")
}

func (t *cicyCloudTransport) processCloudMessages(items []cicyCloudStreamMessage, source string) ([]botMsg, string, error) {
	msgs := make([]botMsg, 0, len(items))
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.Kind == "rpc_request" {
			if err := t.handleRPCRequest(item.ID, item.SenderInstanceID, item.SenderAgentID, item.TargetAgentID, item.Text, source); err != nil {
				log.Printf("[im] cicy cloud rpc failed id=%s op_target=%s: %v", item.ID, item.TargetAgentID, err)
				continue
			}
			// RPC is handled synchronously without entering the Agent dispatcher,
			// so it must ACK itself. Returning only the legacy cursor leaves the
			// row pending in loops that use per-message ACK and causes one reply on
			// every poll.
			if err := t.Ack(item.ID); err != nil {
				log.Printf("[im] cicy cloud rpc ack failed id=%s: %v", item.ID, err)
				continue
			}
			continue
		}
		// Agent output is a terminal reply, not another user request. A reply is
		// acknowledged but must never be fed back into an Agent, otherwise two
		// connected instances automatically answer each other forever.
		if item.Kind == "agent_reply" || item.Kind == "rpc_reply" {
			t.recordCloudReply(item)
			if err := t.Ack(item.ID); err != nil {
				log.Printf("[im] cicy cloud terminal reply ack failed id=%s kind=%s: %v", item.ID, item.Kind, err)
			}
			continue
		}
		ids = append(ids, item.ID)
		peer := item.SenderInstanceID
		if item.SenderAgentID != "" {
			peer += "|" + item.SenderAgentID
		}
		msgs = append(msgs, botMsg{Text: item.Text, FromID: item.SenderInstanceID,
			TargetPaneID: item.TargetAgentID,
			AckID:        item.ID,
			Peer:         botPeer{ChatID: peer, ContextToken: item.TargetAgentID + "|" + item.ID + "|" + source}})
	}
	return msgs, strings.Join(ids, ","), nil
}

func (t *cicyCloudTransport) recordCloudSend(id, transport string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	t.messageMu.Lock()
	defer t.messageMu.Unlock()
	if t.sentMessages == nil {
		t.sentMessages = make(map[string]cicyCloudLocalMessageState)
	}
	state := t.sentMessages[id]
	state.Transport = transport
	state.SentAtMS = time.Now().UnixMilli()
	t.sentMessages[id] = state
	t.pruneCloudMessagesLocked()
}

func (t *cicyCloudTransport) recordCloudReply(reply cicyCloudStreamMessage) {
	replyTo := strings.TrimSpace(reply.ReplyTo)
	if replyTo == "" {
		return
	}
	t.messageMu.Lock()
	defer t.messageMu.Unlock()
	if t.sentMessages == nil {
		t.sentMessages = make(map[string]cicyCloudLocalMessageState)
	}
	state := t.sentMessages[replyTo]
	state.Reply = reply
	state.ReceivedMS = time.Now().UnixMilli()
	t.sentMessages[replyTo] = state
	t.pruneCloudMessagesLocked()

	// The Cloud stream is already a live subscription. Forward terminal
	// replies to connected UIs immediately instead of making every Projects
	// card discover the same reply through message-status/current_reply polls.
	// broadcastAll is intentionally used because Projects can show Agents from
	// several master buckets at once; replyTo lets each client correlate safely.
	hub.broadcastAll(ChatEvent{Type: "cicy_cloud_reply", Data: M{
		"id": reply.ID, "kind": reply.Kind, "replyTo": replyTo,
		"senderInstanceId": reply.SenderInstanceID,
		"senderAgentId":    reply.SenderAgentID,
		"targetAgentId":    reply.TargetAgentID,
		"text":             reply.Text, "enqueuedAtMs": reply.EnqueuedAtMS,
		"receivedAtMs": state.ReceivedMS,
	}})
}

func (t *cicyCloudTransport) cloudMessageState(id string) (cicyCloudLocalMessageState, bool) {
	t.messageMu.Lock()
	defer t.messageMu.Unlock()
	state, ok := t.sentMessages[strings.TrimSpace(id)]
	return state, ok
}

func (t *cicyCloudTransport) pruneCloudMessagesLocked() {
	if len(t.sentMessages) <= 256 {
		return
	}
	cutoff := time.Now().Add(-time.Hour).UnixMilli()
	for id, state := range t.sentMessages {
		if state.SentAtMS > 0 && state.SentAtMS < cutoff {
			delete(t.sentMessages, id)
		}
	}
}

func (t *cicyCloudTransport) handleRPCRequest(messageID, senderInstanceID, senderAgentID, targetAgentID, text, source string) error {
	var req struct {
		Op           string  `json:"op"`
		Full         bool    `json:"full"`
		Index        int     `json:"index"`
		From         string  `json:"from"`
		To           string  `json:"to"`
		Status       string  `json:"status"`
		Open         bool    `json:"open"`
		Title        *string `json:"title"`
		Guidance     *string `json:"guidance"`
		SystemPrompt *string `json:"systemPrompt"`
		Meta         *string `json:"meta"`
	}
	var result M
	var rpcErr error
	if err := json.Unmarshal([]byte(text), &req); err != nil {
		rpcErr = fmt.Errorf("invalid rpc request: %w", err)
	} else {
		target := shortPaneID(normPaneID(targetAgentID))
		switch strings.TrimSpace(req.Op) {
		case "reply":
			result, rpcErr = agentReplyTextData(target, req.Full)
		case "agent_status":
			result, rpcErr = cicyCloudAgentStatus(target)
		case "agent_roster":
			result, rpcErr = cicyCloudAgentRosterData()
		case "current_reply":
			current, currentErr := aiGatewayReadCurrentSnapshotCached(target)
			if currentErr != nil && !os.IsNotExist(currentErr) {
				rpcErr = currentErr
				break
			}
			reply := agentInspectorLoadReply(target)
			status := strings.ToLower(strings.TrimSpace(reply.Status))
			displayItems := aiGatewayFilterTechnicalTransportFailures(reply.Items)
			answer := strings.TrimSpace(reply.Answer)
			if answer == "" {
				answer = aiGatewayReplyItemsText(displayItems, "text", aiGatewayCommittedAssistantTexts(current))
			}
			thinking := strings.TrimSpace(reply.Thinking)
			if thinking == "" {
				thinking = aiGatewayReplyItemsText(displayItems, "thinking", aiGatewayCommittedAssistantThinking(current))
			}
			ctxUsedPct, ctxWindowSize := agentInspectorReadContextWindow(target)
			result, rpcErr = cicyCloudAgentStatus(target)
			if rpcErr != nil {
				break
			}
			replyModel := aiGatewayFirstNonEmpty(aiGatewayReplyPrimaryModel(reply), strings.TrimSpace(current.Model))
			for key, value := range (M{
				"status":   status,
				"complete": status == "" || status == "idle" || status == "done" || isAIGatewayReplyTerminal(status),
				"question": aiGatewayCurrentQuestion(current), "answer": answer, "thinking": thinking, "items": displayItems,
				"started_at":   aiGatewayFirstNonEmpty(strings.TrimSpace(reply.StartedAt), strings.TrimSpace(current.StartedAt), strings.TrimSpace(current.Timestamp)),
				"updated_at":   strings.TrimSpace(reply.UpdatedAt),
				"input_tokens": reply.InputTokens, "output_tokens": reply.OutputTokens, "total_tokens": reply.TotalTokens,
				"cost_credit": reply.CostCredit, "context_used_pct": ctxUsedPct, "context_window_size": ctxWindowSize,
			}) {
				result[key] = value
			}
			if replyModel != "" {
				result["model"] = replyModel
			}
		case "history":
			if req.Index < 0 {
				rpcErr = fmt.Errorf("index must be >= 0")
			} else {
				result, rpcErr = agentChatHistoryData(target, req.Index)
			}
		case "msgs":
			from, to := strings.TrimSpace(req.From), strings.TrimSpace(req.To)
			if from == "" && to == "" {
				to = target
			}
			result, rpcErr = agentMessagesData(agentMessageFilter{From: from, To: to, Status: req.Status, Open: req.Open})
		case "persona":
			result, rpcErr = agentPersonaData(target)
		case "persona_save":
			result, rpcErr = saveAgentPersonaData(target, req.Title, req.Guidance, req.SystemPrompt, req.Meta)
		case "cancel":
			result, rpcErr = cancelAgentTurnData(target)
		default:
			rpcErr = fmt.Errorf("unsupported rpc operation %q", req.Op)
		}
	}
	envelope := M{"ok": rpcErr == nil, "data": result}
	if rpcErr != nil {
		envelope["error"] = rpcErr.Error()
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	payload := M{
		"targetInstanceId": senderInstanceID, "targetAgentId": senderAgentID,
		"senderAgentId": targetAgentID, "text": string(body),
		"kind": "rpc_reply", "replyTo": messageID, "hopCount": 1,
	}
	if source == "http" {
		_, err = t.sendCloudMessageHTTP(payload)
	} else {
		_, err = t.sendCloudMessage(payload)
	}
	return err
}

func saveAgentPersonaData(paneID string, title, guidance, systemPrompt, meta *string) (M, error) {
	guidancePath, _, ok := agentGuidancePath(paneID)
	if !ok {
		return nil, fmt.Errorf("agent guidance file not available")
	}
	if title != nil {
		value := strings.TrimSpace(*title)
		if value == "" {
			return nil, fmt.Errorf("title required")
		}
		if _, err := store.Exec(fmt.Sprintf("UPDATE agent_config SET title=?,updated_at=%s WHERE pane_id=?", store.Now()), value, normPaneID(paneID)); err != nil {
			return nil, fmt.Errorf("rename agent: %w", err)
		}
	}
	if guidance != nil {
		if err := os.WriteFile(guidancePath, []byte(*guidance), 0644); err != nil {
			return nil, fmt.Errorf("write guidance: %w", err)
		}
	}
	if systemPrompt != nil {
		if err := writeAgentRoleFile(paneID, "system.md", *systemPrompt); err != nil {
			return nil, fmt.Errorf("write role system prompt: %w", err)
		}
	}
	if meta != nil {
		if err := writeAgentRoleFile(paneID, "meta.yaml", *meta); err != nil {
			return nil, fmt.Errorf("write role meta: %w", err)
		}
	}
	return agentPersonaData(paneID)
}

func agentPersonaData(paneID string) (M, error) {
	guidancePath, filename, ok := agentGuidancePath(paneID)
	if !ok {
		return nil, fmt.Errorf("agent guidance file not available")
	}
	guidance, _ := os.ReadFile(guidancePath)
	slug := employeeRoleSlug(shortPaneID(normPaneID(paneID)))
	return M{"filename": filename, "guidance": string(guidance), "systemPrompt": cicySystemBase(slug), "meta": readRoleFile(slug, "meta.yaml")}, nil
}

func writeAgentRoleFile(paneID, name, content string) error {
	slug := employeeRoleSlug(shortPaneID(normPaneID(paneID)))
	if slug == "" {
		return fmt.Errorf("agent role not available")
	}
	dir := filepath.Join(agentTemplatesDir(), slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
}

// Ack confirms one Cloud message only after imHandleInbound accepted it. The
// poll endpoint already supports explicit ack ids; any messages returned by
// this acknowledgement request remain unacked and are fetched by the next Poll.
func (t *cicyCloudTransport) Ack(messageID string) error {
	id := strings.TrimSpace(messageID)
	if id == "" {
		return nil
	}
	if response, err := t.requestStream(M{"type": "ack", "ids": []string{id}}); err == nil && response.Type == "acked" {
		log.Printf("[im-cloud] ack.sent transport=ws account=%d id=%s", t.accountID, id)
	}
	var out struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := cloudJSON(http.MethodGet, "/api/code/messages/poll?ack="+url.QueryEscape(id), t.token, nil, &out); err != nil {
		return err
	}
	log.Printf("[im-cloud] ack.sent transport=http account=%d id=%s", t.accountID, id)
	return nil
}

func (t *cicyCloudTransport) Send(peer botPeer, text string) (string, error) {
	targetInstance, targetAgent := splitCiCyCloudPeer(peer.ChatID)
	senderAgent, replyTo, source := splitCiCyCloudReplyContext(peer.ContextToken)
	payload := M{
		"targetInstanceId": targetInstance, "targetAgentId": targetAgent,
		"senderAgentId": senderAgent, "text": text,
		"kind": "agent_reply", "replyTo": replyTo, "hopCount": 1,
	}
	var id string
	var err error
	if source == "http" {
		id, err = t.sendCloudMessageHTTP(payload)
	} else {
		id, err = t.sendCloudMessage(payload)
	}
	if err == nil && strings.TrimSpace(replyTo) != "" {
		markCiCyCloudInboxReplied(replyTo)
	}
	return id, err
}

func (t *cicyCloudTransport) sendCloudMessage(payload M) (string, error) {
	id, _, err := t.sendCloudMessageWithTransport(payload)
	return id, err
}

func (t *cicyCloudTransport) sendCloudMessageWithTransport(payload M) (string, string, error) {
	frame := M{"type": "send"}
	for key, value := range payload {
		frame[key] = value
	}
	if response, err := t.requestStream(frame); err == nil && response.Type == "sent" {
		log.Printf("[im-cloud] send.stored transport=ws account=%d id=%s kind=%v dst_hash=%s target_agent=%v cursor=%d",
			t.accountID, response.ID, payload["kind"], cicyCloudTraceID(fmt.Sprint(payload["targetInstanceId"])), payload["targetAgentId"], response.Cursor)
		t.recordCloudSend(response.ID, "ws")
		return response.ID, "ws", nil
	} else if err != nil {
		log.Printf("[im-cloud] send.fallback transport=ws account=%d kind=%v dst_hash=%s target_agent=%v error_code=stream_unavailable",
			t.accountID, payload["kind"], cicyCloudTraceID(fmt.Sprint(payload["targetInstanceId"])), payload["targetAgentId"])
	} else {
		log.Printf("[im-cloud] send.fallback transport=ws account=%d kind=%v dst_hash=%s target_agent=%v error_code=unexpected_response",
			t.accountID, payload["kind"], cicyCloudTraceID(fmt.Sprint(payload["targetInstanceId"])), payload["targetAgentId"])
	}
	id, err := t.sendCloudMessageHTTP(payload)
	if err != nil {
		return "", "http", err
	}
	t.recordCloudSend(id, "http")
	return id, "http", nil
}

func (t *cicyCloudTransport) sendCloudMessageHTTP(payload M) (string, error) {
	var out struct {
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	if err := cloudJSON(http.MethodPost, "/api/code/messages", t.token, payload, &out); err != nil {
		return "", err
	}
	log.Printf("[im-cloud] send.stored transport=http account=%d id=%s kind=%v dst_hash=%s target_agent=%v",
		t.accountID, out.Message.ID, payload["kind"], cicyCloudTraceID(fmt.Sprint(payload["targetInstanceId"])), payload["targetAgentId"])
	return out.Message.ID, nil
}

func splitCiCyCloudReplyContext(contextToken string) (string, string, string) {
	parts := strings.SplitN(strings.TrimSpace(contextToken), "|", 3)
	if len(parts) >= 2 {
		source := ""
		if len(parts) == 3 {
			source = strings.TrimSpace(parts[2])
		}
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), source
	}
	return strings.TrimSpace(contextToken), "", ""
}

func splitCiCyCloudPeer(peer string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(peer), "|", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}

func cicyCloudPreview(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	suffix := "…"
	if limit < len(suffix) {
		return ""
	}
	cut := limit - len(suffix)
	for cut > 0 && value[cut]&0xc0 == 0x80 {
		cut--
	}
	return strings.TrimSpace(value[:cut]) + suffix
}

func cicyCloudFiniteNumber(value interface{}) (float64, bool) {
	var number float64
	switch value := value.(type) {
	case int:
		number = float64(value)
	case int8:
		number = float64(value)
	case int16:
		number = float64(value)
	case int32:
		number = float64(value)
	case int64:
		number = float64(value)
	case uint:
		number = float64(value)
	case uint8:
		number = float64(value)
	case uint16:
		number = float64(value)
	case uint32:
		number = float64(value)
	case uint64:
		number = float64(value)
	case float32:
		number = float64(value)
	case float64:
		number = value
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func cicyCloudAgentRuntimeState(agentID, defaultModel string, metrics M) M {
	state := M{"agentId": agentID, "status": "", "model": interface{}(nil), "online": false}
	if strings.TrimSpace(defaultModel) != "" {
		state["model"] = strings.TrimSpace(defaultModel)
	}
	if metrics == nil {
		return state
	}
	if value := strings.TrimSpace(aiGatewayString(metrics["status"])); value != "" {
		state["status"] = value
	}
	if value := strings.TrimSpace(aiGatewayString(metrics["model"])); value != "" {
		state["model"] = value
	}
	if value, ok := cicyCloudFiniteNumber(metrics["context_used_pct"]); ok {
		state["contextUsedPct"] = int(max(0, min(100, value)))
	}
	if value, ok := cicyCloudFiniteNumber(metrics["context_window_size"]); ok && value > 0 {
		state["contextWindowSize"] = int(value)
	}
	// cost_credit is the cumulative USD cost reported by the local inspector.
	if value, ok := cicyCloudFiniteNumber(metrics["cost_credit"]); ok && value >= 0 {
		state["cost"] = value
	}
	state["updatedAt"] = strings.TrimSpace(aiGatewayString(metrics["updated_at"]))
	if complete, ok := metrics["complete"].(bool); ok {
		state["working"] = !complete
	}
	// Project cards only need a compact live preview. Keep the authoritative
	// reply.json payload local and publish bounded excerpts so a full 500-agent
	// state snapshot stays below the Hub's 512 KiB frame limit.
	if value := cicyCloudPreview(aiGatewayString(metrics["latest_question"]), 256); value != "" {
		state["latestQuestion"] = value
	}
	if value := cicyCloudPreview(aiGatewayString(metrics["latest_response"]), 256); value != "" {
		state["latestResponse"] = value
		state["latestResponseType"] = truncateRunes(aiGatewayString(metrics["latest_response_type"]), 24)
		state["latestResponseAt"] = strings.TrimSpace(aiGatewayString(metrics["updated_at"]))
	}
	if tool, ok := metrics["latest_tool"].(M); ok {
		if name := cicyCloudPreview(aiGatewayString(tool["name"]), 64); name != "" {
			state["latestTool"] = M{"name": name, "input": cicyCloudPreview(aiGatewayString(tool["input"]), 96)}
		}
	}
	return state
}

func cicyCloudAgentForeground(command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "" {
		return false
	}
	switch command {
	case "bash", "zsh", "sh", "fish", "dash", "ksh", "tcsh":
		return false
	default:
		return true
	}
}

func cicyCloudAgentIdle(online bool, metrics M) interface{} {
	if !online || metrics == nil {
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(aiGatewayString(metrics["status"])))
	switch status {
	case "thinking", "working", "running", "streaming", "pending", "tool_use", "tool_call", "in_progress":
		return false
	case "idle", "done", "completed", "complete", "cancelled", "canceled", "error", "failed":
		return true
	}
	if complete, ok := metrics["complete"].(bool); ok {
		return complete
	}
	return nil
}

// cicyCloudAgentRosterState is the authoritative Cloud roster contract used by
// both agent_status and current_reply. Nullable runtime fields are always
// present, and numeric zero is deliberately preserved.
func cicyCloudAgentRosterState(paneID, title, agentType, defaultModel, workspace string, useCustomGateway bool, metrics M) M {
	agentID := shortPaneID(paneID)
	online := false
	if normalizeAgentType(agentType) == "cicy" {
		online = cicySessionRegistered(agentID)
	} else {
		online = cicyCloudAgentForeground(paneCurrentCommand(normPaneID(paneID)))
	}
	provider, usageModel, usageCost := agentUsageRuntimeSummary(agentID)
	var model interface{}
	if value := strings.TrimSpace(aiGatewayString(metrics["model"])); value != "" {
		model = value
	} else if usageModel != nil {
		model = *usageModel
	} else if value := strings.TrimSpace(defaultModel); value != "" {
		model = value
	}
	var providerValue interface{}
	if provider != nil {
		providerValue = *provider
	}
	var cost interface{}
	if usageCost != nil {
		cost = *usageCost
	}
	var contextUsage interface{}
	if value, ok := cicyCloudFiniteNumber(metrics["context_used_pct"]); ok {
		contextUsage = fmt.Sprintf("%g%%", max(0, min(100, value)))
	}
	return M{
		"id": agentID, "title": title, "agent_type": normalizeAgentType(agentType), "online": online,
		"model": model, "provider": providerValue, "local_gateway": useCustomGateway,
		"context_usage": contextUsage, "cost": cost, "idle": cicyCloudAgentIdle(online, metrics),
		"pane_id": normPaneID(paneID), "workspace": workspace,
		// Compatibility aliases for existing Hub/UI consumers.
		"agentId": agentID, "agentType": normalizeAgentType(agentType), "localGateway": useCustomGateway,
		"useCustomGateway": useCustomGateway, "contextUsage": contextUsage, "paneId": normPaneID(paneID),
	}
}

func cicyCloudAgentStatus(paneID string) (M, error) {
	if store == nil {
		return nil, fmt.Errorf("agent store unavailable")
	}
	var fullPaneID, title, agentType, defaultModel, workspace string
	var useCustomGateway bool
	err := store.QueryRow(`SELECT pane_id,COALESCE(title,''),COALESCE(agent_type,''),
		COALESCE(default_model,''),COALESCE(workspace,''),COALESCE(use_custom_gateway,0)
		FROM agent_config WHERE pane_id=? AND active=1`, normPaneID(paneID)).
		Scan(&fullPaneID, &title, &agentType, &defaultModel, &workspace, &useCustomGateway)
	if err != nil {
		return nil, err
	}
	return cicyCloudAgentRosterState(fullPaneID, title, agentType, defaultModel, workspace,
		useCustomGateway, agentInspectorLiteMetrics(shortPaneID(fullPaneID))), nil
}

func cicyCloudAgentRosterEnvelope(agents []M) M {
	online := 0
	for _, agent := range agents {
		if value, ok := agent["online"].(bool); ok && value {
			online++
		}
	}
	return M{
		"kind": "all", "count": len(agents), "online": online,
		"offline": len(agents) - online, "all": len(agents), "agents": agents,
	}
}

func cicyCloudAgentRosterData() (M, error) {
	_, agents, err := collectCiCyCloudAgents()
	if err != nil {
		return nil, err
	}
	return cicyCloudAgentRosterEnvelope(agents), nil
}

func collectCiCyCloudAgents() ([]M, []M, error) {
	if store == nil {
		return nil, nil, fmt.Errorf("agent store unavailable")
	}
	rows, err := store.Query(`SELECT pane_id,COALESCE(title,''),COALESCE(agent_type,''),COALESCE(role,''),
		COALESCE(default_model,''),COALESCE(workspace,''),COALESCE(use_custom_gateway,0)
		FROM agent_config WHERE active=1 ORDER BY created_at,pane_id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	directory, states := []M{}, []M{}
	for rows.Next() {
		var paneID, title, agentType, role, defaultModel, workspace string
		var useCustomGateway bool
		if err := rows.Scan(&paneID, &title, &agentType, &role, &defaultModel, &workspace, &useCustomGateway); err != nil {
			return nil, nil, err
		}
		if isBuiltinAgent(paneID) {
			continue
		}
		agentID := shortPaneID(paneID)
		directory = append(directory, M{"agentId": agentID, "title": title,
			"agentType": agentType, "role": role, "useCustomGateway": useCustomGateway})
		states = append(states, cicyCloudAgentRosterState(paneID, title, agentType, defaultModel, workspace,
			useCustomGateway, agentInspectorLiteMetrics(agentID)))
	}
	return directory, states, rows.Err()
}

func (t *cicyCloudTransport) currentStreamEpoch() uint64 {
	t.streamConnMu.Lock()
	defer t.streamConnMu.Unlock()
	return t.streamEpoch
}

func (t *cicyCloudTransport) publishAgentState(states []M, epoch uint64) error {
	response, err := t.requestStreamAtEpoch(M{"type": "agent_state_publish", "fullSnapshot": true, "agents": states}, epoch)
	if err != nil {
		return err
	}
	if response.Type != "agent_state_published" || response.Revision == 0 {
		return fmt.Errorf("unexpected agent state response")
	}
	log.Printf("[im-cloud] agent_state.published transport=ws account=%d revision=%d agents=%d",
		t.accountID, response.Revision, len(states))
	return nil
}

func (t *cicyCloudTransport) reportAgentDirectoryAndState(epoch uint64) {
	t.stateMu.Lock()
	directory, states, err := collectCiCyCloudAgents()
	if err != nil {
		t.stateMu.Unlock()
		log.Printf("[im] cicy cloud agent snapshot failed: %v", err)
		return
	}
	if epoch == 0 {
		log.Printf("[im-cloud] agent_state.publish skipped transport=ws account=%d reason=disconnected", t.accountID)
	} else if err := t.publishAgentState(states, epoch); err != nil {
		log.Printf("[im-cloud] agent_state.publish failed transport=ws account=%d error_type=%T", t.accountID, err)
	}
	t.stateMu.Unlock()
	// This HTTP call synchronizes directory/config metadata only. Runtime state
	// is published exclusively over the already-authenticated WebSocket above.
	if err := cloudJSON(http.MethodPost, "/api/code/agents", t.token, M{"agents": directory}, nil); err != nil {
		log.Printf("[im] cicy cloud agent directory failed: %v", err)
	}
}

// reportAllAgents keeps instance liveness and static Agent directory metadata
// fresh. Agent runtime state itself is never sent over HTTP; it uses the Hub WS.
func (t *cicyCloudTransport) reportAllAgents() {
	t.presenceMu.Lock()
	defer t.presenceMu.Unlock()
	now := time.Now()
	heartbeatDue := now.Sub(t.lastHeartbeat) >= cicyCloudHeartbeatInterval
	presenceDue := now.Sub(t.lastPresence) >= cicyCloudPresenceInterval
	if !heartbeatDue && !presenceDue {
		return
	}
	if heartbeatDue {
		// Record the attempt before I/O. A failing Cloud endpoint must not turn
		// the heartbeat path itself into a quota-exhausting retry loop.
		t.lastHeartbeat = now
		t.reportHeartbeat(cftCurrentURL())
	}
	if !presenceDue {
		return
	}
	t.lastPresence = now
	t.syncAgentConfigs()
	t.reportAgentDirectoryAndState(t.currentStreamEpoch())
}

func (t *cicyCloudTransport) reportHeartbeat(tunnelURL string) {
	telemetry := collectCiCyCodeTelemetry()
	heartbeat := M{"platform": telemetry.Platform, "arch": telemetry.Arch,
		"runtime": telemetry.Runtime, "cpuModel": telemetry.CPUModel,
		"cpuCores": telemetry.CPUCores, "memoryTotalMB": telemetry.MemoryTotalMB,
		"gpu": telemetry.GPU}
	heartbeat["ports"] = portMaps(pruneOfflinePublishedPorts())
	if tunnelURL = strings.TrimSpace(tunnelURL); tunnelURL != "" {
		heartbeat["tunnelUrl"] = tunnelURL
		heartbeat["tunnelToken"] = loadAPIToken()
	}
	if err := cloudJSON(http.MethodPost, "/api/code/instances/heartbeat", t.token, heartbeat, nil); err != nil {
		log.Printf("[im] cicy cloud heartbeat failed: %v", err)
	}
}

func (t *cicyCloudTransport) reportTunnelReady(tunnelURL string) {
	t.presenceMu.Lock()
	defer t.presenceMu.Unlock()
	t.lastHeartbeat = time.Now()
	t.reportHeartbeat(tunnelURL)
}

// reportCiCyCloudTunnelReady publishes the assigned URL immediately. Quick
// tunnels become ready asynchronously, often just after the normal startup
// heartbeat, so waiting for the next interval leaves Cloud with a stale URL.
func reportCiCyCloudTunnelReady(tunnelURL string) {
	if strings.TrimSpace(tunnelURL) == "" {
		return
	}
	accounts, err := imListAccounts()
	if err != nil {
		return
	}
	for _, account := range accounts {
		if !account.Enabled || account.Platform != imPlatformCiCyCloud || strings.TrimSpace(account.Secret) == "" {
			continue
		}
		if transport, ok := imTransportFor(account.ID).(*cicyCloudTransport); ok {
			go transport.reportTunnelReady(tunnelURL)
		}
	}
}

// reportAgentState publishes a complete current snapshot on every local state
// transition. Full snapshots make deletion and reconnect semantics unambiguous.
func (t *cicyCloudTransport) reportAgentState(_ string) {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	_, states, err := collectCiCyCloudAgents()
	if err != nil {
		log.Printf("[im] cicy cloud agent snapshot failed: %v", err)
		return
	}
	if err := t.publishAgentState(states, t.currentStreamEpoch()); err != nil {
		log.Printf("[im-cloud] agent_state.publish failed transport=ws account=%d error_type=%T", t.accountID, err)
	}
}

func reportCiCyCloudAgentState(paneID string) {
	accounts, err := imListAccounts()
	if err != nil {
		return
	}
	for _, account := range accounts {
		if !account.Enabled || account.Platform != imPlatformCiCyCloud || strings.TrimSpace(account.Secret) == "" {
			continue
		}
		if transport, ok := imTransportFor(account.ID).(*cicyCloudTransport); ok {
			go transport.reportAgentState(paneID)
		}
	}
}

// reportCiCyCloudAgentRosterNow pushes static directory metadata and a full WS
// state snapshot immediately, including after deletion.
func reportCiCyCloudAgentRosterNow() {
	accounts, err := imListAccounts()
	if err != nil {
		return
	}
	for _, account := range accounts {
		if !account.Enabled || account.Platform != imPlatformCiCyCloud || strings.TrimSpace(account.Secret) == "" {
			continue
		}
		if transport, ok := imTransportFor(account.ID).(*cicyCloudTransport); ok {
			go transport.reportAgentDirectoryAndState(transport.currentStreamEpoch())
		}
	}
}

func handleCiCyCloudLoginRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) >= 2 && parts[1] == "tunnel" && r.Method == http.MethodPost {
		if err := enableCFT(resolvePort(), true); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true, "status": "starting", "url": cftCurrentURL()})
		return
	}
	if len(parts) >= 2 && (parts[1] == "instances" || parts[1] == "agents") && r.Method == http.MethodGet {
		var token string
		_ = store.QueryRow("SELECT secret FROM im_accounts WHERE platform=? ORDER BY id LIMIT 1", imPlatformCiCyCloud).Scan(&token)
		if strings.TrimSpace(token) == "" {
			httpErr(w, 401, "CiCy Cloud account not connected")
			return
		}
		var out any
		route := "/api/code/instances"
		if parts[1] == "agents" {
			route = "/api/code/agents"
		}
		if err := cloudJSON(http.MethodGet, route, token, nil, &out); err != nil {
			httpErr(w, 502, err.Error())
			return
		}
		J(w, out)
		return
	}
	// POST /api/im/cicy-cloud/open {instance_id?, port?} → one-time URL that
	// opens the instance's hub hostname already signed in (hub mode only).
	if len(parts) >= 2 && parts[1] == "open" && r.Method == http.MethodPost {
		var in struct {
			InstanceID string `json:"instance_id"`
			Port       int    `json:"port"`
			Next       string `json:"next"`
		}
		_ = readBody(r, &in)
		var token string
		_ = store.QueryRow("SELECT secret FROM im_accounts WHERE platform=? ORDER BY id LIMIT 1", imPlatformCiCyCloud).Scan(&token)
		if strings.TrimSpace(token) == "" {
			httpErr(w, 401, "CiCy account not connected")
			return
		}
		if hubOriginForToken(token) == "" {
			httpErr(w, 400, "instance domains are issued by CiCy Hub; sign in with Hub first")
			return
		}
		var out struct {
			URL  string `json:"url"`
			Host string `json:"host"`
		}
		if err := cloudJSON(http.MethodPost, "/api/code/gateway-grant", token, M{"instanceId": strings.TrimSpace(in.InstanceID), "port": in.Port, "next": in.Next}, &out); err != nil {
			httpErr(w, 502, err.Error())
			return
		}
		J(w, M{"success": true, "url": out.URL, "host": out.Host})
		return
	}
	if len(parts) >= 2 && parts[1] == "status" && r.Method == http.MethodGet {
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if !cicyCloudMessageRE.MatchString(id) {
			httpErr(w, 400, "invalid message id")
			return
		}
		var accountID int64
		_ = store.QueryRow("SELECT id FROM im_accounts WHERE platform=? ORDER BY id LIMIT 1", imPlatformCiCyCloud).Scan(&accountID)
		transport, ok := imTransportFor(accountID).(*cicyCloudTransport)
		if !ok {
			httpErr(w, 404, "cloud transport unavailable")
			return
		}
		state, ok := transport.cloudMessageState(id)
		if !ok {
			httpErr(w, 404, "message not found")
			return
		}
		status := "pending"
		var reply any
		if state.Reply.ID != "" {
			status = "replied"
			reply = M{"id": state.Reply.ID, "senderInstanceId": state.Reply.SenderInstanceID,
				"senderAgentId": state.Reply.SenderAgentID, "text": state.Reply.Text,
				"replyTo": state.Reply.ReplyTo, "enqueuedAtMs": state.Reply.EnqueuedAtMS,
				"receivedAtMs": state.ReceivedMS}
		}
		J(w, M{"success": true, "status": status, "transport": state.Transport,
			"message": M{"id": id, "sentAtMs": state.SentAtMS}, "reply": reply})
		return
	}
	if len(parts) >= 2 && parts[1] == "send" && r.Method == http.MethodPost {
		var in struct {
			TargetInstanceID string `json:"target_instance_id"`
			TargetAgentID    string `json:"target_agent_id"`
			SenderAgentID    string `json:"sender_agent_id"`
			Text             string `json:"text"`
			Kind             string `json:"kind"`
		}
		if readBody(r, &in) != nil || strings.TrimSpace(in.TargetInstanceID) == "" || strings.TrimSpace(in.Text) == "" {
			httpErr(w, 400, "target_instance_id and text required")
			return
		}
		var accountID int64
		var token string
		_ = store.QueryRow("SELECT id,secret FROM im_accounts WHERE platform=? ORDER BY id LIMIT 1", imPlatformCiCyCloud).Scan(&accountID, &token)
		payload := M{
			"targetInstanceId": strings.TrimSpace(in.TargetInstanceID),
			"targetAgentId":    strings.TrimSpace(in.TargetAgentID),
			"senderAgentId":    strings.TrimSpace(in.SenderAgentID), "text": strings.TrimSpace(in.Text),
			"kind": strings.TrimSpace(in.Kind),
		}
		if payload["kind"] == "" {
			payload["kind"] = "user_message"
		}
		var messageID string
		var sendErr error
		messageTransport := "http"
		if transport, ok := imTransportFor(accountID).(*cicyCloudTransport); ok {
			messageID, messageTransport, sendErr = transport.sendCloudMessageWithTransport(payload)
		} else {
			var out struct {
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
			}
			sendErr = cloudJSON(http.MethodPost, "/api/code/messages", token, payload, &out)
			messageID = out.Message.ID
		}
		if sendErr != nil {
			httpErr(w, 502, sendErr.Error())
			return
		}
		J(w, M{"success": true, "transport": messageTransport, "message": M{"id": messageID}})
		return
	}
	// POST /api/im/cicy-cloud/login; GET /api/im/cicy-cloud/login/{state};
	// POST /api/im/cicy-cloud/login/{state}/code {code} (hub logins only)
	if len(parts) < 2 || parts[1] != "login" {
		httpErr(w, 404, "not found")
		return
	}
	if r.Method == http.MethodPost && len(parts) == 4 && parts[3] == "code" {
		state := strings.TrimSpace(parts[2])
		pendingValue, hasPending := cicyCloudPendingLogins.Load(state)
		pending, _ := pendingValue.(cicyCloudPendingLogin)
		if !hasPending || pending.HubOrigin == "" {
			httpErr(w, 404, "no pending hub login")
			return
		}
		var in struct {
			Code string `json:"code"`
		}
		if readBody(r, &in) != nil || len(strings.TrimSpace(in.Code)) != 6 {
			httpErr(w, 400, "6-digit code required")
			return
		}
		hubStateValue, _ := hubLoginStates.Load(state)
		hubState, _ := hubStateValue.(string)
		if err := cloudJSONAt(pending.HubOrigin, http.MethodPost, "/api/login/code", "", M{"state": hubState, "code": strings.TrimSpace(in.Code)}, nil); err != nil {
			httpErr(w, 401, err.Error())
			return
		}
		J(w, M{"status": "approved"})
		return
	}
	if r.Method == http.MethodPost && len(parts) == 2 {
		var in struct {
			Email     string `json:"email"`
			Team      string `json:"team"`
			HubOrigin string `json:"hub_origin"`
			Hub       bool   `json:"hub"`
		}
		if readBody(r, &in) != nil {
			httpErr(w, 400, "invalid request body")
			return
		}
		email := strings.ToLower(strings.TrimSpace(in.Email))
		team := strings.TrimSpace(in.Team)
		if !cicyCloudEmailRE.MatchString(email) {
			httpErr(w, 400, "invalid email")
			return
		}
		if team == "" || !cicyCloudTeamRE.MatchString(team) {
			httpErr(w, 400, "invalid team")
			return
		}
		state, err := randomLoginState()
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		telemetry := collectCiCyCodeTelemetry()
		hostname, _ := os.Hostname()
		var existing cicyCloudCredential
		if data, readErr := os.ReadFile(cicyCloudCredentialPath()); readErr == nil {
			_ = json.Unmarshal(data, &existing)
		}
		if in.Hub || strings.TrimSpace(in.HubOrigin) != "" {
			hub := strings.TrimRight(strings.TrimSpace(in.HubOrigin), "/")
			if hub == "" {
				hub = cicyHubOrigin()
			}
			if u, parseErr := url.Parse(hub); parseErr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" {
				httpErr(w, 400, "invalid hub origin")
				return
			}
			instanceID := strings.TrimSpace(existing.InstanceID)
			if instanceID == "" {
				instanceID, _ = randomCodeInstanceID()
			}
			var started struct {
				State string `json:"state"`
			}
			if err := cloudJSONAt(hub, http.MethodPost, "/api/login/start", "", M{
				"email": email, "instanceId": instanceID, "name": team, "platform": runtime.GOOS + "/" + telemetry.Runtime,
			}, &started); err != nil {
				httpErr(w, 502, err.Error())
				return
			}
			if strings.TrimSpace(started.State) == "" {
				httpErr(w, 502, "hub returned no login state")
				return
			}
			cicyCloudPendingLogins.Store(state, cicyCloudPendingLogin{Team: team, HubOrigin: hub, InstanceID: instanceID})
			hubLoginStates.Store(state, started.State)
			J(w, M{"state": state, "status": "pending", "email": email, "team": team, "hub_origin": hub})
			return
		}
		err = cloudJSON(http.MethodPost, "/api/auth/email/request", "", M{
			"email": email, "state": state, "flow": "desktop_poll", "lang": "zh",
			"platform": "cicy-code", "system": runtime.GOOS, "arch": runtime.GOARCH,
			"runtime": telemetry.Runtime, "hostname": hostname, "instanceId": existing.InstanceID,
			"teamId":        team,
			"clientVersion": version, "cpu": fmt.Sprintf("%s · %d cores", telemetry.CPUModel, telemetry.CPUCores),
			"memory": fmt.Sprintf("%.1f GB", float64(telemetry.MemoryTotalMB)/1024), "gpu": telemetry.GPU,
		}, nil)
		if err != nil {
			httpErr(w, 502, err.Error())
			return
		}
		cicyCloudPendingLogins.Store(state, cicyCloudPendingLogin{Team: team})
		J(w, M{"state": state, "status": "pending", "email": email, "team": team})
		return
	}
	if r.Method == http.MethodGet && len(parts) == 3 {
		state := strings.TrimSpace(parts[2])
		if len(state) < 32 {
			httpErr(w, 400, "invalid state")
			return
		}
		pendingValue, hasPending := cicyCloudPendingLogins.Load(state)
		pending, _ := pendingValue.(cicyCloudPendingLogin)
		if hasPending && pending.HubOrigin != "" {
			hubStateValue, _ := hubLoginStates.Load(state)
			hubState, _ := hubStateValue.(string)
			var poll struct {
				Status     string `json:"status"`
				Token      string `json:"token"`
				Owner      string `json:"owner"`
				InstanceID string `json:"instanceId"`
			}
			if err := cloudJSONAt(pending.HubOrigin, http.MethodGet, "/api/login/poll?state="+url.QueryEscape(hubState), "", nil, &poll); err != nil {
				httpErr(w, 502, err.Error())
				return
			}
			if poll.Status != "ready" {
				J(w, M{"status": poll.Status})
				return
			}
			cred := cicyCloudCredential{Email: poll.Owner, InstanceID: pending.InstanceID, TeamID: pending.Team, Token: poll.Token,
				Origin: pending.HubOrigin, Mode: cicyCloudModeHub, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
			if poll.InstanceID != "" {
				cred.InstanceID = poll.InstanceID
			}
			if err := saveCiCyCloudCredential(cred); err != nil {
				httpErr(w, 500, err.Error())
				return
			}
			cicyCloudPendingLogins.Delete(state)
			hubLoginStates.Delete(state)
			acc, err := upsertCiCyCloudIMAccount(cred)
			if err != nil {
				httpErr(w, 500, err.Error())
				return
			}
			J(w, M{"status": "ready", "account": imAccountToMap(acc)})
			return
		}
		var poll struct {
			Status string `json:"status"`
			Token  string `json:"token"`
			Email  string `json:"email"`
		}
		if err := cloudJSON(http.MethodGet, "/api/auth/desktop/poll?state="+state, "", nil, &poll); err != nil {
			httpErr(w, 502, err.Error())
			return
		}
		if poll.Status != "ready" {
			J(w, M{"status": poll.Status})
			return
		}
		var existing cicyCloudCredential
		if data, err := os.ReadFile(cicyCloudCredentialPath()); err == nil {
			_ = json.Unmarshal(data, &existing)
		}
		instanceID := strings.TrimSpace(existing.InstanceID)
		if instanceID == "" {
			instanceID, _ = randomCodeInstanceID()
		}
		if instanceID == "" {
			httpErr(w, 500, "instance id failed")
			return
		}
		teamID := strings.TrimSpace(pending.Team)
		if !hasPending || teamID == "" {
			httpErr(w, 400, "login team missing")
			return
		}
		if err := cloudJSON(http.MethodPost, "/api/code/instances/register", poll.Token, M{
			"instanceId": instanceID, "teamId": teamID, "platform": "native", "runtime": "native",
		}, nil); err != nil {
			httpErr(w, 502, err.Error())
			return
		}
		cred := cicyCloudCredential{Email: poll.Email, InstanceID: instanceID, TeamID: teamID, Token: poll.Token, Origin: cicyCloudOrigin(), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
		// A previous hub-mode credential must not survive a cloud login.
		if err := saveCiCyCloudCredential(cred); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		cicyCloudPendingLogins.Delete(state)
		acc, err := upsertCiCyCloudIMAccount(cred)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"status": "ready", "account": imAccountToMap(acc)})
		return
	}
	httpErr(w, 405, "method not allowed")
}
