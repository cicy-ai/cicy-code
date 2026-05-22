package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const tgTypingInterval = 4 * time.Second
const tgTypingDuration = 30 * time.Second
const tgAckText = "Thinking..."
const tgReplyUpdateMinInterval = 1200 * time.Millisecond
const tgMessageTextLimit = 3500

type tgPaneConfig struct {
	PaneID    string
	Token     string
	ChatID    string
	Enabled   bool
	AgentType string
}

type tgLiveMessageState struct {
	mu        sync.Mutex
	messageID int64
	lastText  string
}

type tgReplyPushHook struct {
	paneID      string
	token       string
	chatID      string
	state       *tgLiveMessageState
	mu          sync.Mutex
	thinking    strings.Builder
	answer      strings.Builder
	toolOrder   []string
	toolSummary map[string]string
	lastPushAt  time.Time
	timer       *time.Timer
	closed      bool
}

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

func tgWebhookKey(token string) string {
	sum := sha256.Sum256([]byte("tg-webhook:" + strings.TrimSpace(token)))
	return fmt.Sprintf("%x", sum[:12])
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

func sendTGTextWithToken(token, chatID, text, parseMode string) (map[string]interface{}, error) {
	values := url.Values{"chat_id": {chatID}, "text": {text}}
	if strings.TrimSpace(parseMode) != "" {
		values.Set("parse_mode", parseMode)
	}
	return tgPostFormWithToken(token, "sendMessage", values)
}

func sendTGMessageWithToken(token, chatID, text string) (map[string]interface{}, error) {
	return sendTGTextWithToken(token, chatID, text, "Markdown")
}

func sendTGPlainMessageWithToken(token, chatID, text string) (map[string]interface{}, error) {
	return sendTGTextWithToken(token, chatID, text, "")
}

func sendTGPhotoWithToken(token, chatID, photo, caption string) (map[string]interface{}, error) {
	return tgPostFormWithToken(token, "sendPhoto", url.Values{"chat_id": {chatID}, "photo": {photo}, "caption": {caption}})
}

func editTGMessageTextWithToken(token, chatID string, messageID int64, text string) (map[string]interface{}, error) {
	return tgPostFormWithToken(token, "editMessageText", url.Values{
		"chat_id":    {chatID},
		"message_id": {strconv.FormatInt(messageID, 10)},
		"text":       {text},
	})
}

func sendTGChatActionWithToken(token, chatID, action string) (map[string]interface{}, error) {
	return tgPostFormWithToken(token, "sendChatAction", url.Values{"chat_id": {chatID}, "action": {action}})
}

func startTGTyping(token, chatID, paneID string) {
	if _, err := sendTGChatActionWithToken(token, chatID, "typing"); err != nil {
		log.Printf("[tg] send typing failed chat=%s pane=%s err=%v", chatID, shortPaneID(paneID), err)
		return
	}
	log.Printf("[tg] send typing chat=%s pane=%s", chatID, shortPaneID(paneID))

	go func() {
		deadline := time.Now().Add(tgTypingDuration)
		for {
			time.Sleep(tgTypingInterval)
			if time.Now().After(deadline) {
				return
			}
			if _, err := sendTGChatActionWithToken(token, chatID, "typing"); err != nil {
				log.Printf("[tg] send typing failed chat=%s pane=%s err=%v", chatID, shortPaneID(paneID), err)
				return
			}
			log.Printf("[tg] keep typing chat=%s pane=%s", chatID, shortPaneID(paneID))
		}
	}()
}

// POST /api/tg/send {"text":"hello","chat_id":"optional"}
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

// POST /api/tg/photo {"photo":"url","caption":"optional"}
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

type tgUpdate struct {
	UpdateID      int64            `json:"update_id"`
	Message       *tgMessage       `json:"message"`
	EditedMessage *tgMessage       `json:"edited_message"`
	CallbackQuery *tgCallbackQuery `json:"callback_query"`
}

type tgCallbackQuery struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Message *tgMessage `json:"message"`
	Data    string     `json:"data"`
}

