# cicy-code 审计 — Policy 编写指南

`policy.json` 是审计行为的唯一可调旋钮。本文档逐字段讲清楚每个开关的语义、默认值、互动关系,以及编写自定义规则的常见模式。

> 角色匹配:**安全 / 合规团队**;熟悉 JSON,理解正则。
>
> 想知道 UI 里能干啥:[operator-quickstart.md](./operator-quickstart.md)。
> 想知道怎么部署 / 配 Resend:[admin-setup.md](./admin-setup.md)。

---

## 0. policy.json 概览

```
~/cicy-ai/audit/policy.json          ← 改这个文件
```

容器内 audit 进程通过 fsnotify 监控该文件父目录;**任何 write/rename 在 200ms 内热重载**,无需重启。

加载失败(JSON 语法错 / 字段类型不对 / 引用了不存在的 builtin rule) → log 错误,**保留上一份生效配置**。审计永远在跑,不会因为一份坏 policy 停摆。

最小有效:`{"enabled": true}`(其它字段全用默认值)。

---

## 1. 顶层字段

```jsonc
{
  "version": 1,                        // schema 版本,目前固定 1
  "enabled": true,                     // 总开关,false = scanner 永远返回 []
  "fail_mode": "open",                 // open(默认) | closed,scanner panic 时的策略
  "rules_override": [...],             // §2
  "custom_rules": [...],               // §3
  "allow_list": {...},                 // §4
  "notify": {...},                     // §5
  "preventive": {...},                 // §6
  "incident_response": {...},          // §7
  "responsible_persons": {...}         // §8
}
```

未识别的字段会被静默忽略 — 操作员手动加的内容(注释字段等)不会被改回。

---

## 2. rules_override — 调整内置规则

10 条内置规则在二进制里固定。要在某些规则上 disable / 改 severity / 改默认 action,用 override。

```jsonc
"rules_override": [
  {"id": "secret.aws_akid", "severity": "critical"},   // 升级到 critical
  {"id": "pii.phone_cn",   "disabled": true},          // 完全关掉
  {"id": "secret.jwt",     "default_action": "notify"} // 改默认动作
]
```

### 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 必填,必须是 [内置规则 ID](#内置规则全集);引用不存在的 ID 会触发 validate error |
| `disabled` | bool | true = 完全从 ruleset 中移除该规则 |
| `severity` | "low"\|"medium"\|"high"\|"critical" | 替换默认 severity |
| `default_action` | "log"\|"notify"\|"redact"\|"block"\|"none" | 替换默认 action |

### 内置规则全集

```
secret.private_key        high   outbound  inline  block    -----BEGIN ... PRIVATE KEY-----
secret.aws_akid           high   outbound  inline  redact   (?:AKIA|ASIA)[0-9A-Z]{16}
secret.aws_secret         high   outbound  inline  redact   40-char base64 near AKID/SecretAccessKey
secret.jwt                medium outbound  no      log      eyJ[...]\.eyJ[...]\.[...]
secret.bearer_token       medium outbound  no      log      Bearer\s+[A-Za-z0-9.\-+/=_]{20,}
secret.high_entropy       medium outbound  no      log      Shannon ≥ 4.5 + 关键字上下文
pii.id_card_cn            medium both      no      log      18 位 + 校验位(GB 11643-1999)
pii.bank_card             medium both      no      log      13-19 位 + Luhn
pii.phone_cn              low    both      no      log      1[3-9]\d{9}
network.private_ip        low    outbound  no      log      10. / 172.16-31. / 192.168.
```

---

## 3. custom_rules — 编写自定义规则

```jsonc
"custom_rules": [
  {
    "id": "custom.project_alpha",            // 必须以 "custom." 开头
    "label": "Project Alpha codename",       // 可选,人类可读
    "category": "corp",                      // 可选,default "custom"
    "severity": "medium",                    // 必填
    "scan_directions": ["outbound","inbound"], // 必填,非空
    "inline": false,                         // 可选,默认 false
    "default_action": "log",                 // 可选,默认 log
    "match": {
      "type": "regex",                       // "regex" | "dict_file"
      "pattern": "PROJECT-ALPHA|alpha-falcon",
      "flags": "i"                           // 可选,Go regex flag(i/m/s/U)
    }
  }
]
```

### 必填校验

