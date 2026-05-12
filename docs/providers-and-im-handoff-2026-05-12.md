# AI 供应商 + IM 平台接入 · 进度与未完成事项

记录截至 2026-05-12 的工作交接。涉及两块功能:**AI 供应商管理器** 和 **IM 平台接入**(Telegram + 微信)。

---

## 一、AI 供应商管理器

### 已完成

**后端 (`api/mgr/`)**
- `providers.go` — `~/cicy-ai/global.json` 作为单一事实源,原子写入(保留 `api_token` 及其他键),互斥锁串行化请求
- 端点全部实现:
  - `GET /api/providers` → `{ defaults, items, agent_type_slots, source, source_path }`
  - `POST /api/providers` — 校验 key 正则/唯一性、url、协议 ∈ {openai, anthropic};归一化 models / mapping / defaultModels
  - `GET|PATCH|DELETE /api/providers/{key}` — PATCH 合并到现有记录(拒绝 key 改动);DELETE 被 `providers.default.*` 或某 pane 的 `runtime_ai.provider_name` 引用时返回 409 + references 列表
  - `PUT /api/providers/defaults` — 合并 agent_type → provider_key;空值清除;未知 key → 400;**协议错配 → 400**(claude 必须 anthropic、codex 必须 openai)
  - `POST /api/providers/test` — openai → `GET {url}/v1/models`,anthropic → 最小 `POST /v1/messages`,返回 `{ ok, status, duration_ms, endpoint, detail }`
- `ai_config.go` — 解析 `defaultModels.{claude,codex}` 到 `providerConfig.DefaultModels`
- `tmux.go` / `runtime_ai.go` — `resolveCodexStartupModel` / `resolveClaudeStartupModel` / `runtimeAIDefaultSummaryForAgentType` 全部走 `providerDefaultModelForAgentType(provider, agentType)`(per-agent-type 模型解析,带兜底)
- `providerDefaultAgentTypes = ["claude", "codex"]`(只暴露这两个槽位)
- `providers_test.go` — 6 个测试覆盖 CRUD + 引用检查 + 协议错配,全部通过

**前端 (`app/src/`)**
- `services/api.ts` — `getProviders` / `createProvider` / `updateProvider` / `deleteProvider` / `updateProviderDefaults` / `testProvider`
- `components/providers/ProviderDashboard.tsx` — 经过多轮迭代,最终定型为 **Linear / Vercel 风格暗色 UI**:
  - 页面背景 `#0A0A0A`,卡片 `rounded-2xl border-white/[0.07] bg-white/[0.015]`
  - 统一字号阶梯(17 / 15 / 13 / 12 / 11px),section 标题 uppercase tracking
  - 顶部 slim 48px 头(返回 · Boxes 图标 · 「AI 供应商」· 右侧 mono `global.json` 路径 · 刷新)
  - 下方 segmented pill tab(「Agent 路由」/「供应商」)
