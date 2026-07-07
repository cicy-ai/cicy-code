---
title: 派活与任务
description: 用 cicy-todo 管每个 workspace 的任务清单,配合 test/done 做完成→验收的交接。
---
# 派活与任务

## cicy-todo:每个 workspace 的任务清单

```bash
cicy-todo add "<详细任务说明>"   # 新任务(目标/验收标准/相关文件都写进去)
cicy-todo start <id>            # 开始
cicy-todo done <id>             # 完成
cicy-todo drop <id>             # 丢弃
```

对应前端的 **Todo tab**(同源数据,`/api/todo/*`)。

## 派发规范(推荐)

让**别的** agent 干活时,别把任务全文塞进 `cicy-agent msg`:

1. **`cicy-todo add`** 写一条**详细**任务说明(目标、验收标准、相关文件/上下文);
2. **`cicy-agent msg <agent>`** 只发 `todo id + 一句短 title`;
3. 承接方做完把该 todo 标 **`test`**;编排方安排测试/验收,过了标 **`done`**,不过打回。

> `test` 状态 = 完成方 → 验收方的交接信号。这样任务可跟踪、chat 不刷屏。
