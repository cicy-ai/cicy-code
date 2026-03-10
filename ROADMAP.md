# AI Agent 协作开发平台 — 产品路线图

> 定位：让一个人同时指挥多个 AI Agent 并行干活的 IDE。
> 核心：一个 Go 二进制，管所有大厂 AI Agent 的协同——Kiro、Claude Code、Codex、OpenCode、Copilot。
> Pitch："不造 AI，造 AI 的工头。Cursor 给你一个助手，我们给你一支 AI 团队。"
> 不挑 AI：所有终端 CLI Agent 即插即用，一个平台全管。

## 目标用户

- 独立开发者 / 小团队，想用 AI 提高产出但觉得一个 agent 太慢
- 已经在用 Cursor/Copilot 但想要更高效的工作流
- 对成本敏感，需要控制 token 支出
- 中国开发者：需要用海外强模型（Claude/Codex/Gemini），但成本高、平台割裂

## 核心价值

1. **博众家所长** — Claude 擅长推理、Codex 擅长生成、Gemini 上下文长，让最合适的 AI 干最合适的活
2. **成本控制** — mitmproxy 监控每个 agent 的 token 消耗，超限自动拦截
3. **一个平台打通** — 不用在 5 个终端之间切，一个界面管所有 agent
4. **多 Agent 协同** — Master agent 拆任务，多个不同 AI 并行执行

## 已完成功能

### 后端 — ttyd-manager (Go)
- 单 Go binary 替代 fast-api + ttyd-proxy + cron_pane_watcher
- 嵌入 gotty，goroutine per pane
- fsnotify 实时监控 pane 状态，写入 Redis
- tmux 终端管理全套 API（创建/重启/发送/捕获）
- Agent 绑定/解绑 API
- Token 认证系统
- 全局/Agent 设置 API

### 前端 — tmux-app-v2 (React + Vite)
- 三栏布局：Agent 列表 / 终端 / 功能面板，可折叠可拖拽
- ttyd WebSocket 终端嵌入，多终端视图（单屏/水平/垂直/网格）
- Agent 系统：列表、实时状态、搜索、Pin 置顶、绑定/解绑、创建
- 命令面板：历史、草稿、Ctrl+Enter 英文纠正、快捷操作
- 语音输入：浮动按钮，录音转文字发送
- 右侧 6 Tab：Agents / Code / Prompt / Preview / Password / Settings
- code-server 集成（VS Code 编辑体验）
- Token CRUD 管理、权限控制

### 基础设施
- gotty 源码修改：浏览器文本选择/复制修复
- Electron 桌面壳 + 跨机器控制（Linux/Win/Mac）
- mitmproxy 流量拦截

## 路线图

### P0 — Worker-Master 双 Agent 协同
> 核心差异化功能，优先级最高

- [ ] 共享文档机制：A/B 共用一个 task doc（markdown），记录任务列表和完成状态
- [ ] watcher hook：pane 从 thinking → idle 时，自动 tm msg 给绑定的 master
- [ ] Master 模板 prompt：读文档 → 验收 → 给 worker 发下一步指令
- [ ] 前端：绑定 agent 时可设角色（worker / master）
- [ ] 前端：任务文档实时预览

### P1 — 任务分发器（MVP）

- [ ] 手动模式：用户选择多个 agent，输入子任务，一键分发
- [ ] 任务面板 UI：显示每个 agent 的当前任务、状态
- [ ] 后续：AI 自动拆解大任务为子任务并分配

### P2 — Agent 状态看板升级
> 把左侧栏从"列表"升级为"Dashboard"

- [ ] 进度条 / 当前任务描述
- [ ] 耗时统计
- [ ] Token 消耗 per agent
- [ ] 状态变化时间线

### P3 — Token 消耗面板
> 多 agent 并行跑，成本控制是刚需

- [ ] 实时 token 消耗曲线（per conversation / per agent）
- [ ] 累计消耗统计
- [ ] 阈值报警（单次对话超限弹通知）
- [ ] 数据源：mitmproxy 日志 或 SSE usage 字段