type tgMessage struct {
	MessageID int64        `json:"message_id"`
	Date      int64        `json:"date"`
	Text      string       `json:"text"`
	Caption   string       `json:"caption"`
	From      tgUser       `json:"from"`
	Chat      tgChat       `json:"chat"`
	Voice     *tgVoice     `json:"voice,omitempty"`
	Audio     *tgAudio     `json:"audio,omitempty"`
	Photo     []tgPhotoSize `json:"photo,omitempty"`
	Document  *tgDocument  `json:"document,omitempty"`
	Video     *tgVideo     `json:"video,omitempty"`
	VideoNote *tgVideoNote `json:"video_note,omitempty"`
}

// tgVoice — 语音消息（OGG OPUS）。
type tgVoice struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

// tgAudio — 音频文件（mp3 / m4a 等）。
type tgAudio struct {
	FileID    string `json:"file_id"`
	Duration  int    `json:"duration"`
	MimeType  string `json:"mime_type"`
	FileName  string `json:"file_name"`
	FileSize  int64  `json:"file_size"`
}

// tgPhotoSize — 一张图片的某个分辨率版本。Photo 字段是数组（多个分辨率）。
type tgPhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
}

// tgDocument — 任意文件。
type tgDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

// tgVideo — 视频。
type tgVideo struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

// tgVideoNote — 圆形视频消息（无音频文件名/mime）。
type tgVideoNote struct {
	FileID   string `json:"file_id"`
	Length   int    `json:"length"`
	Duration int    `json:"duration"`
	FileSize int64  `json:"file_size"`
}

type tgUser struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	LanguageCode string `json:"language_code"`
}

type tgChat struct {
	ID int64 `json:"id"`
}

func (u tgUpdate) primaryMessage() *tgMessage {
	if u.Message != nil {
		return u.Message
	}
	return u.EditedMessage
}

func findPaneByTGTokenChat(token, chatID string) (tgPaneConfig, error) {
	rows, err := store.Query(`SELECT pane_id, COALESCE(tg_token, ''), COALESCE(tg_chat_id, ''), COALESCE(tg_enable, 0), COALESCE(agent_type, '')
		FROM agent_config
		WHERE COALESCE(tg_enable, 0)=1 AND COALESCE(tg_token, '')=? AND COALESCE(tg_chat_id, '')=?`, token, chatID)
	if err != nil {
		return tgPaneConfig{}, err
	}
	defer rows.Close()
	var matches []tgPaneConfig
	for rows.Next() {
		var item tgPaneConfig
		var enabled int
		if err := rows.Scan(&item.PaneID, &item.Token, &item.ChatID, &enabled, &item.AgentType); err != nil {
			continue
		}
		item.Enabled = enabled != 0
		item.PaneID = normPaneID(item.PaneID)
		item.AgentType = normalizeAgentType(item.AgentType)
		matches = append(matches, item)
	}
	if len(matches) == 0 {
		return tgPaneConfig{}, fmt.Errorf("telegram worker binding not found")
	}
	if len(matches) > 1 {
		return tgPaneConfig{}, fmt.Errorf("multiple workers share the same tg token/chat_id")
	}
	return matches[0], nil
}

func findPaneByTGToken(token string) (tgPaneConfig, error) {
	rows, err := store.Query(`SELECT pane_id, COALESCE(tg_token, ''), COALESCE(tg_chat_id, ''), COALESCE(tg_enable, 0), COALESCE(agent_type, '')
		FROM agent_config
		WHERE COALESCE(tg_enable, 0)=1 AND COALESCE(tg_token, '')=?`, token)
	if err != nil {
		return tgPaneConfig{}, err
	}
	defer rows.Close()
	var matches []tgPaneConfig
	for rows.Next() {
		var item tgPaneConfig
		var enabled int
		if err := rows.Scan(&item.PaneID, &item.Token, &item.ChatID, &enabled, &item.AgentType); err != nil {
			continue
		}
		item.Enabled = enabled != 0
		item.PaneID = normPaneID(item.PaneID)
		item.AgentType = normalizeAgentType(item.AgentType)
		matches = append(matches, item)
	}
	if len(matches) == 0 {
		return tgPaneConfig{}, fmt.Errorf("telegram worker binding not found")
	}
	if len(matches) > 1 {
		return tgPaneConfig{}, fmt.Errorf("multiple workers share the same tg token")
	}
	return matches[0], nil
}

