# cicy-code 企业审计系统 设计文档

| 项 | 内容 |
|---|---|
| 文档版本 | v1.0 |
| 状态 | Draft — 待评审 |
| 作者 | cicy-code team |
| 适用代码分支 | `audit` |
| 起草日期 | 2026-05-15 |
| 关联实现路径 | `api/mgr/audit/`, `app/src/components/audit/` |

> 本文档为产品级/工业级/企业级方案。任何实现偏离本文档需在评审中提出并更新文档。

---

## 1. Executive Summary

cicy-code 是本地优先的多 agent 开发工作区,agent 在日常工作中会读取企业敏感数据(凭据、客户信息、源码秘钥),并通过外部大模型(Anthropic / OpenAI / DeepSeek 等)进行推理。这构成两类合规风险:

1. **数据出境风险** — 敏感数据被发送给外部 LLM 提供方,违反 GDPR / 个保法 / 合同保密条款
2. **审计缺失风险** — 出现泄露事件后无法追溯 "谁在什么时候用哪个 agent 让什么数据出境"

本系统在 cicy-code 内嵌入企业级审计能力,以"**Detective 全覆盖 + Preventive 高确定性前置**"为核心设计原则,提供:

- 每一次 LLM 出入站均产生不可篡改的审计事件(**Detective Control**)
- 对高确定性敏感数据前置阻断或脱敏(**Preventive Control / DLP**)
- 严重程度四档分级(low / medium / high / critical),直接驱动通道、SLA、事故响应
- 完整的事件生命周期(Hot → Warm → Cold → Archive → Purge)
- 多渠道告警(Dashboard SSE / Webhook / IM / **Email** / SIEM / Page)
- **事故响应**(high/critical 自动触发):AI 自动生成补救建议 + 责任人邮件 + Ack 闭环
- AI 辅助审计(规则建议 / 报告生成 / 上下文解释 / 异常检测,可选)
- 三角色 RBAC(operator / auditor / admin),职责分离
- 元审计(对 audit log 的访问与导出本身亦被记录)

---

## 2. Scope and Stakeholders

### 2.1 In Scope (v1-v3 覆盖范围)

- 所有经过 cicy AI Gateway 的出入站(Channel A)
- 所有经过 mitmproxy 拦截上报的出入站(Channel B,覆盖直连外部 LLM 的 agent)
- agent 通过 cicy 工具读写的文件操作(v2 起,需 wrap)
- audit dashboard 的查阅与导出行为(Meta-audit)

### 2.2 Out of Scope

- agent 在 tmux 内直接 `cat / less / vim` 等不经 wrap 的本地文件操作(留 v3,需 auditd / eBPF)
- 内核级 syscall 审计
- 网络流量包级审计(走 mitm 已经满足合规级别,不下沉到 packet)
- 对 LLM 提供方那一侧的审计(超出我们能控制的边界)
- 第三方插件 / VS Code Marketplace 扩展行为

### 2.3 Stakeholders

| 角色 | 关心什么 |
|---|---|
| CISO / 安全负责人 | 数据出境风险、合规姿态、事件响应能力 |
| 合规 / 法务 | SOC2 / ISO27001 / 等保 / GDPR / 个保法 控制点覆盖 |
| 平台运维 | 性能影响、稳定性、容量、告警噪声 |
| agent 操作者 | 不影响日常使用,出问题能查到原因 |
| 审计员(内/外) | 证据完整、可独立验证、可导出 |

---

## 3. Threat Model

### 3.1 防护对象

- 企业敏感数据:PII、客户数据、源码秘钥、内部系统拓扑、商业机密词汇
- 审计记录本身的完整性与不可篡改性

### 3.2 攻击者画像

| 类型 | 能力 | 动机 |
|---|---|---|
| 无意泄露(主要) | 普通用户,无恶意 | 误粘贴、误读取、误推理 |
| 内部恶意 | 有 cicy-code 操作权限 | 窃取数据、破坏审计记录 |
| 外部攻陷 | 已拿到机器 shell | 通过 agent 套取信息或洗白审计 |
| LLM 提供方泄露 | 拥有出站数据 | 模型训练数据回流 |

### 3.3 安全假设

- **可信**:cicy-code 主进程、宿主操作系统、NTP 服务、cicy-code 团队签名的二进制
- **不可信**:外部 LLM 提供方、网络中间人(故有 TLS)、未受控的 agent 工作内容、用户输入
- **半信任**:operator 角色用户(可能误操作但不应有删除审计的能力)

### 3.4 关键威胁与缓解

| 威胁 | 缓解 |
|---|---|
| agent 向 LLM 泄露敏感数据 | Preventive(inline 拦截/脱敏) + Detective(全量记录) |
| 内部人篡改/删除 audit log | append-only + hash chain + 文件权限 + 元审计 |
| 系统时钟回拨制造记录乱序 | monotonic timestamp + RFC3339 双字段 |
| current.json 被事后篡改"洗白" | event 内冗余 `payload_sha256`,事后核对 |
| scanner 自身 bug 导致漏检 | rules_version 入库,可回放;async + inline 双覆盖 |
| 误报淹没 dashboard | turn 内去重 + 频次告警 + 白名单 + 一键反馈 |
| 使用外部 LLM 二次分类反而泄露 | AI 辅助默认关闭,仅允许企业自托管模型 |

---

## 4. Control Objectives(控制目标)

任何企业级审计架构必须对齐这 6 条。本系统每个组件需在评审时被明确归到某一条以上:

| # | 目标 | 含义 | 本系统对应 |
|---|---|---|---|
| C1 | Completeness | 完整性/全覆盖,无旁路 | Channel A + Channel B 双通道,旁路即视为缺陷 |
| C2 | Integrity | 防篡改 | NDJSON append-only + hash chain + 0600 + monotonic ts |
| C3 | Non-repudiation | 不可否认 | identity 三元组(machine + agent + user)+ 可信时间戳 |
| C4 | Segregation of Duties | 职责分离 | 三角色 RBAC,被审计方无法修改自身审计 |
| C5 | Retention | 留存合规 | 五阶段生命周期,保留期可配,加密归档,合规销毁 |
| C6 | Auditability of Audit | 元审计 | 对 audit log 的读/导出/配置变更全部入审计 |

---

## 5. Architecture

### 5.1 Control Classification

```
┌─────────────────────────────────────────────────────────┐
│  Preventive Control (实时/前置)                          │
│  - 阻止数据出境                                         │
│  - Inline Scanner @ ai_gateway                          │
│  - 高确定性规则,误报代价 = agent 中断                  │
│  - 默认仅 3 条 high secret 启用                         │
│  - fail-mode 可配(open / closed)                       │
├─────────────────────────────────────────────────────────┤
│  Detective Control (异步/事后)                          │
│  - 留下不可篡改证据                                     │
│  - Async Scanner,跑完整规则集                          │
│  - 不可关闭,无旁路                                     │
│  - 0 延迟影响 agent                                     │
└─────────────────────────────────────────────────────────┘
```

**重要**:两者职能不同,**不可互替**。Preventive 是 control(可能失败),Detective 是 evidence(不能缺)。

### 5.2 Data Channels

| 通道 | 接入点 | 覆盖 agent | 已有基础 |
|---|---|---|---|
| Channel A | `api/mgr/ai_gateway_audit.go` 的写入回调 | 走 cicy gateway 的 agent(claude/codex/opencode/cicy-* 默认配置) | 已存在,gateway audit 已有 turn 跟踪 |
| Channel B | `api/mgr/chatbus.go: handleChatWebhook` | 直连外部 LLM 的 agent(用户自配 OpenAI/Anthropic API 直发) | 已存在 webhook 端点,mitmproxy 反代已有 |

**统一 pipeline**:两个 channel 通过统一的 `audit.Pipeline.Submit(event Envelope)` 接入,下游处理一致。

### 5.3 Pipeline(六阶段)

