# Chat V2 — 实时对话系统设计

## 问题

当前 Chat 基于 mitmproxy 抓包 → MySQL → 轮询，存在严重延迟：
1. 用户发送 prompt 后看不到自己的消息（要等 HTTP 响应写入 DB 后才出现）
2. AI 回复要等完整响应存入 DB 后才推送（无法 streaming）
3. SSE 每 2s 轮询一次，最差延迟 2s
4. 工具调用过程中用户完全无感知

## 设计目标

- 用户发送 prompt → **立即**显示在聊天界面
- AI 回复 → **实时 streaming** 推送到前端
- 工具调用 → **实时**显示工具名称、参数、状态
- 历史记录 → 启动时一次性加载
- Token/Credit → 每轮结束时更新
- Agent 状态 → idle/thinking 实时切换

## 架构

```
                    ┌─────────────┐
                    │   Frontend  │
                    │  ChatView   │
                    └──────┬──────┘
                           │ SSE (persistent)
                           ▼
                    ┌─────────────┐
                    │   Go API    │
                    │  /api/chat  │
                    │   EventBus  │
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │ Frontend  │ │ mitmproxy│ │  MySQL   │
        │ POST /send│ │ webhook  │ │ history  │
        └──────────┘ └──────────┘ └──────────┘
```

## 事件流

### 1. 用户发送 Prompt

```
Frontend                    Go API                     mitmproxy
   │                          │                            │
   │── POST /api/chat/send ──▶│                            │
   │   {pane, text}           │── tmux send-keys ─────────▶│ (kiro-cli)
   │                          │                            │
   │◀── SSE: user_message ───│                            │
   │   {id, role:"user",     │                            │
   │    text, ts}             │                            │
   │                          │                            │
   │◀── SSE: status_change ──│                            │
   │   {status:"thinking"}    │                            │
```

前端收到 `user_message` 事件后**立即渲染**用户气泡，无需等待 AI。

### 2. AI Streaming 回复

```
mitmproxy                   Go API                     Frontend
   │                          │                            │
   │── webhook: chunk ───────▶│                            │
   │   {pane, type:"text",   │── SSE: ai_chunk ──────────▶│
   │    delta:"Hello"}        │   {text:"Hello"}           │ ← 实时追加
   │                          │                            │
   │── webhook: chunk ───────▶│                            │
   │   {type:"text",         │── SSE: ai_chunk ──────────▶│
   │    delta:" world"}       │   {text:" world"}          │ ← 实时追加
   │                          │                            │
   │── webhook: done ────────▶│                            │
   │   {usage: 0.05}         │── SSE: ai_done ───────────▶│
   │                          │   {credit:0.05}            │ ← 显示费用
   │                          │                            │
   │                          │── SSE: status_change ─────▶│
   │                          │   {status:"idle"}          │
```

### 3. 工具调用

```
mitmproxy                   Go API                     Frontend
   │                          │                            │
   │── webhook: tool_use ────▶│                            │
   │   {name:"fs_read",      │── SSE: tool_start ────────▶│
   │    args:{path:"/x"}}    │   {name,args}              │ ← 显示工具卡片
   │                          │                            │
   │── webhook: tool_result ─▶│                            │
   │   {output:"..."}        │── SSE: tool_done ─────────▶│
   │                          │   {name,duration_ms}       │ ← 标记完成
   │                          │                            │
   │── webhook: chunk ───────▶│── SSE: ai_chunk ──────────▶│ (下一轮回复)
```

## SSE 事件类型

| Event | 触发时机 | Payload |
|-------|---------|---------|
| `history` | 连接建立 | `{turns: [...]}` 完整历史 |
| `user_message` | 用户发送 | `{id, text, ts}` |
| `status_change` | 状态切换 | `{status: "idle"\|"thinking"}` |
| `ai_chunk` | AI streaming | `{delta: "text chunk"}` |
| `ai_done` | AI 回复结束 | `{credit, duration_ms}` |
| `tool_start` | 工具开始 | `{name, args}` |
| `tool_done` | 工具完成 | `{name, duration_ms}` |
| `error` | 错误 | `{message}` |

## API 端点

### POST `/api/chat/send`

发送消息到 agent（通过 tmux send-keys）。

```json
// Request
{ "pane": "w-10001", "text": "read ~/skills" }

// Response
{ "ok": true, "id": "msg_1710200000" }
```

立即广播 `user_message` SSE 事件给所有订阅该 pane 的客户端。

### GET `/api/chat/stream?pane={id}`

SSE 持久连接。连接后立即推送 `history` 事件（从 DB 加载），之后实时推送所有事件。

### GET `/api/chat/history?pane={id}`

一次性获取历史（备用，正常用 SSE 的 `history` 事件）。

## mitmproxy Webhook