func currentPaneStatusForTelegram(paneID, agentType string) string {
	if st := getPaneStatus(shortPaneID(normPaneID(paneID))); st != nil && st.Status != nil && strings.TrimSpace(*st.Status) != "" {
		return strings.TrimSpace(*st.Status)
	}
	st := checkPane(normPaneID(paneID), map[string]string{"agent_type": normalizeAgentType(agentType)})
	if st.Status == nil {
		return ""
	}
	return strings.TrimSpace(*st.Status)
}

func insertTelegramQueueItem(paneID, text, status string) (int64, error) {
	res, err := store.Exec(`INSERT INTO agent_queue (
		pane_id, message, type, priority, step_kind, title, status, target_pane_id, created_by, sent_at
	) VALUES (?,?,?,?,?,?,?,?,?,CASE WHEN ?='sent' THEN `+store.Now()+` ELSE NULL END)`,
		normPaneID(paneID), text, "telegram", 0, "telegram", "Telegram inbound", status, shortPaneID(normPaneID(paneID)), "telegram", status)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func routeTelegramMessage(cfg tgPaneConfig, text string) (string, error) {
	paneID := normPaneID(cfg.PaneID)
	agentType := normalizeAgentType(cfg.AgentType)
	status := currentPaneStatusForTelegram(paneID, agentType)

	if agentType == "codex" {
		if err := sendTextToPane(paneID, text, true); err != nil {
			return "", err
		}
		if _, err := insertTelegramQueueItem(paneID, text, "sent"); err != nil {
			return "", err
		}
		return "direct_codex_send", nil
	}

	if _, err := insertTelegramQueueItem(paneID, text, "pending"); err != nil {
		return "", err
	}
	if status != "thinking" {
		go dispatchQueue(paneID)
		return "queued_and_dispatched", nil
	}
	return "queued", nil
}

type tgPoller struct {
	token string
	stop  chan struct{}
}

type tgGetUpdatesResp struct {
	OK     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

var tgPollers = struct {
	mu sync.RWMutex
	m  map[string]*tgPoller
}{m: map[string]*tgPoller{}}

var tgLiveMessages = struct {
	mu sync.Mutex
	m  map[string]*tgLiveMessageState
}{m: map[string]*tgLiveMessageState{}}

func tgExtractMessageID(result map[string]interface{}) int64 {
	body, _ := result["result"].(map[string]interface{})
	if body == nil {
		return 0
	}
	switch v := body["message_id"].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	default:
		return 0
	}
}

func tgLiveMessageStateForPane(paneID string) *tgLiveMessageState {
	paneID = normPaneID(paneID)
	tgLiveMessages.mu.Lock()
	defer tgLiveMessages.mu.Unlock()
	state := tgLiveMessages.m[paneID]
	if state == nil {
		state = &tgLiveMessageState{}
		tgLiveMessages.m[paneID] = state
	}
	return state
}

func tgRememberPaneMessageID(paneID string, messageID int64) {
	if messageID <= 0 {
		return
	}
	state := tgLiveMessageStateForPane(paneID)
	state.mu.Lock()
	state.messageID = messageID
	state.mu.Unlock()
	log.Printf("[tg] remember message_id pane=%s message_id=%d", shortPaneID(paneID), messageID)
}

func findEnabledTGPaneConfigByPane(paneID string) (tgPaneConfig, error) {
	paneID = normPaneID(strings.TrimSpace(paneID))
	var item tgPaneConfig
	var enabled int
	err := store.QueryRow(`SELECT pane_id, COALESCE(tg_token, ''), COALESCE(tg_chat_id, ''), COALESCE(tg_enable, 0), COALESCE(agent_type, '')
		FROM agent_config WHERE pane_id=? LIMIT 1`, paneID).
		Scan(&item.PaneID, &item.Token, &item.ChatID, &enabled, &item.AgentType)
	if err != nil {
		return tgPaneConfig{}, err
	}
	item.Enabled = enabled != 0
	item.PaneID = normPaneID(item.PaneID)
	item.AgentType = normalizeAgentType(item.AgentType)
	if !item.Enabled || strings.TrimSpace(item.Token) == "" || strings.TrimSpace(item.ChatID) == "" {
		return tgPaneConfig{}, fmt.Errorf("telegram pane binding not ready")
	}
	return item, nil
}

func tgNormalizePushText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		text = tgAckText
	}
	runes := []rune(text)
	if len(runes) > tgMessageTextLimit {
		text = string(runes[:tgMessageTextLimit]) + "\n..."
	}
	return text
}

