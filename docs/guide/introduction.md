---
title: 介绍
description: cicy-code 是本地优先的多 agent 开发工作区 —— 把 Claude / Codex / OpenCode 编排成 tmux worker,每个 agent 有自己的 pane、记忆与对话。
---
# 介绍

`cicy-code` 是**本地优先的多 agent 开发工作区**。它把 Claude、Codex、OpenCode 这些编码 CLI 编排成一支能并行、能分工、互不串味的团队,收进同一个 React 工作区,通过 npm 分发**单二进制**(`npx cicy-code` 即可)。

::: tip 一句话
不是「一个更好的 chatbot」,而是「你机器上一支能干活的 agent 团队 + 指挥它们的控制台」。
:::

## 为什么

单个编码 agent 已经很强,但真实项目里你往往需要**多个**:一个写前端、一个写后端、一个跑测试;它们要能**并行**、能**互相派活**、还不能把 A 项目的上下文串到 B 项目。手动开四个终端、复制粘贴上下文,既乱又易错。

cicy-code 把这套「多 agent 协作」做成一等能力:

- 每个 agent 是一个**常驻 worker**(真终端、真进程),不是一次性会话;
- 它们在**一个界面**里被指挥、被观察、被编排;
- 每个 agent 的**记忆按项目/角色隔离**,复用模板又互不干扰。

## 三块基石

| 概念 | 一句话 | 详见 |
| --- | --- | --- |
| **Agent** | Claude / Codex / OpenCode 都是一等公民 worker,你跟它们聊天、派活、看进度。 | [Agent 与 Pane](/concepts/agent-pane) |
| **Pane** | 每个 agent 住在自己的 tmux pane(`w-1001:main.0`),真进程、重启不丢、后台常驻。 | [Agent 与 Pane](/concepts/agent-pane) |
| **隔离记忆** | 每个 agent 一份自包含 guidance,由 `global + 项目 + 角色` 模板组装 —— 换项目 = 换记忆。 | [记忆与模板](/concepts/memory) |

## 一个仓库,一把梭

tmux worker、WebTTY 终端、React 工作区、AI 网关、skill 市场,全收在同一个仓库里:

- **终端在浏览器里**(WebTTY),直接看/操 agent 的真实会话;
- **AI 网关**把 agent 路由到 Claude / Codex / OpenCode 与各家 provider;
- **skill 市场**给 agent 按需装能力:浏览器控制、SSH、邮件、代理……

## 接下来

- 想马上跑起来 → [下载与安装](/guide/download) · [快速开始](/guide/getting-started)
- 想先懂心智模型 → [核心概念](/concepts/agent-pane)
- 想复用「前端/后端/测试」模板、且项目记忆独立 → [项目与角色模板](/guides/templates)