```
┌───────────────────────────────────────────────────────────────┐
│                                                                │
│  Submit(envelope)                                              │
│     │                                                          │
│     ▼                                                          │
│  ┌────────────────────────────────────────────────────────┐   │
│  │ 1. Identity Bind                                       │   │
│  │    - machine_id (启动时分配,~/cicy-ai/.machine_id)    │   │
│  │    - agent_id, agent_type                              │   │
│  │    - user_id (token → user 解析)                       │   │
│  │    - source_channel                                    │   │
│  ├────────────────────────────────────────────────────────┤   │
│  │ 2. Timestamp                                           │   │
│  │    - ts: RFC3339 nano (wall clock,NTP 同步)            │   │
│  │    - ts_monotonic: time.Now().UnixNano() 单调          │   │
│  ├────────────────────────────────────────────────────────┤   │
│  │ 3. Scan (Detective:全规则 / Inline:仅 inline=true)     │   │
│  │    - 加载 policy + 字典                                │   │
│  │    - 跑 scanner,产 findings                           │   │
│  │    - 记录 rules_version、scanner_duration_ms           │   │
│  ├────────────────────────────────────────────────────────┤   │
│  │ 4. Decide                                              │   │
│  │    - 按 findings 最高 severity 选 action               │   │
│  │    - 应用 allow_list / rate_limit                      │   │
│  │    - 标记 decision.action / applied / fail_mode        │   │
│  ├────────────────────────────────────────────────────────┤   │
│  │ 5. Append + Hash                                       │   │
│  │    - 读 chain-state.json 取 last_hash                  │   │
│  │    - 计算 self_hash = sha256(canonical_json(event))    │   │
│  │    - 写入 per-agent audit.ndjson                       │   │
│  │    - 更新 chain-state.json                             │   │
│  │    - 同步写全局 index NDJSON                           │   │
│  ├────────────────────────────────────────────────────────┤   │
│  │ 6. Notify                                              │   │
│  │    - 按 severity + rate_limit 决定告警                 │   │
│  │    - 路由到 SSE / Webhook / IM / Email / SIEM          │   │
│  └────────────────────────────────────────────────────────┘   │
└───────────────────────────────────────────────────────────────┘
```

### 5.4 Component Diagram

```
                       Channel A                Channel B
                       (Gateway)                (mitm)
                          │                        │
                          ▼                        ▼
                  ┌─────────────────────────────────────┐
                  │       audit.Submitter (in-proc)     │
                  └─────────────┬───────────────────────┘
                                │
                                ▼
       ┌──────────────────────────────────────────────────┐
       │  audit.Pipeline (Go)                             │
       │                                                  │
       │   audit/policy        audit/scanner              │
       │   audit/identity      audit/decider              │
       │   audit/store         audit/notify               │
       │   audit/chain         audit/meta                 │
       └──────────────────────────────────────────────────┘
                                │
                ┌───────────────┼─────────────────┐
                ▼               ▼                 ▼
        per-agent NDJSON  global index    notify channels
        + chain-state.json  NDJSON         (SSE/IM/SIEM/...)
                │
                └─▶ lifecycle (rotate/seal/archive/purge)
                          │
                          ▼
                  remote archive (S3/COS)
```

---

## 6. Data Specification

### 6.1 Event Schema(权威定义)

每条 audit event 必须严格符合此 schema。**所有字段为必填,缺失视为格式错误**。

```jsonc
{
  // 标识
  "id": "evt_01HK3M8X7Z9YBPQR4XJ2N5K1A",      // ULID
  "schema_version": "1",
  "rules_version": "2026.05.15",

  // 时间(双时钟)
  "ts": "2026-05-15T10:23:45.123456789Z",     // RFC3339 nano
  "ts_monotonic": 1234567890123,               // int64 nanoseconds

  // 完整性
  "prev_hash": "sha256:abc...",                // 前一条 self_hash
  "self_hash": "sha256:def...",                // 本条除 self_hash 外的 sha256

  // 身份(C3 Non-repudiation)
  "identity": {
    "machine_id": "host_a1b2c3",
    "agent_id": "w-10001",
    "agent_type": "claude",
    "user_id": "u-abc123",                     // 可为空字符串,但字段必存在
    "session_id": "sess_xyz",
    "source_channel": "gateway"                // gateway | mitm
  },

  // 被审计对象
  "subject": {
    "turn_id": "turn_xxx",
    "conversation_id": "conv_xxx",
    "provider": "anthropic",
    "model": "claude-opus-4-7",
    "direction": "outbound",                   // outbound | inbound
    "payload_size": 12345,                     // bytes
    "payload_ref": "current.json#turn_xxx",
    "payload_sha256": "sha256:..."             // 原文哈希,事后核对防篡改
  },

  // 命中
  "findings": [
    {
      "rule_id": "secret.aws_akid",
      "rule_version": "2026.05.15",
      "severity": "high",                      // low | medium | high | critical
      "category": "secret",                    // secret | pii | network | corp | custom
      "match_count": 2,
      "spans": [
        {"start": 120, "end": 140, "preview": "AKIA****MPLE"}
      ]
    }
  ],

  // 决策
  "decision": {
    "evaluated_inline": true,
    "evaluated_async": false,
    "action": "redact",                        // log | notify | redact | block | none
    "applied": true,
    "fail_mode": "open"                        // open | closed
  },

  // 元数据
  "meta": {
    "scanner_duration_ms": 3,
    "pipeline_error": null,                    // null 或 string
    "policy_hash": "sha256:..."                // 当前生效的 policy 哈希
  }
}
```

**Canonical JSON**:`self_hash` 计算前对字段排序,数组保留顺序,无空格。规范化算法在 `audit/chain/canonical.go`。

### 6.2 Policy Schema

`~/cicy-ai/audit/policy.json`:

```jsonc
{
  "version": 1,
  "enabled": true,
  "fail_mode": "open",                         // open | closed (全局默认)

  // 内置规则的覆盖(不能新增内置规则,只能覆盖)
  "rules_override": [
    {"id": "pii.phone_cn", "severity": "medium"},
    {"id": "secret.jwt", "disabled": true}
  ],

  // 企业自定义规则
  "custom_rules": [
    {
      "id": "corp.customer_list",
      "label": "客户名单",
      "category": "corp",
      "severity": "medium",
      "match": {"type": "dict_file", "path": "~/cicy-ai/audit/dict/customers.txt"},
      "scan_directions": ["outbound"],
      "inline": false,
      "default_action": "log"
    }
  ],

  // 白名单
  "allow_list": {
    "paths": ["~/.ssh/known_hosts", "~/.gitconfig"],
    "content_hashes": ["sha256:..."],
    "agents": []                               // 特定 agent 豁免(慎用)
  },

  // 告警(详见 §9)
  "notify": {
    "min_severity": "medium",
    "rate_limit": {"window_seconds": 3600, "max_per_agent": 50},
    "channels": [
      {"type": "sse", "enabled": true},
      {"type": "webhook", "url": "https://example/hook", "min_severity": "high"},
      {"type": "im_feishu", "webhook": "...", "min_severity": "high"},
      {"type": "email", "smtp": "smtp://...", "from": "audit@corp", "min_severity": "high"}
    ],
    "escalation": {
      "high_unack_minutes": 30,
      "escalate_to": ["webhook:..."]
    }
  },

  // 责任人映射 — 出事故时邮件发给谁(详见 §9.7)
  "responsible_persons": {
    "default": ["security@corp"],                          // 兜底收件人
    "by_severity": {
      "high":     ["sec-oncall@corp"],
      "critical": ["sec-oncall@corp", "ciso@corp"]
    },
    "by_agent": {
      "w-10001": ["alice@corp"],                           // 特定 agent 的归属人
      "w-1*":    ["team-platform@corp"]                    // 支持通配
    },
    "by_user": {
      "u-abc123": ["alice@corp"]                           // 触发用户的负责人
    },
    "by_rule": {
      "secret.aws_akid": ["devops@corp"],
      "pii.id_card_cn": ["legal@corp"]
    }
  },

  // 事故响应 — 触发条件 + AI 补救建议(详见 §9.7)
  "incident_response": {
    "enabled": true,
    "trigger_min_severity": "high",                        // high/critical 自动触发事故响应
    "ai_remediation": {
      "enabled": false,                                    // 默认关,启用需 admin opt-in
      "endpoint": "https://your-self-hosted-llm/v1",
      "max_tokens": 800,
      "languages": ["zh-CN", "en"]
    },
    "email_template": "default",                           // default | corp-template
    "cooldown_seconds": 1800                               // 同一 finding hash 30 分钟内只发一次事故邮件
  },

  // 生命周期
  "retention": {
    "hot_days": 7,
    "warm_days": 90,
    "cold_days": 365,
    "archive_years": 7,
    "archive": {"type": "s3", "bucket": "...", "kms_key": "..."}
  },

  // AI 辅助(详见 §10),默认禁用
  "ai_assist": {
    "enabled": false,
    "endpoint": "",                            // 必须是企业自托管模型
    "use_cases": []                            // classifier | suggest_rules | report | explain
  }
}
```

