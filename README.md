# cicy-code

AI Agent 协作开发平台 — 让一个人同时指挥多个 AI Agent 并行干活的 IDE。

## 架构

```
cicy-code/
├── ide/          # React + Vite 前端
├── api/      # Go 后端 (ttyd-manager)
├── docs/         # 文档
└── docker-compose.yml
```

## 快速启动

```bash
# 前端开发
make dev-ide        # http://localhost:6902

# 后端开发
make dev-api    # http://localhost:14444

# 构建
make build          # 构建前后端
make build-api  # 只构建后端 → api/ttyd-manager
make build-ide      # 只构建前端 → ide/dist/
```

### 依赖

- Go 1.18+
- Node.js 20+
- MySQL
- tmux

## 技术栈

| 层 | 技术 |
|----|------|
| 前端 | React 19 + Vite + TailwindCSS |
| 后端 | Go (ttyd-manager + 嵌入 gotty) |
| 终端 | xterm.js + WebSocket |
| 数据库 | MySQL 8 |
| 部署 | Docker Compose |
