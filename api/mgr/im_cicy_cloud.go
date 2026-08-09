// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/rand"
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
)

const defaultCiCyCloudOrigin = "https://cicy-ai.com"

var cicyCloudEmailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
var cicyCloudTeamRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var cicyCloudPendingLogins sync.Map // state -> requested team ID

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
	accountID    int64
	token        string
	presenceMu   sync.Mutex
	lastPresence time.Time
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
			if errText == "" && (cfg.SystemPrompt != nil || cfg.Meta != nil) {
				dir := filepath.Join(workspace, ".cicy")
				if err := os.MkdirAll(dir, 0755); err != nil {
					errText = "override_mkdir_failed"
				} else {
					if cfg.SystemPrompt != nil {
						if err := os.WriteFile(filepath.Join(dir, "system.md"), []byte(*cfg.SystemPrompt), 0644); err != nil {
							errText = "system_write_failed"
						}
					}
					if errText == "" && cfg.Meta != nil {
						if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(*cfg.Meta), 0644); err != nil {
							errText = "meta_write_failed"
						}
					}
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
	t.reportAllAgents()
	route := "/api/code/messages/poll"
	if strings.TrimSpace(cursor) != "" {
		route += "?ack=" + strings.TrimSpace(cursor)
	}
	var out struct {
		Messages []struct {
			ID               string `json:"id"`
			SenderInstanceID string `json:"senderInstanceId"`
			SenderAgentID    string `json:"senderAgentId"`
			TargetAgentID    string `json:"targetAgentId"`
			Kind             string `json:"kind"`
			Text             string `json:"text"`
			ReplyTo          string `json:"replyTo"`
		} `json:"messages"`
	}
	if err := cloudJSON(http.MethodGet, route, t.token, nil, &out); err != nil {
		return nil, cursor, err
	}
	if len(out.Messages) == 0 {
		time.Sleep(2 * time.Second)
		return nil, "", nil
	}
	msgs := make([]botMsg, 0, len(out.Messages))
	ids := make([]string, 0, len(out.Messages))
	for _, item := range out.Messages {
		if item.Kind == "rpc_request" {
			if err := t.handleRPCRequest(item.ID, item.SenderInstanceID, item.SenderAgentID, item.TargetAgentID, item.Text); err != nil {
				log.Printf("[im] cicy cloud rpc failed id=%s op_target=%s: %v", item.ID, item.TargetAgentID, err)
				continue
			}
			ids = append(ids, item.ID)
			continue
		}
		ids = append(ids, item.ID)
		// Agent output is a terminal reply, not another user request. A reply is
		// acknowledged but must never be fed back into an Agent, otherwise two
		// connected instances automatically answer each other forever.
		if item.Kind == "agent_reply" || item.Kind == "rpc_reply" {
			continue
		}
		peer := item.SenderInstanceID
		if item.SenderAgentID != "" {
			peer += "|" + item.SenderAgentID
		}
		msgs = append(msgs, botMsg{Text: item.Text, FromID: item.SenderInstanceID,
			TargetPaneID: item.TargetAgentID,
			AckID:        item.ID,
			Peer:         botPeer{ChatID: peer, ContextToken: item.TargetAgentID + "|" + item.ID}})
	}
	return msgs, strings.Join(ids, ","), nil
}

func (t *cicyCloudTransport) handleRPCRequest(messageID, senderInstanceID, senderAgentID, targetAgentID, text string) error {
	var req struct {
		Op     string `json:"op"`
		Full   bool   `json:"full"`
		Index  int    `json:"index"`
		From   string `json:"from"`
		To     string `json:"to"`
		Status string `json:"status"`
		Open   bool   `json:"open"`
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
	return cloudJSON(http.MethodPost, "/api/code/messages", t.token, payload, nil)
}

// Ack confirms one Cloud message only after imHandleInbound accepted it. The
// poll endpoint already supports explicit ack ids; any messages returned by
// this acknowledgement request remain unacked and are fetched by the next Poll.
func (t *cicyCloudTransport) Ack(messageID string) error {
	id := strings.TrimSpace(messageID)
	if id == "" {
		return nil
	}
	var out struct {
		Messages []json.RawMessage `json:"messages"`
	}
	return cloudJSON(http.MethodGet, "/api/code/messages/poll?ack="+url.QueryEscape(id), t.token, nil, &out)
}

func (t *cicyCloudTransport) Send(peer botPeer, text string) (string, error) {
	var out struct {
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	targetInstance, targetAgent := splitCiCyCloudPeer(peer.ChatID)
	senderAgent, replyTo := splitCiCyCloudReplyContext(peer.ContextToken)
	err := cloudJSON(http.MethodPost, "/api/code/messages", t.token, M{
		"targetInstanceId": targetInstance, "targetAgentId": targetAgent,
		"senderAgentId": senderAgent, "text": text,
		"kind": "agent_reply", "replyTo": replyTo, "hopCount": 1,
	}, &out)
	if err == nil && strings.TrimSpace(replyTo) != "" {
		markCiCyCloudInboxReplied(replyTo)
	}
	return out.Message.ID, err
}

func splitCiCyCloudReplyContext(contextToken string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(contextToken), "|", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(contextToken), ""
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
	if time.Since(t.lastPresence) < 15*time.Second {
		return
	}
	t.lastPresence = time.Now()
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
		agents = append(agents, M{"agentId": shortPaneID(id), "title": p.Title,
			"agentType": p.AgentType, "role": p.Role, "status": p.Status,
			"model": p.Model, "contextUsedPct": p.ContextUsedPct})
	}
	if err := cloudJSON(http.MethodPost, "/api/code/agents", t.token, M{"agents": agents}, nil); err != nil {
		log.Printf("[im] cicy cloud presence failed: %v", err)
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
	if len(parts) >= 2 && parts[1] == "send" && r.Method == http.MethodPost {
		var in struct {
			TargetInstanceID string `json:"target_instance_id"`
			TargetAgentID    string `json:"target_agent_id"`
			SenderAgentID    string `json:"sender_agent_id"`
			Text             string `json:"text"`
		}
		if readBody(r, &in) != nil || strings.TrimSpace(in.TargetInstanceID) == "" || strings.TrimSpace(in.Text) == "" {
			httpErr(w, 400, "target_instance_id and text required")
			return
		}
		var token string
		_ = store.QueryRow("SELECT secret FROM im_accounts WHERE platform=? ORDER BY id LIMIT 1", imPlatformCiCyCloud).Scan(&token)
		var out any
		if err := cloudJSON(http.MethodPost, "/api/code/messages", token, M{
			"targetInstanceId": strings.TrimSpace(in.TargetInstanceID),
			"targetAgentId":    strings.TrimSpace(in.TargetAgentID),
			"senderAgentId":    strings.TrimSpace(in.SenderAgentID), "text": strings.TrimSpace(in.Text),
		}, &out); err != nil {
			httpErr(w, 502, err.Error())
			return
		}
		J(w, out)
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