### 6.3 Built-in Rules(v1 内置 10 条)

| ID | 类别 | severity | 方向 | inline | 默认 action | 说明 |
|---|---|---|---|---|---|---|
| `secret.private_key` | secret | high | out | yes | block | `-----BEGIN [A-Z ]+ PRIVATE KEY-----` |
| `secret.aws_akid` | secret | high | out | yes | redact | `AKIA[0-9A-Z]{16}` |
| `secret.aws_secret` | secret | high | out | yes | redact | AKID 周围 40 字符 base64-ish |
| `secret.jwt` | secret | medium | out | no | log | `eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+` |
| `secret.bearer_token` | secret | medium | out | no | log | `Bearer\s+[A-Za-z0-9._-]{20,}` |
| `secret.high_entropy` | secret | medium | out | no | log | 熵 > 4.5 + 上下文有 `=` / `token` / `key` / `secret` |
| `pii.id_card_cn` | pii | medium | both | no | log | 18 位身份证 + 校验位 |
| `pii.bank_card` | pii | medium | both | no | log | 13-19 位数字 + Luhn |
| `pii.phone_cn` | pii | low | both | no | log | `1[3-9]\d{9}` |
| `network.private_ip` | network | low | out | no | log | `10\.` / `172\.(1[6-9]|2\d|3[0-1])\.` / `192\.168\.` |

### 6.4 Storage Format

**目录结构**:

```
~/cicy-ai/audit/
├── policy.json                       # 策略配置(管理员维护)
├── policy.lock.json                  # 当前生效快照(运行时只读)
├── chain-state.json                  # 全局链尾 hash
├── machine_id                        # 机器身份(首次启动生成)
├── dict/                             # 企业字典
│   └── customers.txt
└── index/
    ├── 2026-05-15.ndjson             # 全局索引(仅元数据)
    └── 2026-05-15.ndjson.idx         # SQLite-FTS 索引(查询加速)

~/cicy-ai/workers/<agent>/history/
├── current.json                      # 已有,实时快照
├── reply.json                        # 已有
├── audit.ndjson                      # 该 agent 完整 audit log(append-only)
├── audit-chain.state                 # 该 agent 链尾
└── pre-redact/                       # 仅 Preventive redact 触发时(短期 7 天)
    └── <turn_id>.encrypted
```

**权限**:
- `~/cicy-ai/audit/` 目录:`0700`(仅 owner)
- `audit.ndjson`:`0600`,只能 cicy-code 进程 append
- `pre-redact/`:`0600`,内容用机器本地 key 加密(`~/cicy-ai/audit/.preredact.key`,`0600`)

**Hash chain**:每条 event 包含 `prev_hash`,首条为 `"sha256:GENESIS"`。`chain-state.json` 存当前链尾,启动时强制校验最后 N 条。

---

## 7. 审计策略(Audit Strategy)

### 7.1 三层规则来源(优先级递增)

| 层 | 来源 | 谁维护 | 能做什么 |
|---|---|---|---|
| L1 内置 | Go 二进制内嵌 | cicy-code 团队 | 通用 PII/Secret 规则,跟随版本发布 |
| L2 企业 | `~/cicy-ai/audit/policy.json` + `dict/*.txt` | 企业管理员 | 覆盖 L1、新增自定义规则、配置告警/生命周期 |
| L3 agent | `~/cicy-ai/workers/<agent>/audit-overrides.json` | agent 操作者 | 加严或申请白名单(申请需审计员批准,v2)|

**加载与热重载**:启动加载 + fsnotify 监听变更 + mtime + content-hash 双重确认。变更入审计(`category: policy_change`)。

### 7.2 严重度模型(Severity)

四档严重程度,**严重程度直接决定告警通道、是否触发事故响应、SLA**:

| Severity | 含义 | 典型 action | dashboard 展示 | 通知 | 事故响应 | Ack SLA |
|---|---|---|---|---|---|---|
| **low** | 信息性,不一定真泄露 | log | 默认折叠 | 仅 SIEM | × | — |
| **medium** | 高概率真敏感,需关注 | log + notify | 展开,按规则聚合 | SSE + IM 摘要 | × | 24h |
| **high** | 几乎肯定泄露,需立即处理 | log + notify + (可选 redact/block) | 高亮 + 实时告警 | SSE + Webhook + IM + Email | ✅ 触发(责任人邮件 + AI 补救建议) | 1h |
| **critical** | 关键资产泄露(root CA / AKSK / 大批量 PII) | log + notify + block + 强制告警 | 顶部红条 + 强制 ack | 全通道 + Page/短信 | ✅ 触发(立即邮件 + 短信 + CISO 抄送) | 15min |

**严重程度的来源**(优先级):
1. policy.json 中 `rules_override` 显式指定(企业定制最高优先)
2. 规则定义里的默认 severity
3. 命中数量升级:同一 turn 命中 ≥ N 条 medium 自动升 high(默认 N=3,可配)
4. agent 类别升级:`by_agent.severity_boost`(如 production agent 默认提一档)

**严重程度的下游联动**:

```
severity 决定:
  ├─ 是否 inline 阻断 / redact / 仅 log
  ├─ 告警走哪些通道(§8.2 路由表)
  ├─ 是否触发事故响应流程(§9.7)
  ├─ Ack SLA(超时升级)
  └─ dashboard 展示样式 / 报表分组
```

### 7.3 动作模型(Action)

| Action | 行为 | 是否影响 agent | 默认场景 |
|---|---|---|---|
| `log` | 仅写 audit.ndjson | 不影响 | low/medium |
| `notify` | log + 推 SSE / IM | 不影响 | medium/high(异步) |
| `redact` | log + 改写 payload 替换为 `[REDACTED:<rule_id>]` | 略微改变 prompt | high 已知格式 |
| `block` | log + 中断请求,返回 HTTP 451 | 阻断 agent | high/critical 关键 secret |
| `none` | 仅评估,不动作 | 不影响 | 调试/灰度新规则 |

### 7.4 方向策略(Direction)

- **outbound**:agent → LLM,**主战场**,所有 secret/credential 规则默认只扫 outbound
- **inbound**:LLM → agent,扫"模型回吐"的敏感数据(如越权读取后被模型 echo);PII 类规则建议双向扫

### 7.5 失败模式(Fail Mode)

| Mode | scanner 出错时 | 适用 |
|---|---|---|
| `open`(默认) | 放行 + 写 `pipeline_error` event | 业务可用性优先 |
| `closed` | 阻断请求,返回 503 + 写 event | 极高合规场景(金融、医疗、政企) |

**写明在 policy.json,审计时需可解释**。

### 7.6 噪声治理

| 机制 | 说明 |
|---|---|
| Turn 内去重 | 同一 turn 命中同一规则多次,合并为单 finding,`match_count` 累加 |
| 频次告警 | 同一 agent + 同一规则,一小时内 N 次才触发 notify(默认 max_per_agent=50)|
| 白名单 | 路径 / 内容哈希 / agent 三种粒度;dashboard 一键反馈加入 |
| Cool-down | 同一 finding 哈希 24h 内不重复 notify |
| 规则灰度 | 新规则可设 `action: none`,跑一周看噪声,再决定上正式 action |

### 7.7 规则版本化(Auditable Replay)

每条 event 记录:
- `rules_version`(规则集版本,YYYY.MM.DD 或 git sha)
- `meta.policy_hash`(运行时 policy 内容哈希)

**目的**:审计师要求"用 2026 年 3 月的规则集回放 2026 年 1 月的事件"时,系统能精确还原当时规则。

---

## 8. 报警(Alerting)

### 8.1 通道(Channels)

