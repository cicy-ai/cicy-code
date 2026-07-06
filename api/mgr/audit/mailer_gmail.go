// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// gmailAPI is the minimal surface GmailMailer needs. The real implementation
// talks to Google's OAuth + Gmail REST endpoints; tests inject a stub so the
// unit suite never reaches the network.
type gmailAPI interface {
	senderAddress() (string, error)           // authenticated account's email (RFC822 From)
	sendRaw(rawB64URL string) (string, error) // returns the Gmail message id
}

// GmailMailer delivers incident emails through the Gmail REST API using a
// long-lived OAuth refresh token (the GMAIL_* credentials in global.json).
// No verified sending domain required — mail goes out as the authenticated
// Gmail account, which makes it the pragmatic backend when an operator
// already has Gmail wired but no Resend domain.
type GmailMailer struct {
	api gmailAPI
}

// NewGmailMailer builds a network-backed GmailMailer from OAuth credentials.
func NewGmailMailer(creds *gmailCredentials) *GmailMailer {
	return &GmailMailer{api: &gmailHTTPClient{
		creds:     creds,
		client:    &http.Client{Timeout: 15 * time.Second},
		fromEmail: creds.From, // pre-seed: skip the profile API call when known
	}}
}

func (m *GmailMailer) Send(msg EmailMessage) error {
	if len(msg.To) == 0 {
		return fmt.Errorf("gmail: empty recipient list")
	}
	from, err := m.api.senderAddress()
	if err != nil {
		return fmt.Errorf("gmail: resolve sender: %w", err)
	}
	raw := buildRFC822(from, msg)
	id, err := m.api.sendRaw(base64.URLEncoding.EncodeToString([]byte(raw)))
	if err != nil {
		return err
	}
	log.Printf("[audit] gmail message_id=%s event=%s recipients=%d", id, msg.EventID, len(msg.To))
	return nil
}

// buildRFC822 renders the message with the same X-Cicy-Audit-* headers the
// other mailers set. Subject is RFC2047-encoded so non-ASCII (Chinese / em
// dash) survives transit; body is sent verbatim as UTF-8.
func buildRFC822(from string, msg EmailMessage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(msg.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", msg.Subject))
	fmt.Fprintf(&b, "X-Cicy-Audit-Event: %s\r\n", msg.EventID)
	fmt.Fprintf(&b, "X-Cicy-Audit-Agent: %s\r\n", msg.AgentID)
	fmt.Fprintf(&b, "X-Cicy-Audit-Severity: %s\r\n", msg.Severity)
	b.WriteString("MIME-Version: 1.0\r\n")

	// HTML present → multipart/alternative so clients render the HTML and fall
	// back to plain text. Otherwise unchanged text/plain behaviour.
	if strings.TrimSpace(msg.HTMLBody) != "" {
		boundary := "cicy_audit_" + msg.EventID
		if boundary == "cicy_audit_" {
			boundary = "cicy_audit_boundary"
		}
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.Body)
		b.WriteString("\r\n")
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.HTMLBody)
		b.WriteString("\r\n")
		fmt.Fprintf(&b, "--%s--\r\n", boundary)
		return b.String()
	}

	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(msg.Body)
	if !strings.HasSuffix(msg.Body, "\n") {
		b.WriteString("\r\n")
	}
	return b.String()
}

// ── real Gmail REST transport ──

type gmailCredentials struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	From         string // authorized account email (db/google.json); empty in legacy path
}

type gmailHTTPClient struct {
	creds  *gmailCredentials
	client *http.Client

	mu        sync.Mutex
	token     string
	tokenExp  time.Time
	fromEmail string
}

