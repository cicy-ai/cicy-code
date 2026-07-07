---
title: skill 生态
description: 给 agent 按需装能力 —— 浏览器控制、SSH、邮件、代理;public / private / team 三类规范。
---
# skill 生态

给 agent 按需装能力:浏览器控制、SSH、邮件、代理……

## 装 / 卸 / 列表

```bash
cicy-code skill install <name>
cicy-code skill remove <name>
cicy-code skill installed
```

## 注册表与规范

- public 注册表:`skills.cicy-ai.com`(`workers/skills-registry`),源码在独立仓库 `cicy-skills`
- 三类 skill(public / private / team)的落盘与发布约定见 `cicy-skill-spec`
- 前端市场入口 `/api/skill-market/*`,有更新会亮红点
