---
title: cicy Agent
description: 内置 headless agent 类型的完整机制 —— 提示词分层、人格架构、工具组、对话生命周期、历史压缩与记忆养成。
---
# cicy Agent

`cicy` 是内置的 **headless agent 类型**:不跑外部 CLI,由本地 AI 网关直接驱动(`agent_cicy.go` 的 turn loop)。它是平台上"角色型 agent"(助理、客服、协调官、桌面助手……)的运行时,一个 agent_type、多个 role,**一切个性化靠数据,不靠代码**。

和 CLI agent(`claude` / `codex` / `opencode`)的关系:cicy 把 CLI agent 的「system prompt 与 CLAUDE.md 二分」照搬了过来——system 字段只放角色模板,agent 自己的身份作为消息上下文携带。两者都字节稳定,以命中 prompt cache。

## 提示词分层

每轮请求的组装(`cicyRunWindowLocked`):

```
system    = role 模板的 system.md            (cicySystemBlocks,带 cache 断点)
messages  = 完整历史
            + <role> 块 → 第一条 user 消息   (AGENTS.md 正文,cicyInjectRoleContext)
            + <memories> 块 → 最后一条 user 消息(长期记忆,cicyInjectMemoryContext)
tools     = role 模板 meta.yaml 声明的工具组
```

三个注入都是 **wire-only**:持久化的对话历史不含任何注入块,注入发生在发请求的瞬间。`<role>` 在头部(字节稳定,吃缓存);`<memories>` 在尾部(记忆更新不打深前缀缓存,见下文)。

## 人格架构:三个地方,三种东西

| 层 | 位置 | 是什么 | 生效方式 |
| --- | --- | --- | --- |
| **role 模板** | `~/cicy-ai/memory/agents/<slug>/` | 可复用职责:`system.md`(system 字段)、`role.md`(创建时种进 AGENTS.md)、`meta.yaml`(名字/工具组/greeting) | `agent_config.role_template` 指向 slug,每轮实时读 |
| **AGENTS.md** | `<workspace>/AGENTS.md` | 这个 agent 的**个体身份**(`# Role` 段) | 每轮作为 `<role>` 块注入第一条 user 消息 |
| **agent_config.role** | SQLite `agent_config` 表 | **花名册标签 + 拓扑标记,不是人格** | 只用于 UI 列表;魔法值 `worker`(默认)/`master` 参与 master/worker 拓扑与完成度跟踪 |

::: warning `--role` 不设人格
`cicy-agent create --role <文本>` 只写 `agent_config.role` 这个标签列,**从不进 prompt**。往里填自由文本还会悄悄丢掉 `worker` 标记(完成度跟踪会跳过该 pane)。设人格用 `--role-template`;改个体身份直接编辑该 agent 的 `AGENTS.md # Role` 段(即时生效)。
:::

分工口诀:**可复用职责归模板,个体身份归 AGENTS.md,标签归 DB。**

## 工具

工具集完全由 role 模板 `meta.yaml` 的 `tools:` 声明(自定义 agent 则由其 `AGENT.md`)。工具组在 `~/cicy-ai/db/lite-config.json` 定义,改配置即生效,无需重编译:

- `core` = `shell` + `skill`(完整工作能力);
- 纯聊天角色声明空工具 → 该 agent 退化为纯对话。

## 对话生命周期

- **turn loop**:一条 user 消息 → (模型回复 → 工具执行 → 结果回传)循环,直到 tool-free 回答。最后一轮强制无工具收尾;
- **历史**:完整历史每轮原样发出,**不自动截断**。落盘在 `<workspace>/.cicy/history/`:`current.json`(完整展示历史)、`reply.json`(当前轮快照)、`current.<ts>.json`(compact/clear 前归档);
- **`/compact`**:显式触发,把整段对话折叠成一条结构化摘要(Claude Code 式分节)。阈值常量(`agent_cicy.go`):超 **300** 条消息可压缩、压缩时最近 **160** 条逐字保留、**600** 条为最后兜底硬砍上限;
- **`/clear`**:轮换 conversation id,历史清空——但人格(AGENTS.md)和长期记忆(见下)**不受影响**;
- **aux 旁路**:压缩摘要、记忆识别等内部 LLM 调用走 `X-Cicy-Aux` 头,只记用量、不写对话快照,永不污染 UI 历史。

