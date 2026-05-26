# cicy-mitm 透明审计 + 跨 agent callback 设计

> 状态:草案 v0.1(2026-05-26）
> 关联代码:`api/mgr/mitm/`、`api/mgr/mitm_audit_adapter.go`、`api/mgr/gateway_reply_callback.go`、`api/mgr/ai_gateway_audit.go`
> 关联文档:`docs/v1/audit-v2-design.md`、`api/mgr/mitm/README.md`

---

## 1. 问题

跨 agent callback(A `cicy-agent msg B --callback` → B 干完回 A 一句 `[B] ✅ work done`)**目前只在网关路径生效**:

```
registerReplyCallback(B, A)                 gateway_reply_callback.go:31
 → (B 起新轮) ai_gateway_audit.go:418
    → newReplyHooksForPane(B)               im_reply_hook.go:63
       → drainCallbackHooksForPane(B)       挂 replyCallbackHook
 → (B reply finalize, completed 且无 tool_calls)
    → replyCallbackHook.finalize() → sendTextToPane(A, "[B] ✅ work done")
```

不读 `ANTHROPIC_BASE_URL` 的客户端(官方登录的 claude/Opus 主程、Cursor、桌面/移动 App)**绕过网关**,由 MITM 采集。但 `mitm_audit_adapter.go` 是一条**独立管道**(文件头注释:"Decoupled from ai_gateway_audit.go to avoid cooperative-side drift"),它写 current.json/reply.json,但**从不调** `drainCallbackHooksForPane` / `replyCallbackHook.finalize` → 这些 agent **没法 callback**。

---

## 2. 决策:两条路,职责不重叠,MITM 全程不改请求

| 路径 | 适用客户端 | 换模型/路由/注入 | 审计 + current/reply.json | callback |
|------|-----------|:---:|:---:|:---:|
| **网关** | 协作 CLI(读 `ANTHROPIC_BASE_URL`:claude/codex/opencode/kiro) | ✅ 实时换模型、provider 路由、key/规则注入 | ✅(现状) | ✅(现状) |
| **MITM** | 非协作(官方 Opus、Cursor、桌面/移动、硬编码 SDK) | ❌ **不碰请求**,原样透传真上游 | ✅(现状) | ❌ **本设计要补的唯一一项** |

核心约束:**MITM 是纯透明观察者,不修改请求**。它只需要产出"审计 + current.json + reply.json + callback"。

---

## 3. 被否决的替代方案(留决策依据)

- **A「MITM 吃掉审计,网关退化成转发」** —— 否。MITM 换不了模型:实时换模型 = 改请求体(model 字段)+ 换 provider host + 换 auth key(+ 跨厂 schema 翻译),等于在 MITM 里把网关重写一遍。
- **B「MITM 把请求喂进网关」** —— 否。这会**修改非协作客户端的请求**(被网关换模型),而我们对这类客户端明确"不碰请求,没必要"。
- **结论**:保留两条独立路径。网关保持全权;MITM 只补 callback,绝不动请求。

---

## 4. MITM callback 实现(只改 `mitm_audit_adapter.go`)

current.json / reply.json / 审计已有。点燃 callback 的唯一实质工作是**解析响应**(透传的同时 tee 一份),因为触发条件是"这一轮真结束、且没有 tool_call 在飞"——需要看懂响应的 `stop_reason` / `tool_use`。顺手把 reply.json 从"整段 raw body"升级成结构化。

1. **`StartTurn`(:36)**:`hooks = drainCallbackHooksForPane(agentID)`,存到 `mitmAuditTurn`。
2. **响应解析**:tee 响应流,增量解析成 `aiGatewayReplySnapshot`。**复用网关现成的 SSE→snapshot 解析**——把 `ai_gateway_audit.go` 里那段抽成共享函数(如 `parseUpstreamReply(body) → snapshot`),MITM 和网关共用 → reply.json 字段必然一致,也不必写第二个 SSE 解析器。
3. **`finish()`(:113)/`Fail()`(:91)**:对每个 hook 调 `finalize(snapshot)`,复用 `replyCallbackHook`(连"`completed` 但 `len(ToolCalls)>0` 就 re-queue 延后到下一个请求"的语义一起;MITM 每个上游请求是一次 `StartTurn`,re-queue + 下次 drain 的模型天然吻合)。
4. **reply.json 路径修正**:写**规范路径** `~/cicy-ai/workers/<agent>/.cicy/history/reply.json`(`cicy_agent_get_last_reply` 读的那个,`ai_gateway_audit.go:797`),而非现在的 `history/mitm/<turn>/reply.json`——否则 callback 来了 A 取不到 B 的回复正文。

---

## 5. 前置依赖(均与"改请求"无关,符合不碰请求的约束)

1. **身份解析**:MITM 必须把"这条 SOCKS5 连接"映射到正确的 agent pane ID(`mitm/identity.go`)→ 才能写对 agent 的 reply、把 callback 发给对的 pane。**待定:** 怎么带身份(每 agent 独立 SOCKS5 端口 / 凭证 / 注入 trace header)。
2. **流量真经过 MITM**:要审计的 agent(如官方 Opus 主程)进程要真的走 `ALL_PROXY/HTTPS_PROXY=socks5://127.0.0.1:1085`(或经 mihomo 兜住)+ 信任 cicy CA。claude 的 boot.sh 现在是直连,需补。

---

## 6. 代码触点

- `api/mgr/mitm_audit_adapter.go`:`StartTurn` / `finish` / `Fail` 三处接 hook + 接入响应解析。
- `api/mgr/ai_gateway_audit.go`:抽出 SSE→`aiGatewayReplySnapshot` 共享解析函数(两路复用)。
- `api/mgr/gateway_reply_callback.go`:`registerReplyCallback` / `drainCallbackHooksForPane` / `replyCallbackHook` 原样复用,不改。
- reply.json 写盘路径统一到规范 `history/reply.json`。

---

## 7. 顺带:网关 URL 简化(太丑太啰嗦)

**现状**:`http://127.0.0.1:8008/api/ai-gateway/<provider>/<agentID>`
例:`http://127.0.0.1:8008/api/ai-gateway/anthropic/w-10026`
丑在 `/api/ai-gateway/` 这段又长又啰嗦(provider 和 agentID 是语义必需,留;前缀该砍)。

**提案**:`/g/<provider>/<id>`
例:`http://127.0.0.1:8008/g/anthropic/w-10026`

**触点**:
- `api/mgr/main.go:379` 路由前缀 `/api/ai-gateway/` → 加注册 `/g/`
- `api/mgr/tmux.go:1296` base URL 构造
- `api/mgr/tmux.go:2526` settings.json 模板里的硬编码 URL
- `api/mgr/openclaw_gateway.go:232` `const prefix` 路径解析

**兼容**:同时保留 `/api/ai-gateway/` 作别名一段时间;**注意**已在跑的 agent 其 `settings.json` 里是旧 URL,需重启 pane 才换新。

---

## 8. 实施顺序建议

1. 先抽共享 SSE 解析函数(§6 第 2 点)—— 不改行为,纯重构,可单独验证。
2. 接 MITM callback(§4 步骤 1/3/4)+ reply.json 路径修正。
3. 落地身份解析方案(§5.1)——这步定了 callback 才能发对地方。
4. URL 简化(§7)—— 独立小改,随时可做。
