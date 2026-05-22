# Cicy Skills v2 — 总体设计

**Status:** Draft → In Progress
**Owner:** cicy-ai
**Last updated:** 2026-05-22

---

## 1. 背景与目标

### 1.1 现状的问题

现有 cicy-skills 体系（v1）将所有 skill 打包进 cicy-code 主仓库，使用 Go 编译为
单体二进制（`cicy-skills`、`cicy-hosttools`）+ 大量符号链接（`cf`、`cf-tunnel`、
`cping`、`email`…）的方式分发。这种方式有四个核心问题：

1. **不透明** — 用户拿到的是编译后的二进制，无法审计 skill 源码，
   `~/.local/bin/cf-tunnel` 实际指向 `dist/cicy-hosttools` 这种单体二进制。
2. **不利于开源分发** — skill 跟 cicy-code 强绑定，第三方贡献者无法独立发布
   skill；用户也无法只装单个 skill。
3. **死的安装/卸载** — 安装就是后端拷贝文件，卸载就是删文件，agent 不参与；
   而 skill 本质上是 agent 的能力扩展，应当由 agent 主导。
4. **Skill registry 写死在 Go 里** — `agentgenApprovedMarketSkills`、
   `hosttoolAliasSet` 都是源码常量，新增 skill 必须发版 cicy-code。

### 1.2 v2 目标

| 维度 | v1 | v2 |
|------|----|----|
| Skill 实现 | Go 编译二进制 | Node.js 透明源码 |
| Skill 仓库 | 跟 cicy-code 同仓库 | 独立 monorepo `cicy-ai/cicy-skills` |
| 分发渠道 | 跟 cicy-code 一起 release | CF Worker registry `skills.cicy-ai.com` |
| 版本管理 | 跟 cicy-code 同版本 | 每个 skill 独立 semver |
| 安装方式 | 后端直接拷贝 | 后端发消息给 agent，agent 调 installer |
| Bootstrap 工具 | `cicy-skills` Go CLI | `cicy-code skill` 子命令（Go） |
| 配置位置 | 散落各处 | 统一 `~/cicy-ai/db/<name>.json`（0600） |
| Agent 支持 | claude / codex / opencode 硬编码 | 通过 `agents.json` 注册，可扩展 |

### 1.3 非目标

- 不做沙箱/容器隔离（v1 不做，未来通过 `permissions` 字段引入）
- 不做 skill 之间的依赖图（每个 skill 独立自洽）
- 不做付费/私有 skill（registry 全公开）

---

## 2. 整体架构

```
┌──────────────────────────────────────────────────────────────────┐
│                    cicy-code (Go 单体二进制)                      │
│  ├─ mgr (现有 manager 后端)                                       │
│  ├─ host-runtime (现有)                                           │
│  └─ skill-installer ←  新增子命令                                 │
│       cicy-code skill <list|install|remove|update|info|...>       │
└──────────┬─────────────────────────────────────────┬─────────────┘
           ↓ 索引/路由 HTTPS                          ↓ 直接下载 HTTPS
┌─────────────────────────────────┐    ┌──────────────────────────┐
│   skills.cicy-ai.com (Worker)   │    │  github.com (Releases)   │
│   仅做索引：                     │    │  zip asset 存储          │
│   KV: manifest 索引 / 版本表     │    │  ←─ 由 GitHub Action 上传│
│   不存储 zip                     │    │                          │
└─────────────────────────────────┘    └──────────────────────────┘
           ↑ 发布元数据                           ↑ 上传 zip
           └────────────────┬────────────────────┘
                            │
┌──────────────────────────────────────────────────────────────────┐
│       github.com/cicy-ai/cicy-skills (官方 monorepo)              │
│  skills/<name>/   每个 skill 一个目录，纯 Node.js 源码            │
│  GitHub Action: tag <name>-vX.Y.Z → 同时:                         │
│    1. 打 zip 上传到 GitHub Releases                               │
│    2. POST manifest + sha256 + download_url 到 Worker /admin      │
└──────────────────────────────────────────────────────────────────┘

第三方仓库（cicy-code skill install --repo user/repo）：
┌──────────────────────────────────────────────────────────────────┐
│       github.com/<user>/<repo> (任意公开仓库)                     │
│  cicy-code 直读 GitHub API，不经过 Worker                         │
└──────────────────────────────────────────────────────────────────┘

╭──────────────────────── 用户机器布局 ────────────────────────╮
│ ~/cicy-ai/db/                  配置（0600，不被 skill 输出）  │
│   ├─ global.json               api_token                       │
│   ├─ cf.json                   Cloudflare token                │
│   └─ <name>.json               按 skill 名分文件               │
│                                                                │
│ ~/cicy-ai/skills/              skill 源码（公开透明，可审计）  │
│   ├─ installed.json            本机已装清单                    │
│   ├─ agents.json               agent provider 注册表           │
│   ├─ .cache/                   下载的 zip 缓存                 │
│   ├─ cf-tunnel/                每个 skill 一个目录             │
│   │   ├─ manifest.json                                         │
│   │   ├─ SKILL.md                                              │
│   │   ├─ help.md                                               │
│   │   ├─ tools.md                                              │
│   │   ├─ bin/cf-tunnel  ← #!/usr/bin/env node                  │
│   │   └─ lib/*.js                                              │
│   └─ ...                                                       │
│                                                                │
│ ~/.local/bin/                  symlink 到各 skill 入口         │
│   └─ cf-tunnel → ~/cicy-ai/skills/cf-tunnel/bin/cf-tunnel      │
│                                                                │
│ ~/.claude/skills/<name>/       同步给各 agent 的 SKILL.md      │
│ ~/.codex/skills/<name>/                                        │
│ ~/.opencode/skills/<name>/                                     │
│ ~/.kiro/skills/<name>/                                         │
╰────────────────────────────────────────────────────────────────╯
```

