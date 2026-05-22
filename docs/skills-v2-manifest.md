# Cicy Skills v2 — Manifest Schema

**Status:** Draft → In Progress
**Owner:** cicy-ai
**Last updated:** 2026-05-22

本文档详细规定每个 skill 的 `manifest.json` 字段语义、校验规则，以及
Registry API 的请求/响应 schema。

---

## 1. manifest.json 完整字段表

### 1.1 顶层字段

| 字段 | 类型 | 必需 | 默认值 | 说明 |
|------|------|------|--------|------|
| `$schema` | string | 否 | — | 指向 schema URL，IDE 自动补全用 |
| `name` | string | **是** | — | skill 名，必须等于目录名，正则 `^[a-z][a-z0-9_-]*$`，长度 ≤ 64 |
| `version` | string | **是** | — | semver，格式 `X.Y.Z[-prerelease][+build]` |
| `title` | string | **是** | — | 显示名（人读） |
| `description` | string | **是** | — | 一句话描述（≤ 200 字符），UI 卡片文案 |
| `category` | string | **是** | — | 见 §2 类目枚举 |
| `tags` | string[] | 否 | `[]` | 自由 tag，搜索用 |
| `author` | string | **是** | — | 作者名 / 组织名 |
| `homepage` | string (uri) | 否 | — | 主页 URL |
| `license` | string | **是** | — | SPDX 标识符（`MIT`、`Apache-2.0`、…） |
| `runtime` | object | **是** | — | 见 §1.2 |
| `system_requirements` | string[] | 否 | `[]` | 必需的系统命令（`curl`、`jq`…），见 §1.3 |
| `npm_dependencies` | bool | 否 | `false` | 是否需要安装 node_modules |
| `entry` | string | **是** | — | 主入口路径（相对仓库根），通常 `bin/<name>` |
| `bin_aliases` | string[] | 否 | `[]` | 额外暴露的命令名（symlink 同入口） |
| `config` | object | 否 | — | 见 §1.4 |
| `permissions` | string[] | 否 | `[]` | 见 §1.5 |
| `compatible_agents` | string[] | 否 | `["*"]` | 兼容的 agent id（`*` = 全部） |
| `files` | object | 否 | — | 见 §1.6，文档文件路径 |
| `publish` | object | 否 | — | 由发布流水线注入，见 §1.7 |

### 1.2 runtime

```json
"runtime": {
  "node": ">=18"
}
```

| 字段 | 类型 | 必需 | 说明 |
|------|------|------|------|
| `node` | string | **是** | semver range，安装时强制校验 |

未来可扩展：`python`、`bun` 等，但 v1 仅支持 node。

### 1.3 system_requirements

字符串数组，列出必需的系统命令。`cicy-code skill install` 在安装前用 `which`
检查，若缺失则中止并提示安装。

```json
"system_requirements": ["curl", "jq", "ssh"]
```

每项必须是单个命令名（不允许带参数 / 路径）。

### 1.4 config

声明 skill 使用的持久化配置文件。

```json
"config": {
  "path": "~/cicy-ai/db/cf.json",
  "permissions": "0600",
  "secret_fields": ["api_token", "zone_id"],
  "schema": "config.schema.json"
}
```

| 字段 | 类型 | 必需 | 说明 |
|------|------|------|------|
| `path` | string | **是** | 配置文件路径，强制以 `~/cicy-ai/db/` 开头 |
| `permissions` | string | 否 | 文件权限（八进制），默认 `0600` |
| `secret_fields` | string[] | 否 | 敏感字段名列表，禁止 skill 输出到 stdout/stderr |
| `schema` | string | 否 | 同目录下的 JSON Schema 文件路径 |

### 1.5 permissions

声明性权限（v1 仅做记录，不做强制隔离）：

