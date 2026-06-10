# IM 消息收发链路完整文档

> 本文档描述 cicy-code 中 IM（即时通讯）消息从用户输入到 agent 回复的完整链路，覆盖 WeChat、Telegram 等平台。
>
> 适用版本：基于 `api/mgr/` 模块（截至 2026-05-20）

## 1. 总览

cicy-code 把每个 AI agent 当作一个 tmux pane。IM 集成的本质是：

- **入站**：把 IM 消息写入 agent 所在的 tmux pane（等价于"用户在终端打字"）
- **出站**：拦截 agent 调用 LLM API 的响应，把 answer 推回 IM 用户

平台抽象统一在 `botTransport` 接口后，新增平台只需实现该接口，无需改动 reply hook、agent API、跨 agent 回调等公共逻辑。

```
┌─────────┐  poll      ┌──────────────┐  tmux send  ┌───────────────┐
│  IM     │ ─────────▶ │ imWorker     │ ──────────▶ │ Agent CLI     │
│ Server  │            │ + Transport  │             │ (在 pane 中)   │
│         │ ◀───────── │ + Reply Hook │ ◀────────── │ → LLM API     │
└─────────┘  send/edit └──────────────┘  audit hook └───────────────┘
                                                          │
                                                          ▼
                                                    AI Gateway 反向代理
                                                    (拦截 SSE / 完整响应)
```

## 2. 核心数据结构

### 2.1 `botTransport` —— 平台抽象（im.go）

```go
type botTransport interface {
    Kind() string                                          // "telegram" / "wechat"
    Poll(cursor string) (msgs []botMsg, nextCursor string, err error)
    Send(peer botPeer, text string) (messageID string, err error)
    Edit(peer botPeer, messageID, text string) error      // 不支持时返回 errBotEditUnsupported
    Typing(peer botPeer) error
    CanEdit() bool                                        // 决定是否启用流式 edit
}
```

| 实现 | 文件 | CanEdit | 备注 |
|------|------|---------|------|
| `telegramTransport` | `im_telegram.go` | `true` | Bot Token，长轮询 `getUpdates` |
| `weChatTransport`   | `im_wechat.go`   | `false` | ilink-bot 协议，扫码登录 |

### 2.2 `imAccount` —— 账号配置

存储在 SQLite 表 `im_accounts`，关键字段：

| 字段 | 说明 |
|------|------|
| `platform` | `telegram` / `wechat`（统一通过 `normalizeIMPlatform` 归一化） |
| `secret` | Telegram 是 bot token；WeChat 留空（QR 登录后写文件） |
| `config` | JSON：cursor、chat_id、last_peer_chat_id、last_peer_context_token |
| `bound_pane_id` | 绑定的 agent pane（决定消息送达哪个 agent） |
| `inbound_to_agent` | 是否允许把入站消息送给 agent |

### 2.3 `botMsg` / `botPeer` —— 消息抽象

```go
type botMsg struct {
    Text        string
    Peer        botPeer       // 回复目标
    FromID      string
    VoiceData   []byte        // 语音原始字节（nil = 非语音）
    VoiceFormat string        // "silk" / "amr" / "ogg"
}

type botPeer struct {
    ChatID       string       // 必填：会话 ID
    ContextToken string       // WeChat 专用：每条消息的上下文凭证
}
```

WeChat 的 `ContextToken` 有 30 秒 TTL（`imContextTokenTTL`），超时需用户重新发消息才能拿到新 token。

## 3. 入站链路：用户消息 → Agent

### 3.1 长轮询（imWorker）

启动入口在 `imManagerStart()`（`im.go`），每 30 秒调用 `imReconcile()` 同步数据库：

- 启用的账号 → 启动 `imWorker.loop()` goroutine
- 禁用/删除的账号 → close `stop` channel 停掉 worker

`imWorker.loop()`（`im.go`）：

