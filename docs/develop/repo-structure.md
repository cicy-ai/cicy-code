---
title: 仓库结构
description: cicy-code 仓库的顶层布局与主入口。
---
# 仓库结构

```
cicy-code/
├── app/          React + Vite 前端(App.tsx / Workspace.tsx)
├── api/          Go 后端 + 终端层
│   ├── mgr/      主程序与业务路由(main.go)
│   ├── server/   WebTTY HTTP/WebSocket
│   ├── webtty/   WebTTY 协议
│   ├── js/       浏览器端终端资产(改后 make asset)
│   └── skillcmd/ cicy-code skill 子命令
├── npm/          npm 发布包与启动器
├── scripts/      构建/发版/测试(sync-version.py 等)
├── workers/      Cloudflare Workers(oauth-flow / skills-registry)
├── docs/         本文档站(VitePress)
├── build.sh      标准构建入口
├── dev.py        本地开发入口
└── versions.json base 镜像 / app / ttyd 版本
```

| 用途 | 位置 |
| --- | --- |
| 后端主程序 | `api/mgr/main.go` |
| 前端主入口 | `app/src/App.tsx` |
| 主工作区 | `app/src/components/Workspace.tsx` |
| 版本号源头 | `npm/package.json` |
| 状态根 | `~/cicy-ai` |
