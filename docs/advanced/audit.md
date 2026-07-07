---
title: 审计策略
description: AI 流量审计管线 —— MITM 喂入事件,按策略 triage,cicy-audit-policy 调策略与 allowlist,决策可解释/可回滚。
---
# 审计策略

审计系统把每一条 AI 流量变成**可查、可管、可回滚**的证据链。

## 数据从哪来

[MITM](/advanced/mitm) 的 `audit_hook` 把每条(解密后的)AI 请求/响应喂进审计管线,按 agent 身份记录:模型、token、成本、conversation、命中的规则。

## 端点

| 端点 | 作用 |
| --- | --- |
| `/api/audit/events`、`/events/:id` | 审计事件流 / 单条 |
| `/api/audit/triage` | 分诊(噪声规则 / 惯犯) |
| `/api/audit/snapshot` | 快照 |
| `/api/audit/stats` | 统计 |
| `/api/audit/decisions*` | 自治决策(run / explain / revert) |

## 用 cicy-audit-policy 调策略

`cicy-audit-policy` skill 是**审计策略专员**的工作台(纯 shell + skill,无内置 audit_* 工具):

- 查/收紧/放松/回滚规则,调误报;
- `allowlist-add <sha256>` 按**内容 sha256** 放行(前缀 `sha256:` 可省);
- 改完 recompute hash,**fsnotify 热重载**运行中的管线(~200ms),不用重启;
- 读 events / snapshots / stats 来裁决命中。

::: tip 决策可解释、可回滚
`/api/audit/decisions/explain/…` 解释某条决策为何命中;`/revert/…` 回滚。审计日志和对话同一信任域 —— 是证据,别外泄。手改**不自动 commit**。
:::

## 和网关/记账的关系

- **网关**(/advanced/gateway):怎么转发 + 按 agent 记账;
- **MITM**(/advanced/mitm):按 agent 看见全部流量 + 可当场拦;
- **审计**(本页):把流量沉成事件、按策略分诊、可回滚 —— 三者合起来构成 cicy 的 AI 流量治理。
