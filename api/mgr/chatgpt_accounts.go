// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type chatGPTAccountConfig struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Mobile   string `json:"mobile"`
	TwoFA    string `json:"2fa"`
	Profile  string `json:"profile"`
}

func chatGPTAccountsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "cicy-ai", "db", "chatgpt.json")
}

func readChatGPTAccounts() (map[string]chatGPTAccountConfig, error) {
	accounts := map[string]chatGPTAccountConfig{}
	b, err := os.ReadFile(chatGPTAccountsPath())
	if os.IsNotExist(err) {
		return accounts, nil
	}
	if err != nil {
		return nil, err
	}
	return accounts, json.Unmarshal(b, &accounts)
}

func writeChatGPTAccounts(accounts map[string]chatGPTAccountConfig) error {
	p := chatGPTAccountsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(filepath.Dir(p), ".chatgpt.json.*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(b)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, p); err != nil {
		return err
	}
	return os.Chmod(p, 0o600)
}

func handleChatGPTAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := readChatGPTAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "invalid chatgpt.json")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if name := strings.TrimSpace(r.URL.Query().Get("reveal_secrets")); name != "" {
			account, ok := accounts[name]
			if !ok {
				httpErr(w, http.StatusNotFound, "ChatGPT account not found")
				return
			}
			J(w, M{"password": account.Password, "2fa": account.TwoFA})
			return
		}
		type item struct {
			Name        string `json:"name"`
			Email       string `json:"email"`
			Mobile      string `json:"mobile"`
			PasswordSet bool   `json:"password_set"`
			TwoFASet    bool   `json:"2fa_set"`
			Profile     string `json:"profile"`
		}
		items := make([]item, 0, len(accounts))
		for name, account := range accounts {
			items = append(items, item{Name: name, Email: account.Email, Mobile: account.Mobile, PasswordSet: strings.TrimSpace(account.Password) != "", TwoFASet: strings.TrimSpace(account.TwoFA) != "", Profile: account.Profile})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		J(w, M{"accounts": items})
	case http.MethodPut, http.MethodPost:
		var req struct {
			Name     string `json:"name"`
			OldName  string `json:"old_name"`
			Email    string `json:"email"`
			Password string `json:"password"`
			Mobile   string `json:"mobile"`
			TwoFA    string `json:"2fa"`
			Profile  string `json:"profile"`
		}
		if readBody(r, &req) != nil {
			httpErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		req.Email = strings.TrimSpace(req.Email)
		req.Name = strings.TrimSpace(req.Name)
		if at := strings.Index(req.Email, "@"); at > 0 {
			req.Name = strings.TrimSpace(req.Email[:at])
		}
		if !githubAccountNameRE.MatchString(req.Name) {
			httpErr(w, http.StatusBadRequest, "invalid ChatGPT account name")
			return
		}
		if oldName := strings.TrimSpace(req.OldName); oldName != "" && oldName != req.Name {
			delete(accounts, oldName)
		}
		accounts[req.Name] = chatGPTAccountConfig{Email: req.Email, Password: strings.TrimSpace(req.Password), Mobile: strings.TrimSpace(req.Mobile), TwoFA: strings.TrimSpace(req.TwoFA), Profile: strings.TrimSpace(req.Profile)}
		if err := writeChatGPTAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true, "name": req.Name})
	case http.MethodDelete:
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if _, ok := accounts[name]; !ok {
			httpErr(w, http.StatusNotFound, "ChatGPT account not found")
			return
		}
		delete(accounts, name)
		if err := writeChatGPTAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "GET, POST, PUT or DELETE required")
	}
}
