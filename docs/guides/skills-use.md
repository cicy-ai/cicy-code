---
title: 装用 skill
description: 从 skill 市场给 agent 装能力,更新、卸载,红点提示。
---
# 装用 skill

## 命令行

```bash
cicy-code skill list                # 市场列表
cicy-code skill search <query>
cicy-code skill info <name>
cicy-code skill install <name>[@<version>]
cicy-code skill update <name>       # 或 update --all
cicy-code skill remove <name>
cicy-code skill installed
```

## 前端市场

工作区侧栏 `btn-skill` 打开 **skill 市场**(`/api/skill-market/*`):可视化装/卸/更新。**有 public skill 落后时按钮会亮红点**;更新后(CLI 或 UI)下一次请求即消。

## 强制更新

装过的版本被 `installed.json` 钉住,增量更新会跳过。强制更新:

```bash
cicy-code skill remove <name> && cicy-code skill install <name>
```

## 相关

- skill 是什么 / 三类 → [Skill 生态](/skills/overview)
