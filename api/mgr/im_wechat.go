package main

// WeChat IM transport — a Go port of the ilink-bot protocol used by the
// `cicy-wechat` npm package (https://ilinkai.weixin.qq.com). It is "bot-shaped":
// a QR-login obtains a bot token, then getupdates / sendmessage / sendtyping work
// like a Telegram bot. Persisted login state lives under ~/cicy-ai/db/.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	weChatAPIBase         = "https://ilinkai.weixin.qq.com"
	weChatChannelVersion  = "0.2.9"
	weChatClientVersion   = (0 << 16) | (2 << 8) | 9 // 0x000209
	weChatBotType         = "3"
	weChatMsgTypeUser     = 1
	weChatMsgTypeBot      = 2
	weChatItemTypeText    = 1
	weChatMsgStateFinish  = 2
	weChatQRLoginDeadline = 8 * time.Minute
	weChatMaxQRRefresh    = 3
)

// ── persisted login state ─────────────────────────────────────────────

type weChatState struct {
	BotToken      string `json:"bot_token"`
	BaseURL       string `json:"base_url"`
	ILinkUserID   string `json:"ilink_user_id"`
	ILinkBotID    string `json:"ilink_bot_id"`
	GetUpdatesBuf string `json:"get_updates_buf"`
}

func weChatStatePath(accID int64) string {
	return filepath.Join(cicyDBDir, fmt.Sprintf("im-wechat-%d.json", accID))
}

func weChatLoadState(accID int64) *weChatState {
	body, err := os.ReadFile(weChatStatePath(accID))
	if err != nil {
		return nil
	}
	var st weChatState
	if err := json.Unmarshal(body, &st); err != nil {
		return nil
	}
	if strings.TrimSpace(st.BotToken) == "" {
		return nil
	}
	if strings.TrimSpace(st.BaseURL) == "" {
		st.BaseURL = weChatAPIBase
	}
	return &st
}

func weChatSaveState(accID int64, st *weChatState) {
	if err := os.MkdirAll(cicyDBDir, 0755); err != nil {
		log.Printf("[im-wechat] mkdir db dir failed: %v", err)
		return
	}
	body, _ := json.MarshalIndent(st, "", "  ")
	tmp := weChatStatePath(accID) + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0600); err != nil {
		log.Printf("[im-wechat] write state failed acc=%d: %v", accID, err)
		return
	}
	_ = os.Rename(tmp, weChatStatePath(accID))
}

func weChatRemoveState(accID int64) {
	_ = os.Remove(weChatStatePath(accID))
}

// ── HTTP helpers ──────────────────────────────────────────────────────

func weChatCommonHeaders(h http.Header) {
	h.Set("iLink-App-Id", "bot")
	h.Set("iLink-App-ClientVersion", fmt.Sprintf("%d", weChatClientVersion))
}

func weChatRandomUIN() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	n := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", n)))
}

func weChatClientID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "cicy-wechat-" + hex.EncodeToString(b[:])
}

func weChatBaseInfo() map[string]any {
	return map[string]any{"channel_version": weChatChannelVersion}
}

func weChatJoinURL(base, path string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = weChatAPIBase
	}
	return base + "/" + strings.TrimLeft(path, "/")
}

