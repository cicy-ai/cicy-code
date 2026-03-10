# cicy-code

AI Agent 协作开发平台 — 让一个人同时指挥多个 AI Agent 并行干活的 IDE。

## 架构

```
cicy-code/
├── ide/          # React + Vite 前端
├── backend/      # Go 后端 (ttyd-manager)
├── docs/         # 文档
└── docker-compose.yml
```

## 快速启动

```bash
# 生产模式
make prod
# 访问: http://localhost:8080

# 开发模式 (ide hot-reload)
make dev
# IDE: http://localhost:6903
# API: http://localhost:14444

# 停止
make stop
```

## 技术栈

| 层 | 技术 |
|----|------|
| 前端 | React 19 + Vite + TailwindCSS |
| 后端 | Go (ttyd-manager + 嵌入 gotty) |
| 终端 | xterm.js + WebSocket |
| 数据库 | MySQL 8 |
| 部署 | Docker Compose |
