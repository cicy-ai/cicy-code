---
title: 记忆与模板
description: 每个 agent 的 guidance 文件由 global + 项目 + 角色三层模板在创建时组装写入 —— 无继承、无网关注入,项目记忆天然隔离。
---
# 记忆与模板

cicy-code 的记忆系统是它「多 agent 复用 + 项目隔离」的核心。

## 每个 agent 一份自包含 guidance

每个 agent 在它的 workspace 里拥有**一份自包含的原生 guidance 文件**:

- `claude` → `CLAUDE.md`
- `codex` / `opencode` → `AGENTS.md`
- `kiro` → `.kiro/steering/memory.md`

CLI **原生读取**这个文件 —— **没有继承、没有网关注入**。gateway 和非 gateway 路径因此完全一致。

## 三层模板,创建时组装

guidance 的内容在 **agent 创建时**由分层模板**组装并逐字写入**(`agent_memory_template.go` 组装,`tmux.go` 的 `writeAgentGuidanceFile` 落盘):

```
global   ~/cicy-ai/memory/global.md        （总是）基础规则
  +
project  ~/cicy-ai/memory/projects/<slug>.md（可选）项目规则
  +
role     ~/cicy-ai/memory/agents/<slug>/    （可选）角色人设
```

- **global**:所有 agent 共享的基础规则(协作、知识库、约束…);
- **project**:某个项目的规则/技术栈/约束;
- **role**:某个职能的人设(前端/后端/测试…),含 `role.md`(人设)、`system.md`(系统提示)、`meta.yaml`(名字/工具/greeting)。

## 为什么这样就隔离了

因为组装发生在**创建时**、写进**各自的 workspace**:

- 同一个「前端」角色,在**项目 A** 和**项目 B** 各建一个 agent → 两个**独立 workspace、独立 CLAUDE.md(带各自项目模板)、独立对话历史** → 天然隔离,不串味;
- 复用来自**角色模板**,隔离来自**项目模板 + 独立 workspace**。

这正是 [项目与角色模板](/guides/templates) 那条落地路径的底座。

## 打包默认 seed

新机首次启动时,cicy-code 会把打包内置的默认模板 seed 到 `~/cicy-ai/memory/`(仅当文件缺失,不覆盖你的编辑)。**默认 seed 的唯一来源是 `api/mgr/embed/memory-seed/`**(`global.md` + `projects/default.md` + `agents/<role>/`)—— 改这里 = 改发给所有人的默认。

## reseed:按当前模板重新生成

改了模板想让**已存在**的 agent 也更新:

```bash
cicy-code reseed-memory --all          # 或 --ids w-1,w-2 / --offline
cicy-code reseed-memory --all --dry-run
```

它按当前模板重新生成各 agent 的 guidance,**会先备份**到 `<ws>/.cicy/memory-backups/`,并**保留自定义标记以下的内容**。

::: warning 注意
reseed 是「按当前模板整份重生成」。如果你手改过某个 agent 的 guidance 顶部(模板区),reseed 会覆盖它 —— 自定义内容请放在自定义标记之下,或改模板而不是改单个文件。
:::

## 延伸

- 网关**不注入记忆**,agent 原生读 guidance —— 见 [本地 AI 网关](/advanced/gateway);
- 记忆是 per-agent 的私有 guidance;全团队共享的可召回事实在 [团队知识库](/guides/knowledge);
- 本页说的是**创建时组装的静态模板**。cicy agent 还有一套**运行时自动沉淀的长期记忆**(记忆养成),见 [cicy Agent](/concepts/cicy-agent#记忆养成)。
