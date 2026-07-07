---
title: 跨 agent 协作
description: cicy-agent 的 ls / msg / capture / fork / broadcast —— 多 agent 协作的命令集。
---
# 跨 agent 协作

`cicy-agent` 是多 agent 协作的核心 CLI(`cicy-agent help` 看全部)。

```bash
cicy-agent ls                       # 简短列表
cicy-agent get_online_agents        # 准确的在线名单(有活 tmux 会话)
cicy-agent msg <agent> "<text>"     # 派活/提醒(仅指路;细节进 cicy-todo)
cicy-agent msg <agent> "..." --notify   # 承接方做完时唤醒发送方(协作原语)
cicy-agent capture <agent>          # 看某 agent 当前画面
cicy-agent fork <agent>             # 带上下文分身
cicy-agent broadcast "<text>"       # 只发给在线 agent
```

::: warning ls ≠ 全在线
`cicy-agent ls` 是 online ∪ offline 的全量列表;要「真正在线」用 `get_online_agents`。群发用 `broadcast`(自动跳过 offline),别手动 loop `ls`。
:::

## 跨团队

注册别的团队后可跨团队 msg / reply / capture。团队注册/探活见 `cicy-agent team ...`。

## 相关

- 任务怎么跟踪 → [派活与任务](/guides/tasks)
- master/worker 结构 → [团队与协作](/concepts/teams)
