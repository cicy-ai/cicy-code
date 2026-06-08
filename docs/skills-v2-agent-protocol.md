# Cicy Skills v2 — Agent 安装协议

**Status:** Draft → In Progress
**Owner:** cicy-ai
**Last updated:** 2026-05-22

本文档规定 cicy-code 后端、agent、`cicy-code skill` 子命令、registry 之间的
消息/回调协议，以及如何让多种 agent（claude / codex / opencode / kiro / 未来
扩展）一致地参与 skill 安装与卸载。

---

## 1. 设计原则

1. **Agent 主导** — skill 是 agent 的能力扩展，安装/卸载也由 agent 执行。
   后端不直接动文件，只发指令、收回调。
2. **协议中立** — 不依赖特定 agent 的工具调用机制，靠"用户消息 + 命令行"
   通用模式。
3. **可观测** — 每一步都有状态记录（pending / running / done / failed），UI
   可以实时显示进度。
4. **可降级** — 如果当前没有 active agent 或 agent 不响应，可以降级为
   后端直接调 `cicy-code skill install`（仍走透明 installer，只是不经过 agent）。
5. **可扩展** — 新增 agent 只需在 `agents.json` 注册，无需改 cicy-code 后端。

---

## 2. 端到端流程

### 2.1 安装

```
┌─────────┐
│   UI    │ 用户点击「安装 cf-tunnel」
└────┬────┘
     │ POST /api/skill-market/cf-tunnel/install
     ▼
┌─────────────────────────────────────────────┐
│ cicy-code mgr (api/mgr/skills.go)           │
│ 1. 查询当前 active pane (X-Agent-Show-Id)   │
│ 2. 生成 task_id (uuid)                      │
│ 3. 写 task 到 KV: pending                   │
│ 4. 构造 agent message (见 §3)               │
│ 5. 通过 chatbus 注入消息到 agent            │
│ 6. 返回 { task_id, pane, status: "pending" }│
└────┬────────────────────────────────────────┘
     │ chatbus
     ▼
┌─────────────────────────────────────────────┐
│ Agent (claude/codex/opencode/kiro)          │
│ 1. 收到 user-style message                  │
│ 2. 识别 <cicy-skill-request> 标记           │
│ 3. 调用 shell tool 执行                     │
│    cicy-code skill install cf-tunnel --json │
│    --task-id <task_id>                      │
└────┬────────────────────────────────────────┘
     │ 子进程
     ▼
┌─────────────────────────────────────────────┐
│ cicy-code skill install (Go subcommand)     │
│ 1. POST /api/skill-task/<id>/start          │
│ 2. GET  registry/v1/skills/cf-tunnel/latest │
│ 3. 校验 runtime/system_requirements         │
│ 4. 下载 zip 到 ~/cicy-ai/skills/.cache/     │
│ 5. 校验 sha256                              │
│ 6. 解压到 ~/cicy-ai/skills/cf-tunnel/       │
│ 7. (若需) npm ci --omit=dev --ignore-scripts│
│ 8. chmod +x bin/cf-tunnel                   │
│ 9. 创建 symlink ~/.local/bin/cf-tunnel      │
│ 10. 同步 SKILL.md 到所有 agent skills_dir   │
│ 11. 更新 installed.json                     │
│ 12. POST /api/skill-task/<id>/done          │
│ 13. 输出 JSON 结果到 stdout                 │
└────┬────────────────────────────────────────┘
     │ stdout JSON
     ▼
┌─────────────────────────────────────────────┐
│ Agent                                       │
│ 1. 解析 JSON                                │
│ 2. 用人话回复用户「cf-tunnel 已安装 …」    │
└─────────────────────────────────────────────┘

旁路：
┌─────────────────────────────────────────────┐
│ cicy-code mgr (回调)                        │
│ /api/skill-task/<id>/start  → status: running│
│ /api/skill-task/<id>/done   → status: done   │
│ /api/skill-task/<id>/failed → status: failed │
│ → SSE 推 UI 实时更新                        │
└─────────────────────────────────────────────┘
```

### 2.2 卸载

完全对称：

```
UI → /api/skill-market/cf-tunnel/uninstall
   → 后端发消息 → agent 调
   cicy-code skill remove cf-tunnel --json --task-id <id>
   → 删文件、删 symlink、清各 agent skills_dir
   → 回调 done
```

