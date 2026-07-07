---
title: 三类 skill
description: public / private / team —— 装到哪由来源决定。
---
# 三类 skill

装到哪**由来源决定**,不是你选:

| 来源 | 例 | 装到 |
| --- | --- | --- |
| 公共库 | `skills.cicy-ai.com` | `~/cicy-ai/skills/<name>/` |
| 你的本地库 | `http://localhost:8787` | `~/cicy-ai/skills/private/<name>/` |
| 别的团队库 | `http://team-host:8787` | `~/cicy-ai/skills/team/<team>/<name>/` |

**优先级**:private/team 遮蔽同名 public;多个私有源,最后加入的胜;用 `<source>/<name>` 显式指定源。每个团队的 skill 在各自 `team/<team>/` 子树,同名不冲突。

权威规范见 `cicy-skill-spec`(`cicy-skill-spec spec` / `paths`)。
