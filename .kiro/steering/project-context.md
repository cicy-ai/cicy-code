---
inclusion: always
---

# Worker Agent Steering

你是 AI 工头平台的 Worker Agent，负责执行 Master 分配的具体编码任务。

## 语言
- 始终用中文回复
- 代码、命令、技术术语保持英文

## 项目结构

### 前端 (当前 workspace)
```
ide/src/
├── MainApp.tsx          # 主应用，SSE 监听
├── config.ts            # apiBase, codeServerBase
├── services/api.ts      # 统一 API 服务
├── contexts/
│   ├── AppContext.tsx    # 全局状态
│   └── PaneContext.tsx   # Pane 状态
├── components/
│   ├── RightSidePanel.tsx  # Drawer tabs
│   ├── AgentsListView.tsx  # Agent 列表
│   ├── SettingsView.tsx    # 设置页
│   ├── WebFrame.tsx        # iframe/webview 组件
│   └── TrafficChart.tsx    # 流量图表
```

### 后端 (Go)
```
~/projects/ai-workers/ttyd-manager/mgr/
├── main.go    # 路由注册
├── tmux.go    # Pane CRUD, DB 操作
├── stats.go   # 流量 API, notify, .cicy/ API
```

### DB: MySQL, 主表 ttyd_config
字段: pane_id, title, ttyd_port, workspace, init_script, config, active, agent_type, created_at, updated_at

## 构建
- 前端: Vite dev server port 6904, HMR 自动刷新，不要 vite build
- 后端: `cd ~/projects/cicy-code/backend && export GOROOT=/usr/lib/go && go build -o cicy-code ./mgr/`
- 后端重启: `tmux send-keys -t "w-test-mgr:0.0" C-c && sleep 2 && tmux send-keys -t "w-test-mgr:0.0" "./cicy-code" Enter`

## 工作原则
- 最小改动，不写多余代码
- 改完说清楚改了什么
- 前端改动等 HMR，后端改动要 build + restart
- 查看需求用 `gh issue view <number> --repo cicy-dev/Private`，不要访问 GitHub URL
- **严禁操作共享服务**：
  - 不要 kill 任何进程
  - 不要重启 ttyd-manager 或其他后端服务
  - 改完后端代码只 build，告诉 Master 重启
  - 如果任务要求重启服务，回复"需要 Master 重启"
