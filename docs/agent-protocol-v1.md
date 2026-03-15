# CiCy Agent Protocol v1

Master-Worker 通信协议。基于现有 API 基础设施（queue + chatbus + tmux + status watcher）。

---

## 通信通道

```
Master CLI ←→ API Server ←→ Worker CLI
              (HTTP/WS/tmux)

通道 1: Queue（Master → Worker 异步指令）
  Master → POST /api/workers/queue → Worker idle 时自动 dispatch 到 tmux

通道 2: tmux send-keys（Worker → Master CLI 通知）
  Worker idle → hook → tmux send-keys "pane_idle:w-20147" → Master CLI stdin

通道 3: ChatBus WS（Worker → Master UI 通知）
  Worker idle → hook → hub.broadcast worker_idle → ChatView 显示通知

通道 4: @worker 语法（Master UI → Worker）
  ChatView 输入 "@w-20147 做xxx" → POST /api/workers/queue
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

---

## 传输方式

### Master → Worker（派任务）

**方式 A: Queue API（推荐）**

```bash
POST /api/workers/queue
{
  "pane_id": "w-20147",
  "message": "实现登录页面",
  "type": "task",
  "priority": 0
}
```

Worker idle 时自动 dispatch 到 tmux stdin。

**方式 B: @worker 语法（ChatView UI）**

在 CommandPanel 输入 `@w-20147 实现登录页面`，前端拦截后调用 Queue API。

### Worker → Master（完成通知）

**自动触发**：Worker thinking→idle 时，hook 同时做两件事：

1. **tmux send-keys** → Master CLI 收到 `pane_idle:w-20147`（Master CLI 可据此验收/派新任务）
2. **ChatBus broadcast** → Master ChatView 显示黄色通知条 `🔔 w-20147 finished task (idle)`

```go
// main.go hook
tmuxCmd("send-keys", "-t", masterPane+":main.0", "-l", "pane_idle:"+shortPane)
tmuxCmd("send-keys", "-t", masterPane+":main.0", "Enter")
hub.broadcast(masterPane, ChatEvent{Type: "worker_idle", Data: ...})
```

### 自动 Dispatch

Worker idle 时还会自动 `dispatchQueue(paneID)` — 如果队列里有 pending 任务，直接发给 Worker。

---

## 完整数据流

```
Master CLI / ChatView
  │
  │── @w-20147 做xxx ──▶ POST /api/workers/queue (pending)
  │                                │
  │                     Worker idle 时 dispatch
  │                                │
  │                                ▼
  │                        Worker tmux stdin
  │                                │
  │                        Worker thinking...
  │                                │
  │                        Worker done → idle
  │                                │
  │                         Hook 触发
  │                        ┌───────┴───────┐
  │                        ▼               ▼
  │              tmux send-keys      ChatBus broadcast
  │              "pane_idle:w-20147"  worker_idle event
  │                        │               │
  │◀───────────────────────┘               │
  │  Master CLI 收到                        │
  │  (验收/派新任务)                         │
  │                                        ▼
  │                              ChatView 黄色通知
  │                              🔔 w-20147 finished
```

---

## 任务生命周期

```
pending → dispatched → thinking → idle (done)
                                → failed
                                → cancelled
```

| 状态 | 说明 | 触发 |
|------|------|------|
| pending | 在队列中等待 | Master push queue |
| dispatched | 已发送到 Worker tmux | Worker idle 触发 dispatch |
| thinking | Worker 正在执行 | Status watcher 检测 |
| idle (done) | 完成 | Hook 触发通知 |
| failed | 失败 | Worker 报告 error |
| cancelled | 已取消 | Master 发 cancel |

---

## 前端集成

### ChatView

- **接收 `worker_idle` WS 事件** → 显示黄色通知条
- **system 消息不被 reload 覆盖** → reload 时保留 `system: true` 的消息
- **`@worker` 语法** → CommandPanel 拦截，走 queue API

### TeamPanel

- 所有绑定 Worker 的 ttyd 终端平铺显示
- 状态点（thinking/idle/error）
- 绑定/解绑/新建 Worker

---

## API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/workers/queue` | POST | 推任务到 Worker 队列 |
| `/api/chat/push` | POST | 推事件到 ChatBus WS |
| `/api/chat/ws` | WS | ChatView 实时连接 |
| `/api/chat/debug` | GET | 查看 hub 当前 WS clients |
| `/api/agents/pane/{id}` | GET | 获取绑定的 agents |
| `/api/agents/bind` | POST | 绑定 Worker 到 Master |
| `/api/agents/unbind/{id}` | DELETE | 解绑 |
| `/api/tmux/status` | GET | 所有 agent 状态 |

---

## 实现状态

### Phase 1 — 基础通信 ✅
- [x] Queue API（push + dispatch）
- [x] ChatBus broadcast（worker_idle → ChatView）
- [x] tmux send-keys（worker_idle → Master CLI）
- [x] Status watcher + hook（thinking→idle 触发）
- [x] @worker 语法（CommandPanel 拦截）
- [x] ChatView 通知渲染（黄色通知条）
- [x] debug 端点（/api/chat/debug）

### Phase 2 — 任务管理
- [ ] 任务状态持久化（agent_queue 表扩展）
- [ ] Master CLI 验收流程
- [ ] 任务历史查看
- [ ] TeamPanel 显示当前任务

### Phase 3 — 智能调度
- [ ] 自动选择最空闲的 Worker
- [ ] 任务依赖（A 完成后才执行 B）
- [ ] 失败自动重试
