# cicy-code 审计 — Operator 快速上手

这份文档写给**每天用 cicy-code 跑 agent 的人**。看完你会知道:审计在 UI 哪里、什么会被记录、什么时候你会收到事故邮件、看到误报怎么标。

> 角色匹配:你不需要会读 policy.json,也不需要管 Resend 账号。
>
> 如果你是管理员、要配置策略,请看 [admin-setup.md](./admin-setup.md) 和 [policy-authoring.md](./policy-authoring.md)。

---

## 1. 审计在 UI 哪里

打开浏览器后,左侧 activity bar 自上而下有这几个按钮:

```
👥  Team
📦  Skills
📦  AI providers
💬  IM platforms
🛡   Audit       ← 这个
```

点 **盾牌 (Audit)** 图标 → 全屏 modal 展开,默认在 **Findings** tab。

### Findings tab 长什么样

```
┌── 过滤栏 ─────────────────────────────────────────────────┐
│  [全部 agent ▾] [全部严重度 ▾] [全部方向 ▾]  ☑自动刷新 (5s) │
├──────── 统计 ────────┬─────────────────────────────────────┤
│ Total | CRITICAL | HIGH | MEDIUM | LOW                     │
├──────────────────────────┬─────────────────────────────────┤
│ 时间 | Agent | 方向 | 提供商 | 严重度 | 动作                │
│  10:23 w-10001  out anthropic [HIGH] notify  >              │
│  10:22 w-10001  out anthropic [LOW]  notify  >              │
│  ...                                                         │
└──────────────────────────┴─────────────────────────────────┘
```

每 5 秒自动刷一次(可手动关)。点击行 → 右侧详情面板展开。

---

## 2. 什么东西会被记录

简单规则:**你和你的 agent 跟外部 LLM 之间每一次通信都被记录**。

具体来说:
- agent(claude / codex / opencode 等)通过 cicy gateway 发出去的每一次 prompt
- LLM 返回的每一次 reply
- agent 走 mitmproxy(绕开 gateway)直连 LLM 的请求

每条事件包含:
- **时间** (RFC3339 nano + monotonic,防系统时钟回拨)
- **谁** (agent_id + agent_type + user_id + 机器 ID)
- **目标** (provider / model / 方向)
- **payload SHA256** (内容指纹,**不存原文**,原文继续在 `current.json` / `reply.json` 里)
- **命中** (如果检测到 PII/密钥等)
- **动作** (log / notify / block / redact)

底层 NDJSON 文件 + hash chain。你不需要懂这些,但**审计员可以独立验证记录没被篡改**。

---

## 3. 严重度四档

每条 finding 会被分到下面一档:

| Severity | 颜色 | 含义 | 你会看到 |
|---|---|---|---|
| 🔘 LOW | 灰 | 信息性,不一定真泄露(例:内网 IP)| dashboard 里能看到,不发邮件 |
| 🟡 MEDIUM | 琥珀 | 高概率真敏感(例:JWT / 手机号 / 身份证)| dashboard 高亮,IM 摘要 |
| 🟠 HIGH | 橘 | 几乎肯定泄露(例:AKID / 私钥 header)| dashboard 红边 + **邮件发给责任人** |
| 🔴 CRITICAL | 红 | 关键资产(例:root CA / 大批量 PII)| 顶部红条 + 强制 ack + CISO 抄送 |

---

## 4. Findings 详情面板

点一行 → 右侧展开详情:

```
事件详情  evt_a1b2c3...   [📋 Copy]    [⚠ 标为误报]

身份
    agent_id      w-10001
    agent_type    claude
    user_id       u-abc
    source_channel gateway

主体
    provider      anthropic
    model         claude-opus-4-7
    direction     outbound
    payload_size  12345 bytes
    payload_ref   current.json#turn_xxx
    payload_sha256 sha256:abcd...

决策
    action        notify
    applied       true
    fail_mode     open
    evaluated_inline  false
    evaluated_async   true

时间与链
    ts                2026-05-15T11:21:22.123456789Z
    prev_hash         sha256:...
    self_hash         sha256:...
    rules_version     2026.05.15-skeleton
    policy_hash       sha256:...

命中规则 (1)
  ┌────────────────────────────────────────────┐
  │ [HIGH] secret.aws_akid  ×1                 │
  │   [12:32] AKIA****MPLE                     │
  └────────────────────────────────────────────┘
```

