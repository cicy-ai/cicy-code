# Reply Gateway

> cicy-code AI gateway 的回放（reply）层 —— 把多个 LLM provider（Anthropic / OpenAI Chat Completions / OpenAI Responses）的流式响应实时归一成 cicy 统一的 `reply.json` 内容块（content blocks）。

## 1. 概述

每个 agent pane（Claude Code / OpenCode / Codex …）把 LLM 请求经 cicy-code AI gateway 透传给上游 provider。Gateway 在透传的同时做两件事：

1. **审计（audit）**：解析请求 / 响应，把对话状态写到本地 JSON 文件（`current.json` / `reply.json` / `reply_mirror/*.json`）。
2. **归一（normalize）**：不同 provider 的流式协议天差地别，最终归一成一个 schema 让前端 / IM / 跨 agent 工具消费。

Reply Gateway 就是审计层中**只关心 LLM 响应内容**的部分（"reply" = 当前 user turn 的 assistant 输出）。

文件位置：

| 文件 | 作用 |
|------|------|
| `api/mgr/ai_gateway_audit.go` | Gateway 主体：解析 SSE、累积 reply、写盘 |
| `api/mgr/ai_gateway_reply_mirror.go` | 调试镜像：每次 LLM 响应完整结束时写一份完整快照（默认关闭，`CICY_GATEWAY_REPLY_MIRROR=1` 启用） |
| `api/mgr/im_reply_hook.go` | 把 `reply.json.items` 增量推送到 WeChat / Telegram |
| `api/mgr/gateway_reply_text.go` | 把 `reply.json.items` 渲染成给 cicy_agent_get_last_reply 工具用的纯文本 |

工作目录布局（每个 agent pane 一份）：

```
~/cicy-ai/workers/<agent_id>/.cicy/history/
├── current.json           ← 当前 in-flight 请求快照（含 body.messages / input 完整历史）
├── reply.json             ← 当前 user turn 的 assistant 输出（实时增量）
└── reply_mirror/          ← 每次 HTTP 响应完成时的诊断快照（启用时才写）
    └── <turn>_<req>_<ts>.json
```

## 2. reply.json schema

```jsonc
{
  "turn_id": "...",                 // cicy 生成或继承自 codex turn header
  "status": "thinking|streaming|working|completed|failed",
  "started_at": "<RFC3339>",
  "updated_at": "<RFC3339>",
  "input_tokens": 123,
  "output_tokens": 456,
  "total_tokens": 579,
  "cache_creation_input_tokens": 0,
  "cache_read_input_tokens": 0,
  "cost_credit": 0,

  "items": [
    { "id": 1, "type": "thinking", "thinking": "..." },
    { "id": 2, "type": "text",     "text":     "..." },
    { "id": 3, "type": "tool_use", "tool_id": "call_xxx", "name": "Bash", "input": { ... } }
  ]
}
```

**字段约定**

| 字段 | 含义 | 备注 |
|------|------|------|
| `id` | cicy 自加的递增序号（1, 2, 3, ...） | 用于 IM 增量推送跟踪 / 前端 key |
| `type` | `thinking` / `text` / `tool_use` | 与 Anthropic content block schema 对齐 |
| `thinking` | thinking 内容 | 仅 `type=thinking` |
| `text` | assistant 文本回复 | 仅 `type=text` |
| `tool_id` | LLM 返回的原生 tool call ID（如 `call_00_xxx`） | 仅 `type=tool_use`；与 `current.json` 中下一轮请求的 `tool_calls[].id` / `tool_use_id` 一致，可用于关联 tool_result |
| `name` | 工具名（`Bash` / `Read` / `exec_command` / ...） | 仅 `type=tool_use` |
| `input` | 工具参数（已 unmarshal 的 object，解析失败时为原始 JSON 字符串） | 仅 `type=tool_use` |

**省略字段**：Anthropic 原生的 `signature`（thinking 续签 token）、`cache_control`（prompt cache 标记）等只在协议层有意义的字段一律不保留。

## 3. 数据流

