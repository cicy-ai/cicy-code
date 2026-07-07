---
title: 介绍
description: cicy-code 是什么 —— 本地优先的多 agent 开发工作区,Agent / Pane / 隔离记忆三块基石。
---
# 介绍

`cicy-code` 是**本地优先的多 agent 开发工作区**:把 Claude、Codex、OpenCode 编排成 tmux worker,收进同一个 React 工作区,通过 npm 分发单二进制。

::: tip part of CiCy AI
cicy-code 是 [CiCy AI](https://cicy-ai.com) 平台的一环 —— 你机器上那支能干活的 agent 团队。
:::

## 它是什么

不是一个 chatbot,而是一支能并行、能分工、互不串味的团队。三块基石:

- **Agent** —— Claude / Codex / OpenCode 都是一等公民 worker,同一界面里聊天、派活、看进度。
- **Pane** —— 每个 agent 住在自己的 tmux pane(`w-1001:main.0`),真进程、真终端,重启不丢。
- **隔离记忆** —— 每个 agent 一份自包含 guidance,由 `global + 项目 + 角色` 模板组装,换项目 = 换记忆。

## 一个仓库,一把梭

tmux worker、WebTTY 终端、React 工作区、AI 网关、skill 市场,全收在同一个仓库里,`npx cicy-code` 就跑。
