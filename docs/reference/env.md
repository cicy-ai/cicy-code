---
title: 环境变量
description: cicy-code 常用环境变量。
---
# 环境变量

| 变量 | 作用 |
| --- | --- |
| `PORT` | API 端口(默认 8008;`--port` 优先) |
| `SQLITE_PATH` | SQLite 数据库文件 |
| `CICY_CODE_PORT` | 桌面/外壳读取的 sidecar 端口(默认 8008) |
| `CICY_CFT_TOKEN` / `CICY_CFT_HOST` | named tunnel 连接 token / 报告域名 |
| `CICY_RUNTIME_MODE=api-only` | 关闭 tmux/desktop-only 接口 |
| `CICY_SKILLS_REGISTRY` | 覆盖 skill 注册表 URL(默认 skills.cicy-ai.com) |
| `CICY_SKILLS_REGISTRY_TOKEN` | 上面覆盖库的 bearer token |
| `CICY_NO_BROWSER` | 启动时不自动开浏览器 |
| `CICY_PPROF_PORT` | pprof 端口 |
| `IS_SANDBOX=1` | root 容器里允许 claude 的 `--dangerously-skip-permissions`(cicy 的 pane 已自动设) |
