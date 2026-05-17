package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func getGlobalTGConfig() (token, chatID string) {
	raw, _ := os.ReadFile(cicyGlobalJSONPath)
	var g map[string]interface{}
	json.Unmarshal(raw, &g)
	token, _ = g["TG_BOT_TOKEN"].(string)
	chatID, _ = g["TG_CHAT_ID"].(string)
	return
}

func getTGConfigForPane(paneID string) (token, chatID string) {
	paneID = normPaneID(strings.TrimSpace(paneID))
	if paneID != "" {
		var paneToken, paneChatID sql.NullString
		_ = store.QueryRow(`SELECT COALESCE(tg_token, ''), COALESCE(tg_chat_id, '') FROM agent_config WHERE pane_id=?`, paneID).
			Scan(&paneToken, &paneChatID)
		token = strings.TrimSpace(paneToken.String)
		chatID = strings.TrimSpace(paneChatID.String)
		if token != "" || chatID != "" {
			return token, chatID
		}
	}
	return getGlobalTGConfig()
}

func tgPostFormWithToken(token, method string, values url.Values) (map[string]interface{}, error) {
	resp, err := http.PostForm(
		fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method),
		values,
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if ok, exists := result["ok"].(bool); exists && !ok {
		return result, fmt.Errorf("telegram %s not ok: %v", method, result["description"])
	}
	return result, nil
}

func sendTGMessageWithToken(token, chatID, text string) (map[string]interface{}, error) {
	return tgPostFormWithToken(token, "sendMessage", url.Values{
		"chat_id":    {chatID},
		"text":       {text},
		"parse_mode": {"Markdown"},
	})
}

func sendTGPhotoWithToken(token, chatID, photo, caption string) (map[string]interface{}, error) {
	return tgPostFormWithToken(token, "sendPhoto", url.Values{
		"chat_id": {chatID},
		"photo":   {photo},
		"caption": {caption},
	})
}

// POST /api/tg/send {"text":"hello","chat_id":"optional","pane_id":"optional"}
func handleTGSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaneID string `json:"pane_id"`
		Text   string `json:"text"`
		ChatID string `json:"chat_id"`
	}
	readBody(r, &req)
	if req.Text == "" {
		httpErr(w, 400, "text required")
		return
	}
	token, defaultChat := getTGConfigForPane(req.PaneID)
	if req.ChatID == "" {
		req.ChatID = defaultChat
	}
	if token == "" || strings.TrimSpace(req.ChatID) == "" {
		httpErr(w, 400, "tg token/chat_id not configured")
		return
	}
	result, err := sendTGMessageWithToken(token, req.ChatID, req.Text)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, result)
}

// POST /api/tg/photo {"photo":"url","caption":"optional","chat_id":"optional","pane_id":"optional"}
func handleTGPhoto(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaneID  string `json:"pane_id"`
		Photo   string `json:"photo"`
		Caption string `json:"caption"`
		ChatID  string `json:"chat_id"`
	}
	readBody(r, &req)
	if req.Photo == "" {
		httpErr(w, 400, "photo required")
		return
	}
	token, defaultChat := getTGConfigForPane(req.PaneID)
	if req.ChatID == "" {
		req.ChatID = defaultChat
	}
	if token == "" || strings.TrimSpace(req.ChatID) == "" {
		httpErr(w, 400, "tg token/chat_id not configured")
		return
	}
	result, err := sendTGPhotoWithToken(token, req.ChatID, req.Photo, req.Caption)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, result)
}
