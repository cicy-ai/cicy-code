---
title: 网关 vs 非网关启动
description: claude / codex 以网关模式(use_custom_gateway)或官方登录(非网关)启动的区别、各自怎么走。
---
# 网关 vs 非网关启动

每个 agent 有一个开关 **`use_custom_gateway`**(`agent_config`,新建默认 **开**)。它决定 claude / codex 这类 CLI 启动时**指向本地网关**还是**用官方登录**。

## 两种模式

| | 网关模式(默认) | 非网关模式(官方登录) |
| --- | --- | --- |
| `use_custom_gateway` | `true` | `false` |
| 上游端点 | 指向 **cicy 本地网关** | provider **官方端点** |
| 鉴权 | 团队网关 key / 本地 token | CLI 自己的登录 / 订阅 |
| provider / model | 由 `runtime_ai` override 决定 | CLI 自己的账号决定,**忽略**网关/model 设置 |
| 记账 / 审计 | ✅ 按 agent 统一记账 | 不经网关(仅 MITM 流量审计仍可开) |
| 典型用途 | 团队统一供给、计费、切模型 | 你已有 Claude/Codex 订阅、直接用官方额度 |

## 网关模式怎么起

boot 时把 CLI 的上游 base URL 指向本地网关(如 `ANTHROPIC_BASE_URL` / codex 的对应 base 指向 cicy 网关),鉴权用本地/团队 key;网关按 `runtime_ai` 选 provider/model,转发到真正的上游,并记账 + 审计。

## 非网关模式怎么起

不改上游端点,CLI 走它自己的官方登录(claude login / codex 登录),cicy 只负责把它跑在 pane 里、管生命周期。此时网关/model 设置对它无意义。

## 怎么选 / 切换

- 新建 agent 时勾 `use_custom_gateway`(默认开)。
- **gateway agent 必须和源同 provider**:fork 会把源的 `runtime_ai` 带过去,避免掉回默认 claude provider、boot 出别的模型。
- 官方登录 agent 忽略网关/model —— 想统一记账/切模型就用网关模式。

::: warning SERVER 自身别走 agent 代理
cicy-code 服务进程**绝不能**走某个 agent 的 MITM 代理(否则每次网关上游调用会被自己的 MITM 再拦一次、按那个 agent 双重记账)。启动时会 strip 掉泄漏进来的 loopback agent-MITM 代理 env。见 [MITM](/advanced/mitm)。
:::