加载时 validate:
- `id` 必须以 `custom.` 开头(避免与内置 ID 冲突)
- `severity` 必须是四档之一
- `scan_directions` 至少含 outbound / inbound 中一个
- `match.type` 必须是 `regex` 或 `dict_file`
- `match.pattern`(regex 时)必须能 `regexp.Compile` 通过
- `match.path`(dict_file 时)必须非空(但可以指向不存在的文件;运行时打不开就该规则空匹配)

不通过 → policy reload 失败,**保留上一份**,容器日志报错。

### 3.1 regex 模式

```jsonc
{"id": "custom.demo", "severity": "medium", "scan_directions": ["outbound"],
 "match": {"type": "regex", "pattern": "DEMO-\\d{4}"}}
```

- 完全是 Go `regexp` (RE2),**不支持反向引用 / lookahead**
- `flags` 拼成 `(?<flags>)` 前缀,例:`flags="i"` → `(?i)DEMO-\d{4}`
- 命中时 preview 取整个匹配,**自动脱敏**(前 4 + `****` + 后 4)

### 3.2 dict_file 模式

适合**词典**类(客户名、产品代号、内部系统名)。

```jsonc
{"id": "custom.customer_list", "severity": "medium",
 "scan_directions": ["outbound","inbound"],
 "match": {"type": "dict_file", "path": "~/cicy-ai/audit/dict/customers.txt"}}
```

dict 文件格式:

```
# 每行一个词,# 开头注释,空行跳过
Acme Corp
Beta Industries
Charlie LLC
```

匹配:
- **literal substring** —— `bytes.Index` loop;**不是正则**
- **大小写敏感**(目前 v1 不支持忽略大小写;workaround 是把不同大小写都列出来)
- 多匹配 → 一条 finding 多个 spans,每个 span 一个 preview
- 词典大小没硬上限,但**逐词 `bytes.Index` 复杂度 O(n × m × payload)** —— 几百词内 OK,过万的话等 Phase 2-T3 Aho-Corasick 优化

### 3.3 常见自定义规则配方

**内部 Jira ticket ID 不准外传**:
```jsonc
{"id": "custom.jira_internal", "severity": "medium",
 "scan_directions": ["outbound"],
 "match": {"type": "regex", "pattern": "INTERNAL-\\d{4,6}"}}
```

**邮箱**:
```jsonc
{"id": "custom.email", "severity": "low",
 "scan_directions": ["outbound","inbound"],
 "match": {"type": "regex", "pattern": "[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}"}}
```

**自定义 token 前缀(API key 形式)**:
```jsonc
{"id": "custom.your_token", "severity": "high",
 "scan_directions": ["outbound"],
 "inline": true,           // 走 preventive 阻断/脱敏
 "default_action": "redact",
 "match": {"type": "regex", "pattern": "sk-corp-[A-Za-z0-9]{32}"}}
```

---

## 4. allow_list — 误报豁免

**event 永远落盘(detective 完整性)**,但匹配 allow_list 的事件 findings = [] 且 `meta.allowlisted_by` 标明原因。

```jsonc
"allow_list": {
  "agents":         ["w-trusted-bot", "w-1*"],     // 整个 agent 豁免
  "paths":          ["mitm:flow-known-fp-prefix"], // payload_ref 前缀豁免
  "content_hashes": ["sha256:abc...","sha256:def..."]
}
```

### 4.1 三种匹配方式

| 字段 | 匹配 | 用途 |
|---|---|---|
| `agents` | 完全匹配 agent_id;不支持通配 | 标记可信 agent(eg 已审过的 bot,放心让它读敏感内容) |
| `paths` | `payload_ref` 前缀 | 整个 mitm flow 类别豁免(eg 所有走 `mitm:flow-known-fp-` 前缀的事件)|
| `content_hashes` | `payload_sha256` 完全匹配 | 已确认是 FP 的具体内容 |

**注**:`agents` 不支持通配符,跟 `responsible_persons.by_agent` 不一样。

### 4.2 通过 Mark FP 按钮自动加 content_hash

operator 在 dashboard 详情面板点 **⚠ 标为误报**,后端调 `POST /api/audit/allowlist/content`,**自动 append 到 `content_hashes`,原子写 policy.json,fsnotify 热重载**。手编辑同一文件不会丢 — 用了 `map[string]interface{}` 写回,未知字段保留。

### 4.3 优先级

