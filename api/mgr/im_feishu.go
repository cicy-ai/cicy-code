// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// 飞书(Lark)IM transport。
//
// 入站:飞书没有消息轮询 API,桌面/本机部署又没有公网 IP 配 webhook——和
// BaiLongma 一样选官方 SDK 的**长连接**(WebSocket)收事件,只需要 App ID/Secret。
// 但 im worker 的循环是 Poll 拉模型,所以这里做一座「WS→Poll 桥」:
// SDK 长连接收到的消息推进 inbox channel,Poll() 阻塞取,worker 循环零改动。
//
// 出站:普通 HTTP(im/v1/messages,receive_id_type=chat_id),tenant token 缓存,
// 与长连接无关。飞书没有 typing 指示,Typing 为 no-op;CanEdit=false(per-item
// 推送模式不需要 edit)。
//
// 长连接生命周期:首次 Poll 才拉起(懒启动);watchdog 检查「最近有没有人 Poll」,
// worker 停了(账号禁用/删除)超过 2 分钟没人 Poll 就自杀,避免 goroutine 泄漏。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const feishuBaseURL = "https://open.feishu.cn"
const feishuPollWait = 25 * time.Second
const feishuWSIdleTimeout = 2 * time.Minute

// feishuValidateCredentials 用 tenant_access_token 接口验证 App ID/Secret。
// reachable=false 表示网络不通(凭据先存着,worker 会重试);appName 尽力而为,拿不到给空。
func feishuValidateCredentials(appID, appSecret string) (appName string, reachable bool, err error) {
	body, _ := json.Marshal(map[string]string{"app_id": appID, "app_secret": appSecret})
	req, _ := http.NewRequest(http.MethodPost, feishuBaseURL+"/open-apis/auth/v3/tenant_access_token/internal", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, herr := client.Do(req)
	if herr != nil {
		return "", false, herr
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if jerr := json.Unmarshal(raw, &out); jerr != nil {
		return "", true, fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(raw)))
	}
	if out.Code != 0 {
		return "", true, fmt.Errorf("feishu code=%d: %s", out.Code, out.Msg)
	}
	return "", true, nil
}

type feishuTransport struct {
	accID     int64
	appID     string
	appSecret string

	inbox chan botMsg

	mu        sync.Mutex
	wsRunning bool
	wsErr     error
	lastPoll  time.Time

	tokenMu  sync.Mutex
	token    string
	tokenExp time.Time
}

func newFeishuTransport(acc *imAccount) (botTransport, error) {
	appID := strings.TrimSpace(acc.configString("app_id"))
	appSecret := strings.TrimSpace(acc.Secret)
	if appID == "" || appSecret == "" {
		return nil, fmt.Errorf("feishu app_id/app_secret not configured")
	}
	return &feishuTransport{
		accID:     acc.ID,
		appID:     appID,
		appSecret: appSecret,
		inbox:     make(chan botMsg, 64),
	}, nil
}

func (t *feishuTransport) Kind() string  { return imPlatformFeishu }
func (t *feishuTransport) CanEdit() bool { return false }

func (t *feishuTransport) Edit(peer botPeer, messageID, text string) error {
	return errBotEditUnsupported
}

// Typing:飞书没有输入中指示,no-op。
func (t *feishuTransport) Typing(peer botPeer) error { return nil }

// feishuMentionRe 去掉飞书群里 @机器人 产生的占位符(@_user_1 等)。
var feishuMentionRe = regexp.MustCompile(`@_user_\d+\s*`)

// Poll 实现「WS→Poll 桥」:确保长连接在跑,然后最多等 feishuPollWait 取 inbox。
// cursor 对飞书无意义(事件由长连接实时投递),恒返回 ""。
func (t *feishuTransport) Poll(cursor string) ([]botMsg, string, error) {
	t.mu.Lock()
	t.lastPoll = time.Now()
	if err := t.wsErr; err != nil {
		t.wsErr = nil
		t.mu.Unlock()
		return nil, "", err
	}
	t.ensureWSLocked()
	t.mu.Unlock()

	var msgs []botMsg
	timer := time.NewTimer(feishuPollWait)
	defer timer.Stop()
	select {
	case m := <-t.inbox:
		msgs = append(msgs, m)
		// 把已经到队的都带走
		for {
			select {
			case m2 := <-t.inbox:
				msgs = append(msgs, m2)
			default:
				return msgs, "", nil
			}
		}
	case <-timer.C:
		return nil, "", nil
	}
}

