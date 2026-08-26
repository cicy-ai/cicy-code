// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Aliyun (阿里云) account matrix — AccessKey ID + AccessKey Secret pairs kept in
// ~/cicy-ai/db/aliyun.json (0600). The list endpoint returns the AccessKey ID
// (it is an identifier, not a secret) but never the secret: only whether it is
// set plus its last four characters. "Bind" writes an aliyun-cli profile.

import (
	"crypto/hmac"
	"crypto/sha1"
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

type aliyunAccountConfig struct {
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
	Region          string `json:"region"`
	Account         string `json:"account"`
	Email           string `json:"email"`
	TwoFA           string `json:"2fa"`
	Profile         string `json:"profile"`
	Password        string `json:"password"`
}

const aliyunDefaultRegion = "cn-hangzhou"

var aliyunAccountNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

func readAliyunAccounts() (map[string]aliyunAccountConfig, error) {
	return readAccountStore[aliyunAccountConfig]("aliyun.json")
}

func writeAliyunAccounts(accounts map[string]aliyunAccountConfig) error {
	return writeAccountStore("aliyun.json", accounts)
}

func aliyunRegionOf(account aliyunAccountConfig) string {
	if region := strings.TrimSpace(account.Region); region != "" {
		return region
	}
	return aliyunDefaultRegion
}

func aliyunAccountByName(name string) (aliyunAccountConfig, error) {
	accounts, err := readAliyunAccounts()
	if err != nil {
		return aliyunAccountConfig{}, err
	}
	account, ok := accounts[strings.TrimSpace(name)]
	if !ok {
		return aliyunAccountConfig{}, fmt.Errorf("aliyun account not found")
	}
	return account, nil
}

// aliyunPercentEncode is Aliyun's RFC3986 variant: the POP signature spec
// requires "+"→"%20", "*"→"%2A" and "%7E"→"~" on top of Go's escaping.
func aliyunPercentEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

// aliyunSignedQuery builds the canonical query string plus HMAC-SHA1 signature
// for an RPC-style (POP) API call. nonce/timestamp are injected by the caller so
// the signing itself stays deterministic and testable.
func aliyunSignedQuery(method, accessKeyID, accessKeySecret string, params map[string]string) string {
	canonical := map[string]string{
		"Format":           "JSON",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"AccessKeyId":      accessKeyID,
	}
	for key, value := range params {
		canonical[key] = value
	}
	keys := make([]string, 0, len(canonical))
	for key := range canonical {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, aliyunPercentEncode(key)+"="+aliyunPercentEncode(canonical[key]))
	}
	query := strings.Join(pairs, "&")
	stringToSign := method + "&" + aliyunPercentEncode("/") + "&" + aliyunPercentEncode(query)
	mac := hmac.New(sha1.New, []byte(accessKeySecret+"&"))
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return query + "&Signature=" + aliyunPercentEncode(signature)
}

// aliyunCall issues a signed RPC request and returns the decoded body plus the
// API's own error code, which is far more useful than a bare 400.
func aliyunCall(account aliyunAccountConfig, endpoint string, params map[string]string, out any) (int, string, error) {
	full := map[string]string{
		"SignatureNonce": strconv.FormatInt(time.Now().UnixNano(), 36),
		"Timestamp":      time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	for key, value := range params {
		full[key] = value
	}
	query := aliyunSignedQuery(http.MethodGet, account.AccessKeyID, account.AccessKeySecret, full)
	req, err := http.NewRequest(http.MethodGet, "https://"+endpoint+"/?"+query, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cicy-code")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out != nil {
			_ = json.Unmarshal(body, out)
		}
		return resp.StatusCode, "", nil
	}
	var apiErr struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	_ = json.Unmarshal(body, &apiErr)
	message := strings.TrimSpace(apiErr.Message)
	if message == "" {
		message = strings.TrimSpace(apiErr.Code)
	}
	return resp.StatusCode, message, nil
}

func handleAliyunAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := readAliyunAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		if name := strings.TrimSpace(r.URL.Query().Get("reveal_token")); name != "" {
			account, ok := accounts[name]
			if !ok {
				httpErr(w, http.StatusNotFound, "aliyun account not found")
				return
			}
			J(w, M{"access_key_id": account.AccessKeyID, "access_key_secret": account.AccessKeySecret, "2fa": account.TwoFA, "password": account.Password})
			return
		}
		type item struct {
			Name        string `json:"name"`
			AccessKeyID string `json:"access_key_id"`
			SecretSet   bool   `json:"secret_set"`
			SecretTail  string `json:"secret_tail,omitempty"`
			Region      string `json:"region"`
			Account     string `json:"account"`
			Email       string `json:"email"`
			TwoFASet    bool   `json:"2fa_set"`
			Profile     string `json:"profile"`
			PasswordSet bool   `json:"password_set"`
		}
		items := make([]item, 0, len(accounts))
		for name, account := range accounts {
			items = append(items, item{
				Name:        name,
				AccessKeyID: account.AccessKeyID,
				SecretSet:   strings.TrimSpace(account.AccessKeySecret) != "",
				SecretTail:  secretTail(account.AccessKeySecret),
				Region:      aliyunRegionOf(account),
				Account:     account.Account,
				Email:       account.Email,
				TwoFASet:    strings.TrimSpace(account.TwoFA) != "",
				Profile:     account.Profile,
				PasswordSet: strings.TrimSpace(account.Password) != "",
			})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		J(w, M{"accounts": items})
	case http.MethodPut, http.MethodPost:
		var req struct {
			Name            string `json:"name"`
			OldName         string `json:"old_name"`
			AccessKeyID     string `json:"access_key_id"`
			AccessKeySecret string `json:"access_key_secret"`
			Region          string `json:"region"`
			Account         string `json:"account"`
			Email           string `json:"email"`
			TwoFA           string `json:"2fa"`
			Profile         string `json:"profile"`
			Password        string `json:"password"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.OldName = strings.TrimSpace(req.OldName)
		if !aliyunAccountNameRE.MatchString(req.Name) {
			httpErr(w, http.StatusBadRequest, "invalid aliyun account name")
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
		current.Region = strings.TrimSpace(req.Region)
		current.Account = strings.TrimSpace(req.Account)
		current.Email = strings.TrimSpace(req.Email)
		current.TwoFA = strings.TrimSpace(req.TwoFA)
		current.Profile = strings.TrimSpace(req.Profile)
		current.Password = strings.TrimSpace(req.Password)
		if strings.TrimSpace(req.AccessKeyID) != "" {
			current.AccessKeyID = strings.TrimSpace(req.AccessKeyID)
		}
		if strings.TrimSpace(req.AccessKeySecret) != "" {
			current.AccessKeySecret = strings.TrimSpace(req.AccessKeySecret)
		}
		if strings.TrimSpace(current.AccessKeyID) == "" || strings.TrimSpace(current.AccessKeySecret) == "" {
			httpErr(w, http.StatusBadRequest, "access_key_id and access_key_secret required")
			return
		}
		accounts[req.Name] = current
		if err := writeAliyunAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true, "name": req.Name, "secret_set": true, "secret_tail": secretTail(current.AccessKeySecret)})
	case http.MethodDelete:
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if _, ok := accounts[name]; !ok {
			httpErr(w, http.StatusNotFound, "aliyun account not found")
			return
		}
		delete(accounts, name)
		if err := writeAliyunAccounts(accounts); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "GET, POST, PUT or DELETE required")
	}
}

func handleAliyunAccountTOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := aliyunAccountByName(req.Name)
	if err != nil {
		httpErr(w, http.StatusNotFound, "aliyun account not found")
		return
	}
	if strings.TrimSpace(account.TwoFA) == "" {
		httpErr(w, http.StatusBadRequest, "2fa not configured")
		return
	}
	serveAccountTOTP(w, account.TwoFA)
}

// aliyunCallerIdentity is STS GetCallerIdentity — the AccessKey whoami. Every
// AccessKey can call it regardless of RAM policy, so it is the honest test.
func aliyunCallerIdentity(account aliyunAccountConfig) (M, string, error) {
	var identity struct {
		AccountID    string `json:"AccountId"`
		UserID       string `json:"UserId"`
		Arn          string `json:"Arn"`
		IdentityType string `json:"IdentityType"`
	}
	status, message, err := aliyunCall(account, "sts.aliyuncs.com", map[string]string{
		"Action":  "GetCallerIdentity",
		"Version": "2015-04-01",
	}, &identity)
	if err != nil {
		return nil, "", err
	}
	if status < 200 || status >= 300 {
		if message == "" {
			message = "AccessKey rejected"
		}
		return nil, message, nil
	}
	return M{"account_id": identity.AccountID, "user_id": identity.UserID, "arn": identity.Arn, "identity_type": identity.IdentityType}, "", nil
}

func handleAliyunAccountTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := aliyunAccountByName(req.Name)
	if err != nil || strings.TrimSpace(account.AccessKeySecret) == "" {
		httpErr(w, http.StatusNotFound, "aliyun account not found")
		return
	}
	identity, message, err := aliyunCallerIdentity(account)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "Aliyun request failed")
		return
	}
	if identity == nil {
		httpErr(w, http.StatusUnauthorized, message)
		return
	}
	identity["success"] = true
	identity["region"] = aliyunRegionOf(account)
	J(w, identity)
}

// handleAliyunAccountUsage pairs the AccessKey's identity with the account
// balance (BSS OpenAPI), which is the number worth watching per key. A key
// without billing permission still returns its identity.
func handleAliyunAccountUsage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := aliyunAccountByName(req.Name)
	if err != nil || strings.TrimSpace(account.AccessKeySecret) == "" {
		httpErr(w, http.StatusNotFound, "aliyun account not found")
		return
	}
	identity, message, err := aliyunCallerIdentity(account)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "Aliyun request failed")
		return
	}
	if identity == nil {
		httpErr(w, http.StatusUnauthorized, message)
		return
	}
	var balance struct {
		Data struct {
			AvailableAmount  string `json:"AvailableAmount"`
			Currency         string `json:"Currency"`
			CreditAmount     string `json:"CreditAmount"`
			AvailableCashAmt string `json:"AvailableCashAmount"`
		} `json:"Data"`
	}
	balanceStatus, balanceMessage, _ := aliyunCall(account, "business.aliyuncs.com", map[string]string{
		"Action":  "QueryAccountBalance",
		"Version": "2017-12-14",
	}, &balance)
	identity["region"] = aliyunRegionOf(account)
	identity["balance"] = strings.TrimSpace(balance.Data.AvailableAmount)
	identity["currency"] = strings.TrimSpace(balance.Data.Currency)
	identity["credit"] = strings.TrimSpace(balance.Data.CreditAmount)
	identity["balance_available"] = balanceStatus >= 200 && balanceStatus < 300
	if balanceStatus < 200 || balanceStatus >= 300 {
		identity["balance_error"] = balanceMessage
	}
	J(w, identity)
}

// handleAliyunAccountBind upserts an aliyun-cli AK profile and makes it current,
// so `aliyun ecs ...` on this host runs as that account.
func handleAliyunAccountBind(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	account, err := aliyunAccountByName(req.Name)
	if err != nil || strings.TrimSpace(account.AccessKeySecret) == "" {
		httpErr(w, http.StatusNotFound, "aliyun account not found")
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	path := filepath.Join(home, ".aliyun", "config.json")
	if err := writeAliyunCLIProfile(path, strings.TrimSpace(req.Name), account); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, M{"success": true, "config": path, "profile": strings.TrimSpace(req.Name), "region": aliyunRegionOf(account)})
}

func writeAliyunCLIProfile(path, name string, account aliyunAccountConfig) error {
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
	profile := map[string]any{
		"name":              name,
		"mode":              "AK",
		"access_key_id":     account.AccessKeyID,
		"access_key_secret": account.AccessKeySecret,
		"region_id":         aliyunRegionOf(account),
		"output_format":     "json",
		"language":          "zh",
	}
	existing, _ := config["profiles"].([]any)
	profiles := make([]any, 0, len(existing)+1)
	replaced := false
	for _, entry := range existing {
		item, ok := entry.(map[string]any)
		if ok && strings.TrimSpace(fmt.Sprint(item["name"])) == name {
			for key, value := range profile {
				item[key] = value
			}
			profiles = append(profiles, item)
			replaced = true
			continue
		}
		profiles = append(profiles, entry)
	}
	if !replaced {
		profiles = append(profiles, profile)
	}
	config["profiles"] = profiles
	config["current"] = name
	data, err := json.MarshalIndent(config, "", "\t")
	if err != nil {
		return err
	}
	return writeFileAtomic0600(path, append(data, '\n'))
}

// aliyunInspectResult is what one AccessKey pair can tell us about itself, so
// the matrix can be filled from a paste instead of hand-typed fields.
type aliyunInspectResult struct {
	AccountID    string   `json:"account_id"`
	UserID       string   `json:"user_id"`
	Arn          string   `json:"arn"`
	IdentityType string   `json:"identity_type"`
	UserName     string   `json:"user_name"`
	DisplayName  string   `json:"display_name"`
	Email        string   `json:"email"`
	Region       string   `json:"region"`
	Balance      string   `json:"balance"`
	Currency     string   `json:"currency"`
	Notes        []string `json:"notes"`
}

// aliyunArnUserName pulls the RAM user (or role) name out of an ARN such as
// "acs:ram::1799:user/cicy-dev".
func aliyunArnUserName(arn string) string {
	if idx := strings.LastIndex(arn, "/"); idx >= 0 && idx+1 < len(arn) {
		return arn[idx+1:]
	}
	return ""
}

// aliyunInspectKey probes an AccessKey. GetCallerIdentity always works for a
// valid key; RAM and billing lookups are best-effort and noted when denied.
func aliyunInspectKey(accessKeyID, accessKeySecret, region string) (*aliyunInspectResult, string, error) {
	account := aliyunAccountConfig{AccessKeyID: strings.TrimSpace(accessKeyID), AccessKeySecret: strings.TrimSpace(accessKeySecret), Region: region}
	identity, message, err := aliyunCallerIdentity(account)
	if err != nil {
		return nil, "", err
	}
	if identity == nil {
		return nil, message, nil
	}
	result := &aliyunInspectResult{
		AccountID:    fmt.Sprint(identity["account_id"]),
		UserID:       fmt.Sprint(identity["user_id"]),
		Arn:          fmt.Sprint(identity["arn"]),
		IdentityType: fmt.Sprint(identity["identity_type"]),
		Region:       aliyunRegionOf(account),
	}
	result.UserName = aliyunArnUserName(result.Arn)

	// RAM profile (display name / email) — only a key with ram:GetUser can read
	// it, and a root-account key has no RAM user at all.
	if result.UserName != "" && strings.EqualFold(result.IdentityType, "RAMUser") {
		var user struct {
			User struct {
				UserName    string `json:"UserName"`
				DisplayName string `json:"DisplayName"`
				Email       string `json:"Email"`
			} `json:"User"`
		}
		status, _, _ := aliyunCall(account, "ram.aliyuncs.com", map[string]string{
			"Action":   "GetUser",
			"Version":  "2015-05-01",
			"UserName": result.UserName,
		}, &user)
		if status >= 200 && status < 300 && user.User.UserName != "" {
			result.UserName = user.User.UserName
			result.DisplayName = strings.TrimSpace(user.User.DisplayName)
			result.Email = strings.TrimSpace(user.User.Email)
		} else {
			result.Notes = append(result.Notes, "ram")
		}
	}

	var balance struct {
		Data struct {
			AvailableAmount string `json:"AvailableAmount"`
			Currency        string `json:"Currency"`
		} `json:"Data"`
	}
	status, _, _ := aliyunCall(account, "business.aliyuncs.com", map[string]string{
		"Action":  "QueryAccountBalance",
		"Version": "2017-12-14",
	}, &balance)
	if status >= 200 && status < 300 {
		result.Balance = strings.TrimSpace(balance.Data.AvailableAmount)
		result.Currency = strings.TrimSpace(balance.Data.Currency)
	} else {
		result.Notes = append(result.Notes, "billing")
	}
	return result, "", nil
}

// handleAliyunAccountInspect fills the matrix from one pasted AccessKey pair.
func handleAliyunAccountInspect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string `json:"name"`
		AccessKeyID     string `json:"access_key_id"`
		AccessKeySecret string `json:"access_key_secret"`
		Region          string `json:"region"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	id := strings.TrimSpace(req.AccessKeyID)
	secret := strings.TrimSpace(req.AccessKeySecret)
	region := strings.TrimSpace(req.Region)
	if id == "" || secret == "" {
		account, err := aliyunAccountByName(req.Name)
		if err != nil || strings.TrimSpace(account.AccessKeySecret) == "" {
			httpErr(w, http.StatusNotFound, "aliyun account not found")
			return
		}
		if id == "" {
			id = account.AccessKeyID
		}
		if secret == "" {
			secret = account.AccessKeySecret
		}
		if region == "" {
			region = account.Region
		}
	}
	result, message, err := aliyunInspectKey(id, secret, region)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "Aliyun request failed")
		return
	}
	if result == nil {
		httpErr(w, http.StatusUnauthorized, message)
		return
	}
	J(w, result)
}