mitmproxy 实时解析 streaming response，通过 HTTP webhook 推送到 Go API：

```python
# mitmproxy addon (简化)
class ChatRelay:
    def responseheaders(self, flow):
        if "GenerateAssistantResponse" in flow.request.text:
            flow.metadata["chat_pane"] = extract_pane(flow)

    def response_chunk(self, flow, chunk):
        # 解析 event-stream chunk
        if "assistantResponseEvent" in chunk:
            post_webhook("ai_chunk", {delta: extract_text(chunk)})
        elif "toolUseEvent" in chunk:
            post_webhook("tool_start", {name, args})

    def response_done(self, flow):
        post_webhook("ai_done", {credit: extract_usage(flow)})
```

关键改动：mitmproxy 从「存完 DB 再查」变为「边收边推 webhook」。

## Go API EventBus

```go
// 内存中的 per-pane 事件总线
type EventBus struct {
    mu       sync.RWMutex
    clients  map[string][]chan Event  // pane_id -> SSE clients
}

func (eb *EventBus) Publish(pane string, event Event) {
    eb.mu.RLock()
    defer eb.mu.RUnlock()
    for _, ch := range eb.clients[pane] {
        select {
        case ch <- event:
        default: // drop if client too slow
        }
    }
}
```

## 前端状态机

```
                    ┌──────┐
         ┌─────────│ idle │◀────────────┐
         │         └──┬───┘             │
    user sends        │            ai_done
         │            ▼                 │
         │     ┌────────────┐     ┌─────┴─────┐
         └────▶│  thinking  │────▶│ streaming  │
               └────────────┘     └─────┬─────┘
                     ▲                  │
                     │    tool_start    │
                     │         │        │
                     │    ┌────▼────┐   │
                     └────│tool_use │───┘
                          └─────────┘
```

### 前端数据结构

```typescript
interface ChatTurn {
  id: string;
  role: 'user' | 'assistant';
  text: string;           // 用户消息 or AI 累积文本
  tools: ToolAction[];    // 本轮工具调用
  credit: number;
  status: 'done' | 'streaming' | 'tool_use' | 'pending';
  ts: number;
}

interface ToolAction {
  name: string;
  args: Record<string, any>;
  status: 'running' | 'done';
  duration_ms?: number;
}

// 状态管理
const [turns, setTurns] = useState<ChatTurn[]>([]);
const [agentStatus, setAgentStatus] = useState<'idle'|'thinking'>('idle');

// SSE 事件处理
eventSource.onmessage = (e) => {
  const {type, ...data} = JSON.parse(e.data);
  switch(type) {
    case 'history':
      setTurns(data.turns);
      break;
    case 'user_message':
      setTurns(prev => [...prev, {id: data.id, role:'user', text: data.text, status:'done', ts: data.ts}]);
      break;
    case 'status_change':
      setAgentStatus(data.status);
      if (data.status === 'thinking')
        setTurns(prev => [...prev, {role:'assistant', text:'', status:'streaming', tools:[]}]);
      break;
    case 'ai_chunk':
      setTurns(prev => {
        const last = {...prev[prev.length-1]};
        last.text += data.delta;
        return [...prev.slice(0,-1), last];
      });
      break;
    case 'tool_start':
      setTurns(prev => {
        const last = {...prev[prev.length-1]};
        last.tools = [...last.tools, {name: data.name, args: data.args, status:'running'}];
        last.status = 'tool_use';
        return [...prev.slice(0,-1), last];
      });
      break;
    case 'tool_done':
      // mark tool as done
      break;
    case 'ai_done':
      setTurns(prev => {
        const last = {...prev[prev.length-1]};
        last.status = 'done';
        last.credit = data.credit;
        return [...prev.slice(0,-1), last];
      });
      setAgentStatus('idle');
      break;
  }
};
```

## 对比

| | V1 (当前) | V2 (新设计) |
|---|---|---|
| 用户消息显示 | 等 DB 写入 + 2s 轮询 | **立即** (POST 后 SSE 推送) |
| AI 回复 | 等完整响应存 DB | **实时 streaming** |
| 工具调用 | 下一轮请求才能看到 | **实时显示** |
| 状态切换 | 轮询 | **实时推送** |
| 延迟 | 2-4s | **<100ms** |
| 数据流 | mitmproxy→DB→轮询 | mitmproxy→webhook→EventBus→SSE |

## 实现优先级

1. **Go API EventBus** + SSE endpoint — 基础设施
2. **POST /api/chat/send** — 用户消息立即推送
3. **mitmproxy webhook** — streaming chunk 实时转发
4. **前端 ChatView V2** — 基于 SSE 事件的状态机渲染
5. **历史加载** — 连接时从 DB 加载，之后纯事件驱动