---

## 3. 核心组件

### 3.1 cicy-code 内置 skill-installer (Go)

集成到 cicy-code 主二进制，作为子命令暴露：

```
cicy-code skill list                       # 远程 registry 列表
cicy-code skill search <query>
cicy-code skill info <name>[@<version>]
cicy-code skill install <name>[@<version>]
cicy-code skill update <name>
cicy-code skill update --all
cicy-code skill remove <name>
cicy-code skill installed                  # 本机已装
cicy-code skill doctor [<name>]            # 诊断 node/curl/jq + 单个 skill
cicy-code skill agents                     # 列已识别的 agent
cicy-code skill sync <name>                # 重同步 SKILL.md 到所有 agent dirs
cicy-code skill dev <path>                 # 本地目录直装（开发模式）
```

所有命令支持 `--json` 输出，给 agent 解析。

**为什么 installer 用 Go：**
- 跟随 cicy-code 主二进制分发，用户已经装过
- bootstrap 阶段用户机器可能还没有 node（installer 检测后引导安装）
- installer ≠ skill，installer 是 host runtime 的一部分，skill 才是用户脚本

### 3.2 Cloudflare Worker (skills-registry)

部署到 `skills.cicy-ai.com`。**仅做官方索引，不存储任何 zip**。

**存储：**
- KV namespace `SKILLS_KV` — 仅 manifest 索引、版本列表、download_url

**核心特性：**
- zip 文件实际存在 GitHub Releases（`cicy-ai/cicy-skills` repo）
- Worker 在 manifest 中记录 `download_url`，client 拿到后直接 `curl` GitHub
- Worker 可选缓存 manifest JSON（`cache: max-age=300`）加速读
- `/v1/skills/:name/:version/download` 返回 302 到 GitHub Releases URL

**API（详见 §5）：**
- 读 API 全部公开
- 写 API 需 admin token（仅 `cicy-ai/cicy-skills` 的 GitHub Action 持有）

### 3.3 多源安装能力

`cicy-code skill install` 支持四种来源：

| 来源 | 命令示例 | 说明 |
|------|----------|------|
| 官方索引（默认） | `cicy-code skill install cping` | 走 Worker → GitHub Releases |
| 第三方 GitHub repo | `cicy-code skill install --repo user/skills cping` | 直读 GitHub API，不经过 Worker |
| 自定义 zip URL | `cicy-code skill install --url https://example.com/skill.zip` | 直接下载 zip，需配 `--sha256` |
| Git clone | `cicy-code skill install --git https://github.com/user/repo.git` | 直接 clone（开发用） |

第三方仓库需要符合下列任一约定：

1. **single-skill repo**：根目录直接是 skill（`manifest.json` 在根）
2. **monorepo**：`skills/<name>/` 子目录下放 skill

#### 3.3.1 GitHub Releases 资产命名约定

每个 skill 一个 tag、一个 release、一个 zip asset：

