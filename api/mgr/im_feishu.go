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