```
for {
    acc := imGetAccount(w.accID)
    tr  := imBuildTransport(acc)              // → newTelegramTransport / newWeChatTransport
    cursor := imLoadCursor(acc)
    for {
        msgs, next, err := tr.Poll(cursor)    // 阻塞长轮询
        if errIMSessionExpired { break }      // WeChat session 失效 → 重新扫码
        if next != cursor { imSaveCursor }
        for _, msg := range msgs {
            imHandleInbound(acc, tr, msg)
        }
    }
}
```

### 3.2 平台 Poll 实现

#### Telegram（`im_telegram.go:Poll`）

```
GET https://api.telegram.org/bot{TOKEN}/getUpdates?timeout=30&offset={cursor}
→ 解析 result[].message.text/caption → botMsg{Text, Peer{ChatID}}
→ next cursor = max(update_id) + 1
```

#### WeChat（`im_wechat.go:Poll`）

```
POST {baseURL}/ilink/bot/getupdates  body={get_updates_buf: cursor}, timeout=42s
errcode == -14 → errIMSessionExpired (token 失效，触发重新扫码)
errcode != 0  → 返回错误
否则：
  - resp.GetUpdatesBuf 持久化为新 cursor
  - 遍历 resp.Msgs，过滤 MessageType=1 (用户消息)
  - 文本消息：weChatExtractText → botMsg{Text}
  - 语音消息：weChatDownloadVoiceCDN / weChatDownloadVoiceByMsgID → botMsg{VoiceData}
```

### 3.3 `imHandleInbound`（im.go）—— 入站消息处理

```
imHandleInbound(acc, tr, msg)
├─ 1. 语音转文字
│  if len(msg.VoiceData) > 0:
│      transcribed = imTranscribeVoice(VoiceData, VoiceFormat)
│      msg.Text = transcribed
│
├─ 2. 记住 peer & 时间戳
│  imRememberPeer(accID, msg.Peer)              # 保存 peer
│  imLastInboundTime[accID] = now              # context_token 时效起点
│
├─ 3. 自动捕获 chat_id（首次绑定）
│  Telegram: 第一次收到消息时把 chat_id 写入 config
│  WeChat:   持久化 last_peer_chat_id + last_peer_context_token
│
├─ 4. 路由检查
│  if pane=="" || !acc.InboundToAgent: 直接丢弃，记日志
│
├─ 5. 发送 ack（仅可编辑 transport）
│  Telegram: 发送 "Thinking..." 占位消息，记 messageID 供后续 edit
│  WeChat:   跳过（不可编辑）
│  tr.Typing(peer)                              # "正在输入" 状态
│
├─ 6. 注册 reply push（关键！）
│  imRegisterReplyPushForInbound(pane, accID)
│  # 标记"接下来 pane 这个 turn 的回复要推给这个 IM 账号"
│
└─ 7. 把文本送进 pane
   sendTextToPane(pane, text, true)             # tmux send-keys
   失败 → imCancelReplyPushForInbound + 发送错误消息
```

### 3.4 `imPendingReplyPush` —— 来源追踪

这是 IM 与其他入口（web、CLI、API）的关键隔离机制：

- **写入**：`imHandleInbound` → `imRegisterReplyPushForInbound(pane, accID)`
- **读取**：`newReplyHooksForPane(pane)` → `imPeekReplyPushAccountsForPane`
- **清理**：`imReplyPushHook.finalize` → `imCancelReplyPushForInbound`

如果是 web 用户在浏览器输入消息，pending 列表是空的，agent 回复不会推到 IM —— **避免泄漏**。

## 4. 中间环节：Agent CLI → AI Gateway

agent CLI（claude/codex/opencode 等）配置成走本地 AI 网关代理：

```
Agent CLI ──HTTP──▶ http://localhost:PORT/api/openclaw/...
                              │
                              ▼
              openclaw_gateway.go:handleOpenClawProvider
              ├─ 1. 解析 provider / agent_id / 改写路径
              ├─ 2. newAIGatewayAuditSession(...)
              │     └─ replyHooks = newReplyHooksForPane(agentID)
              ├─ 3. audit.writeStartSnapshots()
              └─ 4. 反向代理 ServeHTTP（响应包装为 auditReadCloser）
                              │
                              ▼
                    真实 LLM provider (Claude/OpenAI/DeepSeek/...)
```

