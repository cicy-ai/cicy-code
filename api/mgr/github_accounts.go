// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type githubAccountConfig struct {
	APIToken string `json:"api_token"`
	Email    string `json:"email"`
}

var githubAccountNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

func githubAccountsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cicy-ai", "db", "github.json"), nil
}

func readGithubAccounts() (map[string]githubAccountConfig, error) {
	path, err := githubAccountsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]githubAccountConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	accounts := map[string]githubAccountConfig{}
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, fmt.Errorf("parse github.json: %w", err)
	}
	return accounts, nil
}

func writeGithubAccounts(accounts map[string]githubAccountConfig) error {
	path, err := githubAccountsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".github.json.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func githubTokenTail(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 4 {
		return token
	}
	return token[len(token)-4:]
}

func handleGithubAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := readGithubAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		if name := strings.TrimSpace(r.URL.Query().Get("reveal_token")); name != "" {
			account, ok := accounts[name]
			if !ok {
				httpErr(w, http.StatusNotFound, "GitHub account not found")
				return
			}
			J(w, M{"api_token": account.APIToken})
			return
		}
		type item struct {
			Name      string `json:"name"`
			Email     string `json:"email"`
			TokenSet  bool   `json:"token_set"`
			TokenTail string `json:"token_tail,omitempty"`
		}
		items := make([]item, 0, len(accounts))
		for name, account := range accounts {
			items = append(items, item{Name: name, Email: account.Email, TokenSet: strings.TrimSpace(account.APIToken) != "", TokenTail: githubTokenTail(account.APIToken)})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		J(w, M{"accounts": items})
	case http.MethodPut, http.MethodPost:
		var req struct {
			Name     string `json:"name"`
			OldName  string `json:"old_name"`
			Email    string `json:"email"`
			APIToken string `json:"api_token"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.OldName = strings.TrimSpace(req.OldName)
		if !githubAccountNameRE.MatchString(req.Name) {
			httpErr(w, http.StatusBadRequest, "invalid GitHub account name")
			return
		}
		current := accounts[req.Name]
		if req.OldName != "" {
			if old, ok := accounts[req.OldName]; ok {
				current = old
				if req.OldName != req.Name {
					delete(accounts, req.OldName)
				}
			}
		}
		current.Email = strings.TrimSpace(req.Email)
		if strings.TrimSpace(req.APIToken) != "" {
			current.APIToken = strings.TrimSpace(req.APIToken)
		}
		if strings.TrimSpace(current.APIToken) == "" {
			httpErr(w, http.StatusBadRequest, "api_token required for a new account")
			return
		}
		accounts[req.Name] = current
		if err := writeGithubAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true, "name": req.Name, "token_set": true, "token_tail": githubTokenTail(current.APIToken)})
	case http.MethodDelete:
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if _, ok := accounts[name]; !ok {
			httpErr(w, http.StatusNotFound, "GitHub account not found")
			return
		}
		delete(accounts, name)
		if err := writeGithubAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "GET, POST, PUT or DELETE required")
	}
}

func handleGithubAccountTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	accounts, err := readGithubAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	account, ok := accounts[strings.TrimSpace(req.Name)]
	if !ok || strings.TrimSpace(account.APIToken) == "" {
		httpErr(w, http.StatusNotFound, "GitHub account not found")
		return
	}
	httpReq, _ := http.NewRequest(http.MethodGet, "https://api.github.com/user", nil)
	httpReq.Header.Set("Authorization", "Bearer "+account.APIToken)
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("User-Agent", "cicy-code")
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "GitHub request failed")
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		httpErr(w, resp.StatusCode, "GitHub authentication failed")
		return
	}
	var profile struct {
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	_ = json.Unmarshal(body, &profile)
	J(w, M{"success": true, "login": profile.Login, "display_name": profile.Name})
}
