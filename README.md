# cicy-code

AI Agent 协作开发平台 — 让一个人同时指挥多个 AI Agent 并行干活的云端工作站。

## 架构

```
cicy-code/
├── app/          # React + Vite 前端 (~4500 行 TypeScript)
├── api/          # Go 后端 (ttyd-manager)
├── landing/      # 落地页 (CF Worker + Static Assets)
└── docker-compose.yml
```

## 快速启动

```bash
# 前端开发
cd app && npm run dev    # http://localhost:6902

# 后端开发
cd api && go run .       # http://localhost:8008

# 构建
make build               # 构建前后端
```

### 依赖

- Go 1.18+
- Node.js 20+
- MySQL + Redis
- tmux

## 技术栈

| 层 | 技术 |
|----|------|
| 前端 | React 19 + Vite + TailwindCSS |
| 编辑器 | code-server (VS Code) |
| 终端 | gotty (魔改) + WebFrame (Electron webview / iframe) |
| 后端 | Go (ttyd-manager) |
| 终端管理 | tmux |
| 数据库 | MySQL + Redis |
| 流量监控 | mitmproxy |
| 外网访问 | CF Tunnel / FRP |
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

单 Go binary (ttyd-manager)，嵌入 gotty，goroutine per pane。

- HTTP API: Pane CRUD / Agent 管理 / Chat 历史 / 设置 / 流量统计
- WebSocket 代理: 路由到对应 ttyd-go 实例
- SSE 实时推送: Agent 状态变更通知
- fsnotify: 实时监控 pane 状态写入 Redis

```bash
TOKEN=$(jq -r '.api_token' ~/global.json)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8008/api/tmux/panes
```

## 部署

见 [DEPLOY.md](DEPLOY.md)

## 路线图

见 [ROADMAP.md](ROADMAP.md)