| 通道 | 用途 | 实现 |
|---|---|---|
| **SSE** | dashboard 实时推送 | 复用现有 `/api/notify/stream` |
| **Webhook** | 自定义集成(SIEM 入口) | `POST <url>` JSON envelope |
| **IM Feishu / WeCom / Slack / 钉钉** | 团队即时告警 | 各 IM 的 webhook,模板渲染 |
| **Email** | 周期摘要 / 升级 | SMTP,默认仅 escalation |
| **SIEM Forward** | Splunk / ELK / QRadar | Syslog(CEF / LEEF 格式) |
| **PagerDuty / 短信** | critical 强制响应 | 通过 webhook 桥接 |

### 8.2 路由(Severity Routing)

| Severity | SSE | Webhook | IM | Email | SIEM | Page |
|---|---|---|---|---|---|---|
| low | × | × | × | × | ✓ | × |
| medium | ✓ | × | × | × | ✓ | × |
| high | ✓ | ✓ | ✓ | × | ✓ | × |
| critical | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |

**可在 policy.json 改路由表**。

### 8.3 限流与去重

- 单一 finding hash 24h cool-down(防同一事件刷屏)
- per-channel 每分钟最多 N 条(防 channel 被打挂)
- 高频规则(如 phone_cn)默认聚合为"过去 1 小时 X 次"摘要,而非每条单推

### 8.4 升级(Escalation)

```
high 触发 → notify 主收件人
   │
   ├── 30 分钟内未 ack → 升级到二级收件人(admin)
   │
   └── 1 小时内未 ack → 升级到 PagerDuty / 短信
```

`ack` 操作在 dashboard 上完成,本身亦入 audit(`category: alert_ack`)。

### 8.5 模板(Alert Payload)

```json
{
  "alert_id": "alt_01HK...",
  "event_id": "evt_01HK...",
  "severity": "high",
  "ts": "2026-05-15T10:23:45Z",
  "agent": "w-10001 (claude)",
  "user": "u-abc123",
  "provider": "anthropic",
  "model": "claude-opus-4-7",
  "rules_hit": [
    {"id": "secret.aws_akid", "count": 1}
  ],
  "action_taken": "redacted",
  "dashboard_url": "https://.../audit?event=evt_01HK...",
  "runbook_url": "https://.../runbook/secret-aws-akid"
}
```

### 8.6 Runbook Hook

每条规则可挂 `runbook_url`,告警 payload 自动包含。Runbook 文档结构(企业自维护):

- **What**:这条规则在抓什么
- **Why severity**:为什么是这个等级
- **Triage**:5 分钟内应做的判断步骤
- **Mitigate**:确认真泄露后的应急动作
- **Lessons**:历史相似事件回顾

---

## 9. AI 辅助审计(AI-Assisted Audit)

**核心原则**:**Audit-Of-AI 不能成为新的泄露源**。所有 AI 辅助默认关闭,启用必须显式 opt-in,且**只能调用企业自托管模型**(禁止把可疑内容再次送外部 SaaS LLM)。

### 9.1 用例 1:不确定匹配的二级分类(Classifier)

**场景**:某些规则会产生"灰区"(熵检测、模糊词典),scanner 标记 `uncertain: true`,送 LLM 判断"这真的是 secret/PII 吗"。

- 输入:finding 上下文 ±200 字符 + 规则 metadata
- 输出:`{"verdict": "true_positive | false_positive", "confidence": 0-1, "reason": "..."}`
- 结果写入 event `meta.ai_classification`,**不修改原始 findings**(保留可追溯性)

### 9.2 用例 2:规则建议(Rule Suggestions)

**场景**:auditor 在 dashboard 上累计标记了 N 条 false-positive,系统周期(每天 02:00)扫一遍,LLM 总结模式,生成建议。

- 输入:最近 30 天的 false-positive 列表 + 当前 policy
- 输出:建议的 `allow_list` 项 / 规则微调 / 新规则草案
- 形态:dashboard 上 "AI 建议" 面板,**不自动应用**,需 admin 审批

### 9.3 用例 3:报告生成(Report Generation)

**场景**:给管理层 / 审计师生成周报、月报、事件叙述。

- 输入:时间区间内的 events 统计 + 关键事件
- 输出:Markdown / PDF 叙述,含执行摘要、趋势、Top N 命中、建议
- 必须人工 review 后再发出(prompt 末尾加 disclaimer)

### 9.4 用例 4:Operator 上下文解释(Explain)

**场景**:dashboard 上单条 finding 点击"AI 解释",LLM 用人话解释:"这是什么、为何高危、建议如何处置"。

- 仅传规则 metadata + 脱敏 preview,**不传原文**
- 调用本身入 audit(`category: ai_explain`)

### 9.5 用例 5:异常行为检测(Anomaly Detection)

**场景**:基线学习每个 agent 正常的 prompt 模式 / 调用频率 / 命中分布,偏离基线触发"行为异常"提示。

- 用 embedding + 滑窗 z-score
- 输出"agent 今日异常分数",而非具体内容判定

### 9.6 防滥用与隔离

| 风险 | 缓解 |
|---|---|
| AI 辅助本身造成数据回流 | 仅允许企业自托管模型;endpoint 显式列白名单 |
| 用 LLM 审 LLM 的循环 | AI 辅助调用入 audit;有专用 budget cap |
| 模型误判被当成权威 | AI 输出仅作辅助,原始 findings 不变;auditor 必须能看到原始 |
| 模型 prompt 注入 | 输入限长 + 模板固定 + 输出 JSON schema 校验 |

### 9.7 用例 6:事故响应 + AI 自动补救建议 + 责任人邮件(**核心企业能力**)

**触发条件**:event severity ∈ {`high`, `critical`},且当前 finding hash 在 `incident_response.cooldown_seconds` 内首次出现。

**流程**:

```
┌──────────────────────────────────────────────────────────────────┐
│                                                                  │
│  high/critical event 落盘                                         │
│           │                                                      │
│           ▼                                                      │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │ 1. 解析责任人(§6.2 responsible_persons)                  │  │
│  │    优先级:by_rule > by_user > by_agent > by_severity      │  │
│  │             > default;并集后去重                          │  │
│  ├────────────────────────────────────────────────────────────┤  │
│  │ 2. 组装上下文(脱敏)                                     │  │
│  │    - event 元数据(ts / agent / user / provider / model)  │  │
│  │    - findings(rule_id / severity / preview 已脱敏)        │  │
│  │    - decision.action(redact/block/none)                    │  │
│  │    - 命中规则的 runbook_url                                │  │
│  │    *** 严禁把原文(payload)送入 AI ***                     │  │
│  ├────────────────────────────────────────────────────────────┤  │
│  │ 3. 调企业自托管 LLM 生成 JSON                              │  │
│  │    {                                                       │  │
│  │      "summary": "一句话事故摘要(中英双语)",            │  │
│  │      "severity_explain": "为什么是 high/critical",         │  │
│  │      "immediate_actions": [                                │  │
│  │        "立即吊销...",                                      │  │
│  │        "禁用 agent w-10001 直至复核",                      │  │
│  │        "检查 git log 是否已外推"                           │  │
│  │      ],                                                    │  │
│  │      "longer_term": [                                      │  │
│  │        "加严该路径白名单",                                 │  │
│  │        "对该 agent 启用 inline block"                      │  │
│  │      ],                                                    │  │
│  │      "investigation_links": [                              │  │
│  │        "dashboard URL", "runbook URL"                      │  │
│  │      ]                                                     │  │
│  │    }                                                       │  │
│  ├────────────────────────────────────────────────────────────┤  │
│  │ 4. 生成事故邮件(template 渲染)                          │  │
│  │    - 主题:[CICY-AUDIT][HIGH] secret.aws_akid leaked       │  │
│  │            from w-10001 (alice@corp)                       │  │
│  │    - 正文:AI 摘要 + 立即措施 + dashboard 链接 + ack 链接 │  │
│  │    - 附件:event JSON(脱敏版)                            │  │
│  ├────────────────────────────────────────────────────────────┤  │
│  │ 5. 发送 + 落入审计                                         │  │
│  │    - SMTP 发送(支持 DKIM 签名)                           │  │
│  │    - 写 `category: incident_response_email` event          │  │
│  │    - 写 `category: ai_remediation_call` event(AI 调用本身)│  │
│  │    - 更新 dashboard "事故" tab                             │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

**邮件模板(default)**:

```
Subject: [CICY-AUDIT][{{severity}}] {{top_rule_id}} - {{agent_id}} ({{user_id}})