| 值 | 含义 |
|----|------|
| `network` | 访问外部网络 |
| `network:cicy-code` | 调用本地 cicy-code API |
| `filesystem:home` | 读写用户家目录 |
| `filesystem:db` | 读写 `~/cicy-ai/db/` |
| `filesystem:tmp` | 仅 `/tmp` |
| `process:spawn` | 启动子进程 |
| `process:tmux` | 操作 tmux session |
| `system:exec` | 任意 exec（高危） |

未声明视为不需要。UI 安装前展示权限清单。

### 1.6 files

声明文档文件路径，用于 Registry `/v1/skills/:name/:version/files/:file`：

```json
"files": {
  "skill_md": "SKILL.md",
  "help_md": "help.md",
  "tools_md": "tools.md",
  "readme": "README.md"
}
```

未声明的文件 Registry 不暴露（避免任意文件读取）。

### 1.7 publish（自动注入）

不要手写，由发布流水线（GitHub Action）在 zip 上传到 GitHub Releases 之后填入：

```json
"publish": {
  "published_at": "2026-05-22T04:00:00Z",
  "sha256": "abc123...",
  "size": 12345,
  "download_url": "https://github.com/cicy-ai/cicy-skills/releases/download/cping-v1.0.0/cping-1.0.0.zip",
  "source": {
    "type": "github",
    "repository": "cicy-ai/cicy-skills",
    "tag": "cping-v1.0.0",
    "commit": "a1b2c3d4..."
  },
  "signature": null
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `published_at` | ISO 8601 | 发布时间（UTC） |
| `sha256` | string | zip 包 sha256（hex） |
| `size` | int | zip 字节数 |
| `download_url` | string (uri) | 实际 zip 下载地址（GitHub Releases） |
| `source.type` | enum | `github` / `url` / `git`，标识来源类型 |
| `source.repository` | string | `<owner>/<repo>`，type=github 时必填 |
| `source.tag` | string | git tag，type=github 时必填 |
| `source.commit` | string | 发布时的 git commit sha（可追溯） |
| `signature` | string \| null | 预留 GPG 签名（v2） |

---

## 2. 类目枚举

`category` 字段必须为下列之一：

| category | 描述 |
|----------|------|
| `network` | 网络工具（cping、cf、frp） |
| `cloud` | 云服务（aliyun、cf-tunnel） |
| `ai` | AI 集成（gpt-chat、gemini-ask） |
| `dev` | 开发工具（agent-code-server、cicy-todo） |
| `system` | 系统管理（cicy-mihomo、proxy_ssh） |
| `productivity` | 生产力（email、google） |
| `agent` | Agent 控制（agent-chrome、agent-desktop、agent-webpage） |
| `infra` | 基础设施（hk-spot-dev、us-spot-dev） |
| `other` | 兜底 |

UI 按 category 分组展示。

---

## 3. JSON Schema 全文

文件位置：`schemas/manifest.schema.json`，由 Registry 在
`https://skills.cicy-ai.com/v1/manifest.schema.json` 静态提供。

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "https://skills.cicy-ai.com/v1/manifest.schema.json",
  "title": "Cicy Skill Manifest",
  "type": "object",
  "required": ["name", "version", "title", "description", "category", "author", "license", "runtime", "entry"],
  "properties": {
    "$schema": { "type": "string", "format": "uri" },
    "name": {
      "type": "string",
      "pattern": "^[a-z][a-z0-9_-]*$",
      "maxLength": 64
    },
    "version": {
      "type": "string",
      "pattern": "^\\d+\\.\\d+\\.\\d+(-[A-Za-z0-9.-]+)?(\\+[A-Za-z0-9.-]+)?$"
    },
    "title": { "type": "string", "minLength": 1, "maxLength": 100 },
    "description": { "type": "string", "minLength": 1, "maxLength": 200 },
    "category": {
      "type": "string",
      "enum": ["network", "cloud", "ai", "dev", "system", "productivity", "agent", "infra", "other"]
    },
    "tags": { "type": "array", "items": { "type": "string" }, "default": [] },
    "author": { "type": "string", "minLength": 1 },
    "homepage": { "type": "string", "format": "uri" },
    "license": { "type": "string", "minLength": 1 },
    "runtime": {
      "type": "object",
      "required": ["node"],
      "properties": {
        "node": { "type": "string" }
      }
    },
    "system_requirements": {
      "type": "array",
      "items": { "type": "string", "pattern": "^[a-zA-Z0-9_-]+$" },
      "default": []
    },
    "npm_dependencies": { "type": "boolean", "default": false },
    "entry": { "type": "string", "pattern": "^bin/" },
    "bin_aliases": {
      "type": "array",
      "items": { "type": "string", "pattern": "^[a-z][a-z0-9_-]*$" },
      "default": []
    },
    "config": {
      "type": "object",
      "required": ["path"],
      "properties": {
        "path": { "type": "string", "pattern": "^~/cicy-ai/db/" },
        "permissions": { "type": "string", "pattern": "^0[0-7]{3}$", "default": "0600" },
        "secret_fields": { "type": "array", "items": { "type": "string" } },
        "schema": { "type": "string" }
      }
    },
    "permissions": {
      "type": "array",
      "items": {
        "type": "string",
        "enum": [
          "network", "network:cicy-code",
          "filesystem:home", "filesystem:db", "filesystem:tmp",
          "process:spawn", "process:tmux", "system:exec"
        ]
      },
      "default": []
    },
    "compatible_agents": {
      "type": "array",
      "items": { "type": "string" },
      "default": ["*"]
    },
    "files": {
      "type": "object",
      "properties": {
        "skill_md": { "type": "string" },
        "help_md": { "type": "string" },
        "tools_md": { "type": "string" },
        "readme": { "type": "string" }
      }
    },
    "publish": {
      "type": "object",
      "properties": {
        "published_at": { "type": "string", "format": "date-time" },
        "sha256": { "type": "string", "pattern": "^[a-f0-9]{64}$" },
        "size": { "type": "integer", "minimum": 0 },
        "download_url": { "type": "string", "format": "uri" },
        "source": {
          "type": "object",
          "properties": {
            "type": { "type": "string", "enum": ["github", "url", "git"] },
            "repository": { "type": "string" },
            "tag": { "type": "string" },
            "commit": { "type": "string", "pattern": "^[a-f0-9]{7,64}$" }
          }
        },
        "signature": { "type": ["string", "null"] }
      }
    }
  },
  "additionalProperties": false
}
```

---

## 4. 校验规则（tools/validate-skill.js）

除了 JSON Schema 校验，还要做：

### 4.1 文件存在性

- `entry` 指向的文件必须存在且可执行
- 该文件必须以 `#!/usr/bin/env node` 开头
- `files.skill_md`、`files.help_md` 等指向的文件必须存在
- 若 `npm_dependencies: true`，必须有 `package.json` + `package-lock.json`

