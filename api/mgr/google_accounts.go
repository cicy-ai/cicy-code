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

func chromeAccountsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "cicy-ai", "db", "chrome.json")
}

func readChromeAccounts() (map[string]map[string]any, error) {
	profiles := map[string]map[string]any{}
	b, err := os.ReadFile(chromeAccountsPath())
	if os.IsNotExist(err) {
		return profiles, nil
	}
	if err != nil {
		return nil, err
	}
	return profiles, json.Unmarshal(b, &profiles)
}

func writeChromeAccounts(profiles map[string]map[string]any) error {
	p := chromeAccountsPath()
	b, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(filepath.Dir(p), ".chrome.json.*")
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

func googleAccountNode(profile map[string]any) (map[string]any, string) {
	accounts, _ := profile["accounts"].(map[string]any)
	if accounts != nil {
		for _, provider := range []string{"google", "gmail"} {
			if account, ok := accounts[provider].(map[string]any); ok {
				return account, provider
			}
		}
	}
	return nil, ""
}

func googleString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func handleGoogleAccounts(w http.ResponseWriter, r *http.Request) {
	profiles, err := readChromeAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "invalid chrome.json")
		return
	}
	if r.Method == http.MethodGet {
		if profileName := strings.TrimSpace(r.URL.Query().Get("reveal_secrets")); profileName != "" {
			profile, ok := profiles[profileName]
			if !ok {
				httpErr(w, http.StatusNotFound, "Chrome profile not found")
				return
			}
			account, _ := googleAccountNode(profile)
			J(w, M{"password": googleString(account, "password"), "2fa": googleString(account, "totp")})
			return
		}
		type item struct {
			Profile       string `json:"profile"`
			Email         string `json:"email"`
			Mobile        string `json:"mobile"`
			RecoveryEmail string `json:"recovery_email"`
			PasswordSet   bool   `json:"password_set"`
			TwoFASet      bool   `json:"2fa_set"`
		}
		items := make([]item, 0, len(profiles))
		for profileName, profile := range profiles {
			account, _ := googleAccountNode(profile)
			email := googleString(account, "account")
			if email == "" {
				email, _ = profile["gmail"].(string)
				email = strings.TrimSpace(email)
			}
			if email == "" && account == nil {
				continue
			}
			items = append(items, item{Profile: profileName, Email: email, Mobile: googleString(account, "mobile"), RecoveryEmail: googleString(account, "recovery_email"), PasswordSet: googleString(account, "password") != "", TwoFASet: googleString(account, "totp") != ""})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Profile < items[j].Profile })
		J(w, M{"accounts": items})
		return
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		var req struct {
			Profile       string `json:"profile"`
			Email         string `json:"email"`
			Password      string `json:"password"`
			TwoFA         string `json:"2fa"`
			Mobile        string `json:"mobile"`
			RecoveryEmail string `json:"recovery_email"`
		}
		if readBody(r, &req) != nil {
			httpErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		req.Profile = strings.TrimSpace(req.Profile)
		if !githubAccountNameRE.MatchString(req.Profile) {
			httpErr(w, http.StatusBadRequest, "invalid Chrome profile")
			return
		}
		profile := profiles[req.Profile]
		if profile == nil {
			profile = map[string]any{}
			profiles[req.Profile] = profile
		}
		accounts, _ := profile["accounts"].(map[string]any)
		if accounts == nil {
			accounts = map[string]any{}
			profile["accounts"] = accounts
		}
		account, provider := googleAccountNode(profile)
		if account == nil {
			account = map[string]any{}
			provider = "google"
			accounts[provider] = account
		}
		setOptional := func(key, value string) {
			value = strings.TrimSpace(value)
			if value == "" {
				delete(account, key)
			} else {
				account[key] = value
			}
		}
		setOptional("account", req.Email)
		setOptional("password", req.Password)
		setOptional("totp", req.TwoFA)
		setOptional("mobile", req.Mobile)
		setOptional("recovery_email", req.RecoveryEmail)
		if strings.TrimSpace(req.Email) != "" {
			profile["gmail"] = strings.TrimSpace(req.Email)
		}
		if err := writeChromeAccounts(profiles); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true})
		return
	}
	httpErr(w, http.StatusMethodNotAllowed, "GET, POST or PUT required")
}
