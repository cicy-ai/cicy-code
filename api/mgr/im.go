// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// IM platform integration: register Telegram bots / WeChat (ilink) accounts,
// bind them to agent panes, route inbound messages into the pane via /api/tmux/send
// and stream the agent reply back out through the AI-gateway reply hook.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// errBotEditUnsupported is returned by transports that cannot edit a sent message.
var errBotEditUnsupported = errors.New("bot transport does not support editing messages")

// errWeChatNoActiveSession is returned by the WeChat transport when ilink rejects
// a send with ret=-2 — the bot is outside the user's active session window, so it
// cannot proactively push. ilink bots can only reply within a short window after
// the user messages them; a cold "test"/proactive send always hits this.
var errWeChatNoActiveSession = errors.New("wechat: no active session window (ilink ret=-2); the user must message the bot first")

const (
	imPlatformTelegram = "telegram"
	imPlatformWeChat   = "wechat"
	imPlatformFeishu   = "feishu"
)

// botPeer identifies who/where to send a message. Telegram only uses ChatID;
// WeChat needs the target user id plus the per-message context token.
type botPeer struct {
	ChatID       string `json:"chat_id"`
	ContextToken string `json:"context_token,omitempty"`
}

func (p botPeer) empty() bool { return strings.TrimSpace(p.ChatID) == "" }

// botMsg is a normalized inbound message.
// botAttachment 描述 IM 消息中携带的非文本媒体（图片 / 文件 / 视频）。
// transport 在 Poll 时填充 URL 或 Bytes（任选其一），imHandleInbound 负责
// 把内容下载并落到 workspace 的 inbox 目录。
//
// AESKey 非空时表示下载下来的字节是 AES-128-ECB + PKCS7 加密的，
// imSaveAttachmentsToInbox 会自动解密。WeChat ilink-bot 协议下大多数
// 媒体（image / file / voice / video）都用这种加密方式。
type botAttachment struct {
	Kind     string // "image" | "file" | "video"
	Filename string // 原始文件名（含扩展名），可空
	URL      string // 下载 URL（CDN 或公开 URL）
	Bytes    []byte // 已经下载好的字节（与 URL 二选一）
	MD5      string // 校验，可空
	Size     int64  // 字节数（来自 transport 元数据），可空
	AESKey   []byte // 16-byte AES-128-ECB key；nil = 明文，无需解密
}

type botMsg struct {
	Text        string
	Peer        botPeer
	FromID      string
	LangCode    string // e.g. "zh-hans", "en", "ja", "fr"
	VoiceData   []byte // raw audio bytes (nil if not voice)
	VoiceFormat string // "silk", "amr", "ogg", etc.
	Attachments []botAttachment
}

// botTransport abstracts a "bot-shaped" IM platform: a token, long-poll updates,
// send/edit message, typing indicator.
type botTransport interface {
	Kind() string
	// Poll long-polls for new messages starting at cursor; returns messages,
	// the next cursor, and an error. A nil error with no messages is fine.
	Poll(cursor string) (msgs []botMsg, nextCursor string, err error)
	// Send delivers text to peer; returns an opaque message id (may be "").
	Send(peer botPeer, text string) (messageID string, err error)
	// Edit replaces an existing message's text; returns errBotEditUnsupported if not possible.
	Edit(peer botPeer, messageID, text string) error
	// Typing sends a "typing" indicator (best-effort; may be a no-op).
	Typing(peer botPeer) error
	// CanEdit reports whether Edit is supported (used to decide streaming vs. send-once).
	CanEdit() bool
}

/* ───────────────────────── account store ───────────────────────── */

type imAccount struct {
	ID             int64
	Platform       string
	Name           string
	Secret         string
	Config         map[string]any
	Enabled        bool
	State          string
	StateDetail    string
	BoundPaneID    string
	InboundToAgent bool
	CreatedAt      string
	UpdatedAt      string
}

func (a *imAccount) configString(key string) string {
	if a.Config == nil {
		return ""
	}
	v, _ := a.Config[key].(string)
	return strings.TrimSpace(v)
}

func (a *imAccount) setConfig(key string, val any) {
	if a.Config == nil {
		a.Config = map[string]any{}
	}
	if val == nil || (fmt.Sprintf("%v", val) == "") {
		delete(a.Config, key)
		return
	}
	a.Config[key] = val
}

func imLogPreview(text string) string {
	const maxLen = 120
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, text)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if len(cleaned) > maxLen {
		return cleaned[:maxLen] + "..."
	}
	return cleaned
}

type imOutboundPurpose string

const (
	imOutboundPurposeUnknown      imOutboundPurpose = "unknown"
	imOutboundPurposeAck          imOutboundPurpose = "ack"
	imOutboundPurposeReply        imOutboundPurpose = "reply"
	imOutboundPurposeError        imOutboundPurpose = "error"
	imOutboundPurposeTest         imOutboundPurpose = "test"
	imOutboundPurposeProgrammatic imOutboundPurpose = "programmatic"
)

type imOutboundMessage struct {
	AccountID int64
	Transport botTransport
	Peer      botPeer
	Text      string
	Purpose   imOutboundPurpose
}

type imOutboundResult struct {
	AccountID int64   `json:"account_id"`
	Platform  string  `json:"platform"`
	Peer      botPeer `json:"peer"`
	MessageID string  `json:"message_id"`
}

func (m imOutboundMessage) purpose() imOutboundPurpose {
	if strings.TrimSpace(string(m.Purpose)) == "" {
		return imOutboundPurposeUnknown
	}
	return m.Purpose
}

// imSendOutbound is the single outbound IM dispatch path. Platform-specific
// code belongs behind botTransport; callers provide intent, target, and text.
// Adding Telegram/WeChat/Discord/etc. should not require new send branches in
// reply hooks, tests, or agent-facing APIs.
func imSendOutbound(msg imOutboundMessage) (imOutboundResult, error) {
	res := imOutboundResult{AccountID: msg.AccountID, Peer: msg.Peer}
	if msg.Transport == nil {
		return res, fmt.Errorf("im transport not connected")
	}
	res.Platform = msg.Transport.Kind()
	if msg.Peer.empty() {
		return res, fmt.Errorf("no send target")
	}
	text := imClampMessage(msg.Text)
	mid, err := msg.Transport.Send(msg.Peer, text)
	if err != nil {
		log.Printf("[im] send FAIL account=%d kind=%s purpose=%s to=%s len=%d preview=%q err=%v", msg.AccountID, res.Platform, msg.purpose(), msg.Peer.ChatID, len(text), imLogPreview(text), err)
		return res, err
	}
	res.MessageID = mid
	log.Printf("[im] send OK account=%d kind=%s purpose=%s to=%s len=%d preview=%q", msg.AccountID, res.Platform, msg.purpose(), msg.Peer.ChatID, len(text), imLogPreview(text))
	return res, nil
}

// imSendMessage is kept as a small compatibility wrapper. New call sites should
// prefer imSendOutbound with an explicit purpose.
func imSendMessage(tr botTransport, accID int64, peer botPeer, text string) (string, error) {
	res, err := imSendOutbound(imOutboundMessage{
		AccountID: accID,
		Transport: tr,
		Peer:      peer,
		Text:      text,
		Purpose:   imOutboundPurposeUnknown,
	})
	return res.MessageID, err
}

