---
title: 配置与路径
description: cicy 状态根 ~/cicy-ai 的目录布局与敏感配置位置。
---
# 配置与路径

cicy 状态根 `~/cicy-ai`。

| 路径 | 作用 |
| --- | --- |
| `~/cicy-ai/global.json` | 全局配置 + api_token |
| `~/cicy-ai/db/` | 敏感配置(email/cf/frps/mihomo,chmod 600) |
| `~/cicy-ai/memory/` | 记忆模板(global.md / projects / agents) |
| `~/cicy-ai/workers/` | 各 agent workspace |
| `~/cicy-ai/skills/` | 已装 skill(public / private / team) |
| `~/cicy-ai/assets/` | 上传资源目录 |