### 4.2 SKILL.md frontmatter

```markdown
---
name: <必须等于 manifest.name>
description: <必须等于 manifest.description>
---
```

### 4.3 大小限制

- 单 skill 解压后 ≤ 10 MB（含 node_modules）
- 单 skill zip ≤ 5 MB
- 超过需在 manifest 显式声明 `large: true`（v1 不允许）

### 4.4 安全检查

- `bin/<name>` 不允许写硬编码绝对路径（`/home/<user>/...`）
- `package.json` 的 `scripts.preinstall`/`postinstall`/`install` 不允许存在
- 不允许 `bin/<name>` 内有 `eval(...)` / `Function(...)` 调用（静态扫描）

---

## 5. Registry API 详细 schema

### 5.1 GET /v1/skills

请求：
```
GET /v1/skills?q=tunnel&category=network&agent=claude&limit=50&offset=0
```

响应：
```json
{
  "ok": true,
  "data": {
    "skills": [
      {
        "name": "cf-tunnel",
        "version": "1.2.0",
        "title": "Cloudflare Tunnel",
        "description": "...",
        "category": "network",
        "tags": ["cloudflare"],
        "author": "cicy-ai",
        "license": "MIT",
        "compatible_agents": ["*"],
        "size": 12345,
        "published_at": "2026-05-22T04:00:00Z"
      }
    ],
    "total": 1,
    "limit": 50,
    "offset": 0
  }
}
```

