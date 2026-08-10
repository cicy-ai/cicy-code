// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
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
	TwoFA    string `json:"2fa"`
	Profile  string `json:"profile"`
	Password string `json:"password"`
}

func handleGithubAccountTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	accounts, err := readGithubAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	account, ok := accounts[strings.TrimSpace(req.Name)]
	if !ok {
		httpErr(w, http.StatusNotFound, "GitHub account not found")
		return
	}
	if strings.TrimSpace(account.TwoFA) == "" {
		httpErr(w, http.StatusBadRequest, "2fa not configured")
		return
	}
	payload, _ := json.Marshal(M{"twoFactor": account.TwoFA})
	httpReq, _ := http.NewRequest(http.MethodPost, "https://otp.cicy-ai.com/api/totp", bytes.NewReader(payload))
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(httpReq)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "TOTP service request failed")
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		httpErr(w, http.StatusBadGateway, "TOTP service rejected the key")
		return
	}
	var result struct {
		Capture   string `json:"capture"`
		Code      string `json:"code"`
		Countdown int    `json:"countdown"`
		Period    int    `json:"period"`
	}
	if json.Unmarshal(body, &result) != nil || result.Code == "" {
		httpErr(w, http.StatusBadGateway, "invalid TOTP service response")
		return
	}
	J(w, result)
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
			J(w, M{"api_token": account.APIToken, "2fa": account.TwoFA, "password": account.Password})
			return
		}
		type item struct {
			Name        string `json:"name"`
			Email       string `json:"email"`
			TokenSet    bool   `json:"token_set"`
			TokenTail   string `json:"token_tail,omitempty"`
			TwoFASet    bool   `json:"2fa_set"`
			Profile     string `json:"profile"`
			PasswordSet bool   `json:"password_set"`
		}
		items := make([]item, 0, len(accounts))
		for name, account := range accounts {
			items = append(items, item{Name: name, Email: account.Email, TokenSet: strings.TrimSpace(account.APIToken) != "", TokenTail: githubTokenTail(account.APIToken), TwoFASet: strings.TrimSpace(account.TwoFA) != "", Profile: account.Profile, PasswordSet: strings.TrimSpace(account.Password) != ""})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		J(w, M{"accounts": items})
	case http.MethodPut, http.MethodPost:
		var req struct {
			Name     string `json:"name"`
			OldName  string `json:"old_name"`
			Email    string `json:"email"`
			APIToken string `json:"api_token"`
			TwoFA    string `json:"2fa"`
			Profile  string `json:"profile"`
			Password string `json:"password"`
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
		current.TwoFA = strings.TrimSpace(req.TwoFA)
		current.Profile = strings.TrimSpace(req.Profile)
		current.Password = strings.TrimSpace(req.Password)
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

func handleGithubAccountUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if readBody(r, &req) != nil {
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
	client := &http.Client{Timeout: 15 * time.Second}
	githubGet := func(url string, out any) int {
		httpReq, _ := http.NewRequest(http.MethodGet, url, nil)
		httpReq.Header.Set("Authorization", "Bearer "+account.APIToken)
		httpReq.Header.Set("Accept", "application/vnd.github+json")
		httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		httpReq.Header.Set("User-Agent", "cicy-code")
		resp, requestErr := client.Do(httpReq)
		if requestErr != nil {
			return 0
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			_ = json.Unmarshal(body, out)
		}
		return resp.StatusCode
	}
	var profile struct {
		Login string `json:"login"`
		Plan  struct {
			Name string `json:"name"`
		} `json:"plan"`
	}
	if githubGet("https://api.github.com/user", &profile) != http.StatusOK || profile.Login == "" {
		httpErr(w, http.StatusBadGateway, "GitHub authentication failed")
		return
	}
	now := time.Now().UTC()
	var enhanced struct {
		UsageItems []struct {
			Product        string  `json:"product"`
			SKU            string  `json:"sku"`
			Quantity       float64 `json:"quantity"`
			UnitType       string  `json:"unitType"`
			GrossAmount    float64 `json:"grossAmount"`
			DiscountAmount float64 `json:"discountAmount"`
			NetAmount      float64 `json:"netAmount"`
		} `json:"usageItems"`
	}
	enhancedURL := fmt.Sprintf("https://api.github.com/users/%s/settings/billing/usage?year=%d&month=%d", profile.Login, now.Year(), int(now.Month()))
	enhancedStatus := githubGet(enhancedURL, &enhanced)
	var legacy struct {
		TotalMinutesUsed     float64 `json:"total_minutes_used"`
		TotalPaidMinutesUsed float64 `json:"total_paid_minutes_used"`
		IncludedMinutes      float64 `json:"included_minutes"`
	}
	legacyStatus := githubGet("https://api.github.com/users/"+profile.Login+"/settings/billing/actions", &legacy)
	minutes := legacy.TotalMinutesUsed
	enhancedMinutes := 0.0
	gross, discount, net := 0.0, 0.0, 0.0
	for _, item := range enhanced.UsageItems {
		if !strings.EqualFold(item.Product, "Actions") {
			continue
		}
		gross += item.GrossAmount
		discount += item.DiscountAmount
		net += item.NetAmount
		if strings.Contains(strings.ToLower(item.UnitType), "minute") {
			enhancedMinutes += item.Quantity
		}
	}
	if legacyStatus < 200 || legacyStatus >= 300 {
		minutes = enhancedMinutes
	}
	if enhancedStatus < 200 || enhancedStatus >= 300 {
		if legacyStatus < 200 || legacyStatus >= 300 {
			httpErr(w, http.StatusForbidden, "GitHub Token lacks Billing read permission")
			return
		}
	}
	included := legacy.IncludedMinutes
	includedAvailable := legacyStatus >= 200 && legacyStatus < 300
	if !includedAvailable {
		switch strings.ToLower(strings.TrimSpace(profile.Plan.Name)) {
		case "free":
			included = 2000
			includedAvailable = true
		case "pro":
			included = 3000
			includedAvailable = true
		}
	}
	reset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	J(w, M{"login": profile.Login, "actions_minutes": minutes, "included_minutes": included, "paid_minutes": legacy.TotalPaidMinutesUsed, "gross_amount": gross, "discount_amount": discount, "net_amount": net, "reset_at": reset.Format(time.RFC3339), "included_available": includedAvailable, "plan": profile.Plan.Name})
}