- **「Agent 路由」tab** — 居中 760px 列,Claude / Codex 两张卡片(头像 + 名称 + 协议徽章 + 状态 pill「已配置 / 协议不匹配 / 未配置」+ `ProviderPicker` + 上游/模型小定义列表)
- **「供应商」tab** — 左 288px 列(搜索 + 列表 + 「添加供应商」CTA)+ 右 680px 表单(「基本信息 / 接入 / 模型」三段分区),Cmd/Ctrl+S 保存,dirty 跟踪
- 自定义 `ProviderPicker` 下拉:rounded-xl bg-[#141416] shadow-2xl,选中带 ✓,「仅 X 协议」头,不匹配的当前项单独列在底部
- **Chrome「保存密码」弹窗修复** — API Key 框改成 `<input type="text">` + `-webkit-text-security: disc`(点眼睛切换),加 `autoComplete="off"` / `data-lpignore` / `data-1p-ignore`
- `Workspace.tsx` — `btn-providers` SideBtn(Boxes 图标) + `providersOpen` state + `createPortal` 全屏 overlay(`bg-[#0A0A0A]` z-9000),**关闭只是 `setProvidersOpen(false)`,Workspace 不卸载、team 面板状态完整保留**

### 未完成 / 设计上保留

- 默认供应商只暴露 `claude` / `codex`,但旧的 `global.json` 里若有 `providers.default.opencode` 等,**API 仍兼容保留**,只是 UI 不显示
- GET `/api/providers` 返回 `apiKey` 明文(与现有 `/api/settings/global` 一致),没有 server-side masking

---

## 二、IM 平台接入(Telegram + 微信)

### 已完成

**数据层**
- `db.go` — 新增 `im_accounts` 表(`id`/`platform`/`name`/`secret`/`config(JSON)`/`enabled`/`state`/`state_detail`/`bound_pane_id`/`inbound_to_agent`/`created_at`/`updated_at`)+ `idx_im_accounts_bound` 索引;走 `Migrate()` 自动建
- SQLite `busy_timeout(5000)` 加固(早先的修复)

**核心抽象 (`api/mgr/im.go`)**
- `botTransport` 接口(`Kind` / `Poll` / `Send` / `Edit` / `Typing` / `CanEdit`)
- `imAccount` 存取(GET 时打码 secret,只返回 `has_secret` + `secret_tail`)
- `imManager` 单例 — `main.go` 启动时 `go imManagerStart()`,`Reconcile()` 给每个 enabled 账号起一个被监督的 worker;30s 健康 tick 重启挂掉的;账号增删改 / 绑定变更 → `imReconcileAccount(id)` 立即生效不重启后端
- inbound 处理 — IM 消息 → `sendTextToPane(boundPaneID, text, true)`(走 `/api/tmux/send`,**不走 queue**);TG 先发一条可编辑的「Thinking...」打底
- 一对一绑定校验(每个账号 ≤1 agent;同一 agent 同平台 ≤1)
- `imWorkersDisabled` 标志 — 测试用,跳过后台 goroutine

**Telegram (`api/mgr/im_telegram.go`)**
- 复用 `tg.go` 的 `getUpdates` / `sendTGPlainMessageWithToken` / `editTGMessageTextWithToken` / `sendTGChatActionWithToken`
- `telegramValidateToken(token) (username, reachable, err)` — 调 `getMe` 校验、取 `bot_username`;网络不可达时仍接受 token 但 state=`error`
- cursor = offset,在 worker 内维护
- 创建账号支持空 token(在详情页再填),也支持立即带 token

**微信 (`api/mgr/im_wechat.go`)**
- 移植 `cicy-wechat` npm 包的 ilink bot 协议为 Go(纯 HTTP,无 node 依赖):
  - 扫码登录:`GET ilink/bot/get_bot_qrcode?bot_type=3` → 长轮询 `GET ilink/bot/get_qrcode_status` 状态机(`wait` / `scaned` / `confirmed` / `expired`(刷新 ≤3 次) / `scaned_but_redirect` 切换 `redirect_host`)
  - 收发:`POST ilink/bot/getupdates`(游标 `get_updates_buf`,`errcode=-14` → 清状态重扫)、`POST ilink/bot/sendmessage`(带 `to_user_id` + `context_token`,**不能 edit**)、`getconfig` + `sendtyping`
- 登录态(token + 游标)持久化到 `~/cicy-ai/db/im-wechat-<id>.json`,后端重启自动续上;失效才重扫
- **「扫码后才入列表」流程** — 直接 `POST /api/im/accounts` 创建微信账号现在返回 400,必须走:
  - `POST /api/im/wechat/login` — 发起扫码 session(已有进行中 session 或未 connected 微信账号 → 409)
  - `GET /api/im/wechat/login/{id}` — 轮询 state(`qr_wait` / `scaned` / `expired` / `error` / `created`)
  - `POST /api/im/wechat/login/{id}/cancel` — 取消
  - 后端 confirmed 时:先 enabled=0 插入 → 写状态文件 → enabled=1 → reconcile(避免 worker 在状态文件写好前抢着扫码)

**回复推送 (`api/mgr/im_reply_hook.go` + `ai_gateway_audit.go`)**
- 新增 `aiGatewayReplyHook` 接口,把 `aiGatewayAuditSession.tgHook *tgReplyPushHook` 改为 `replyHooks []aiGatewayReplyHook`
- `newReplyHooksForPane(agentID)` — 同时返回旧的 per-pane TG hook(兼容)+ 每个 `im_accounts WHERE bound_pane_id=agentID AND enabled` 的 hook
- `imReplyPushHook` — 限频(1.2s),Telegram edit 一条 live 消息流式更新、微信 finalize 整条发
- **只推 thinking text + answer text,不推任何 tool 调用 / 结果**(`renderLocked` 已去掉 Tools: 段;working 时最多一行不带参数的「🔧 工作中…」)
- `emitReplyStreamPayload` / `completeFromResponse` 循环 `replyHooks`

**REST 路由**
- `GET /api/im/platforms` — UI 渲染加什么平台 + 帮助文案(Telegram 的 BotFather 步骤等)
- `GET|POST /api/im/accounts`
- `GET|PATCH|DELETE /api/im/accounts/{id}`
- `POST /api/im/accounts/{id}/test`
- `POST /api/im/accounts/{id}/bind|unbind`(校验:同 pane 同平台只能绑 1 个;pane 必须存在)
- `GET /api/im/accounts/{id}/qr`(微信扫码时前端轮询;**目前已经废弃,改走 wechat/login session**)
- `POST /api/im/accounts/{id}/relogin`
- `POST /api/im/send {pane_id, text}` — agent 用,路由到该 pane 绑定的账号
- 旧 `/api/tg/send` / `/api/tg/photo` / `syncTelegramPollers` / AgentInspector Telegram tab **全部保留不动**(向后兼容)

**前端**
- `services/api.ts` — `getIMPlatforms` / `getIMAccounts` / `createIMAccount` / `updateIMAccount` / `deleteIMAccount` / `testIMAccount` / `bindIMAccount` / `unbindIMAccount` / `getIMAccountQR` / `reloginIMAccount` / `startWeChatLogin` / `getWeChatLoginStatus` / `cancelWeChatLogin`
- `components/im/IMDashboard.tsx` — master-detail UI(复用供应商页设计原子):
  - 左栏:IM 账号列表(平台图标 + 名字 + 状态点「已连接 / 等待扫码 / 已扫码待确认 / 错误 / 待命」+ 绑定标)+ 搜索 + 「+ 添加」下拉(Telegram / 微信 / 灰掉的 Discord)
  - 右栏 Telegram:名称 / Bot Token(眼睛切换、不弹 Chrome 存密码)/ 折叠的「获取 Token」(BotFather 步骤 + 链接)/ 聊天绑定状态 / 测试发送 / Agent 绑定 + inbound 开关 / 启用开关
  - 右栏 微信:未登录显示二维码 + 状态 + 「重新生成」;已登录显示昵称/userId + 「退出」+ Agent 绑定 + 启用开关 + 测试发送
  - **微信扫码 modal**(portal,2.5s 轮询)— `state==='created'` 时关闭 modal、toast「微信已添加」、刷新列表;「添加微信」选项在已有进行中扫码或未 connected 微信账号时置灰
- `Workspace.tsx` — `btn-im` SideBtn(MessageCircle 图标,在 `activity-bar-top` 第三个) + `imOpen` state + `createPortal` 全屏 overlay
- 自定义 `useDialogs` confirm/prompt 组件(`components/ui/Modal.tsx`)— 替代 `window.confirm/prompt`,在 IM 页用于删除/重启扫码等确认

**测试**
- `api/mgr/im_test.go` — `/api/im/platforms`、微信账号 PATCH/绑定/解绑/删除流程、Telegram 无 token 创建、绑定 1:1 校验(同 pane 第二个微信 → 409、绑不存在的 pane → 400、解绑后能再绑);全部通过

### 未完成 / 已知问题

- **二维码渲染依赖外部服务** — 前端目前用 `api.qrserver.com` 渲染微信扫码 QR(QR 内容会发给这个第三方)。要完全自托管,需要打包个 QR 库或让后端渲染成 PNG data-url
- **旧 `agent_config.tg_*` 列未迁移** — `syncTelegramPollers` 和 AgentInspector Telegram tab 还在用旧 per-pane 模型,与新 `im_accounts` 共存。**Phase 4 收尾任务**:迁进 `im_accounts`、删 AgentInspector Telegram tab、`/api/tg/send` 内部走 `/api/im/send`
- **Discord 等其他 IM 平台未实现** — `botTransport` 接口已经能容下,但没写实现
- **微信版 reply hook 不能 edit** — 流式回复期间会重复发(限频 1.2s),不像 Telegram 是 edit 一条 live 消息。当前用 finalize 时整条发的折中做法
- **code-server iframe `:code-ext` 乒乓问题未彻底解决** — 工作区只有一个 code-server iframe,但两个 tab 共用同一 `${pageClientId}:code-ext` 会触发 BroadcastChannel dedup。已经加了 client 侧 dedup,但**服务端「同 client_id 多连接」加固还没做**。会话最后用户开了两个同 agent tab 让排查,API ECONNRESET 中断了

---

## 三、构建 / 部署状态

- `./build.sh test-go ./mgr/...` 全部通过
- `npm run lint` — 11 个**预存**错误(`Workspace.tsx`, `chat/*`, `AgentCanvas`),新代码 0 错误
- `npm run build` 通过
- 新二进制已构建:`/Users/ton/projects/cicy-code/api/cicy-code`(linux/amd64)
- **后端没重启**(按用户惯例)— 要看到所有新功能生效需:重启 cicy-code 后端 → 刷新页面 → 按需重启 worker

---

## 四、关键文件清单

```
api/mgr/
  providers.go              新建 - AI 供应商 CRUD
  providers_test.go         新建 - 6 个测试
  ai_config.go              改 - defaultModels 解析
  tmux.go / runtime_ai.go   改 - per-agent-type 模型解析
  im.go                     新建 - IM 抽象 + manager + REST
  im_telegram.go            新建 - telegramTransport
  im_wechat.go              新建 - weChatTransport (ilink 协议)
  im_reply_hook.go          新建 - aiGatewayReplyHook 接口 + impl
  im_test.go                新建 - IM 流程测试
  ai_gateway_audit.go       改 - tgHook → replyHooks []
  db.go                     改 - im_accounts 表
  main.go                   改 - 注册 /api/providers/* 和 /api/im/*
                                  + go imManagerStart()

app/src/
  components/providers/ProviderDashboard.tsx   新建 - 供应商管理 UI
  components/im/IMDashboard.tsx                新建 - IM 管理 UI
  components/ui/Modal.tsx                      新建 - useDialogs confirm/prompt
  components/Workspace.tsx                     改 - btn-providers / btn-im + portal overlay
  services/api.ts                              改 - provider / IM / wechat-login 方法
```

---

## 五、下次接手时的优先级建议

1. **彻底解决 code-server `:code-ext` 乒乓** — 服务端「同 client_id 多连接」加固
2. **Phase 4 收尾** — 旧 `tg_*` 列迁进 `im_accounts`、删 AgentInspector Telegram tab、`/api/tg/send` 内部走 `/api/im/send`
3. **QR 自托管** — 打包 QR 库或后端渲染 PNG data-url,去掉 `api.qrserver.com` 依赖
4. **Discord 接入** — 加一个 `botTransport` 实现
5. **微信 reply hook 优化** — 现在是 finalize 整条发,可以考虑「整条覆盖发 + 限频」的中间形态
