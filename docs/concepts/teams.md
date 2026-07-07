---
title: 团队与协作
description: master / worker 结构、团队面板、cicy-agent 通信 —— 让多个 agent 组队干活。
---
# 团队与协作

## 团队面板

工作区左侧是**团队面板**:列出所有 agent,显示每个的角色、类型、在线状态、上下文占用、成本。点一个 agent 进它的对话/终端。fork 出来的 agent 会**嵌套**在源 agent 下,形成团队树。

## master / worker

agent 之间可以绑定 **master**(编排方)。默认 master 是 `w-1001`(PM / 调度位),这样 fork/新建的 agent 都挂在某个 master 下,在 UI 里可见、可管。

## cicy-agent:跨 agent 的协作原语

`cicy-agent` skill 是多 agent 协作的核心 CLI:

```bash
cicy-agent ls                    # 列出 agent
cicy-agent msg <agent> "<text>"  # 给某 agent 派活/提醒(仅指路,细节进 todo)
cicy-agent capture <agent>       # 看某 agent 的当前画面/进度
cicy-agent fork <agent>          # 带上下文分身
cicy-agent broadcast "<text>"    # 群发给所有在线 agent
```

::: tip 任务派发规范
**不要用 `cicy-agent msg` 发长任务全文。** 让别的 agent 干活时:① `cicy-todo add` 写详细任务说明;② `cicy-agent msg` 只发 `todo id + 一句短 title`;③ 承接方做完标 `test`,编排方验收后标 `done`。见 [派活与任务](/guides/tasks)。
:::

## 相关

- 任务清单与验收流 → [派活与任务](/guides/tasks)
- 具体协作命令 → [跨 agent 协作](/guides/collaboration)
