# cicy-code — 产品路线图

> **定位：云端 AI 协同工作站**
> 
> 一键部署在任何海外机房，用户只需要浏览器就能流畅使用全球所有 AI Agent 并行协作。
> 
> Pitch："你的 AI 团队在云端，你只需要一个浏览器。"
> 
> 创始人日常：Mac 上只开一个浏览器，零行代码在本地写，所有开发全在 GCP 上完成。
> 
> 不造 AI，造 AI 的工头。不做 IDE，做 AI 团队的云端指挥部。

## 目标用户

- 中国开发者：想用 Claude/GPT/Gemini/Codex 但被墙挡着，VPN 体验极差
- 独立开发者 / 小团队：想用 AI 提高产出但觉得一个 agent 太慢
- 对成本敏感：需要知道每个 AI 花了多少钱
- 网络受限地区的开发者：需要一个低延迟的云端 AI 工作环境

## 核心价值

1. **云端零延迟** — AI Agent 和代码全在海外 VPS，用户只传文字指令，不受防火墙影响
2. **多 AI 协同** — kiro-cli + claude-code + codex-cli 同时跑，不同 AI 干不同的活
3. **全透明可控** — 每个 agent 实时可见，token 消耗实时监控，成本可控
4. **一键部署** — Docker Compose 一条命令，5 分钟从零到可用

## 架构优势

```
竞品（Cursor/Windsurf/Antigravity）:
  中国用户电脑 ←防火墙/高延迟→ 海外 AI API    ← 卡死

cicy-code:
  中国用户浏览器 ←CF Tunnel/FRP→ 海外 VPS
                                  ├── Web UI（轻量交互，几 KB）
                                  ├── AI Agent × N（本地调 API，零延迟）
                                  ├── code-server（本地，零延迟）
                                  └── mitmproxy（流量监控）
```

---

## 已完成功能

### 后端 — ttyd-manager (Go)
- 单 Go binary，嵌入 gotty，goroutine per pane
- fsnotify 实时监控 pane 状态，写入 Redis
- tmux 终端管理全套 API（创建/重启/发送/捕获）
- Agent 绑定/解绑 API + Token 认证
- Chat 历史 API（两遍提取，tool arg 填充，行号范围）
- SSE 实时推送
- 全局/Agent 设置 API

### 前端 — React + Vite + TailwindCSS
- 三栏布局：Agent 列表 / 终端 / 功能面板，可折叠可拖拽
- ChatView：AI Studio 风格对话界面，tool 汇总，Markdown + GFM 表格
- Workers Tab：卡片网格，内嵌终端/ChatView，响应式布局
- 命令面板：历史、草稿、Ctrl+Enter 英文纠正
- 语音输入：浮动按钮，录音转文字发送
- code-server dialog：点击文件路径弹出编辑器
- 全局 CSS 变量主题系统

### 基础设施
- mitmproxy 流量拦截 + MySQL 存储
- Docker Compose 部署（API + 前端 + code-server + mitmproxy + Redis + MySQL）
- CF Tunnel / FRP 外网访问
- Electron 桌面壳 + 跨机器控制（Linux/Win/Mac）

---

## Phase 1 — 一键部署 + 开箱即用

> 核心卖点是云端工作站，部署体验就是产品本身。
> 目标：用户租 VPS → 装 Docker → 一条命令 → 5 分钟可用。

- [ ] docker compose up 一条命令跑起全套
- [ ] 自动初始化：建表、默认配置、生成 token
- [ ] 首次访问引导页：设置密码、绑定第一个 agent
- [ ] 部署文档：GCP / AWS / 搬瓦工 / Vultr 各写一份
- [ ] CF Tunnel 一键配置脚本
- [ ] 健康检查 API：前端显示各服务状态
- [ ] README 重写：面向中国用户，突出"翻墙 AI 工作站"卖点

## Phase 2 — Worker-Master 协同引擎

> 核心差异化功能。不同 AI 厂商的 agent 协同工作。

### 2.1 基础协同
- [ ] Agent 角色：config JSON 支持 role（worker / master）
- [ ] 前端绑定 agent 时选角色 UI
- [ ] 角色标签显示（📋 Master / 🔧 Worker）

### 2.2 Watcher Hook
- [ ] Worker idle 检测（fsnotify: thinking → idle）
- [ ] 自动查找同 workspace 的 Master
- [ ] `tm msg` 通知 Master 验收
- [ ] 通知 API：`POST /api/notify` → Redis PUBLISH → SSE

### 2.3 共享文档
- [ ] `.cicy/` 目录约定（todo.md / arch.md / context.md）
- [ ] `GET /api/cicy/files` — 列出 .cicy/ 文件
- [ ] `GET /api/cicy/file` — 读取文件内容
- [ ] 前端 Dashboard tab：文件列表 + todo 进度条 + markdown 渲染
- [ ] 5s 轮询刷新

### 2.4 Master Prompt 模板
- [ ] System prompt：plan → 写 .cicy/ → 等确认 → 分配执行
- [ ] 验收 prompt：读 todo → 检查 → 派下一项
- [ ] 内置 prompt 模板库（可选）

## Phase 3 — 任务分发器