// accessToken exchanges the refresh token for a short-lived access token,
// caching it until ~30s before expiry.
func (g *gmailHTTPClient) accessToken() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && time.Now().Before(g.tokenExp.Add(-30*time.Second)) {
		return g.token, nil
	}
	form := url.Values{
		"client_id":     {g.creds.ClientID},
		"client_secret": {g.creds.ClientSecret},
		"refresh_token": {g.creds.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	resp, err := g.client.PostForm("https://oauth2.googleapis.com/token", form)
	if err != nil {
		return "", fmt.Errorf("oauth refresh: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth refresh http %d: %s", resp.StatusCode, gmailClip(string(body), 200))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("oauth parse: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("oauth: empty access_token")
	}
	g.token = tr.AccessToken
	g.tokenExp = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return g.token, nil
}

func (g *gmailHTTPClient) senderAddress() (string, error) {
	g.mu.Lock()
	cached := g.fromEmail
	g.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	tok, err := g.accessToken()
	if err != nil {
		return "", err
	}
	req, _ := http.NewRequest(http.MethodGet, "https://gmail.googleapis.com/gmail/v1/users/me/profile", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gmail profile http %d: %s", resp.StatusCode, gmailClip(string(body), 200))
	}
	var pr struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return "", err
	}
	if pr.EmailAddress == "" {
		return "", fmt.Errorf("gmail profile: empty emailAddress")
	}
	g.mu.Lock()
	g.fromEmail = pr.EmailAddress
	g.mu.Unlock()
	return pr.EmailAddress, nil
}

func (g *gmailHTTPClient) sendRaw(rawB64URL string) (string, error) {
	tok, err := g.accessToken()
	if err != nil {
		return "", err
	}
	payload, _ := json.Marshal(map[string]string{"raw": rawB64URL})
	req, _ := http.NewRequest(http.MethodPost,
		"https://gmail.googleapis.com/gmail/v1/users/me/messages/send", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gmail send http %d: %s", resp.StatusCode, gmailClip(string(body), 300))
	}
	var sr struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &sr)
	return sr.ID, nil
}

// loadGmailCredentials resolves the Gmail OAuth credentials from the canonical
// db/ location the in-product "Connect Google" flow writes (db/google.json +
// db/google_oauth_client.json, per oauth_google.go). The legacy global.json
// GMAIL_* keys are intentionally NOT consulted — db/ is the single source of
// truth and persists across redeploys. Returns nil when db/ has no usable
// credentials.
func loadGmailCredentials() *gmailCredentials {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return loadGmailFromDB(filepath.Join(home, "cicy-ai", "db"))
}

// oauthClientCreds matches one client entry; google_oauth_client.json is
// Google's downloaded format, wrapping creds under "web" or "installed".
type oauthClientCreds struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type googleOAuthClientFile struct {
	Web          *oauthClientCreds `json:"web"`
	Installed    *oauthClientCreds `json:"installed"`
	ClientID     string            `json:"client_id"`     // un-wrapped form
	ClientSecret string            `json:"client_secret"` // un-wrapped form
}

// loadGmailFromDB reads db/google.json (refresh_token, authorized_email,
// client_id) + db/google_oauth_client.json (client_secret). Env
// CICY_GOOGLE_OAUTH_CLIENT_ID/SECRET override the on-disk client.
func loadGmailFromDB(dbDir string) *gmailCredentials {
	data, err := os.ReadFile(filepath.Join(dbDir, "google.json"))
	if err != nil {
		return nil
	}
	var gs struct {
		RefreshToken    string `json:"refresh_token"`
		AuthorizedEmail string `json:"authorized_email"`
		ClientID        string `json:"client_id"`
	}
	if json.Unmarshal(data, &gs) != nil || strings.TrimSpace(gs.RefreshToken) == "" {
		return nil
	}
	clientID, clientSecret := strings.TrimSpace(gs.ClientID), ""
	if oc, err := os.ReadFile(filepath.Join(dbDir, "google_oauth_client.json")); err == nil {
		var f googleOAuthClientFile
		if json.Unmarshal(oc, &f) == nil {
			cc := f.Web
			if cc == nil {
				cc = f.Installed
			}
			if cc != nil {
				if clientID == "" {
					clientID = strings.TrimSpace(cc.ClientID)
				}
				clientSecret = strings.TrimSpace(cc.ClientSecret)
			} else {
				if clientID == "" {
					clientID = strings.TrimSpace(f.ClientID)
				}
				clientSecret = strings.TrimSpace(f.ClientSecret)
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv("CICY_GOOGLE_OAUTH_CLIENT_ID")); v != "" {
		clientID = v
	}
	if v := strings.TrimSpace(os.Getenv("CICY_GOOGLE_OAUTH_CLIENT_SECRET")); v != "" {
		clientSecret = v
	}
	if clientID == "" || clientSecret == "" {
		return nil
	}
	return &gmailCredentials{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RefreshToken: strings.TrimSpace(gs.RefreshToken),
		From:         strings.TrimSpace(gs.AuthorizedEmail),
	}
}

func gmailClip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
