# Gateway Inject Rules Files (CLAUDE.md / AGENTS.md)

## 背景

`CLAUDE.md` / `AGENTS.md` 当前只在 agent 启动时由 agent 自己读取。问题：

- agent 行为不可控：模型可能选择性忽略文件内容
- @-import 链脆弱：链路上任何 prose 引用都会断掉继承
- OpenCode 不支持 `@<path>` 自动解析，等于继承失效
- 用户在 chat 里说"忘掉规则"模型可能服从

**目标**：在 `ai_gateway` 转发请求前，把 agent 相关的所有 `CLAUDE.md` / `AGENTS.md` 文件**完整内容**收集起来，注入到 system prompt 里。每次请求都注入，无法被用户对话稀释。

**适用范围**：只覆盖走本地 gateway（`use_custom_gateway=true`）的 agent。非本地 gateway 用 mitm proxy 是另一条路，v1 不做。

## 方案

### 数据流

```
请求进入 cicy-code gateway
       ↓
agentInspectorRewriteRequestBody(provider, agentID, body)
       ↓
agentInspectorInjectPrompt(body, provider, agentID)
       ↓
agentInspectorBuildPromptOverlay(agentID)
       │
       ├── 现有：global/project/agent memory（prompt_rules 表）
       └── 新增：agentInspectorBuildRulesFilesOverlay(agentID)
              ↓
       collectRulesFiles(agentID)
              ↓
       [project root, master 链..., self] × {CLAUDE.md, AGENTS.md}
              ↓
       去重 + 大小裁剪 + 包裹 XML 标签
              ↓
       拼接成 overlay 字符串
       ↓
拼到 body.system / body.messages[0] / body.instructions
       ↓
转发到上游
```

### Map 收集规则

给定 `agentID`，按顺序收集（project → workspace）。**仅两层**，不走 master 链，不读 user-global：

1. **Project 层**（按 pane 动态解析）：
   - 读 `agent_config.config` 的 JSON，取 `projects[0]` 或 `project` 字段（已有 `agentInspectorProjectKey(agentID)` 可复用）
   - 收集 `<project>/CLAUDE.md` 和 `<project>/AGENTS.md`
   - 项目目录字段为空时整层跳过

2. **Workspace 层**（当前目录）：
   - `agent_config.workspace WHERE pane_id=?`
   - 收集 `<workspace>/CLAUDE.md` 和 `<workspace>/AGENTS.md`
   - workspace 字段为空时整层跳过

**显式不做**（v1 范围外）：

- ⛔ master 链递归（继承靠文件内 `@<path>` 引用，agent 自己解析，gateway 不展开）
- ⛔ user-global 文件（`~/.claude/CLAUDE.md`、`~/cicy-ai/CLAUDE.md` 等）
- ⛔ `@<path>` 自动递归 inline

### 注入格式

每个文件包成独立 XML 块，附 `source` 属性给 debug 用：

```xml
<project-rules source="/home/cicy/projects/cicy-code/CLAUDE.md">
[full file content]
</project-rules>

<project-rules source="/home/cicy/projects/cicy-code/AGENTS.md">
[full file content]
</project-rules>

<workspace-rules pane="w-10018" source="/home/cicy/cicy-ai/workers/w-10018/CLAUDE.md">
[full file content]
</workspace-rules>

<workspace-rules pane="w-10018" source="/home/cicy/cicy-ai/workers/w-10018/AGENTS.md">
[full file content]
</workspace-rules>
```

### 边界处理

| 情形 | 处理 |
|---|---|
| 文件不存在 | 静默跳过 |
| 文件 > 50KB | 截断 + 末尾 `... [truncated NNN bytes]` |
| 总 overlay > 100KB | 优先丢 project 层、保留 workspace 层 |
| 同一绝对路径出现多次（project == workspace 时） | 去重（按 abs path），只保留一份 |
| `agent_config.config.projects[0]` 为空 | 整 project 层跳过 |
| `agent_config.workspace` 为空 | 整 workspace 层跳过 |
| 路径越权（`@/etc/passwd`） | v1 不解析 @-import，无此风险 |

### 开关（全局 + per-agent 双层）

**全局开关**：环境变量 `CICY_GATEWAY_INJECT_RULES`（默认 `1`/on）。设为 `0` 全部禁用，紧急回滚用。

**单 agent 开关**：`agent_config.inject_rules_files INTEGER DEFAULT 1`。
- `1` → 注入（默认）
- `0` → 跳过文件注入（但 `prompt_rules` 表 3 个 scope 仍正常注入）
- 通过 `/api/tmux/panes/<id>` PATCH 修改，UI 在 agent inspector 加 toggle

**优先级**：全局 off → 不注入；全局 on + per-pane off → 不注入；全局 on + per-pane on → 注入。两者 AND 关系。

### 性能