**Preview 是脱敏后的**:`AKIA****MPLE` —— 前后 4 字符可见,中间星号。即便你能看到 dashboard,也看不到完整密钥。

---

## 5. 标记误报 (Mark FP)

scanner 偶尔会误报。例:你的客户名单包含"AcmeCorp",但有条 finding 把它当 secret 报出来 —— 这就是误报。

**操作**:
1. 在详情面板右上点 **⚠ 标为误报** 按钮
2. 弹出输入框填一句话说明(可选,但建议写。比如 "公司客户名,非凭据")
3. 后端把这条 event 的 `payload_sha256` 加进策略白名单
4. **以后任何内容 SHA 完全相同的事件,scanner 不再报这条规则**
5. dashboard 列表自动刷新,这条事件 findings 变空,标记 `allowlisted_by: content_hash`

按钮状态:
- **琥珀色 "标为误报"** = 可点
- **灰色 "提交中..."** = 后端在写 policy 文件
- **绿色 "✓ 已加入白名单"** = 完成,再次点不会重复加

**已经被 allowlist 命中的 event 看不到按钮** —— 已经豁免了无需再标。

> 提示:Mark FP 操作不会改原事件,而是改 policy.json。后续相同内容才被过滤。**历史已记录的事件不会被回溯删除** —— 审计完整性优先。

---

## 6. 事故邮件什么时候发

**满足以下全部条件,你或责任人会收到邮件**:
1. 事件 severity ≥ high(默认,管理员可改)
2. policy.incident_response.enabled = true
3. 同一 finding 30 分钟内首次出现(cooldown)
4. 系统配好了 Resend 凭据 + 发件域