func imScanAccount(scan func(dest ...any) error) (*imAccount, error) {
	var (
		id          int64
		platform    string
		name        string
		secret      string
		configStr   string
		enabled     int
		state       string
		stateDetail string
		boundPane   string
		inbound     int
		createdAt   string
		updatedAt   string
	)
	if err := scan(&id, &platform, &name, &secret, &configStr, &enabled, &state, &stateDetail, &boundPane, &inbound, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	cfg := map[string]any{}
	if strings.TrimSpace(configStr) != "" {
		_ = json.Unmarshal([]byte(configStr), &cfg)
		if cfg == nil {
			cfg = map[string]any{}
		}
	}
	return &imAccount{
		ID: id, Platform: normalizeIMPlatform(platform), Name: name, Secret: secret, Config: cfg,
		Enabled: enabled != 0, State: state, StateDetail: stateDetail,
		BoundPaneID: strings.TrimSpace(boundPane), InboundToAgent: inbound != 0,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

const imAccountColumns = "id, platform, COALESCE(name,''), COALESCE(secret,''), COALESCE(config,'{}'), COALESCE(enabled,1), COALESCE(state,'pending'), COALESCE(state_detail,''), COALESCE(bound_pane_id,''), COALESCE(inbound_to_agent,1), COALESCE(created_at,''), COALESCE(updated_at,'')"

func imListAccounts() ([]*imAccount, error) {
	if store == nil {
		return nil, fmt.Errorf("store not ready")
	}
	rows, err := store.Query("SELECT " + imAccountColumns + " FROM im_accounts ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*imAccount
	for rows.Next() {
		acc, err := imScanAccount(rows.Scan)
		if err != nil {
			continue
		}
		out = append(out, acc)
	}
	return out, nil
}

func imGetAccount(id int64) (*imAccount, error) {
	if store == nil {
		return nil, fmt.Errorf("store not ready")
	}
	row := store.QueryRow("SELECT "+imAccountColumns+" FROM im_accounts WHERE id=?", id)
	return imScanAccount(row.Scan)
}

func imAccountsForPane(paneID string) []*imAccount {
	paneID = normPaneID(strings.TrimSpace(paneID))
	if paneID == "" || store == nil {
		return nil
	}
	rows, err := store.Query("SELECT "+imAccountColumns+" FROM im_accounts WHERE bound_pane_id=? AND COALESCE(enabled,1)=1", paneID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*imAccount
	for rows.Next() {
		acc, err := imScanAccount(rows.Scan)
		if err != nil {
			continue
		}
		out = append(out, acc)
	}
	return out
}

func imSaveAccountConfig(acc *imAccount) {
	if acc == nil || store == nil {
		return
	}
	body, _ := json.Marshal(acc.Config)
	if _, err := store.Exec("UPDATE im_accounts SET config=?, updated_at="+store.Now()+" WHERE id=?", string(body), acc.ID); err != nil {
		log.Printf("[im] save config failed id=%d: %v", acc.ID, err)
	}
}

func imSetAccountState(id int64, state, detail string) {
	if store == nil {
		return
	}
	if _, err := store.Exec("UPDATE im_accounts SET state=?, state_detail=?, updated_at="+store.Now()+" WHERE id=?", state, detail, id); err != nil {
		log.Printf("[im] set state failed id=%d: %v", id, err)
	}
}

func imAccountStateIs(id int64, state string) bool {
	if store == nil {
		return false
	}
	var cur string
	if err := store.QueryRow("SELECT state FROM im_accounts WHERE id=?", id).Scan(&cur); err != nil {
		return false
	}
	return cur == state
}

func imSetAccountSecret(id int64, secret string) {
	if store == nil {
		return
	}
	if _, err := store.Exec("UPDATE im_accounts SET secret=?, updated_at="+store.Now()+" WHERE id=?", secret, id); err != nil {
		log.Printf("[im] set secret failed id=%d: %v", id, err)
	}
}

func normalizeIMPlatform(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "telegram", "tg":
		return imPlatformTelegram
	case "wechat", "weixin", "wx", "ilink":
		return imPlatformWeChat
	case "feishu", "lark":
		return imPlatformFeishu
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

/* ───────────────────────── per-account live state ───────────────────────── */

// imLiveMessageState tracks the "current" outbound message we keep editing for a chat.
type imLiveMessageState struct {
	mu           sync.Mutex
	messageID    string
	lastText     string
	lastSendTime time.Time // per-account cooldown to suppress retry garbage
	lastItemID   int       // last reply.json item id sent to this account
	lastTurnID   string    // turn_id of the last processed reply
}

const imSendCooldown = 6 * time.Second

var imLiveState = struct {
	mu sync.Mutex
	m  map[int64]*imLiveMessageState
}{m: map[int64]*imLiveMessageState{}}

func imLiveStateFor(accID int64) *imLiveMessageState {
	imLiveState.mu.Lock()
	defer imLiveState.mu.Unlock()
	st := imLiveState.m[accID]
	if st == nil {
		st = &imLiveMessageState{}
		imLiveState.m[accID] = st
	}
	return st
}

func imResetLiveState(accID int64) {
	st := imLiveStateFor(accID)
	st.mu.Lock()
	st.messageID = ""
	st.lastText = ""
	st.mu.Unlock()
}

// imLastPeer remembers the most recent inbound peer per account so the reply hook
// knows where to send the answer (esp. WeChat, where each message carries a context token).
var imLastPeer = struct {
	mu sync.Mutex
	m  map[int64]botPeer
}{m: map[int64]botPeer{}}

// imLastInboundTime tracks when the last inbound message was received per account.
// Used to decide whether the stored context_token is still fresh enough to use.
var imLastInboundTime = struct {
	mu sync.Mutex
	m  map[int64]time.Time
}{m: map[int64]time.Time{}}

const imContextTokenTTL = 30 * time.Second

func imRememberPeer(accID int64, peer botPeer) {
	if peer.empty() {
		return
	}
	imLastPeer.mu.Lock()
	imLastPeer.m[accID] = peer
	imLastPeer.mu.Unlock()
}

func imLastInboundTimeGet(accID int64) time.Time {
	imLastInboundTime.mu.Lock()
	t := imLastInboundTime.m[accID]
	imLastInboundTime.mu.Unlock()
	return t
}

func imContextTokenStillValid(accID int64) bool {
	imLastInboundTime.mu.Lock()
	t, ok := imLastInboundTime.m[accID]
	imLastInboundTime.mu.Unlock()
	return ok && time.Since(t) < imContextTokenTTL
}

func imPeerForAccount(acc *imAccount) botPeer {
	if acc == nil {
		return botPeer{}
	}
	imLastPeer.mu.Lock()
	p, ok := imLastPeer.m[acc.ID]
	imLastPeer.mu.Unlock()
	if ok && !p.empty() {
		return p
	}
	if cid := acc.configString("chat_id"); cid != "" {
		return botPeer{ChatID: cid}
	}
	// WeChat: restore the last-seen peer that we persisted on inbound. This is
	// what lets a previously-talked-to user be reached again after a restart.
	if acc.Platform == imPlatformWeChat {
		if cid := acc.configString("last_peer_chat_id"); cid != "" {
			return botPeer{ChatID: cid, ContextToken: acc.configString("last_peer_context_token")}
		}
	}
	return botPeer{}
}

/* ───────────────────────── transports ───────────────────────── */

func imBuildTransport(acc *imAccount) (botTransport, error) {
	switch acc.Platform {
	case imPlatformTelegram:
		return newTelegramTransport(acc)
	case imPlatformWeChat:
		return newWeChatTransport(acc)
	case imPlatformFeishu:
		return newFeishuTransport(acc)
	default:
		return nil, fmt.Errorf("unsupported IM platform %q", acc.Platform)
	}
}

/* ───────────────────────── inbound routing ───────────────────────── */

// imPaneSessionOnline reports whether the pane's tmux session is currently
// running. A pane id like "w-10054:main.0" lives in session "w-10054".
func imPaneSessionOnline(paneID string) bool {
	pane := normPaneID(paneID)
	// Headless cicy has no tmux session — its liveness is server-side session
	// registry membership, not `tmux has-session`. Without this a cicy agent would
	// always read offline here and inbound IM would wrongly fall back to w-1001.
	if paneAgentType(pane) == "cicy" {
		return cicySessionRegistered(shortPaneID(pane))
	}
	session := strings.Split(pane, ":")[0]
	if session == "" {
		return false
	}
	_, err := runTmux("has-session", "-t", session)
	return err == nil
}

func imHandleInbound(acc *imAccount, tr botTransport, msg botMsg) {
	if acc == nil || tr == nil {
		return
	}

	// Voice message: try to transcribe audio → text
	if len(msg.VoiceData) > 0 {
		transcribed, err := imTranscribeVoice(msg.VoiceData, msg.VoiceFormat, "voice."+msg.VoiceFormat)
		if err != nil {
			log.Printf("[im] account=%d voice transcribe failed: %v", acc.ID, err)
			imSendOutbound(imOutboundMessage{AccountID: acc.ID, Transport: tr, Peer: msg.Peer, Text: "收到语音消息，转文字失败", Purpose: imOutboundPurposeError})
			return
		}
		msg.Text = transcribed
	}

	// Attachments (image / file / video): 下载到 workspace 的 .cicy/inbox/，
	// 并把相对路径拼到 msg.Text 让 agent 用 Read 工具读。
	if len(msg.Attachments) > 0 {
		paneID := imChatBoundPane(acc.ID, msg.Peer.ChatID)
		if paneID == "" {
			paneID = strings.TrimSpace(acc.BoundPaneID)
		}
		if paneID == "" {
			log.Printf("[im] account=%d got %d attachment(s) but no bound pane", acc.ID, len(msg.Attachments))
		} else {
			paths := imSaveAttachmentsToInbox(paneID, msg.Attachments)
			if len(paths) > 0 {
				note := imRenderAttachmentNote(paths)
				if strings.TrimSpace(msg.Text) == "" {
					msg.Text = note
				} else {
					msg.Text = strings.TrimSpace(msg.Text) + "\n\n" + note
				}
				log.Printf("[im] account=%d attachments saved pane=%s count=%d", acc.ID, shortPaneID(paneID), len(paths))
			}
		}
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	// 先记录来源,再处理命令。以前捕获在命令处理**后面**:只发过 /help 的账号
	// 提前 return,chat_id 永远不落盘,重启后体检误报「从未收到过消息」(实测踩坑)。
	imRememberPeer(acc.ID, msg.Peer)
	if acc.Platform == imPlatformFeishu && strings.HasPrefix(msg.FromID, "feishu:open_id:") {
		openID := strings.TrimSpace(strings.TrimPrefix(msg.FromID, "feishu:open_id:"))
		if openID != "" && acc.configString("last_feishu_open_id") != openID {
			acc.setConfig("last_feishu_open_id", openID)
			imSaveAccountConfig(acc)
		}
	}
	imLastInboundTime.mu.Lock()
	imLastInboundTime.m[acc.ID] = time.Now()
	imLastInboundTime.mu.Unlock()

	// auto-capture chat_id for telegram/feishu so /api/im/send and the reply hook know the target
	if (acc.Platform == imPlatformTelegram || acc.Platform == imPlatformFeishu) && acc.configString("chat_id") == "" && strings.TrimSpace(msg.Peer.ChatID) != "" {
		acc.setConfig("chat_id", strings.TrimSpace(msg.Peer.ChatID))
		imSaveAccountConfig(acc)
		log.Printf("[im] account=%d %s captured chat_id=%s", acc.ID, acc.Platform, msg.Peer.ChatID)
	}

	// Handle bot commands (/help, /start, etc.) locally without forwarding to agent.
	if acc.Platform == imPlatformTelegram && strings.HasPrefix(text, "/") {
		if telegramHandleCommand(acc, tr, msg, text) {
			return
		}
	}
	// 飞书没有 inline keyboard,走纯文本命令:/agents /bind /unbind /status /help。
	if acc.Platform == imPlatformFeishu && strings.HasPrefix(text, "/") {
		if imHandleGenericCommand(acc, tr, msg, text) {
			return
		}
	}

	// persist last-seen wechat peer (chat_id + context_token) so it survives
	// process restarts; without this the bot "forgets" the user after every
	// boot until they message again, and any active send (Test send, agent
	// reply) fails with "no send target" even though we'd already learned it.
	if acc.Platform == imPlatformWeChat && strings.TrimSpace(msg.Peer.ChatID) != "" {
		chatChanged := acc.configString("last_peer_chat_id") != msg.Peer.ChatID
		tokenChanged := acc.configString("last_peer_context_token") != msg.Peer.ContextToken
		if chatChanged || tokenChanged {
			acc.setConfig("last_peer_chat_id", strings.TrimSpace(msg.Peer.ChatID))
			acc.setConfig("last_peer_context_token", strings.TrimSpace(msg.Peer.ContextToken))
			imSaveAccountConfig(acc)
			if chatChanged {
				log.Printf("[im] account=%d wechat captured peer=%s", acc.ID, msg.Peer.ChatID)
			}
		}
	}

	// 会话级绑定优先(一个 bot 多会话,各会话各自的 agent),没绑再退回账号级绑定。
	pane := imChatBoundPane(acc.ID, msg.Peer.ChatID)
	if pane == "" {
		pane = normPaneID(strings.TrimSpace(acc.BoundPaneID))
	}
	if pane == "" || !acc.InboundToAgent {
		log.Printf("[im] account=%d inbound dropped (chat=%s bound=%q inbound=%t): %q", acc.ID, msg.Peer.ChatID, acc.BoundPaneID, acc.InboundToAgent, text)
		return
	}
	// If the bound agent's tmux session isn't running (offline), fall back to the
	// master pane w-1001 so the message still reaches an agent instead of failing.
	if !imPaneSessionOnline(pane) {
		fallback := normPaneID("w-1001")
		if pane != fallback && imPaneSessionOnline(fallback) {
			log.Printf("[im] account=%d bound pane=%s offline → fallback to %s", acc.ID, shortPaneID(pane), shortPaneID(fallback))
			pane = fallback
		}
	}

	// 注：旧 streaming edit 模式会先发一条 "Thinking..." 占位让后续 edit。
	// 现在 per-item push 模式（每个 reply item 单独发一条）不再需要占位，
	// 直接调用 Typing() 让对端显示 "正在输入" 状态即可。
	_ = tr.Typing(msg.Peer)

	imRegisterReplyPushForInbound(pane, acc.ID, msg.Peer)
	// Headless cicy: no tmux pane — feed the inbound text to the server-side
	// runtime in-process. The reply still streams back to the IM peer via the
	// gateway reply-push hook registered just above (cicyCallGateway runs the same
	// gateway path), so this changes only the input hop.
	if paneAgentType(pane) == "cicy" {
		if ws := paneWorkspace(shortPaneID(pane)); ws != "" {
			go deliverCicyMessage(shortPaneID(pane), ws, text)
			log.Printf("[im] account=%d inbound → cicy(headless) %s: %q", acc.ID, shortPaneID(pane), text)
			return
		}
		imCancelReplyPushForInbound(pane, acc.ID)
		log.Printf("[im] account=%d cicy pane=%s has no workspace; inbound dropped", acc.ID, shortPaneID(pane))
		imSendOutbound(imOutboundMessage{AccountID: acc.ID, Transport: tr, Peer: msg.Peer, Text: "⚠️ 发送给 agent 失败: 找不到工作区", Purpose: imOutboundPurposeError})
		return
	}
	if err := sendTextToPane(pane, text, true); err != nil {
		imCancelReplyPushForInbound(pane, acc.ID)
		log.Printf("[im] account=%d send to pane=%s failed: %v", acc.ID, shortPaneID(pane), err)
		imSendOutbound(imOutboundMessage{AccountID: acc.ID, Transport: tr, Peer: msg.Peer, Text: "⚠️ 发送给 agent 失败: " + err.Error(), Purpose: imOutboundPurposeError})
		return
	}
	log.Printf("[im] account=%d inbound → pane=%s: %q", acc.ID, shortPaneID(pane), text)
}

func imSendForPaneWithPurpose(paneID, platform, text string, purpose imOutboundPurpose) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, fmt.Errorf("text required")
	}
	platform = normalizeIMPlatform(platform)
	accounts := imAccountsForPane(paneID)
	sent := 0
	var lastErr error
	delivered := map[string]bool{} // "accID|chatID" 去重:账号级 + 会话级都命中时只发一次
	for _, acc := range accounts {
		if platform != "" && acc.Platform != platform {
			continue
		}
		peer := imPeerForAccount(acc)
		if peer.empty() {
			lastErr = fmt.Errorf("account %d (%s) has no known chat target yet", acc.ID, acc.Platform)
			continue
		}
		tr := imTransportFor(acc.ID)
		if tr == nil {
			lastErr = fmt.Errorf("account %d not connected", acc.ID)
			continue
		}
		if _, err := imSendOutbound(imOutboundMessage{AccountID: acc.ID, Transport: tr, Peer: peer, Text: text, Purpose: purpose}); err != nil {
			lastErr = err
			continue
		}
		delivered[fmt.Sprintf("%d|%s", acc.ID, peer.ChatID)] = true
		sent++
	}
	// 会话级绑定的目标:每个绑到这个 agent 的 (账号, 会话) 也各发一份。
	for _, b := range imChatBindingsForPane(paneID) {
		acc, _ := imGetAccount(b.AccountID)
		if acc == nil || (platform != "" && acc.Platform != platform) {
			continue
		}
		key := fmt.Sprintf("%d|%s", b.AccountID, b.ChatID)
		if delivered[key] {
			continue
		}
		tr := imTransportFor(b.AccountID)
		if tr == nil {
			lastErr = fmt.Errorf("account %d not connected", b.AccountID)
			continue
		}
		if _, err := imSendOutbound(imOutboundMessage{AccountID: b.AccountID, Transport: tr, Peer: botPeer{ChatID: b.ChatID}, Text: text, Purpose: purpose}); err != nil {
			lastErr = err
			continue
		}
		delivered[key] = true
		sent++
	}
	if sent == 0 && lastErr != nil {
		return 0, lastErr
	}
	return sent, nil
}

// imSendForPane is the agent-facing path (used by /api/im/send and legacy /api/tg/send):
// push a one-off message to whatever IM accounts are bound to the pane.
func imSendForPane(paneID, platform, text string) (int, error) {
	return imSendForPaneWithPurpose(paneID, platform, text, imOutboundPurposeProgrammatic)
}

/* ───────────────────────── per-chat agent binding ─────────────────────────
   一个 bot(telegram/feishu)服务多个会话:每个会话可以各绑一个 agent。
   路由优先级:im_chat_bindings(account_id, chat_id) → im_accounts.bound_pane_id。 */

func imChatBoundPane(accID int64, chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if store == nil || accID == 0 || chatID == "" {
		return ""
	}
	var pane string
	_ = store.QueryRow("SELECT COALESCE(pane_id,'') FROM im_chat_bindings WHERE account_id=? AND chat_id=?", accID, chatID).Scan(&pane)
	return normPaneID(strings.TrimSpace(pane))
}

// imBindChatToPane 把某个会话绑到 agent;paneID 为空 = 解绑该会话。
func imBindChatToPane(accID int64, chatID, paneID string) error {
	chatID = strings.TrimSpace(chatID)
	paneID = normPaneID(strings.TrimSpace(paneID))
	if store == nil || accID == 0 || chatID == "" {
		return fmt.Errorf("account/chat required")
	}
	if paneID == "" {
		_, err := store.Exec("DELETE FROM im_chat_bindings WHERE account_id=? AND chat_id=?", accID, chatID)
		return err
	}
	_, err := store.Exec(`INSERT INTO im_chat_bindings (account_id, chat_id, pane_id, updated_at)
		VALUES (?,?,?,datetime('now'))
		ON CONFLICT(account_id, chat_id) DO UPDATE SET pane_id=excluded.pane_id, updated_at=excluded.updated_at`,
		accID, chatID, paneID)
	return err
}

type imChatBinding struct {
	AccountID int64
	ChatID    string
}

func imBindNamedChatToPane(accID int64, chatID, chatName, bindingType, paneID string) error {
	chatID = strings.TrimSpace(chatID)
	chatName = strings.TrimSpace(chatName)
	bindingType = strings.TrimSpace(bindingType)
	paneID = normPaneID(strings.TrimSpace(paneID))
	if store == nil || accID == 0 || chatID == "" || paneID == "" {
		return fmt.Errorf("account/chat/pane required")
	}
	_, err := store.Exec(`INSERT INTO im_chat_bindings (account_id, chat_id, pane_id, chat_name, binding_type, updated_at)
		VALUES (?,?,?,?,?,datetime('now'))
		ON CONFLICT(account_id, chat_id) DO UPDATE SET
			pane_id=excluded.pane_id,
			chat_name=CASE WHEN excluded.chat_name<>'' THEN excluded.chat_name ELSE im_chat_bindings.chat_name END,
			binding_type=CASE WHEN excluded.binding_type<>'' THEN excluded.binding_type ELSE im_chat_bindings.binding_type END,
			updated_at=excluded.updated_at`,
		accID, chatID, paneID, chatName, bindingType)
	return err
}

// imChatBindingsForPane 反查:哪些 (账号, 会话) 绑到了这个 agent。
// /api/im/send(agent 主动推送)用它把消息送到按会话绑定的目标。
func imChatBindingsForPane(paneID string) []imChatBinding {
	paneID = normPaneID(strings.TrimSpace(paneID))
	if store == nil || paneID == "" {
		return nil
	}
	rows, err := store.Query("SELECT account_id, chat_id FROM im_chat_bindings WHERE pane_id=?", paneID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []imChatBinding
	for rows.Next() {
		var b imChatBinding
		if rows.Scan(&b.AccountID, &b.ChatID) == nil && b.ChatID != "" {
			out = append(out, b)
		}
	}
	return out
}

// imHandleGenericCommand 纯文本命令(飞书等没有 inline keyboard 的平台):
// /agents 列出可绑 agent;/bind <pane> 把**当前会话**绑到 agent;/unbind 解绑;
// /status 看当前会话绑定;/help 用法。返回 true = 已处理,不再转发给 agent。
func imHandleGenericCommand(acc *imAccount, tr botTransport, msg botMsg, text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}
	cmd := strings.ToLower(fields[0])
	reply := func(s string) {
		imSendOutbound(imOutboundMessage{AccountID: acc.ID, Transport: tr, Peer: msg.Peer, Text: s, Purpose: imOutboundPurposeProgrammatic})
	}
	switch cmd {
	case "/help", "/start":
		reply("📖 命令\n/agents — 列出所有 agent\n/bind <编号> — 把本会话绑定到 agent(如 /bind w-10242)\n/unbind — 解绑本会话\n/status — 查看本会话绑定\n\n绑定后直接发消息即可与该 agent 对话;每个会话可以绑不同的 agent。")
		return true
	case "/agents":
		agents := telegramQueryAgents()
		if len(agents) == 0 {
			reply("(暂无在线 agent)")
			return true
		}
		var b strings.Builder
		b.WriteString("🤖 可绑定的 agent(用 /bind <编号> 绑定本会话):\n")
		for _, a := range agents {
			title := a.Title
			if title == "" {
				title = a.PaneID
			}
			fmt.Fprintf(&b, "· %s — %s (%s)\n", a.PaneID, title, a.AgentType)
		}
		reply(strings.TrimSpace(b.String()))
		return true
	case "/bind":
		if len(fields) < 2 {
			reply("用法:/bind <编号>,先用 /agents 看列表。")
			return true
		}
		pane := normPaneID(strings.TrimSpace(fields[1]))
		var exists int
		if store != nil {
			_ = store.QueryRow("SELECT COUNT(1) FROM agent_config WHERE pane_id=? AND active=1", pane).Scan(&exists)
		}
		if exists == 0 {
			reply(fmt.Sprintf("⚠️ 找不到 agent %s,用 /agents 看可用列表。", fields[1]))
			return true
		}
		if err := imBindChatToPane(acc.ID, msg.Peer.ChatID, pane); err != nil {
			reply("⚠️ 绑定失败: " + err.Error())
			return true
		}
		var title string
		_ = store.QueryRow("SELECT COALESCE(title,'') FROM agent_config WHERE pane_id=?", pane).Scan(&title)
		if title == "" {
			title = shortPaneID(pane)
		}
		reply(fmt.Sprintf("✅ 本会话已绑定 %s(%s),直接发消息开聊。", title, shortPaneID(pane)))
		return true
	case "/unbind":
		_ = imBindChatToPane(acc.ID, msg.Peer.ChatID, "")
		reply("✅ 本会话已解绑。用 /agents 重新选择。")
		return true
	case "/status":
		pane := imChatBoundPane(acc.ID, msg.Peer.ChatID)
		src := "会话绑定"
		if pane == "" {
			pane = normPaneID(strings.TrimSpace(acc.BoundPaneID))
			src = "账号默认绑定"
		}
		if pane == "" {
			reply("当前会话未绑定 agent。用 /agents 选一个,再 /bind <编号>。")
			return true
		}
		var title string
		if store != nil {
			_ = store.QueryRow("SELECT COALESCE(title,'') FROM agent_config WHERE pane_id=?", pane).Scan(&title)
		}
		reply(fmt.Sprintf("✅ 本会话 → %s(%s,来源:%s)", title, shortPaneID(pane), src))
		return true
	}
	return false
}

/* ───────────────────────── IM-origin reply routing ───────────────────────── */

// imPendingReplyPush records which IM account(s) initiated the next turn for a
// pane — including WHICH chat it came from (peer), so multi-chat accounts reply
// to the right conversation. The AI gateway drains this at turn start and only
// attaches IM reply hooks for those accounts. This prevents normal web/CLI/API
// sends to an IM-bound agent from leaking replies to the IM user side.
var imPendingReplyPush = struct {
	mu sync.Mutex
	m  map[string]map[int64]botPeer
}{m: map[string]map[int64]botPeer{}}

func imRegisterReplyPushForInbound(paneID string, accID int64, peer botPeer) {
	paneID = normPaneID(paneID)
	if paneID == "" || accID == 0 {
		return
	}
	imPendingReplyPush.mu.Lock()
	if imPendingReplyPush.m[paneID] == nil {
		imPendingReplyPush.m[paneID] = map[int64]botPeer{}
	}
	imPendingReplyPush.m[paneID][accID] = peer
	imPendingReplyPush.mu.Unlock()
	log.Printf("[im] reply push registered pane=%s account=%d chat=%s", shortPaneID(paneID), accID, peer.ChatID)
}

func imCancelReplyPushForInbound(paneID string, accID int64) {
	paneID = normPaneID(paneID)
	if paneID == "" || accID == 0 {
		return
	}
	imPendingReplyPush.mu.Lock()
	if set := imPendingReplyPush.m[paneID]; set != nil {
		delete(set, accID)
		if len(set) == 0 {
			delete(imPendingReplyPush.m, paneID)
		}
	}
	imPendingReplyPush.mu.Unlock()
}

func imPeekReplyPushAccountsForPane(paneID string) map[int64]botPeer {
	paneID = normPaneID(paneID)
	if paneID == "" {
		return nil
	}
	imPendingReplyPush.mu.Lock()
	set := imPendingReplyPush.m[paneID]
	imPendingReplyPush.mu.Unlock()
	if len(set) == 0 {
		return nil
	}
	out := make(map[int64]botPeer, len(set))
	for id, peer := range set {
		out[id] = peer
	}
	return out
}

func imDrainReplyPushAccountsForPane(paneID string) map[int64]botPeer {
	paneID = normPaneID(paneID)
	if paneID == "" {
		return nil
	}
	imPendingReplyPush.mu.Lock()
	set := imPendingReplyPush.m[paneID]
	delete(imPendingReplyPush.m, paneID)
	imPendingReplyPush.mu.Unlock()
	if len(set) == 0 {
		return nil
	}
	out := make(map[int64]botPeer, len(set))
	for id, peer := range set {
		out[id] = peer
	}
	return out
}

/* ───────────────────────── manager ───────────────────────── */

type imWorker struct {
	accID     int64
	stop      chan struct{}
	transport botTransport
	mu        sync.Mutex
}

var imMgr = struct {
	mu      sync.Mutex
	workers map[int64]*imWorker
}{workers: map[int64]*imWorker{}}

func (w *imWorker) setTransport(tr botTransport) {
	w.mu.Lock()
	w.transport = tr
	w.mu.Unlock()
}
func (w *imWorker) getTransport() botTransport {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.transport
}

func imTransportFor(accID int64) botTransport {
	imMgr.mu.Lock()
	w := imMgr.workers[accID]
	imMgr.mu.Unlock()
	if w == nil {
		return nil
	}
	return w.getTransport()
}

func imManagerStart() {
	imReconcile()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			imReconcile()
		}
	}()
}

// imWorkersDisabled, when true, makes the manager skip starting background
// workers. Used by tests to avoid spawning long-poll / QR-login goroutines.
var imWorkersDisabled bool

func imReconcile() {
	if store == nil || imWorkersDisabled {
		return
	}
	accounts, err := imListAccounts()
	if err != nil {
		log.Printf("[im] reconcile list failed: %v", err)
		return
	}
	want := map[int64]bool{}
	for _, acc := range accounts {
		if acc.Enabled {
			want[acc.ID] = true
		}
	}
	imMgr.mu.Lock()
	defer imMgr.mu.Unlock()
	for id, w := range imMgr.workers {
		if want[id] {
			continue
		}
		close(w.stop)
		delete(imMgr.workers, id)
		imSetAccountState(id, "disabled", "")
		log.Printf("[im] stopped worker account=%d", id)
	}
	for id := range want {
		if _, ok := imMgr.workers[id]; ok {
			continue
		}
		w := &imWorker{accID: id, stop: make(chan struct{})}
		imMgr.workers[id] = w
		go w.loop()
		log.Printf("[im] started worker account=%d", id)
	}
}

func imReconcileAccount(id int64) {
	// the simplest correct behaviour: stop the worker (if any) and re-reconcile,
	// which restarts it picking up the latest row.
	imMgr.mu.Lock()
	if w, ok := imMgr.workers[id]; ok {
		close(w.stop)
		delete(imMgr.workers, id)
	}
	imMgr.mu.Unlock()
	imReconcile()
}

func (w *imWorker) stopped() bool {
	select {
	case <-w.stop:
		return true
	default:
		return false
	}
}

func (w *imWorker) sleep(d time.Duration) bool {
	select {
	case <-w.stop:
		return true
	case <-time.After(d):
		return false
	}
}

func (w *imWorker) loop() {
	backoff := 2 * time.Second
	for {
		if w.stopped() {
			return
		}
		acc, err := imGetAccount(w.accID)
		if err != nil || acc == nil {
			if w.sleep(10 * time.Second) {
				return
			}
			continue
		}
		if !acc.Enabled {
			imSetAccountState(acc.ID, "disabled", "")
			return
		}
		tr, err := imBuildTransport(acc)
		if err != nil {
			imSetAccountState(acc.ID, "error", err.Error())
			if w.sleep(backoff) {
				return
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
			continue
		}
		w.setTransport(tr)
		imSetAccountState(acc.ID, "connected", "")
		backoff = 2 * time.Second
		if acc.Platform == imPlatformTelegram {
			go telegramSyncBotCommands(acc.Secret)
		}

		// poll loop
		cursor := imLoadCursor(acc)
		for {
			if w.stopped() {
				return
			}
			msgs, next, err := tr.Poll(cursor)
			if err != nil {
				if errors.Is(err, errIMSessionExpired) {
					log.Printf("[im] account=%d session expired, re-authenticating", acc.ID)
					imSetAccountState(acc.ID, "logged_out", "")
					break // back to outer loop → rebuild transport (re-login)
				}
				log.Printf("[im] account=%d poll error: %v", acc.ID, err)
				imSetAccountState(acc.ID, "error", err.Error())
				if w.sleep(3 * time.Second) {
					return
				}
				continue
			}
			if backoff > 2*time.Second || imAccountStateIs(acc.ID, "error") {
				imSetAccountState(acc.ID, "connected", "")
				backoff = 2 * time.Second
			}
			if next != "" && next != cursor {
				cursor = next
				imSaveCursor(acc, cursor)
			}
			for _, msg := range msgs {
				// reload the row so bound_pane_id / inbound flag changes take effect live
				if fresh, ferr := imGetAccount(acc.ID); ferr == nil && fresh != nil {
					fresh.Config = acc.Config // keep volatile cursor we just bumped
					imHandleInbound(fresh, tr, msg)
				} else {
					imHandleInbound(acc, tr, msg)
				}
			}
		}
		w.setTransport(nil)
	}
}

// errIMSessionExpired signals that the transport's credentials are no longer valid
// and the worker should rebuild the transport (e.g. WeChat QR re-login).
var errIMSessionExpired = errors.New("im session expired")

// cursor persistence: telegram uses an int offset, wechat uses get_updates_buf.
// Both live in im_accounts.config under "cursor".
func imLoadCursor(acc *imAccount) string { return acc.configString("cursor") }
func imSaveCursor(acc *imAccount, cursor string) {
	acc.setConfig("cursor", cursor)
	if store != nil {
		if _, err := store.Exec("UPDATE im_accounts SET config=?, updated_at="+store.Now()+" WHERE id=?", imJSON(acc.Config), acc.ID); err != nil {
			log.Printf("[im] save cursor failed id=%d: %v", acc.ID, err)
		}
	}
}

func imJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

/* ───────────────────────── REST ───────────────────────── */

type imPlatformInfo struct {
	Kind       string            `json:"kind"`
	Label      string            `json:"label"`
	NeedsToken bool              `json:"needs_token"`
	NeedsQR    bool              `json:"needs_qr"`
	CanEdit    bool              `json:"can_edit"`
	Help       map[string]string `json:"help,omitempty"`
}

func imPlatforms() []imPlatformInfo {
	return []imPlatformInfo{
		{
			Kind: imPlatformTelegram, Label: "Telegram", NeedsToken: true, NeedsQR: false, CanEdit: true,
			Help: map[string]string{
				"title": "获取 Bot Token",
				"steps": "在 @BotFather 里：发送 /newbot 新建一个 bot 并复制返回的 token；或发送 /mybots 选已有 bot → API Token。token 形如 123456:ABC-DEF…，粘贴到上面的 Bot Token 框后点保存。",
				"link":  "https://t.me/BotFather",
			},
		},
		{
			Kind: imPlatformWeChat, Label: "微信", NeedsToken: false, NeedsQR: true, CanEdit: false,
			Help: map[string]string{
				"title": "微信扫码登录",
				"steps": "保存后会生成二维码，用微信扫码登录即可；登录态保存在 ~/cicy-ai/db/ 下，后端重启会自动续上，会话失效需重新扫码。",
			},
		},
		{
			Kind: imPlatformFeishu, Label: "飞书", NeedsToken: true, NeedsQR: false, CanEdit: false,
			Help: map[string]string{
				"title": "创建飞书企业自建应用",
				"steps": "飞书开放平台 → 创建企业自建应用 → 添加「机器人」能力 → 权限里开通 im:message(发消息)→「事件与回调」选择**使用长连接接收事件**并订阅 im.message.receive_v1 → 发布应用。然后把凭证页的 App ID / App Secret 填到上面。绑定后在飞书里给机器人发 /help 查看会话绑定命令(每个会话可绑不同 agent)。",
				"link":  "https://open.feishu.cn/app",
			},
		},
	}
}

func imAccountToMap(acc *imAccount) M {
	secretTail := ""
	hasSecret := strings.TrimSpace(acc.Secret) != ""
	if hasSecret && len(acc.Secret) > 4 {
		secretTail = acc.Secret[len(acc.Secret)-4:]
	}
	cfg := map[string]any{}
	for k, v := range acc.Config {
		if k == "cursor" {
			continue
		}
		cfg[k] = v
	}
	boundTitle := ""
	if acc.BoundPaneID != "" && store != nil {
		var t string
		_ = store.QueryRow("SELECT COALESCE(title,'') FROM agent_config WHERE pane_id=?", normPaneID(acc.BoundPaneID)).Scan(&t)
		boundTitle = t
	}
	return M{
		"id":               acc.ID,
		"platform":         acc.Platform,
		"name":             acc.Name,
		"has_secret":       hasSecret,
		"secret_tail":      secretTail,
		"config":           cfg,
		"enabled":          acc.Enabled,
		"state":            acc.State,
		"state_detail":     acc.StateDetail,
		"bound_pane_id":    shortPaneID(acc.BoundPaneID),
		"bound_pane_title": boundTitle,
		"inbound_to_agent": acc.InboundToAgent,
		"created_at":       acc.CreatedAt,
		"updated_at":       acc.UpdatedAt,
	}
}

func handleIMRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/im/")
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")
	switch parts[0] {
	case "platforms":
		if r.Method != http.MethodGet {
			httpErr(w, 405, "method not allowed")
			return
		}
		J(w, M{"platforms": imPlatforms()})
		return
	case "send":
		if r.Method != http.MethodPost {
			httpErr(w, 405, "method not allowed")
			return
		}
		handleIMSend(w, r)
		return
	case "wechat":
		// /api/im/wechat/login        POST   -> start a QR-login session (adds a new account on success)
		// /api/im/wechat/login/{id}   GET    -> poll the session
		// /api/im/wechat/login/{id}/cancel POST -> cancel the session
		if len(parts) >= 2 && parts[1] == "login" {
			handleWeChatLoginRoute(w, r, parts)
			return
		}
		httpErr(w, 404, "not found")
		return
	case "accounts":
		if len(parts) == 1 {
			handleIMAccounts(w, r)
			return
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			httpErr(w, 400, "invalid account id")
			return
		}
		action := ""
		if len(parts) >= 3 {
			action = parts[2]
		}
		handleIMAccountByID(w, r, id, action)
		return
	case "chat-bindings":
		handleIMChatBindings(w, r)
		return
	}
	httpErr(w, 404, "not found")
}

func handleIMAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		accounts, err := imListAccounts()
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		out := make([]M, 0, len(accounts))
		for _, a := range accounts {
			out = append(out, imAccountToMap(a))
		}
		J(w, M{"accounts": out})
	case http.MethodPost:
		var body struct {
			Platform string `json:"platform"`
			Name     string `json:"name"`
			Secret   string `json:"secret"`
			AppID    string `json:"app_id"` // feishu only: 应用 App ID(secret 存 App Secret)
		}
		if err := readBody(r, &body); err != nil {
			httpErr(w, 400, "invalid request body")
			return
		}
		platform := normalizeIMPlatform(body.Platform)
		if platform != imPlatformTelegram && platform != imPlatformWeChat && platform != imPlatformFeishu {
			httpErr(w, 400, "platform must be telegram, wechat or feishu")
			return
		}
		name := strings.TrimSpace(body.Name)
		secret := strings.TrimSpace(body.Secret)
		state := "pending"
		detail := ""
		cfg := map[string]any{}
		if platform == imPlatformTelegram {
			if secret == "" {
				// allow creating the account first; the token is filled in the detail form
				detail = "待填写 Bot Token"
				if name == "" {
					name = "Telegram Bot"
				}
			} else {
				username, reachable, verr := telegramValidateToken(secret)
				if verr != nil && reachable {
					httpErr(w, 400, "invalid telegram token: "+verr.Error())
					return
				}
				if verr != nil { // unreachable — keep it, worker/PATCH can retry
					state = "error"
					detail = "无法连接 Telegram 验证 token（检查网络/代理），已保存: " + verr.Error()
					if name == "" {
						name = "Telegram Bot"
					}
				} else {
					detail = "@" + username
					cfg["bot_username"] = username
					if name == "" {
						name = username
					}
				}
			}
		}
		if platform == imPlatformWeChat {
			// wechat accounts are created only after a successful QR scan, via /api/im/wechat/login
			httpErr(w, 400, "请用『添加微信』(扫码登录) 来添加微信账号")
			return
		}
		if platform == imPlatformFeishu {
			appID := strings.TrimSpace(body.AppID)
			if appID == "" || secret == "" {
				httpErr(w, 400, "feishu 需要 app_id 和 app_secret(飞书开放平台 → 企业自建应用)")
				return
			}
			cfg["app_id"] = appID
			appName, reachable, verr := feishuValidateCredentials(appID, secret)
			if verr != nil && reachable {
				httpErr(w, 400, "invalid feishu credentials: "+verr.Error())
				return
			}
			if verr != nil { // 网络不通:先存,worker 会重试
				state = "error"
				detail = "无法连接飞书验证凭据(检查网络/代理),已保存: " + verr.Error()
			} else if appName != "" {
				detail = appName
				cfg["app_name_synced"] = true
			}
			if name == "" {
				if appName != "" {
					name = appName
				} else if len(appID) > 6 {
					name = "飞书应用 …" + appID[len(appID)-6:] // 多应用时可区分
				} else {
					name = "飞书应用"
				}
			}
		}
		res, err := store.Exec(
			"INSERT INTO im_accounts (platform, name, secret, config, enabled, state, state_detail, inbound_to_agent) VALUES (?,?,?,?,?,?,?,1)",
			platform, name, secret, imJSON(cfg), 1, state, detail,
		)
		if err != nil {
			httpErr(w, 500, "create failed: "+err.Error())
			return
		}
		id, _ := res.LastInsertId()
		imReconcileAccount(id)
		acc, _ := imGetAccount(id)
		if acc != nil {
			J(w, M{"success": true, "account": imAccountToMap(acc)})
		} else {
			J(w, M{"success": true, "id": id})
		}
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleIMAccountByID(w http.ResponseWriter, r *http.Request, id int64, action string) {
	acc, err := imGetAccount(id)
	if err != nil || acc == nil {
		httpErr(w, 404, "account not found")
		return
	}
	switch action {
	case "":
		switch r.Method {
		case http.MethodGet:
			J(w, M{"account": imAccountToMap(acc)})
		case http.MethodPatch, http.MethodPut:
			handleIMAccountPatch(w, r, acc)
		case http.MethodDelete:
			imMgr.mu.Lock()
			if wk, ok := imMgr.workers[id]; ok {
				close(wk.stop)
				delete(imMgr.workers, id)
			}
			imMgr.mu.Unlock()
			if _, err := store.Exec("DELETE FROM im_accounts WHERE id=?", id); err != nil {
				httpErr(w, 500, err.Error())
				return
			}
			if acc.Platform == imPlatformWeChat {
				weChatRemoveState(id)
			}
			J(w, M{"success": true})
		default:
			httpErr(w, 405, "method not allowed")
		}
	case "secret":
		// reveal the full stored token on explicit request (eye toggle in the UI).
		// not included in the account list/detail maps — fetched only when the user asks.
		if r.Method != http.MethodGet {
			httpErr(w, 405, "method not allowed")
			return
		}
		J(w, M{"secret": acc.Secret})
	case "test":
		if r.Method != http.MethodPost {
			httpErr(w, 405, "method not allowed")
			return
		}
		handleIMAccountTest(w, acc)
	case "sync-name":
		if r.Method != http.MethodPost {
			httpErr(w, 405, "method not allowed")
			return
		}
		if acc.Platform != imPlatformFeishu {
			httpErr(w, 400, "sync-name only applies to feishu")
			return
		}
		appName, reachable, syncErr := feishuValidateCredentials(acc.configString("app_id"), acc.Secret)
		if syncErr != nil {
			if !reachable {
				httpErr(w, 502, "无法连接飞书: "+syncErr.Error())
			} else {
				httpErr(w, 400, "飞书凭据无效: "+syncErr.Error())
			}
			return
		}
		if appName == "" {
			httpErr(w, 502, "飞书未返回应用名称，请确认已添加机器人能力")
			return
		}
		acc.setConfig("app_name_synced", true)
		if _, err := store.Exec(
			"UPDATE im_accounts SET name=?, state_detail=?, config=?, updated_at="+store.Now()+" WHERE id=?",
			appName, appName, imJSON(acc.Config), acc.ID,
		); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		acc, _ = imGetAccount(id)
		J(w, M{"success": true, "name": appName, "account": imAccountToMap(acc)})
	case "create-chat":
		if r.Method != http.MethodPost {
			httpErr(w, 405, "method not allowed")
			return
		}
		if acc.Platform != imPlatformFeishu {
			httpErr(w, 400, "create-chat only applies to feishu")
			return
		}
		var body struct {
			PaneID string `json:"pane_id"`
			Mode   string `json:"mode"`
		}
		if err := readBody(r, &body); err != nil {
			httpErr(w, 400, "invalid request body")
			return
		}
		paneID := normPaneID(strings.TrimSpace(body.PaneID))
		var paneTitle string
		if err := store.QueryRow("SELECT COALESCE(title,'') FROM agent_config WHERE pane_id=? AND active=1", paneID).Scan(&paneTitle); err != nil {
			httpErr(w, 404, "agent not found")
			return
		}
		chatName := strings.TrimSpace(paneTitle)
		if chatName == "" {
			chatName = shortPaneID(paneID)
		} else {
			chatName += " · " + shortPaneID(paneID)
		}
		mode := strings.TrimSpace(body.Mode)
		if mode == "" {
			mode = "group"
		}
		var chatID string
		var createErr error
		switch mode {
		case "direct":
			chatID, createErr = feishuOpenDirectChat(acc, chatName)
		case "group":
			// Do not gate a real bind on synthetic permission probes. Feishu can
			// reject the probes for their fake chat/member IDs (or require an
			// unrelated read scope), which previously produced a false "missing
			// permission" result even after the app had been authorized.
			//
			// Creating and binding the group only requires the real create-chat
			// call below to succeed. im:message.group_msg and im:resource affect
			// subsequent receive/media behavior and remain visible in the setup
			// health check, but must not prevent the group itself from binding.
			chatID, createErr = feishuCreateChat(acc, chatName)
		default:
			httpErr(w, 400, "mode must be direct or group")
			return
		}
		if createErr != nil {
			httpErr(w, 502, createErr.Error())
			return
		}
		if mode == "direct" {
			chatName = acc.Name + " Bot 私聊"
		}
		if err := imBindNamedChatToPane(acc.ID, chatID, chatName, mode, paneID); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true, "mode": mode, "chat_id": chatID, "chat_name": chatName, "pane_id": shortPaneID(paneID)})
	case "bind":
		if r.Method != http.MethodPost {
			httpErr(w, 405, "method not allowed")
			return
		}
		handleIMAccountBind(w, r, acc)
	case "unbind":
		if r.Method != http.MethodPost {
			httpErr(w, 405, "method not allowed")
			return
		}
		if _, err := store.Exec("UPDATE im_accounts SET bound_pane_id='', updated_at="+store.Now()+" WHERE id=?", id); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true})
	case "qr":
		if r.Method != http.MethodGet {
			httpErr(w, 405, "method not allowed")
			return
		}
		J(w, M{
			"state":      acc.State,
			"detail":     acc.StateDetail,
			"qrcode_url": acc.configString("qrcode_url"),
			"nick_name":  acc.configString("nick_name"),
		})
	case "relogin":
		if r.Method != http.MethodPost {
			httpErr(w, 405, "method not allowed")
			return
		}
		if acc.Platform != imPlatformWeChat {
			httpErr(w, 400, "relogin only applies to wechat")
			return
		}
		imSetAccountSecret(id, "")
		weChatRemoveState(id)
		acc.setConfig("qrcode_url", "")
		acc.setConfig("nick_name", "")
		imSaveAccountConfig(acc)
		imSetAccountState(id, "qr_wait", "")
		imReconcileAccount(id)
		J(w, M{"success": true})
	default:
		httpErr(w, 404, "not found")
	}
}

