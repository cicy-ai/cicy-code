---
title: 架构
description: Go 后端 api/mgr + 终端层 + React 前端 + 独立的 Cloudflare Workers。
---
# 架构

Go 后端 + 终端层 + React 前端 + 独立的 Cloudflare Workers。

## 后端 `api/mgr`

`main.go` 注册全部路由与启动。关键文件:

- `setup.go` —— 环境检查、内置 worker、开机 seed(dotfiles / 记忆模板 / 内置 skill)
- `tmux.go` —— pane 生命周期、tmux send、boot.sh、fork
- `chatbus.go` —— 聊天 WebSocket、poll、广播
- `ai_gateway_*.go` / `providers*.go` —— provider 适配与 AI 网关

## 前端 · 终端层

- **终端层**:`api/server`(HTTP/WS)、`api/webtty`(协议)、`api/js`(浏览器资产)
- **前端 app**:`App.tsx` 路由;`Workspace.tsx` 主工作区(团队 / 对话 / Todo / WS)

## Cloudflare Workers

| Worker | 域名 / 作用 |
| --- | --- |
| `oauth-flow` | oauth-flow.cicy-ai.com · Google OAuth 无状态中继 |
| `skills-registry` | skills.cicy-ai.com · public skill 注册表 API |