```
                        ┌────────────────────────────────────────────────────┐
HTTP request   ──────►  │ Reverse-proxy handler (ai_gateway_*.go)            │
   (agent CLI)          │ wraps request + response in audit session          │
                        └────────────────────────────────────────────────────┘
                                       │
                                       ▼
                        ┌─────────────────────────────────────────┐
                        │ newAIGatewayAuditSession                │
                        │  - 提取 question / turn_id / model      │
                        │  - 决定是否 inherit prevReply.Items     │
                        │  - 决定是否标记 auxiliary (SUGGESTION)  │
                        └─────────────────────────────────────────┘
                                       │
                          ┌────────────┴────────────┐
                          ▼                         ▼
                  ┌──────────────────┐    ┌─────────────────────┐
                  │ writeStartSnaps  │    │ stream tap          │
                  │  current.json    │    │  (audit ReadCloser  │
                  │  reply.json (空) │    │   intercept SSE)    │
                  └──────────────────┘    └─────────────────────┘
                                                   │
                                                   ▼
                                ┌────────────────────────────────┐
                                │ aiGatewayParseSSELine          │
                                │  → aiGatewayReplyEvent[]       │
                                │   (sse / tool_call / web_search│
                                │    / status_change / ...)      │
                                └────────────────────────────────┘
                                                   │
                                                   ▼
                          ┌─────────────────────────────────────────┐
                          │ applyStreamEventsLocked                 │
                          │  - 累加 reply.Thinking / Answer /       │
                          │    ToolCalls (legacy 字段)              │
                          │  - 维护 pendingItem (per-block buffer)  │
                          │  - 切换或 stop 时 → flushPendingItem    │
                          │     → append reply.Items + 写 reply.json│
                          └─────────────────────────────────────────┘
                                                   │
                                                   ▼
                                ┌────────────────────────────────┐
                                │ HTTP response 完整结束          │
                                │ completeFromResponse           │
                                │  - flush 残留 pendingItem      │
                                │  - 兜底：若整次未 flush，从     │
                                │    parsed (acc) 一次性补 items  │
                                │  - 写 reply.json (final)       │
                                │  - 写 reply_mirror/*.json (debug)│
                                │  - 触发 reply hooks (IM 推送)   │
                                └────────────────────────────────┘
```

## 4. Per-item Flush（核心机制）

### 4.1 问题

LLM 流式响应里 thinking / text / tool_use 是按 token 增量到达的。如果等到整次 HTTP 完成才生成 `reply.Items`，前端 / IM 要等几秒甚至几十秒才能看到内容。

### 4.2 解决方案

audit session 维护一个 `pendingItem`（`aiGatewayPendingItem`）：当前正在流式累积的 item buffer。

- 同一 kind 的 chunk 累加到 pending；
- kind / tool 切换时 **flush** 当前 pending 到 `reply.Items` 并立刻刷盘；
- HTTP 完成时 flush 剩余 pending。

```go
type aiGatewayPendingItem struct {
    Kind         string // "thinking" | "text" | "tool_use"
    Thinking     string // for thinking
    Text         string // for text
    ToolID       string // stream 累积 key（Codex=fc_xxx；其他=call_xxx）
    OutputToolID string // reply.items 输出用的 tool_id（统一=call_xxx）
    ToolName     string
    Arguments    string // tool input 累积的原始 JSON
}
```

### 4.3 Block 边界识别

`switchPendingItemLocked(kind, toolID, toolName)` 判断要不要 flush：

```
新 kind == pending.Kind           → 同一个 block，继续累积
新 kind != pending.Kind           → flush 旧 + 开新 pending

特例（tool_use）：
  toolID == ""                     → 同一个 tool 的 delta 续传（许多 provider 续传 chunk 不带 toolID）
  toolID == pending.ToolID         → 同一个 tool
  toolID != "" && != pending.Tid   → 不同 tool，flush 旧 + 开新 pending
```

### 4.4 flush 规则

```go
func flushPendingItemLocked():
  根据 pi.Kind 构造 item：
    thinking → {id, type:"thinking", thinking}
    text     → {id, type:"text", text}
    tool_use → {id, type:"tool_use", tool_id, name, input}
                tool_id 优先用 OutputToolID，否则 ToolID
                input 是 unmarshal 后的 object（失败时保留原始字符串）
  append 到 reply.Items
  if !auxiliary: 立即写盘 reply.json
```

## 5. Provider 协议差异

三种协议在流式事件层面差异巨大，但都被归一成相同的 reply event：

| Provider | 流协议 | thinking 字段 | tool_id 表示 | item 边界事件 |
|----------|--------|----------------|--------------|---------------|
| **Anthropic** | `content_block_start` / `_delta` / `_stop` | `delta.thinking_delta.thinking` | `content_block.id = call_xxx` | `content_block_stop` |
| **OpenAI Chat Completions** | `choices[0].delta.{content, tool_calls, reasoning_content}` | `delta.reasoning_content`（DeepSeek 扩展） | `tool_calls[].id = call_xxx` | 仅 `finish_reason` 在尾部 |
| **OpenAI Responses (Codex)** | `response.output_item.added/done`、`response.function_call_arguments.delta/done`、`response.reasoning_summary_text.delta` | reasoning_summary_text | `function_call.id = fc_xxx` + `call_id = call_xxx` | `response.output_item.done` |