func tgPushPaneText(paneID, token, chatID string, state *tgLiveMessageState, text string) error {
	text = tgNormalizePushText(text)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.lastText == text {
		log.Printf("[tg] skip push unchanged pane=%s chat=%s", shortPaneID(paneID), chatID)
		return nil
	}
	if state.messageID > 0 {
		if _, err := editTGMessageTextWithToken(token, chatID, state.messageID, text); err == nil {
			state.lastText = text
			log.Printf("[tg] edit message pane=%s chat=%s message_id=%d text_len=%d", shortPaneID(paneID), chatID, state.messageID, len([]rune(text)))
			return nil
		} else {
			log.Printf("[tg] edit message failed pane=%s chat=%s message_id=%d err=%v", shortPaneID(paneID), chatID, state.messageID, err)
		}
		state.messageID = 0
	}
	result, err := sendTGPlainMessageWithToken(token, chatID, text)
	if err != nil {
		return err
	}
	state.messageID = tgExtractMessageID(result)
	state.lastText = text
	log.Printf("[tg] send message pane=%s chat=%s message_id=%d text_len=%d", shortPaneID(paneID), chatID, state.messageID, len([]rune(text)))
	return nil
}

func tgCompactToolSummary(name, arguments string) string {
	name = strings.TrimSpace(name)
	args := strings.TrimSpace(arguments)
	if name == "" {
		name = "tool"
	}
	if strings.HasPrefix(args, "{") || strings.HasPrefix(args, "[") {
		args = strings.ReplaceAll(args, "\n", " ")
	}
	args = strings.Join(strings.Fields(args), " ")
	runes := []rune(args)
	if len(runes) > 120 {
		args = string(runes[:120]) + "..."
	}
	if args == "" {
		return name
	}
	return name + ": " + args
}

func newTGReplyPushHook(paneID string) *tgReplyPushHook {
	cfg, err := findEnabledTGPaneConfigByPane(paneID)
	if err != nil {
		return nil
	}
	hook := &tgReplyPushHook{
		paneID:      cfg.PaneID,
		token:       cfg.Token,
		chatID:      cfg.ChatID,
		state:       tgLiveMessageStateForPane(cfg.PaneID),
		toolSummary: map[string]string{},
	}
	log.Printf("[tg] hook enabled pane=%s chat=%s agent=%s", shortPaneID(cfg.PaneID), cfg.ChatID, cfg.AgentType)
	return hook
}

func (h *tgReplyPushHook) onItems(items []map[string]interface{}) {
	if h == nil || len(items) == 0 {
		return
	}
	// 每个 reply item 一条 TG 消息立即发送，不做 streaming edit。
	for _, item := range items {
		text := renderReplyItemForIM(item)
		if text == "" {
			continue
		}
		text = tgNormalizePushText(text)
		if _, err := sendTGPlainMessageWithToken(h.token, h.chatID, text); err != nil {
			log.Printf("[tg] reply per-item push failed pane=%s chat=%s type=%s err=%v",
				shortPaneID(h.paneID), h.chatID, item["type"], err)
			continue
		}
		log.Printf("[tg] reply per-item push pane=%s chat=%s type=%s len=%d",
			shortPaneID(h.paneID), h.chatID, item["type"], len([]rune(text)))
	}
}
// handleEvents 在 per-item 推送模式下不再 streaming edit。
// 每个 reply item flush 时通过 onItems 单独推送一条 TG 消息。
func (h *tgReplyPushHook) handleEvents(_ []aiGatewayReplyEvent) {}

func (h *tgReplyPushHook) finalize(reply aiGatewayReplySnapshot) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	log.Printf("[tg] hook finalize pane=%s status=%s items=%d", shortPaneID(h.paneID), reply.Status, len(reply.Items))
}

