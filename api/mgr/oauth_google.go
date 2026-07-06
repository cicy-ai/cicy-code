// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Google OAuth flow for the skill marketplace "Connect Google" button.
//
// Flow (Desktop OAuth client — Google auto-permits http://localhost:* and
// http://127.0.0.1:* for this type, so we don't need to pre-register every
// cicy-code container port):
//
//   1. POST /api/skill-config/google/connect
//      → server generates state, returns Google authorization URL
//   2. user clicks the URL → Google consent page → user clicks Allow
//   3. Google redirects to /api/skill-config/google/callback?code&state
//      → server exchanges code → fetches user email → saves to
//        ~/cicy-ai/db/google.json → returns a self-closing HTML page
//   4. front-end polls /api/skill-config/google to detect "connected"
//
// Credentials lookup order (client_id + client_secret):
//   a) env CICY_GOOGLE_OAUTH_CLIENT_ID / CICY_GOOGLE_OAUTH_CLIENT_SECRET
//   b) ~/cicy-ai/db/google_oauth_client.json {client_id, client_secret}
//   c) ~/cicy-ai/global.json keys GMAIL_CLIENT_ID / GMAIL_CLIENT_SECRET
//      (legacy, for back-compat with existing get-token.js workflow)

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	googleAuthURL       = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL      = "https://oauth2.googleapis.com/token"
	googleUserURL       = "https://openidconnect.googleapis.com/v1/userinfo"
	googleRevokURL      = "https://oauth2.googleapis.com/revoke"
	googleDeviceCodeURL = "https://oauth2.googleapis.com/device/code"

	googleStateTTL = 5 * time.Minute
)

var googleOAuthScopes = []string{
	"https://mail.google.com/",
	"https://www.googleapis.com/auth/spreadsheets",
	"https://www.googleapis.com/auth/drive",
	"https://www.googleapis.com/auth/calendar",
	"openid",
	"email",
}

type googleOAuthClient struct {
	ClientID     string
	ClientSecret string
	Source       string // "env" | "db_file" | "global_json"
}

type googleStateEntry struct {
	Client      googleOAuthClient
	RedirectURI string
	ExpiresAt   time.Time
}

type googleDeviceEntry struct {
	Client     googleOAuthClient
	DeviceCode string
	Interval   int
	ExpiresAt  time.Time
}

var (
	googleStateMu    sync.Mutex
	googleStateStore = map[string]googleStateEntry{}

	googleDeviceMu    sync.Mutex
	googleDeviceStore = map[string]googleDeviceEntry{}
)

func googleConfigPath() string {
	return filepath.Join(cicyDBDir, "google.json")
}

func googleOAuthClientFilePath() string {
	return filepath.Join(cicyDBDir, "google_oauth_client.json")
}

// loadGoogleOAuthClient returns the shared cicy-ai OAuth client credentials.
// Source order:
//  1. env CICY_GOOGLE_OAUTH_CLIENT_ID (+ optional CICY_GOOGLE_OAUTH_CLIENT_SECRET)
//  2. ~/cicy-ai/db/google_oauth_client.json {client_id, client_secret}
//
// For the Device Authorization Grant the client_secret may be empty (TV /
// Limited Input clients are public). The legacy global.json GMAIL_* keys are
// intentionally NOT consulted — those refer to the old Web-application client.
func loadGoogleOAuthClient() (googleOAuthClient, bool) {
	if id := strings.TrimSpace(os.Getenv("CICY_GOOGLE_OAUTH_CLIENT_ID")); id != "" {
		secret := strings.TrimSpace(os.Getenv("CICY_GOOGLE_OAUTH_CLIENT_SECRET"))
		return googleOAuthClient{ClientID: id, ClientSecret: secret, Source: "env"}, true
	}
	if data, err := os.ReadFile(googleOAuthClientFilePath()); err == nil {
		var f struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if json.Unmarshal(data, &f) == nil && f.ClientID != "" {
			return googleOAuthClient{ClientID: f.ClientID, ClientSecret: f.ClientSecret, Source: "db_file"}, true
		}
	}
	return googleOAuthClient{}, false
}

type googleAuthState struct {
	RefreshToken     string `json:"refresh_token,omitempty"`
	AuthorizedEmail  string `json:"authorized_email,omitempty"`
	AuthorizedAt     string `json:"authorized_at,omitempty"`
	ClientKind       string `json:"client_kind,omitempty"` // "shared"
	ClientIDSnapshot string `json:"client_id,omitempty"`   // for traceability
}