### 5.1 关键归一规则

- **content / arguments / reasoning_content** 必须用 `aiGatewayRawString` 读，不能 trim：SSE chunk 间隔的前导 / 尾随空格是文本的一部分（"The"+" user" → "The user"）。`aiGatewayString` 会 `TrimSpace`，会把空格吃掉。
- **Anthropic `content_block_delta`** 后续 chunk **不带 ToolID**，只带 blockIndex。emit 端必须传 `ToolIndex`，applyStream 处理时空 ToolID 视为延续当前 pending tool。
- **Codex Responses** 同一个工具调用通过 `output_item.added` (id=fc_xxx, call_id=call_xxx, name) 和 `function_call_arguments.delta` (item_id=fc_xxx) 两种事件分开发送。归一规则：
  - emit `output_item.{added,done}` 让 stream live 拿到真实 toolName；
  - emit 的 `tool_id` 一律用 `item.id` (fc_xxx)，与 delta 对齐 → pending 不分裂；
  - emit 携带 `output_tool_id` = `item.call_id` (call_xxx) → flush 写到 reply.items.tool_id，与 `current.json` 中 `tool_calls[].id` 对齐。

## 6. Items 继承 vs 重置

新 audit session 创建时（`newAIGatewayAuditSession`）需要决定：当前请求的 `reply.Items` 应该接续上一份 reply（多轮 tool 续传），还是清空重开（用户新 q）？

判断依据：**当前请求 body 最后一条记录的语义**（不是时间窗口）。

```go
aiGatewayIsToolContinuation(body) bool:
  最后一条 messages[] role=tool / function          → 续传 (OpenAI Chat)
  最后一条 messages[] role=user, content[] 含
    type=tool_result / function_call_output         → 续传 (Anthropic)
  最后一条 input[] type=function_call_output /
    tool_result / shell_call_output / ...           → 续传 (Codex Responses)
  其他                                              → 用户新 q
```

```go
if prevReply.TurnID != "" && aiGatewayIsToolContinuation(body):
    prevItems = prevReply.Items   // 继承
    turnID    = prevReply.TurnID  // 同 turn
else:
    prevItems = []                // 清空重开
    turnID    = newID()           // 新 turn
```

## 7. Auxiliary（SUGGESTION MODE）

某些 agent CLI 会发**辅助调用**预测下一步用户输入（典型例子：Claude Code 的 SUGGESTION MODE，question 以 `[SUGGESTION MODE:` 开头）。这种调用：

- 不应该污染主 `reply.json` / `current.json`（用户看到的应该是真实 q 的回复）；
- 但仍写 `reply_mirror/*.json` 以便诊断。

机制：

```go
session.auxiliary = aiGatewayIsAuxiliaryRequest(question, body):
  question 以 "[SUGGESTION MODE" 开头             → auxiliary
  body.metadata.{purpose,kind,category} 含 "suggestion" → auxiliary
  其他                                            → 主请求
```

`if s.auxiliary` 时跳过：
- `writeStartSnapshots`（不清空 reply 目录、不写初始 current/reply）
- `applyStreamEventsLocked` 中的 reply 刷盘（line ~2545）
- `completeFromResponse` 中的 reply 终态写盘（line ~700）

`reply_mirror` 写盘照常。

## 8. Mirror（诊断快照）

`api/mgr/ai_gateway_reply_mirror.go` 在每次 HTTP 完整结束时写一份**只读**快照，包含：

```jsonc
{
  "mirrored_at": "...",
  "agent_id": "...",
  "turn_id": "...",
  "request_id": "...",
  "provider": "anthropic|openai",
  "url": "...",
  "method": "POST",
  "status_code": 200,
  "latency_ms": 1234,
  "question": "...",
  "request_headers": { ... },
  "request_body": { ... },          // 完整请求 body（messages / input 等）
  "response_headers": { ... },
  "response_body": "<raw SSE 文本>",
  "parsed_thinking": "...",
  "parsed_answer": "...",
  "parsed_tool_calls": [...],
  "parsed_usage": { ... },
  "reply_snapshot": { ... }         // 那一刻 reply.json 的完整内容
}
```

