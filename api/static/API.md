# AI Team Server API 文档

Base URL: `http://tn.cicy-ai.com:19010`

---

## HTTP 接口

### POST /api/send
向指定 tmux pane 发送消息（即向 kiro-cli 输入文字）。

**Request**
```json
{
  "pane_id": "w-20160:main.0",
  "text": "用户消息内容"
}
```

**Response**
```json
{ "success": true }
```

---

### POST /api/capture
获取指定 tmux pane 的当前屏幕内容。

**Request**
```json
{
  "pane_id": "w-20160:main.0",
  "lines": 30
}
```

**Response**
```json
{ "output": "pane 内容文本..." }
```

---

### POST /api/proxy
在指定 pane 里启动 kiro-cli（带 mitmproxy 代理）。
流程：C-c → 安装 CA 证书 → export proxy env → kiro-cli chat -a

**Request**
```json
{
  "pane_id": "w-20160:main.0",
  "url": "http://w-20160:x@127.0.0.1:18888"
}
```

**Response**
```json
{ "success": true }
```

---

### POST /api/reload
重启 mitmproxy 容器（热重载 addon）。

**Request**: 无 body

**Response**
```json
{ "success": true }
```

---

### POST /api/webhook
内部接口，由 mitmproxy addon 调用，将事件广播给所有 WebSocket 客户端。前端无需直接调用。

**Request**: JSON 字符串（见 WebSocket 事件格式）

**Response**
```json
{ "success": true }
```

---

## WebSocket

### GET /api/ws
建立 WebSocket 连接，实时接收 AI 事件推送。

```js
const ws = new WebSocket('ws://tn.cicy-ai.com:19010/api/ws');
ws.onmessage = (e) => {
  const { pane, event, data } = JSON.parse(e.data);
};
```

断线后建议自动重连（2s 延迟）。

---

## WebSocket 事件

所有事件格式：
```json
{
  "pane": "w-20160",
  "event": "<事件名>",
  "data": { ... }
}
```

`pane` 为 worker 名，用于区分多个 agent。

---

### event: `user_q`
用户向 kiro-cli 发送了消息时触发。

```json
{
  "pane": "w-20160",
  "event": "user_q",
  "data": {
    "q": "用户消息内容",
    "model": "deepseek-3.2"
  }
}
```

| 字段 | 说明 |
|------|------|
| `q` | 用户消息文本 |
| `model` | 本次使用的模型 ID（如 `auto` `deepseek-3.2` `claude-sonnet-4.5`） |

---

### event: `ai_chunk`
AI 流式回复中，每隔 ~150ms 推送一次。

```json
{
  "pane": "w-20160",
  "event": "ai_chunk",
  "data": {
    "delta": "累积到目前为止的完整回复文本"
  }
}
```

> ⚠️ `delta` 是**累积全文**，不是增量。前端应直接替换气泡内容，不要追加。

---

### event: `ai_done`
AI 回复完成时触发。

```json
{
  "pane": "w-20160",
  "event": "ai_done",
  "data": {
    "text_length": 1290,
    "tool_count": 0,
    "credits": 0.199,
    "context_pct": 11.41,
    "conversation_id": "33b31543-ce87-405d-88de-51dd4efdb464"
  }
}
```

| 字段 | 说明 |
|------|------|
| `text_length` | 回复字符数 |
| `tool_count` | 本轮调用工具次数 |
| `credits` | 本轮消耗 credits |
| `context_pct` | 当前 context 使用百分比 |
| `conversation_id` | 会话 ID |

收到 `ai_done` 后应解除 loading 状态，允许用户继续输入。

---

## Agent / Pane 映射

| Agent | pane_id |
|-------|---------|
| agent-0（艾丽丝，CEO顾问，免费） | `w-20160:main.0` |
| agent-1~4 | 付费解锁后分配 |

`pane` 字段（WS 事件里）= `pane_id` 的 worker 部分，如 `w-20160`。

---

## 典型前端流程

```
用户输入 → POST /api/send
         ↓
         等待 WS 事件：
         user_q  → 显示用户消息 + model badge
         ai_chunk → 流式更新 AI 气泡（替换不追加）
         ai_done  → 定稿，显示 credits，解除 loading
```
