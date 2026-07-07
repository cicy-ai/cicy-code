---
title: MITM 审计代理
description: 每个 agent 的 HTTP(S) 出站经本地 MITM 代理(127.0.0.1:9001),按 agent 身份拦截、审计、可熔断。
---
# MITM 审计代理

cicy-code 给每个 agent 的出站 HTTP(S) 挂了一层**本地 MITM 代理**(`api/mgr/mitm/`),用来**按 agent 身份**拦截、审计、必要时熔断它的 AI/网络流量。

## 怎么接进去

- MITM 监听 **`127.0.0.1:9001`**;
- 每个 agent 的 pane shell 里,`.cicy_tmux.conf` 会导出 **`HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY`**(大小写全套)指向它 —— 于是 agent 的所有 HTTP(S) 出站都过 MITM;
- **agent 身份**通过代理鉴权带进去(用户名 = agent id,如 `w-1013`),所以每条流量都能挂到具体 agent;
- 开关 **`use_mitm`**(`agent_config`,默认 **开**),可按 agent 关。

## 组件(`api/mgr/mitm/`)

| 文件 | 作用 |
| --- | --- |
| `ca.go` | 本地 CA;`/api/mitm/ca` 供 install-ca 取 PEM |
| `tls_terminate.go` | TLS 终止(解密后才能审计内容) |
| `http_connect_server.go` / `socks5_server.go` | HTTP CONNECT / SOCKS5 入口 |
| `audit_hook.go` | 把请求/响应喂给[审计](/advanced/audit)管线 |
| `breaker.go` | 熔断 / 策略拦截(按 BreakerDecision) |
| `consent.go` | 敏感操作同意 |
| `identity.go` | 从代理凭据解析 agent 身份 |

## 为什么要 MITM

- **审计**:解密后能看到「哪个 agent、调了哪个模型/接口、多少 token、多少钱、哪个 conversation」;
- **熔断/策略**:命中策略可当场拦截(见 [审计策略](/advanced/audit));
- 和 [本地网关](/advanced/gateway) 配合:网关管「怎么转发+记账」,MITM 管「按 agent 看见全部流量+可拦」。

::: warning 服务进程绝不走 agent MITM
cicy-code 服务本身的出站**绝不能**经某 agent 的 MITM(否则网关上游调用会被自己的 MITM 再拦一次,按那个 agent 双重审计、污染 usage 与 conversation_id)。启动时会**主动 strip** 掉泄漏进来的 loopback agent-MITM 代理 env(`sanitizeAgentMitmProxyEnv`)。
:::

## CA 安装

审计 HTTPS 需要客户端信任 MITM 的 CA。通过 `/api/mitm/ca` 取 CA、用 install-ca 装进系统/运行时信任库(容器里由 boot 处理)。
