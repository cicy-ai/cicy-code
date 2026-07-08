---
title: CLI 命令
description: cicy-code 主命令、启动选项、skill 子命令与其它子命令的完整速查。
---
# CLI 命令

```bash
cicy-code [options]                 # 启动 server(默认)
cicy-code <subcommand> [args]       # skill / audit / mitm / reseed-memory / cicy-repl
```

## 启动选项

| 选项 | 作用 |
| --- | --- |
| `--port N` | API 端口(覆盖 `PORT`,默认 `8008`) |
| `--public` | 绑 `0.0.0.0` 暴露到网络(默认仅 `127.0.0.1`,**含容器内**);务必配强 `api_token` |
| `--dev` | 开发模式 |
| `--preview` | 从磁盘 serve `app/dist`(改前端后 `npm run build` 刷新) |
| `--hot` | 把 UI 代理到 `:8022` 的 vite dev server(HMR) |
| `--cdn` | App SPA + ttyd 资产走 Cloudflare R2(默认用内嵌本地资产) |
| `--lab` | lab 模式(放出更全的 agent 候选) |
| `--audit` | 审计模式(等价 `AUDIT_MODE=1`) |
| `--helper=1` | Team-Helper:在 `w-1001` 上一个 headless "团队助手" |
| `--version`, `-v` | 版本 |
| `--help`, `-h` | 帮助 |

### Cloudflare 隧道

| 选项 | 作用 |
| --- | --- |
| `--cft` | quick tunnel(`https://<随机>.trycloudflare.com`,每次重启换域名) |
| `--cft-token TOKEN` | named tunnel(**域名稳定**,隐含 `--cft`);也可用 `CICY_CFT_TOKEN` |
| `--cft-host FQDN` | named tunnel 对外报告的完整域名;也可用 `CICY_CFT_HOST` |

URL 会打日志、写入 `~/cicy-ai/db/cft.json`,并由 `/api/health` 的 `tunnel_url` 报告。

## `skill` 子命令

```bash
cicy-code skill list [--query <q>] [--category <c>] [--json]
cicy-code skill search <query>
cicy-code skill info <name>[@<version>]
cicy-code skill install <name>[@<version>]
cicy-code skill update <name> | update --all
cicy-code skill remove <name>
cicy-code skill installed              # 本地已装(快,无远程)
cicy-code skill dev <path>             # 本地开发一个 skill
cicy-code skill eject <name>           # 释出到工作树
cicy-code skill registry <serve|publish|add|remove|sources>
```

详见 [Skill 生态 · 概览与安装](/skills/overview)。

## 其它子命令

| 命令 | 作用 |
| --- | --- |
| `cicy-code reseed-memory (--ids w-1,w-2 \| --offline \| --all) [--dry-run]` | 重新生成 agent 的记忆(会备份、保留自定义标记) |
| `cicy-code audit ...` | 审计策略 CLI(见 [审计策略](/advanced/audit)) |
| `cicy-code mitm ...` | MITM CA / 代理管理(见 [MITM 审计代理](/advanced/mitm)) |
| `cicy-code cicy-repl` | 内置 cicy agent 的交互 REPL |

## 更新自身

```bash
cicy-code-update            # → latest
cicy-code-update 2.3.194    # → 指定版本
```

并排装到 `~/.local/cicy-code/<ver>`,翻软链,只重启 cicy-code —— 不动容器/隧道。见 [部署 · Docker / runtime](/deploy/docker)。