allow_list 检查发生在 **scanner 之后,decideAction 之前**:
1. event 已构建,findings 已 scan
2. CheckAllowList(agent, payload_ref, payload_sha256) 按 agents > paths > content_hashes 顺序找
3. 命中 → 清空 findings,标 meta,继续走 Append
4. **preventive 路径有独立检查**:在 PreventiveCheck 入口处先看 allow_list,trusted agent 即便命中 block 规则也不会被阻断

---

## 5. notify — 通知 + 噪声治理

```jsonc
"notify": {
  "min_severity": "medium",                          // 低于此不上 SSE
  "rate_limit": {
    "window_seconds": 3600,                          // 默认 1 小时
    "max_per_agent_per_rule": 50                     // 默认 50
  },
  "cooldown": {
    "seconds": 86400                                 // 默认 24 小时
  },
  "channels": [                                      // 仅元数据,真正通道实现见 §7
    {"type":"sse"},
    {"type":"webhook","url":"https://your-siem"}
  ],
  "suspended": false                                 // 紧急静音开关
}
```

### 5.1 rate_limit:同 agent + 同规则 滑动窗口

> 同一 agent 同一规则,1 小时内 notify 超过 50 次 → 后续事件标 `meta.notify_suppressed_by: rate_limit`,event 仍落盘,channel 不发(Phase 3+ 实现后)。

调整建议:
- 实验室环境 / 调试:把 max 调大(200+),避免被限到看不见信号
- 生产 / 安静期:默认 50 即可

### 5.2 cooldown:同 finding-hash 长 TTL

> finding-hash = sha256(agent + rule + first_span_preview)。同一 hash 24 小时内 notify 一次。

跟 incident_response.cooldown 是**两套不同的 cooldown** — 一个管 dashboard SSE,一个管邮件。

### 5.3 suspended:紧急静音

例:误报洪水 / 凭据被泄洪水搞撑系统。设 `suspended: true`,热重载,所有 notify 标 `meta.notify_suppressed_by: suspended`。

事件依然落盘,所以审计完整性不丢。

### 5.4 channels(Phase 3+ 实现路径)

Phase 1-2 这块只是 metadata 占位。Phase 3 起会按 type 路由:
- `sse` → 推给 dashboard 前端
- `webhook` → POST 到指定 URL
- `im_feishu` / `im_wecom` / `im_slack` / `im_dingtalk` → IM bot
- `email` → 走 Resend(目前**只在 incident_response 用到 Resend**)
- `syslog_cef` / `syslog_leef` → 转发到 SIEM

目前 cut 2c 配上没有效果,留 design 完整性。

---

## 6. preventive — 实时 DLP

```jsonc
"preventive": {
  "enabled": false,         // 默认 OFF
  "fail_mode": "open"       // open | closed
}
```

### 6.1 默认 OFF