### 5.2 GET /v1/skills/:name

响应：
```json
{
  "ok": true,
  "data": {
    "manifest": { /* 完整 manifest.json */ },
    "files": {
      "skill_md": "<内容字符串>",
      "help_md": "<内容字符串>",
      "tools_md": "<内容字符串>",
      "readme": "<内容字符串>"
    }
  }
}
```

UI 详情页直接用此响应渲染。

### 5.3 GET /v1/skills/:name/versions

```json
{
  "ok": true,
  "data": {
    "name": "cf-tunnel",
    "latest": "1.2.0",
    "versions": [
      { "version": "1.2.0", "published_at": "2026-05-22T04:00:00Z", "size": 12345 },
      { "version": "1.1.0", "published_at": "2026-05-15T00:00:00Z", "size": 12000 },
      { "version": "1.0.0", "published_at": "2026-05-01T00:00:00Z", "size": 11500 }
    ]
  }
}
```

### 5.4 GET /v1/skills/:name/:version/download

返回 `302 Found`，`Location` 头指向 manifest.publish.download_url（即 GitHub
Releases 资产的直链 URL）。Worker 自身不存储任何二进制内容。

例：
```
HTTP/1.1 302 Found
Location: https://github.com/cicy-ai/cicy-skills/releases/download/cping-v1.0.0/cping-1.0.0.zip
Cache-Control: public, max-age=300
```

也可以直接用 `manifest.publish.download_url` 跳过这一步，client 习惯直接拿
manifest 里的 URL 自己 curl。

### 5.5 POST /v1/admin/publish

**注意：v2 不再上传 zip 到 Worker。** zip 由 GitHub Releases 存储；本接口仅
注册元数据。

请求：

```
POST /v1/admin/publish
Authorization: Bearer <admin_token>
Content-Type: application/json
```

请求体：
```json
{
  "manifest": { /* 完整 manifest.json，含已填好的 publish 字段 */ },
  "verify": {
    "download_url": "https://github.com/cicy-ai/cicy-skills/releases/download/cping-v1.0.0/cping-1.0.0.zip",
    "sha256": "abc123...",
    "size": 12345
  }
}
```

Worker 端处理：
1. 校验 admin token
2. 解析 manifest，按 §3 schema 校验
3. 校验 `manifest.publish.download_url == verify.download_url`、
   `manifest.publish.sha256 == verify.sha256`
4. （可选）Worker 主动 HEAD 一次 `download_url` 确认 GitHub 资产可达且
   `Content-Length == verify.size`，失败则返回 422 Unprocessable
5. 写 KV：
   - `skill:<name>:<version>` = manifest（含 publish 字段）
   - 更新 `skill:<name>:versions`
   - 更新 `skill:<name>:latest`（若 version > 当前 latest）
   - 更新 `catalog`（若是新 skill）
6. 返回：
```json
{
  "ok": true,
  "data": {
    "name": "cf-tunnel",
    "version": "1.2.0",
    "manifest_url": "https://skills.cicy-ai.com/v1/skills/cf-tunnel/1.2.0",
    "download_url": "https://github.com/cicy-ai/cicy-skills/releases/download/cf-tunnel-v1.2.0/cf-tunnel-1.2.0.zip"
  }
}
```

幂等性：如果 `<name>:<version>` 已存在且 sha256 一致 → 200 OK；不一致 →
409 Conflict。

### 5.6 DELETE /v1/admin/skills/:name/:version

撤回单个版本（不删 GitHub Releases 资产，只把 KV 中标记为 yanked，安装时报错；
管理员可在 GitHub 端手动删除 release asset）。

请求：
```
DELETE /v1/admin/skills/cf-tunnel/1.0.0
Authorization: Bearer <admin_token>
```

