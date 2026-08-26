// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Docker registry account matrix. Tokens live in ~/cicy-ai/db/docker.json
// (0600); the list endpoint only reports whether one is set plus its last four
// characters. "Bind" merges the credential into ~/.docker/config.json so
// `docker push` on this host runs as that account without the page ever seeing
// the secret.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type dockerAccountConfig struct {
	Username string `json:"username"`
	APIToken string `json:"api_token"`
	Email    string `json:"email"`
	TwoFA    string `json:"2fa"`
	Profile  string `json:"profile"`
	Password string `json:"password"`
	Registry string `json:"registry"`
}

const (
	dockerDefaultRegistry = "docker.io"
	dockerHubAuthKey      = "https://index.docker.io/v1/"
)

var dockerAccountNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

func readDockerAccounts() (map[string]dockerAccountConfig, error) {
	return readAccountStore[dockerAccountConfig]("docker.json")
}

func writeDockerAccounts(accounts map[string]dockerAccountConfig) error {
	return writeAccountStore("docker.json", accounts)
}

// dockerRegistryOf normalises to a bare host ("docker.io", "ghcr.io",
// "registry.cn-hangzhou.aliyuncs.com") — that is what `docker login` takes.
func dockerRegistryOf(account dockerAccountConfig) string {
	registry := strings.TrimSpace(account.Registry)
	if registry == "" {
		return dockerDefaultRegistry
	}
	registry = strings.TrimSuffix(strings.TrimSpace(registry), "/")
	if parsed, err := url.Parse(registry); err == nil && parsed.Host != "" {
		registry = parsed.Host
	}
	if registry == "index.docker.io" || registry == "registry-1.docker.io" {
		return dockerDefaultRegistry
	}
	return registry
}

func dockerIsHub(registry string) bool { return registry == dockerDefaultRegistry }

// dockerAuthKeyFor is the key `docker login` writes in ~/.docker/config.json —
// Hub keeps its historical v1 URL, everything else is the bare host.
func dockerAuthKeyFor(registry string) string {
	if dockerIsHub(registry) {
		return dockerHubAuthKey
	}
	return registry
}

func dockerAccountByName(name string) (dockerAccountConfig, error) {
	accounts, err := readDockerAccounts()
	if err != nil {
		return dockerAccountConfig{}, err
	}
	account, ok := accounts[strings.TrimSpace(name)]
	if !ok {
		return dockerAccountConfig{}, fmt.Errorf("docker account not found")
	}
	return account, nil
}

func dockerLoginName(name string, account dockerAccountConfig) string {
	if user := strings.TrimSpace(account.Username); user != "" {
		return user
	}
	return name
}

// dockerHubLogin exchanges username + token for a Hub JWT, which is what the
// hub.docker.com v2 API (repositories, rate limits) authenticates with.
func dockerHubLogin(username, token string) (string, int, error) {
	payload, _ := json.Marshal(M{"username": username, "password": token})
	req, err := http.NewRequest(http.MethodPost, "https://hub.docker.com/v2/users/login", strings.NewReader(string(payload)))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "cicy-code")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, nil
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &out)
	return out.Token, resp.StatusCode, nil
}

// dockerRegistryPing does a v2 API probe with HTTP basic auth, which is how a
// non-Hub registry (ghcr, ACR, Harbor…) validates a token.
func dockerRegistryPing(registry, username, token string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, "https://"+registry+"/v2/", nil)
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(username, token)
	req.Header.Set("User-Agent", "cicy-code")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, nil
}

func handleDockerAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := readDockerAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		if name := strings.TrimSpace(r.URL.Query().Get("reveal_token")); name != "" {
			account, ok := accounts[name]
			if !ok {
				httpErr(w, http.StatusNotFound, "docker account not found")
				return
			}
			J(w, M{"api_token": account.APIToken, "2fa": account.TwoFA, "password": account.Password})
			return
		}
		type item struct {
			Name        string `json:"name"`
			Username    string `json:"username"`
			Email       string `json:"email"`
			TokenSet    bool   `json:"token_set"`
			TokenTail   string `json:"token_tail,omitempty"`
			TwoFASet    bool   `json:"2fa_set"`
			Profile     string `json:"profile"`
			PasswordSet bool   `json:"password_set"`
			Registry    string `json:"registry"`
		}
		items := make([]item, 0, len(accounts))
		for name, account := range accounts {
			items = append(items, item{
				Name:        name,
				Username:    dockerLoginName(name, account),
				Email:       account.Email,
				TokenSet:    strings.TrimSpace(account.APIToken) != "",
				TokenTail:   secretTail(account.APIToken),
				TwoFASet:    strings.TrimSpace(account.TwoFA) != "",
				Profile:     account.Profile,
				PasswordSet: strings.TrimSpace(account.Password) != "",
				Registry:    dockerRegistryOf(account),
			})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		J(w, M{"accounts": items})
	case http.MethodPut, http.MethodPost:
		var req struct {
			Name     string `json:"name"`
			OldName  string `json:"old_name"`
			Username string `json:"username"`
			Email    string `json:"email"`
			APIToken string `json:"api_token"`
			TwoFA    string `json:"2fa"`
			Profile  string `json:"profile"`
			Password string `json:"password"`
			Registry string `json:"registry"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.OldName = strings.TrimSpace(req.OldName)
		if !dockerAccountNameRE.MatchString(req.Name) {
			httpErr(w, http.StatusBadRequest, "invalid docker account name")
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
		current.Username = strings.TrimSpace(req.Username)
		current.Email = strings.TrimSpace(req.Email)
		current.TwoFA = strings.TrimSpace(req.TwoFA)
		current.Profile = strings.TrimSpace(req.Profile)
		current.Password = strings.TrimSpace(req.Password)
		current.Registry = strings.TrimSpace(req.Registry)
		if strings.TrimSpace(req.APIToken) != "" {
			current.APIToken = strings.TrimSpace(req.APIToken)
		}
		if strings.TrimSpace(current.APIToken) == "" {
			httpErr(w, http.StatusBadRequest, "api_token required for a new account")
			return
		}
		accounts[req.Name] = current
		if err := writeDockerAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true, "name": req.Name, "token_set": true, "token_tail": secretTail(current.APIToken)})
	case http.MethodDelete:
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if _, ok := accounts[name]; !ok {
			httpErr(w, http.StatusNotFound, "docker account not found")
			return
		}
		delete(accounts, name)
		if err := writeDockerAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "GET, POST, PUT or DELETE required")
	}
}

func handleDockerAccountTOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := dockerAccountByName(req.Name)
	if err != nil {
		httpErr(w, http.StatusNotFound, "docker account not found")
		return
	}
	if strings.TrimSpace(account.TwoFA) == "" {
		httpErr(w, http.StatusBadRequest, "2fa not configured")
		return
	}
	serveAccountTOTP(w, account.TwoFA)
}

func handleDockerAccountTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := dockerAccountByName(req.Name)
	if err != nil || strings.TrimSpace(account.APIToken) == "" {
		httpErr(w, http.StatusNotFound, "docker account not found")
		return
	}
	registry := dockerRegistryOf(account)
	username := dockerLoginName(strings.TrimSpace(req.Name), account)
	if dockerIsHub(registry) {
		token, status, err := dockerHubLogin(username, account.APIToken)
		if err != nil {
			httpErr(w, http.StatusBadGateway, "Docker Hub request failed")
			return
		}
		if status != http.StatusOK || token == "" {
			httpErr(w, http.StatusUnauthorized, "Docker Hub authentication failed")
			return
		}
		J(w, M{"success": true, "username": username, "registry": registry})
		return
	}
	status, err := dockerRegistryPing(registry, username, account.APIToken)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "registry request failed")
		return
	}
	if status < 200 || status >= 300 {
		httpErr(w, http.StatusUnauthorized, "registry authentication failed")
		return
	}
	J(w, M{"success": true, "username": username, "registry": registry})
}

// handleDockerAccountUsage reports what a Hub token is worth: how many
// repositories it owns, their total pulls, and the account's remaining
// anonymous-vs-authenticated pull budget.
func handleDockerAccountUsage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := dockerAccountByName(req.Name)
	if err != nil || strings.TrimSpace(account.APIToken) == "" {
		httpErr(w, http.StatusNotFound, "docker account not found")
		return
	}
	registry := dockerRegistryOf(account)
	username := dockerLoginName(strings.TrimSpace(req.Name), account)
	if !dockerIsHub(registry) {
		status, err := dockerRegistryPing(registry, username, account.APIToken)
		if err != nil || status < 200 || status >= 300 {
			httpErr(w, http.StatusUnauthorized, "registry authentication failed")
			return
		}
		J(w, M{"username": username, "registry": registry, "repositories": 0, "pulls": 0, "registry_only": true})
		return
	}
	jwt, status, err := dockerHubLogin(username, account.APIToken)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "Docker Hub request failed")
		return
	}
	if status != http.StatusOK || jwt == "" {
		httpErr(w, http.StatusUnauthorized, "Docker Hub authentication failed")
		return
	}
	var repos struct {
		Count   int `json:"count"`
		Results []struct {
			Name      string `json:"name"`
			PullCount int64  `json:"pull_count"`
			IsPrivate bool   `json:"is_private"`
		} `json:"results"`
	}
	repoURL := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/?page_size=100", url.PathEscape(username))
	repoReq, _ := http.NewRequest(http.MethodGet, repoURL, nil)
	repoReq.Header.Set("Authorization", "JWT "+jwt)
	repoReq.Header.Set("User-Agent", "cicy-code")
	client := &http.Client{Timeout: 15 * time.Second}
	if resp, err := client.Do(repoReq); err == nil {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			_ = json.Unmarshal(body, &repos)
		}
	}
	var pulls int64
	private := 0
	for _, repo := range repos.Results {
		pulls += repo.PullCount
		if repo.IsPrivate {
			private++
		}
	}
	limit, remaining := dockerHubPullLimit(username, account.APIToken)
	J(w, M{
		"username":      username,
		"registry":      registry,
		"repositories":  repos.Count,
		"private_repos": private,
		"pulls":         pulls,
		"pull_limit":    limit,
		"pull_remain":   remaining,
		"repos_partial": repos.Count > len(repos.Results),
	})
}

// dockerHubPullLimit reads the RateLimit-* headers Docker exposes on the
// ratelimitpreview image. Returns (-1, -1) when the account is unlimited or the
// probe fails — the UI simply omits the row then.
func dockerHubPullLimit(username, token string) (int, int) {
	authURL := "https://auth.docker.io/token?service=registry.docker.io&scope=repository:ratelimitpreview/test:pull"
	req, err := http.NewRequest(http.MethodGet, authURL, nil)
	if err != nil {
		return -1, -1
	}
	req.SetBasicAuth(username, token)
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return -1, -1
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return -1, -1
	}
	var auth struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(body, &auth) != nil || auth.Token == "" {
		return -1, -1
	}
	headReq, err := http.NewRequest(http.MethodHead, "https://registry-1.docker.io/v2/ratelimitpreview/test/manifests/latest", nil)
	if err != nil {
		return -1, -1
	}
	headReq.Header.Set("Authorization", "Bearer "+auth.Token)
	headResp, err := client.Do(headReq)
	if err != nil {
		return -1, -1
	}
	defer headResp.Body.Close()
	return dockerRateLimitValue(headResp.Header.Get("RateLimit-Limit")), dockerRateLimitValue(headResp.Header.Get("RateLimit-Remaining"))
}

// dockerRateLimitValue parses "100;w=21600" into 100.
func dockerRateLimitValue(header string) int {
	header = strings.TrimSpace(header)
	if header == "" {
		return -1
	}
	if idx := strings.Index(header, ";"); idx >= 0 {
		header = header[:idx]
	}
	value, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil {
		return -1
	}
	return value
}

// handleDockerAccountBind merges the credential into ~/.docker/config.json the
// same way `docker login` does, leaving other registries' entries alone.
func handleDockerAccountBind(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := dockerAccountByName(req.Name)
	if err != nil || strings.TrimSpace(account.APIToken) == "" {
		httpErr(w, http.StatusNotFound, "docker account not found")
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	registry := dockerRegistryOf(account)
	path := filepath.Join(home, ".docker", "config.json")
	if err := writeDockerConfigAuth(path, dockerAuthKeyFor(registry), dockerLoginName(strings.TrimSpace(req.Name), account), account.APIToken, account.Email); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, M{"success": true, "config": path, "registry": registry})
}

func writeDockerConfigAuth(path, authKey, username, token, email string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	config := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &config); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	auths, _ := config["auths"].(map[string]any)
	if auths == nil {
		auths = map[string]any{}
	}
	entry, _ := auths[authKey].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	entry["auth"] = base64.StdEncoding.EncodeToString([]byte(username + ":" + token))
	if strings.TrimSpace(email) != "" {
		entry["email"] = strings.TrimSpace(email)
	}
	auths[authKey] = entry
	config["auths"] = auths
	data, err := json.MarshalIndent(config, "", "\t")
	if err != nil {
		return err
	}
	return writeFileAtomic0600(path, append(data, '\n'))
}

// dockerInspectResult is what a single PAT can tell us about its owner, so the
// matrix can be filled from one paste instead of hand-typed fields.
type dockerInspectResult struct {
	Username     string   `json:"username"`
	FullName     string   `json:"full_name"`
	Email        string   `json:"email"`
	Company      string   `json:"company"`
	Registry     string   `json:"registry"`
	Orgs         []string `json:"orgs"`
	Repositories int      `json:"repositories"`
	PrivateRepos int      `json:"private_repos"`
	Pulls        int64    `json:"pulls"`
	PullLimit    int      `json:"pull_limit"`
	PullRemain   int      `json:"pull_remain"`
	Notes        []string `json:"notes"`
}

// dockerHubGet performs an authenticated hub.docker.com v2 call with the JWT
// obtained from a PAT login.
func dockerHubGet(jwt, rawURL string, out any) int {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("Authorization", "JWT "+jwt)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cicy-code")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out != nil {
		_ = json.Unmarshal(body, out)
	}
	return resp.StatusCode
}

// dockerInspectToken probes a credential. Only the login itself is mandatory:
// a registry-scoped token legitimately cannot read Hub's user API, and the
// result then carries fewer fields plus a note saying so.
func dockerInspectToken(username, token, registry string) (*dockerInspectResult, string, error) {
	registry = dockerRegistryOf(dockerAccountConfig{Registry: registry})
	username = strings.TrimSpace(username)
	token = strings.TrimSpace(token)
	result := &dockerInspectResult{Registry: registry, Username: username, PullLimit: -1, PullRemain: -1}
	if !dockerIsHub(registry) {
		if username == "" {
			return nil, "username required for a private registry", nil
		}
		status, err := dockerRegistryPing(registry, username, token)
		if err != nil {
			return nil, "", err
		}
		if status < 200 || status >= 300 {
			return nil, "registry authentication failed", nil
		}
		result.Notes = append(result.Notes, "registry_only")
		return result, "", nil
	}
	if username == "" {
		return nil, "username required to exchange a Docker Hub token", nil
	}
	jwt, status, err := dockerHubLogin(username, token)
	if err != nil {
		return nil, "", err
	}
	if status != http.StatusOK || jwt == "" {
		return nil, "Docker Hub authentication failed", nil
	}
	var profile struct {
		Username string `json:"username"`
		FullName string `json:"full_name"`
		Company  string `json:"company"`
		Email    string `json:"email"`
	}
	if dockerHubGet(jwt, "https://hub.docker.com/v2/user/", &profile) == http.StatusOK {
		if profile.Username != "" {
			result.Username = profile.Username
		}
		result.FullName = strings.TrimSpace(profile.FullName)
		result.Company = strings.TrimSpace(profile.Company)
		result.Email = strings.TrimSpace(profile.Email)
	} else {
		result.Notes = append(result.Notes, "profile")
	}
	var orgs struct {
		Results []struct {
			OrgName string `json:"orgname"`
		} `json:"results"`
	}
	if dockerHubGet(jwt, "https://hub.docker.com/v2/user/orgs/?page_size=100", &orgs) == http.StatusOK {
		for _, org := range orgs.Results {
			if org.OrgName != "" {
				result.Orgs = append(result.Orgs, org.OrgName)
			}
		}
		sort.Strings(result.Orgs)
	} else {
		result.Notes = append(result.Notes, "orgs")
	}
	var repos struct {
		Count   int `json:"count"`
		Results []struct {
			PullCount int64 `json:"pull_count"`
			IsPrivate bool  `json:"is_private"`
		} `json:"results"`
	}
	if dockerHubGet(jwt, fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/?page_size=100", url.PathEscape(result.Username)), &repos) == http.StatusOK {
		result.Repositories = repos.Count
		for _, repo := range repos.Results {
			result.Pulls += repo.PullCount
			if repo.IsPrivate {
				result.PrivateRepos++
			}
		}
	} else {
		result.Notes = append(result.Notes, "repositories")
	}
	result.PullLimit, result.PullRemain = dockerHubPullLimit(result.Username, token)
	return result, "", nil
}

// handleDockerAccountInspect fills the matrix from one pasted credential.
func handleDockerAccountInspect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Username string `json:"username"`
		APIToken string `json:"api_token"`
		Registry string `json:"registry"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	username := strings.TrimSpace(req.Username)
	token := strings.TrimSpace(req.APIToken)
	registry := strings.TrimSpace(req.Registry)
	if token == "" {
		account, err := dockerAccountByName(req.Name)
		if err != nil || strings.TrimSpace(account.APIToken) == "" {
			httpErr(w, http.StatusNotFound, "docker account not found")
			return
		}
		token = account.APIToken
		if username == "" {
			username = dockerLoginName(strings.TrimSpace(req.Name), account)
		}
		if registry == "" {
			registry = account.Registry
		}
	}
	if username == "" {
		username = strings.TrimSpace(req.Name)
	}
	result, message, err := dockerInspectToken(username, token, registry)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "Docker request failed")
		return
	}
	if result == nil {
		httpErr(w, http.StatusUnauthorized, message)
		return
	}
	J(w, result)
}
