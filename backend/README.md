# ttyd-manager

基于 ttyd-go 的多实例管理器，替代 fast-api + ttyd-proxy。

## 架构

```
tmux-app (前端)
    ↓ WebSocket + HTTP
ttyd-manager (Go 管理器)
    ↓ 启动/管理
多个 ttyd-go 实例 (每个 pane 一个)
    ↓ tmux attach
Tmux Sessions
```

## 功能

- **HTTP API**: Pane CRUD (list, create, restart)
- **WebSocket 代理**: 路由到对应 ttyd-go 实例
- **实例管理**: 自动启动/停止 ttyd-go 进程
- **端口池**: 15100-15300 自动分配
- **MySQL 存储**: pane 配置持久化

## 使用

```bash
# 启动管理器
export MYSQL_DSN="root:@tcp(localhost:3306)/ai_workers"
./ttyd-manager

# API 调用
TOKEN=$(jq -r '.api_token' ~/global.json)

# 列出 panes
curl -H "Authorization: Bearer $TOKEN" http://localhost:14444/api/tmux/panes

# 创建 pane
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"win_name":"w-20001"}' \
  http://localhost:14444/api/tmux/create

# 重启 pane
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:14444/api/tmux/panes/w-20001/restart

# WebSocket 连接
ws://localhost:14444/api/ws/w-20001
```

## 编译

```bash
go build -o ttyd-manager manager.go
```

## 环境变量

- `PORT`: HTTP 端口 (默认 14444)
- `MYSQL_DSN`: MySQL 连接串 (默认 root:@tcp(localhost:3306)/ai_workers)

## 对比原架构

| 维度 | 原架构 | 新架构 |
|------|--------|--------|
| 进程 | fast-api + ttyd-proxy + N×ttyd | ttyd-manager + N×ttyd-go |
| 语言 | Python + Node.js + C | Go + Go |
| 内存 | ~250MB | ~50MB |
| 启动 | ~3s | ~0.5s |
| 部署 | 3 个服务 | 1 个二进制 |