func loadGoogleAuthState() (googleAuthState, error) {
	var state googleAuthState
	data, err := os.ReadFile(googleConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if len(data) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	return state, nil
}

func saveGoogleAuthState(state googleAuthState) error {
	if err := os.MkdirAll(cicyDBDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(cicyDBDir, ".google.json.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, googleConfigPath())
}

func randomStateToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func detectRedirectURI(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	return fmt.Sprintf("%s://%s/api/skill-config/google/callback", scheme, host)
}

func putGoogleState(state string, entry googleStateEntry) {
	googleStateMu.Lock()
	defer googleStateMu.Unlock()
	// opportunistic GC: drop expired entries
	now := time.Now()
	for k, v := range googleStateStore {
		if v.ExpiresAt.Before(now) {
			delete(googleStateStore, k)
		}
	}
	googleStateStore[state] = entry
}

func takeGoogleState(state string) (googleStateEntry, bool) {
	googleStateMu.Lock()
	defer googleStateMu.Unlock()
	entry, ok := googleStateStore[state]
	if !ok {
		return googleStateEntry{}, false
	}
	delete(googleStateStore, state)
	if entry.ExpiresAt.Before(time.Now()) {
		return googleStateEntry{}, false
	}
	return entry, true
}

// ── HTTP handlers ────────────────────────────────────────────────────────────

// handleGoogleSkillConfig serves the authed endpoints (status / connect /
// disconnect). The OAuth callback is on a separate route registered without
// auth — Google's redirect won't carry our Bearer token.
func handleGoogleSkillConfig(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/skill-config/google")
	switch {
	case path == "" || path == "/":
		switch r.Method {
		case "GET":
			googleStatus(w, r)
		case "DELETE":
			googleDisconnect(w, r)
		default:
			httpErr(w, 405, "method not allowed")
		}
	case path == "/connect":
		if r.Method != "POST" {
			httpErr(w, 405, "method not allowed")
			return
		}
		googleConnect(w, r)
	case path == "/device-connect":
		if r.Method != "POST" {
			httpErr(w, 405, "method not allowed")
			return
		}
		googleDeviceConnect(w, r)
	case path == "/device-poll":
		if r.Method != "POST" {
			httpErr(w, 405, "method not allowed")
			return
		}
		googleDevicePoll(w, r)
	default:
		httpErr(w, 404, "not found")
	}
}

func putGoogleDevice(state string, entry googleDeviceEntry) {
	googleDeviceMu.Lock()
	defer googleDeviceMu.Unlock()
	now := time.Now()
	for k, v := range googleDeviceStore {
		if v.ExpiresAt.Before(now) {
			delete(googleDeviceStore, k)
		}
	}
	googleDeviceStore[state] = entry
}

func getGoogleDevice(state string) (googleDeviceEntry, bool) {
	googleDeviceMu.Lock()
	defer googleDeviceMu.Unlock()
	entry, ok := googleDeviceStore[state]
	if !ok {
		return googleDeviceEntry{}, false
	}
	if entry.ExpiresAt.Before(time.Now()) {
		delete(googleDeviceStore, state)
		return googleDeviceEntry{}, false
	}
	return entry, true
}

func dropGoogleDevice(state string) {
	googleDeviceMu.Lock()
	defer googleDeviceMu.Unlock()
	delete(googleDeviceStore, state)
}

// googleDeviceConnect kicks off the OAuth 2.0 Device Authorization Grant.
// Requires a "TV and Limited Input devices" OAuth client (Web app clients
// will be rejected by Google with "invalid_client" / "unauthorized_client").
func googleDeviceConnect(w http.ResponseWriter, _ *http.Request) {
	client, ok := loadGoogleOAuthClient()
	if !ok {
		httpErr(w, 400, "no OAuth client configured — create ~/cicy-ai/db/google_oauth_client.json with {\"client_id\":\"<TV/Limited-Input client_id>\",\"client_secret\":\"\"}")
		return
	}
	form := url.Values{}
	form.Set("client_id", client.ClientID)
	form.Set("scope", strings.Join(googleOAuthScopes, " "))
	resp, err := http.PostForm(googleDeviceCodeURL, form)
	if err != nil {
		httpErr(w, 502, "device/code request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		httpErr(w, 502, fmt.Sprintf("device/code %d: %s (the OAuth client may not be of type 'TV and Limited Input devices')", resp.StatusCode, body))
		return
	}
	var dr struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURL         string `json:"verification_url"`
		VerificationURLComplete string `json:"verification_url_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &dr); err != nil || dr.DeviceCode == "" {
		httpErr(w, 502, "parse device/code response: "+err.Error())
		return
	}
	interval := dr.Interval
	if interval <= 0 {
		interval = 5
	}
	state := randomStateToken()
	putGoogleDevice(state, googleDeviceEntry{
		Client:     client,
		DeviceCode: dr.DeviceCode,
		Interval:   interval,
		ExpiresAt:  time.Now().Add(time.Duration(dr.ExpiresIn) * time.Second),
	})
	if dr.VerificationURLComplete == "" && dr.VerificationURL != "" && dr.UserCode != "" {
		dr.VerificationURLComplete = dr.VerificationURL + "?user_code=" + url.QueryEscape(dr.UserCode)
	}
	J(w, M{
		"state":                     state,
		"user_code":                 dr.UserCode,
		"verification_url":          dr.VerificationURL,
		"verification_url_complete": dr.VerificationURLComplete,
		"expires_in":                dr.ExpiresIn,
		"interval":                  interval,
	})
}

// googleDevicePoll exchanges the stored device_code for tokens. Returns one of:
//   - {connected:true, email}     — success
//   - {pending:true}              — user hasn't approved yet (caller retries)
//   - {error:"slow_down"}         — caller should back off
//   - {error:"access_denied"}     — user denied, stop
//   - {error:"expired_token"}     — device_code expired, restart
func googleDevicePoll(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		body, _ := io.ReadAll(r.Body)
		var p struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal(body, &p)
		state = p.State
	}
	if state == "" {
		httpErr(w, 400, "missing state")
		return
	}
	entry, ok := getGoogleDevice(state)
	if !ok {
		httpErr(w, 410, "expired or unknown state — please restart authorization")
		return
	}
	form := url.Values{}
	form.Set("client_id", entry.Client.ClientID)
	if entry.Client.ClientSecret != "" {
		form.Set("client_secret", entry.Client.ClientSecret)
	}
	form.Set("device_code", entry.DeviceCode)
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	resp, err := http.PostForm(googleTokenURL, form)
	if err != nil {
		httpErr(w, 502, "token poll failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		switch e.Error {
		case "authorization_pending":
			J(w, M{"pending": true, "interval": entry.Interval})
			return
		case "slow_down":
			J(w, M{"pending": true, "interval": entry.Interval + 5, "error": "slow_down"})
			return
		case "access_denied", "expired_token":
			dropGoogleDevice(state)
			J(w, M{"error": e.Error})
			return
		default:
			J(w, M{"error": fmt.Sprintf("%s: %s", e.Error, body)})
			return
		}
	}
	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokens); err != nil {
		httpErr(w, 502, "parse token response: "+err.Error())
		return
	}
	if tokens.RefreshToken == "" {
		J(w, M{"error": "Google returned no refresh_token — disconnect and retry"})
		return
	}
	dropGoogleDevice(state)
	email := fetchGoogleUserEmail(tokens.AccessToken)
	authState := googleAuthState{
		RefreshToken:     tokens.RefreshToken,
		AuthorizedEmail:  email,
		AuthorizedAt:     time.Now().UTC().Format(time.RFC3339),
		ClientKind:       "shared",
		ClientIDSnapshot: entry.Client.ClientID,
	}
	if err := saveGoogleAuthState(authState); err != nil {
		httpErr(w, 500, "save google.json: "+err.Error())
		return
	}
	log.Printf("[google-oauth] device-connected: %s (client source: %s)", email, entry.Client.Source)
	J(w, M{"connected": true, "authorized_email": email})
}

// handleGoogleSkillCallback is mounted WITHOUT auth — Google's redirect won't
// carry our Bearer token. Security is enforced by the random state token
// generated in /connect (must round-trip and match a recently-issued one).
func handleGoogleSkillCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	googleCallback(w, r)
}

func googleStatus(w http.ResponseWriter, _ *http.Request) {
	client, hasClient := loadGoogleOAuthClient()
	state, _ := loadGoogleAuthState()
	J(w, M{
		"connected":         state.RefreshToken != "",
		"authorized_email":  state.AuthorizedEmail,
		"authorized_at":     state.AuthorizedAt,
		"has_shared_client": hasClient,
		"client_source":     client.Source,
	})
}

func googleConnect(w http.ResponseWriter, r *http.Request) {
	client, ok := loadGoogleOAuthClient()
	if !ok {
		httpErr(w, 400, "no OAuth client configured — create ~/cicy-ai/db/google_oauth_client.json with {\"client_id\":\"<TV/Limited-Input client_id>\",\"client_secret\":\"\"}")
		return
	}
	redirectURI := detectRedirectURI(r)
	state := randomStateToken()
	putGoogleState(state, googleStateEntry{
		Client:      client,
		RedirectURI: redirectURI,
		ExpiresAt:   time.Now().Add(googleStateTTL),
	})
	q := url.Values{}
	q.Set("client_id", client.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(googleOAuthScopes, " "))
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("state", state)
	J(w, M{
		"auth_url": googleAuthURL + "?" + q.Encode(),
		"state":    state,
	})
}

func googleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	stateToken := r.URL.Query().Get("state")
	errParam := r.URL.Query().Get("error")
	if errParam != "" {
		writeGoogleCallbackHTML(w, false, "Google denied: "+errParam)
		return
	}
	if code == "" || stateToken == "" {
		writeGoogleCallbackHTML(w, false, "missing code or state")
		return
	}
	entry, ok := takeGoogleState(stateToken)
	if !ok {
		writeGoogleCallbackHTML(w, false, "expired or unknown state — please retry")
		return
	}

	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", entry.Client.ClientID)
	form.Set("client_secret", entry.Client.ClientSecret)
	form.Set("redirect_uri", entry.RedirectURI)
	form.Set("grant_type", "authorization_code")

	resp, err := http.PostForm(googleTokenURL, form)
	if err != nil {
		writeGoogleCallbackHTML(w, false, "token exchange request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		writeGoogleCallbackHTML(w, false, fmt.Sprintf("token exchange %d: %s", resp.StatusCode, body))
		return
	}
	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokens); err != nil {
		writeGoogleCallbackHTML(w, false, "parse token response: "+err.Error())
		return
	}
	if tokens.RefreshToken == "" {
		writeGoogleCallbackHTML(w, false, "Google returned no refresh_token (consent screen may have remembered a prior grant — try Disconnect then Authorize again)")
		return
	}

	email := fetchGoogleUserEmail(tokens.AccessToken)
	authState := googleAuthState{
		RefreshToken:     tokens.RefreshToken,
		AuthorizedEmail:  email,
		AuthorizedAt:     time.Now().UTC().Format(time.RFC3339),
		ClientKind:       "shared",
		ClientIDSnapshot: entry.Client.ClientID,
	}
	if err := saveGoogleAuthState(authState); err != nil {
		writeGoogleCallbackHTML(w, false, "save google.json: "+err.Error())
		return
	}
	log.Printf("[google-oauth] connected: %s (client source: %s)", email, entry.Client.Source)
	writeGoogleCallbackHTML(w, true, email)
}

func fetchGoogleUserEmail(accessToken string) string {
	req, err := http.NewRequest("GET", googleUserURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	body, _ := io.ReadAll(resp.Body)
	var u struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(body, &u)
	return u.Email
}

func googleDisconnect(w http.ResponseWriter, _ *http.Request) {
	state, err := loadGoogleAuthState()
	if err == nil && state.RefreshToken != "" {
		go func(tok string) {
			form := url.Values{}
			form.Set("token", tok)
			resp, err := http.PostForm(googleRevokURL, form)
			if err != nil {
				log.Printf("[google-oauth] revoke failed: %v", err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 300 {
				body, _ := io.ReadAll(resp.Body)
				log.Printf("[google-oauth] revoke status %d: %s", resp.StatusCode, body)
			}
		}(state.RefreshToken)
	}
	if err := os.Remove(googleConfigPath()); err != nil && !os.IsNotExist(err) {
		httpErr(w, 500, "remove google.json: "+err.Error())
		return
	}
	J(w, M{"ok": true})
}

func writeGoogleCallbackHTML(w http.ResponseWriter, success bool, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	tone := "#16a34a"
	title := "Google connected"
	if !success {
		tone = "#dc2626"
		title = "Connection failed"
	}
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title></head>
<body style="background:#0a0a0a;color:#e4e4e7;font-family:-apple-system,system-ui,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0">
<div style="text-align:center;padding:2rem">
<div style="font-size:48px;line-height:1">%s</div>
<div style="font-size:18px;font-weight:600;color:%s;margin-top:.75rem">%s</div>
<div style="font-size:13px;color:#9ca3af;margin-top:.5rem;max-width:420px;word-break:break-word">%s</div>
<div style="font-size:11px;color:#52525b;margin-top:1.5rem">You can close this tab.</div>
</div>
<script>setTimeout(()=>{try{window.close()}catch(e){}},800)</script>
</body></html>`,
		htmlEscape(title),
		map[bool]string{true: "✓", false: "✕"}[success],
		tone,
		htmlEscape(title),
		htmlEscape(detail),
	)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
}