// ensureWSLocked 懒启动长连接(调用方须持有 t.mu)。
func (t *feishuTransport) ensureWSLocked() {
	if t.wsRunning {
		return
	}
	t.wsRunning = true
	go t.runWS()
}

func (t *feishuTransport) runWS() {
	defer func() {
		t.mu.Lock()
		t.wsRunning = false
		t.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// watchdog:worker 不再 Poll(账号停用/删除/进程收尾)就撤掉长连接
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.mu.Lock()
				idle := time.Since(t.lastPoll)
				t.mu.Unlock()
				if idle > feishuWSIdleTimeout {
					log.Printf("[feishu] account=%d ws idle %s, shutting down", t.accID, idle.Round(time.Second))
					cancel()
					return
				}
			}
		}
	}()

	handler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			msg := feishuExtractMessage(event)
			if msg.Text == "" || msg.Peer.ChatID == "" {
				return nil
			}
			select {
			case t.inbox <- msg:
			default:
				log.Printf("[feishu] account=%d inbox full, dropping message", t.accID)
			}
			return nil
		})

	cli := larkws.NewClient(t.appID, t.appSecret,
		larkws.WithEventHandler(handler),
		larkws.WithAutoReconnect(true),
	)
	log.Printf("[feishu] account=%d 长连接启动 (app=%s)", t.accID, t.appID)
	if err := cli.Start(ctx); err != nil && ctx.Err() == nil {
		log.Printf("[feishu] account=%d 长连接退出: %v", t.accID, err)
		t.mu.Lock()
		t.wsErr = err
		t.mu.Unlock()
	}
}

// feishuExtractMessage 把长连接事件解析成 botMsg(只处理 text 消息)。
func feishuExtractMessage(event *larkim.P2MessageReceiveV1) botMsg {
	var out botMsg
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return out
	}
	m := event.Event.Message
	if m.ChatId != nil {
		out.Peer = botPeer{ChatID: strings.TrimSpace(*m.ChatId)}
	}
	if event.Event.Sender != nil && event.Event.Sender.SenderId != nil && event.Event.Sender.SenderId.OpenId != nil {
		out.FromID = "feishu:open_id:" + *event.Event.Sender.SenderId.OpenId
	}
	msgType := ""
	if m.MessageType != nil {
		msgType = *m.MessageType
	}
	content := ""
	if m.Content != nil {
		content = *m.Content
	}
	switch msgType {
	case "text":
		var parsed struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(content), &parsed) == nil {
			out.Text = strings.TrimSpace(feishuMentionRe.ReplaceAllString(parsed.Text, ""))
		}
	default:
		// 非文本(图片/富文本/语音)暂不支持,给出可见的占位让用户知道没收到
		out.Text = ""
	}
	return out
}

/* ───────────────────────── outbound (HTTP) ───────────────────────── */

func (t *feishuTransport) tenantToken() (string, error) {
	t.tokenMu.Lock()
	defer t.tokenMu.Unlock()
	if t.token != "" && time.Now().Before(t.tokenExp) {
		return t.token, nil
	}
	body, _ := json.Marshal(map[string]string{"app_id": t.appID, "app_secret": t.appSecret})
	req, _ := http.NewRequest(http.MethodPost, feishuBaseURL+"/open-apis/auth/v3/tenant_access_token/internal", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("feishu token: unexpected response")
	}
	if out.Code != 0 || out.TenantAccessToken == "" {
		return "", fmt.Errorf("feishu token failed code=%d: %s", out.Code, out.Msg)
	}
	t.token = out.TenantAccessToken
	// 提前 2 分钟过期,避免边界撞线
	t.tokenExp = time.Now().Add(time.Duration(maxInt(60, out.Expire-120)) * time.Second)
	return t.token, nil
}