```
tag:     <name>-v<X.Y.Z>      例如  cping-v1.0.0
release: same as tag
asset:   <name>-<X.Y.Z>.zip   例如  cping-1.0.0.zip
URL:     https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>
```

cicy-code 既知道这个约定，又信任 manifest 的 `download_url` 字段，二者一致即可。

### 3.3 Skill 源码仓库（cicy-ai/cicy-skills）

Monorepo 布局，每个 skill 一个目录：

```
cicy-skills/
├── README.md
├── CONTRIBUTING.md
├── LICENSE
├── package.json                    # workspace root（仅 tools 用）
├── .github/workflows/
│   ├── publish.yml                 # tag 触发：打包+上传+更新 KV
│   ├── validate.yml                # PR 触发：校验所有 manifest
│   └── ci.yml                      # 跑 skill 测试
├── schemas/
│   └── manifest.schema.json        # JSON Schema for manifest.json
├── tools/
│   ├── validate-skill.js           # 校验单 skill 目录
│   ├── pack-skill.js               # 打 zip + sha256
│   └── publish.js                  # 调 worker /v1/admin/publish
├── templates/
│   └── skill-template/             # 新 skill 脚手架
└── skills/
    ├── cping/                      # 参考实现：零依赖
    ├── cf-tunnel/
    ├── google/                     # 有 npm 依赖
    └── ...
```

---

## 4. Skill 规范

### 4.1 目录布局

```
skills/<name>/
├── manifest.json             # 必需，元数据
├── SKILL.md                  # 必需，YAML frontmatter + agent 指令
├── README.md                 # 必需，给人看的概览
├── help.md                   # 推荐，命令使用文档
├── tools.md                  # 推荐，endpoint/env/exit-code 表
├── config.schema.json        # 可选，配置 JSON Schema
├── bin/
│   └── <name>                # 必需，#!/usr/bin/env node 主入口
├── lib/                      # 可选
│   └── *.js
├── package.json              # 仅当有 npm 依赖
├── package-lock.json
└── test/                     # 可选，node --test
    └── *.test.js
```

### 4.2 编码约定

按优先级递增：

1. **系统 CLI**（`curl`、`jq`、`git`、`ssh`） — 优先用，借力宿主环境
2. **Node 内置**（`fetch`、`fs`、`crypto`、`child_process`） — 次优先
3. **npm 依赖** — 必要时才用，必须锁版本（`package-lock.json` 入库）

不允许：
- TypeScript 源码进 skill 目录（源码即分发，无编译步骤）
- Bash 脚本作为主入口（统一 Node.js）
- 全局污染（写 `/etc`、`/usr` 之外的系统目录）

### 4.3 配置约定

所有需要持久化的配置统一放 `~/cicy-ai/db/<name>.json`，权限 `0600`。

```javascript
// 推荐 helper（每个 skill 内联，零依赖）
import { readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

const DB = process.env.CICY_DB_OVERRIDE || join(homedir(), 'cicy-ai/db');
function loadDB(name) {
  try { return JSON.parse(readFileSync(join(DB, `${name}.json`), 'utf8')); }
  catch (e) { if (e.code === 'ENOENT') return null; throw e; }
}
```

Skill 在 `manifest.json` 中声明配置：

```json
"config": {
  "path": "~/cicy-ai/db/cf.json",
  "permissions": "0600",
  "secret_fields": ["api_token"]
}
```

`cicy-code skill doctor <name>` 会检查文件权限是否为 0600。

### 4.4 输出约定

每个 skill 必须支持两种输出模式：

- 默认 — 人类可读
- `--json` — 机器可读（给 agent 解析）

退出码约定：

| 码 | 含义 |
|----|------|
| 0  | 成功 |
| 1  | 通用失败 |
| 2  | 参数错误 |
| 3  | 配置/凭据缺失 |
| 4  | 网络/外部 API 失败 |
| 5  | 权限不足 |

---

## 5. Registry API（CF Worker）

详见 [skills-v2-manifest.md](./skills-v2-manifest.md)。本节列接口签名。

