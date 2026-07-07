---
title: 浏览器 / 桌面控制
description: 让 agent 驱动系统 Chrome(agent-chrome)或 Electron 沙箱窗口(agent-electron),含 per-profile 代理。
---
# 浏览器 / 桌面控制

cicy-code 能让 agent 驱动真实浏览器 —— 联调、抓数据、跑网页任务。

## agent-chrome:系统 Chrome

驱动宿主机上的系统 Chrome,每个 profile 独立 `user-data-dir` + 独立代理:

```bash
agent-chrome profiles                 # 列 profile
agent-chrome proxy <idx> <url>        # 给 profile 设代理(配 cicy-mihomo 监听端口)
agent-chrome launch <idx> [--url <u>] # 起某 profile 的 Chrome
agent-chrome cdp <method> [params] --idx <n>   # 原始 CDP 调用
agent-chrome ip <idx>                 # 查该 profile 的出口 IP/地区
```

## agent-electron:Electron 沙箱窗口

驱动 cicy-desktop 的 Electron BrowserWindow;每个 `accountIdx` = 一个 `persist:sandbox-N` 会话(独立 cookie/存储/代理):

```bash
agent-electron sessions               # 活动窗口
agent-electron proxy <idx> <url>      # 会话级代理(setProxy)
agent-electron tab-open <idx> [url]   # 开 tab
agent-electron tab-eval <wcId> "<js>" # 在 tab 里执行 JS
agent-electron snapshot <winId>       # DOM 快照(优先于截图)
```

::: tip 代理
`*.trycloudflare.com`、`claude.ai` 等对某些出口有 SNI/区域限制 —— 给 profile/会话配一个能到的代理(cicy-mihomo 监听),再打开目标页。
:::