### P4 — Telegram 远程控制
> 手机语音指挥 AI 团队，不用打开电脑

- [ ] TG Bot 接收语音/文字消息，转发给 master agent
- [ ] master agent 自动拆任务分发给子 agents
- [ ] 执行结果通过 TG Bot 推送回手机
- [ ] 链路：TG 语音 → STT → master agent → tm msg → 子 agents → tg send → 手机通知

### P5 — gotty iframe → xterm.js 直连
> 去掉 iframe，前端直接 WebSocket 连 gotty，每个终端 = div + xterm.js 实例

- [ ] 前端集成 xterm.js 4.x/5.x，替换 iframe 嵌入
- [ ] 实现 gotty WebSocket 协议（Input/Output/Resize/AuthToken）
- [ ] 统一主题、字体、快捷键
- [ ] 选择/复制原生支持，删除 gotty-bundle.js 的 sed hack
- [ ] 清理后端：移除 HTML 注入、/static/ 路由、asset.go dev mode

### P6 — Docker Compose 开箱即用
> 让用户一条命令跑起来

- [ ] Docker Compose：ttyd-manager + Redis + MySQL + code-server + mitmproxy
- [ ] 初始化脚本：自动建表、默认配置
- [ ] README 安装文档

### P7 — 去依赖（单二进制）
> 长期目标：一个 Go binary 搞定一切

- [ ] MySQL → SQLite（优先）
- [ ] Redis → 内嵌 KV（bbolt/badger）
- [ ] mitmproxy → Go 内置 HTTPS proxy（只抓大小和 pane_id）

### P8 — 冲突检测
> 多 agent 同时改代码的必然问题

- [ ] 检测多个 agent 修改同一文件
- [ ] 弹通知提醒
- [ ] 简单 diff 合并界面

## 技术栈

| 层 | 技术 |
|----|------|
| 前端 | React + Vite + TailwindCSS |
| 编辑器 | code-server (VS Code) |
| 终端 | gotty (魔改) + xterm.js |
| 后端 | Go (ttyd-manager) |
| 终端管理 | tmux |
| 数据库 | MySQL + Redis |
| 桌面 | Electron |

### P9 — 分布式多设备 Agent 网络
- GCP 为中心调度节点，各设备为执行节点
- Mac: ssh + tmux 远程控制，可跑 Xcode/Swift 编译
- Windows: ssh + Electron MCP + GUI Master，可跑 Visual Studio/.NET
- iPad/iPhone: 浏览器 + 语音输入，frp 穿透回 GCP
- Android: Termux + frp，轻量执行节点
- 设备注册/发现：agent 启动时自动注册到 GCP，上报能力（OS/GPU/SDK）
- Master 按任务需求自动选设备：iOS 编译 → Mac，GPU 推理 → Windows，文档整理 → 任意
- 统一 agent 协议：不管设备在哪，master 用同一套指令控制

## 竞品对比

| 功能 | Cursor | Windsurf | 本产品 |
|------|--------|----------|--------|
| 代码编辑 | ✓ | ✓ | ✓ (code-server) |
| 单 AI 对话 | ✓ | ✓ | ✓ |
| 多 Agent 并行 | ✗ | ✗ | ✓ |
| 多 AI 支持 | ✗ (仅自家) | ✗ (仅自家) | ✓ (Kiro/Claude/Codex/OpenCode/Copilot) |
| Agent 状态监控 | ✗ | ✗ | ✓ |
| 任务分发/编排 | ✗ | ✗ | 计划中 |
| Token 成本控制 | ✗ | ✗ | 计划中 |
| 语音输入 | ✗ | ✗ | ✓ |
| TG 远程控制 | ✗ | ✗ | 计划中 |
| 多设备分布式控制 | ✗ | ✗ | ✓ (Mac/Win/iPad/Android) |
