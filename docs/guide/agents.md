---
title: agent · pane · 记忆
description: pane 是核心运行单位;记忆按项目隔离、角色可复用;模板在创建时组装写入。
---
# agent · pane · 记忆

pane 是核心运行单位;记忆按项目隔离,角色可复用。

## pane 与内置 agent

pane ID 形如 `w-1001` / `w-1001:main.0`。内置 agent(`api/mgr/setup.go`):`claude` / `codex` / `opencode` / `kiro-cli` / `cicy` 等;**非 lab 模式默认只暴露 claude / codex / opencode**。

## 记忆 / guidance 文件

每个 agent 在其 workspace 有一份自包含的原生 guidance 文件(`CLAUDE.md` / `AGENTS.md`)。内容在**创建时**由分层模板组装并逐字写入:

- `global` —— `~/cicy-ai/memory/global.md`
- 可选 `项目` —— `projects/<slug>.md`
- 可选 `角色` —— `agents/<slug>/`

**没有继承、没有网关注入**,CLI 直接原生读取。打包默认 seed 在 `api/mgr/embed/memory-seed/`;`cicy-code reseed-memory` 可按当前模板重新生成(会备份)。