响应：
```json
{
  "ok": true,
  "data": { "name": "cf-tunnel", "version": "1.0.0", "yanked": true }
}
```

---

## 6. installed.json 本机清单 schema

`~/cicy-ai/skills/installed.json`：

```json
{
  "schema_version": 1,
  "skills": [
    {
      "name": "cf-tunnel",
      "version": "1.2.0",
      "installed_at": "2026-05-22T04:30:00Z",
      "source": {
        "type": "registry",
        "download_url": "https://github.com/cicy-ai/cicy-skills/releases/download/cf-tunnel-v1.2.0/cf-tunnel-1.2.0.zip",
        "repository": "cicy-ai/cicy-skills"
      },
      "sha256": "abc123...",
      "agents_synced": ["claude", "codex", "opencode", "kiro"]
    },
    {
      "name": "my-tool",
      "version": "0.1.0",
      "installed_at": "2026-05-22T05:00:00Z",
      "source": {
        "type": "github",
        "repository": "user/my-tool",
        "ref": "main",
        "download_url": "https://github.com/user/my-tool/archive/refs/heads/main.zip"
      },
      "sha256": "def456...",
      "agents_synced": ["claude"]
    }
  ]
}
```

`source.type` 枚举：

| 值 | 含义 | 使用命令 |
|----|------|----------|
| `registry` | 官方索引 cicy-ai/cicy-skills | `cicy-code skill install <name>` |
| `github` | 第三方 GitHub 仓库 | `cicy-code skill install --repo <owner/repo> <name>` |
| `url` | 自定义 zip URL | `cicy-code skill install --url <zip-url>` |
| `git` | 直接 git clone | `cicy-code skill install --git <git-url>` |
| `local` | 本地目录开发模式 | `cicy-code skill dev <path>` |
| `migrated` | v1 → v2 自动迁移 | （内部） |

更新策略由 `source.type` 决定：

- `registry` / `github` — `cicy-code skill update <name>` 检查最新版
- `url` — 没有更新机制（只能 reinstall 新 URL）
- `git` — 走 `git pull` 更新到最新 commit
- `local` — `cicy-code skill dev <path>` 重装
- `migrated` — 提示用户迁移到 registry 版

---

## 7. 版本约定

- 严格 SemVer 2.0.0
- 不允许 `0.0.0` 作为正式版本（保留给 dev 模式）
- 主版本 `0.x.y` 表示不稳定 API；`>=1.0.0` 表示稳定
- 相同 `<name>:<version>` 不允许重复发布（除非 sha256 一致幂等）
- yanked 版本仍保留在 versions 列表中，但 latest 跳过它

---

## 8. 示例：cping 完整 manifest

```json
{
  "$schema": "https://skills.cicy-ai.com/v1/manifest.schema.json",
  "name": "cping",
  "version": "1.0.0",
  "title": "China-side Reachability",
  "description": "Check network latency and reachability to a domain or IP from this host.",
  "category": "network",
  "tags": ["network", "ping", "latency"],
  "author": "cicy-ai",
  "homepage": "https://github.com/cicy-ai/cicy-skills/tree/main/skills/cping",
  "license": "MIT",
  "runtime": { "node": ">=18" },
  "system_requirements": [],
  "npm_dependencies": false,
  "entry": "bin/cping",
  "permissions": ["network"],
  "compatible_agents": ["*"],
  "files": {
    "skill_md": "SKILL.md",
    "help_md": "help.md",
    "tools_md": "tools.md",
    "readme": "README.md"
  }
}
```

发布后 Registry 会注入：
```json
"publish": {
  "published_at": "2026-05-22T04:00:00Z",
  "sha256": "f1a2b3c4...",
  "size": 8192,
  "download_url": "https://github.com/cicy-ai/cicy-skills/releases/download/cping-v1.0.0/cping-1.0.0.zip",
  "source": {
    "type": "github",
    "repository": "cicy-ai/cicy-skills",
    "tag": "cping-v1.0.0",
    "commit": "a1b2c3d4..."
  },
  "signature": null
}
```