func (t *feishuTransport) Send(peer botPeer, text string) (string, error) {
	chatID := strings.TrimSpace(peer.ChatID)
	if chatID == "" {
		return "", fmt.Errorf("feishu send: empty chat_id")
	}
	token, err := t.tenantToken()
	if err != nil {
		return "", err
	}
	content, _ := json.Marshal(map[string]string{"text": text})
	body, _ := json.Marshal(map[string]string{
		"receive_id": chatID,
		"msg_type":   "text",
		"content":    string(content),
	})
	req, _ := http.NewRequest(http.MethodPost, feishuBaseURL+"/open-apis/im/v1/messages?receive_id_type=chat_id", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("feishu send: unexpected response")
	}
	if out.Code != 0 {
		// token 失效即刻作废缓存,下次重取
		if out.Code == 99991663 || out.Code == 99991661 {
			t.tokenMu.Lock()
			t.token = ""
			t.tokenMu.Unlock()
		}
		return "", fmt.Errorf("feishu send failed code=%d: %s", out.Code, out.Msg)
	}
	return out.Data.MessageID, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

/* ───────────────────────── 权限体检(测试按钮)─────────────────────────
   飞书没有公开的「查我开通了哪些权限」接口,用探针推断:
   1. 凭据:tenant_access_token 换取成功与否
   2. 长连接:worker 里的 transport WS 是否在跑
   3. 发消息权限(im:message):向一个不存在的 chat 试发,错误码是「无权限」
      还是「目标非法」——后者说明权限已就绪,报错只是因为假目标(预期)
   4. 事件订阅(im.message.receive_v1 + 长连接模式 + 发版):无法直接查,
      用「是否收到过任何入站消息」经验判断
   5. 若已捕获真实 chat_id,追加一次真实测试发送 */

// feishuPermErrCodes:调用 API 时因缺权限/未授权返回的错误码。
// 99991679=无该 scope;99991672=应用未启用相关能力;99991668=token 无权限。
var feishuPermErrCodes = map[int]bool{99991679: true, 99991672: true, 99991668: true}

func feishuLooksLikePermError(code int, msg string) bool {
	if feishuPermErrCodes[code] {
		return true
	}
	m := strings.ToLower(msg)
	return strings.Contains(m, "permission") || strings.Contains(m, "scope") ||
		strings.Contains(m, "无权限") || strings.Contains(m, "权限")
}

// probeSendPermission 用假 chat_id 探测 im:message 发送权限。
// ok=true 表示权限就绪(错误来自假目标,预期);ok=false 表示确认缺权限。
func (t *feishuTransport) probeSendPermission() (ok bool, detail string) {
	_, err := t.Send(botPeer{ChatID: "oc_cicycode_permission_probe"}, "probe")
	if err == nil { // 理论上不可能;当作就绪
		return true, "发送权限就绪"
	}
	es := err.Error()
	var code int
	if _, serr := fmt.Sscanf(es, "feishu send failed code=%d", &code); serr == nil && feishuLooksLikePermError(code, es) {
		return false, es
	}
	if strings.Contains(es, "feishu token failed") {
		return false, es // 凭据层的问题,让上一条检查兜住语义
	}
	if feishuLooksLikePermError(0, es) {
		return false, es
	}
	return true, "" // 走到了业务校验(假目标被拒)= 权限已开通
}

// feishuCheckItem 是体检单里的一项:配置向导按它逐项亮灯,每项可带直达链接。
type feishuCheckItem struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail,omitempty"`
	Link   string `json:"link,omitempty"`
}

