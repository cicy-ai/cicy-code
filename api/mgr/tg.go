package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

func getTGConfig() (token, chatID string) {
	home, _ := os.UserHomeDir()
	raw, _ := os.ReadFile(filepath.Join(home, "global.json"))
	var g map[string]interface{}
	json.Unmarshal(raw, &g)
	token, _ = g["TG_BOT_TOKEN"].(string)
	chatID, _ = g["TG_CHAT_ID"].(string)
	return
}

// POST /api/tg/send {"text":"hello","chat_id":"optional"}
func handleTGSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text   string `json:"text"`
		ChatID string `json:"chat_id"`
	}
	readBody(r, &req)
	if req.Text == "" {
		httpErr(w, 400, "text required")
		return
	}
	token, defaultChat := getTGConfig()
	if req.ChatID == "" {
		req.ChatID = defaultChat
	}
	resp, err := http.PostForm(fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token),
		url.Values{"chat_id": {req.ChatID}, "text": {req.Text}, "parse_mode": {"Markdown"}})
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	J(w, result)
}

// POST /api/tg/photo {"photo":"url","caption":"optional"}
func handleTGPhoto(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Photo   string `json:"photo"`
		Caption string `json:"caption"`
		ChatID  string `json:"chat_id"`
	}
	readBody(r, &req)
	if req.Photo == "" {
		httpErr(w, 400, "photo required")
		return
	}
	token, defaultChat := getTGConfig()
	if req.ChatID == "" {
		req.ChatID = defaultChat
	}
	resp, err := http.PostForm(fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", token),
		url.Values{"chat_id": {req.ChatID}, "photo": {req.Photo}, "caption": {req.Caption}})
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	J(w, result)
}
