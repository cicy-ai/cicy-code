---
title: 环境变量
description: cicy-code 支持的环境变量 —— 端口、运行模式、隧道、skill 注册表、MITM/网关、mihomo。
---
# 环境变量

所有变量都是可选的;不设时用括号里的默认值。命令行 `--flag` 优先级高于同义的环境变量。

## 核心 / 端口

| 变量 | 默认 | 作用 |
| --- | --- | --- |
| `PORT` | `8008` | API + 工作区监听端口。`--port N` 优先。 |
| `CICY_API_PORT` | `PORT` | 各 agent shell 里注入的端口,指回同一个 server。 |
| `SQLITE_PATH` | `~/cicy-ai/db/data.db` | SQLite 数据库文件。 |
| `CICY_API_TOKEN` | 自动生成 | API 鉴权 token(写在 `~/cicy-ai/global.json`)。 |
| `CICY_CODE_VERSION` | `latest` | 容器首启时 `cicy-code-update.sh` 安装的版本。 |
| `CICY_CODE_STORE` | `~/.local/cicy-code` | 版本化二进制的安装根目录。 |
| `CICY_PPROF_PORT` | `6060` | Go pprof 端口(设了才开)。 |

## 运行模式

| 变量 | 作用 |
| --- | --- |
| `CICY_RUNTIME_MODE=api-only` | 只开 API,关掉 tmux / 桌面专属接口(生产 sidecar 用)。 |
| `CICY_NO_BROWSER` | 启动时不自动打开浏览器。 |
| `CICY_HELPER=1` | Team-Helper 模式(等价 `--helper=1`)。 |
| `IS_SANDBOX=1` | root 容器里放行 claude 的 `--dangerously-skip-permissions`(cicy 的 pane 已自动设)。 |

## 分发 / 隧道 / skill

| 变量 | 默认 | 作用 |
| --- | --- | --- |
| `NPM_REGISTRY` | 探测 npmmirror→npmjs | 安装/自更新用的 npm 源。 |
| `CICY_CFT_TOKEN` | — | Named Cloudflare tunnel 的连接 token(隐含 `--cft`,域名稳定)。 |
| `CICY_CFT_HOST` | — | Named tunnel 对外报告的完整域名。 |
| `CICY_SKILLS_REGISTRY` | `skills.cicy-ai.com` | skill 注册表 URL。 |
| `CICY_SKILLS_ROOT` | `~/cicy-ai/skills` | 已装 skill 根目录。 |

## 审计 / MITM / 网关 / mihomo

| 变量 | 默认 | 作用 |
| --- | --- | --- |
| `AUDIT_MODE=1` | — | 审计模式(等价 `--audit`)。 |
| `CICY_MITM_HTTP_PORT` | `8007` | 每 agent 的 MITM 审计代理监听端口。 |
| `CICY_CA_TRUST_CONSENT` | — | 跳过 MITM CA 安装的交互确认。 |
| `CICY_MIHOMO_HOST` / `CICY_MIHOMO_PORT` | `127.0.0.1` / `19001` | mihomo 控制器地址(外部 controller)。 |
| `CICY_MIHOMO_BIN` | runtime store | 覆盖 mihomo 二进制路径。 |
| `CICY_AI_GATEWAY_LLM_ENDPOINT` / `..._API_KEY` | — | 本地 AI 网关的上游 LLM 端点 / key。 |

> 端口/路径的完整含义见 [配置与路径](/reference/config);MITM/网关见 [MITM 审计代理](/advanced/mitm)、[本地 AI 网关](/advanced/gateway)。