开起来需要管理员显式 opt-in,见 [admin-setup.md §4](./admin-setup.md#4-启用-preventive-dlp-阻断)。

### 6.2 触发条件

事件在 Channel A(cicy gateway) 或 Channel B(mitm) 入站时:
1. 检查 allow_list(允许 trusted agent 跳过)
2. 跑完整 scanner
3. 过滤出 findings 中**源规则同时满足** `Inline:true + DefaultAction in {block, redact}` 的子集
4. 若有 block-default → 整个请求被拒,HTTP 451
5. 否则若有 redact-default → 改写 payload(`[REDACTED:rule_id]`)+ 加密保存原文 + 转发改写版
6. 否则放行

**block 永远赢 redact**。同 turn 命中私钥+AKID 时,block + 仅记录 private_key finding。

### 6.3 fail_mode

scanner 内部 panic 时:
- `open`(默认):返回 Action=none,事件像没事一样继续走 detective
- `closed`:返回 Action=block,请求 503 拒绝

金融 / 政企等合规严格场景用 closed。

### 6.4 redact 后的原文保存

每次 redact 触发:
- 原 payload AES-256-GCM 加密,存到 `~/cicy-ai/workers/<agent>/.cicy/history/pre-redact/<event_id>.enc`
- 密钥独立机器:`~/cicy-ai/audit/.preredact.key`(0600,丢了不可恢复)
- TTL 7 天(目前 v1 没自动 rotate,手工清)

auditor 拿到 event_id → `audit DecryptPreRedact` 工具(尚未独立成 CLI 子命令,留 Phase 4)。

---

## 7. incident_response — 事故邮件

```jsonc
"incident_response": {
  "enabled": false,
  "trigger_min_severity": "high",
  "cooldown_seconds": 1800,
  "email_template": "default",
  "email_from": "audit@yourcorp.com",   // 可选,覆盖 email.json 的 from_address
  "languages": ["zh-CN","en"],
  "ai_remediation": {
    "enabled": false,
    "endpoint": "https://internal-llm.corp/v1",
    "model": "internal-fast",
    "api_key": "sk-internal-...",
    "max_tokens": 600,
    "timeout_seconds": 10
  }
}
```

### 7.1 触发条件

`enabled=true` AND event 的 top finding severity ≥ `trigger_min_severity`(默认 high),AND 当前 finding-hash 在 `cooldown_seconds` 内首次出现(默认 30 分钟)。

### 7.2 邮件 from / API key 来源优先级

**From 地址**:
1. `policy.incident_response.email_from`
2. `CICY_RESEND_FROM` env
3. `~/cicy-ai/db/email.json` 的 `from_address`

**API key**:
1. `CICY_RESEND_API_KEY` env
2. `~/cicy-ai/db/email.json` 的 `api_key`

任一不全 → 退到 `FileMailer`(写 `.eml` 文件到 `~/cicy-ai/audit/email-out/`,不发真邮件)。

### 7.3 cooldown

per finding-hash,默认 1800s。同一密钥/PII 反复出现不会刷邮件。

> 跟 notify.cooldown 是两套独立 cooldown — notify.cooldown 管 dashboard,这个管邮件。

### 7.4 AI remediation(可选)

**默认关**。开起来需要:
- 企业自托管 OpenAI-compatible /chat/completions endpoint
- model + api_key
- **绝不连 SaaS LLM**(audit 元数据二次外泄)

prompt body **只含 metadata + 脱敏 preview**,绝不发原 payload(代码 + 单测保证)。

失败兜底:DNS / timeout / parse 错 → 邮件照发,AI 段是占位文字 ("AI 辅助未启用或不可用 — 配置...")。

---

## 8. responsible_persons — 邮件收件人

```jsonc
"responsible_persons": {
  "default":     ["sec-oncall@corp"],
  "by_severity": {"critical": ["ciso@corp"]},
  "by_agent":    {"w-prod*": ["platform@corp"], "w-10001": ["alice@corp"]},
  "by_user":     {"u-abc": ["alice@corp"]},
  "by_rule":     {"secret.aws_akid": ["devops@corp"]}
}
```

### 8.1 解析顺序(并集去重)

按以下顺序匹配所有列表,**取并集** + **字典序去重**:

1. `by_rule[<rule_id>]` —— 任一 finding 的 rule_id
2. `by_user[<user_id>]` —— 事件的 user_id
3. `by_agent` —— event.agent_id 匹配 key(支持 trailing `*` 通配)
4. `by_severity[<severity>]` —— event 的 top severity
5. **若以上没有任何匹配** → 使用 `default`

`default` 是 fallback。任一上层匹配 → **不再加 default**。

### 8.2 通配符

`by_agent` 支持 trailing `*`:
- `"w-prod*"` 匹配 `w-prod-bot`, `w-prod-001`, ...
- `"w-10001"` 严格相等
- 不支持 middle `*` / regex / 别的通配

### 8.3 验证

写完 policy 后,可用以下命令模拟解析(目前没 CLI,看 logs):

```bash
# trigger 一次 high event 给已配的 agent,看 logs
docker logs cicy-code-audit-dev | grep "incident email dispatched"
# [audit] incident email dispatched event=... severity=high recipients=[a@b, c@d]
```

---

## 9. fail_mode 横向对照

不同子系统都有 fail_mode 概念,容易混。**整理对照**:

| 配置 | 影响 | open(默认) | closed |
|---|---|---|---|
| `policy.fail_mode` | scanner 总体 | scanner panic 时返回空 findings,detective 继续 | scanner panic 时返回 503 (TODO: cut 2.5 还没生效) |
| `policy.preventive.fail_mode` | inline preventive | scanner panic 时 action=none,放行 | scanner panic 时 action=block,拒绝 |

目前(Phase 3 cut 2)只有 preventive.fail_mode 真生效。policy.fail_mode 暂时是 reserved field。

---

## 10. 调试与验证流程

### 10.1 改 policy 之前先 dry-run

```bash
# 校验 JSON 语法
jq . < new-policy.json

# 让 audit 加载并 validate(不写入)
docker exec -i cicy-code-audit-dev sh -c '
  cp /home/cicy/cicy-ai/audit/policy.json /tmp/policy.bak
  cat > /home/cicy/cicy-ai/audit/policy.json' < new-policy.json
sleep 1
docker logs cicy-code-audit-dev --tail 5 | grep -E "policy"
# 若 fail → cp /tmp/policy.bak ... 回滚
```

### 10.2 试规则匹配

写完新 custom_rule,trigger 一次包含目标内容的 event:

```bash
TOKEN=cicy_xxx
curl -sS -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"agent_id":"w-test","direction":"outbound","payload":"PROJECT-ALPHA next steps"}' \
  http://localhost:8027/api/audit/ingest

sleep 1
curl -sS -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8027/api/audit/events?agent_id=w-test&limit=1" \
  | jq '.events[0].findings'
# [{"rule_id":"custom.project_alpha","severity":"medium","match_count":1,"spans":[{...}]}]
```

### 10.3 验证 allowlist 生效

把上一步那条 event 的 `payload_sha256` 加到 allow_list:

```bash
SHA=$(curl -sS -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8027/api/audit/events?agent_id=w-test&limit=1" \
  | jq -r '.events[0].subject.payload_sha256')

curl -sS -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"sha256\":\"$SHA\",\"reason\":\"FP test\"}" \
  http://localhost:8027/api/audit/allowlist/content

# 重发同 payload
curl -sS -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"agent_id":"w-test","direction":"outbound","payload":"PROJECT-ALPHA next steps"}' \
  http://localhost:8027/api/audit/ingest

sleep 1
curl -sS -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8027/api/audit/events?agent_id=w-test&limit=2" \
  | jq '.events[0] | {findings, allowlisted_by: .meta.allowlisted_by}'
# {"findings":[], "allowlisted_by":"content_hash"}
```

---

## 11. 完整 policy.json 样例

可以直接 copy → 改邮箱后扔进生产:

```jsonc
{
  "version": 1,
  "enabled": true,
  "fail_mode": "open",

  "rules_override": [
    {"id": "pii.phone_cn", "severity": "medium"},
    {"id": "secret.jwt",   "severity": "high"}
  ],

  "custom_rules": [
    {
      "id": "custom.customer_list",
      "category": "corp",
      "severity": "medium",
      "scan_directions": ["outbound","inbound"],
      "default_action": "log",
      "match": {"type": "dict_file", "path": "~/cicy-ai/audit/dict/customers.txt"}
    },
    {
      "id": "custom.internal_jira",
      "severity": "medium",
      "scan_directions": ["outbound"],
      "match": {"type": "regex", "pattern": "INTERNAL-\\d{4,6}"}
    }
  ],

  "allow_list": {
    "agents":         ["w-trusted-research"],
    "paths":          [],
    "content_hashes": []
  },

  "notify": {
    "min_severity": "medium",
    "rate_limit": {"window_seconds": 3600, "max_per_agent_per_rule": 50},
    "cooldown":   {"seconds": 86400},
    "suspended": false
  },

  "preventive": {
    "enabled": true,
    "fail_mode": "open"
  },

  "incident_response": {
    "enabled": true,
    "trigger_min_severity": "high",
    "cooldown_seconds": 1800,
    "email_from": "audit@yourcorp.com",
    "ai_remediation": {
      "enabled": false,
      "endpoint": "https://internal-llm.yourcorp.corp/v1",
      "model": "internal-fast"
    }
  },

  "responsible_persons": {
    "default":     ["sec-oncall@yourcorp.com"],
    "by_severity": {"critical": ["ciso@yourcorp.com"]},
    "by_agent":    {"w-prod*": ["platform@yourcorp.com"]},
    "by_rule":     {
      "secret.aws_akid":   ["devops@yourcorp.com"],
      "secret.private_key":["devops@yourcorp.com","sec-oncall@yourcorp.com"],
      "pii.id_card_cn":    ["legal@yourcorp.com"]
    }
  }
}
```

---

## 12. 参考

- 设计文档(深入架构原理):[../v1/audit-system-design.md](../v1/audit-system-design.md)
- 部署指南:[admin-setup.md](./admin-setup.md)
- 终端用户视角:[operator-quickstart.md](./operator-quickstart.md)

— *policy-authoring.md / cicy-code audit v1*
