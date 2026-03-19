package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

func handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	raw, _ := os.ReadFile(filepath.Join(home, "global.json"))
	var g map[string]interface{}
	json.Unmarshal(raw, &g)
	clientID, _ := g["GMAIL_WEB_CLIENT_ID"].(string)
	redirect := fmt.Sprintf("https://%s/oauth/callback", r.Host)
	u := fmt.Sprintf("https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&access_type=offline&prompt=consent",
		url.QueryEscape(clientID), url.QueryEscape(redirect),
		url.QueryEscape("https://mail.google.com/ https://www.googleapis.com/auth/spreadsheets https://www.googleapis.com/auth/drive https://www.googleapis.com/auth/calendar"))
	http.Redirect(w, r, u, 302)
}

func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", 400)
		return
	}

	home, _ := os.UserHomeDir()
	raw, err := os.ReadFile(filepath.Join(home, "global.json"))
	if err != nil {
		http.Error(w, "cannot read global.json", 500)
		return
	}
	var g map[string]interface{}
	json.Unmarshal(raw, &g)

	clientID, _ := g["GMAIL_WEB_CLIENT_ID"].(string)
	clientSecret, _ := g["GMAIL_WEB_CLIENT_SECRET"].(string)

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", url.Values{
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {fmt.Sprintf("https://%s/oauth/callback", r.Host)},
		"grant_type":    {"authorization_code"},
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var tokens map[string]interface{}
	json.Unmarshal(body, &tokens)

	rt, _ := tokens["refresh_token"].(string)
	if rt != "" {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<h2>✅ Success</h2><p>refresh_token:</p><pre>%s</pre><p>已自动保存到 global.json</p>", rt)
		// 自动写入 global.json
		g["GMAIL_REFRESH_TOKEN"] = rt
		updated, _ := json.MarshalIndent(g, "", "  ")
		os.WriteFile(filepath.Join(home, "global.json"), updated, 0600)
	} else {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<h2>⚠️ Response</h2><pre>%s</pre>", string(body))
	}
}
