# P0 — 双 Agent 协同 + Plan Mode

> 隶属：Phase 2 — Worker-Master 协同引擎
> 详细 TODO：见 P0-todo.md

## 概述

两个 Agent（Master + Worker）协同工作，通过 `.cicy/` 共享文档协调。采用 Plan → Confirm → Execute 工作流：Master 出方案，用户审阅确认，Worker 执行。

## 为什么要双 Agent

单 agent 既规划又执行，存在根本问题：

1. **无法并行** — 一个 agent 执行代码时不能同时规划下一步，串行效率低
2. **无法自动验收** — 单 agent 干完活就停了，没人检查质量、推进下一步，需要用户手动介入
3. **Prompt 臃肿** — 一个 agent 既要懂规划又要懂执行，prompt 越来越长，效果越来越差
4. **可扩展性** — 双 agent 架构天然支持 1 Master 管 N Worker（P1 任务分发器），单 agent 做不到

双 Agent 的核心机制：
- Worker 执行完毕（thinking → idle）→ watcher 自动 `tm msg` 通知 Master
- Master 收到通知 → 读 `.cicy/todo.md` 验收 → 分配下一个任务
- 全程自动流转，用户只需要在 Plan 阶段确认方案

## 竞品分析与差异化

### 主流竞品

| 产品 | 模式 | 局限 |
|------|------|------|
| Kiro IDE | Spec-driven：需求→规格→任务→执行 | 单 agent 串行，锁定 Claude |
| Cursor | Plan mode：Shift+Tab，plan 在对话里 | 单 agent，plan 滚过去就没了 |
| Claude Code | Sub-agent：主 agent spawn 隐藏子 agent | 子 agent 黑盒，用户看不到过程 |
| Devin | 全自主：丢任务自己跑 | 黑盒，失去控制，$20-500/月 |
| OpenHands | 单 agent + sandbox | 开源但单 agent，无协同 |

### 所有竞品的共同痛点

1. **锁定单一 AI** — 每个产品只能用自家模型，不能混用最强的
2. **过程不透明** — sub-agent 是黑盒，用户看不到谁在干什么
3. **Plan 不持久** — 方案在对话里输出，滚过去就找不到，无法系统性审阅
4. **无法人工介入** — 要么全自主（Devin），要么全手动，没有中间态
5. **成本不可控** — 不知道每个 agent 花了多少 token，超支了才发现

### 我们的五大卖点

#### 1. 🔀 不挑 AI — 博众家所长
> "Cursor 给你一个助手，我们给你一支 AI 团队"

- Master 用 Claude（擅长推理规划），Worker 用 Codex（擅长代码生成）
- 同一个平台跑 Kiro、Claude Code、Codex、OpenCode、Copilot
- 用户按任务特点选最合适的 AI，不被任何一家锁定
- **竞品做不到**：Cursor 只能用 Cursor 的模型，Kiro IDE 只能用 Claude

#### 2. 👁️ 全透明 — 看得见的 AI 团队
> "不是黑盒，是玻璃房"

- 每个 agent 有独立终端，实时看到在干什么
- `.cicy/` 文档在 code-server 里随时查看编辑
- 流量监控面板看到每个 agent 的 API 调用和 token 消耗
- **竞品做不到**：Claude Code 的 sub-agent 是隐藏的，Devin 是黑盒

#### 3. 📄 Plan 落文件 — 方案不会丢
> "不是聊天记录，是项目文档"

- Master 的方案写入 `.cicy/todo.md`、`.cicy/arch.md`，持久化到文件系统
- 用 code-server（VS Code）审阅，不是在 terminal 里滚
- 方案就是代码仓库的一部分，可以 git 追踪
- **竞品做不到**：Cursor plan 在对话里滚没了，Kiro spec 在 IDE 专有格式里

#### 4. 🎛️ 人在回路 — 可控的自动化
> "不是全自主，也不是全手动，是你说了算"

