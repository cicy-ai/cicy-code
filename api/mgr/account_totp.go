package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

func serveAccountTOTP(w http.ResponseWriter, secret string) {
	payload, _ := json.Marshal(M{"twoFactor": secret})
	req, _ := http.NewRequest(http.MethodPost, "https://otp.cicy-ai.com/api/totp", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "TOTP service request failed")
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		httpErr(w, http.StatusBadGateway, "TOTP service rejected the key")
		return
	}
	var result struct {
		Capture   string `json:"capture"`
		Code      string `json:"code"`
		Countdown int    `json:"countdown"`
		Period    int    `json:"period"`
	}
	if json.Unmarshal(body, &result) != nil || result.Code == "" {
		httpErr(w, http.StatusBadGateway, "invalid TOTP service response")
		return
	}
	J(w, result)
}

func handleGoogleAccountTOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile string `json:"profile"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	profiles, err := readChromeAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "invalid chrome.json")
		return
	}
	profile, ok := profiles[req.Profile]
	if !ok {
		httpErr(w, http.StatusNotFound, "Chrome profile not found")
		return
	}
	account, _ := googleAccountNode(profile)
	secret := googleString(account, "totp")
	if secret == "" {
		httpErr(w, http.StatusBadRequest, "2fa not configured")
		return
	}
	serveAccountTOTP(w, secret)
}

func handleChatGPTAccountTOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	accounts, err := readChatGPTAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "invalid chatgpt.json")
		return
	}
	account, ok := accounts[req.Name]
	if !ok {
		httpErr(w, http.StatusNotFound, "ChatGPT account not found")
		return
	}
	if account.TwoFA == "" {
		httpErr(w, http.StatusBadRequest, "2fa not configured")
		return
	}
	serveAccountTOTP(w, account.TwoFA)
}
