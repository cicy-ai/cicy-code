// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// npm account matrix — the npm sibling of github_accounts.go. Tokens live only
// in ~/cicy-ai/db/npm.json (0600); the list endpoint returns just "is it set"
// plus the last four characters, and the only way a token leaves the host is
// the explicit ~/.npmrc bind below, which never echoes it back to the page.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type npmAccountConfig struct {
	APIToken string `json:"api_token"`
	Email    string `json:"email"`
	TwoFA    string `json:"2fa"`
	Profile  string `json:"profile"`
	Password string `json:"password"`
	Registry string `json:"registry"`
	Scope    string `json:"scope"`
}

const npmDefaultRegistry = "https://registry.npmjs.org"

var npmAccountNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

func readNpmAccounts() (map[string]npmAccountConfig, error) {
	return readAccountStore[npmAccountConfig]("npm.json")
}

func writeNpmAccounts(accounts map[string]npmAccountConfig) error {
	return writeAccountStore("npm.json", accounts)
}

// npmRegistryOf normalises a stored registry to an origin without a trailing
// slash, so both the API calls and the ~/.npmrc key derive from one value.
func npmRegistryOf(account npmAccountConfig) string {
	registry := strings.TrimSpace(account.Registry)
	if registry == "" {
		registry = npmDefaultRegistry
	}
	if !strings.Contains(registry, "://") {
		registry = "https://" + registry
	}
	return strings.TrimRight(registry, "/")
}

// npmrcKeyFor turns a registry URL into the ~/.npmrc auth key npm expects:
// "//registry.npmjs.org/:_authToken".
func npmrcKeyFor(registry string) string {
	parsed, err := url.Parse(registry)
	if err != nil || parsed.Host == "" {
		return "//registry.npmjs.org/:_authToken"
	}
	path := strings.TrimRight(parsed.Path, "/")
	return "//" + parsed.Host + path + "/:_authToken"
}

func npmAccountByName(name string) (npmAccountConfig, error) {
	accounts, err := readNpmAccounts()
	if err != nil {
		return npmAccountConfig{}, err
	}
	account, ok := accounts[strings.TrimSpace(name)]
	if !ok {
		return npmAccountConfig{}, fmt.Errorf("npm account not found")
	}
	return account, nil
}

// npmGet issues an authenticated registry request and decodes JSON on 2xx.
func npmGet(account npmAccountConfig, rawURL string, out any) (int, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	if token := strings.TrimSpace(account.APIToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cicy-code")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out != nil {
		_ = json.Unmarshal(body, out)
	}
	return resp.StatusCode, nil
}

func handleNpmAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := readNpmAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		if name := strings.TrimSpace(r.URL.Query().Get("reveal_token")); name != "" {
			account, ok := accounts[name]
			if !ok {
				httpErr(w, http.StatusNotFound, "npm account not found")
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
			Registry    string `json:"registry"`
			Scope       string `json:"scope"`
		}
		items := make([]item, 0, len(accounts))
		for name, account := range accounts {
			items = append(items, item{
				Name:        name,
				Email:       account.Email,
				TokenSet:    strings.TrimSpace(account.APIToken) != "",
				TokenTail:   secretTail(account.APIToken),
				TwoFASet:    strings.TrimSpace(account.TwoFA) != "",
				Profile:     account.Profile,
				PasswordSet: strings.TrimSpace(account.Password) != "",
				Registry:    npmRegistryOf(account),
				Scope:       account.Scope,
			})
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
			Registry string `json:"registry"`
			Scope    string `json:"scope"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.OldName = strings.TrimSpace(req.OldName)
		if !npmAccountNameRE.MatchString(req.Name) {
			httpErr(w, http.StatusBadRequest, "invalid npm account name")
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
		current.Registry = strings.TrimSpace(req.Registry)
		current.Scope = strings.TrimSpace(req.Scope)
		if strings.TrimSpace(req.APIToken) != "" {
			current.APIToken = strings.TrimSpace(req.APIToken)
		}
		if strings.TrimSpace(current.APIToken) == "" {
			httpErr(w, http.StatusBadRequest, "api_token required for a new account")
			return
		}
		accounts[req.Name] = current
		if err := writeNpmAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true, "name": req.Name, "token_set": true, "token_tail": secretTail(current.APIToken)})
	case http.MethodDelete:
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if _, ok := accounts[name]; !ok {
			httpErr(w, http.StatusNotFound, "npm account not found")
			return
		}
		delete(accounts, name)
		if err := writeNpmAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "GET, POST, PUT or DELETE required")
	}
}

func handleNpmAccountTOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := npmAccountByName(req.Name)
	if err != nil {
		httpErr(w, http.StatusNotFound, "npm account not found")
		return
	}
	if strings.TrimSpace(account.TwoFA) == "" {
		httpErr(w, http.StatusBadRequest, "2fa not configured")
		return
	}
	serveAccountTOTP(w, account.TwoFA)
}

func handleNpmAccountTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := npmAccountByName(req.Name)
	if err != nil || strings.TrimSpace(account.APIToken) == "" {
		httpErr(w, http.StatusNotFound, "npm account not found")
		return
	}
	registry := npmRegistryOf(account)
	var who struct {
		Username string `json:"username"`
	}
	status, err := npmGet(account, registry+"/-/whoami", &who)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "npm registry request failed")
		return
	}
	if status != http.StatusOK || who.Username == "" {
		httpErr(w, http.StatusUnauthorized, "npm authentication failed")
		return
	}
	J(w, M{"success": true, "username": who.Username, "registry": registry})
}

// handleNpmAccountUsage is the npm counterpart of the GitHub Actions-minutes
// row: who the token belongs to, how many packages it maintains, when the most
// recent publish was, and last-month downloads across those packages.
func handleNpmAccountUsage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := npmAccountByName(req.Name)
	if err != nil || strings.TrimSpace(account.APIToken) == "" {
		httpErr(w, http.StatusNotFound, "npm account not found")
		return
	}
	registry := npmRegistryOf(account)
	var who struct {
		Username string `json:"username"`
	}
	status, err := npmGet(account, registry+"/-/whoami", &who)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "npm registry request failed")
		return
	}
	if status != http.StatusOK || who.Username == "" {
		httpErr(w, http.StatusUnauthorized, "npm authentication failed")
		return
	}
	var search struct {
		Total   int `json:"total"`
		Objects []struct {
			Package struct {
				Name string `json:"name"`
				Date string `json:"date"`
			} `json:"package"`
		} `json:"objects"`
	}
	searchURL := fmt.Sprintf("%s/-/v1/search?text=maintainer:%s&size=250", registry, url.QueryEscape(who.Username))
	if _, err := npmGet(account, searchURL, &search); err != nil {
		httpErr(w, http.StatusBadGateway, "npm registry request failed")
		return
	}
	names := make([]string, 0, len(search.Objects))
	latest := ""
	for _, object := range search.Objects {
		if object.Package.Name == "" {
			continue
		}
		if object.Package.Date > latest {
			latest = object.Package.Date
		}
		// The bulk downloads endpoint rejects scoped names, so only unscoped
		// packages contribute to the total (reported via downloads_partial).
		if !strings.HasPrefix(object.Package.Name, "@") {
			names = append(names, object.Package.Name)
		}
	}
	downloads, partial := npmLastMonthDownloads(account, names)
	J(w, M{
		"username":           who.Username,
		"registry":           registry,
		"packages":           search.Total,
		"last_publish":       latest,
		"downloads":          downloads,
		"downloads_partial":  partial || len(names) < len(search.Objects),
		"downloads_period":   "last-month",
		"packages_truncated": search.Total > len(search.Objects),
	})
}

// npmLastMonthDownloads sums the bulk downloads endpoint over the maintainer's
// unscoped packages (128 names per request, which is the API's documented cap).
func npmLastMonthDownloads(account npmAccountConfig, names []string) (int64, bool) {
	if len(names) == 0 {
		return 0, false
	}
	var total int64
	partial := false
	for start := 0; start < len(names); start += 128 {
		end := start + 128
		if end > len(names) {
			end = len(names)
		}
		batch := names[start:end]
		var point map[string]struct {
			Downloads int64 `json:"downloads"`
		}
		endpoint := "https://api.npmjs.org/downloads/point/last-month/" + strings.Join(batch, ",")
		if len(batch) == 1 {
			// A single name returns the object directly rather than a map.
			var one struct {
				Downloads int64 `json:"downloads"`
			}
			status, err := npmGet(account, endpoint, &one)
			if err != nil || status != http.StatusOK {
				partial = true
				continue
			}
			total += one.Downloads
			continue
		}
		status, err := npmGet(account, endpoint, &point)
		if err != nil || status != http.StatusOK {
			partial = true
			continue
		}
		for _, entry := range point {
			total += entry.Downloads
		}
	}
	return total, partial
}

// handleNpmAccountBind writes the account's token into ~/.npmrc so `npm
// publish` / `npm whoami` run as that account. Every other line of the file is
// preserved and the token is never returned to the caller.
func handleNpmAccountBind(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := npmAccountByName(req.Name)
	if err != nil || strings.TrimSpace(account.APIToken) == "" {
		httpErr(w, http.StatusNotFound, "npm account not found")
		return
	}
	registry := npmRegistryOf(account)
	path, err := npmrcPath()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := writeNpmrcAuth(path, registry, account.APIToken, account.Scope); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, M{"success": true, "npmrc": path, "registry": registry, "scope": account.Scope})
}

func npmrcPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".npmrc"), nil
}

