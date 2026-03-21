# cicy-code

AI Agent 协作开发平台 — 让一个人同时指挥多个 AI Agent 并行干活的移动 AI 工具包。

## 架构

```
cicy-code/
├── app/          # React + Vite 前端 (~4500 行 TypeScript)
├── api/          # Go 后端 (ttyd-manager), 单 binary 编译
│   └── mgr/      # 主程序 + 嵌入资源 (inject HTML, tmux.conf, UI, monitor)
├── mitmproxy/    # mitmproxy monitor 脚本（构建时嵌入 binary）
├── build.sh      # 统一构建脚本（prepare embed → go build → cleanup）
├── scripts/      # Supervisor 配置 + 部署脚本
├── docs/         # 文档
└── docker-compose.yml
```

## 快速启动

```bash
# 一键安装运行（推荐）
npx cicy-code

# 访问
open http://localhost:18008/?token=$(jq -r '.api_token' ~/global.json)
```

### 开发模式

```bash
# 前端开发
cd app && npm run dev    # http://localhost:6902

# 后端开发（从文件系统加载资源，支持热重载）
cd api && go run ./mgr/ --dev   # http://localhost:18008

# 构建（单平台，默认 linux/amd64）
./build.sh build
# 或
make build

# 交叉编译 linux+darwin 全平台
./build.sh all
```

### 依赖

- Go 1.25+
- Node.js 20+
- tmux
- SQLite（本地模式，自动创建，migration 内置于 binary）/ MySQL + Redis（SaaS 模式）

