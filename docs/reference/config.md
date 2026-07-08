---
title: 配置与路径
description: cicy 状态根 ~/cicy-ai 的完整目录布局、~/.local/cicy-code 版本库、端口清单与敏感文件位置。
---
# 配置与路径

cicy-code 的所有状态都在**状态根 `~/cicy-ai`** 下(二进制本身另存于 `~/.local/cicy-code`)。

## `~/cicy-ai` 目录布局

| 路径 | 作用 |
| --- | --- |
| `global.json` | 全局配置 + `api_token`(自动生成) |
| `db/` | SQLite 数据库 + 敏感配置(见下,`chmod 600`) |
| `memory/` | 记忆模板:`global.md`、`projects/<name>.md`、`agents/<role>.md` |
| `memory-seed/` | 内置种子模板(dev 机上软链到仓库 `api/mgr/embed/memory-seed`) |
| `workers/` | 每个 agent 的 workspace(`w-1001` …) |
| `skills/` | 已装 skill(public / private / team) |
| `knowledge/` | 团队知识库(`cicy-knowledge`) |
| `assets/` | 上传资源目录 |
| `runtime/` | mihomo / cloudflared 等运行时二进制(版本化,`versions.json`) |
| `mitm/` | MITM 配置 + CA(`config.json`、`mitm-ca.crt`) |
| `audit/` | 审计事件与策略 |
| `snapshots/` | 桌面快照 |
| `supervisor/` | supervisord 运行数据(容器) |
| `bin/` | 辅助脚本 |

## `~/cicy-ai/db/` 敏感文件

| 文件 | 内容 |
| --- | --- |
| `data.db` | 主 SQLite 库(`SQLITE_PATH` 可覆盖) |
| `global.json`(在上一层) | api_token 等 |
| `email.json` | 令牌投递邮箱 / SMTP |
| `cf.json` · `cft.json` · `cf-tunnel.json` | Cloudflare API token / tunnel |
| `frpc.toml` | frp 隧道配置 |
| `mihomo.yaml` | mihomo 代理配置 |
| `mitm-ca.crt` · `mitm-ca.key` | MITM 根证书 |
| `kv.json` · `cli-installed.json` | 轻量 KV / 已装 CLI 缓存 |

> ⚠️ `db/` 下都是密钥/凭证,别提交、别外泄。

## 二进制与版本库

| 路径 | 作用 |
| --- | --- |
| `~/.local/bin/cicy-code` | PATH 入口(软链 → 当前版本) |
| `~/.local/cicy-code/<ver>/` | 版本化安装(`CICY_CODE_STORE` 可覆盖) |
| `~/cicy-ai/runtime/versions.json` | `cicy-code` / `mihomo` 的当前版本指针 |

更新机制见 [部署 · Docker / runtime](/deploy/docker)。

## 端口清单

| 端口 | 用途 | 覆盖 |
| --- | --- | --- |
| `8008` | API + 工作区 | `PORT` / `--port` |
| `8022` | 前端 vite dev server(`--hot` 代理它) | — |
| `8007` | 每 agent MITM 审计代理 | `CICY_MITM_HTTP_PORT` |
| `19001` | mihomo 外部控制器 | `CICY_MIHOMO_PORT` |
| `6060` | Go pprof(设 `CICY_PPROF_PORT` 才开) | `CICY_PPROF_PORT` |

变量清单见 [环境变量](/reference/env),命令见 [CLI 命令](/reference/cli)。