- Plan 阶段：Master 出方案 → 用户审阅确认 → 才开始执行
- Execute 阶段：自动流转（Worker 完成 → Master 验收 → 下一项）
- 随时可以暂停、修改 `.cicy/todo.md`、调整方向
- **竞品做不到**：Devin 全自主失控，Cursor 全手动低效

#### 5. 💰 成本透明 — 每分钱花在哪
> "不是月底账单吓一跳，是实时看到花了多少"

- mitmproxy 监控每个 agent 的 HTTP 流量和 token 消耗
- 实时流量面板，按 agent 分别统计
- 未来：阈值报警，超限自动拦截
- **竞品做不到**：没有任何竞品提供 per-agent 的实时成本监控

## 用户流程

```
┌─────────────────────────────────────────────────┐
│ Phase 1: Plan                                   │
│                                                 │
│ 用户 → "我想做一个登录页面"                       │
│ Master → 理解需求                            │
│           → 写 .cicy/todo.md（任务拆解）          │
│           → 写 .cicy/arch.md（架构设计，如需要）   │
│           → 告诉用户："方案已写好，请在 code-server │
│             中查看 .cicy/ 目录"                   │
│                                                 │
│ ⏸️ 等待用户确认                                  │
├─────────────────────────────────────────────────┤
│ Phase 2: Confirm                                │
│                                                 │
│ 用户在 code-server 中：                          │
│   - 查看 .cicy/todo.md，觉得拆解合理             │
│   - 查看 .cicy/arch.md，修改了一些设计            │
│   - 回到 terminal 说 "ok" / "改好了，继续"        │
│                                                 │
├─────────────────────────────────────────────────┤
│ Phase 3: Execute                                │
│                                                 │
│ Master → 读取最新 .cicy/todo.md              │
│           → 给 Worker 发指令执行第一个任务         │
│ Worker → 执行 → 完成 → 更新 .cicy/todo.md        │
│ Watcher → 检测 Worker idle → 通知 Master     │
│ Master → 验收 → 派下一个任务                  │
│ 循环直到所有任务完成                              │
└─────────────────────────────────────────────────┘
```

## 用户视角

用户只做两件事：
1. **说需求** — 在 Master terminal 里用自然语言描述
2. **审阅确认** — 在 code-server 里看 `.cicy/` 文件，改或不改，回来说 ok

前端展示：
- 左侧：agent 列表（带角色标签 📋 Master / 🔧 Worker）
- 中间上：Worker TTY + Master TTY（并排 iframe）
- 中间下：`.cicy/` 概览面板（文件列表 + todo 进度条）
- 右侧：code-server（已有，用于审阅/编辑 `.cicy/` 文件）

## .cicy/ 共享文档

位置：`{workspace}/.cicy/`（workspace 从 DB pane 记录获取）

由 Master agent 创建和管理，用户可通过 code-server 编辑。

常见文件（agent 自由创建，不限于这些）：
- `todo.md` — 任务清单（`- [ ]` / `- [x]` 格式）
- `arch.md` — 架构设计
- `context.md` — 项目上下文
- 其他 agent 认为需要的文件

## 后端

### API

```
GET /api/cicy/files?pane={pane_id}
→ 列出 {workspace}/.cicy/ 下所有文件
→ [{name, size, mtime}]

GET /api/cicy/file?pane={pane_id}&name={filename}
→ 读取文件内容
→ {name, content, mtime}
```

### Agent 角色

config JSON 增加 role：
```json
{"role": "master"}  // "worker" | "master" | null
```

### Watcher Hook

Worker idle → 查找同 workspace 的 Master → `tm msg` 通知验收

## 前端

### Drawer 里的三个视角

**Dashboard tab（新增）— .cicy/ 任务仪表盘**
- 文件列表（todo.md / arch.md / ...）
- todo 进度条（解析 checkbox，显示 4/6 67%）
- markdown 渲染，快速浏览
- 5s 轮询刷新
- 用户一眼看到：做了什么、还剩什么、整体进度