func (h *tgReplyPushHook) schedulePushLocked(force bool) {
	if force {
		if h.timer != nil {
			h.timer.Stop()
			h.timer = nil
		}
		go h.flush()
		return
	}
	wait := tgReplyUpdateMinInterval - time.Since(h.lastPushAt)
	if wait <= 0 && h.timer == nil {
		go h.flush()
		return
	}
	if h.timer != nil {
		return
	}
	h.timer = time.AfterFunc(wait, func() {
		h.mu.Lock()
		h.timer = nil
		h.mu.Unlock()
		h.flush()
	})
}

func (h *tgReplyPushHook) flush() {
	if h == nil {
		return
	}
	h.mu.Lock()
	text := h.renderLocked()
	h.lastPushAt = time.Now()
	h.mu.Unlock()
	log.Printf("[tg] hook flush pane=%s text_len=%d", shortPaneID(h.paneID), len([]rune(text)))
	if err := tgPushPaneText(h.paneID, h.token, h.chatID, h.state, text); err != nil {
		log.Printf("[tg] push reply failed chat=%s pane=%s err=%v", h.chatID, shortPaneID(h.paneID), err)
	}
}

func (h *tgReplyPushHook) renderLocked() string {
	answer := strings.TrimSpace(h.answer.String())
	if h.closed && answer != "" {
		return answer
	}
	parts := []string{tgAckText}
	thinking := strings.TrimSpace(h.thinking.String())
	if thinking != "" {
		parts = append(parts, "Thinking:\n"+thinking)
	}
	if len(h.toolOrder) > 0 {
		lines := make([]string, 0, len(h.toolOrder))
		for _, key := range h.toolOrder {
			if item := strings.TrimSpace(h.toolSummary[key]); item != "" {
				lines = append(lines, "- "+item)
			}
		}
		if len(lines) > 0 {
			parts = append(parts, "Tools:\n"+strings.Join(lines, "\n"))
		}
	}
	if answer != "" {
		parts = append(parts, "Reply:\n"+answer)
	}
	return strings.Join(parts, "\n\n")
}

func syncTelegramPollers() {
	rows, err := store.Query(`SELECT DISTINCT COALESCE(tg_token, '') FROM agent_config WHERE COALESCE(tg_enable, 0)=1 AND COALESCE(tg_token, '')<>''`)
	if err != nil {
		log.Printf("[tg] sync pollers query failed: %v", err)
		return
	}
	defer rows.Close()

	want := map[string]struct{}{}
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			continue
		}
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if _, err := findPaneByTGToken(token); err != nil {
			log.Printf("[tg] skip poller for token %s: %v", tgWebhookKey(token), err)
			continue
		}
		want[token] = struct{}{}
	}

	tgPollers.mu.Lock()
	defer tgPollers.mu.Unlock()

	for token, poller := range tgPollers.m {
		if _, ok := want[token]; ok {
			continue
		}
		close(poller.stop)
		delete(tgPollers.m, token)
		log.Printf("[tg] stopped poller token=%s", tgWebhookKey(token))
	}
	for token := range want {
		if _, ok := tgPollers.m[token]; ok {
			continue
		}
		poller := &tgPoller{token: token, stop: make(chan struct{})}
		tgPollers.m[token] = poller
		go poller.loop()
		log.Printf("[tg] started poller token=%s", tgWebhookKey(token))
	}
}

