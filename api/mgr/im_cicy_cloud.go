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
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
		_, err = store.Exec("UPDATE im_accounts SET name=?,secret=?,config=?,enabled=0,state='connected',state_detail='',updated_at="+store.Now()+" WHERE id=?",
			cred.Email, cred.Token, string(cfg), id)
	} else {
		res, insertErr := store.Exec("INSERT INTO im_accounts (platform,name,secret,config,enabled,state,state_detail,inbound_to_agent) VALUES (?,?,?,?,0,'connected','',1)",
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

func handleCiCyCloudLoginRoute(w http.ResponseWriter, r *http.Request, parts []string) {
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
