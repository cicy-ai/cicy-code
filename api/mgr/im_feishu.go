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
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const feishuPollWait = 25 * time.Second
const feishuWSIdleTimeout = 2 * time.Minute

var feishuBaseURL = "https://open.feishu.cn"

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
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if jerr := json.Unmarshal(raw, &out); jerr != nil {
		return "", true, fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(raw)))
	}
	if out.Code != 0 {
		return "", true, fmt.Errorf("feishu code=%d: %s", out.Code, out.Msg)
	}
	if strings.TrimSpace(out.TenantAccessToken) == "" {
		return "", true, nil
	}

	// bot/v3/info 不需要额外的应用权限，适合在刚创建账号、尚未完成权限配置时
	// 读取开放平台里的当前应用名。名称读取失败不影响凭据校验和账号创建。
	infoReq, _ := http.NewRequest(http.MethodGet, feishuBaseURL+"/open-apis/bot/v3/info", nil)
	infoReq.Header.Set("Authorization", "Bearer "+out.TenantAccessToken)
	infoResp, infoErr := client.Do(infoReq)
	if infoErr != nil {
		return "", true, nil
	}
	defer infoResp.Body.Close()
	infoRaw, _ := io.ReadAll(io.LimitReader(infoResp.Body, 1<<20))
	var info struct {
		Code int `json:"code"`
		Bot  struct {
			AppName string `json:"app_name"`
		} `json:"bot"`
	}
	if infoResp.StatusCode >= 200 && infoResp.StatusCode < 300 &&
		json.Unmarshal(infoRaw, &info) == nil && info.Code == 0 {
		return strings.TrimSpace(info.Bot.AppName), true, nil
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

// feishuWSRegistry:同一个 app_id 全局只允许一条长连接。飞书是 cluster 模式,
// 同 app 多条连接时事件**随机投递**到其中一条——账号 bounce/worker 重建留下的
// 旧连接会变成吞消息的僵尸(实测踩坑)。新连接上位前必须先掐死旧的。
var feishuWSRegistry = struct {
	mu sync.Mutex
	m  map[string]func()
}{m: map[string]func(){}}

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
			msg, msgID, refs := feishuExtractMessage(event)
			if msg.Peer.ChatID == "" {
				return nil
			}
			if msg.Text == "" && len(refs) == 0 {
				return nil
			}
			// 媒体下载可能要几秒,放 goroutine 别堵事件回调
			go func() {
				for _, ref := range refs {
					data, err := t.downloadResource(msgID, ref.Key, ref.Type)
					if err != nil {
						log.Printf("[feishu] account=%d 媒体下载失败 key=%s: %v", t.accID, ref.Key, err)
						if strings.Contains(strings.ToLower(err.Error()), "permission") || strings.Contains(err.Error(), "权限") {
							// 缺 im:resource 权限:直接在会话里教用户开
							_, _ = t.Send(msg.Peer, "⚠️ 收到媒体消息,但缺「获取与上传图片或文件资源」权限(im:resource)。\n去开通并发版: https://open.feishu.cn/app/"+t.appID+"/auth")
						}
						continue
					}
					if ref.Audio {
						msg.VoiceData = data
						msg.VoiceFormat = "opus"
					} else {
						name := ref.Name
						if name == "" {
							name = ref.Key
							if ref.Kind == "image" {
								name += ".png"
							}
						}
						msg.Attachments = append(msg.Attachments, botAttachment{Kind: ref.Kind, Filename: name, Bytes: data})
					}
				}
				if msg.Text == "" && len(msg.VoiceData) == 0 && len(msg.Attachments) == 0 {
					return
				}
				select {
				case t.inbox <- msg:
				default:
					log.Printf("[feishu] account=%d inbox full, dropping message", t.accID)
				}
			}()
			return nil
		}).
		// 用户点开和机器人的单聊(控制台常见默认订阅)——不需要处理,但注册个
		// 空 handler,免得 SDK 对没 handler 的事件刷 error 日志。
		OnP2ChatAccessEventBotP2pChatEnteredV1(func(ctx context.Context, event *larkim.P2ChatAccessEventBotP2pChatEnteredV1) error {
			return nil
		})

	cli := larkws.NewClient(t.appID, t.appSecret,
		larkws.WithEventHandler(handler),
		larkws.WithAutoReconnect(true),
	)

	// 上位:掐死同 app 的旧连接(cancel 不够,必须 cli.Close() 真关底层 conn,
	// 否则旧连接变僵尸,和新连接抢事件),再把自己的关闭函数登记进去。
	closeSelf := func() { cancel(); cli.Close() }
	feishuWSRegistry.mu.Lock()
	if old := feishuWSRegistry.m[t.appID]; old != nil {
		old()
	}
	feishuWSRegistry.m[t.appID] = closeSelf
	feishuWSRegistry.mu.Unlock()
	defer func() {
		// 只清掉还是自己的登记(可能已被更新的连接顶掉)
		feishuWSRegistry.mu.Lock()
		if fn, ok := feishuWSRegistry.m[t.appID]; ok && isSameCloser(fn, closeSelf) {
			delete(feishuWSRegistry.m, t.appID)
		}
		feishuWSRegistry.mu.Unlock()
		cli.Close() // 兜底:无论怎么退出,底层连接必须关死
	}()

	log.Printf("[feishu] account=%d 长连接启动 (app=%s)", t.accID, t.appID)
	if err := cli.Start(ctx); err != nil && ctx.Err() == nil {
		log.Printf("[feishu] account=%d 长连接退出: %v", t.accID, err)
		t.mu.Lock()
		t.wsErr = err
		t.mu.Unlock()
	}
}

