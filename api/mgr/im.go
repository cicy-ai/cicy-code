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

const (
	imPlatformTelegram = "telegram"
	imPlatformWeChat   = "wechat"
)

// botPeer identifies who/where to send a message. Telegram only uses ChatID;
// WeChat needs the target user id plus the per-message context token.
type botPeer struct {
	ChatID       string `json:"chat_id"`
	ContextToken string `json:"context_token,omitempty"`
}

func (p botPeer) empty() bool { return strings.TrimSpace(p.ChatID) == "" }

// botMsg is a normalized inbound message.
type botMsg struct {
	Text   string
	Peer   botPeer
	FromID string
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
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

/* ───────────────────────── per-account live state ───────────────────────── */

// imLiveMessageState tracks the "current" outbound message we keep editing for a chat.
type imLiveMessageState struct {
	mu        sync.Mutex
	messageID string
	lastText  string
}

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

func imRememberPeer(accID int64, peer botPeer) {
	if peer.empty() {
		return
	}
	imLastPeer.mu.Lock()
	imLastPeer.m[accID] = peer
	imLastPeer.mu.Unlock()
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
	return botPeer{}
}

/* ───────────────────────── transports ───────────────────────── */

func imBuildTransport(acc *imAccount) (botTransport, error) {
	switch acc.Platform {
	case imPlatformTelegram:
		return newTelegramTransport(acc)
	case imPlatformWeChat:
		return newWeChatTransport(acc)
	default:
		return nil, fmt.Errorf("unsupported IM platform %q", acc.Platform)
	}
}

/* ───────────────────────── inbound routing ───────────────────────── */

func imHandleInbound(acc *imAccount, tr botTransport, msg botMsg) {
	if acc == nil || tr == nil {
		return
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	imRememberPeer(acc.ID, msg.Peer)

	// auto-capture chat_id for telegram so /api/im/send and the reply hook know the target
	if acc.Platform == imPlatformTelegram && acc.configString("chat_id") == "" && strings.TrimSpace(msg.Peer.ChatID) != "" {
		acc.setConfig("chat_id", strings.TrimSpace(msg.Peer.ChatID))
		imSaveAccountConfig(acc)
		log.Printf("[im] account=%d telegram captured chat_id=%s", acc.ID, msg.Peer.ChatID)
	}

	pane := normPaneID(strings.TrimSpace(acc.BoundPaneID))
	if pane == "" || !acc.InboundToAgent {
		log.Printf("[im] account=%d inbound dropped (bound=%q inbound=%t): %q", acc.ID, acc.BoundPaneID, acc.InboundToAgent, text)
		return
	}

	// post an editable ack so the streaming reply hook has something to edit
	if tr.CanEdit() {
		if mid, err := tr.Send(msg.Peer, "Thinking..."); err == nil {
			st := imLiveStateFor(acc.ID)
			st.mu.Lock()
			st.messageID = mid
			st.lastText = "Thinking..."
			st.mu.Unlock()
		}
	} else {
		imResetLiveState(acc.ID)
	}
	_ = tr.Typing(msg.Peer)

	if err := sendTextToPane(pane, text, true); err != nil {
		log.Printf("[im] account=%d send to pane=%s failed: %v", acc.ID, shortPaneID(pane), err)
		_, _ = tr.Send(msg.Peer, "⚠️ 发送给 agent 失败: "+err.Error())
		return
	}
	log.Printf("[im] account=%d inbound → pane=%s: %q", acc.ID, shortPaneID(pane), text)
}

// imSendForPane is the agent-facing path (used by /api/im/send and legacy /api/tg/send):
// push a one-off message to whatever IM accounts are bound to the pane.
func imSendForPane(paneID, platform, text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, fmt.Errorf("text required")
	}
	platform = normalizeIMPlatform(platform)
	accounts := imAccountsForPane(paneID)
	sent := 0
	var lastErr error
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
		if _, err := tr.Send(peer, text); err != nil {
			lastErr = err
			continue
		}
		sent++
	}
	if sent == 0 && lastErr != nil {
		return 0, lastErr
	}
	return sent, nil
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
				imSetAccountState(acc.ID, "error", err.Error())
				if w.sleep(3 * time.Second) {
					return
				}
				continue
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
		}
		if err := readBody(r, &body); err != nil {
			httpErr(w, 400, "invalid request body")
			return
		}
		platform := normalizeIMPlatform(body.Platform)
		if platform != imPlatformTelegram && platform != imPlatformWeChat {
			httpErr(w, 400, "platform must be telegram or wechat")
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
	case "test":
		if r.Method != http.MethodPost {
			httpErr(w, 405, "method not allowed")
			return
		}
		handleIMAccountTest(w, acc)
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
	if peer.empty() {
		J(w, M{"ok": false, "detail": "还没有可发送的目标。" + map[string]string{imPlatformTelegram: "请先向这个 bot 发一条消息以绑定 chat。", imPlatformWeChat: "请先完成扫码登录并在微信里给它发一条消息。"}[acc.Platform]})
		return
	}
	if _, err := tr.Send(peer, "✅ cicy 测试消息"); err != nil {
		J(w, M{"ok": false, "detail": err.Error(), "duration_ms": time.Since(start).Milliseconds()})
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
		if weChatHasPendingLogin() {
			httpErr(w, 409, "已有进行中的微信扫码，请先完成或取消它")
			return
		}
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