### 2.3 更新

```
UI → /api/skill-market/cf-tunnel/update
   → 后端发消息 → agent 调
   cicy-code skill update cf-tunnel --json --task-id <id>
   → 等价于 remove + install latest
```

---

## 3. Agent 消息格式

### 3.1 包裹标记

后端注入到 agent 的消息使用专用 XML 块包裹，方便 agent 识别：

```
<cicy-skill-request task_id="t-1234abcd">
<action>install</action>
<name>cf-tunnel</name>
<version>latest</version>
<callback>http://127.0.0.1:8008/api/skill-task/t-1234abcd</callback>
</cicy-skill-request>

请帮我安装 cf-tunnel。执行：
  cicy-code skill install cf-tunnel --json --task-id t-1234abcd

完成后告诉我结果。
```

外层 XML 给 agent 程序解析（如果它支持），内层自然语言确保即使 agent 不识别
XML 也能按指令执行。

### 3.2 消息字段

| 字段 | 必需 | 说明 |
|------|------|------|
| `task_id` | 是 | UUID，关联请求与回调 |
| `action` | 是 | `install` / `uninstall` / `update` |
| `name` | 是 | skill 名 |
| `version` | 否 | 版本号或 `latest`，默认 `latest` |
| `callback` | 是 | 回调 URL 前缀 |

### 3.3 注入路径

cicy-code 通过现有 `chatbus` 注入消息。注入时模拟 user 角色发送，agent 看到的
就是一条普通"用户消息"。

实际注入由 `api/mgr/skills.go` 的新函数 `dispatchSkillTaskToAgent` 完成。

---

## 4. cicy-code 后端 API

### 4.1 触发安装/卸载/更新

```
POST /api/skill-market/<name>/install
POST /api/skill-market/<name>/uninstall
POST /api/skill-market/<name>/update
```

请求 header（可选）：
```
X-Agent-Show-Id: w-1001
```
不传则使用当前 active pane。

请求 body（可选）：
```json
{ "version": "1.2.0" }
```
不传则 latest（仅对 install / update 有意义）。

响应：
```json
{
  "ok": true,
  "data": {
    "task_id": "t-1234abcd",
    "pane": "w-1001",
    "agent_id": "claude",
    "action": "install",
    "name": "cf-tunnel",
    "version": "latest",
    "status": "pending",
    "created_at": "2026-05-22T04:30:00Z"
  }
}
```

降级：如果当前 pane 没有 agent 或 agent provider 检测失败，后端直接调
`cicy-code skill install ...`（直执行模式），返回的 `agent_id: "system"`。

### 4.2 任务回调（installer 调）

```
POST /api/skill-task/<task_id>/start
POST /api/skill-task/<task_id>/done
POST /api/skill-task/<task_id>/failed
POST /api/skill-task/<task_id>/progress
```

`installer` 在执行过程中调用这些端点更新状态。

请求 body：
```json
{
  "step": "downloading",
  "percent": 30,
  "message": "downloading cf-tunnel-1.2.0.zip ..."
}
```

`/done` 的 body：
```json
{
  "name": "cf-tunnel",
  "version": "1.2.0",
  "installed_at": "2026-05-22T04:30:15Z",
  "sha256": "...",
  "agents_synced": ["claude", "codex", "opencode", "kiro"]
}
```

`/failed` 的 body：
```json
{
  "code": "DOWNLOAD_FAILED",
  "message": "sha256 mismatch",
  "details": { ... }
}
```

### 4.3 任务查询

```
GET /api/skill-task/<task_id>
```

响应：
```json
{
  "ok": true,
  "data": {
    "task_id": "t-1234abcd",
    "action": "install",
    "name": "cf-tunnel",
    "version": "latest",
    "pane": "w-1001",
    "agent_id": "claude",
    "status": "running",
    "step": "downloading",
    "percent": 30,
    "logs": [
      { "ts": "2026-05-22T04:30:00Z", "msg": "task created" },
      { "ts": "2026-05-22T04:30:05Z", "msg": "started by agent" },
      { "ts": "2026-05-22T04:30:10Z", "msg": "downloading" }
    ],
    "created_at": "2026-05-22T04:30:00Z",
    "updated_at": "2026-05-22T04:30:10Z"
  }
}
```