事故级别: {{severity}}
触发时间: {{ts}}
触发 agent: {{agent_id}} ({{agent_type}}) — 责任人: {{responsible}}
触发用户: {{user_id}}
出站目标: {{provider}} / {{model}}
当时动作: {{decision.action}} (applied: {{decision.applied}})

──────── 命中 ────────
{{#findings}}
  • {{rule_id}}  [{{severity}}]  命中 {{match_count}} 次  preview: {{spans.0.preview}}
{{/findings}}

──────── AI 摘要 ────────
{{ai.summary}}

为什么是 {{severity}}:
{{ai.severity_explain}}

──────── 立即处置(请在 {{sla}} 内完成)────────
{{#ai.immediate_actions}}
  □ {{.}}
{{/ai.immediate_actions}}

──────── 后续加固建议 ────────
{{#ai.longer_term}}
  • {{.}}
{{/ai.longer_term}}

──────── 查阅 ────────
事件详情:{{dashboard_url}}
规则手册:{{runbook_url}}
确认/解除(Ack):{{ack_url}}

—— 本邮件由 cicy-code 审计系统自动生成。AI 建议供参考,处置以企业流程为准。
```

**关键设计**:

1. **AI 不入主路径**:AI 调用是 best-effort,**失败时邮件仍发**(只是不带 AI 摘要,改用模板默认描述)。审计代码绝不依赖 AI 才能工作
2. **不发原文**:AI 上下文绝不含 prompt 原文,只含元数据和脱敏 preview
3. **责任人解析有审计**:`responsible_persons` 解析过程入 event meta(可追溯"为什么发给这些人")
4. **AI 调用本身入审计**:`category: ai_remediation_call`,含 input_summary、output_json、token_usage
5. **邮件发送本身入审计**:`category: incident_response_email`,含 recipients、message_id、delivery_status
6. **Cooldown**:同一 finding hash 30 分钟内只发一次邮件,但事件本身照常落盘
7. **DKIM/SPF 签名**:防伪造(企业域必备)
8. **多语言**:`languages` 配置生成中英双语正文,跨国企业友好

**实施细节**:
- AI 调用超时 10 秒,超时回落到无 AI 模板
- 邮件队列持久化(`~/cicy-ai/audit/email-queue/`),崩溃恢复
- 邮件失败 3 次重试,最终失败 → critical 告警(因为事故邮件没发出本身就是 critical 问题)
- 提供 dashboard 上"重发事故邮件"按钮(auditor only)

### 10.1 五阶段

```
Hot (0-7d)  →  Warm (7-90d)  →  Cold (90-365d)  →  Archive (1-7y)  →  Purge
   ▼              ▼                  ▼                   ▼               ▼
本机 NDJSON   本机 NDJSON       本机 gz + seal     远程加密 + 证书    安全销毁
SSE 实时      dashboard 查询     dashboard 慢查询    需触发拉回       证书入审计
```

### 10.2 切片与封存(Seal)

- 按日切片(每日 00:00 UTC 创建新文件)
- 切片完成后追加 `_seal` 行:`{"_seal":true,"first_hash":..., "last_hash":..., "count":..., "signature":...}`
- `signature`:用机器私钥(`~/cicy-ai/audit/.sign.key`)签名,可在归档侧用对应公钥独立验证

### 10.3 归档(Archive)

- 加密:AES-256-GCM,KEK 由 KMS / 企业密钥管理托管
- 上传:S3 / 阿里 OSS / 腾讯 COS,Object Lock(WORM)模式
- 元数据清单(manifest)单独存,带签名

### 10.4 销毁(Purge)

- 保留期满后**安全删除**(覆写 + unlink)
- **不**删除元数据清单(留作"曾经存在并已合规销毁"的证据)
- 销毁事件本身入审计(`category: lifecycle_purge`)

### 10.5 恢复(Recovery)

- 启动时强制 hash chain 校验最近 7 天
- 损坏检测:`audit-chain.state` 与文件实际尾部不符 → 进入"调查模式":只读,告警 admin
- 提供 CLI:`cicy audit verify [path]` / `cicy audit restore-from-archive [date]`

---

## 11. 访问控制 / SoD

### 11.1 角色定义

| 角色 | 能力 | 限制 |
|---|---|---|
| `operator` | 看自己的 agent;触发 agent;看自己 agent 的 audit 摘要 | 不能看别人;不能改 policy;不能导出原文 |
| `auditor` | 读所有 events;导出报表;标记 false-positive;查阅元审计 | 不能改 agent;不能写 / 删 audit data;不能改 cicy-code 代码 |
| `admin` | 改 policy;部署;运维 | 不能 / 删 / 改 audit data;改 policy 入审计 |

### 11.2 实现

- v1:基于 token + perms(沿用现有 `verifyToken`),新增 `audit_read` / `audit_export` / `audit_policy_write` perms
- v1.5:dashboard 区分视图(operator 只看自己)
- v2:audit 文件由 root 拥有,cicy-code 用专门的 unix socket / setcap 单向 append

### 11.3 元审计(Meta-Audit)

任何对以下操作均生成 audit event(`category: meta_*`):

| 操作 | category |
|---|---|
| 查询 audit events | `meta_query` |
| 导出报表 | `meta_export` |
| 修改 policy | `meta_policy_change` |
| 标记 false-positive | `meta_fp_mark` |
| Alert ack | `meta_alert_ack` |
| 访问 pre-redact 原文 | `meta_preredact_access` |

---

## 12. Non-functional Requirements(NFRs)

### 12.1 性能 SLO

| 指标 | 目标 | 退化条件 |
|---|---|---|
| Inline scan 延迟 (P50) | < 5 ms | 规则数 < 50,内容 < 100 KB |
| Inline scan 延迟 (P99) | < 20 ms | 同上 |
| Async scan 延迟 (P99) | < 200 ms | 不阻塞 agent |
| Pipeline 写入吞吐 | ≥ 200 events/sec per agent | 单机标准 ECS |
| dashboard 查询(单 agent 7 天)| < 500 ms | 用 SQLite-FTS 索引 |
| dashboard 查询(全量 7 天)| < 2 s | 索引必备 |

### 12.2 容量规划

- 单条 event:JSON ~1 KB(含 finding 时 ~2 KB)
- 单 agent 日活 500 turn × 2 方向 × 平均 1.5 events = 1500 events/day ≈ 3 MB/day
- 100 agent × 30 天 = ~9 GB(未压缩),~2 GB(gzip)

### 12.3 可靠性

- audit pipeline 是 best-effort detective + 同步 inline:**inline 必同步,async 异步**
- async 队列 buffer 长度有上限(默认 10000),溢出策略:**drop oldest + 写 pipeline_error**,绝不丢 inline event
- 启动恢复:`audit-chain.state` 与文件不一致 → 进调查模式

### 12.4 安全

- 所有 audit 文件 `0600/0700`
- pre-redact 文件加密(本机 key)
- 归档加密(KMS)
- chain signature(机器私钥)
- TLS 强制(所有 API)

### 12.5 可观测性

| Metric | 含义 | 告警阈值 |
|---|---|---|
| `audit_events_total` | 事件总数(按 channel / severity) | 同比骤降 50% → 怀疑旁路 |
| `audit_pipeline_error_total` | pipeline 错误数 | > 0.1% → 告警 |
| `audit_inline_block_total` | 实际阻断数 | 突增 → 怀疑攻击或新业务 |
| `audit_scan_duration_p99` | scan p99 延迟 | > SLO → 告警 |
| `audit_chain_integrity_check` | 链完整性校验结果 | 失败 → critical |

---

## 13. Compliance Mapping(合规映射)

| 标准 | 关键控制点 | 本系统覆盖 |
|---|---|---|
| **SOC 2 Type II** | CC6.1 逻辑访问 | RBAC + token |
|  | CC6.6 数据传输 | Channel A/B + Preventive |
|  | CC7.2 系统监控 | Detective 全覆盖 + Alerting |
|  | CC7.3 安全事件评估 | Severity model + Runbook |
| **ISO/IEC 27001** | A.12.4 日志与监控 | NDJSON + hash chain + 保留策略 |
|  | A.18.1.3 记录保护 | 0600 + 加密归档 + WORM |
| **等保 2.0(三级)** | 8.1.3.5 安全审计 | 完整覆盖 |
|  | 8.1.4.7 个人信息保护 | PII 规则 + 双向扫 |
| **GDPR / 个保法** | 数据最小化 | redact + 不冗余原文 |
|  | 数据可携带 / 删除 | 导出 + 合规销毁 |
| **PCI-DSS v4** | 10.x 日志与监控 | 完整覆盖 |
|  | 3.x 数据保护 | bank_card 规则 + redact |
| **HIPAA**(可选) | 164.312(b) 审计控制 | 完整覆盖 |

---

## 14. 分阶段实施(Phased Implementation)

> 每个 Phase 都有 TODO 与"审计师签字"等级的验收标准。Phase 间可重叠,但**前一 Phase 验收未过不开后续 Phase**。

### Phase 1 — Audit-Ready Baseline(2-3 周,v1)

**目标**:每一次 LLM 出入站都有完整、不可篡改、可时间排序的记录。

#### TODO

- [ ] **P1-T1** 新建 Go 包 `api/mgr/audit/`,子包 `policy / scanner / chain / store / decider / notify / pipeline / identity / canonical / meta`
- [ ] **P1-T2** 定义 `Event` / `Policy` / `Finding` 类型(`audit/types.go`),所有字段加 tag,Canonical JSON 序列化函数
- [ ] **P1-T3** 实现 `audit/chain`:hash chain、`chain-state.json` 读写、启动校验最近 N 条
- [ ] **P1-T4** 实现 `audit/store`:per-agent NDJSON + 全局 index NDJSON 双写,原子追加(O_APPEND + O_SYNC)
- [ ] **P1-T5** 实现 `audit/identity`:`machine_id` 生成 / 读取;token → user_id 解析(沿用 `verifyToken`)
- [ ] **P1-T6** 实现 `audit/scanner` 骨架 + 内置 10 条规则(`scanner/builtin.go`),全部单测覆盖
- [ ] **P1-T7** 实现 `audit/pipeline`:Submit / Identity Bind / Timestamp / Scan / Decide / Append / Notify 六阶段
- [ ] **P1-T8** 接入 Channel A:`ai_gateway_audit.go` 中 `aiGatewayWriteCurrentSnapshot` / `aiGatewayWriteReplySnapshot` 写入后调 `audit.Submit(envelope)`(异步)
- [ ] **P1-T9** 接入 Channel B:`chatbus.go: handleChatWebhook` 收到 mitm 事件后转 `audit.Submit`
- [ ] **P1-T10** API 路由 `/api/audit/events`(list)`/api/audit/events/{id}`(detail)`/api/audit/stats`(汇总),authM 保护
- [ ] **P1-T11** 前端 `AuditDashboard.tsx` 新增 Tab `Findings`:list + filter (severity / agent / time / rule_id) + detail panel
- [ ] **P1-T12** CLI:`cicy audit verify` 校验本机 hash chain;`cicy audit init` 初始化 policy 模板
- [ ] **P1-T13** 单元测试:scanner 命中 / 错过 / 上下文边界;chain 篡改检测;canonical JSON 稳定性
- [ ] **P1-T14** 集成测试:跑一个真实 agent turn,验证 event 落盘 + 链完整
- [ ] **P1-T15** 性能基准:`go test -bench` for inline 路径 P50 / P99
- [ ] **P1-T16** 文档:`docs/audit/operator-quickstart.md`(operator 如何看)+ `docs/audit/admin-setup.md`(admin 如何配)

#### 验收标准

| # | 标准 | 测量方式 | 通过条件 |
|---|---|---|---|
| AC-P1-1 | 完整性 | 跑 100 次真实 LLM 调用,统计 audit events 数 | events ≥ 100 × 2(out+in)× 1.0,丢失率 = 0 |
| AC-P1-2 | 不可篡改 | 手动改 audit.ndjson 一行,跑 `cicy audit verify` | 检出篡改并报告位置 |
| AC-P1-3 | 时钟健壮 | 系统时钟前后调整 ±2h | 链顺序不乱(monotonic 保证) |
| AC-P1-4 | 旁路检测 | 启用一个未走 gateway / mitm 的 agent | 该 agent 在 dashboard 应该看不到任何 event,运维 metrics `audit_events_total` 同比降低应触发告警 |
| AC-P1-5 | 性能 | inline 路径 bench | P50 < 5ms,P99 < 20ms |
| AC-P1-6 | 内置规则正确性 | 跑标准测试集(每条规则 10 个 positive / 10 个 negative) | precision ≥ 0.99,recall ≥ 0.95 |
| AC-P1-7 | RBAC 基线 | operator token 访问 `/api/audit/events` | 403 |
| AC-P1-8 | 文件权限 | `ls -la ~/cicy-ai/audit/` | 全部 0600 / 0700 |
| AC-P1-9 | 元审计(基线) | 用 auditor token 查询 events 一次 | 产生一条 `category: meta_query` event |
| AC-P1-10 | 重启恢复 | 故意 kill -9,重启 | 启动时 chain check 通过,无数据丢失 |

#### 退出条件

以上 10 条验收标准全过,且文档 `docs/audit/admin-setup.md` 由 admin 角色独立按文档完成一次完整部署。

---

### Phase 2 — Detection Coverage(2-3 周,v1.5)

**目标**:能识别企业声明的所有敏感数据类型,误报率有度量,误报有治理。

#### TODO

- [ ] **P2-T1** policy.json 完整 schema + 加载 + fsnotify 热重载
- [ ] **P2-T2** custom_rules:regex + dict_file 两种 match
- [ ] **P2-T3** 大词典 Aho-Corasick(选型:`github.com/cloudflare/ahocorasick` 或自实现)
- [ ] **P2-T4** 规则版本化:`rules_version` 写入 event,policy 修改生成新 `policy_hash`
- [ ] **P2-T5** 噪声治理:turn 内去重 / 频次告警 / Cool-down
- [ ] **P2-T6** allow_list:paths / content_hashes / agents
- [ ] **P2-T7** 误报反馈面:dashboard 一键标记 false-positive → 进 allow_list(需 auditor 角色)
- [ ] **P2-T8** per-agent audit tab:`AgentInspector` 加 "审计" tab,传 `agentId` 过滤
- [ ] **P2-T9** 规则灰度:`action: none` 调试模式
- [ ] **P2-T10** SIEM 导出格式(CEF / LEEF)
- [ ] **P2-T11** 周期 false-positive 报表:auditor 每周看一次"Top 5 误报规则"
- [ ] **P2-T12** 文档:`docs/audit/policy-authoring.md`(如何写规则)

#### 验收标准

| # | 标准 | 通过条件 |
|---|---|---|
| AC-P2-1 | 热重载 | 修改 policy.json,生效 ≤ 5 秒,旧 events 保留旧 rules_version |
| AC-P2-2 | 大词典性能 | 10K 词典,inline P99 仍 < 20ms |
| AC-P2-3 | 误报治理 | 在 1000 个 turn 跑标准误报样本集,误报率 < 5%;白名单后再跑,误报率 < 1% |
| AC-P2-4 | per-agent 视图 | operator 只看自己 agent,auditor 看全部 |
| AC-P2-5 | 灰度 | `action: none` 不触发任何业务影响,但能产 findings |
| AC-P2-6 | SIEM 导出 | 导出 1000 条,Splunk / ELK 可解析 |
| AC-P2-7 | 元审计扩展 | policy 变更入审计,内容含变更 diff |
| AC-P2-8 | 文档可执行 | 第三方按 policy-authoring.md 在 30 分钟内写出第一条自定义规则 |

---

### Phase 3 — Preventive Control(2-3 周,v2)

**目标**:能在高敏感数据外发前阻断或脱敏,并记录控制是否生效。

#### TODO

- [ ] **P3-T1** Inline scanner 接入 outbound 出站路径(在 `recordOutboundRequest` 前)
- [ ] **P3-T2** action: redact 实现(改写 body,落 pre-redact 原文加密文件)
- [ ] **P3-T3** action: block 实现(返回 HTTP 451,event 标 `applied: true`)
- [ ] **P3-T4** fail_mode 实现(open / closed),错误路径完整测试
- [ ] **P3-T5** pre-redact 短期存储(默认 7 天) + 访问控制(auditor only) + meta_preredact_access
- [ ] **P3-T6** dashboard 显示"今日 preventive 拦截 / 涉及 agent / 涉及规则"
- [ ] **P3-T7** 多通道告警:Webhook + IM(Feishu/WeCom/Slack/钉钉)+ Email
- [ ] **P3-T8** Severity routing 表 + per-channel 限流
- [ ] **P3-T9** Escalation:30min 未 ack 升级
- [ ] **P3-T10** Runbook URL 字段 + dashboard 跳转
- [ ] **P3-T11** 混沌测试:scanner 注入 panic,验证 fail-open / fail-closed
- [ ] **P3-T12** 文档:`docs/audit/preventive-tuning.md`
- [ ] **P3-T13** 事故响应:责任人解析(by_rule / by_user / by_agent / by_severity)+ 落 event meta
- [ ] **P3-T14** Email 通道:SMTP 客户端 + 邮件队列持久化 + DKIM 签名 + 3 次重试
- [ ] **P3-T15** AI 补救建议:企业自托管 LLM 调用 + JSON schema 校验 + 10s 超时 + 无 AI 回落模板
- [ ] **P3-T16** 事故邮件模板(中英双语)+ 上下文脱敏(严禁原文入 prompt)
- [ ] **P3-T17** Ack 链接(magic link,签名 token,30 天过期)+ ack 入审计
- [ ] **P3-T18** "事故" tab 前端:列表 + 责任人 + AI 摘要 + ack 状态 + 重发按钮
- [ ] **P3-T19** Cooldown 与 dedup(同一 finding hash 30min 内一封)
- [ ] **P3-T20** 文档:`docs/audit/incident-response.md`(企业如何配置责任人与模板)

#### 验收标准

| # | 标准 | 通过条件 |
|---|---|---|
| AC-P3-1 | block 有效 | 注入测试 secret,inline rule action=block,请求被中断,agent 收到 451 |
| AC-P3-2 | redact 有效 | 注入 AKID,外发 body 中确实被替换,LLM 看不到原值 |
| AC-P3-3 | pre-redact 可追溯 | auditor 可调取原文(经 meta_preredact_access 记录) |
| AC-P3-4 | fail-open | 注入 scanner panic,请求不被阻断,event 标 pipeline_error |
| AC-P3-5 | fail-closed | 切换 fail_mode=closed,scanner panic 时 503 |
| AC-P3-6 | 告警路由 | high 触发,SSE+Webhook+IM 同时到达;low 仅 SIEM |
| AC-P3-7 | 限流 | 单 finding hash 24h cool-down,实测无重复 |
| AC-P3-8 | escalation | 模拟 30min 不 ack,触发二级告警 |
| AC-P3-9 | 事故邮件送达 | 触发 high event,2 分钟内责任人收到邮件;邮件含 AI 建议或回落模板 |
| AC-P3-10 | 责任人正确性 | 测试 4 种映射(by_rule/by_user/by_agent/by_severity)优先级,实测命中预期收件人 |
| AC-P3-11 | AI 不入主路径 | 模拟 AI endpoint 宕机,邮件仍发(使用回落模板),event 标 `ai_remediation_error` |
| AC-P3-12 | 严禁原文入 AI | 静态检查 AI 调用代码路径,确认无 payload 字段;运行时 prompt log 抽样审核 |
| AC-P3-13 | Cooldown 生效 | 同一 finding hash 30min 内连发 10 次,实际邮件 = 1;事件落盘 = 10 |
| AC-P3-14 | Ack 闭环 | 收件人点 ack 链接,dashboard 状态变更,生成 `meta_alert_ack` event |
| AC-P3-15 | Critical 通道 | critical event 触发后,SSE+IM+Email+Page 全到达;CISO 抄送出现在 To/CC |
| AC-P3-16 | 邮件失败告警 | 3 次重试失败 → critical 告警写入审计且本身可被告警 |

---

### Phase 4 — Forensics & Reporting(2-3 周,v2.5)

**目标**:能产出合规报表,能支持事后调查取证。

#### TODO

- [ ] **P4-T1** 完整 turn 回放:event ↔ current.json ↔ reply.json 关联视图
- [ ] **P4-T2** 报表生成:按周/月,Markdown + PDF
- [ ] **P4-T3** 导出:CSV / NDJSON / CEF,文件附 SHA256 + 签名
- [ ] **P4-T4** SIEM 实时转发(Syslog/UDP / TCP / TLS)
- [ ] **P4-T5** 关联分析:同一 user 跨 agent 的 findings 聚合
- [ ] **P4-T6** 时间轴(timeline)视图:某 agent 24h findings 分布
- [ ] **P4-T7** 搜索:按规则 / 提供商 / 模型 / 关键字过滤,FTS 索引
- [ ] **P4-T8** "调查模式":pin 一段时间区间,生成完整证据包(zip,含 events + 关联 history + 签名 manifest)

#### 验收标准

| # | 标准 | 通过条件 |
|---|---|---|
| AC-P4-1 | 报表 | 生成周报,含 stats / top rules / top agents / 异常事件叙述 |
| AC-P4-2 | 导出完整性 | 导出 NDJSON 1万条,第三方用 `cicy audit verify` 独立校验通过 |
| AC-P4-3 | SIEM 接入 | 实时转发至 Splunk,延迟 < 5s |
| AC-P4-4 | 搜索性能 | 全量 30 天搜索关键字,< 2s |
| AC-P4-5 | 证据包 | 调查模式生成的 zip 包,内含 manifest.json + signature,Mac/Linux 通用解压验证 |

---

### Phase 5 — Compliance Posture(3-4 周,v3)

**目标**:能直接配合 SOC2 / ISO27001 / 等保 审计。

#### TODO

- [ ] **P5-T1** 控制点映射文档(SOC2 / ISO / 等保)
- [ ] **P5-T2** 生命周期自动化(rotate / seal / archive / purge)
- [ ] **P5-T3** 远程归档(S3 / OSS / COS),WORM(Object Lock)
- [ ] **P5-T4** 加密:KMS 集成,KEK + DEK 模式
- [ ] **P5-T5** 三角色 RBAC(operator / auditor / admin)
- [ ] **P5-T6** 元审计完整(所有访问/变更全覆盖)
- [ ] **P5-T7** 独立验证工具:开源 / 单独 binary,审计师离线校验归档
- [ ] **P5-T8** 文档:`docs/audit/compliance-soc2.md` / `compliance-iso27001.md` / `compliance-dengbao.md`
- [ ] **P5-T9** 第三方渗透测试(优先级中)
- [ ] **P5-T10** 合规演练:模拟一次完整外审

#### 验收标准

| # | 标准 | 通过条件 |
|---|---|---|
| AC-P5-1 | RBAC | 三角色权限矩阵全部通过测试用例 |
| AC-P5-2 | 归档 WORM | S3 Object Lock 生效,即使 admin 也无法 7 年内删除 |
| AC-P5-3 | 加密 | 归档密文用错误的 KEK 解密失败;正确 KEK 解密 + 校验签名通过 |
| AC-P5-4 | 独立验证工具 | 审计师在不接触 cicy-code 的环境下,用工具校验归档完整 |
| AC-P5-5 | 控制映射 | SOC2 / ISO / 等保 三份映射表完整,无 Gap |
| AC-P5-6 | 模拟外审 | 安排一次内部模拟审计,所有提问可在 1 工作日内出证据 |

---

### Phase 6 — AI-Assisted Audit(2-3 周,v3.5,可选)

**目标**:在不引入新泄露风险的前提下,用 AI 提升 audit 体验。

#### TODO

- [ ] **P6-T1** AI 调用统一封装,仅允许 policy.ai_assist.endpoint 白名单
- [ ] **P6-T2** Classifier 用例(uncertain 二级分类)
- [ ] **P6-T3** Suggest rules 用例
- [ ] **P6-T4** Report generation 用例
- [ ] **P6-T5** Explain 用例
- [ ] **P6-T6** Anomaly detection(embedding + z-score)
- [ ] **P6-T7** AI 调用本身入审计 + budget cap

#### 验收标准

| # | 标准 | 通过条件 |
|---|---|---|
| AC-P6-1 | 默认关闭 | 全新部署 AI 辅助默认 disabled |
| AC-P6-2 | endpoint 白名单 | 配置 SaaS LLM URL 启动失败 |
| AC-P6-3 | AI 调用入审计 | 每次 AI 调用产生 `category: ai_*` event |
| AC-P6-4 | budget | 超限自动停 + 告警 |
| AC-P6-5 | 误判可追溯 | AI 输出与原始 findings 并存,auditor 可看原始 |

---

## 15. 风险登记(Risk Register)

| # | 风险 | 概率 | 影响 | 缓解 | 责任人 |
|---|---|---|---|---|---|
| R1 | NDJSON 单文件过大 | 中 | 中 | 按日 + 按大小双切分(>100MB) | 后端 |
| R2 | scanner 性能退化 | 中 | 高 | 规则数上限 50,大词典 AC,bench gate | 后端 |
| R3 | hash chain 在并发写下断链 | 低 | 高 | per-agent 独立 chain,全局索引串行写入 | 后端 |
| R4 | mitm 部署门槛高 | 高 | 中 | 提供安装脚本 + 证书部署文档 + 自动化测试 | 平台 |
| R5 | 误报淹没 dashboard | 高 | 中 | 噪声治理(7.6)+ 灰度 + 一键 FP | 后端 + 前端 |
| R6 | preventive 误报阻断真实业务 | 中 | 高 | inline 默认仅 3 条 + 灰度策略 + 紧急 disable 开关 | admin |
| R7 | 归档密钥丢失 | 低 | 极高 | KMS + 密钥托管 + 多人控制 | 安全 |
| R8 | 审计代码本身有 bug 漏检 | 中 | 极高 | Detective 不可关 + 多版本 rules + 外部验证工具 | 后端 |
| R9 | AI 辅助造成回流泄露 | 中 | 高 | 默认关 + endpoint 白名单 + 调用入审计 | 安全 + 后端 |
| R10 | 时钟篡改影响顺序 | 低 | 中 | monotonic + chain | 后端 |
| R11 | 事故邮件被垃圾邮件过滤 | 中 | 高 | DKIM + SPF + DMARC + 多通道冗余(IM 同发) | 安全 + 平台 |
| R12 | 责任人离职但仍是收件人 | 中 | 中 | `responsible_persons` 定期复查(每月)+ 邮件投递失败检测 | admin |
| R13 | AI 给出错误补救建议 | 中 | 中 | 邮件免责声明 + 必须以企业流程为准 + AI 输出入审计可回溯 | 安全 |
| R14 | Ack 链接被钓鱼伪造 | 低 | 中 | HMAC 签名 + 30 天过期 + ack 行为本身入审计 | 后端 |

---

## 16. 测试策略

| 类型 | 内容 | 频率 |
|---|---|---|
| 单元测试 | scanner / chain / canonical / decider | 每次 commit |
| 集成测试 | 端到端 turn → event → dashboard | 每次 PR |
| 性能测试 | inline / async / dashboard 查询 | 每次 release |
| 混沌测试 | scanner panic / 磁盘满 / 时钟跳 / 进程被 kill | 每月 |
| 合规验证 | hash chain 篡改检测 / 旁路检测 | 每次 release |
| 渗透测试 | 第三方 | 每半年 |
| 外审模拟 | 内部模拟 SOC2 审计流程 | 每年 |

---

## 17. Operational Runbook(运维手册)

### 17.1 启动检查清单

- [ ] `~/cicy-ai/audit/policy.json` 存在且解析通过
- [ ] `chain-state.json` 与 audit.ndjson 一致
- [ ] machine_id 与历史一致(防机器混淆)
- [ ] 通知通道连通性(SSE 自检)
- [ ] 磁盘剩余 > 10 GB
- [ ] 上次 rotate 完成时间 < 25 小时

### 17.2 常见故障处理

| 现象 | 处置 |
|---|---|
| `audit_pipeline_error_total` 突增 | 检查 scanner 日志,定位规则;可临时灰度 |
| chain check 失败 | 进入调查模式;不允许新写入;告警 admin;手动复核 |
| 磁盘将满 | 触发紧急 cold→archive;调短 hot_days |
| inline 阻断频次异常 | 检查最近 policy 变更;考虑回滚 |
| AI 辅助预算超限 | 自动停;通知 admin |
| 事故邮件 3 次重试失败 | critical 告警 + dashboard 红条 + 手动转人工 + 检查 SMTP 配置 |
| AI 补救调用大量失败 | 邮件继续走回落模板;同时排查 endpoint;持续 1h 失败 → 告警 |

### 17.3 事故响应 SOP(对应 §9.7)

收件人收到 `[CICY-AUDIT][HIGH/CRITICAL]` 邮件后的标准动作:

1. **0-5 分钟**:点邮件中 `dashboard_url` 确认事件真实性(非误报)
2. **5-15 分钟**:按 AI 给出的 `immediate_actions` 逐条执行(优先吊销凭据 / 断连 agent)
3. **15-30 分钟**:确认无二次扩散后,点邮件 `ack_url` 关闭告警(防止 escalation)
4. **24 小时内**:在 dashboard 上补充 `incident_notes` 说明真实原因 + 完成 `longer_term` 加固
5. **每周复盘**:Top N 事故纳入安全周会

**ack_url 安全**:
- 携带签名 token(HMAC-SHA256,token 含 event_id + recipient + exp)
- 30 天过期
- 点击仅完成 ack 动作,不展示敏感内容(需登录 dashboard 看详情)

### 17.4 应急开关

| 场景 | 开关 | 影响 |
|---|---|---|
| inline 误阻断 | `policy.preventive.disabled = true` + reload | 仅 detective,业务恢复 |
| pipeline 卡死 | `policy.enabled = false`(极端)| 全部停,等同关闭审计;**慎用,需上级批准** |
| 告警风暴 | `policy.notify.suspended = true` | 仅落盘不通知 |

---

## 18. 附录

### A. 术语表(Glossary)

- **Audit Event**:本系统输出的一条记录,代表一次 LLM 出入站或一次元审计操作
- **Detective Control**:事后侦测控制(本系统的 async pipeline)
- **Preventive Control**:事前预防控制(本系统的 inline scanner)
- **Channel**:audit 数据来源通道(A: gateway / B: mitm)
- **Finding**:一条规则在 payload 中的命中
- **Hash Chain**:每条 event 包含前一条 hash 形成的不可篡改链
- **Seal**:对一个时间窗内 audit 切片的链尾签名归档
- **Meta-audit**:对 audit 本身访问/变更的审计
- **Severity**:findings 的严重等级(low/medium/high/critical)
- **Action**:命中后的处置(log/notify/redact/block/none)
- **Fail Mode**:scanner 异常时的策略(open/closed)
- **WORM**:Write-Once-Read-Many,归档防篡改存储模式

### B. 参考标准

- SOC 2 Type II — Trust Service Criteria(2017,AICPA)
- ISO/IEC 27001:2022
- GB/T 22239-2019 信息安全技术 网络安全等级保护基本要求
- PCI DSS v4.0
- GDPR(EU 2016/679)/ 中国个人信息保护法
- NIST SP 800-92 — Guide to Computer Security Log Management

### C. policy.json 完整样例

见 §6.2,运行时部署样例置于 `api/mgr/audit/templates/policy.default.json`。

### D. Sample Audit Event

见 §6.1。运行后第一条 event 会写入 `~/cicy-ai/workers/w-10001/history/audit.ndjson`,可用 `jq` 验证。

### E. CLI 规格

```
cicy audit verify [--from DATE] [--to DATE] [path]
    校验本机 hash chain 完整性
    退出码 0 完整,1 损坏,2 错误

cicy audit init [--policy PATH]
    初始化 policy.json 模板与目录结构

cicy audit export --from DATE --to DATE --format ndjson|csv|cef [--out FILE]
    导出指定时间区间,附 sha256 + signature

cicy audit verify-archive ARCHIVE_PATH [--public-key KEY]
    独立验证归档(可在无 cicy-code 环境运行)

cicy audit replay --event ID
    用当前规则集回放某条 event,显示新旧 findings 差异
```

---

**文档结束。下一步**:Phase 1 起手,先实现 P1-T1 / P1-T2(包骨架 + 类型定义),配合单元测试。