func (p *tgPoller) loop() {
	backoff := time.Second
	for {
		select {
		case <-p.stop:
			return
		default:
		}

		if err := p.pollOnce(); err != nil {
			log.Printf("[tg] poll token=%s error: %v", tgWebhookKey(p.token), err)
			select {
			case <-p.stop:
				return
			case <-time.After(backoff):
			}
			if backoff < 10*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

func (p *tgPoller) pollOnce() error {
	offset := tgLoadOffset(p.token)
	reqURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=30&offset=%d", p.token, offset)
	resp, err := http.Get(reqURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var payload tgGetUpdatesResp
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if !payload.OK {
		return fmt.Errorf("telegram getUpdates returned not ok")
	}

	for _, update := range payload.Result {
		if update.UpdateID >= offset {
			tgSaveOffset(p.token, update.UpdateID+1)
		}
		p.handleUpdate(update)
	}
	return nil
}

func (p *tgPoller) handleUpdate(update tgUpdate) {
	msg := update.primaryMessage()
	if msg == nil {
		return
	}
	text := strings.TrimSpace(firstNonEmpty(msg.Text, msg.Caption))
	if text == "" {
		return
	}

	cfg, err := findPaneByTGToken(p.token)
	if err != nil {
		log.Printf("[tg] token=%s route skipped: %v", tgWebhookKey(p.token), err)
		return
	}

	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	if strings.TrimSpace(cfg.ChatID) == "" {
		if _, err := store.Exec("UPDATE agent_config SET tg_chat_id=?, updated_at=datetime('now') WHERE pane_id=?", chatID, cfg.PaneID); err != nil {
			log.Printf("[tg] bind chat_id failed pane=%s: %v", shortPaneID(cfg.PaneID), err)
			return
		}
		cfg.ChatID = chatID
		log.Printf("[tg] auto-bound chat_id=%s pane=%s token=%s", chatID, shortPaneID(cfg.PaneID), tgWebhookKey(p.token))
	} else if strings.TrimSpace(cfg.ChatID) != chatID {
		log.Printf("[tg] ignored chat_id=%s pane=%s bound_chat_id=%s token=%s", chatID, shortPaneID(cfg.PaneID), cfg.ChatID, tgWebhookKey(p.token))
		return
	}

		if result, err := sendTGPlainMessageWithToken(p.token, chatID, tgAckText); err != nil {
			log.Printf("[tg] send ack failed chat=%s pane=%s err=%v", chatID, shortPaneID(cfg.PaneID), err)
		} else {
			tgRememberPaneMessageID(cfg.PaneID, tgExtractMessageID(result))
			log.Printf("[tg] send ack chat=%s pane=%s", chatID, shortPaneID(cfg.PaneID))
		}
	startTGTyping(p.token, chatID, cfg.PaneID)

	mode, err := routeTelegramMessage(cfg, text)
	if err != nil {
		log.Printf("[tg] routed update=%d pane=%s error=%v", update.UpdateID, shortPaneID(cfg.PaneID), err)
		return
	}
	log.Printf("[tg] routed update=%d chat=%s pane=%s mode=%s agent=%s",
		update.UpdateID, chatID, shortPaneID(cfg.PaneID), mode, cfg.AgentType)
}

func tgOffsetKey(token string) string {
	return "tg_offset:" + tgWebhookKey(token)
}

func tgLoadOffset(token string) int64 {
	var raw string
	if err := store.QueryRow("SELECT value FROM global_vars WHERE key_name=?", tgOffsetKey(token)).Scan(&raw); err != nil {
		return 0
	}
	value, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if value < 0 {
		return 0
	}
	return value
}

func tgSaveOffset(token string, offset int64) {
	if offset < 0 {
		offset = 0
	}
	if _, err := store.Exec(store.Upsert("global_vars", "key_name", []string{"key_name", "value"}, []string{"value"}), tgOffsetKey(token), strconv.FormatInt(offset, 10)); err != nil {
		log.Printf("[tg] save offset failed token=%s: %v", tgWebhookKey(token), err)
	}
}


// tgGetFileResp — getFile API 响应。
type tgGetFileResp struct {
	OK     bool `json:"ok"`
	Result struct {
		FileID   string `json:"file_id"`
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	} `json:"result"`
}

// tgResolveFileURL 通过 getFile API 把 file_id 解析成可下载的 URL。
// 注意 URL 包含 bot token，禁止打日志或写盘——只在内存里使用。
func tgResolveFileURL(token, fileID string) (string, error) {
	if strings.TrimSpace(fileID) == "" {
		return "", fmt.Errorf("empty file_id")
	}
	reqURL := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", token, url.QueryEscape(fileID))
	resp, err := http.Get(reqURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var payload tgGetFileResp
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if !payload.OK || payload.Result.FilePath == "" {
		return "", fmt.Errorf("tg getFile returned not ok")
	}
	return fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", token, payload.Result.FilePath), nil
}

// tgDownloadFile 把 TG file_id 解析+下载成字节，限制 50MB。
func tgDownloadFile(token, fileID string) ([]byte, error) {
	dlURL, err := tgResolveFileURL(token, fileID)
	if err != nil {
		return nil, err
	}
	return imDownloadAttachment(dlURL)
}
