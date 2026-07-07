---
title: Agent 与 Pane
description: agent 是一等公民 worker,每个住在自己的 tmux pane 里 —— 真进程、真终端、重启可恢复。
---
# Agent 与 Pane

## Agent = 一等公民 worker

在 cicy-code 里,一个 **agent** 就是一个常驻的编码 worker:它跑某个 CLI(`claude` / `codex` / `opencode`),有自己的身份(角色)、自己的记忆、自己的对话历史。你在统一界面里跟它聊天、派活、观察,而不是切多个终端。

**内置 agent 类型**(`api/mgr/setup.go` 的 `builtinAgents`):

| 类型 | 说明 |
| --- | --- |
| `claude` | Claude Code |
| `codex` | Codex |
| `opencode` | OpenCode |
| `cicy` | 内置 headless 会话(网关驱动,无独立终端 CLI) |

::: tip lab 模式
**非 lab 模式(默认)只暴露 `claude` / `codex` / `opencode` / `cicy`**;lab 模式才会放出更完整的候选集。默认 dev agent 是 `claude`。
:::

## Pane = agent 的真终端

每个 agent 住在一个 **tmux pane** 里 —— 一个真实的终端进程。这带来几个关键性质:

- **真进程、真终端**:CLI 就是在这个 pane 里真的跑着;
- **常驻**:后台一直在,不是一次性会话;
- **重启可恢复**:cicy-code 重启后 pane 依旧在(tmux server 独立存活)。

**pane ID** 形如:

```
w-1001          # 短 ID(agent id)
w-1001:main.0   # 完整 pane ID(session:window.pane)
```

`w-1001` 是首个内置 worker(团队里的 PM / 知识专员位),`primaryWorkerPaneID = w-1001:main.0`。

## boot.sh:pane 怎么起 agent

每个 agent 的 workspace 下有 `.cicy/boot.sh` —— pane 的 shell 启动后 source 它,检查 CLI 是否就绪、然后启动对应 agent(claude/codex/…)并带上 resume 状态。`cicy-code` 通过 tmux 的 `default-command`(`bash --rcfile ~/.cicy_shell_init`)保证每个 pane 都加载了 cicy 的 shell 环境。

## 相关

- 它的记忆从哪来 → [记忆与模板](/concepts/memory)
- 多个 agent 怎么组队协作 → [团队与协作](/concepts/teams)