> 从手动到半自动。

- [ ] 手动模式：选多个 agent → 输入子任务 → 一键分发
- [ ] 任务面板 UI：每个 agent 的当前任务、状态、进度
- [ ] 任务队列：agent 完成当前任务后自动领取下一个
- [ ] 后续：AI 自动拆解大任务为子任务并分配

## Phase 4 — Token 消耗面板 + 审计

> 成本控制是刚需，也是 B2B 变现基础。

- [ ] 实时 token 消耗曲线（per agent / per conversation）
- [ ] 累计消耗统计 + 日/周/月报表
- [ ] 阈值报警（单次对话超限弹通知）
- [ ] 自动拦截（超限暂停 agent）
- [ ] 审计日志：谁调了什么 API、改了什么文件、花了多少钱
- [ ] 数据源：mitmproxy 日志 + SSE usage 字段

## Phase 5 — TG 远程控制

> 手机语音指挥 AI 团队，不用打开电脑。

- [ ] TG Bot 接收语音/文字消息
- [ ] 语音 → STT → 转发给 master agent
- [ ] Master 自动拆任务分发给子 agents
- [ ] 执行结果通过 TG Bot 推送回手机
- [ ] 状态查询：发 /status 看所有 agent 状态

## Phase 6 — 分布式节点网络

> 长期护城河。一个控制台管理全球节点。

- [ ] 节点注册/发现：agent 启动时自动注册，上报能力（OS/GPU/SDK）
- [ ] CF Tunnel / FRP 连接多节点
- [ ] Master 按任务需求自动选节点：iOS 编译 → Mac，GPU → Windows
- [ ] 统一 agent 协议：不管节点在哪，同一套指令控制
- [ ] 节点健康监控 + 自动故障转移

## Phase 7 — UI 持续打磨

> 不追求花哨，追求干净、一致、专业。参考 Vercel / Linear 调性。

- [ ] Agent 状态看板升级：进度条、当前任务、耗时统计
- [ ] 冲突检测：多 agent 同时改同一文件时弹通知
- [ ] 简单 diff 合并界面
- [ ] 深色/浅色主题切换
- [ ] 移动端适配（手机浏览器基本可用）

## Phase 8 — Agent Market + Plugin Market

> Conversation 为核心，做可扩展生态。

### Agent Market
- [ ] Agent 注册协议：名称、CLI 命令、安装脚本、图标、描述
- [ ] 一键安装 agent（kiro-cli / claude-code / codex-cli / opencode / aider）
- [ ] 社区提交 agent 配置
- [ ] agent 版本管理 + 自动更新

### Plugin Market
- [ ] 插件 API：hook 进 conversation 生命周期（before_send / after_reply / on_tool_use）
- [ ] 内置插件：自动翻译、代码审查、自动测试、文档生成
- [ ] Prompt 模板市场：Master prompt、验收 prompt、场景 prompt
- [ ] MCP Server 市场：给 agent 扩展能力（数据库、API、浏览器）
- [ ] 社区提交 + 审核机制

---

## 设计原则

- **站在巨人肩膀上** — 不造轮子，组装最强的轮子。VS Code（code-server）做编辑、Claude/Kiro/Codex 做 AI、mitmproxy 做监控、tmux 做隔离、CF Tunnel 做穿透。我们只做胶水 + 管控 + 可视化
- **Chat 为主** — 对话是核心交互方式，用户通过 Chat 指挥 AI 团队，面板/按钮是辅助
- **云端优先** — 所有功能围绕"云端工作站"设计，不做本地 IDE
- **UI 不 low** — 不跟 Cursor 比，但要干净专业有质感
- **部署极简** — Docker Compose 是唯一部署方式
- **不挑 AI** — 所有终端 CLI Agent 即插即用，一个平台全管
- **成本可控** — 每分钱花在哪都看得见，巨人们做不到的我们来补

## 技术栈

| 层 | 技术 |
|----|------|
| 前端 | React 19 + Vite + TailwindCSS |
| 编辑器 | code-server (VS Code) |
| 终端 | gotty (魔改) + xterm.js |
| 后端 | Go (ttyd-manager) |
| 终端管理 | tmux |
| 数据库 | MySQL + Redis |
| 流量监控 | mitmproxy |
| 外网访问 | CF Tunnel / FRP |
| 桌面 | Electron (可选) |

## 竞品对比

| 维度 | Cursor/Windsurf | Claude Code | Devin | cicy-code |
|------|----------------|-------------|-------|-----------|
| 形态 | 本地 IDE | 终端工具 | SaaS | 云端工作站 |
| 中国可用 | 卡/不可用 | 需 VPN | 需 VPN | 浏览器直连 |
| 多 AI 厂商 | 单一 | 单一 | 单一 | 全部 |
| 多 Agent 并行 | 同一 AI 多实例 | Agent Teams | 自主并行 | 不同 AI 协同 |
| 成本监控 | ✗ | ✗ | ✗ | ✓ 实时 |
| 部署位置 | 用户电脑 | 用户电脑 | 云端(黑盒) | 用户自己的 VPS |
| 数据归属 | 厂商 | 厂商 | 厂商 | 用户自己 |