- 每次请求都读文件偏贵 → 加 in-memory cache，key=path，value=(mtime, size, content)
- cache invalidation：每次请求 `os.Stat(path)` 检查 (mtime, size) 都没变才命中
- 文件总量 ≤ 几十 KB，读盘 < 10ms，可接受

### 与既有机制的关系

```
最终注入到 system 的内容（按顺序）：

<global-memory>...</global-memory>              ← prompt_rules.scope=global（保留，关键）
<project-memory>...</project-memory>            ← prompt_rules.scope=project
<agent-memory>...</agent-memory>                ← prompt_rules.scope=agent
<project-rules source=...>...</project-rules>   ← 新增：项目目录（按 pane 动态）
<workspace-rules pane=... source=...>...</workspace-rules>  ← 新增：当前 workspace
```

`prompt_rules` 表机制完全不动，文件注入在其后追加。`<global-memory>` 是用户级强约束（如"中文回复"、"不准乱重启服务"），必须保留。

### Inspector UI 支持

`/api/agents/inspector/<paneID>` 返回的 bundle 里增加字段：

```json
{
  "prompt_rules": { ... 旧的 ... },
  "rules_files": {
    "items": [
      { "source": "/.../CLAUDE.md", "scope": "project", "bytes": 1234, "truncated": false },
      ...
    ],
    "total_bytes": 5678
  },
  "overlay_preview": "..."   ← 老字段，但现在包含完整注入内容
}
```

前端 Inspector 模态框加一个 tab 或 section："注入文件 (N 个，X KB)"，展开显示每个文件来源 + 字节数。

## TODO

### 后端

- [x] **T1** 新增 `agentInspectorCollectRulesFiles(agentID) []rulesFile` 函数：
  - 输入 `agentID`，输出有序 `[{Path, Scope, PaneID, Content, Truncated, Bytes}, ...]`
  - 收集 project（按 `agentInspectorProjectKey` 解析）+ workspace 两层
  - 同一绝对路径去重（project == workspace 时只保留一份）
  - 单文件截断 50KB，总量截断 100KB
- [x] **T2** 新增 `agentInspectorBuildRulesFilesOverlay(agentID) string`：
  - 调 T1 拿文件列表
  - 按 XML 标签格式拼接
  - 返回最终字符串
- [x] **T3** 修改 `agentInspectorBuildPromptOverlay(agentID)`：
  - 在现有 `<global-memory>/<project-memory>/<agent-memory>` 之后追加 T2 的输出
  - 保持 strings.TrimSpace 不变其它行为
- [x] **T4** 加 in-memory cache：
  - `var rulesFileCache sync.Map` (key=path, value={mtime, content})
  - 读文件前 stat 检查 mtime
- [x] **T5** 修改 inspector bundle (`/api/agents/inspector/<paneID>`)：
  - 加 `rules_files` 字段，给前端展示用
- [x] **T6** 单测 `api/mgr/gateway_inject_rules_test.go`（9 个用例）：
  - 文件不存在路径 → 跳过
  - project 字段为空 → 整层跳过（workspace 必须匹配 `/workers/w-\d+$` 才触发）
  - workspace 字段为空 → 整层跳过
  - project == workspace（同一目录）→ 去重
  - 单文件截断
  - 总量截断（project 层先丢，workspace 层保留）
  - 缓存 mtime 变化重读
  - 全局开关 off
  - 单 agent 开关 off

### 前端

- [x] **T7** Inspector 模态框加"注入文件"section：
  - 列出每个 source 路径 + 字节数 + truncated 标记
  - 点击可展开看完整内容
- [x] **T8** Workspace.tsx 加"记忆"tab（Inspector Memory tab embedded）：
  - 取 `overlay_full` 字段渲染，可复制

### 文档

- [x] **T9** 更新 `~/projects/cicy-code/CLAUDE.md` 加一节"Gateway 注入规则"，说明：
  - 所有 CLAUDE.md / AGENTS.md 会被自动注入到 system prompt
  - 项目地图（如本文档）和约束规则混在一起；后续考虑拆 RULES.md
  - 临时禁用方法（如有 toggle）

### 配置

- [x] **T10**（可选）后端加 env / config 开关 `CICY_GATEWAY_INJECT_RULES=1`，默认 on，方便回滚

## 验收标准

### 功能验收

**A1** 给一个走本地 gateway 的 Claude pane（w-10001），发起一次 chat 请求：

- ✅ 抓 `~/cicy-ai/.cicy/ai-gateway-history/<agentID>/current.json`，确认 `system` 字段或 `messages[0]` 包含 `<project-rules source="/home/cicy/projects/cicy-code/CLAUDE.md">...</project-rules>` 块
- ✅ 内容是 CLAUDE.md 原文，未被改写

**A2** 给一个走本地 gateway 的 Codex pane，发起一次请求：