### 4.4 SSE 推送

```
GET /api/skill-task/<task_id>/events    (SSE)
```

每次状态变化推一个 event：
```
event: progress
data: {"step":"downloading","percent":30}

event: done
data: {"name":"cf-tunnel","version":"1.2.0"}
```

UI 侧用 EventSource 订阅。

---

## 5. installer ↔ 后端鉴权

`cicy-code skill install` 调后端 `/api/skill-task/...` 需要鉴权。复用现有
全局 token 机制：

- 优先 env `CICY_API_TOKEN`
- 否则读 `~/cicy-ai/global.json` 的 `api_token` 字段

`cicy-code` 在启动 installer 子进程时不传明文 token，installer 自己读
`global.json`。

---

## 6. agents.json 与 agent provider

### 6.1 schema

`~/cicy-ai/skills/agents.json`：

```json
{
  "schema_version": 1,
  "agents": [
    {
      "id": "claude",
      "name": "Claude Code",
      "skills_dir": "~/.claude/skills",
      "manifest_file": "SKILL.md",
      "detect": {
        "command": "claude",
        "version_flag": "--version",
        "version_pattern": "^claude (\\d+\\.\\d+\\.\\d+)"
      },
      "min_version": "0.1.0"
    },
    {
      "id": "codex",
      "name": "Codex CLI",
      "skills_dir": "~/.codex/skills",
      "manifest_file": "SKILL.md",
      "detect": { "command": "codex", "version_flag": "--version" }
    },
    {
      "id": "opencode",
      "name": "OpenCode",
      "skills_dir": "~/.opencode/skills",
      "manifest_file": "SKILL.md",
      "detect": { "command": "opencode", "version_flag": "--version" }
    },
    {
      "id": "kiro",
      "name": "Kiro CLI",
      "skills_dir": "~/.kiro/skills",
      "manifest_file": "SKILL.md",
      "detect": { "command": "kiro-cli", "version_flag": "--version" }
    }
  ]
}
```

### 6.2 字段语义

| 字段 | 必需 | 说明 |
|------|------|------|
| `id` | 是 | 唯一标识，小写英文，用于 manifest.compatible_agents |
| `name` | 是 | 人类可读名 |
| `skills_dir` | 是 | 该 agent 读 SKILL.md 的目录 |
| `manifest_file` | 是 | 通常 `SKILL.md` |
| `detect.command` | 否 | 探测命令，能在 PATH 找到则视为已安装 |
| `detect.version_flag` | 否 | 用于探测版本 |
| `detect.version_pattern` | 否 | 正则，从 `command --version` 输出捕获版本号 |
| `min_version` | 否 | 最低支持版本（不满足则跳过同步） |

### 6.3 同步行为

`cicy-code skill install` 完成主体安装后，按 `agents.json` 遍历：

1. 检查 `detect.command` 是否存在（若否，跳过该 agent）
2. 创建 `<skills_dir>/<name>/`（递归）
3. 把以下文件 symlink 到该目录（首选 symlink，跨平台失败时降级 copy）：
   - `SKILL.md`
   - `help.md`、`tools.md`、`README.md`
   - `references/`（若存在）
4. 把成功同步的 agent id 写入 `installed.json` 的 `agents_synced` 数组

卸载时反向操作。

---

## 7. cicy-code 当前 active pane / agent 解析

`api/mgr/agents.go` 现有逻辑：

```
X-Agent-Show-Id header → pane id
↓
查找 panes[paneID].agent_id
↓
返回 (pane, agent_id)
```

需要补充：从 pane 信息推断 agent provider。当前可以通过
`runtime_ai_provider_names.go` 的映射做最小改造：把 runtime 里跑的 CLI 名字
（`claude` / `codex` / `opencode` / `kiro-cli`）转成 agent_id。

如果这个 pane 没有 agent（比如纯 shell pane），则降级为「直执行模式」。

---

## 8. 错误码

任务失败时 `failed` 回调中的 code：