// writeNpmrcAuth replaces the auth line for one registry (and the scope's
// registry mapping, when the account declares a scope) and leaves every other
// line of ~/.npmrc untouched.
func writeNpmrcAuth(path, registry, token, scope string) error {
	authKey := npmrcKeyFor(registry)
	scopeKey := ""
	if scope = strings.TrimSpace(scope); scope != "" {
		if !strings.HasPrefix(scope, "@") {
			scope = "@" + scope
		}
		scopeKey = scope + ":registry"
	}
	lines := []string{}
	if file, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			key := strings.TrimSpace(strings.SplitN(line, "=", 2)[0])
			if key == authKey || (scopeKey != "" && key == scopeKey) {
				continue
			}
			lines = append(lines, line)
		}
		file.Close()
		if err := scanner.Err(); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if scopeKey != "" {
		lines = append(lines, scopeKey+"="+registry)
	}
	lines = append(lines, authKey+"="+token)
	return writeFileAtomic0600(path, []byte(strings.Join(lines, "\n")+"\n"))
}

// npmInspectResult is everything a bare token can tell us about its owner, so
// the matrix can be filled from a single paste instead of hand-typed fields.
type npmInspectResult struct {
	Username       string   `json:"username"`
	Email          string   `json:"email"`
	EmailVerified  bool     `json:"email_verified"`
	Fullname       string   `json:"fullname"`
	TwoFAMode      string   `json:"tfa_mode"`
	Registry       string   `json:"registry"`
	Scopes         []string `json:"scopes"`
	Packages       int      `json:"packages"`
	PrivatePkgs    int      `json:"private_packages"`
	PublicPackages int      `json:"public_packages"`
	TokenReadonly  bool     `json:"token_readonly"`
	TokenAutomatic bool     `json:"token_automation"`
	TokenCreated   string   `json:"token_created"`
	LastPublish    string   `json:"last_publish"`
	Notes          []string `json:"notes"`
}

