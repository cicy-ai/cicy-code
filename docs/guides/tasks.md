---
title: 派活与任务
description: 用 cicy-todo 管每个 workspace 的任务清单,配合 test/done 做「完成→验收」的交接,附端到端派活走查。
---
# 派活与任务

## cicy-todo:每个 workspace 的任务清单

所有 todo 存在 **master pane**(`w-1001`)workspace 下的一个 store(`<master-ws>/.cicy/todos.yaml`),每条盖上属主 worker 的 `pane_id`。**worker 只看得到/改得动自己的**;master 默认看全部,`--pane <w-xxxxx>` 可限定到某 worker。

```bash
cicy-todo add "<详细任务说明>"          # 新任务(目标/验收标准/相关文件都写进去)
cicy-todo list [--status=todo|test|done|dropped] [-q <kw>] [--all]
cicy-todo show <id-prefix>             # 看详情
cicy-todo test <id>                    # 编码完成、待验收
cicy-todo done <id>                    # 验收通过
cicy-todo drop <id>                    # 丢弃
cicy-todo back <id>                    # 打回,重回 todo
```

状态机:`todo → test → done`(或 `drop`;打回用 `back`)。对应前端的 **Todo tab**(同源数据)。

## 派发规范(推荐)

让**别的** agent 干活时,别把任务全文塞进 `cicy-agent msg` —— chat 会刷屏、任务也不可跟踪:

1. **`cicy-todo add`** 写一条**详细**任务说明(目标、验收标准、相关文件/上下文);
2. **`cicy-agent msg <agent>`** 只发 `todo id + 一句短 title` 指路;
3. 承接方做完把该 todo 标 **`test`**;编排方安排验收,过了标 **`done`**,不过 **`back`** 打回。

> `test` 状态 = 完成方 → 验收方的**交接信号**。任务可跟踪、chat 不刷屏。

## 端到端走查(master 给 worker 派一个活)

```bash
# 1) master 上:写一条详细 todo,挂到目标 worker w-1004 名下
$ cicy-todo add "给 /api/health 加 tunnel_url 字段并写单测" --pane w-1004
todo_id=a1b2c3d4

# 2) 只发一句指路(带 todo id),让 w-1004 去做
$ cicy-agent msg w-1004 "接 todo a1b2c3d4:health 加 tunnel_url + 测试" --notify
msg_id=42

# 3) w-1004 做完,自己标 test(编码完成、待验收)
#    （在 w-1004 的 shell 里)
$ cicy-todo test a1b2c3d4

# 4) master 看谁提交了验收
$ cicy-todo list --status=test
a1b2c3d4  w-1004  给 /api/health 加 tunnel_url 字段并写单测

# 5) 验收:过了标 done;不过打回
$ cicy-todo done a1b2c3d4      # 或:cicy-todo back a1b2c3d4
```

`--notify`(见 [跨 agent 协作](/guides/collaboration))会在 w-1004 这一轮结束时给发送方推一条 `🔔 … → done`,不用手动轮询。

## 相关

- 派活/协作命令全集 → [跨 agent 协作](/guides/collaboration)
- master/worker 结构 → [团队与协作](/concepts/teams)