`newReplyHooksForPane(agentID)`（`im_reply_hook.go`）：

```go
hooks = []
hooks += drainCallbackHooksForPane(agentID)           // 跨 agent 回调
hooks += newTGReplyPushHook(agentID)                  // legacy TG (单 pane 配置)
for accID := range imPeekReplyPushAccountsForPane(agentID):
    if !tr.CanEdit():                                 // WeChat
        skip  // 由 completeFromResponse 直推
    else:                                             // Telegram
        hooks += &imReplyPushHook{...}
return hooks
```

## 5. 出站链路：Agent 回复 → IM 用户

### 5.1 流式阶段（仅可编辑 transport）

`auditReadCloser.Read` 解析 SSE → `emitReplyStreamPayload` → `replyHooks[].handleEvents`：

```
imReplyPushHook.handleEvents(events):
    for each "sse" event:
        if stream_kind == "thinking": h.thinking += content
        if stream_kind == "answer":   h.answer   += content
    if changed && canEdit:
        schedulePushLocked(force=false)              # 间隔 ≥ 1.5s
            └─ flush()
                └─ tr.Edit(peer, messageID, renderLocked())
                   失败 → 改为 Send 一条新消息
```

`renderLocked()` 输出：

```
Thinking...

Thinking:
<累积的思考内容>

Reply:
<累积的回复内容>
```

### 5.2 完成阶段

`completeFromResponse`（`ai_gateway_audit.go:549-714`）：

```
1. 解析最终响应：thinking / answer / tool_calls / usage
2. 累加 reply.Items（type: thinking / text / tool_use）
3. 【WeChat 直推点】（line 619）
   if parsed.Answer != "":
       imSendForPaneWithPurpose(agentID, "", parsed.Answer, "reply")
4. 写 reply.json 快照
5. broadcastStatusLocked → WebSocket
6. replyHooks[].finalize(replySnapshot)
   ├─ imReplyPushHook (Telegram):
   │   收集本 turn 新增的 text item
   │   → schedulePushLocked(force=true) → flush() → Edit/Send 最终回复
   └─ replyCallbackHook (跨 agent):
       sendTextToPane(callbackTo, "[B] ✅ work done", true)
```

### 5.3 统一出站 `imSendOutbound`（im.go）

所有 IM 发送（ack / reply / error / programmatic / test）都走这一个函数：

```go
func imSendOutbound(msg imOutboundMessage) (imOutboundResult, error) {
    text := imClampMessage(msg.Text)              // 清空白 / 限长 3500 rune
    mid, err := msg.Transport.Send(msg.Peer, text)
    log.Printf("[im] send %s account=%d kind=%s purpose=%s ...",
               ok/FAIL, accID, kind, purpose)
    return result, err
}
```

`Purpose` 枚举（`imOutboundPurpose`）：

| Purpose | 触发场景 |
|---------|----------|
| `ack` | 入站时发的 "Thinking..." 占位 |
| `reply` | Agent 最终回答 |
| `error` | 内部错误反馈给用户 |
| `test` | UI 上的"测试发送"按钮 |
| `programmatic` | `/api/im/send` API 直接调用 |
| `unknown` | 兜底 |

### 5.4 平台 Send 实现

#### Telegram（`im_telegram.go:Send/Edit`）

```
Send:  POST https://api.telegram.org/bot{TOKEN}/sendMessage
       form: chat_id, text → 返回 message_id

Edit:  POST https://api.telegram.org/bot{TOKEN}/editMessageText
       form: chat_id, message_id, text
```

#### WeChat（`im_wechat.go:Send`）

```
POST {baseURL}/ilink/bot/sendmessage
body: {
    to_user_id:    peer.ChatID,
    context_token: peer.ContextToken,    // ← 必须，30s 内有效
    msg_type:      1 (text),
    item_list:     [{type:1, text:"..."}],
}
```

注意 WeChat 必须在 30 秒内回复（`imContextTokenTTL`）。如果 LLM 思考太久，会出现"context_token 过期"，此时只能等用户再次发消息。

