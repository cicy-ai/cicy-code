---
title: Skill 能力
description: skill 是给 agent 按需装能力的机制 —— 浏览器控制、SSH、邮件、代理…… public / private / team 三类。
---
# Skill 能力

**Skill** 是给 agent「按需装能力」的机制。一个 skill 通常是一个零依赖 CLI(+ `SKILL.md` 规范文档),装上后 agent 就能调用它完成某类任务:驱动浏览器、连 SSH、发邮件、管代理、查知识库……

## 装 / 卸 / 列表

```bash
cicy-code skill install <name>     # 装
cicy-code skill remove <name>      # 卸
cicy-code skill installed          # 已装列表
cicy-code skill update --all       # 全部更新
```

前端也有 **skill 市场**(`/api/skill-market/*`),可视化装/卸;有 public skill 落后时 `btn-skill` 会亮**红点**提示更新。

## 三类 skill

| 类 | 装到 | 来源 |
| --- | --- | --- |
| **public** | `~/cicy-ai/skills/<name>/` | `skills.cicy-ai.com`(公共库) |
| **private** | `~/cicy-ai/skills/private/<name>/` | 你自己的本地库(localhost) |
| **team** | `~/cicy-ai/skills/team/<team>/<name>/` | 别的团队的私有库 |

**装到哪由来源决定**,不是你选。规范见 [三类 skill](/skills/kinds) 与 `cicy-skill-spec`。

## 相关

- 概览与安装 → [概览与安装](/skills/overview)
- 写自己的 skill → [写自己的 skill](/skills/authoring)