| code | 含义 |
|------|------|
| `INVALID_NAME` | skill 名格式错 |
| `NOT_FOUND` | registry 中找不到 |
| `RUNTIME_INCOMPATIBLE` | node 版本不满足 |
| `MISSING_SYSTEM_REQ` | 缺 curl / jq 等 |
| `DOWNLOAD_FAILED` | 网络错误 / 重试用尽 |
| `SHA256_MISMATCH` | 包完整性校验失败 |
| `EXTRACT_FAILED` | 解压失败 |
| `NPM_INSTALL_FAILED` | 装 node_modules 失败 |
| `PERMISSION_DENIED` | 文件权限不够 |
| `AGENT_SYNC_FAILED` | 同步到 agent skills_dir 失败 |
| `ALREADY_INSTALLED` | 同版本已装（除非 --force） |
| `NOT_INSTALLED` | 卸载/更新时本地没有 |
| `INTERNAL` | 兜底 |

---

## 9. 时序图

```
UI    mgr           agent           installer       registry
 │     │             │                  │              │
 │POST install       │                  │              │
 ├────►│             │                  │              │
 │     │ inject msg  │                  │              │
 │     ├────────────►│                  │              │
 │ 200 │             │                  │              │
 │◄────┤             │                  │              │
 │SSE  │             │                  │              │
 │◄────┤             │                  │              │
 │     │             │ exec installer   │              │
 │     │             ├─────────────────►│              │
 │     │ start cb    │                  │              │
 │     │◄────────────┼──────────────────┤              │
 │SSE  │             │                  │              │
 │◄────┤             │                  │              │
 │     │             │                  │ GET manifest │
 │     │             │                  ├─────────────►│
 │     │             │                  │◄─────────────┤
 │     │ progress cb │                  │              │
 │     │◄────────────┼──────────────────┤              │
 │SSE  │             │                  │              │
 │◄────┤             │                  │              │
 │     │             │                  │ download zip │
 │     │             │                  ├─────────────►│
 │     │             │                  │◄─────────────┤
 │     │             │                  │ extract...   │
 │     │ done cb     │                  │              │
 │     │◄────────────┼──────────────────┤              │
 │SSE  │             │ stdout JSON      │              │
 │◄────┤             │◄─────────────────┤              │
 │     │             │ reply user       │              │
 │     │             │ (人话总结)       │              │
```

---

## 10. 安全考虑

### 10.1 防止恶意 agent 滥用

由于 installer 是 agent 调起的子进程，理论上 agent 可以把 task_id 改成别的、
或者跳过 installer 直接用 shell 命令乱来。

防御：
- installer 调 `/api/skill-task/<id>/start` 时校验 task_id 必须存在且未过期
- 后端记录 task → pane 绑定，`/start` 必须从 pane id 一致的来源调
- 如果 agent 没用 installer 而是手动改 `~/cicy-ai/skills/`，下次 doctor 时
  会发现 manifest 与 installed.json 不一致并告警

### 10.2 防止冒充任务

- task_id 用 cryptographically-random UUID
- task 在 KV 中 TTL 30 分钟，超时即过期
- `/start`、`/done`、`/failed` 只允许单次调用（状态机推进，重复调返回 409）

### 10.3 凭据传递

不通过命令行参数传 `api_token`（避免 ps 泄露），全部走 env / config 文件。

---

## 11. 降级 / 直执行模式

如果以下任一条件成立，cicy-code 后端不发消息给 agent，直接 fork 执行
`cicy-code skill install ...`：

- 当前 pane 没有 agent
- agents.json 中没有匹配的 provider
- 用户在 UI 设置中选了「直接安装（不经过 agent）」
- 同 task_id 的 agent 路径超时未响应（>30s）后回退

降级时仍然走 task 状态机，UI 行为对用户透明。

---

## 12. 未来扩展

- **批量任务** — 一次 install 多个 skill：让 agent 收到 list of requests
- **Skill 配置向导** — 安装后让 agent 引导用户填 `~/cicy-ai/db/<name>.json`
- **离线模式** — `cicy-code skill install --offline <zip-path>` 直接用本地 zip
- **私有 registry** — 多 registry 优先级，企业内部自托管

---

## 13. 相关文档

- [skills-v2-design.md](./skills-v2-design.md) — 总体设计
- [skills-v2-manifest.md](./skills-v2-manifest.md) — manifest schema 详细
