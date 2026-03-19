package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func getJWTSecret() string {
	s := os.Getenv("JWT_SECRET")
	if s == "" {
		s = "cicy-dev-secret"
	}
	return s
}

func githubEnabled() bool { return os.Getenv("GITHUB_CLIENT_ID") != "" }
func googleEnabled() bool { return os.Getenv("GOOGLE_CLIENT_ID") != "" }

// Email whitelist — only these can login via OAuth
var allowedEmails = map[string]bool{
	"w3c.offical@gmail.com": true,
	"cicybot@icloud.com":    true,
}

func emailAllowed(email string) bool {
	return allowedEmails[strings.ToLower(email)]
}

// GET /api/auth/github
func handleGithubAuth(w http.ResponseWriter, r *http.Request) {
	u := fmt.Sprintf("https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=user:email",
		os.Getenv("GITHUB_CLIENT_ID"), url.QueryEscape(os.Getenv("OAUTH_REDIRECT_BASE")+"/api/auth/github/callback"))
	http.Redirect(w, r, u, 302)
}

// GET /api/auth/google
func handleGoogleAuth(w http.ResponseWriter, r *http.Request) {
	u := fmt.Sprintf("https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=email+profile&access_type=online",
		os.Getenv("GOOGLE_CLIENT_ID"), url.QueryEscape(os.Getenv("OAUTH_REDIRECT_BASE")+"/api/auth/google/callback"))
	http.Redirect(w, r, u, 302)
}

// GET /api/auth/google/callback
func handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", 400)
		return
	}

	// Exchange code for token
	data := url.Values{
		"code":          {code},
		"client_id":     {os.Getenv("GOOGLE_CLIENT_ID")},
		"client_secret": {os.Getenv("GOOGLE_CLIENT_SECRET")},
		"redirect_uri":  {os.Getenv("OAUTH_REDIRECT_BASE") + "/api/auth/google/callback"},
		"grant_type":    {"authorization_code"},
	}
	resp, err := http.PostForm("https://oauth2.googleapis.com/token", data)
	if err != nil {
		http.Error(w, "token exchange failed", 500)
		return
	}
	defer resp.Body.Close()

	var tok struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(resp.Body).Decode(&tok)
	if tok.AccessToken == "" {
		http.Error(w, "no access token", 500)
		return
	}

	// Get user info
	req, _ := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "failed to get user info", 500)
		return
	}
	defer resp2.Body.Close()

	var info struct {
		Email string `json:"email"`
	}
	json.NewDecoder(resp2.Body).Decode(&info)
	if info.Email == "" {
		http.Error(w, "no email from google", 500)
		return
	}
	if !emailAllowed(info.Email) {
		http.Error(w, "access denied", 403)
		return
	}

	// Find or create user (same as GitHub flow)
	var userID string
	err = db.QueryRow("SELECT id FROM saas_users WHERE email=?", info.Email).Scan(&userID)
	if err != nil {
		userID = fmt.Sprintf("%d", time.Now().UnixNano())
		db.Exec("INSERT INTO saas_users (id,email) VALUES (?,?)", userID, info.Email)
	}

	slug := "u-" + userID[:8]

	var vmToken string
	db.QueryRow("SELECT vm_token FROM saas_users WHERE id=?", userID).Scan(&vmToken)

	authCode := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", userID, time.Now().UnixNano()))))[:32]
	db.Exec("INSERT INTO auth_codes (code,user_id,slug,vm_token) VALUES (?,?,?,?)", authCode, userID, slug, vmToken)

	http.Redirect(w, r, "https://app.cicy-ai.com?code="+authCode, 302)
}

