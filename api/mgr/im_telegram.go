package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// telegramTransport implements botTransport over the Telegram Bot API,
// reusing the helpers in tg.go.
type telegramTransport struct {
	token string
}

func newTelegramTransport(acc *imAccount) (botTransport, error) {
	token := strings.TrimSpace(acc.Secret)
	if token == "" {
		return nil, fmt.Errorf("telegram bot token not set")
	}
	return &telegramTransport{token: token}, nil
}

func (t *telegramTransport) Kind() string  { return imPlatformTelegram }
func (t *telegramTransport) CanEdit() bool { return true }

// telegramValidateToken probes getMe with the token.
//   - returns (username, true, nil) on success;
//   - (_, true, err) when Telegram itself rejected the token (a real bad token);
//   - (_, false, err) when api.telegram.org was unreachable (network/proxy issue —
//     not necessarily a bad token, so callers may still accept it).
func telegramValidateToken(token string) (username string, reachable bool, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", true, fmt.Errorf("empty token")
	}
	result, perr := tgPostFormWithToken(token, "getMe", url.Values{})
	if perr != nil {
		if result != nil {
			return "", true, perr // Telegram replied ok:false → token rejected
		}
		return "", false, perr // could not reach Telegram
	}
	body, _ := result["result"].(map[string]interface{})
	if body == nil {
		return "", true, fmt.Errorf("getMe returned no result")
	}
	username, _ = body["username"].(string)
	if strings.TrimSpace(username) == "" {
		if name, ok := body["first_name"].(string); ok {
			username = strings.TrimSpace(name)
		}
	}
	if strings.TrimSpace(username) == "" {
		username = "bot"
	}
	return username, true, nil
}

func (t *telegramTransport) Poll(cursor string) ([]botMsg, string, error) {
	offset, _ := strconv.ParseInt(strings.TrimSpace(cursor), 10, 64)
	if offset < 0 {
		offset = 0
	}
	reqURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=30&offset=%d", t.token, offset)
	resp, err := http.Get(reqURL)
	if err != nil {
		return nil, cursor, err
	}
	defer resp.Body.Close()
	var payload tgGetUpdatesResp
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, cursor, err
	}
	if !payload.OK {
		return nil, cursor, fmt.Errorf("telegram getUpdates returned not ok")
	}
	var msgs []botMsg
	maxUpdate := offset - 1
	for _, update := range payload.Result {
		if update.UpdateID > maxUpdate {
			maxUpdate = update.UpdateID
		}
		m := update.primaryMessage()
		if m == nil {
			continue
		}
		text := strings.TrimSpace(firstNonEmpty(m.Text, m.Caption))
		if text == "" {
			continue
		}
		msgs = append(msgs, botMsg{
			Text:   text,
			Peer:   botPeer{ChatID: strconv.FormatInt(m.Chat.ID, 10)},
			FromID: strconv.FormatInt(m.From.ID, 10),
		})
	}
	next := cursor
	if maxUpdate >= offset {
		next = strconv.FormatInt(maxUpdate+1, 10)
	}
	return msgs, next, nil
}

func (t *telegramTransport) Send(peer botPeer, text string) (string, error) {
	if peer.empty() {
		return "", fmt.Errorf("no chat id")
	}
	text = imClampMessage(text)
	result, err := sendTGPlainMessageWithToken(t.token, peer.ChatID, text)
	if err != nil {
		return "", err
	}
	mid := tgExtractMessageID(result)
	if mid <= 0 {
		return "", nil
	}
	return strconv.FormatInt(mid, 10), nil
}

func (t *telegramTransport) Edit(peer botPeer, messageID, text string) error {
	if peer.empty() {
		return fmt.Errorf("no chat id")
	}
	mid, _ := strconv.ParseInt(strings.TrimSpace(messageID), 10, 64)
	if mid <= 0 {
		return errBotEditUnsupported
	}
	text = imClampMessage(text)
	_, err := editTGMessageTextWithToken(t.token, peer.ChatID, mid, text)
	return err
}

func (t *telegramTransport) Typing(peer botPeer) error {
	if peer.empty() {
		return nil
	}
	_, err := sendTGChatActionWithToken(t.token, peer.ChatID, "typing")
	return err
}

// imClampMessage trims and length-limits outbound IM text.
func imClampMessage(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		text = "..."
	}
	runes := []rune(text)
	const limit = 3500
	if len(runes) > limit {
		text = string(runes[:limit]) + "\n..."
	}
	return text
}