## 6. 关键设计要点

### 6.1 Edit vs Send 策略

| Transport | 流式行为 | 用户感受 |
|-----------|----------|----------|
| Telegram | 1 条消息持续 edit（间隔 ≥1.5s） | 像"打字机"实时增长 |
| WeChat | 完成时一次性 send | 思考期间静默，最后一条消息 |

为什么 WeChat 不流式发？
1. ilink-bot 协议无 edit 接口
2. 频繁 send 会触发风控
3. context_token 30s TTL，等不及多次发送

### 6.2 防重发冷却

`imSendCooldown = 6s`：tmux send-keys 有时会因为 retry 多发一次回车，触发第二个虚假回复。`imLiveMessageState.lastSendTime` 记录上次发送时间，6 秒内的重复 flush 直接跳过。

### 6.3 Item ID 增量推送

`imLiveMessageState.lastItemID` 记录上次已推送的 item id：

- 同一 turn 内（`turnID` 相同）累加
- 新 turn 开始（`turnID` 变化）→ 重置为 0

这样 Telegram 在 `finalize` 时只发"本次新增的 text items"，不会重复发以前的内容。

### 6.4 Tool call 不推送

只有 `type=text` 和 `stream_kind=thinking|answer` 会被推到 IM。`tool_use` / `tool_result` / `web_search` 完全不进入 IM，避免给用户看到原始 JSON。

### 6.5 跨 Agent 回调

agent A 的 CLI 通过 `cicy-agent msg --callback` 给 agent B 发消息：

```
A.CLI → /api/agents/msg {to:B, text, callback:true}
     → registerReplyCallback(B, callbackTo=A)
     → sendTextToPane(B, text)

后续 B 的 turn 开始：
     → newReplyHooksForPane(B) → drainCallbackHooksForPane(B)
       → 把 pendingCallbacks[B] 转成 replyCallbackHook 列表

B 的 turn 完成（且无 tool_calls）：
     → replyCallbackHook.finalize
       → sendTextToPane(A, "[B] ✅ work done")
```

如果 B 还在调 tools（中间响应有 `tool_calls`），回调会被 re-queue，等真正的最终响应再触发。

## 7. 文件路径速查

| 文件 | 职责 |
|------|------|
| `api/mgr/im.go` | 通用框架：botTransport、imAccount、imWorker、imSendOutbound、入站路由、reply push 注册 |
| `api/mgr/im_telegram.go` | Telegram transport 实现 |
| `api/mgr/im_wechat.go` | WeChat ilink-bot transport 实现（QR 登录、长轮询、语音下载） |
| `api/mgr/im_voice.go` | 语音转文字（imTranscribeVoice） |
| `api/mgr/im_reply_hook.go` | imReplyPushHook：流式 edit + finalize 发送 |
| `api/mgr/gateway_reply_callback.go` | aiGatewayReplyHook 接口 + 跨 agent 回调 |
| `api/mgr/gateway_reply_text.go` | `/api/agents/reply-text`：跨 agent 读取最终回复文本 |
| `api/mgr/ai_gateway_audit.go` | AI 网关审计：拦截请求/响应、创建 hooks、调用 finalize |
| `api/mgr/ai_gateway.go` | 通用 AI 网关反向代理入口（`handleAIGatewayProxy`）：`newAIGatewayAuditSession` 调用点 |
| `api/mgr/tmux.go` | `sendTextToPane`：tmux send-keys 把文本写到 agent pane |
| `api/mgr/tg.go` | Telegram legacy（按 pane 配置的 chat_id 推送 hook） |

## 8. 增加新平台（如 Discord）的步骤

1. **实现 `botTransport`** —— 新建 `api/mgr/im_discord.go`
   ```go
   type discordTransport struct{ ... }
   func (t *discordTransport) Kind() string { return "discord" }
   func (t *discordTransport) CanEdit() bool { return true }   // Discord 支持 edit
   func (t *discordTransport) Poll(cursor) ...
   func (t *discordTransport) Send(peer, text) ...
   func (t *discordTransport) Edit(peer, mid, text) ...
   func (t *discordTransport) Typing(peer) ...
   ```

