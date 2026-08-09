package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type cloudflareConfig struct {
	Version  int                       `json:"version"`
	Default  string                    `json:"default"`
	Accounts map[string]map[string]any `json:"accounts"`
}

func cloudflarePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "cicy-ai", "db", "cf.json")
}
func readCloudflareConfig() (cloudflareConfig, error) {
	c := cloudflareConfig{Version: 1, Accounts: map[string]map[string]any{}}
	b, err := os.ReadFile(cloudflarePath())
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	if c.Accounts == nil {
		c.Accounts = map[string]map[string]any{}
	}
	return c, err
}
func writeCloudflareConfig(c cloudflareConfig) error {
	p := cloudflarePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(filepath.Dir(p), ".cf.json.*")
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

func handleCloudflareAccounts(w http.ResponseWriter, r *http.Request) {
	c, err := readCloudflareConfig()
	if err != nil {
		httpErr(w, 500, "invalid cf.json")
		return
	}
	if r.Method == http.MethodGet {
		if name := strings.TrimSpace(r.URL.Query().Get("reveal_token")); name != "" {
			a, ok := c.Accounts[name]
			if !ok {
				httpErr(w, http.StatusNotFound, "Cloudflare account not found")
				return
			}
			token, _ := a["api_token"].(string)
			J(w, M{"api_token": token})
			return
		}
		type item struct {
			Name      string            `json:"name"`
			Label     string            `json:"label"`
			Kind      string            `json:"kind"`
			AccountID string            `json:"account_id"`
			Target    string            `json:"target"`
			TokenSet  bool              `json:"token_set"`
			IsDefault bool              `json:"is_default"`
			Details   map[string]string `json:"details"`
		}
		items := make([]item, 0, len(c.Accounts))
		for name, a := range c.Accounts {
			str := func(k string) string { v, _ := a[k].(string); return strings.TrimSpace(v) }
			target := str("hostname")
			if target == "" {
				target = str("bucket")
			}
			if target == "" {
				target = str("service")
			}
			details := map[string]string{}
			for k, v := range a {
				if k != "api_token" {
					if s, ok := v.(string); ok {
						details[k] = s
					}
				}
			}
			items = append(items, item{Name: name, Label: str("label"), Kind: str("kind"), AccountID: str("account_id"), Target: target, TokenSet: str("api_token") != "", IsDefault: name == c.Default, Details: details})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		J(w, M{"accounts": items})
		return
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		var req struct {
			Name      string            `json:"name"`
			OldName   string            `json:"old_name"`
			Label     string            `json:"label"`
			Kind      string            `json:"kind"`
			AccountID string            `json:"account_id"`
			APIToken  string            `json:"api_token"`
			IsDefault bool              `json:"is_default"`
			Details   map[string]string `json:"details"`
		}
		if readBody(r, &req) != nil {
			httpErr(w, 400, "invalid body")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.OldName = strings.TrimSpace(req.OldName)
		if !githubAccountNameRE.MatchString(req.Name) {
			httpErr(w, 400, "invalid Cloudflare account name")
			return
		}
		a := c.Accounts[req.Name]
		if req.OldName != "" {
			if old, ok := c.Accounts[req.OldName]; ok {
				a = old
				if req.OldName != req.Name {
					delete(c.Accounts, req.OldName)
				}
			}
		}
		if a == nil {
			a = map[string]any{}
		}
		a["label"] = strings.TrimSpace(req.Label)
		a["kind"] = strings.TrimSpace(req.Kind)
		a["account_id"] = strings.TrimSpace(req.AccountID)
		for _, key := range []string{"domain", "hostname", "service", "tunnel_name", "zone", "zone_id", "bucket", "public_url"} {
			if value, ok := req.Details[key]; ok {
				a[key] = strings.TrimSpace(value)
			}
		}
		if strings.TrimSpace(req.APIToken) != "" {
			a["api_token"] = strings.TrimSpace(req.APIToken)
		}
		if token, _ := a["api_token"].(string); strings.TrimSpace(token) == "" {
			httpErr(w, 400, "api_token required")
			return
		}
		c.Accounts[req.Name] = a
		if req.IsDefault || c.Default == "" || c.Default == req.OldName {
			c.Default = req.Name
		}
		if err := writeCloudflareConfig(c); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true})
		return
	}
	if r.Method == http.MethodDelete {
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if _, ok := c.Accounts[name]; !ok {
			httpErr(w, 404, "Cloudflare account not found")
			return
		}
		delete(c.Accounts, name)
		if c.Default == name {
			c.Default = ""
			for n := range c.Accounts {
				c.Default = n
				break
			}
		}
		if err := writeCloudflareConfig(c); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true})
		return
	}
	httpErr(w, 405, "GET, POST, PUT or DELETE required")
}

func handleCloudflareAccountTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, 400, "POST required")
		return
	}
	c, err := readCloudflareConfig()
	if err != nil {
		httpErr(w, 500, "invalid cf.json")
		return
	}
	a, ok := c.Accounts[strings.TrimSpace(req.Name)]
	token, _ := a["api_token"].(string)
	if !ok || strings.TrimSpace(token) == "" {
		httpErr(w, 404, "Cloudflare account not found")
		return
	}
	httpReq, _ := http.NewRequest(http.MethodGet, "https://api.cloudflare.com/client/v4/user/tokens/verify", nil)
	httpReq.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(httpReq)
	if err != nil {
		httpErr(w, 502, "Cloudflare request failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		httpErr(w, resp.StatusCode, "Cloudflare authentication failed")
		return
	}
	J(w, M{"success": true})
}