### 5.1 路由总览

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| GET  | `/v1/health` | 无 | 健康检查 |
| GET  | `/v1/skills` | 无 | 列表（`?q=&category=&agent=`） |
| GET  | `/v1/skills/:name` | 无 | 详情（latest 版本 + 文档片段） |
| GET  | `/v1/skills/:name/versions` | 无 | 所有版本列表 |
| GET  | `/v1/skills/:name/:version` | 无 | 指定版本 manifest |
| GET  | `/v1/skills/:name/:version/files/:file` | 无 | 单文件预览（SKILL.md / help.md / tools.md / README.md） |
| GET  | `/v1/skills/:name/:version/download` | 无 | 302 到 GitHub Releases zip URL |
| GET  | `/v1/categories` | 无 | 分类列表 |
| POST | `/v1/admin/publish` | admin | 发布版本 |
| DELETE | `/v1/admin/skills/:name/:version` | admin | 撤回版本 |

### 5.2 响应通用结构

```json
{
  "ok": true,
  "data": { ... },
  "error": null
}
```

错误：
```json
{
  "ok": false,
  "data": null,
  "error": { "code": "NOT_FOUND", "message": "skill not found" }
}
```

### 5.3 鉴权

- 读 API：完全公开
- 写 API：HTTP header `Authorization: Bearer <admin_token>`，token 存在 Worker
  secret `ADMIN_TOKEN`，由 GitHub Action 持有

---

## 6. 安装/卸载流程（Agent 主导）

详见 [skills-v2-agent-protocol.md](./skills-v2-agent-protocol.md)。本节给概览。

### 6.1 用户视角

1. 用户在 cicy-code UI 的 Skill Market 中点击「安装 cf-tunnel」
2. UI 状态变为「正在安装」
3. 当前 active agent 收到一条消息（看起来像用户发的）：
   > 请帮我安装 cf-tunnel skill。执行 `cicy-code skill install cf-tunnel --json`，
   > 完成后告诉我结果。
4. Agent 执行命令、解析输出、回复用户
5. UI 通过 SSE/poll 收到完成信号，状态变「已安装」

### 6.2 系统视角

```
┌──────────┐   POST /api/skill-market/cf-tunnel/install
│  UI      │ ──────────────────────────────┐
└──────────┘                               ↓
                                  ┌────────────────┐
                                  │  cicy-code mgr │
                                  └────────┬───────┘
                                           │ chatbus 注入消息
                                           ↓
                                  ┌────────────────┐
                                  │ Active Agent   │
                                  │  (claude/...)  │
                                  └────────┬───────┘
                                           │ 调子命令
                                           ↓
                                  ┌────────────────┐
                                  │ cicy-code skill│
                                  │     install    │
                                  └────────┬───────┘
                          ┌────────────────┼────────────────┐
                          ↓                ↓                ↓
                   ┌────────────┐   ┌────────────────┐   ┌────────────┐
                   │  Registry  │   │ GitHub         │   │ ~/cicy-ai/ │
                   │  (Worker)  │   │ Releases (zip) │   │  skills/   │
                   └────────────┘   └────────────────┘   └────────────┘
                                           │ 完成
                                           ↓
                                  POST /api/skill-market/.../installed
                                           ↓
                                  ┌────────────────┐
                                  │  cicy-code mgr │ → SSE 推 UI
                                  └────────────────┘
```

### 6.3 后端改动

```go
// /api/skill-market/:name/install (POST)
//   旧：直接拷贝文件
//   新：发 IM 消息给 active agent, 标记 pending, 返回 task id

// 新增：
//   POST /api/skill-market/:name/installed   ← agent 完成后回调
//   POST /api/skill-market/:name/uninstalled
//   POST /api/skill-market/:name/failed
```

---

## 7. Agent 扩展性

### 7.1 agents.json

`~/cicy-ai/skills/agents.json`（首次安装由 cicy-code 写入默认值）：

