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
var cicyCloudPendingLogins sync.Map // state -> requested team ID

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
	UpdatedAt  string `json:"updated_at"`
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

func cloudJSON(method, route, token string, requestBody any, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		data, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, cicyCloudOrigin()+route, body)
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
	cfg, _ := json.Marshal(M{"email": cred.Email, "instance_id": cred.InstanceID, "team_id": cred.TeamID, "cloud_origin": cred.Origin})
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
				t.streamConnMu.Unlock()
				t.signalStreamWake()
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
	if t.streamConn == nil {
		t.streamConnMu.Unlock()
		return cicyCloudServerFrame{}, fmt.Errorf("cloud websocket disconnected")
	}
	t.streamWaiters[requestID] = waiter
	t.streamConnMu.Unlock()
	if err := t.writeStream(frame); err != nil {
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

// reportAllAgents follows cicy-hub's full-snapshot presence model: every report
// replaces this instance's directory, so deleted agents disappear naturally.
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
		telemetry := collectCiCyCodeTelemetry()
		heartbeat := M{"platform": telemetry.Platform, "arch": telemetry.Arch,
			"runtime": telemetry.Runtime, "cpuModel": telemetry.CPUModel,
			"cpuCores": telemetry.CPUCores, "memoryTotalMB": telemetry.MemoryTotalMB,
			"gpu": telemetry.GPU}
		heartbeat["ports"] = portMaps(loadPublishedPorts())
		if tunnelURL := cftCurrentURL(); tunnelURL != "" {
			heartbeat["tunnelUrl"] = tunnelURL
			heartbeat["tunnelToken"] = loadAPIToken()
		}
		if err := cloudJSON(http.MethodPost, "/api/code/instances/heartbeat", t.token, heartbeat, nil); err != nil {
			log.Printf("[im] cicy cloud heartbeat failed: %v", err)
		}
	}
	if !presenceDue {
		return
	}
	t.lastPresence = now
	t.syncAgentConfigs()
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8008"
	}
	u := "http://127.0.0.1:" + port + "/api/panes?token=" + url.QueryEscape(loadAPIToken())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(u)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}
	var doc struct {
		Panes []struct {
			PaneID         string `json:"pane_id"`
			ID             string `json:"id"`
			Title          string `json:"title"`
			AgentType      string `json:"agent_type"`
			Role           string `json:"role"`
			Status         string `json:"status"`
			Model          string `json:"model"`
			ContextUsedPct int    `json:"context_used_pct"`
			UseCustomGateway bool `json:"use_custom_gateway"`
		} `json:"panes"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&doc) != nil {
		return
	}
	agents := make([]M, 0, len(doc.Panes))
	for _, p := range doc.Panes {
		id := strings.TrimSpace(p.PaneID)
		if id == "" {
			id = strings.TrimSpace(p.ID)
		}
		if id == "" {
			continue
		}
		status, model, contextUsedPct, cost := p.Status, p.Model, p.ContextUsedPct, float64(0)
		if metrics := agentInspectorLiteMetrics(shortPaneID(id)); metrics != nil {
			status = aiGatewayString(metrics["status"])
			model = aiGatewayString(metrics["model"])
			contextUsedPct = int(aiGatewayFloat(metrics["context_used_pct"]))
			cost = aiGatewayFloat(metrics["cost_credit"])
		}
		agents = append(agents, M{"agentId": shortPaneID(id), "title": p.Title,
			"agentType": p.AgentType, "role": p.Role, "status": status,
			"model": model, "contextUsedPct": contextUsedPct, "cost": cost, "useCustomGateway": p.UseCustomGateway})
	}
	if err := cloudJSON(http.MethodPost, "/api/code/agents", t.token, M{"agents": agents}, nil); err != nil {
		log.Printf("[im] cicy cloud presence failed: %v", err)
	}
}

// reportAgentState sends the same live snapshot used by TeamPanel. Unlike the
// roster POST this is an incremental update and therefore cannot remove peers.
func (t *cicyCloudTransport) reportAgentState(paneID string) {
	metrics := agentInspectorLiteMetrics(shortPaneID(paneID))
	if metrics == nil {
		return
	}
	payload := M{"agentId": shortPaneID(paneID), "status": metrics["status"],
		"model": metrics["model"], "contextUsedPct": metrics["context_used_pct"],
		"cost": metrics["cost_credit"]}
	if err := cloudJSON(http.MethodPatch, "/api/code/agents", t.token, payload, nil); err != nil {
		log.Printf("[im] cicy cloud live agent state failed: %v", err)
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
		transport, err := newCiCyCloudTransport(account)
		if err == nil {
			go transport.(*cicyCloudTransport).reportAgentState(paneID)
		}
	}
}

// reportCiCyCloudAgentRosterNow pushes a full snapshot immediately. A fresh
// transport has no presence timestamps, so reportAllAgents bypasses the normal
// polling interval. This is used after deletion where an incremental state
// update cannot express that an agent disappeared.
func reportCiCyCloudAgentRosterNow() {
	accounts, err := imListAccounts()
	if err != nil {
		return
	}
	for _, account := range accounts {
		if !account.Enabled || account.Platform != imPlatformCiCyCloud || strings.TrimSpace(account.Secret) == "" {
			continue
		}
		transport, err := newCiCyCloudTransport(account)
		if err == nil {
			go transport.(*cicyCloudTransport).reportAllAgents()
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
	// POST /api/im/cicy-cloud/login; GET /api/im/cicy-cloud/login/{state}
	if len(parts) < 2 || parts[1] != "login" {
		httpErr(w, 404, "not found")
		return
	}
	if r.Method == http.MethodPost && len(parts) == 2 {
		var in struct {
			Email string `json:"email"`
			Team  string `json:"team"`
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
		cicyCloudPendingLogins.Store(state, team)
		J(w, M{"state": state, "status": "pending", "email": email, "team": team})
		return
	}
	if r.Method == http.MethodGet && len(parts) == 3 {
		state := strings.TrimSpace(parts[2])
		if len(state) < 32 {
			httpErr(w, 400, "invalid state")
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
		pendingTeam, ok := cicyCloudPendingLogins.Load(state)
		teamID := strings.TrimSpace(fmt.Sprint(pendingTeam))
		if !ok || teamID == "" {
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
