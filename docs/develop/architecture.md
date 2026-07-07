---
title: 架构
description: Go 后端 api/mgr + 终端层 + React 前端 + Cloudflare Workers。
---
# 架构

## 后端 `api/mgr`

`main.go` 注册全部路由与启动。关键文件:

- `setup.go` —— 环境检查、内置 worker、开机 seed(dotfiles / 记忆模板 / 内置 skill)
- `tmux.go` —— pane 生命周期、tmux send、boot.sh、fork
- `chatbus.go` —— 聊天 WebSocket、poll、client 广播
- `agent_memory_template.go` —— 记忆模板组装
- `newtab.go` —— `/newtab` 单源浏览器起始页(chrome + electron 共用)
- `skills.go` / `skill_market*.go` —— skill 市场 API
- `ai_gateway_*.go` / `providers*.go` —— provider 适配与 AI 网关
- `ui.go` —— 内嵌 UI 或 Vite 反代;`paths.go` —— 状态根与路径常量

## 终端层

`api/server`(HTTP/WS)、`api/webtty`(协议)、`api/js`(浏览器端资产,改后 `make asset`)。

## 前端 `app`

`App.tsx` 路由;`Workspace.tsx` 主工作区(团队 / 对话 / code / Todo / WS 状态);`services/api.ts` 统一 API 客户端;`config.ts` 版本号与 API base。

## Cloudflare Workers

| Worker | 域名 / 作用 |
| --- | --- |
| `oauth-flow` | oauth-flow.cicy-ai.com · Google OAuth 无状态中继 |
| `skills-registry` | skills.cicy-ai.com · public skill 注册表 API |