2. **注册到 `imBuildTransport`**（`im.go`）
   ```go
   case "discord":
       return newDiscordTransport(acc)
   ```

3. **加平台元信息**（`im.go:imPlatforms()`）
   ```go
   {Kind:"discord", Label:"Discord", NeedsToken:true, CanEdit:true, ...}
   ```

4. **`normalizeIMPlatform`** 加同义词归一化（如果需要）

5. **可选：登录流程**（如果不是简单 token，比如 OAuth）
   仿照 `weChatStartLoginSession` + `/api/im/wechat/login` 路由扩展

**不需要改的地方**：
- `imHandleInbound`（统一入口）
- `imSendOutbound`（统一出口）
- `imReplyPushHook`（CanEdit=true 自动走流式 edit）
- `completeFromResponse` 中的 IM 直推
- `newReplyHooksForPane`（自动迭代所有 IM 账号）

## 9. 调试 / 排查

### 9.1 关键日志关键字

| 日志 tag | 来源 | 说明 |
|----------|------|------|
| `[im] account=N inbound → pane=...` | imHandleInbound | 入站送达 pane |
| `[im] reply push registered pane=...` | imRegisterReplyPushForInbound | 标记 IM 触发的 turn |
| `[im] reply hook attached account=N` | newReplyHooksForPane | hook 已绑定 |
| `[im] send OK/FAIL account=N kind=X purpose=Y` | imSendOutbound | 出站结果 |
| `[im] reply finalize account=N turn=...` | imReplyPushHook.finalize | turn 完成 |
| `[im-wechat] ...` | im_wechat.go | WeChat 协议层 |
| `[ai-gateway] complete agent=... reply_hooks=N` | completeFromResponse | hooks 数量 |
| `[reply-callback] fired pane=A -> B` | replyCallbackHook | 跨 agent 回调触发 |

### 9.2 常见问题

**问题：WeChat 用户发了消息但收不到回复**

1. 检查 `[im] reply push registered`：是否有这条日志？没有 → `bound_pane_id` 或 `inbound_to_agent` 没配
2. 检查 `[ai-gateway] complete ... reply_hooks=N`：N=0 → audit session 没拿到 push 标记（可能 turn 已经在用户发消息前启动了）
3. 检查 `[im] send FAIL`：错误信息是否是 `context_token` 失效？→ LLM 思考太慢超过 30s

**问题：Telegram 流式 edit 一直显示 "Thinking..."**

1. 检查 stream_kind 是否走了 thinking/answer 分支：grep `[im] reply onItems`
2. 检查 transport.Edit 是否报 429（Telegram rate limit）→ 增大 `imReplyUpdateMinInterval`

**问题：web 用户输入也被推送到了 IM**

1. 不应该出现 —— 检查 `imRegisterReplyPushForInbound` 是不是被错误调用
2. 检查 `imDrainReplyPushAccountsForPane`：drain 是否在每个 turn 开始正确执行（应该在 `newReplyHooksForPane` 里 peek，在 finalize 时 cancel）

### 9.3 测试入口

| 测试文件 | 覆盖内容 |
|----------|----------|
| `api/mgr/im_test.go` | imAccount 增删、normalize、send 路由 |
| `api/mgr/agent_inspector_thinking_test.go` | thinking/answer 解析 |
| `api/mgr/ai_gateway_audit_test.go` | audit session 生命周期 |

手工触发：
```bash
# 给某 pane 触发一次外发（模拟 reply）
curl -X POST http://localhost:PORT/api/im/send \
     -d '{"pane_id":"w-10003","text":"hello","platform":"wechat"}'
```

---

**总结**：整条链路的"心跳"是 `imWorker` 的长轮询 + `aiGatewayAuditSession` 的响应拦截，二者通过 `imPendingReplyPush` 这个全局映射建立"哪条 turn 由哪个 IM 触发"的关联。新平台只需提供 `botTransport` 实现，剩下的全部公共逻辑零改动。
