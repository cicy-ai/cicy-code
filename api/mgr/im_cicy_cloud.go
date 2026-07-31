// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const defaultCiCyCloudOrigin = "https://cicy-ai.com"

var cicyCloudEmailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

type cicyCloudCredential struct {
	Email      string `json:"email"`
	InstanceID string `json:"instance_id"`
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
	cfg, _ := json.Marshal(M{"email": cred.Email, "instance_id": cred.InstanceID, "cloud_origin": cred.Origin})
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
			Text             string `json:"text"`
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
		ids = append(ids, item.ID)
		peer := item.SenderInstanceID
		if item.SenderAgentID != "" {
			peer += "|" + item.SenderAgentID
		}
		msgs = append(msgs, botMsg{Text: item.Text, FromID: item.SenderInstanceID,
			TargetPaneID: item.TargetAgentID,
			Peer:         botPeer{ChatID: peer, ContextToken: item.TargetAgentID}})
	}
	return msgs, strings.Join(ids, ","), nil
}

func (t *cicyCloudTransport) Send(peer botPeer, text string) (string, error) {
	var out struct {
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	targetInstance, targetAgent := splitCiCyCloudPeer(peer.ChatID)
	err := cloudJSON(http.MethodPost, "/api/code/messages", t.token, M{
		"targetInstanceId": targetInstance, "targetAgentId": targetAgent,
		"senderAgentId": strings.TrimSpace(peer.ContextToken), "text": text,
	}, &out)
	return out.Message.ID, err
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
		}
		if readBody(r, &in) != nil {
			httpErr(w, 400, "invalid request body")
			return
		}
		email := strings.ToLower(strings.TrimSpace(in.Email))
		if !cicyCloudEmailRE.MatchString(email) {
			httpErr(w, 400, "invalid email")
			return
		}
		state, err := randomLoginState()
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		err = cloudJSON(http.MethodPost, "/api/auth/email/request", "", M{
			"email": email, "state": state, "flow": "desktop_poll", "lang": "zh",
		}, nil)
		if err != nil {
			httpErr(w, 502, err.Error())
			return
		}
		J(w, M{"state": state, "status": "pending", "email": email})
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
		if err := cloudJSON(http.MethodPost, "/api/code/instances/register", poll.Token, M{
			"instanceId": instanceID, "platform": "native", "runtime": "native",
		}, nil); err != nil {
			httpErr(w, 502, err.Error())
			return
		}
		cred := cicyCloudCredential{Email: poll.Email, InstanceID: instanceID, Token: poll.Token, Origin: cicyCloudOrigin(), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
		if err := saveCiCyCloudCredential(cred); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
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
