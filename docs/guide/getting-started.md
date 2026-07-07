---
title: 快速开始
description: 5 分钟跑起 cicy-code —— 安装、打开工作区、建第一个 agent、派第一个活、fork 分身。
---
# 快速开始

从零到指挥一支 agent 团队,大约 5 分钟。

## 1 · 安装并启动

```bash
npx cicy-code
```

首次会拉取匹配当前平台的二进制(~30 MB),然后在本机 `8008` 起服务。详见 [下载与安装](/guide/download)。

## 2 · 打开工作区

浏览器打开:

```
http://127.0.0.1:8008
```

你会看到**团队面板**(左)+ **对话/终端**(右)。默认已有一个内置 agent `w-1001`。

| 服务 | 默认端口 |
| --- | --- |
| API / 工作区 | `8008` |
| Vite(仅 dev) | `8022` |

## 3 · 建第一个 agent

在团队面板点「+」新建一个 agent,选:

- **类型**:`claude` / `codex` / `opencode`(非 lab 模式默认这三种)+ 内置 `cicy`;
- **角色模板**(可选):没有就用默认 `assistant`;
- **项目模板**(可选):决定它带哪个项目的规则。

创建时,cicy-code 会把 `global + 项目 + 角色` 组装成这个 agent 的 `CLAUDE.md` / `AGENTS.md`,**逐字写进它自己的 workspace**。详见 [记忆与模板](/concepts/memory)。

## 4 · 派第一个活

选中 agent,在对话框发指令。它在自己的 pane 里跑对应 CLI(claude / codex / …),你能实时看到终端输出。

跨 agent 协作与任务清单:

```bash
cicy-agent msg <agent> "<一句指路>"   # 给别的 agent 派活/提醒
cicy-todo add "<详细任务说明>"          # 详细任务写进 todo 跟踪
```

详见 [派活与任务](/guides/tasks) 和 [跨 agent 协作](/guides/collaboration)。

## 5 · fork 一个分身

需要在现有 agent 的上下文上再开一路(比如"接着补 e2e"):

```bash
cicy-agent fork <agent>
```

fork 会带上源 agent 的**完整对话上下文**,并继承它的**角色 + 项目**,然后独立继续。详见 [Fork 分身](/concepts/fork)。

## 下一步

- 复用团队模板 + 项目记忆独立 → [项目与角色模板](/guides/templates)
- 给 agent 装能力 → [装用 skill](/guides/skills-use)
