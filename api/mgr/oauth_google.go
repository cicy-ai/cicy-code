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
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	googleUserURL  = "https://openidconnect.googleapis.com/v1/userinfo"
	googleRevokURL = "https://oauth2.googleapis.com/revoke"

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

var (
	googleStateMu    sync.Mutex
	googleStateStore = map[string]googleStateEntry{}
)

func googleConfigPath() string {
	return filepath.Join(cicyDBDir, "google.json")
}

func googleOAuthClientFilePath() string {
	return filepath.Join(cicyDBDir, "google_oauth_client.json")
}

// loadGoogleOAuthClient returns the shared cicy-ai OAuth client credentials.
// Returns an empty struct + false if none configured.
func loadGoogleOAuthClient() (googleOAuthClient, bool) {
	if id := strings.TrimSpace(os.Getenv("CICY_GOOGLE_OAUTH_CLIENT_ID")); id != "" {
		secret := strings.TrimSpace(os.Getenv("CICY_GOOGLE_OAUTH_CLIENT_SECRET"))
		if secret != "" {
			return googleOAuthClient{ClientID: id, ClientSecret: secret, Source: "env"}, true
		}
	}
	if data, err := os.ReadFile(googleOAuthClientFilePath()); err == nil {
		var f struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if json.Unmarshal(data, &f) == nil && f.ClientID != "" && f.ClientSecret != "" {
			return googleOAuthClient{ClientID: f.ClientID, ClientSecret: f.ClientSecret, Source: "db_file"}, true
		}
	}
	if data, err := os.ReadFile(cicyGlobalJSONPath); err == nil {
		var f map[string]any
		if json.Unmarshal(data, &f) == nil {
			id, _ := f["GMAIL_CLIENT_ID"].(string)
			secret, _ := f["GMAIL_CLIENT_SECRET"].(string)
			if id != "" && secret != "" {
				return googleOAuthClient{ClientID: id, ClientSecret: secret, Source: "global_json"}, true
			}
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
	default:
		httpErr(w, 404, "not found")
	}
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
		"connected":        state.RefreshToken != "",
		"authorized_email": state.AuthorizedEmail,
		"authorized_at":    state.AuthorizedAt,
		"has_shared_client": hasClient,
		"client_source":   client.Source,
	})
}

func googleConnect(w http.ResponseWriter, r *http.Request) {
	client, ok := loadGoogleOAuthClient()
	if !ok {
		httpErr(w, 400, "no cicy-ai shared OAuth client configured (set CICY_GOOGLE_OAUTH_CLIENT_ID/SECRET or ~/cicy-ai/db/google_oauth_client.json)")
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