// isSameCloser 比较两个闭包是否同一个(func 不能直接 ==,用指针比较)。
func isSameCloser(a, b func()) bool {
	return fmt.Sprintf("%p", a) == fmt.Sprintf("%p", b)
}

// feishuMediaRef 指向消息里的一个媒体资源,下载走 messages/{id}/resources/{key}。
type feishuMediaRef struct {
	Key   string // image_key / file_key
	Type  string // 资源接口的 type 参数:"image" | "file"
	Kind  string // 附件分类:"image" | "file" | "video"
	Name  string // 原始文件名(可空)
	Audio bool   // 语音消息(下载后走转写,不当附件)
}

// feishuExtractMessage 把长连接事件解析成 botMsg + 媒体引用。
// 支持:text、post(富文本取文字+图)、image、audio(语音)、media(视频)、file。
func feishuExtractMessage(event *larkim.P2MessageReceiveV1) (out botMsg, msgID string, refs []feishuMediaRef) {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return out, "", nil
	}
	m := event.Event.Message
	if m.MessageId != nil {
		msgID = strings.TrimSpace(*m.MessageId)
	}
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
	case "post":
		// 富文本:{"title":"..","content":[[{tag:text|a|at|img,...}]]}
		var parsed struct {
			Title   string                     `json:"title"`
			Content [][]map[string]interface{} `json:"content"`
		}
		if json.Unmarshal([]byte(content), &parsed) == nil {
			var sb strings.Builder
			if parsed.Title != "" {
				sb.WriteString(parsed.Title + "\n")
			}
			for _, line := range parsed.Content {
				for _, run := range line {
					switch run["tag"] {
					case "text":
						if s, _ := run["text"].(string); s != "" {
							sb.WriteString(s)
						}
					case "a":
						if s, _ := run["href"].(string); s != "" {
							sb.WriteString(" " + s + " ")
						}
					case "img":
						if k, _ := run["image_key"].(string); k != "" && len(refs) < 4 {
							refs = append(refs, feishuMediaRef{Key: k, Type: "image", Kind: "image"})
						}
					}
				}
				sb.WriteString("\n")
			}
			out.Text = strings.TrimSpace(feishuMentionRe.ReplaceAllString(sb.String(), ""))
		}
	case "image":
		var parsed struct {
			ImageKey string `json:"image_key"`
		}
		if json.Unmarshal([]byte(content), &parsed) == nil && parsed.ImageKey != "" {
			refs = append(refs, feishuMediaRef{Key: parsed.ImageKey, Type: "image", Kind: "image"})
		}
	case "audio":
		var parsed struct {
			FileKey string `json:"file_key"`
		}
		if json.Unmarshal([]byte(content), &parsed) == nil && parsed.FileKey != "" {
			refs = append(refs, feishuMediaRef{Key: parsed.FileKey, Type: "file", Audio: true})
		}
	case "media": // 视频
		var parsed struct {
			FileKey  string `json:"file_key"`
			FileName string `json:"file_name"`
		}
		if json.Unmarshal([]byte(content), &parsed) == nil && parsed.FileKey != "" {
			refs = append(refs, feishuMediaRef{Key: parsed.FileKey, Type: "file", Kind: "video", Name: parsed.FileName})
		}
	case "file":
		var parsed struct {
			FileKey  string `json:"file_key"`
			FileName string `json:"file_name"`
		}
		if json.Unmarshal([]byte(content), &parsed) == nil && parsed.FileKey != "" {
			refs = append(refs, feishuMediaRef{Key: parsed.FileKey, Type: "file", Kind: "file", Name: parsed.FileName})
		}
	}
	return out, msgID, refs
}

