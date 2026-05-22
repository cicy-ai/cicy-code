# Kiro CLI Agent 集成摘要

## 概述

kiro-cli 是 AWS Kiro 团队的官方 CLI agent。与 claude / codex / opencode 等 agent 不同，kiro-cli **不能走本地 AI Gateway 授权**，因此 cicy-code 的 `reply.json` / `current.json` / `history.db` 管线对 kiro 不生效。

## 会话存储

### kiro-cli 自身存储

| 存储 | 路径 | 格式 | 实时写入 |
|---|---|---|---|
| 会话数据 | `~/.kiro/sessions/cli/<session_id>.jsonl` | 每行一个事件（Prompt / AssistantMessage / ToolResults） | 按消息粒度落盘（消息完成后追加一行） |
| 会话元数据 | `~/.kiro/sessions/cli/<session_id>.json` | JSON | 按需写入 |
| SQLite | `~/.local/share/kiro-cli/data.sqlite3` | SQLite | **未使用** — `conversations_v2` 表结构存在但 0 行数据 |

### 如何通过 agent_id 找到对应的 kiro session

kiro 的 `.json` 元数据文件中记录了 `cwd`（工作目录），可以通过 workspace 路径反查：

```bash
# 已知 agent_id=w-10024，找它的 kiro jsonl
grep -l "w-10024" ~/.kiro/sessions/cli/*.json
# → ~/.kiro/sessions/cli/6e1e2316-....json
# → session_id = 6e1e2316-...
# → 全量会话文件 = ~/.kiro/sessions/cli/6e1e2316-....jsonl
```

`.json` 元数据字段：

| 字段 | 示例 | 说明 |
|---|---|---|
| `session_id` | `6e1e2316-...` | 即文件名 |
| `cwd` | `/home/cicy/cicy-ai/workers/w-10024` | 工作目录，对应 agent workspace |
| `created_at` | `2026-05-20T04:12:08Z` | 会话创建时间 |
| `title` | `你的ip 是那一个` | 首个用户问题作为标题 |

会话目录 `~/.kiro/sessions/cli/` 下每个 session 对应三个文件：

| 文件 | 用途 |
|---|---|
| `<id>.json` | 会话元数据（session_id, cwd, title, created_at 等） |
| `<id>.jsonl` | 全量对话记录，每行一个事件 |
| `<id>.lock` | 进程锁，内含 PID 和 started_at |
| `<id>/` | 子目录，内含 `tasks/` 等会话附属数据 |

### cicy-code 侧（对 kiro 不适用）

以下 cicy-code 的 AI Gateway 审计产物对 kiro **不产生数据**：

- `reply.json` — 不产生（不走 `ai_gateway_audit.go`）
- `current.json` — 不产生
- `history.db` — 不产生（SQLite 中无 kiro 会话记录）
- `reply_mirror/` — 不产生

## 授权方式

kiro-cli 使用自己的登录体系，不走 cicy-code 的 `CICY_API_KEY`：

```
kiro-cli login --license free --use-device-flow   # Builder ID / Google / Github
kiro-cli login --license pro --use-device-flow    # Identity Center (专业版)
```

Session 存储在 `~/.kiro/sessions/cli/`，由 kiro CLI 自身管理，token 缓存在 `data.sqlite3` 的 `auth_kv` 表中。

## API 路由

kiro-cli 直接访问 Anthropic API（或自配的 `ANTHROPIC_BASE_URL`），**不经过** cicy-code 的本地 AI Gateway 代理：

```
cicy-code gateway:  http://127.0.0.1:8008/api/ai-gateway/anthropic/<agent_id>
                     ↑ kiro 不走这条路
                     
kiro 直连:          https://api.anthropic.com (或 $ANTHROPIC_BASE_URL)
```

因此在 `tmux.go` 中，kiro-cli 与 claude / codex / copilot / opencode 同属**自管 API 路由**的 agent 类型（line 3876），不注入 `CICY_OPENAI_BASE_URL` 和 `CICY_ANTHROPIC_BASE_URL` 环境变量。

## 启动流程

1. 安装检测：`__cicy_local_install_kiro` 从 `https://cli.kiro.dev/install` 下载安装
2. Steering：写入 `$WORKSPACE/.kiro/steering/reply-in-chinese.md`（若配置中文回复）
3. 登录检查：`kiro-cli whoami`，若未登录则引导 device-flow 登录
4. 启动：`kiro-cli chat [--trust-all-tools]`

## tmux send 兼容性

kiro-cli 的 `isAgentInputReady` 没有专门实现（2026-05-20 修复前会导致 `waitForAgentInputReady` 超时）。修复方式是跳过 kiro-cli 的 ready-wait 轮询，与 opencode/codex/hermes 同等待遇。

## 关键差异对比

| | claude / cicy-claude | kiro-cli |
|---|---|---|
| API 路由 | 经本地 gateway 代理 | 直连 Anthropic |
| 授权 | `CICY_API_KEY` | `kiro-cli login` |
| reply.json | 产生 | 不产生 |
| current.json | 产生 | 不产生 |
| history.db | 产生 | 不产生 |
| 会话存储 | cicy-code gateway audit | `~/.kiro/sessions/cli/*.jsonl` |
| IM 推送 | 走 `im_reply_hook.go` | 需另外处理 |
| 跨 agent callback | 走 `gateway_reply_callback.go` | 不支持 |