**Preview tab（已有）— 产出实时预览**
- iframe 加载 vite/前端页面 URL
- 用户实时看到 agent 做出来的东西长什么样
- 保持现有功能不变

**Code tab（已有）— code-server 编辑器**
- 打开 `.cicy/` 目录看具体文档内容
- 编辑 todo.md、arch.md，修改方案
- 看代码变更、审阅实现细节

用户流程：
1. Master 写完方案 → 用户打开 Drawer
2. Dashboard 看进度概览
3. 想看细节 → Code tab（code-server）编辑
4. 想看效果 → Preview tab 看产出页面
5. 确认 → 回 terminal 说 ok

### 双终端布局

选中 master 或 worker 时，自动并排显示配对的另一个终端：
```
┌──────────────┬──────────────┐
│ Master   │ Worker       │
│ (iframe)     │ (iframe)     │
├──────────────┴──────────────┤
│ .cicy/ 概览                 │
│ todo.md: ████░░ 4/6 (67%)  │
└─────────────────────────────┘
```

### Drawer Tabs（6 个）

```
Dashboard / Agents / Code / Preview / Traffic / Settings
```

- **Dashboard**（新增）— .cicy/ 任务仪表盘
- **Agents** — agent 列表、绑定管理
- **Code** — code-server 编辑器
- **Preview** — 产出预览（vite 等 iframe URL）
- **Traffic** — 流量监控（已完成）
- **Settings** — 全局设置

删除：Password（个人工具）、Prompt（移到终端 topbar 作为快捷指令）

## Master Prompt 要点

- 你是项目调度者
- 用户说需求后，先写方案到 `.cicy/` 目录，不要直接执行
- 写完后调用通知 API 让用户审阅（前端自动弹出 Dashboard）
- 用户确认后，按 `.cicy/todo.md` 逐项分配给 Worker
- Worker 完成后验收，更新 todo，分配下一项

## Agent → Frontend 通知机制（SSE）

Agent 通过 API 主动推消息给前端，触发 UI 动作。

### 后端

```
POST /api/notify
Body: {"pane":"w-20083", "action":"open_drawer", "tab":"Dashboard", "message":"方案已写好，请确认"}
→ Redis PUBLISH kiro_notify → SSE 推给前端

GET /api/notify/stream?token=xxx
→ SSE 连接，接收通知事件
```

### 支持的 action

| action | 参数 | 效果 |
|--------|------|------|
| open_drawer | tab | 打开 Drawer 到指定 tab（Dashboard/Code/Preview） |
| open_file | file | code-server 打开指定文件 |
| toast | message | 弹通知消息 |
| refresh | target | 刷新指定面板（dashboard/traffic） |

### Agent 调用方式

Master 写完方案后：
```bash
curl -X POST http://127.0.0.1:8008/api/notify \
  -d '{"pane":"w-20083","action":"open_drawer","tab":"Dashboard","message":"方案已写好，请在 Dashboard 确认"}'
```

Worker 完成任务后：
```bash
curl -X POST http://127.0.0.1:8008/api/notify \
  -d '{"pane":"w-20083","action":"toast","message":"✅ 任务 3/6 已完成"}'
```

### 前端

- 页面加载时建立 SSE 连接 `/api/notify/stream`
- 收到事件后执行对应 action（打开 drawer、切 tab、弹 toast）
- 用户无需手动操作，agent 推什么就显示什么

## 技术依赖

- 后端：Go (ttyd-manager)，已有 fsnotify、tmux API、Redis
- 前端：React，已有 WebFrame、多终端布局、code-server 集成
- 数据库：MySQL ttyd_config 表（config JSON 字段）
- workspace 路径：DB pane 记录的 workspace 字段
- 流量监控：mitmproxy + kiro_monitor.py + Redis（已完成）