func weChatGet(base, path string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, weChatJoinURL(base, path), nil)
	if err != nil {
		return nil, err
	}
	weChatCommonHeaders(req.Header)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GET %s -> %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func weChatPost(base, path, token string, payload map[string]any, timeout time.Duration) ([]byte, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["base_info"] = weChatBaseInfo()
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, weChatJoinURL(base, path), strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("X-WECHAT-UIN", weChatRandomUIN())
	weChatCommonHeaders(req.Header)
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("POST %s -> %d: %s", path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

// ── QR login ──────────────────────────────────────────────────────────

type weChatQRCodeResp struct {
	Qrcode           string `json:"qrcode"`
	QrcodeImgContent string `json:"qrcode_img_content"`
}

type weChatQRStatusResp struct {
	Status       string `json:"status"` // wait|scaned|confirmed|expired|scaned_but_redirect
	BotToken     string `json:"bot_token"`
	ILinkBotID   string `json:"ilink_bot_id"`
	BaseURL      string `json:"baseurl"`
	ILinkUserID  string `json:"ilink_user_id"`
	RedirectHost string `json:"redirect_host"`
}

func weChatFetchQRCode() (*weChatQRCodeResp, error) {
	body, err := weChatGet(weChatAPIBase, "ilink/bot/get_bot_qrcode?bot_type="+url.QueryEscape(weChatBotType), 15*time.Second)
	if err != nil {
		return nil, err
	}
	var qr weChatQRCodeResp
	if err := json.Unmarshal(body, &qr); err != nil {
		return nil, err
	}
	if strings.TrimSpace(qr.Qrcode) == "" {
		return nil, fmt.Errorf("empty qrcode")
	}
	return &qr, nil
}

func weChatPollQRStatus(base, qrcode string) weChatQRStatusResp {
	body, err := weChatGet(base, "ilink/bot/get_qrcode_status?qrcode="+url.QueryEscape(qrcode), 38*time.Second)
	if err != nil {
		// long-poll/gateway timeout is normal; treat as "wait"
		return weChatQRStatusResp{Status: "wait"}
	}
	var st weChatQRStatusResp
	if err := json.Unmarshal(body, &st); err != nil {
		return weChatQRStatusResp{Status: "wait"}
	}
	if strings.TrimSpace(st.Status) == "" {
		st.Status = "wait"
	}
	return st
}

// weChatDoQRLogin runs the full QR-login state machine, updating acc state/config
// so the UI can show the QR. Returns the persisted state on success.
func weChatDoQRLogin(acc *imAccount) (*weChatState, error) {
	qr, err := weChatFetchQRCode()
	if err != nil {
		imSetAccountState(acc.ID, "error", "get qrcode failed: "+err.Error())
		return nil, err
	}
	acc.setConfig("qrcode_url", qr.QrcodeImgContent)
	imSaveAccountConfig(acc)
	imSetAccountState(acc.ID, "qr_wait", "扫码登录中")
	log.Printf("[im-wechat] acc=%d qr ready: %s", acc.ID, qr.QrcodeImgContent)

	pollBase := weChatAPIBase
	deadline := time.Now().Add(weChatQRLoginDeadline)
	refreshCount := 0
	scaned := false

	for time.Now().Before(deadline) {
		st := weChatPollQRStatus(pollBase, qr.Qrcode)
		switch st.Status {
		case "wait":
			// keep polling
		case "scaned":
			if !scaned {
				scaned = true
				imSetAccountState(acc.ID, "scaned", "已扫码，请在微信确认")
			}
		case "scaned_but_redirect":
			if h := strings.TrimSpace(st.RedirectHost); h != "" {
				pollBase = "https://" + h
				log.Printf("[im-wechat] acc=%d redirect polling host to %s", acc.ID, pollBase)
			}
		case "expired":
			refreshCount++
			if refreshCount > weChatMaxQRRefresh {
				imSetAccountState(acc.ID, "error", "二维码多次过期，请重新发起登录")
				return nil, fmt.Errorf("qrcode expired %d times", weChatMaxQRRefresh)
			}
			newQR, ferr := weChatFetchQRCode()
			if ferr != nil {
				imSetAccountState(acc.ID, "error", "刷新二维码失败: "+ferr.Error())
				return nil, ferr
			}
			qr = newQR
			pollBase = weChatAPIBase
			scaned = false
			acc.setConfig("qrcode_url", qr.QrcodeImgContent)
			imSaveAccountConfig(acc)
			log.Printf("[im-wechat] acc=%d qr refreshed (%d/%d)", acc.ID, refreshCount, weChatMaxQRRefresh)
		case "confirmed":
			if strings.TrimSpace(st.ILinkBotID) == "" {
				imSetAccountState(acc.ID, "error", "登录返回缺少 ilink_bot_id")
				return nil, fmt.Errorf("confirmed but no ilink_bot_id")
			}
			state := &weChatState{
				BotToken:    strings.TrimSpace(st.BotToken),
				BaseURL:     strings.TrimSpace(firstNonEmpty(st.BaseURL, weChatAPIBase)),
				ILinkUserID: strings.TrimSpace(st.ILinkUserID),
				ILinkBotID:  strings.TrimSpace(st.ILinkBotID),
			}
			weChatSaveState(acc.ID, state)
			acc.setConfig("qrcode_url", "")
			if state.ILinkUserID != "" {
				acc.setConfig("ilink_user_id", state.ILinkUserID)
			}
			imSaveAccountConfig(acc)
			imSetAccountState(acc.ID, "connected", "")
			log.Printf("[im-wechat] acc=%d login confirmed bot_id=%s user=%s", acc.ID, state.ILinkBotID, state.ILinkUserID)
			return state, nil
		}
		time.Sleep(1 * time.Second)
	}
	imSetAccountState(acc.ID, "error", "登录超时")
	return nil, fmt.Errorf("qr login timeout")
}

// ── standalone QR login (for adding a NEW wechat account via a modal) ──────────
//
// Adding a wechat account: the UI starts a login session, polls it, and the
// im_accounts row + state file are created only once the scan is confirmed.

type weChatLoginSession struct {
	ID        string
	mu        sync.Mutex
	State     string // qr_wait | scaned | confirmed | created | expired | error
	QRCodeURL string
	Detail    string
	AccountID int64
	stop      chan struct{}
	createdAt time.Time
}

var weChatLogins = struct {
	mu sync.Mutex
	m  map[string]*weChatLoginSession
}{m: map[string]*weChatLoginSession{}}

func weChatGetLoginSession(id string) *weChatLoginSession {
	weChatLogins.mu.Lock()
	defer weChatLogins.mu.Unlock()
	return weChatLogins.m[strings.TrimSpace(id)]
}

func (s *weChatLoginSession) snapshot() (state, qrcodeURL, detail string, accID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.State, s.QRCodeURL, s.Detail, s.AccountID
}
func (s *weChatLoginSession) set(state, qrcodeURL, detail string) {
	s.mu.Lock()
	if state != "" {
		s.State = state
	}
	if qrcodeURL != "" {
		s.QRCodeURL = qrcodeURL
	}
	s.Detail = detail
	s.mu.Unlock()
}
func (s *weChatLoginSession) stopped() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}
func (s *weChatLoginSession) cancel() {
	s.mu.Lock()
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	s.mu.Unlock()
}

// weChatHasPendingLogin reports whether a standalone wechat login is currently in progress.
func weChatHasPendingLogin() bool {
	weChatLogins.mu.Lock()
	defer weChatLogins.mu.Unlock()
	for _, s := range weChatLogins.m {
		switch func() string { st, _, _, _ := s.snapshot(); return st }() {
		case "qr_wait", "scaned", "confirmed":
			return true
		}
	}
	return false
}

func weChatStartLoginSession() (*weChatLoginSession, error) {
	qr, err := weChatFetchQRCode()
	if err != nil {
		return nil, err
	}
	s := &weChatLoginSession{
		ID:        "wxlogin-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		State:     "qr_wait",
		QRCodeURL: qr.QrcodeImgContent,
		stop:      make(chan struct{}),
		createdAt: time.Now(),
	}
	weChatLogins.mu.Lock()
	weChatLogins.m[s.ID] = s
	weChatLogins.mu.Unlock()
	go s.runLoop(qr)
	go func() {
		select {
		case <-time.After(11 * time.Minute):
		case <-s.stop:
			time.Sleep(2 * time.Second)
		}
		weChatLogins.mu.Lock()
		delete(weChatLogins.m, s.ID)
		weChatLogins.mu.Unlock()
	}()
	return s, nil
}

func (s *weChatLoginSession) runLoop(qr *weChatQRCodeResp) {
	pollBase := weChatAPIBase
	deadline := time.Now().Add(weChatQRLoginDeadline)
	refreshCount := 0
	scaned := false
	for time.Now().Before(deadline) {
		if s.stopped() {
			return
		}
		st := weChatPollQRStatus(pollBase, qr.Qrcode)
		switch st.Status {
		case "wait":
		case "scaned":
			if !scaned {
				scaned = true
				s.set("scaned", "", "已扫码，请在微信确认")
			}
		case "scaned_but_redirect":
			if h := strings.TrimSpace(st.RedirectHost); h != "" {
				pollBase = "https://" + h
			}
		case "expired":
			refreshCount++
			if refreshCount > weChatMaxQRRefresh {
				s.set("expired", "", "二维码多次过期，请重新生成")
				return
			}
			newQR, ferr := weChatFetchQRCode()
			if ferr != nil {
				s.set("error", "", "刷新二维码失败: "+ferr.Error())
				return
			}
			qr = newQR
			pollBase = weChatAPIBase
			scaned = false
			s.set("qr_wait", qr.QrcodeImgContent, "")
		case "confirmed":
			if strings.TrimSpace(st.ILinkBotID) == "" {
				s.set("error", "", "登录返回缺少 ilink_bot_id")
				return
			}
			state := &weChatState{
				BotToken:    strings.TrimSpace(st.BotToken),
				BaseURL:     strings.TrimSpace(firstNonEmpty(st.BaseURL, weChatAPIBase)),
				ILinkUserID: strings.TrimSpace(st.ILinkUserID),
				ILinkBotID:  strings.TrimSpace(st.ILinkBotID),
			}
			accID, cerr := weChatCreateAccountFromState(state)
			if cerr != nil {
				s.set("error", "", "创建账号失败: "+cerr.Error())
				return
			}
			s.mu.Lock()
			s.State = "created"
			s.AccountID = accID
			s.Detail = ""
			s.mu.Unlock()
			log.Printf("[im-wechat] login session %s -> account=%d user=%s", s.ID, accID, state.ILinkUserID)
			return
		}
		time.Sleep(1 * time.Second)
	}
	s.set("expired", "", "登录超时，请重新生成二维码")
}

func weChatCreateAccountFromState(state *weChatState) (int64, error) {
	if store == nil {
		return 0, fmt.Errorf("store not ready")
	}
	name := "微信"
	if u := strings.TrimSpace(state.ILinkUserID); u != "" {
		if len(u) > 6 {
			name = "微信 " + u[len(u)-6:]
		} else {
			name = "微信 " + u
		}
	}
	cfg := map[string]any{}
	if state.ILinkUserID != "" {
		cfg["ilink_user_id"] = state.ILinkUserID
	}
	if state.ILinkBotID != "" {
		cfg["ilink_bot_id"] = state.ILinkBotID
	}
	if state.BaseURL != "" {
		cfg["base_url"] = state.BaseURL
	}
	// insert disabled first so the worker can't start before the state file is written
	res, err := store.Exec(
		"INSERT INTO im_accounts (platform, name, secret, config, enabled, state, state_detail, inbound_to_agent) VALUES ('wechat',?,?,?,0,'connected','',1)",
		name, "stored-in-db-dir", imJSON(cfg),
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	weChatSaveState(id, state)
	if _, err := store.Exec("UPDATE im_accounts SET enabled=1, updated_at="+store.Now()+" WHERE id=?", id); err != nil {
		return id, err
	}
	imReconcileAccount(id)
	return id, nil
}

// ── transport ─────────────────────────────────────────────────────────

type weChatTransport struct {
	accID      int64
	state      *weChatState
	updatesBuf string
}

func newWeChatTransport(acc *imAccount) (botTransport, error) {
	st := weChatLoadState(acc.ID)
	if st == nil {
		var err error
		st, err = weChatDoQRLogin(acc)
		if err != nil {
			return nil, err
		}
	}
	imSetAccountSecret(acc.ID, "stored-in-db-dir")
	return &weChatTransport{accID: acc.ID, state: st, updatesBuf: st.GetUpdatesBuf}, nil
}

func (t *weChatTransport) Kind() string  { return imPlatformWeChat }
func (t *weChatTransport) CanEdit() bool { return false }

type weChatTextItem struct {
	Text string `json:"text"`
}
type weChatItem struct {
	Type     int             `json:"type"`
	TextItem *weChatTextItem `json:"text_item,omitempty"`
}
type weChatMessage struct {
	MessageType  int          `json:"message_type"`
	MessageState int          `json:"message_state,omitempty"`
	FromUserID   string       `json:"from_user_id"`
	ToUserID     string       `json:"to_user_id"`
	ClientID     string       `json:"client_id"`
	ContextToken string       `json:"context_token"`
	ItemList     []weChatItem `json:"item_list"`
}
type weChatGetUpdatesResp struct {
	Ret           int             `json:"ret"`
	Errcode       int             `json:"errcode"`
	Errmsg        string          `json:"errmsg"`
	GetUpdatesBuf string          `json:"get_updates_buf"`
	Msgs          []weChatMessage `json:"msgs"`
}

func weChatExtractText(m weChatMessage) string {
	var parts []string
	for _, item := range m.ItemList {
		if item.Type == weChatItemTypeText && item.TextItem != nil && strings.TrimSpace(item.TextItem.Text) != "" {
			parts = append(parts, item.TextItem.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func (t *weChatTransport) Poll(_ string) ([]botMsg, string, error) {
	body, err := weChatPost(t.state.BaseURL, "ilink/bot/getupdates", t.state.BotToken, map[string]any{
		"get_updates_buf": t.updatesBuf,
	}, 42*time.Second)
	if err != nil {
		return nil, t.updatesBuf, err
	}
	var resp weChatGetUpdatesResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, t.updatesBuf, err
	}
	if resp.Errcode == -14 {
		// session expired — drop the persisted token so the worker re-runs QR login
		weChatRemoveState(t.accID)
		return nil, t.updatesBuf, errIMSessionExpired
	}
	if resp.Errcode != 0 {
		return nil, t.updatesBuf, fmt.Errorf("getupdates errcode=%d errmsg=%s", resp.Errcode, resp.Errmsg)
	}
	if strings.TrimSpace(resp.GetUpdatesBuf) != "" {
		t.updatesBuf = resp.GetUpdatesBuf
		t.state.GetUpdatesBuf = resp.GetUpdatesBuf
		weChatSaveState(t.accID, t.state)
	}
	var msgs []botMsg
	for _, m := range resp.Msgs {
		if m.MessageType != weChatMsgTypeUser {
			continue
		}
		text := weChatExtractText(m)
		if text == "" {
			continue
		}
		msgs = append(msgs, botMsg{
			Text:   text,
			Peer:   botPeer{ChatID: strings.TrimSpace(m.FromUserID), ContextToken: strings.TrimSpace(m.ContextToken)},
			FromID: strings.TrimSpace(m.FromUserID),
		})
	}
	return msgs, t.updatesBuf, nil
}

func (t *weChatTransport) Send(peer botPeer, text string) (string, error) {
	if peer.empty() {
		return "", fmt.Errorf("no wechat user id")
	}
	text = imClampMessage(text)
	msg := weChatMessage{
		MessageType:  weChatMsgTypeBot,
		MessageState: weChatMsgStateFinish,
		FromUserID:   "",
		ToUserID:     peer.ChatID,
		ClientID:     weChatClientID(),
		ContextToken: peer.ContextToken,
		ItemList:     []weChatItem{{Type: weChatItemTypeText, TextItem: &weChatTextItem{Text: text}}},
	}
	_, err := weChatPost(t.state.BaseURL, "ilink/bot/sendmessage", t.state.BotToken, map[string]any{
		"msg": msg,
	}, 20*time.Second)
	return "", err
}

func (t *weChatTransport) Edit(_ botPeer, _ string, _ string) error {
	return errBotEditUnsupported
}

type weChatGetConfigResp struct {
	TypingTicket string `json:"typing_ticket"`
}

func (t *weChatTransport) Typing(peer botPeer) error {
	if peer.empty() {
		return nil
	}
	body, err := weChatPost(t.state.BaseURL, "ilink/bot/getconfig", t.state.BotToken, map[string]any{
		"ilink_user_id": peer.ChatID,
		"context_token": peer.ContextToken,
	}, 10*time.Second)
	if err != nil {
		return err
	}
	var cfg weChatGetConfigResp
	if err := json.Unmarshal(body, &cfg); err != nil || strings.TrimSpace(cfg.TypingTicket) == "" {
		return nil
	}
	_, err = weChatPost(t.state.BaseURL, "ilink/bot/sendtyping", t.state.BotToken, map[string]any{
		"ilink_user_id": peer.ChatID,
		"typing_ticket": cfg.TypingTicket,
		"typing_status": 1,
	}, 10*time.Second)
	return err
}
