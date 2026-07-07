---
title: 本地 AI 网关
description: cicy-code 内置的 AI 网关如何把 agent 统一路由到 Claude / Codex / OpenAI / Anthropic / Gemini,按 agent 记账。
---
# 本地 AI 网关

cicy-code 内置一层 **AI 网关**:把 agent 的模型调用统一收拢,由本地网关转发到上游 provider,并**按 agent 身份记账、审计、可切换 provider**。

## 它解决什么

- **统一入口**:不同 agent(claude / codex / opencode)、不同上游(anthropic / openai / gemini)走一个网关,协议兼容;
- **按 agent 记账**:每次模型调用挂到发起它的 agent(用量、成本、conversation_id);
- **可切 provider / model**:通过 `runtime_ai` override 决定网关背后实际走哪个 provider 与模型,不用改 agent 本身;
- **和审计打通**:配合 [MITM](/advanced/mitm) + [审计策略](/advanced/audit),每一条 AI 流量都可见、可管。

## 关键端点 / 文件

| 位置 | 作用 |
| --- | --- |
| `/api/providers`、`/api/providers/…` | provider 配置(key / base URL / 默认) |
| `ai_gateway*.go` | 网关转发、余额闸、用量 |
| `agent_inspector.go` | 出站请求改写(模型 override + Anthropic system 规范化)——**不再注入记忆** |
| `/api/openclaw/*` | OpenClaw(IM agent)专属网关 |

::: tip 网关不改写系统提示注入记忆
网关**不再**把记忆/规则注入进出站请求的 system —— agent 直接原生读自己的 guidance 文件([记忆与模板](/concepts/memory)),保证网关 / 非网关路径一致。`agentInspectorRewriteRequestBody` 只做模型 override + Anthropic system 规范化。
:::

## runtime_ai override

每个 agent 的 `config.runtime_ai` 决定网关背后走哪个 provider/模型。fork 会**继承源的 runtime_ai**(否则会掉回全局默认、boot 出别的模型 —— "fork 不是 opus" 那个坑)。

下一页讲**同一个 agent,网关模式 vs 非网关模式怎么启动、差在哪** → [网关 vs 非网关启动](/advanced/gateway-modes)。
