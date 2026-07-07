---
title: 团队知识库
description: cicy-knowledge —— 团队二层知识库,canon(已核实事实)可召回,草稿不外露,知识专员审核晋升。
---
# 团队知识库

`cicy-knowledge` 是团队的**二层知识库**:把「团队约定、坑、过往决策」沉成**已核实、有出处的 canon**,任何 agent 动手前先召回,避免重复造轮子 / 重复踩坑。

## 心智模型:canon vs 草稿

知识有**成熟度**,只有 **canon** 会被召回:

```
draft(草稿)──提交──▶ pending(待审)──知识专员审核──▶ canon(正典) ──▶ deprecated(弃用)
```

- **recall 只搜 canon** —— 草稿 / 未成稿 / pending **永不出现**,所以召回回来的都是 settled fact,不是提案;
- 提交默认落 **pending**,由**知识专员**(`cicy` 角色之一)审核 → 晋升 canon / 打回;
- 成熟度标记:`draft` / `promote` / `reject` / `supersede`(新条替代旧条)/ `deprecate`。

## 命令

```bash
cicy-knowledge recall <keyword>      # 关键词 + 标签召回(只出 canon)
cicy-knowledge get <id>              # 读一条全文
cicy-knowledge add "<title>" --body <md> [--tags "a b"]   # 提一条(→ pending)
cicy-knowledge add --draft ...       # 存草稿
cicy-knowledge promote <id>          # 晋升(专员)
cicy-knowledge reject <id>           # 打回
cicy-knowledge supersede <old> ...   # 用新条替代旧条
```

::: tip 召回是关键词+标签,不是向量 RAG
`recall` 走**关键词 + 标签**匹配 canon,不是向量检索 —— 命中靠标题/标签/正文关键词,所以**加好标签**很重要。
:::

## 什么时候用

- **动手前**:做团队可能已知的事(坑、约定、规范、决策)之前先 `recall`;
- **完成后**:有复用价值的结论 → `cicy-knowledge add`(而不是散一个 `.md`);草稿 → `--draft`;
- 审计日志 / 敏感内容是证据、同一信任域,**别外泄**。

## 存哪

后端 `/api/knowledge/*`;召回搜的是 canon 层。它和 [记忆模板](/concepts/memory) 不同:记忆是**每个 agent 自己的 guidance**,知识库是**全团队共享的可召回事实**。
