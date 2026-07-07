---
title: 概览与安装
description: skill 是什么、装/卸/更新、市场与红点。
---
# 概览与安装

**skill** = 给 agent 装能力的可复用单元(通常一个零依赖 CLI + `SKILL.md`)。

```bash
cicy-code skill list                 # 市场列表
cicy-code skill info <name>          # 详情
cicy-code skill install <name>[@ver] # 装
cicy-code skill update --all         # 全部更新
cicy-code skill remove <name>        # 卸
cicy-code skill installed            # 已装
```

装好的 skill 会 link 到 `~/.local/bin/`、`~/cicy-ai/skills/<name>/` 等;agent 启动时按当前已装列表生成它的 SKILL.md。前端 skill 市场(`/api/skill-market/*`)提供可视化装/卸,有更新亮红点。

运行时 secrets 放 `~/cicy-ai/db/<name>.json` 或 `~/cicy-ai/global.json` —— **不要提交进 skill**。