> **⚠️ Windows 用户**：不提供 Windows 二进制，请通过 [WSL2](https://learn.microsoft.com/en-us/windows/wsl/install) 运行 linux-amd64 版本。详见 [本地部署指南](docs/local-deploy.md#windows-通过-wsl2)

## 预置 AI Agent

7 大内置 Agent，固定端口 10001-10007：

| 端口 | Agent | 说明 | 必装 |
|------|-------|------|------|
| 10001 | Kiro CLI | 多功能 AI 助手 | ✅ |
| 10002 | Claude Code | Anthropic 代码助手 | |
| 10003 | GitHub Copilot CLI | GitHub AI 助手 | |
| 10004 | Gemini CLI | Google AI 助手 | |
| 10005 | OpenAI Codex | 代码生成助手 | |
| 10006 | OpenCode | 开源代码助手 | ✅ |
| 10007 | Cursor Agent | Cursor AI 助手 | |

首次安装时选择需要的 Agent，之后启动自动恢复，不重复创建。用户自建 Worker 从端口 20001+ 动态分配。

## 端口说明

| 服务 | 端口 | 说明 |
|------|------|------|
| API | 8008 | 主服务，含嵌入式管理 UI |
| code-server | 8002 | 代码编辑器 |
| Vite 前端 | 8001 | 前端开发服务 |
| 内置 Agent | 10001-10007 | 7 大 Agent 终端 (ttyd) |
| 用户 Worker | 20001+ | 用户自建 Worker，动态分配 |

## 技术栈

| 层 | 技术 |
|----|------|
| 前端 | React 19 + Vite + TailwindCSS |
| 编辑器 | code-server (VS Code) |
| 终端 | ttyd-go (内嵌) + WebFrame (Electron webview / iframe) |
| 后端 | Go (单 binary, `//go:embed` 资源内嵌) |
| 终端管理 | tmux |
| 数据库 | SQLite (本地) / MySQL + Redis (SaaS) |
| 流量监控 | mitmproxy (审计模式，monitor 脚本嵌入 binary) |
| 桌面 | Electron (可选) |

## 前端结构

```
app/src/
├── main.tsx                    # Vite 入口
├── App.tsx                     # 根组件，hash 路由 Workspace/Desktop
├── config.ts                   # 动态 URL 配置（自动检测 workspace 模式）
├── types.ts                    # Position/Size/AppSettings 类型
├── index.css                   # Tailwind + VSCode 风格 CSS 变量
│
├── services/
│   ├── api.ts                  # axios 封装所有后端 API
│   ├── tokenManager.ts         # JWT token 生命周期管理
│   ├── paneManager.ts          # 当前选中 pane 缓存
│   └── mockApi.ts              # sendCommand/sendShortcut 快捷函数
│
├── lib/
│   ├── utils.ts                # cn() — clsx + tailwind-merge
│   ├── pointerLock.ts          # 防止 iframe 拖拽时抢 pointer
│   └── devStore.ts             # 全局调试 store
│
├── contexts/                   # 6 个 React Context Provider
│   ├── AppContext.tsx           # 全局状态：token/panes/agents/settings，5s 轮询
│   ├── PaneContext.tsx          # 当前 pane：布局/tab/agent 状态/网络延迟
│   ├── AuthContext.tsx          # OAuth + token 验证 + perms/plan
│   ├── VoiceContext.tsx         # 录音 → STT → 发送
│   ├── DialogContext.tsx        # 全局对话框管理
│   └── SendingContext.tsx       # 命令发送状态追踪
│
└── components/
    ├── Workspace.tsx            # 主工作区（左侧 Agent 列表 + 右侧终端/聊天）
    ├── Desktop.tsx              # AI 桌面模式（聊天创建应用）
    ├── WebFrame.tsx             # iframe/webview 统一封装（Electron webview + 浏览器 iframe）
    ├── Login.tsx                # Google/GitHub OAuth + Token 登录
    ├── FloatingPanel.tsx        # 通用可拖拽浮动面板
    ├── VoiceFloatingButton.tsx  # 按住录音松开发送
    ├── EditPaneDialog.tsx       # Pane 编辑对话框
    ├── SettingsView.tsx         # 嵌入式设置视图
    ├── ProvisionScreen.tsx      # 工作区部署进度（SSE）
    ├── ConfirmDialog.tsx        # 确认对话框
    ├── TerminalControls.tsx     # 终端控制按钮
    │
    ├── chat/
    │   └── ChatView.tsx         # AI 对话界面（Markdown + tool 汇总 + mini terminal）
    │
    ├── terminal/
    │   ├── CommandPanel.tsx      # 命令面板（历史/草稿/语音/模型选择）
    │   ├── CommandInput.tsx      # 命令输入框
    │   ├── TerminalFrame.tsx     # WebFrame 封装的 ttyd 终端
    │   └── WindowManager.tsx     # tmux 窗口管理
    │
    ├── layout/
    │   ├── DesktopCanvas.tsx     # 桌面应用画布
    │   ├── TeamPanel.tsx         # 团队面板（跨 workspace agent）
    │   ├── SettingsFloat.tsx     # 浮动设置面板
    │   └── useDesktopEvents.ts   # 桌面 SSE 事件监听
    │
    ├── desktop/
    │   └── useDesktopApps.ts     # 桌面应用状态 + openInElectron
    │
    ├── dev/
    │   └── DevPanel.tsx          # 开发调试面板
    │
    └── ui/
        └── Select.tsx            # 自定义下拉选择（带搜索）
```

## 后端 API

单 Go binary (ttyd-manager)，内嵌 ttyd-go，goroutine per pane。

- HTTP API: Pane CRUD / Agent 管理 / Chat 历史 / 设置 / 流量统计
- WebSocket 代理: 路由到对应 ttyd-go 实例
- SSE 实时推送: Agent 状态变更通知
- `//go:embed`: inject HTML、tmux.conf、管理 UI 全部编译进 binary

```bash
TOKEN=$(jq -r '.api_token' ~/global.json)
curl -H "Authorization: Bearer $TOKEN" http://localhost:18008/api/tmux/panes
```

## 部署

三种部署层级，详见 [部署架构文档](docs/deploy-architecture.md)：

| 层级 | 模式 | 适用 |
|------|------|------|
| 🏠 [本机部署](docs/local-deploy.md) | `npx cicy-code` | 开发者自用 |
| ☁️ Cloud Run 试用 | `--saas --public` | 新用户体验 |
| 🚀 PRO VM | 独占 VM + Supervisor | 付费订阅 |

### 进程管理

Supervisor 配置位于 `scripts/` 目录，通过符号链接部署：

```bash
sudo ln -sf $(pwd)/scripts/cicy-code.supervisor.conf /etc/supervisor/conf.d/cicy-code.conf
sudo supervisorctl reread && sudo supervisorctl update
```

## 文档索引

- [本地部署指南](docs/local-deploy.md) — 安装、Supervisor、launchd、`--dev` 模式
- [部署架构](docs/deploy-architecture.md) — 本机 / Cloud Run / PRO VM 三层对比
- [终端问题排查](docs/terminal-clear-error.md) — "terminal does not support clear" 修复
- [审计模式](docs/audit.md) — mitmproxy 流量审计
- [发版流程](docs/release.md) — Tag 触发 CI/CD
