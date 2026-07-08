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

## 追踪一次派发

`cicy-agent msgs` 把「谁 → 谁、状态、id」连同**双方**的 q→answer 摘要 JOIN 出来 —— 派出去的活到哪一步、对方做了什么,一眼看清:

```bash
$ cicy-agent msgs --from w-1001 --status done
# from   to      id  status  q ⟶ answer
# w-1001 w-1004  42  done    "接 todo a1b2… health 加 tunnel_url" ⟶ "已加字段+单测,标 test"
```

配合 `cicy-agent msg … --notify`(对方做完推一条唤醒),就不用手动 `capture` 轮询了。

## 跨团队

注册别的团队后可跨团队 msg / reply / capture。

## 相关

- 任务怎么跟踪 → [派活与任务](/guides/tasks)
- master/worker 结构 → [团队与协作](/concepts/teams)
