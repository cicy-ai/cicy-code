---
title: 创建与管理 agent
description: 在团队面板新建 agent、选类型/角色/项目,重启、fork、绑定 master。
---
# 创建与管理 agent

## 新建

团队面板点「+」,填:

- **类型**:`claude` / `codex` / `opencode` / `cicy`;
- **角色模板**(可选):决定人设 + 可用工具;缺省用 `assistant`;
- **项目模板**(可选):决定带哪个项目的规则;
- **语言**:中 / EN(影响组合进 guidance 的语言变体)。

提交后 cicy-code 会:建 tmux pane → 组装并写入 guidance(`global + 项目 + 角色`)→ boot.sh 启动对应 CLI。

## 管理

| 操作 | 怎么做 |
| --- | --- |
| 派活 | 选中 agent,在对话框发指令 |
| 看进度 | 直接看它的终端;或 `cicy-agent capture <id>` |
| 重启 | UI 的重启;或后端 `restart` —— kill + 重建 session(会走当前 `default-command`) |
| fork | `cicy-agent fork <id>`(带上下文分身) |
| 绑 master | 新建/ fork 时指定 master(默认 `w-1001`),UI 里嵌套显示 |

## 相关

- 复用职能模板 → [项目与角色模板](/guides/templates)
- 记忆怎么组装 → [记忆与模板](/concepts/memory)
