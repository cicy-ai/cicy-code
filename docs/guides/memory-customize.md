---
title: 定制记忆
description: 改 global / 项目 / 角色模板,给已有 agent reseed,私有 seed 不进包。
---
# 定制记忆

## 改哪一层

| 想改… | 改这个文件 | 影响 |
| --- | --- | --- |
| 所有 agent 的基础规则 | `~/cicy-ai/memory/global.md` | 之后新建的 agent |
| 某项目的规则 | `~/cicy-ai/memory/projects/<slug>.md` | 该项目下的 agent |
| 某职能的人设/工具 | `~/cicy-ai/memory/agents/<slug>/` | 用该角色的 agent |

::: warning 别改 repo 里的默认
运行时改 `~/cicy-ai/memory/*`(不进包、seed 只在缺失时写、不覆盖你的编辑)。**别去改 repo 的 `embed/memory-seed/`** —— 那是发给所有人的默认。
:::

## 让已有 agent 也更新

改完模板,给现有 agent 重新组装 guidance:

```bash
cicy-code reseed-memory --all --dry-run   # 先预览
cicy-code reseed-memory --all             # 执行(自动备份)
```

## 只想 patch 一两段(不整份重生成)

reseed 是整份按模板重生成。如果只想给现有 agent 追加一小段(而不动其余),直接对各 workspace 的 `CLAUDE.md` / `AGENTS.md` **追加**即可 —— 是活文件、幂等追加不影响原内容。

## 私有 seed 不进包

想让自定义默认 seed 到新机、又不进公共包:把 repo 的默认 seed 目录软链到 `~/cicy-ai/memory-seed`(dev 机上 setup 会自动建),编辑那里,不碰 `embed/memory-seed`。
