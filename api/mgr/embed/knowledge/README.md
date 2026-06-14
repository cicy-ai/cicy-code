# 团队知识库（cicy team knowledge）

这是 cicy 团队的**唯一知识真相**——"团队踩过什么坑、定过什么红线、沉淀了什么做法、有哪些企业资料"，都在这里。**人和 agent 共同治理**，git 版本化、可审计、可回滚。

> 不是数据库、不是聊天记录、不是某个 agent 的私有草稿。这里只放**经核实、对团队有复用价值的决策知识**。

---

## 目录职责（各管一摊）

| 目录 / 文件 | 职责 | 谁写 |
|---|---|---|
| `_inbox/` | **待评审区**：新提案在此排队，未进正典前都不可信 | memory hook（agent 写 auto-memory 自动投递）/ harvest / 手动 `add` |
| `<域>/`（如 `deploy/` `gateway/` …） | **正典（canon）**：已核实、已去重、可信、可被全团队 recall | 知识专员 `promote` 后落此 |
| `_archive/` | **退役区**：被 reject 或被 supersede 的旧条目，留作历史不删 | 知识专员 `reject` / `supersede` |
| `docs/` | **企业文档素材**：上传的 PDF/Word/原始资料（git 不跟踪，见 .gitignore） | 人在「知识库」tab 上传 |
| `KNOWLEDGE.md` | 正典索引（可选，类似 MEMORY.md 的目录） | 知识专员维护 |

**治理状态 = 文件所在目录**：`_inbox`=待审、`<域>/`=正典、`_archive`=退役。没有隐藏的 DB 状态字段，一眼看目录就知道。

---

## 域（domain）口径

正典按域分文件夹，分类要一致：

| 域 | 装什么 |
|---|---|
| `deploy/` | 发版、构建、共享工作树、npm/CI、docker、热修 |
| `gateway/` | AI 网关、模型路由、MITM、审计接线 |
| `desktop/` | cicy-desktop（Electron、安装、签名、平台分叉） |
| `mihomo/` | 代理/出口/mihomo 配置与端口 |
| `skills/` | cicy-skills 体系、发版流水线、skill 约定 |
| `audit/` | 审计/安全/合规 |
| `agent/` | agent 编排、记忆、lite/工具配置、消息链路 |
| `cloud/` | cicy-cloud、企业 SSO、定价、落地页 |
| `product/` | 愿景、市场、A2A、商业策略 |
| `general/` | 一时归不进上面的，但确属团队工程知识 |
| `knowledge/` | 知识库自身的治理规则、使用指南 |

> **不属于团队工程知识的不收**（如某人的个人经历/偏好）——那是个人 agent memory，不进这里。

---

## 谁负责（责任人）

- **知识专员**（团队知识库的专家与负责人）：治 `_inbox`（核实 / 去重 / 归域 / 退役）、对外答疑（`recall` 出带源头的准话）、补缺。它是"团队知道什么"的权威。详见其角色：`~/cicy-ai/memory/agents/知识专员.md`。
- **人（Barry / 团队）**：在 cicy-code 的「知识库」tab(在 memory 左边)浏览/编辑/上传，可直接改文件；高破坏动作（批量清退、删企业文档）由人拍板。
- **memory hook（系统）**：任何 agent 写自己的 auto-memory → 网关 hook 自动把提案投进 `_inbox/` 并通知知识专员。源头自动汇集，不靠人记得上报。

---

## 怎么用（功能）

- **命令行 `cicy-knowledge`**（agent 和人都可用）：
  `add` 新增 · `list` 列表 · `get <id>` 看正文 · `recall <关键词/tag>` 检索正典 · `promote <id> --domain <域>` 升正典 · `reject <id>` 退役 · `supersede <old> <new>` 取代。
- **「知识库」tab**（cicy-code UI，memory 左边）：可视化浏览/编辑/上传企业文档、看 `_inbox` 待审角标。
- **recall = 关键字/标签 grep over markdown，不走向量 RAG**——靠治理过的 canon + 精确召回，从根上防幻觉。

---

## 原则

1. **文件为真相**：人和 agent 同改一套文件，所见即所得。
2. **不靠 RAG**：知识经治理后常驻、按需 recall；不做 embedding/chunk/re-rank。
3. **git 可审计可回滚**：每次变更一个 commit，谁改的、改了啥、何时，`git log`/`git diff` 全看得到；改错 `git revert`。
4. **来源可追**：每条正典 frontmatter 记 source / date / verified_by。
5. **宁缺毋编**：答不出说"库里没有"，绝不编。

---

## 生命周期（一条知识怎么进来）

```
agent 干活学到东西 → 写自己 auto-memory
   → 网关 memory hook 自动投 _inbox/  （+ 通知知识专员）
   → 知识专员 核实 / 去重(recall 查重) / 定域
        ├─ 是新知识 → promote --domain <域>  → 进 <域>/ 成 canon
        ├─ 与旧重复且更优 → supersede 旧条   → 旧入 _archive
        └─ 不该进 / 更差 → reject            → 入 _archive
   → 任何 agent / 人 cicy-knowledge recall 即取 canon —— 知识"刚好在场"
```