func handleIMChatBindings(w http.ResponseWriter, r *http.Request) {
	paneID := normPaneID(strings.TrimSpace(r.URL.Query().Get("pane_id")))
	if paneID == "" {
		httpErr(w, 400, "pane_id required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		rows, err := store.Query(`SELECT b.account_id, b.chat_id, COALESCE(b.chat_name,''),
				COALESCE(NULLIF(b.binding_type,''), CASE WHEN b.chat_name LIKE '%Bot 私聊' THEN 'direct' ELSE 'group' END), a.name
			FROM im_chat_bindings b
			JOIN im_accounts a ON a.id=b.account_id
			WHERE b.pane_id=? AND a.platform='feishu'
			ORDER BY b.updated_at DESC`, paneID)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		defer rows.Close()
		bindings := []M{}
		for rows.Next() {
			var accountID int64
			var chatID, chatName, bindingType, accountName string
			if rows.Scan(&accountID, &chatID, &chatName, &bindingType, &accountName) == nil {
				bindings = append(bindings, M{
					"account_id": accountID, "account_name": accountName,
					"chat_id": chatID, "chat_name": chatName, "binding_type": bindingType, "pane_id": shortPaneID(paneID),
				})
			}
		}
		allRows, allErr := store.Query(`SELECT b.account_id, b.chat_id,
				COALESCE(NULLIF(b.binding_type,''), CASE WHEN b.chat_name LIKE '%Bot 私聊' THEN 'direct' ELSE 'group' END),
				b.pane_id, COALESCE(b.chat_name,''), COALESCE(c.title,'')
			FROM im_chat_bindings b
			JOIN im_accounts a ON a.id=b.account_id
			LEFT JOIN agent_config c ON c.pane_id=b.pane_id AND c.active=1
			WHERE a.platform='feishu'
			ORDER BY b.updated_at DESC`)
		if allErr != nil {
			httpErr(w, 500, allErr.Error())
			return
		}
		defer allRows.Close()
		accountBindings := []M{}
		for allRows.Next() {
			var accountID int64
			var chatID, bindingType, boundPaneID, chatName, paneTitle string
			if allRows.Scan(&accountID, &chatID, &bindingType, &boundPaneID, &chatName, &paneTitle) == nil {
				accountBindings = append(accountBindings, M{
					"account_id": accountID, "chat_id": chatID, "binding_type": bindingType,
					"pane_id": shortPaneID(boundPaneID), "pane_title": paneTitle, "chat_name": chatName,
				})
			}
		}
		J(w, M{"bindings": bindings, "account_bindings": accountBindings})
	case http.MethodDelete:
		chatID := strings.TrimSpace(r.URL.Query().Get("chat_id"))
		accountID, _ := strconv.ParseInt(r.URL.Query().Get("account_id"), 10, 64)
		if chatID == "" || accountID == 0 {
			httpErr(w, 400, "account_id and chat_id required")
			return
		}
		if _, err := store.Exec("DELETE FROM im_chat_bindings WHERE account_id=? AND chat_id=? AND pane_id=?", accountID, chatID, paneID); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleIMAccountPatch(w http.ResponseWriter, r *http.Request, acc *imAccount) {
	var body map[string]any
	if err := readBody(r, &body); err != nil {
		httpErr(w, 400, "invalid request body")
		return
	}
	sets := []string{}
	vals := []any{}
	if v, ok := body["name"]; ok {
		if s, _ := v.(string); strings.TrimSpace(s) != "" {
			sets = append(sets, "name=?")
			vals = append(vals, strings.TrimSpace(s))
		}
	}
	if v, ok := body["secret"]; ok && acc.Platform == imPlatformFeishu {
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		if s == "" {
			httpErr(w, 400, "feishu app_secret cannot be empty")
			return
		}
		appName, reachable, verr := feishuValidateCredentials(acc.configString("app_id"), s)
		if verr != nil && reachable {
			httpErr(w, 400, "invalid feishu credentials: "+verr.Error())
			return
		}
		sets = append(sets, "secret=?")
		vals = append(vals, s)
		if verr != nil { // 网络不通:先存,worker 重试
			sets = append(sets, "state=?", "state_detail=?")
			vals = append(vals, "error", "无法连接飞书验证凭据(检查网络/代理): "+verr.Error())
		} else {
			sets = append(sets, "state=?", "state_detail=?")
			vals = append(vals, "pending", appName)
		}
	}
	if v, ok := body["secret"]; ok && acc.Platform == imPlatformTelegram {
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		if s == "" {
			httpErr(w, 400, "telegram token cannot be empty")
			return
		}
		username, reachable, verr := telegramValidateToken(s)
		if verr != nil && reachable {
			httpErr(w, 400, "invalid telegram token: "+verr.Error())
			return
		}
		sets = append(sets, "secret=?")
		vals = append(vals, s)
		acc.setConfig("chat_id", "") // token changed → re-capture chat
		if verr != nil {             // unreachable; keep the token, worker will retry
			acc.setConfig("bot_username", "")
			sets = append(sets, "state=?", "state_detail=?")
			vals = append(vals, "error", "无法连接 Telegram 验证 token（检查网络/代理）: "+verr.Error())
		} else {
			acc.setConfig("bot_username", username)
			sets = append(sets, "state=?", "state_detail=?")
			vals = append(vals, "pending", "@"+username)
		}
	}
	// feishu:App ID 也可改(换应用)。改了就清掉旧应用的 chat_id 并让 worker 重建。
	if v, ok := body["app_id"]; ok && acc.Platform == imPlatformFeishu {
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		if s == "" {
			httpErr(w, 400, "feishu app_id cannot be empty")
			return
		}
		if s != acc.configString("app_id") {
			acc.setConfig("app_id", s)
			acc.setConfig("chat_id", "") // 换应用 = 旧会话作废,重新捕获
			sets = append(sets, "state=?", "state_detail=?")
			vals = append(vals, "pending", "")
		}
	}
	if v, ok := body["enabled"]; ok {
		b, _ := v.(bool)
		sets = append(sets, "enabled=?")
		vals = append(vals, boolToInt(b))
	}
	if v, ok := body["inbound_to_agent"]; ok {
		b, _ := v.(bool)
		sets = append(sets, "inbound_to_agent=?")
		vals = append(vals, boolToInt(b))
	}
	// always persist any config change accumulated above
	body2, _ := json.Marshal(acc.Config)
	sets = append(sets, "config=?")
	vals = append(vals, string(body2))
	sets = append(sets, "updated_at="+store.Now())
	vals = append(vals, acc.ID)
	if _, err := store.Exec("UPDATE im_accounts SET "+strings.Join(sets, ", ")+" WHERE id=?", vals...); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	imReconcileAccount(acc.ID)
	fresh, _ := imGetAccount(acc.ID)
	if fresh != nil {
		J(w, M{"success": true, "account": imAccountToMap(fresh)})
	} else {
		J(w, M{"success": true})
	}
}

func handleIMAccountBind(w http.ResponseWriter, r *http.Request, acc *imAccount) {
	var body struct {
		PaneID string `json:"pane_id"`
	}
	readBody(r, &body)
	pane := normPaneID(strings.TrimSpace(body.PaneID))
	if pane == "" {
		httpErr(w, 400, "pane_id required")
		return
	}
	if store != nil {
		var exists int
		_ = store.QueryRow("SELECT COUNT(1) FROM agent_config WHERE pane_id=?", pane).Scan(&exists)
		if exists == 0 {
			httpErr(w, 400, "pane not found: "+shortPaneID(pane))
			return
		}
		// one account per platform per agent
		var other int
		_ = store.QueryRow("SELECT COUNT(1) FROM im_accounts WHERE bound_pane_id=? AND platform=? AND id<>?", pane, acc.Platform, acc.ID).Scan(&other)
		if other > 0 {
			httpErr(w, 409, fmt.Sprintf("pane %s already has a %s account bound", shortPaneID(pane), acc.Platform))
			return
		}
	}
	if _, err := store.Exec("UPDATE im_accounts SET bound_pane_id=?, updated_at="+store.Now()+" WHERE id=?", pane, acc.ID); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"success": true})
}

func handleIMAccountTest(w http.ResponseWriter, acc *imAccount) {
	// 飞书走专用体检:凭据/长连接/发消息权限/事件订阅逐项检查(权限探针见 im_feishu.go)。
	if acc.Platform == imPlatformFeishu {
		feishuHandleTest(w, acc)
		return
	}
	start := time.Now()
	tr := imTransportFor(acc.ID)
	if tr == nil && acc.Platform == imPlatformTelegram {
		// telegram transports are cheap to build (no login); allow testing even if the
		// worker hasn't picked it up yet.
		if built, err := imBuildTransport(acc); err == nil {
			tr = built
		}
	}
	if tr == nil {
		J(w, M{"ok": false, "detail": "账号尚未连接。" + map[string]string{imPlatformWeChat: "请先在「登录」区完成微信扫码。"}[acc.Platform]})
		return
	}
	peer := imPeerForAccount(acc)
	// WeChat fallback: when no inbound peer has been recorded yet, try the
	// bot's own ilink_user_id. The ilink protocol typically lets a logged-in
	// bot send a message to itself (acts like 文件传输助手), giving the user
	// a sanity-check test send without requiring an inbound first.
	if peer.empty() && acc.Platform == imPlatformWeChat {
		if uid := acc.configString("ilink_user_id"); uid != "" {
			peer = botPeer{ChatID: uid}
		}
	}
	if peer.empty() {
		J(w, M{"ok": false, "detail": "还没有可发送的目标。" + map[string]string{imPlatformTelegram: "请先向这个 bot 发一条消息以绑定 chat。", imPlatformWeChat: "请先完成扫码登录并在微信里给它发一条消息。"}[acc.Platform]})
		return
	}
	if _, err := imSendOutbound(imOutboundMessage{AccountID: acc.ID, Transport: tr, Peer: peer, Text: "✅ cicy 测试消息", Purpose: imOutboundPurposeTest}); err != nil {
		detail := err.Error()
		if errors.Is(err, errWeChatNoActiveSession) {
			// Not a bug — ilink only lets the bot reply inside an active session.
			detail = "微信机器人只能在「用户刚给它发过消息」的会话窗口内推送(ilink 协议限制,主动推送会被拒)。请先在微信里给这个 bot 发一条消息,再点测试。"
		}
		J(w, M{"ok": false, "detail": detail, "duration_ms": time.Since(start).Milliseconds()})
		return
	}
	J(w, M{"ok": true, "detail": "ok", "duration_ms": time.Since(start).Milliseconds()})
}

func handleIMSend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PaneID   string `json:"pane_id"`
		Platform string `json:"platform"`
		Text     string `json:"text"`
	}
	if err := readBody(r, &body); err != nil {
		httpErr(w, 400, "invalid request body")
		return
	}
	pane := normPaneID(strings.TrimSpace(body.PaneID))
	if pane == "" {
		httpErr(w, 400, "pane_id required")
		return
	}
	sent, err := imSendForPane(pane, body.Platform, body.Text)
	if err != nil {
		httpErr(w, 502, err.Error())
		return
	}
	J(w, M{"success": true, "sent": sent})
}

/* ───────────────────────── wechat add-via-scan flow ───────────────────────── */

// wechatHasUnconnectedAccount reports whether any wechat account is not in the
// "connected" state (so adding a new one is disallowed until it's resolved).
func wechatHasUnconnectedAccount() bool {
	accounts, err := imListAccounts()
	if err != nil {
		return false
	}
	for _, a := range accounts {
		if a.Platform == imPlatformWeChat && a.State != "connected" {
			return true
		}
	}
	return false
}

func handleWeChatLoginRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	// parts: ["wechat", "login", <id?>, "cancel"?]
	if len(parts) == 2 {
		if r.Method != http.MethodPost {
			httpErr(w, 405, "method not allowed")
			return
		}
		// Stale in-memory sessions (user closed the modal before cancel finished,
		// double-clicked +, etc.) used to block here with a 409. Just cancel them
		// transparently so the user always gets a fresh QR.
		weChatCancelAllPendingLogins()
		if wechatHasUnconnectedAccount() {
			httpErr(w, 409, "已有未登录成功的微信账号，请先在它的『登录』里完成扫码，或删掉它")
			return
		}
		s, err := weChatStartLoginSession()
		if err != nil {
			httpErr(w, 502, "获取二维码失败: "+err.Error())
			return
		}
		st, qr, detail, accID := s.snapshot()
		J(w, M{"session_id": s.ID, "state": st, "qrcode_url": qr, "detail": detail, "account_id": accID})
		return
	}
	id := strings.TrimSpace(parts[2])
	s := weChatGetLoginSession(id)
	if s == nil {
		httpErr(w, 404, "login session not found")
		return
	}
	if len(parts) >= 4 && parts[3] == "cancel" {
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			httpErr(w, 405, "method not allowed")
			return
		}
		s.cancel()
		weChatLogins.mu.Lock()
		delete(weChatLogins.m, id)
		weChatLogins.mu.Unlock()
		J(w, M{"success": true})
		return
	}
	if r.Method != http.MethodGet {
		httpErr(w, 405, "method not allowed")
		return
	}
	st, qr, detail, accID := s.snapshot()
	J(w, M{"session_id": s.ID, "state": st, "qrcode_url": qr, "detail": detail, "account_id": accID})
}
