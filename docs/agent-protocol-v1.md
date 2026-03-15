# CiCy Agent Protocol v1

Master-Worker 通信协议。基于现有 API 基础设施（queue + chatbus + status watcher）。

---

## 通信通道

```
Master ←→ API Server ←→ Worker
         (HTTP/WS)

通道 1: Queue（异步指令）
  Master → POST /api/workers/queue → Worker idle 时自动 dispatch

通道 2: ChatBus（实时事件）
  POST /api/chat/push → WS 广播 → 前端/Agent 接收

通道 3: Status Watcher（状态变化）
  Worker thinking→idle → 触发 hook → 通知 Master
```

---

## 消息格式

所有消息统一 JSON 格式：

```json
{
  "protocol": "cicy/v1",
  "from": "w-10001",
  "to": "w-20147",
  "type": "<message_type>",
  "id": "<uuid>",
  "ts": 1773570000,
  "data": { ... }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| protocol | string | 固定 `cicy/v1` |
| from | string | 发送方 pane_id |
| to | string | 接收方 pane_id |
| type | string | 消息类型 |
| id | string | 消息唯一 ID（用于 ack/reply） |
| ts | number | Unix timestamp |
| data | object | 消息体 |

---

## 消息类型

### 1. task — Master 派任务

```json
{
  "type": "task",
  "data": {
    "task_id": "t-001",
    "title": "实现登录页面",
    "prompt": "请在 src/pages/Login.tsx 实现登录页面，要求...",
    "priority": 0,
    "context": ["参考 .cicy/arch.md 的设计"]
  }
}
```

### 2. task_result — Worker 报告结果

```json
{
  "type": "task_result",
  "data": {
    "task_id": "t-001",
    "status": "done",
    "summary": "已完成登录页面，包含表单验证和错误提示",
    "files_changed": ["src/pages/Login.tsx", "src/styles/login.css"],
    "error": null
  }
}
```

status: `done` | `failed` | `blocked`

### 3. status — 状态广播

```json
{
  "type": "status",
  "data": {
    "state": "thinking",
    "task_id": "t-001",
    "progress": "正在编写组件..."
  }
}
```

state: `idle` | `thinking` | `running` | `blocked` | `error`

### 4. ping / pong — 心跳

```json
{ "type": "ping", "data": {} }
{ "type": "pong", "data": {} }
```

### 5. cancel — 取消任务

```json
{
  "type": "cancel",
  "data": {
    "task_id": "t-001",
    "reason": "需求变更"
  }
}
```

### 6. query — Master 查询 Worker 状态

```json
{
  "type": "query",
  "data": {
    "what": "current_task"
  }
}
```

### 7. reply — 通用回复

```json
{
  "type": "reply",
  "data": {
    "reply_to": "<original_msg_id>",
    "result": { ... }
  }
}
```

---

## 传输方式

### Master → Worker（派任务）

通过 Queue API，Worker idle 时自动 dispatch 到 tmux：

```bash
POST /api/workers/queue
{
  "pane_id": "w-20147",
  "message": "<json_msg>",
  "type": "task",
  "priority": 0
}
```

Queue dispatch 时，消息通过 `tm msg` 发送到 Worker 的 kiro-cli stdin。

### Worker → Master（报告结果）

通过 ChatBus push，Master 前端 WS 接收：

```bash
POST /api/chat/push
{
  "pane": "w-10001",
  "type": "agent_event",
  "data": {
    "protocol": "cicy/v1",
    "from": "w-20147",
    "type": "task_result",
    ...
  }
}
```

### 状态变化通知（自动）

已有 watcher hook：Worker thinking→idle 时自动通知 Master。

```go
// stats.go — 已有
RegisterHook(func(paneID string, old, new paneSt) {
    // Worker idle → dispatch queued messages
    go dispatchQueue(paneID)
})
```

---

## 任务生命周期

```
Master                    API                     Worker
  │                        │                        │
  │── task ──────────────▶│                        │
  │                        │── queue (pending) ──▶ │
  │                        │   (wait for idle)      │
  │                        │── dispatch ──────────▶│
  │                        │                        │── thinking
  │                        │                        │── running
  │◀── status(thinking) ──│◀── watcher ───────────│
  │                        │                        │
  │                        │                        │── done
  │◀── task_result ───────│◀── chat/push ─────────│
  │                        │                        │
  │── next task ─────────▶│                        │
  │                        │                        │
```

---

## 任务状态机

```
pending → queued → dispatched → running → done
                                       → failed
                                       → cancelled
```

| 状态 | 说明 | 触发 |
|------|------|------|
| pending | 在队列中等待 | Master push |
| queued | 已入队 | API 确认 |
| dispatched | 已发送到 Worker | Worker idle 触发 |
| running | Worker 正在执行 | Worker status=thinking |
| done | 完成 | Worker 报告 task_result |
| failed | 失败 | Worker 报告 error |
| cancelled | 已取消 | Master 发 cancel |

---

## 前端集成

### TeamPanel 显示

每个 Worker 卡片显示：
- 当前任务标题 + 进度
- 状态点（thinking/idle/error）
- ttyd 终端实时输出

### Chat 集成

Master Chat 里显示：
- Worker 完成通知（task_result）
- Worker 状态变化
- 可以直接在 Chat 里给 Worker 下任务

---

## 实现优先级

### Phase 1 — 基础通信（用现有设施）
- [x] Queue API（已有）
- [x] ChatBus push（已有）
- [x] Status watcher + hook（已有）
- [ ] 统一消息格式（JSON wrapper）
- [ ] TeamPanel 显示当前任务

### Phase 2 — 任务管理
- [ ] 任务状态持久化（agent_queue 表扩展）
- [ ] 任务历史查看
- [ ] 批量任务派发

### Phase 3 — 智能调度
- [ ] 自动选择最空闲的 Worker
- [ ] 任务依赖（A 完成后才执行 B）
- [ ] 失败自动重试
