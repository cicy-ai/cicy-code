// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

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
	"path"
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

	start := time.Now()
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
		log.Printf("[wechat] POST %s FAIL err=%v dur=%dms", path, err, time.Since(start).Milliseconds())
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	dur := time.Since(start).Milliseconds()
	if resp.StatusCode >= 400 {
		log.Printf("[wechat] POST %s FAIL status=%d body=%s dur=%dms", path, resp.StatusCode, strings.TrimSpace(string(respBody)), dur)
		return nil, fmt.Errorf("POST %s -> %d: %s", path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	// Only log non-poll paths at this level; sendmessage is logged by caller.
	if path != "ilink/bot/getupdates" && path != "ilink/bot/sendmessage" {
		log.Printf("[wechat] POST %s OK len=%d dur=%dms", path, len(respBody), dur)
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

// weChatCancelAllPendingLogins cancels every in-flight QR login session and
// removes it from the registry. Called when the user starts a fresh scan so a
// stale session (e.g. modal closed before cancel completed) does not block.
func weChatCancelAllPendingLogins() {
	weChatLogins.mu.Lock()
	stale := make([]*weChatLoginSession, 0, len(weChatLogins.m))
	for id, s := range weChatLogins.m {
		stale = append(stale, s)
		delete(weChatLogins.m, id)
	}
	weChatLogins.mu.Unlock()
	for _, s := range stale {
		s.cancel()
	}
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

func jsonMarshalAny(v any) string {
	if v == nil {
		return "{}"
	}
	if raw, ok := v.(map[string]any); ok && len(raw) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (t *weChatTransport) Kind() string  { return imPlatformWeChat }
func (t *weChatTransport) CanEdit() bool { return false }

type weChatTextItem struct {
	Text string `json:"text"`
}

type weChatCDNMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param"`
	AESKey            string `json:"aes_key"`
	EncryptType       int    `json:"encrypt_type"`
	FullURL           string `json:"full_url"`
}

type weChatVoiceItem struct {
	Media         *weChatCDNMedia `json:"media,omitempty"`
	EncodeType    int             `json:"encode_type"`
	BitsPerSample int             `json:"bits_per_sample"`
	SampleRate    int             `json:"sample_rate"`
	Playtime      int             `json:"playtime"`
	Text          string          `json:"text"`
}

type weChatImageItem struct {
	Media      *weChatCDNMedia `json:"media,omitempty"`
	ThumbMedia *weChatCDNMedia `json:"thumb_media,omitempty"`
	AESKey     string          `json:"aeskey"`
	URL        string          `json:"url"`
}

type weChatFileItem struct {
	Media    *weChatCDNMedia `json:"media,omitempty"`
	FileName string          `json:"file_name"`
	MD5      string          `json:"md5"`
	Len      string          `json:"len"`
}

type weChatVideoItem struct {
	Media      *weChatCDNMedia `json:"media,omitempty"`
	ThumbMedia *weChatCDNMedia `json:"thumb_media,omitempty"`
	VideoSize  int             `json:"video_size"`
	PlayLength int             `json:"play_length"`
	VideoMD5   string          `json:"video_md5"`
}

type weChatItem struct {
	Type      int              `json:"type"`
	TextItem  *weChatTextItem  `json:"text_item,omitempty"`
	VoiceItem *weChatVoiceItem `json:"voice_item,omitempty"`
	ImageItem *weChatImageItem `json:"image_item,omitempty"`
	FileItem  *weChatFileItem  `json:"file_item,omitempty"`
	VideoItem *weChatVideoItem `json:"video_item,omitempty"`
	Extra     map[string]any   `json:"-"`
}

func (it *weChatItem) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["type"]; ok {
		if n, ok := v.(float64); ok {
			it.Type = int(n)
		}
	}
	if v, ok := raw["text_item"]; ok {
		b, _ := json.Marshal(v)
		var ti weChatTextItem
		if err := json.Unmarshal(b, &ti); err == nil {
			it.TextItem = &ti
		}
	}
	if v, ok := raw["voice_item"]; ok {
		b, _ := json.Marshal(v)
		var vi weChatVoiceItem
		if err := json.Unmarshal(b, &vi); err == nil {
			it.VoiceItem = &vi
		}
	}
	if v, ok := raw["image_item"]; ok {
		b, _ := json.Marshal(v)
		var ii weChatImageItem
		if err := json.Unmarshal(b, &ii); err == nil {
			it.ImageItem = &ii
		}
	}
	if v, ok := raw["file_item"]; ok {
		b, _ := json.Marshal(v)
		var fi weChatFileItem
		if err := json.Unmarshal(b, &fi); err == nil {
			it.FileItem = &fi
		}
	}
	if v, ok := raw["video_item"]; ok {
		b, _ := json.Marshal(v)
		var vi weChatVideoItem
		if err := json.Unmarshal(b, &vi); err == nil {
			it.VideoItem = &vi
		}
	}
	delete(raw, "type")
	delete(raw, "text_item")
	delete(raw, "voice_item")
	delete(raw, "image_item")
	delete(raw, "file_item")
	delete(raw, "video_item")
	if len(raw) > 0 {
		it.Extra = raw
	}
	return nil
}

type weChatMessage struct {
	MessageType  int          `json:"message_type"`
	MessageState int          `json:"message_state,omitempty"`
	FromUserID   string       `json:"from_user_id"`
	ToUserID     string       `json:"to_user_id"`
	ClientID     string       `json:"client_id"`
	ContextToken string       `json:"context_token"`
	ItemList     []weChatItem `json:"item_list"`
	Extra        map[string]any `json:"-"`
}

func (m *weChatMessage) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["message_type"]; ok {
		if n, ok := v.(float64); ok {
			m.MessageType = int(n)
		}
	}
	if v, ok := raw["message_state"]; ok {
		if n, ok := v.(float64); ok {
			m.MessageState = int(n)
		}
	}
	if v, ok := raw["from_user_id"]; ok {
		m.FromUserID, _ = v.(string)
	}
	if v, ok := raw["to_user_id"]; ok {
		m.ToUserID, _ = v.(string)
	}
	if v, ok := raw["client_id"]; ok {
		m.ClientID, _ = v.(string)
	}
	if v, ok := raw["context_token"]; ok {
		m.ContextToken, _ = v.(string)
	}
	if v, ok := raw["item_list"]; ok {
		b, _ := json.Marshal(v)
		var items []weChatItem
		if err := json.Unmarshal(b, &items); err == nil {
			m.ItemList = items
		}
	}
	delete(raw, "message_type")
	delete(raw, "message_state")
	delete(raw, "from_user_id")
	delete(raw, "to_user_id")
	delete(raw, "client_id")
	delete(raw, "context_token")
	delete(raw, "item_list")
	if len(raw) > 0 {
		m.Extra = raw
	}
	return nil
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
		// Server-side voice transcription (if available)
		if item.Type == 3 && item.VoiceItem != nil && item.VoiceItem.Text != "" {
			parts = append(parts, item.VoiceItem.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

// weChatVoiceDownloadable returns true if the voice item has CDN media details.
func weChatVoiceDownloadable(vi *weChatVoiceItem) bool {
	if vi == nil || vi.Media == nil {
		return false
	}
	return vi.Media.EncryptQueryParam != "" || vi.Media.FullURL != ""
}

// weChatHasVoice returns true if any item in the message is a voice item.
func weChatHasVoice(items []weChatItem) bool {
	for _, item := range items {
		if item.Type == 3 {
			return true
		}
	}
	return false
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
		peer := botPeer{ChatID: strings.TrimSpace(m.FromUserID), ContextToken: strings.TrimSpace(m.ContextToken)}
		fromID := strings.TrimSpace(m.FromUserID)
		log.Printf("[im] wechat pollMsg from=%s text=%q hasVoice=%v items=%d", fromID, text, weChatHasVoice(m.ItemList), len(m.ItemList))

		if text == "" {
			// Voice message: try CDN download or getmedia endpoint for audio.
			if weChatHasVoice(m.ItemList) {
				var voiceAudio []byte
				msgID := fmt.Sprint(m.Extra["message_id"])
				for _, item := range m.ItemList {
					if item.Type == 3 && item.VoiceItem != nil {
						if weChatVoiceDownloadable(item.VoiceItem) {
							voiceAudio = weChatDownloadVoiceCDN(t, item.VoiceItem)
						}
					}
				}
				if len(voiceAudio) == 0 && msgID != "" && msgID != "<nil>" {
					voiceAudio = weChatDownloadVoiceByMsgID(t, msgID, m.ContextToken)
				}
				if len(voiceAudio) > 0 {
					msgs = append(msgs, botMsg{
						Text:        "",
						Peer:        peer,
						FromID:      fromID,
						VoiceData:   voiceAudio,
						VoiceFormat: "silk",
					})
					continue
				}
				msgs = append(msgs, botMsg{
					Text:   "[语音消息]",
					Peer:   peer,
					FromID: fromID,
				})
				continue
			}
			// 图片 / 文件 / 视频：提取 attachment（下载在 imHandleInbound 里做）。
			if atts := weChatExtractAttachments(m.ItemList); len(atts) > 0 {
				log.Printf("[im] wechat pollMsg from=%s attachments=%d kinds=%v",
					fromID, len(atts), weChatAttachmentKinds(atts))
				msgs = append(msgs, botMsg{
					Text:        "",
					Peer:        peer,
					FromID:      fromID,
					Attachments: atts,
				})
				continue
			}
			if len(m.ItemList) > 0 || len(m.Extra) > 0 {
				if raw, err := json.Marshal(m); err == nil {
					log.Printf("[im] wechat non-text msg from=%s items=%d msg_extra=%s raw=%s", fromID, len(m.ItemList), jsonMarshalAny(m.Extra), string(raw))
				}
				for i, item := range m.ItemList {
					log.Printf("[im] wechat item[%d] type=%d voice_item=%v extra=%s", i, item.Type, item.VoiceItem != nil, jsonMarshalAny(item.Extra))
				}
			}
			continue
		}
		msgs = append(msgs, botMsg{
			Text:   text,
			Peer:   peer,
			FromID: fromID,
		})
	}
	return msgs, t.updatesBuf, nil
}

// weChatDownloadVoiceCDN downloads voice audio via CDN media details.
func weChatDownloadVoiceCDN(t *weChatTransport, vi *weChatVoiceItem) []byte {
	if vi == nil || vi.Media == nil {
		return nil
	}
	cdnURL := strings.TrimSpace(vi.Media.FullURL)
	if cdnURL == "" {
		// Build CDN download URL from encrypt_query_param
		q := strings.TrimSpace(vi.Media.EncryptQueryParam)
		if q == "" {
			return nil
		}
		cdnURL = "https://novac2c.cdn.weixin.qq.com/c2c/download?encrypted_query_param=" + url.QueryEscape(q)
	}
	log.Printf("[im] wechat downloading voice from CDN: %s", cdnURL[:min(len(cdnURL), 80)])
	audio, err := weChatDownloadFile(cdnURL)
	if err != nil {
		log.Printf("[im] wechat CDN voice download failed: %v", err)
		return nil
	}
	log.Printf("[im] wechat CDN voice downloaded: %d bytes", len(audio))
	return audio
}

// weChatDownloadVoiceByMsgID tries to download voice via message-level API endpoints.
func weChatDownloadVoiceByMsgID(t *weChatTransport, msgID, contextToken string) []byte {
	// Try multiple possible endpoints
	endpoints := []string{
		fmt.Sprintf("ilink/bot/getmsgmedia?msg_id=%s", url.QueryEscape(msgID)),
		fmt.Sprintf("ilink/bot/getmedia?message_id=%s", url.QueryEscape(msgID)),
		fmt.Sprintf("ilink/bot/download?message_id=%s&type=voice", url.QueryEscape(msgID)),
	}
	for _, ep := range endpoints {
		log.Printf("[im] wechat trying endpoint: %s", ep)
		body, err := weChatGet(t.state.BaseURL, ep, 20*time.Second)
		if err != nil {
			log.Printf("[im] wechat endpoint %s failed: %v", ep, err)
			continue
		}
		if len(body) > 0 {
			log.Printf("[im] wechat endpoint %s returned %d bytes (first 100: %s)", ep, len(body), fmt.Sprintf("%x", body[:min(len(body), 100)]))
			if len(body) > 1024 {
				return body
			}
			// Small response — likely JSON error, log it
			log.Printf("[im] wechat endpoint %s small response: %s", ep, string(body))
		}
	}

	// Also try POST variants
	postEndpoints := []string{
		"ilink/bot/getmsgmedia",
		"ilink/bot/getmedia",
	}
	for _, ep := range postEndpoints {
		payload := map[string]any{
			"message_id":    msgID,
			"context_token": contextToken,
		}
		log.Printf("[im] wechat trying POST %s with msg_id=%s", ep, msgID)
		body, err := weChatPost(t.state.BaseURL, ep, t.state.BotToken, payload, 20*time.Second)
		if err != nil {
			log.Printf("[im] wechat POST %s failed: %v", ep, err)
			continue
		}
		if len(body) > 0 {
			log.Printf("[im] wechat POST %s returned %d bytes (first 100: %s)", ep, len(body), fmt.Sprintf("%x", body[:min(len(body), 100)]))
			if len(body) > 1024 {
				return body
			}
			log.Printf("[im] wechat POST %s small response: %s", ep, string(body))
		}
	}

	return nil
}

// weChatDownloadFile downloads a file from the given URL.
func weChatDownloadFile(rawURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	weChatCommonHeaders(req.Header)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, voiceMaxBytes))
}

func (t *weChatTransport) Send(peer botPeer, text string) (string, error) {
	if peer.empty() {
		return "", fmt.Errorf("no wechat user id")
	}
	text = imClampMessage(text)
	ctxToken := peer.ContextToken
	if ctxToken != "" && !imContextTokenStillValid(t.accID) {
		ctxToken = ""
	}
	msg := weChatMessage{
		MessageType:  weChatMsgTypeBot,
		MessageState: weChatMsgStateFinish,
		FromUserID:   "",
		ToUserID:     peer.ChatID,
		ClientID:     weChatClientID(),
		ContextToken: ctxToken,
		ItemList:     []weChatItem{{Type: weChatItemTypeText, TextItem: &weChatTextItem{Text: text}}},
	}
	respBody, err := weChatPost(t.state.BaseURL, "ilink/bot/sendmessage", t.state.BotToken, map[string]any{
		"msg": msg,
	}, 20*time.Second)
	if err != nil {
		return "", err
	}
	var resp struct {
		Ret int    `json:"ret"`
		Msg string `json:"errmsg"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", nil
	}
	if resp.Ret != 0 {
		// ret=-2 = outside the user's active session window (ilink won't let a bot
		// push proactively). Surface a typed error so callers (e.g. the connectivity
		// test) can explain it instead of showing a raw "ret=-2".
		if resp.Ret == -2 {
			return "", errWeChatNoActiveSession
		}
		return "", fmt.Errorf("ilink ret=%d msg=%s", resp.Ret, resp.Msg)
	}
	return "", nil
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


// weChatExtractAttachments 把 weChatItemList 中的 image / file / video 转成 botAttachment。
// 优先用 item.URL 字段（直接的可下载 URL），否则尝试从 Media.FullURL / EncryptQueryParam
// 构造 CDN URL。Bytes 字段留空，让 imHandleInbound 阶段统一下载。
func weChatExtractAttachments(items []weChatItem) []botAttachment {
	var out []botAttachment
	for _, item := range items {
		switch {
		case item.ImageItem != nil:
			if att := weChatBuildAttachmentFromImage(item.ImageItem); att != nil {
				out = append(out, *att)
			}
		case item.FileItem != nil:
			if att := weChatBuildAttachmentFromFile(item.FileItem); att != nil {
				out = append(out, *att)
			}
		case item.VideoItem != nil:
			if att := weChatBuildAttachmentFromVideo(item.VideoItem); att != nil {
				out = append(out, *att)
			}
		}
	}
	return out
}

func weChatAttachmentKinds(atts []botAttachment) []string {
	out := make([]string, 0, len(atts))
	for _, a := range atts {
		out = append(out, a.Kind)
	}
	return out
}

func weChatBuildAttachmentFromImage(ii *weChatImageItem) *botAttachment {
	if ii == nil {
		return nil
	}
	urlStr := strings.TrimSpace(ii.URL)
	if urlStr == "" {
		urlStr = weChatCDNDownloadURL(ii.Media)
	}
	if urlStr == "" {
		return nil
	}
	// image: aes_key 来源优先 image_item.aeskey（32 字符 hex string），
	// fallback 到 media.aes_key（base64 编码的 16 raw byte 或 32 ASCII hex）。
	var key []byte
	if k, err := hex.DecodeString(strings.TrimSpace(ii.AESKey)); err == nil && len(k) == 16 {
		key = k
	} else if ii.Media != nil {
		if k := weChatParseAESKey(ii.Media.AESKey); len(k) == 16 {
			key = k
		}
	}
	return &botAttachment{
		Kind:     "image",
		Filename: weChatGuessFilename("image", "image.jpg", ii.URL),
		URL:      urlStr,
		AESKey:   key,
	}
}

func weChatBuildAttachmentFromFile(fi *weChatFileItem) *botAttachment {
	if fi == nil {
		return nil
	}
	urlStr := weChatCDNDownloadURL(fi.Media)
	if urlStr == "" {
		return nil
	}
	name := strings.TrimSpace(fi.FileName)
	if name == "" {
		name = "file.bin"
	}
	var size int64
	if n, err := strconv.ParseInt(strings.TrimSpace(fi.Len), 10, 64); err == nil {
		size = n
	}
	var key []byte
	if fi.Media != nil {
		key = weChatParseAESKey(fi.Media.AESKey)
	}
	return &botAttachment{
		Kind:     "file",
		Filename: name,
		URL:      urlStr,
		MD5:      strings.TrimSpace(fi.MD5),
		Size:     size,
		AESKey:   key,
	}
}

func weChatBuildAttachmentFromVideo(vi *weChatVideoItem) *botAttachment {
	if vi == nil {
		return nil
	}
	urlStr := weChatCDNDownloadURL(vi.Media)
	if urlStr == "" {
		return nil
	}
	var key []byte
	if vi.Media != nil {
		key = weChatParseAESKey(vi.Media.AESKey)
	}
	return &botAttachment{
		Kind:     "video",
		Filename: "video.mp4",
		URL:      urlStr,
		MD5:      strings.TrimSpace(vi.VideoMD5),
		Size:     int64(vi.VideoSize),
		AESKey:   key,
	}
}

// weChatParseAESKey 解析 weChatCDNMedia.aes_key 字段成 16-byte raw AES key。
// 两种 encoding（来自 ilink-bot 协议）：
//   - base64(raw 16 bytes)             → image (media.aes_key 字段)
//   - base64(32-char ASCII hex string) → file / voice / video
//
// 返回 nil 表示无法解析（aes_key 空或格式错误）。
func weChatParseAESKey(aesKeyBase64 string) []byte {
	s := strings.TrimSpace(aesKeyBase64)
	if s == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	if len(decoded) == 16 {
		return decoded
	}
	if len(decoded) == 32 {
		// 检查是不是 32 字符 ASCII hex
		ascii := string(decoded)
		if k, err := hex.DecodeString(ascii); err == nil && len(k) == 16 {
			return k
		}
	}
	return nil
}

// weChatCDNDownloadURL 从 weChatCDNMedia 构造可下载的 CDN URL。
// 优先 FullURL；次选 EncryptQueryParam（拼成 c2c download URL）。
func weChatCDNDownloadURL(m *weChatCDNMedia) string {
	if m == nil {
		return ""
	}
	if u := strings.TrimSpace(m.FullURL); u != "" {
		return u
	}
	if q := strings.TrimSpace(m.EncryptQueryParam); q != "" {
		return "https://novac2c.cdn.weixin.qq.com/c2c/download?encrypted_query_param=" + url.QueryEscape(q)
	}
	return ""
}

// weChatGuessFilename 给 image / video 生成默认文件名（如果原数据没带文件名）。
func weChatGuessFilename(kind, fallback string, hintURL string) string {
	hintURL = strings.TrimSpace(hintURL)
	if hintURL != "" {
		// 尝试从 URL path 提取
		if u, err := url.Parse(hintURL); err == nil && u.Path != "" {
			seg := path.Base(u.Path)
			if seg != "" && seg != "." && seg != "/" && strings.Contains(seg, ".") {
				return seg
			}
		}
	}
	return fallback
}