收件人怎么决定?管理员在 policy.json 里配 `responsible_persons` —— 按规则 ID / 用户 / agent / 严重度匹配,并集去重。详细见 [policy-authoring.md](./policy-authoring.md#responsiblepersons)。

### 邮件长什么样

```
From:    audit@yourcorp.com
To:      你被列为责任人的邮箱
Subject: [CICY-AUDIT][HIGH] secret.aws_akid — w-10001

事故级别: HIGH
触发时间: 2026-05-15T11:21:22Z
触发 agent: w-10001 (claude)
触发用户: u-abc
出站目标: anthropic / claude-opus-4-7
当时动作: redact (applied: true)
事件 ID: evt_xxx
原文(加密): pre-redact:w-10001/evt_xxx.enc

──────── 命中规则 ────────
  • secret.aws_akid  [high]  ×1  preview: AKIA****MPLE

──────── AI 摘要 ────────
AKID leaked from w-10001; redacted before send.   ← 启用了 AI 才有
...

──────── 立即处置 ────────
  □ 立即吊销该 AKID(AWS console → IAM)
  □ Audit CloudTrail 看是否已被滥用
  □ 处置完点下方 ack 链接

──────── 后续加固 ────────
  • 给该 rule 启用 inline=true block
  • ...

──────── 确认 / Acknowledge ────────
  完成处置后请点击以下链接关闭告警(30 天内有效):
  https://your-public-url/api/audit/ack?token=...

──────── English summary ────────
  Severity: HIGH | Agent: w-10001 | Top rule: secret.aws_akid
  Event ID: evt_xxx | Action: redact (applied=true)

— cicy-code audit automated alert. AI suggestions are advisory.
```

---

## 7. Ack 流程 — 点了链接发生什么

邮件底部的 **确认链接** 是 HMAC 签名的 URL。点击后:

1. 浏览器打开一个朴素页面:`cicy-code audit: 已确认`
2. 后端验证签名 + 检查 30 天有效期
3. 写一条 `meta_alert_ack` 事件到审计链 —— **谁点击、什么时候、从什么 IP** 都被记录
4. 同一事件再点 → 重复记一条 ack 事件(幂等性靠人工流程而非系统强制)

签名校验失败的情况:
- **400** = 链接坏掉了 / 没有 token
- **403** = 签名不对(被人改过或重新拼接的链接)
- **410** = 过期了(超过 30 天)
- **500** = 后端临时故障,稍后重试

> ⚠️ **链接是单次签名,不要转发**。如果你的邮箱被人看到,看到的人也能点 ack —— 这从设计上是个折衷,30 天 TTL 兜底。
>
> 转发链接进群、复制到 Slack 不算违规,但要意识到任何能看到 URL 的人都能确认事件。

---

## 8. 日常工作流(典型一天)

```
9:00   你打开 cicy-code → 跑 agent 写代码 / 调 LLM
       ↑ 后台 audit pipeline 不影响你

11:21  agent 不小心把测试 AKID 粘进 prompt
       → preventive=on:cicy gateway 改写 body,LLM 看不到原值
       → preventive=off:LLM 看到,detective 记录,sev=high

11:21  你被列为该 agent 的 responsible_person
       → 30s 内收到邮件 [CICY-AUDIT][HIGH] secret.aws_akid

11:23  你看了邮件,确认是测试 key,不是生产 → 操作:
       ○ 在 AWS console 吊销那个 key(企业 SOP)
       ○ 回到 cicy-code,打开 Audit dashboard 找到这条事件
       ○ 在详情面板点 ⚠ "标为误报",填 "test AKID, deleted from source"

11:24  Future events with same content → suppressed
       原事件保留,合规可查

11:24  点邮件底部 Ack 链接 → 浏览器显示"已确认" → 审计链多一条 meta_alert_ack
```

---

## 9. 不会泄露什么(给你的隐私保证)

- ✅ Audit 记录 **payload 的 SHA256 + 长度 + 元数据**
- ❌ Audit **NDJSON 文件不存 prompt 原文**
- ✅ 原文仍在 `~/cicy-ai/workers/<agent>/.cicy/history/current.json`(本地 0600)
- ❌ AI 补救建议被发到 LLM 时,**只发元数据 + 脱敏 preview**,绝不发原文(`AKIA****MPLE` 这种)
- ✅ 邮件正文同样只引用 preview,不带原文
- ❌ Ack URL 不带任何敏感内容,只带 event_id + HMAC

---

## 10. FAQ

**Q: 我看不到自己 agent 的事件?**
A: dashboard 左上选择 agent dropdown → 找到 `w-10001:claude`。或者直接在过滤栏选你的 agent_id。

**Q: 同一个误报每次都报,Mark FP 不管用?**
A: Mark FP 按 `payload_sha256` 匹配,即整段内容完全相同才豁免。如果是相似但不一样的内容(例:不同的客户名),需要更广泛规则。请联系管理员调 `allow_list.paths` 或 `dict_file` 词典。

**Q: 我没收到事故邮件?**
A: 多半是管理员还没配 Resend / 没把你加进 responsible_persons。让管理员看 [admin-setup.md](./admin-setup.md)。

**Q: 我以为某事件应该 block,但实际放行了?**
A: 默认 preventive 是关的。打开需要管理员在 policy 里设 `preventive.enabled=true`。即便开了,只有 `inline=true + DefaultAction=block` 的规则才阻断 —— 私钥头是,AKID 不是(它默认 redact 而非 block)。详见 [policy-authoring.md](./policy-authoring.md)。

**Q: chain 校验是什么意思?**
A: 每条事件包含前一条的 hash,改一个字段会断链。审计员能跑 `cicy-code audit verify` 独立验证你的审计记录是否被篡改过。运维不会随意 hash,所以正常情况一直绿。

---

## 11. 紧急情况

| 现象 | 找谁 |
|---|---|
| dashboard 报 ws-connecting 卡住 | 重新登录,持续不通找运维(主程序的事,不是审计的) |
| 我标 FP 按钮变红 / 报错 | 这是新功能,截图给管理员排查 |
| 我收到陌生人的事故邮件 | 邮箱地址被前任放进了 responsible_persons,联系管理员清理 |
| 邮件链接点开 403 | 链接已过期或被改过,联系发件方重发邮件 |

— *operator-quickstart.md / cicy-code audit v1*