## 记忆养成

cicy agent 自带**运行时长期记忆**(`agent_cicy_memory.go`),三条循环,借鉴 BaiLongma 的记忆池设计并做了文件化轻装:

### 写入:去抖批处理识别

每轮成功收尾后,该轮(user 问题 + 回答 + 用到的工具)进入内存缓冲;满足任一条件触发一次识别:

| 触发 | 阈值 | 说明 |
| --- | --- | --- |
| 空闲 | 5 分钟 | **刻意对齐 prompt cache TTL**:缓存冷却后更新记忆是免费的 |
| 攒满 | 6 轮 | 把"每轮一次 LLM"摊薄成"一批一次" |
| 兜底 | 20 分钟 | 最早一轮等待上限 |
| 显式 | 立即 | 用户说"记住 / 别忘了 / remember"直接 flush |

识别是一次 **aux LLM 调用**(用该 agent 自己的模型/凭据):判断全权交给模型——值得记的(稳定偏好、长期约束、关于用户及其项目环境的事实、高成本结论、可复用流程/教训)写下来;不值得的(易逝数据、临时会话指令、任务中间态、未确认猜测)明确不记。**临时指令 ≠ 长期偏好**是写死在 prompt 里的规则。

去重靠 **mem_id 语义命名**(`preference_color`、`lesson_proxy_localhost`……):识别时给出现有索引,命中同一 mem_id 即为更新而非新建。

### 存储:纯文件,人肉可读可改

```
<workspace>/.cicy/memory/
├── MEMORY.md            自动重建的索引(即注入文本)
├── <mem_id>.md          一条记忆一个文件
│                        frontmatter: mem_id/type/title/salience(1-5)/tags
└── _archive/            超限归档(软移除,不硬删)
```

类型:`preference | fact | person | knowledge | procedure | constraint | lesson`。超过 **120** 条时机械归档(低 salience + 陈旧优先)至保留 100 条。

### 读取:尾部 wire 注入

每轮发请求前,`MEMORY.md`(上限 6KB)作为 `<memories>` 块追加到**最后一条 user 消息**。选尾部是缓存工程:

- 深前缀(system + role + 几乎全部历史)字节不变 → 前缀缓存照常命中;
- 记忆块随本就未缓存的尾部重发,每轮固定几 KB,便宜一个数量级于头部注入(那会在每次记忆更新时作废整段历史缓存);
- 配合"空闲 5 分钟才 flush",绝大多数记忆更新落在缓存已冷却的窗口——净成本约等于零。

### 效果

`/clear`、压缩、重启都冲不掉记忆;agent 跨会话记得用户的偏好和环境,并会主动运用。验证方式:记入一条事实 → `/clear` 清空历史 → 提问——答案只可能来自记忆注入。

## 创建与管理

```sh
cicy-agent create <标题> --type cicy --role-template <slug>   # 人格选模板
# 改个体身份:编辑 <workspace>/AGENTS.md 的 # Role 段(即时生效)
# 或走 API:PUT /api/memory/templates/agent/<paneID>
```

role 模板可自建:在 `~/cicy-ai/memory/agents/<slug>/` 放 `system.md` + `role.md` + `meta.yaml` 即可被 `--role-template <slug>` 引用,无需注册。

## 相关

- 模板三层组装与项目隔离 → [记忆与模板](/concepts/memory)
- agent 与 pane 的关系 → [Agent 与 Pane](/concepts/agent-pane)