func feishuRunChecks(acc *imAccount) (checks []feishuCheckItem, allOK bool) {
	allOK = true
	add := func(key, name, status, detail, link string) {
		if status == "fail" {
			allOK = false
		}
		checks = append(checks, feishuCheckItem{Key: key, Name: name, Status: status, Detail: detail, Link: link})
	}

	appID := strings.TrimSpace(acc.configString("app_id"))
	authURL := "https://open.feishu.cn/app/" + appID + "/auth"           // 权限管理
	eventURL := "https://open.feishu.cn/app/" + appID + "/event"         // 事件与回调
	versionURL := "https://open.feishu.cn/app/" + appID + "/app_version" // 版本管理与发布

	// 1. 凭据
	_, reachable, verr := feishuValidateCredentials(appID, strings.TrimSpace(acc.Secret))
	switch {
	case verr == nil:
		add("creds", "应用凭据", "ok", "App ID/Secret 有效", "")
	case !reachable:
		add("creds", "应用凭据", "fail", "网络不通,无法访问 open.feishu.cn(检查网络/代理): "+verr.Error(), "")
	default:
		add("creds", "应用凭据", "fail", "凭据无效,检查 App ID/App Secret: "+verr.Error(), "https://open.feishu.cn/app/"+appID+"/baseinfo")
	}

	// 2. 长连接
	var ft *feishuTransport
	if tr := imTransportFor(acc.ID); tr != nil {
		ft, _ = tr.(*feishuTransport)
	}
	if ft != nil {
		ft.mu.Lock()
		running := ft.wsRunning
		ft.mu.Unlock()
		if running {
			add("ws", "长连接", "ok", "事件通道在线", "")
		} else {
			add("ws", "长连接", "warn", "正在重连或刚启动,稍等几秒", "")
		}
	} else {
		add("ws", "长连接", "warn", "账号 worker 未连接(账号可能被停用)", "")
		if built, err := imBuildTransport(acc); err == nil {
			ft, _ = built.(*feishuTransport)
		}
	}

	// 3. 发消息权限(假目标探针)
	if verr == nil && ft != nil {
		if ok, detail := ft.probeSendPermission(); ok {
			add("send_perm", "发消息权限", "ok", "im:message 已开通", "")
		} else {
			add("send_perm", "发消息权限", "fail",
				"在「权限管理」搜 im:message,开通「获取与发送单聊、群组消息」,然后到版本管理**创建版本并发布**。"+detail, authURL)
		}
	}

	// 4. 收消息(事件订阅,经验判断)。收不到=端到端没通,判失败。
	if acc.configString("chat_id") != "" || !imLastInboundTimeGet(acc.ID).IsZero() {
		add("inbound", "接收消息", "ok", "已收到过消息,事件订阅正常", "")
	} else {
		add("inbound", "接收消息", "fail",
			"机器人还听不见你说话。到「事件与回调」:订阅方式选「使用长连接接收事件」,添加事件「接收消息 im.message.receive_v1」(会提示顺带申请单聊消息读取权限,一起开通);再到版本管理**创建版本并发布**。发布后在飞书搜索你的应用名,私聊发一条 /help——这一项会自动变绿。", eventURL)
	}

	// 5. 发布提示:凡有失败项,都补一条发版检查(飞书所有配置不发版不生效)
	if !allOK {
		add("publish", "发布版本", "warn",
			"改完任何权限/事件后,必须到「版本管理与发布」创建版本并发布,配置才会生效(企业自建应用自己就是审核人,秒过)。", versionURL)
	}

	// 6. 真实测试发送(已捕获会话才发)
	if peer := imPeerForAccount(acc); !peer.empty() && ft != nil && verr == nil {
		if _, err := imSendOutbound(imOutboundMessage{AccountID: acc.ID, Transport: ft, Peer: peer, Text: "✅ cicy 测试消息", Purpose: imOutboundPurposeTest}); err != nil {
			add("send", "测试发送", "fail", err.Error(), "")
		} else {
			add("send", "测试发送", "ok", "已发出,去飞书看一眼", "")
		}
	}
	return checks, allOK
}

func feishuHandleTest(w http.ResponseWriter, acc *imAccount) {
	start := time.Now()
	checks, allOK := feishuRunChecks(acc)
	// 兼容纯文本展示(账号详情页的测试结果框)
	icon := map[string]string{"ok": "✅", "warn": "⚠️", "fail": "❌"}
	var lines []string
	for _, c := range checks {
		line := icon[c.Status] + " " + c.Name + ":" + c.Detail
		if c.Link != "" && c.Status != "ok" {
			line += "\n   → " + c.Link
		}
		lines = append(lines, line)
	}
	J(w, M{"ok": allOK, "detail": strings.Join(lines, "\n"), "checks": checks, "duration_ms": time.Since(start).Milliseconds()})
}
