// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Kiro account matrix — the sign-in identities used for Kiro (kiro-cli /
// Kiro IDE): which provider the account logs in with (GitHub, AWS Builder ID,
// Google, …), the email, password, 2FA seed, Chrome profile and a note. Kept in
// ~/cicy-ai/db/kiro.json (0600). The list endpoint never returns the password
// or the 2FA seed, only whether they are set. Bulk import takes one account per
// line in the form  type:email:password:2fa  (2fa optional).

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
)

type kiroAccountConfig struct {
	LoginType string `json:"login_type"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	TwoFA     string `json:"2fa"`
	Profile   string `json:"profile"`
	Note      string `json:"note"`
}

// Names default to the email, so '@' and '+' must be allowed.
var kiroAccountNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@+-]{0,127}$`)

func readKiroAccounts() (map[string]kiroAccountConfig, error) {
	return readAccountStore[kiroAccountConfig]("kiro.json")
}

func writeKiroAccounts(accounts map[string]kiroAccountConfig) error {
	return writeAccountStore("kiro.json", accounts)
}

// normalizeKiroLoginType maps the spellings people paste (awsBuildId,
// builder-id, gh…) onto the canonical values the UI shows.
func normalizeKiroLoginType(raw string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	key = strings.NewReplacer("-", "", "_", "", " ", "").Replace(key)
	switch key {
	case "github", "gh":
		return "github"
	case "google", "gmail":
		return "google"
	case "awsbuildid", "awsbuilderid", "builderid", "buildid", "aws":
		return "builder_id"
	case "identitycenter", "idc", "sso", "pro":
		return "identity_center"
	case "":
		return ""
	default:
		// Tolerate typos like "githhub" / "gihub".
		if strings.HasPrefix(key, "gith") || strings.HasPrefix(key, "gihub") {
			return "github"
		}
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

// parseKiroImportLine reads "type:email:password:2fa". The password is
// everything between the email and the last field, so a password containing ':'
// survives; with only three fields there is no 2FA.
func parseKiroImportLine(line string) (kiroAccountConfig, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return kiroAccountConfig{}, false
	}
	parts := strings.Split(line, ":")
	if len(parts) < 3 || !strings.Contains(parts[1], "@") {
		return kiroAccountConfig{}, false
	}
	account := kiroAccountConfig{LoginType: normalizeKiroLoginType(parts[0]), Email: strings.TrimSpace(parts[1])}
	if len(parts) == 3 {
		account.Password = strings.TrimSpace(parts[2])
	} else {
		account.Password = strings.TrimSpace(strings.Join(parts[2:len(parts)-1], ":"))
		account.TwoFA = strings.ReplaceAll(strings.TrimSpace(parts[len(parts)-1]), " ", "")
	}
	return account, account.Email != ""
}

func handleKiroAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := readKiroAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		if name := strings.TrimSpace(r.URL.Query().Get("reveal_token")); name != "" {
			account, ok := accounts[name]
			if !ok {
				httpErr(w, http.StatusNotFound, "kiro account not found")
				return
			}
			J(w, M{"login_type": account.LoginType, "email": account.Email, "password": account.Password, "2fa": account.TwoFA, "note": account.Note})
			return
		}
		type item struct {
			Name        string `json:"name"`
			LoginType   string `json:"login_type"`
			Email       string `json:"email"`
			PasswordSet bool   `json:"password_set"`
			TwoFASet    bool   `json:"2fa_set"`
			Profile     string `json:"profile"`
			Note        string `json:"note"`
		}
		items := make([]item, 0, len(accounts))
		for name, account := range accounts {
			items = append(items, item{
				Name:        name,
				LoginType:   account.LoginType,
				Email:       account.Email,
				PasswordSet: strings.TrimSpace(account.Password) != "",
				TwoFASet:    strings.TrimSpace(account.TwoFA) != "",
				Profile:     account.Profile,
				Note:        account.Note,
			})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		J(w, M{"accounts": items})
	case http.MethodPut, http.MethodPost:
		var req struct {
			Name      string `json:"name"`
			OldName   string `json:"old_name"`
			LoginType string `json:"login_type"`
			Email     string `json:"email"`
			Password  string `json:"password"`
			TwoFA     string `json:"2fa"`
			Profile   string `json:"profile"`
			Note      string `json:"note"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" {
			req.Name = strings.TrimSpace(req.Email)
		}
		req.OldName = strings.TrimSpace(req.OldName)
		if !kiroAccountNameRE.MatchString(req.Name) {
			httpErr(w, http.StatusBadRequest, "invalid kiro account name")
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
		current.LoginType = normalizeKiroLoginType(req.LoginType)
		current.Email = strings.TrimSpace(req.Email)
		current.Profile = strings.TrimSpace(req.Profile)
		current.Note = strings.TrimSpace(req.Note)
		// Secrets left blank on edit keep their stored value.
		if v := strings.TrimSpace(req.Password); v != "" {
			current.Password = v
		}
		if v := strings.ReplaceAll(strings.TrimSpace(req.TwoFA), " ", ""); v != "" {
			current.TwoFA = v
		}
		if current.Email == "" {
			httpErr(w, http.StatusBadRequest, "email required")
			return
		}
		accounts[req.Name] = current
		if err := writeKiroAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true, "name": req.Name})
	case http.MethodDelete:
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if _, ok := accounts[name]; !ok {
			httpErr(w, http.StatusNotFound, "kiro account not found")
			return
		}
		delete(accounts, name)
		if err := writeKiroAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "GET, POST, PUT or DELETE required")
	}
}

func handleKiroAccountTOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	accounts, err := readKiroAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	account, ok := accounts[strings.TrimSpace(req.Name)]
	if !ok {
		httpErr(w, http.StatusNotFound, "kiro account not found")
		return
	}
	if strings.TrimSpace(account.TwoFA) == "" {
		httpErr(w, http.StatusBadRequest, "2fa not configured")
		return
	}
	serveAccountTOTP(w, account.TwoFA)
}

// handleKiroAccountImport upserts one account per "type:email:password:2fa"
// line, keyed by email. Existing Chrome profile / note are kept. The response
// lists names only — never the secrets it just stored.
func handleKiroAccountImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	accounts, err := readKiroAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	added, updated, skipped := []string{}, []string{}, []int{}
	for i, line := range strings.Split(req.Text, "\n") {
		parsed, ok := parseKiroImportLine(line)
		if !ok {
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				skipped = append(skipped, i+1)
			}
			continue
		}
		name := parsed.Email
		if !kiroAccountNameRE.MatchString(name) {
			skipped = append(skipped, i+1)
			continue
		}
		if old, exists := accounts[name]; exists {
			parsed.Profile, parsed.Note = old.Profile, old.Note
			if parsed.TwoFA == "" {
				parsed.TwoFA = old.TwoFA
			}
			updated = append(updated, name)
		} else {
			added = append(added, name)
		}
		accounts[name] = parsed
	}
	if len(added)+len(updated) > 0 {
		if err := writeKiroAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	J(w, M{"success": true, "added": added, "updated": updated, "skipped_lines": skipped})
}
