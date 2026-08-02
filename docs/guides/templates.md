---
title: 项目与角色模板
description: 用角色模板复用「前端/后端/测试」agent,用项目模板做到不同项目记忆独立 —— 一次定义,处处复用,互不串味。
---
# 项目与角色模板

这是 cicy-code 最有价值的用法之一:**agent 复用 + 项目记忆独立**。目标 —— 定义一次「前端 / 后端 / 测试」这样的稳定 agent 模板,任何项目都能用它起 agent,且不同项目之间会话/记忆完全隔离。

## 两个原语

| 你要的 | 用什么 | 放哪 |
| --- | --- | --- |
| **复用**(稳定 agent 模板) | **角色模板 role** | `~/cicy-ai/memory/agents/<slug>/` |
| **隔离**(项目记忆独立) | **项目模板 project** | `~/cicy-ai/memory/projects/<slug>.md` |

因为 agent 的 guidance = `global + 项目 + 角色`,**创建时组装、写进各自 workspace**([记忆与模板](/concepts/memory)),所以:同一个「前端」角色在不同项目各起一个 agent,就是各自独立的 workspace / 记忆 / 对话 —— 天然隔离。

## 一、做三个角色模板

每个角色一个目录,含:

```
~/cicy-ai/memory/agents/前端工程师/
├── role.md        # 英文人设 / 职责(落进 agent 的 CLAUDE.md)
├── role.zh.md     # 中文人设
├── system.md      # 系统提示(cicy 每轮的 system 字段)
└── meta.yaml      # name / name_zh / tools 白名单 / greeting / max_tool_rounds
```

### 用 `cicy-agent-role` 创建（推荐）

公共 Skill `cicy-agent-role` 会生成并校验上面的标准四文件结构，避免把角色模板与
`~/cicy-ai/agents/<slug>/AGENT.md` 的「自定义智能体」格式混用。

安装：

```sh
cicy-code skill install cicy-agent-role
```

准备一个 JSON spec：

```json
{
  "name": "Sales Assistant",
  "name_zh": "销售助手",
  "tools": ["core"],
  "greeting": "Hi, tell me which customer needs follow-up.",
  "greeting_zh": "你好，请告诉我需要跟进的客户。",
  "role": "# Role\n\nYou are a sales assistant. Confirm facts and never invent prices or commitments.",
  "role_zh": "# 角色\n\n你是销售助手。确认事实，不得编造价格或承诺。",
  "system": "You are a conversational work agent operating inside CiCy. Follow the selected role and report outcomes faithfully."
}
```

创建并校验：

```sh
cicy-agent-role create sales-assistant --spec ./sales-assistant.json
cicy-agent-role validate sales-assistant
cicy-agent-role list
```

默认输出目录：

```text
~/cicy-ai/memory/agents/sales-assistant/
├── meta.yaml
├── role.md
├── role.zh.md
└── system.md
```

`create` 默认拒绝覆盖已有模板。只有在检查现有文件并确认更新时才使用 `--force`。
创建完成后无需注册或重启，新模板会进入「创建 Agent」的角色模板列表。

`meta.yaml` 示例:

```yaml
profile: assistant
name: Frontend Engineer
name_zh: 前端工程师
tools: [core]
greeting: |
  I handle frontend components and integration.
greeting_zh: |
  我负责前端组件与联调。
```

`role.md` 里写清**职责边界**(比如"React/Vite 组件与联调,不实现后端接口")。后端、测试同理各建一个目录。

::: tip 想发给所有机器
把三个角色目录放进打包 seed `api/mgr/embed/memory-seed/agents/`,发版后**所有机器开箱即用**;只想本机用就放 `~/cicy-ai/memory/agents/`。
:::

## 二、每个项目建项目模板

```
~/cicy-ai/memory/projects/<项目名>.md
```

里面写这个项目的规则、技术栈、约束(会作为 project 层组合进该项目下每个 agent 的 guidance)。

## 三、起 agent

新项目来了:

1. 建 `projects/<项目名>.md`;
2. 用「前端 / 后端 / 测试」三个角色**各起一个 agent**,`project` 选该项目。

→ 3 个专职 agent,记忆各自独立;换个项目重复步骤,两项目之间互不串味。

## 可选:PM 编排 + fork

也可以让一个「项目 PM」agent 绑定项目,再**固定 fork** 出前端/后端/测试三个职能(fork 继承角色 + 项目)。这是编排层的糖 —— 底座仍是角色 + 项目模板。

## 相关

- [Fork 分身](/concepts/fork) · [定制记忆](/guides/memory-customize)