// npmInspectToken probes the registry with one token. Every step past /-/whoami
// is best-effort: granular tokens legitimately lack access to the profile or
// token-list endpoints, and the result just carries fewer fields then.
func npmInspectToken(token, registry string) (*npmInspectResult, string, error) {
	probe := npmAccountConfig{APIToken: strings.TrimSpace(token), Registry: registry}
	base := npmRegistryOf(probe)
	result := &npmInspectResult{Registry: base}
	var who struct {
		Username string `json:"username"`
	}
	status, err := npmGet(probe, base+"/-/whoami", &who)
	if err != nil {
		return nil, "", err
	}
	if status != http.StatusOK || who.Username == "" {
		return nil, "npm authentication failed", nil
	}
	result.Username = who.Username

	// Profile: name/email/2FA mode. Granular tokens may be denied here.
	var profile struct {
		Name          string `json:"name"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Fullname      string `json:"fullname"`
		TFA           any    `json:"tfa"`
	}
	if profileStatus, err := npmGet(probe, base+"/-/npm/v1/user", &profile); err == nil && profileStatus == http.StatusOK {
		result.Email = strings.TrimSpace(profile.Email)
		result.EmailVerified = profile.EmailVerified
		result.Fullname = strings.TrimSpace(profile.Fullname)
		result.TwoFAMode = npmTwoFAMode(profile.TFA)
	} else {
		result.Notes = append(result.Notes, "profile")
	}

	// Owned packages, including private ones — the authoritative list. Falls
	// back to public search when the token cannot read it.
	scopes := map[string]bool{}
	var owned map[string]string
	if ownedStatus, err := npmGet(probe, base+"/-/user/"+url.PathEscape(who.Username)+"/package", &owned); err == nil && ownedStatus == http.StatusOK && len(owned) > 0 {
		result.Packages = len(owned)
		for name := range owned {
			if strings.HasPrefix(name, "@") {
				scopes[strings.SplitN(name, "/", 2)[0]] = true
			}
		}
	} else {
		result.Notes = append(result.Notes, "packages")
	}
	var search struct {
		Total   int `json:"total"`
		Objects []struct {
			Package struct {
				Name string `json:"name"`
				Date string `json:"date"`
			} `json:"package"`
		} `json:"objects"`
	}
	searchURL := fmt.Sprintf("%s/-/v1/search?text=maintainer:%s&size=250", base, url.QueryEscape(who.Username))
	if searchStatus, err := npmGet(probe, searchURL, &search); err == nil && searchStatus == http.StatusOK {
		result.PublicPackages = search.Total
		for _, object := range search.Objects {
			if object.Package.Date > result.LastPublish {
				result.LastPublish = object.Package.Date
			}
			if strings.HasPrefix(object.Package.Name, "@") {
				scopes[strings.SplitN(object.Package.Name, "/", 2)[0]] = true
			}
		}
	}
	if result.Packages == 0 {
		result.Packages = result.PublicPackages
	}
	if result.Packages >= result.PublicPackages {
		result.PrivatePkgs = result.Packages - result.PublicPackages
	}
	for scope := range scopes {
		result.Scopes = append(result.Scopes, scope)
	}
	sort.Strings(result.Scopes)

	// Token metadata (readonly / automation / created). Classic tokens only:
	// the list is matched by prefix because npm never returns a full token.
	var tokens struct {
		Objects []struct {
			Token      string `json:"token"`
			Key        string `json:"key"`
			Readonly   bool   `json:"readonly"`
			Automation bool   `json:"automation"`
			Created    string `json:"created"`
		} `json:"objects"`
	}
	if tokenStatus, err := npmGet(probe, base+"/-/npm/v1/tokens", &tokens); err == nil && tokenStatus == http.StatusOK {
		for _, entry := range tokens.Objects {
			if entry.Token == "" || !strings.Contains(strings.TrimSpace(token), entry.Token) {
				continue
			}
			result.TokenReadonly = entry.Readonly
			result.TokenAutomatic = entry.Automation
			result.TokenCreated = entry.Created
			break
		}
	} else {
		result.Notes = append(result.Notes, "tokens")
	}
	return result, "", nil
}

// npmTwoFAMode normalises npm's tfa field, which is `false` when disabled and
// an object ({mode, pending}) when enabled.
func npmTwoFAMode(value any) string {
	switch tfa := value.(type) {
	case map[string]any:
		mode := strings.TrimSpace(fmt.Sprint(tfa["mode"]))
		if mode == "" || mode == "<nil>" {
			return "enabled"
		}
		return mode
	case string:
		return strings.TrimSpace(tfa)
	case bool:
		if tfa {
			return "enabled"
		}
	}
	return ""
}

// handleNpmAccountInspect fills the matrix from a single pasted token: the
// caller sends either a raw token (before saving) or the name of a stored one.
func handleNpmAccountInspect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		APIToken string `json:"api_token"`
		Registry string `json:"registry"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	token := strings.TrimSpace(req.APIToken)
	registry := strings.TrimSpace(req.Registry)
	if token == "" {
		account, err := npmAccountByName(req.Name)
		if err != nil || strings.TrimSpace(account.APIToken) == "" {
			httpErr(w, http.StatusNotFound, "npm account not found")
			return
		}
		token = account.APIToken
		if registry == "" {
			registry = account.Registry
		}
	}
	result, message, err := npmInspectToken(token, registry)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "npm registry request failed")
		return
	}
	if result == nil {
		httpErr(w, http.StatusUnauthorized, message)
		return
	}
	J(w, result)
}