用途：
- 对比 stream 解析结果 (`parsed_*`) 与最终 `reply.items` 是否一致；
- 检查 SUGGESTION 是否被正确识别成 auxiliary；
- 排查空格 / 字段名 / tool 分裂 bug 时直接看 raw SSE chunk。

启用：`CICY_GATEWAY_REPLY_MIRROR=1`（dev.py 默认开启）。

存储路径：`<workspace>/.cicy/history/reply_mirror/<turn>_<req>_<ts>.json`。

## 9. 调用链（自顶向下）

```
ReverseProxyHandler (ai_gateway_*.go)
  └─ newAIGatewayAuditSession ────────── aiGatewayExtractQuestion
                                        aiGatewayIsToolContinuation
                                        aiGatewayIsAuxiliaryRequest
                                        aiGatewayLoadReplySnapshot (load prev)
  └─ session.writeStartSnapshots ─────── (skip if auxiliary)
  └─ wrap response with aiGatewayAuditReadCloser
       └─ on each SSE line:
            aiGatewayParseSSELine
              └─ aiGatewayReplyEventsFromStreamPayload
                   ├─ web_search emit
                   ├─ output_item.{added,done} emit  (Codex 真实 toolName)
                   ├─ function_call_arguments.done emit  (cumulative args)
                   └─ aiGatewayExtractStreamDeltas
                        ├─ Anthropic content_block_start/delta
                        ├─ OpenAI Chat tool_calls / content / reasoning_content
                        └─ Codex response.* events
            session.applyStreamEventsLocked
              ├─ accumulate Thinking / Answer / ToolCalls (legacy)
              ├─ switchPendingItemLocked (flush + new pending)
              └─ flushPendingItemLocked → append reply.Items → write reply.json
       └─ on response complete:
            session.completeFromResponse
              ├─ flushPendingItemLocked (last residual)
              ├─ fallback: append from parsed (if stream emit nothing)
              ├─ write reply.json (final, skip if auxiliary)
              ├─ aiGatewayWriteReplyMirror
              └─ run reply hooks (IM push, callbacks)
```

## 10. 常见问题与排查

### 10.1 "reply.json 里字都挤在一起了，没空格"
SSE chunk 内容字段（content / arguments / reasoning_content）必须用 `aiGatewayRawString` 读取。`aiGatewayString` 会 `strings.TrimSpace`，把每个 chunk 前导空格吃了。
查：`api/mgr/ai_gateway_audit.go` 中所有用 `aiGatewayString` 读 stream chunk content 字段的地方。

### 10.2 "tool_use 被切成几十条小块"
说明 `switchPendingItemLocked` 把 delta chunk 当成不同 tool 了。检查：
- 空 ToolID 是否被视为延续（不是切换）？
- 同一 tool 的 added / delta / done 三种事件 emit 的 `tool_id` 是否对齐？

### 10.3 "reply.json items 是空的，turn_id 是 SUGGESTION 的"
SUGGESTION 写穿了主 reply。说明 `aiGatewayIsAuxiliaryRequest` 没识别到，或某个 `aiGatewayWriteReplySnapshot` 调用没加 `if !s.auxiliary` 保护。

### 10.4 "新 q 没清空旧 items"
`aiGatewayIsToolContinuation` 误判。检查请求 body 末尾是不是真的有 `tool_result` / `function_call_output`；如果是 user 文本但 cicy-cli 把它包成了 `tool_result` 形式，需要扩展判断规则。

### 10.5 "items 增长很慢，要等几秒才看到"
确认 `applyStreamEventsLocked` 真的在 stream 阶段调 `flushPendingItemLocked`（不是只在 HTTP 完成时）。空格 / item 分裂等 bug 可能让 `pendingItem` 不切换从而不刷盘。

## 11. 测试样本

- 单元测试：`api/mgr/ai_gateway_audit_test.go`
- 三 provider 真实流量样本：`/tmp/cicy_reply_mirror_test.sh`（使用 cicy-agent / curl 给三个 agent 发短/中/长消息），跑完后检查每个 agent 的 `reply_mirror/*.json`。
- 手工实时观察增长：

  ```sh
  cicy-agent msg w-10018 "运行 echo cicy-test"
  for i in $(seq 1 30); do
    jq -c '{n: (.items|length), last: (.items|last|.type), updated: .updated_at}' \
      ~/cicy-ai/workers/w-10018/.cicy/history/reply.json
    sleep 0.5
  done
  ```