- ✅ 注入内容包含 `<project-rules source=".../AGENTS.md">...</project-rules>` 和 `<project-rules source=".../CLAUDE.md">...</project-rules>`（项目目录两个都注入）
- ✅ 包含该 codex pane 自己 workspace 下的 AGENTS.md/CLAUDE.md（如果存在）

**A3** Inspector 返回的 `rules_files.items`：

- ✅ 包含 project / workspace 两层各最多两个文件（CLAUDE.md + AGENTS.md）
- ✅ 顺序正确：project → workspace
- ✅ `bytes` 字段非零，`truncated` 字段反映真实状态
- ✅ 当 project == workspace 时，只出现一份（去重）

**A4** 删除某个文件（如 `<workspace>/AGENTS.md`）：

- ✅ 注入不报错
- ✅ 该文件对应的块不出现在 system 里
- ✅ 其它块完整

**A5** 修改 `<project>/CLAUDE.md`：

- ✅ 下一次请求立刻反映新内容（cache 按 mtime 失效）
- ✅ 不需要重启 cicy-code

**A6**（关键）`prompt_rules.scope=global` 已配置且 `inject_on_request=true`：

- ✅ `<global-memory>...</global-memory>` 块仍然注入
- ✅ 出现在所有文件块之前
- ✅ 文件注入新增的内容不覆盖/遮蔽 global-memory

### 性能 / 稳定性验收

**B1** 连续发 10 次相同 agent 的请求：

- ✅ 文件只 stat 不读盘（除非 mtime 变化）；用 strace 或日志确认
- ✅ 单次注入逻辑耗时 < 5ms

**B2** 制造一个 1MB 大的 CLAUDE.md：

- ✅ 注入被截断到 50KB
- ✅ 末尾出现 `... [truncated NNN bytes]` 标记

**B3** 制造 project + workspace 文件总和 > 100KB：

- ✅ project 层先丢，workspace 层优先保留
- ✅ 总注入字节数 < 100KB

**B4** 无 master 链（v1 不做）；保留位以备 v2 加 master 链时复用条目编号

### 边界验收

**C1** 给一个走非本地 gateway 的 agent（`use_custom_gateway=false`）发请求：

- ✅ 注入不发生（请求未经过 gateway）
- ✅ agent 行为不受影响

**C2** Agent 没有 `config.projects[0]`：

- ✅ project 层跳过
- ✅ workspace 层正常

**C3** Agent workspace 字段为空：

- ✅ workspace 层跳过
- ✅ project 层（如有）正常

**C4** prompt_rules 表里有规则且 `inject_on_request=true`：

- ✅ 既有 `<global-memory>` / `<project-memory>` / `<agent-memory>` 块继续注入
- ✅ 新文件块在其之后追加
- ✅ 两者不冲突，顺序：表的 3 个 scope → project 文件 → workspace 文件

**C5** project 目录与 workspace 目录相同（agent 在项目根下工作）：

- ✅ 同一文件不重复注入（abs path 去重）
- ✅ 标签从 `<project-rules>` 还是 `<workspace-rules>` 出来要确定一致（建议优先 `<project-rules>` 避免歧义）

### UI 验收

**D1** Agent Inspector 模态框打开：

- ✅ 看到"注入文件"section
- ✅ 列出 N 个 source 路径 + 字节数
- ✅ 总字节数显示正确

**D2** 点击某个 source：

- ✅ 展开看到该文件的完整内容（或截断后的内容）
- ✅ 有 truncated 标记的话明确显示

**D3** 修改文件 → Inspector 刷新：

- ✅ 字节数 / 内容跟着变

## 风险与回滚

| 风险 | 缓解 |
|---|---|
| Token 成本爆炸 | Anthropic prompt caching 命中后摊薄；监控总注入字节数 |
| 文件内容包含越权信息（如 API key）泄漏到上游 | 用户责任；文档警告"不要在 CLAUDE.md 写敏感信息" |
| 与用户 prompt 冲突让模型困惑 | system 优先级最高，模型一般偏向 system；measure 后再优化 |
| 性能回退 | T10 的 env 开关一关回滚 |

## 后续迭代（v2+ 不在本次范围）

- Section 标记（`<!-- @inject:begin/end -->`）只注入选定部分
- 拆分 `CLAUDE-RULES.md` / `AGENTS-RULES.md` 双文件方案
- `@<path>` 递归 import resolver
- 跨机器同步（远端 master 的文件）
- mitm proxy 路径以覆盖非本地 gateway 场景
- Per-request `X-No-Inject-Rules: 1` 临时禁用

## 实施估算

| 任务 | 难度 | 预计 |
|---|---|---|
| T1+T2+T3+T4 | 中 | 2-3 小时 |
| T5 | 易 | 30 分钟 |
| T6 | 中 | 1-2 小时 |
| T7+T8 | 中 | 2 小时 |
| T9+T10 | 易 | 30 分钟 |
| 测试 + 验收 | — | 1-2 小时 |
| **合计** | | **半天到一天** |