// GET /api/auth/github/callback
func handleGithubCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", 400)
		return
	}

	// Exchange code for token
	data := fmt.Sprintf(`{"client_id":"%s","client_secret":"%s","code":"%s"}`, os.Getenv("GITHUB_CLIENT_ID"), os.Getenv("GITHUB_CLIENT_SECRET"), code)
	req, _ := http.NewRequest("POST", "https://github.com/login/oauth/access_token", strings.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "token exchange failed", 500)
		return
	}
	defer resp.Body.Close()

	var tok struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(resp.Body).Decode(&tok)

	// Get email
	req2, _ := http.NewRequest("GET", "https://api.github.com/user/emails", nil)
	req2.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		http.Error(w, "failed to get emails", 500)
		return
	}
	defer resp2.Body.Close()

	var emails []struct {
		Email   string `json:"email"`
		Primary bool   `json:"primary"`
	}
	body, _ := io.ReadAll(resp2.Body)
	json.Unmarshal(body, &emails)

	var email string
	for _, e := range emails {
		if e.Primary {
			email = e.Email
			break
		}
	}
	if email == "" && len(emails) > 0 {
		email = emails[0].Email
	}
	if email == "" {
		http.Error(w, "no email from github", 500)
		return
	}
	if !emailAllowed(email) {
		http.Error(w, "access denied", 403)
		return
	}

	// Find or create user
	var userID string
	err = db.QueryRow("SELECT id FROM saas_users WHERE email=?", email).Scan(&userID)
	if err != nil {
		userID = fmt.Sprintf("%d", time.Now().UnixNano())
		db.Exec("INSERT INTO saas_users (id,email) VALUES (?,?)", userID, email)
	}

	slug := "u-" + userID[:8]

	// Get VM token
	var vmToken string
	db.QueryRow("SELECT vm_token FROM saas_users WHERE id=?", userID).Scan(&vmToken)

	// Generate one-time auth code
	authCode := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", userID, time.Now().UnixNano()))))[:32]
	db.Exec("INSERT INTO auth_codes (code,user_id,slug,vm_token) VALUES (?,?,?,?)", authCode, userID, slug, vmToken)

	http.Redirect(w, r, "https://app.cicy-ai.com?code="+authCode, 302)
}

// GET /api/auth/saas/verify (JWT auth)
func handleSaasVerify(w http.ResponseWriter, r *http.Request) {
	token := getToken(r)
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		httpErr(w, 401, "no token")
		return
	}

	userID, _, err := parseJWT(token)
	if err != nil {
		httpErr(w, 401, "invalid token")
		return
	}

	var email, plan, backend string
	db.QueryRow("SELECT email,plan,backend_url FROM saas_users WHERE id=?", userID).Scan(&email, &plan, &backend)

	J(w, M{"valid": true, "user_id": userID, "email": email, "plan": plan, "backend": backend})
}

// GET /api/auth/saas/me
func handleSaasMe(w http.ResponseWriter, r *http.Request) {
	handleSaasVerify(w, r)
}

// --- JWT helpers (HS256, no external deps) ---

func signJWT(sub, slug string) string {
	header := base64url([]byte(`{"alg":"HS256","typ":"JWT"}`))
	exp := time.Now().Add(7 * 24 * time.Hour).Unix()
	payload := base64url([]byte(fmt.Sprintf(`{"sub":"%s","slug":"%s","exp":%d}`, sub, slug, exp)))
	sig := hmacSHA256(header+"."+payload, getJWTSecret())
	return header + "." + payload + "." + base64url(sig)
}

func parseJWT(token string) (string, string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", fmt.Errorf("bad jwt")
	}
	// Verify signature
	expected := base64url(hmacSHA256(parts[0]+"."+parts[1], getJWTSecret()))
	if parts[2] != expected {
		log.Printf("[parseJWT] sig mismatch: got=%s expected=%s secret=%s", parts[2], expected, getJWTSecret())
		return "", "", fmt.Errorf("bad sig")
	}
	// Decode payload
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", err
	}
	var claims struct {
		Sub  string  `json:"sub"`
		Slug string  `json:"slug"`
		Exp  float64 `json:"exp"`
	}
	json.Unmarshal(payload, &claims)
	if time.Now().Unix() > int64(claims.Exp) {
		return "", "", fmt.Errorf("expired")
	}
	return claims.Sub, claims.Slug, nil
}

func hmacSHA256(data, secret string) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(data))
	return h.Sum(nil)
}

func base64url(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}