// downloadResource 下载消息里的媒体(需要 im:resource 权限)。50MB 上限。
func (t *feishuTransport) downloadResource(msgID, key, typ string) ([]byte, error) {
	token, err := t.tenantToken()
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/open-apis/im/v1/messages/%s/resources/%s?type=%s", feishuBaseURL, msgID, key, typ)
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var out struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		_ = json.Unmarshal(raw, &out)
		return nil, fmt.Errorf("feishu resource failed code=%d: %s", out.Code, out.Msg)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("feishu resource HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if err != nil {
		return nil, err
	}
	return data, nil
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

// feishuCreateChat creates one private group per Agent. A bot has only one P2P
// chat with a given user, so P2P cannot independently route multiple Agents.
// Separate groups let one Feishu app/bot serve any number of Agents.
func feishuCreateChat(acc *imAccount, name string) (string, error) {
	receiveID := strings.TrimSpace(acc.configString("last_feishu_open_id"))
	receiveIDType := "open_id"
	if receiveID == "" {
		receiveID, receiveIDType = feishuLocalUserID(acc.configString("app_id"))
	}
	if receiveID == "" {
		return "", fmt.Errorf("找不到当前飞书用户，请先在飞书里给这个机器人发一条消息后重试")
	}
	tr, err := newFeishuTransport(acc)
	if err != nil {
		return "", err
	}
	ft := tr.(*feishuTransport)
	token, err := ft.tenantToken()
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(M{
		"name":         strings.TrimSpace(name),
		"description":  "由 cicy-code 为 Agent 自动创建",
		"chat_mode":    "group",
		"chat_type":    "private",
		"user_id_list": []string{receiveID},
	})
	req, _ := http.NewRequest(http.MethodPost, feishuBaseURL+"/open-apis/im/v1/chats?user_id_type="+receiveIDType, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("创建飞书会话失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ChatID string `json:"chat_id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Code != 0 || strings.TrimSpace(out.Data.ChatID) == "" {
		if feishuLooksLikePermError(out.Code, out.Msg) {
			authURL := fmt.Sprintf(
				"https://open.feishu.cn/app/%s/auth?q=im:chat:create&op_from=openapi&token_type=tenant",
				ft.appID,
			)
			return "", fmt.Errorf("缺少创建群聊权限 im:chat:create，请开通后发布新版本。\n%s", authURL)
		}
		return "", fmt.Errorf("创建 Agent 飞书群失败 code=%d: %s", out.Code, strings.TrimSpace(out.Msg))
	}
	chatID := strings.TrimSpace(out.Data.ChatID)
	_, _ = ft.Send(botPeer{ChatID: chatID}, fmt.Sprintf("✅ 已绑定 Agent：%s\n之后在本群发送消息即可交给该 Agent 处理。", strings.TrimSpace(name)))
	return chatID, nil
}

// feishuOpenDirectChat opens or reuses the app bot's single P2P conversation
// with the current user and returns its chat_id.
func feishuOpenDirectChat(acc *imAccount, name string) (string, error) {
	receiveID := strings.TrimSpace(acc.configString("last_feishu_open_id"))
	receiveIDType := "open_id"
	if receiveID == "" {
		receiveID, receiveIDType = feishuLocalUserID(acc.configString("app_id"))
	}
	if receiveID == "" {
		return "", fmt.Errorf("找不到当前飞书用户，请先在飞书里给这个机器人发一条消息后重试")
	}
	tr, err := newFeishuTransport(acc)
	if err != nil {
		return "", err
	}
	ft := tr.(*feishuTransport)
	token, err := ft.tenantToken()
	if err != nil {
		return "", err
	}
	content, _ := json.Marshal(M{
		"text": fmt.Sprintf("✅ Bot 私聊已绑定 Agent：%s\n之后在这个私聊发送消息即可交给该 Agent 处理。", strings.TrimSpace(name)),
	})
	body, _ := json.Marshal(M{
		"receive_id": receiveID,
		"msg_type":   "text",
		"content":    string(content),
	})
	req, _ := http.NewRequest(http.MethodPost,
		feishuBaseURL+"/open-apis/im/v1/messages?receive_id_type="+receiveIDType, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("打开飞书 Bot 私聊失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ChatID string `json:"chat_id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Code != 0 || strings.TrimSpace(out.Data.ChatID) == "" {
		return "", fmt.Errorf("打开飞书 Bot 私聊失败 code=%d: %s", out.Code, strings.TrimSpace(out.Msg))
	}
	return strings.TrimSpace(out.Data.ChatID), nil
}

// feishuLocalUserID reuses lark-cli's locally authorized user. open_id is
// app-scoped, so it is used only when both sides use the same app. For a
// different bot app, union_id identifies the same user across apps in the
// tenant and lets that bot open the P2P conversation without a first message.
func feishuLocalUserID(appID string) (string, string) {
	path, err := exec.LookPath("lark-cli")
	if err != nil {
		return "", ""
	}
	out, err := exec.Command(path, "auth", "status").Output()
	if err == nil {
		var status struct {
			AppID      string `json:"appId"`
			Identities struct {
				User struct {
					Available bool   `json:"available"`
					OpenID    string `json:"openId"`
				} `json:"user"`
			} `json:"identities"`
		}
		if json.Unmarshal(out, &status) == nil &&
			strings.TrimSpace(status.AppID) == strings.TrimSpace(appID) &&
			status.Identities.User.Available &&
			strings.TrimSpace(status.Identities.User.OpenID) != "" {
			return strings.TrimSpace(status.Identities.User.OpenID), "open_id"
		}
	}

	out, err = exec.Command(path, "api", "GET", "/open-apis/authen/v1/user_info", "--as", "user").Output()
	if err != nil {
		return "", ""
	}
	var current struct {
		OK   bool `json:"ok"`
		Data struct {
			UnionID string `json:"union_id"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &current) != nil || !current.OK {
		return "", ""
	}
	return strings.TrimSpace(current.Data.UnionID), "union_id"
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

// probeCreateChatPermission uses an invalid member ID so no group is actually
// created. Permission errors happen before member validation; a member error
// therefore means im:chat:create is already available.
func (t *feishuTransport) probeCreateChatPermission() (ok bool, detail string) {
	token, err := t.tenantToken()
	if err != nil {
		return false, err.Error()
	}
	body, _ := json.Marshal(M{
		"name":         "cicy-code permission probe",
		"chat_mode":    "group",
		"chat_type":    "private",
		"user_id_list": []string{"ou_cicycode_permission_probe"},
	})
	req, _ := http.NewRequest(http.MethodPost,
		feishuBaseURL+"/open-apis/im/v1/chats?user_id_type=open_id", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return false, "创建群聊权限探针返回异常"
	}
	if out.Code == 0 {
		return true, ""
	}
	if feishuLooksLikePermError(out.Code, out.Msg) {
		return false, fmt.Sprintf("code=%d: %s", out.Code, out.Msg)
	}
	return true, ""
}

// probeGroupMessagePermission checks im:message.group_msg without consuming
// real chat data. With the scope enabled, the fake chat fails business
// validation; without it, Feishu returns a permission error first.
func (t *feishuTransport) probeGroupMessagePermission() (ok bool, detail string) {
	token, err := t.tenantToken()
	if err != nil {
		return false, err.Error()
	}
	req, _ := http.NewRequest(http.MethodGet,
		feishuBaseURL+"/open-apis/im/v1/messages?container_id_type=chat&container_id=oc_cicycode_permission_probe&page_size=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return false, "群消息权限探针返回异常"
	}
	if out.Code == 0 {
		return true, ""
	}
	if feishuLooksLikePermError(out.Code, out.Msg) {
		return false, fmt.Sprintf("code=%d: %s", out.Code, out.Msg)
	}
	return true, ""
}

// probeResourceUploadPermission checks media upload scope without uploading a
// file. Once authorized, the empty request reaches parameter validation.
func (t *feishuTransport) probeResourceUploadPermission() (ok bool, detail string) {
	token, err := t.tenantToken()
	if err != nil {
		return false, err.Error()
	}
	req, _ := http.NewRequest(http.MethodPost, feishuBaseURL+"/open-apis/im/v1/images", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return false, "媒体资源权限探针返回异常"
	}
	if out.Code == 0 {
		return true, ""
	}
	if feishuLooksLikePermError(out.Code, out.Msg) {
		return false, fmt.Sprintf("code=%d: %s", out.Code, out.Msg)
	}
	return true, ""
}

func feishuGroupBindMissingPermissions(acc *imAccount) ([]string, error) {
	tr, err := newFeishuTransport(acc)
	if err != nil {
		return nil, err
	}
	ft := tr.(*feishuTransport)
	missing := []string{}
	if ok, _ := ft.probeCreateChatPermission(); !ok {
		missing = append(missing, "im:chat:create")
	}
	if ok, _ := ft.probeGroupMessagePermission(); !ok {
		missing = append(missing, "im:message.group_msg")
	}
	if ok, _ := ft.probeResourceUploadPermission(); !ok {
		missing = append(missing, "im:resource")
	}
	return missing, nil
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

	// 4. 创建群权限:一个 App 服务多个 Agent 时,每个 Agent 需要独立群聊。
	if verr == nil && ft != nil {
		if ok, detail := ft.probeCreateChatPermission(); ok {
			add("create_chat_perm", "创建群聊权限", "ok", "im:chat:create 已开通", "")
		} else {
			add("create_chat_perm", "创建群聊权限", "fail",
				"在「权限管理」搜 im:chat:create 并开通，然后到版本管理创建版本并发布。"+detail,
				authURL+"?q=im:chat:create&op_from=openapi&token_type=tenant")
		}
	}

	// 5. 普通群消息权限:没有它时只有 @Bot 的群消息能进入 Agent。
	if verr == nil && ft != nil {
		if ok, detail := ft.probeGroupMessagePermission(); ok {
			add("group_message_perm", "接收群消息权限", "ok", "im:message.group_msg 已开通", "")
		} else {
			add("group_message_perm", "接收群消息权限", "fail",
				"在「权限管理」开通 im:message.group_msg，然后到版本管理创建版本并发布。"+detail,
				authURL+"?q=im:message.group_msg&op_from=openapi&token_type=tenant")
		}
	}

	// 6. 媒体资源权限:图片、文件、视频和音频收发都依赖它。
	if verr == nil && ft != nil {
		if ok, detail := ft.probeResourceUploadPermission(); ok {
			add("resource_perm", "媒体文件权限", "ok", "im:resource 已开通", "")
		} else {
			add("resource_perm", "媒体文件权限", "fail",
				"在「权限管理」开通 im:resource，然后到版本管理创建版本并发布。"+detail,
				authURL+"?q=im:resource&op_from=openapi&token_type=tenant")
		}
	}

	// 7. 收消息(事件订阅,经验判断)。收不到=端到端没通,判失败。
	if acc.configString("chat_id") != "" || !imLastInboundTimeGet(acc.ID).IsZero() {
		add("inbound", "接收消息", "ok", "已收到过消息,事件订阅正常", "")
	} else {
		add("inbound", "接收消息", "fail",
			"机器人还听不见你说话。到「事件与回调」:订阅方式选「使用长连接接收事件」,添加事件「接收消息 im.message.receive_v1」(会提示顺带申请单聊消息读取权限,一起开通);再到版本管理**创建版本并发布**。发布后在飞书搜索你的应用名,私聊发一条 /help——这一项会自动变绿。", eventURL)
	}

	// 8. 发布提示:凡有失败项,都补一条发版检查(飞书所有配置不发版不生效)
	if !allOK {
		add("publish", "发布版本", "warn",
			"改完任何权限/事件后,必须到「版本管理与发布」创建版本并发布,配置才会生效(企业自建应用自己就是审核人,秒过)。", versionURL)
	}

	// 9. 真实测试发送(已捕获会话才发)
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