```json
{
  "agents": [
    {
      "id": "claude",
      "name": "Claude Code",
      "skills_dir": "~/.claude/skills",
      "manifest_file": "SKILL.md",
      "detect": { "command": "claude", "version_flag": "--version" }
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

新增 agent 只需追加一条记录。

### 7.2 兼容性声明

每个 skill 在 `manifest.json` 中：

```json
"compatible_agents": ["*"]                    // 通用（默认）
"compatible_agents": ["claude", "codex"]      // 仅特定 agent
```

`cicy-code skill list --agent kiro` 会按这个字段过滤。

---

## 8. 迁移计划

### 8.1 现有 Go skill 迁移清单

| 旧 alias | 新 skill | 优先级 | 难度 |
|----------|----------|--------|------|
| `cping` | skills/cping | P0 | 极简 |
| `cf` | skills/cf | P0 | 简 |
| `cf-tunnel` | skills/cf-tunnel | P0 | 中 |
| `email` | skills/email | P0 | 简 |
| `globalApiToken` | skills/globalApiToken | P0 | 极简 |
| `cicy-todo` | skills/cicy-todo | P0 | 简（已是 bash） |
| `agent-chrome` | skills/agent-chrome | P1 | 中 |
| `agent-code-server` | skills/agent-code-server | P1 | 中 |
| `agent-desktop` | skills/agent-desktop | P1 | 中 |
| `agent-webpage` | skills/agent-webpage | P1 | 中 |
| `frp-client` | skills/frp-client | P1 | 中 |
| `frp-server` | skills/frp-server | P1 | 中 |
| `cicy-mihomo` | skills/cicy-mihomo | P1 | 中 |
| `cicy-agent` | skills/cicy-agent | P1 | 中 |
| `cicy-ssh` | skills/cicy-ssh | P1 | 中 |
| `aliyun-cli` | skills/aliyun-cli | P1 | 简 |
| `google` | skills/google | P2 | 难（npm 依赖） |
| `gpt-chat` / `gemini-ask` / `eng` | skills/* | P2 | 中 |
| `mysql-exec` | skills/mysql-exec | P2 | 中 |
| `tg` | skills/tg | P2 | 中 |

迁完后下线：`cicy-skills` Go CLI、`cicy-hosttools` Go CLI、`agentgenApprovedMarketSkills`、`hosttoolAliasSet`。

### 8.2 阶段

| Phase | 内容 | 工时 |
|-------|------|------|
| 1 | 协议 + 仓库骨架 + tools | 1 天 |
| 2 | CF Worker 读+写 API | 1.5 天 |
| 3 | cicy-code 内置 skill-installer | 2 天 |
| 4 | cicy-code 后端改造（agent 主导安装） | 1 天 |
| 5 | 发布流水线 (GitHub Action) | 0.5 天 |
| 6 | P0 skill 全量 Node 化 + 联调 | 2 天 |
| 7 | P1 skill 迁移 | 3 天 |
| 8 | P2 skill 迁移 + 下线 Go | 2 天 |
| 9 | 文档 + 公测 | 1 天 |
| **合计** | | **14 天** |

### 8.3 过渡兼容

- **双轨期 ~2 周**：旧 Go skill 保留可用，新 Node skill 标记 v2，UI 优先选新版
- **强制切换**：cicy-code 启动时检测旧版自动迁移，UI 隐藏旧 skill
- **下线**：删 Go skill 代码，`~/.local/bin/<old-name>` 被新 symlink 覆盖

---

## 9. 安全考虑

### 9.1 完整性

- 每个 zip 都有 sha256 在 manifest 里
- `cicy-code skill install` 下载后强制校验 sha256
- HTTPS + Cloudflare 提供传输安全

### 9.2 配置保护

- `~/cicy-ai/db/*.json` 强制 0600
- `cicy-code skill doctor <name>` 检查权限
- skill 源码不应直接 `console.log` 任何 secret 字段
- manifest 在 `secret_fields` 里声明哪些字段是机密

### 9.3 npm 依赖

- 必须有 `package-lock.json`
- 安装时 `npm ci --omit=dev --ignore-scripts`（禁用 install 钩子）
- PR validate 时跑 `npm audit`

### 9.4 v2 暂不做

- 沙箱 / 容器隔离
- 二进制签名（v1 用 sha256 + HTTPS）
- 私有 / 付费 skill

未来 v3 可考虑。

---

## 10. 开放问题

- [ ] cicy-code 的 IM 消息注入路径（chatbus）能否做到对所有 agent provider 一致？
- [ ] 不同 agent 的 SKILL.md 解析能力是否完全一致？需要 fallback 策略？
- [ ] kiro-cli 的 skill 路径是 `~/.kiro/skills` 还是其它？需要确认。
- [ ] 第三方仓库的鉴权（私有 repo / 高 rate limit）是否在 v2 版本支持？v1 仅公开 repo
- [ ] GitHub Releases 出口流量限制（无认证 5000 req/h）是否需要 CDN 镜像？

---

## 11. 相关文档

- [skills-v2-manifest.md](./skills-v2-manifest.md) — manifest schema 完整规范
- [skills-v2-agent-protocol.md](./skills-v2-agent-protocol.md) — agent 安装协议
