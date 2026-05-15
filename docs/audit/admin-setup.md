# cicy-code 审计 — Admin 部署手册

这份文档写给**部署 / 配置 / 维护 cicy-code 审计的人**。覆盖:首次启用、Resend / 邮件凭据、最小可用 policy、preventive 开关、CLI 校验、合规导出、故障排查。

> 角色匹配:你能 ssh / docker exec / 改 host 配置。
>
> 终端用户怎么用,看 [operator-quickstart.md](./operator-quickstart.md)。
> 规则编写细节,看 [policy-authoring.md](./policy-authoring.md)。

---

## 0. 30 秒上手清单

1. cicy-code 跑起来 — `python3 dev.py --docker --port 8027`
2. 配 Resend 凭据 — host 端 `email config`
3. 写 policy.json — 见 [§2](#2-最小可用-policyjson)
4. 浏览器打开 dashboard 看 Findings tab → 触发一次 turn → 应该有事件
5. 运行 `docker exec cicy-code-audit-dev /app/cicy-code audit verify` → exit 0

后面是细节展开。

---

## 1. 部署模型

```
host machine                                container
─────────────────────────────────────       ─────────────────────────
~/cicy-ai/
  global.json          ─── dev.py ────►    /home/cicy/cicy-ai/global.json
  db/email.json        ─── dev.py ────►    /home/cicy/cicy-ai/db/email.json
                                            /home/cicy/cicy-ai/audit/
                                              .machine_id   (auto-gen 0600)
                                              .preredact.key (auto-gen 0600)
                                              .ack.key       (auto-gen 0600)
                                              chain-state.json
                                              index/YYYY-MM-DD.ndjson
                                              email-out/      (FileMailer 写)
                                              policy.json     ◄── 你编辑
                                            /home/cicy/cicy-ai/workers/<agent>/
                                              .cicy/history/
                                                current.json
                                                reply.json
                                                audit.ndjson
                                                audit-chain.state
                                                pre-redact/*.enc
```

**关键**:
- audit 数据全在容器内
- email.json 通过 dev.py `docker cp` 同步进容器
- policy.json 你直接 `docker exec -i ... > policy.json` 写入
- audit 进程 fsnotify policy.json,~200ms 内热重载

---

## 2. 最小可用 policy.json

最简起点(只开 detective,所有事件落盘,不发邮件):

```json
{
  "version": 1,
  "enabled": true
}
```

写入容器:

```bash
docker exec -i cicy-code-audit-dev sh -c \
  'cat > /home/cicy/cicy-ai/audit/policy.json' <<'EOF'
{
  "version": 1,
  "enabled": true
}
EOF
```

容器日志应出现:

```
[audit] policy reloaded hash=sha256:... active_rules=10 custom=0 enabled=true
```

triger 一次 LLM turn,然后:

```bash
docker exec cicy-code-audit-dev /app/cicy-code audit verify
# OK   /home/cicy/cicy-ai/audit/index/2026-05-15.ndjson  (N events)
# OK   /home/cicy/cicy-ai/workers/w-10001/.cicy/history/audit.ndjson  (N events)
# Summary: X file(s), Y event(s) total, X ok, 0 failed
# exit=0
```

---

## 3. 配置 Resend 邮件

cicy-code 审计内嵌 `ResendMailer`(走 https://api.resend.com/emails)。凭据复用 host 端的 `email` skill 配置文件,**避免维护两份**。

### 3.1 host 端先配好 email skill

```bash
email config   # 交互式,填 Resend API key + verified from_address
email status   # 应显示 config: ready + api_key: re_J****
```

配完之后,`~/cicy-ai/db/email.json` 内容形如:

```json
{
  "api_key": "re_J....rvkW",
  "from_address": "audit@yourcorp.com",
  "default_to": "..."
}
```

### 3.2 让 audit 拿到这份文件

下次跑 `python3 dev.py --docker` 时,dev.py 自动 `docker cp` 进容器。如果容器已在跑:

```bash
docker exec cicy-code-audit-dev mkdir -p /home/cicy/cicy-ai/db
docker cp ~/cicy-ai/db/email.json cicy-code-audit-dev:/home/cicy/cicy-ai/db/email.json
docker exec cicy-code-audit-dev chmod 600 /home/cicy/cicy-ai/db/email.json
```

audit 内部 fsnotify 200ms 内会热切到 ResendMailer:

```
[audit] mailer -> ResendMailer from=audit@yourcorp.com src=file
```

如果看到 `mailer=FileMailer` 一直没切,检查:
- `docker exec cicy-code-audit-dev cat /home/cicy/cicy-ai/db/email.json | jq .api_key`(应非空)
- `docker exec cicy-code-audit-dev cat /home/cicy/cicy-ai/db/email.json | jq .from_address`(应非空)
- 或在 policy.json 加 `incident_response.email_from`(override 用)

### 3.3 验证邮件能真发

```bash
TOKEN=$(grep api_token ~/cicy-ai/global.json | sed 's/.*"\(cicy_[^"]*\)".*/\1/')

# 写一个会触发 high-severity 的 policy
docker exec -i cicy-code-audit-dev sh -c \
  'cat > /home/cicy/cicy-ai/audit/policy.json' <<EOF
{
  "version": 1,
  "enabled": true,
  "incident_response": {"enabled": true, "trigger_min_severity": "high"},
  "responsible_persons": {"default": ["你的邮箱@xxx"]}
}
EOF
sleep 1

# trigger
curl -sS -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"agent_id":"w-test","direction":"outbound","payload":"-----BEGIN RSA PRIVATE KEY-----"}' \
  http://localhost:8027/api/audit/ingest
sleep 3

# log 应该有
docker logs cicy-code-audit-dev | grep "resend message_id"
# [audit] resend message_id=<uuid> event=evt_... recipients=1
```

收件箱(注意垃圾邮件)看到 `[CICY-AUDIT][HIGH] secret.private_key — w-test` → 端到端通了。

---

## 4. 启用 Preventive(DLP 阻断)

**默认 OFF**:cicy-code 装出来是 detective-only。要开 inline 阻断:

```json
{
  "version": 1,
  "enabled": true,
  "preventive": {
    "enabled": true,
    "fail_mode": "open"
  }
}
```

`fail_mode`:
- `open`(默认):scanner 出错时放行,但写 pipeline_error event(可用性优先)
- `closed`:scanner 出错时返回 503,阻止请求出去(合规严格模式)

### preventive 实际效果

只对**同时满足两个条件**的内置规则触发:
- `Inline: true`
- `DefaultAction: block`

目前内置规则里只有 `secret.private_key`。`secret.aws_akid` / `secret.aws_secret` 是 `inline + redact`(脱敏后转发,不阻断)。

效果(Channel B 示例):

```bash
# 没启用 preventive:
curl -X POST .../api/audit/ingest -d '{"payload":"-----BEGIN RSA PRIVATE KEY-----"}'
# HTTP 204, 仅 detective 记录

# 启用 preventive:
curl -X POST .../api/audit/ingest -d '{"payload":"-----BEGIN RSA PRIVATE KEY-----"}'
# HTTP 451 + JSON:
# {"blocked":true,"event_id":"evt_...","reason":"block","rules_hit":["secret.private_key"]}
```

Channel A(走 cicy gateway 的 agent)同样行为 — agent 收到 451,LLM 永远看不到 prompt。

---

## 5. 启用 AI 补救建议

默认 OFF。开起来需要**企业自托管 LLM endpoint**(OpenAI-compatible /chat/completions)。

```json
{
  "incident_response": {
    "enabled": true,
    "ai_remediation": {
      "enabled": true,
      "endpoint": "https://internal-llm.corp.local/v1",
      "model": "internal-fast",
      "api_key": "sk-internal-...",
      "max_tokens": 600,
      "timeout_seconds": 10
    }
  }
}
```

### 严禁连 SaaS LLM

设计上不能把 audit 元数据再交给 OpenAI / Anthropic / DeepSeek 等外部服务 — 即便 prompt 不含原文,SaaS 会记录元数据,违反"audit-of-audit 不能成为新泄露源"。

可接的:
- Ollama / vLLM / SGLang(自托管推理服务)
- Azure OpenAI Private Endpoint(法务确认数据驻留)
- 内部专用 model server

### 失败兜底

任何错(DNS / timeout / HTTP 5xx / JSON 解析失败)→ log+skip → **邮件照发,AI 段是占位文字**。可用性优先。

---

## 6. 公网 URL(给 ack 链接用)

事故邮件里的 ack 链接 `https://your-host/api/audit/ack?token=...` 默认是 `http://localhost:8008`(只有容器内部能访问)。要让真收件人能点:

```bash
# host 端启动前 export(dev.py 会 passthrough 这个 env 到 container)
export CICY_PUBLIC_URL=https://audit.yourcorp.com
python3 dev.py --docker --port 8027
```

或在容器里:

```bash
docker exec cicy-code-audit-dev sh -c \
  'export CICY_PUBLIC_URL=https://audit.yourcorp.com && \
   killall -USR1 cicy-code'  # 当前不支持热切 env;需要重启容器
```

下次重启后,所有新邮件的 ack URL 自动用这个前缀。

> 生产部署强烈建议套 **Cloudflare Tunnel / 反代** + TLS — `cicy-code` 本身只 HTTP 监听。

---

## 7. 噪声治理(收件箱不被淹没)

默认值:

| 配置 | 默认 |
|---|---|
| 同 finding 邮件 cooldown | 30 分钟 |
| 同 (agent, rule) 邮件速率上限 | 50 次/小时 |
| 同 finding-hash dashboard 通知 cooldown | 24 小时 |
| 同 (agent, rule) dashboard 通知速率 | 50 次/小时 |

收件人投诉太吵的常见调整:

```json
{
  "incident_response": {"cooldown_seconds": 7200},   // 改成 2 小时
  "notify": {
    "rate_limit": {"window_seconds": 3600, "max_per_agent_per_rule": 20}
  }
}
```

紧急消音(诈骗式告警洪水):

```json
{ "notify": {"suspended": true} }
```

事件继续落盘,所有 notify 标记 `notify_suppressed_by: suspended`。等危机过后改回 false。

---

## 8. CLI 工具

容器内:

```bash
# 校验当前所有 audit chain
docker exec cicy-code-audit-dev /app/cicy-code audit verify
# Summary: N file(s), M event(s) total, N ok, 0 failed
# exit=0

# 校验单个 agent
docker exec cicy-code-audit-dev /app/cicy-code audit verify \
  /home/cicy/cicy-ai/workers/w-10001/.cicy/history/audit.ndjson

# help
docker exec cicy-code-audit-dev /app/cicy-code audit help

# exit codes
#   0 = 全部完整
#   1 = 至少一条链有完整性错误(篡改 / 顺序错乱 / state 不匹配)
#   2 = 调用错误(参数 / 文件不存在 / 解析错)
```

定时跑(每天):

```bash
# host 端 crontab
0 4 * * * docker exec cicy-code-audit-dev /app/cicy-code audit verify || \
  mail -s "[CICY] AUDIT VERIFY FAILED $(date)" sec-oncall@corp
```

---

## 9. REST API(给 dashboard / 集成用)

所有都需要 `Authorization: Bearer cicy_xxx`(从 `~/cicy-ai/global.json` 拿 `api_token`)。

| 路径 | 方法 | 用途 |
|---|---|---|
| `/api/audit/events` | GET | 列事件,query: `agent_id`, `from`, `to`, `severity`(csv), `rule_id`(csv), `direction`, `limit`, `offset` |
| `/api/audit/events/{id}` | GET | 单事件详情;404 若不存在 |
| `/api/audit/stats` | GET | 聚合:by_severity / by_rule / by_agent / by_action / by_direction |
| `/api/audit/agents` | GET | 有 audit 数据的 agent_id 列表 |
| `/api/audit/ingest` | POST | mitmproxy 上报 (Channel B);body 见 [policy-authoring.md](./policy-authoring.md#channel-b-payload) |
| `/api/audit/allowlist/content` | POST | `{sha256, reason}` 加入 allow_list(Mark FP 用)|
| `/api/audit/ack` | GET | 邮件 ack 链接,无 Bearer,token 为唯一鉴权 |

示例:

```bash
TOKEN=cicy_xxx

# 最近 10 个 high+ 事件
curl -sS -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8027/api/audit/events?severity=high,critical&limit=10" \
  | jq '.events[] | {id, ts, agent: .identity.agent_id, rules: [.findings[].rule_id]}'

# 当日统计
curl -sS -H "Authorization: Bearer $TOKEN" "http://localhost:8027/api/audit/stats" | jq
```

---

## 10. 部署前 checklist

- [ ] `~/cicy-ai/global.json` 含合法 `api_token`
- [ ] `~/cicy-ai/db/email.json` 配好 Resend(`email status` 显示 ready)
- [ ] verified from_address 在 Resend dashboard
- [ ] `~/cicy-ai/audit/policy.json` 至少有 `{enabled: true, incident_response.enabled: true, responsible_persons.default: [...]}`
- [ ] `CICY_PUBLIC_URL` env 指向收件人能点的公网 URL
- [ ] 反代 / cf-tunnel / TLS 配好
- [ ] crontab 加 `audit verify` daily 跑
- [ ] 给 operator 发 [operator-quickstart.md](./operator-quickstart.md)
- [ ] 把 `~/cicy-ai/audit/.ack.key` / `.preredact.key` 备份到独立机器(掉了恢复不了)
- [ ] `~/cicy-ai/workers/*/.cicy/history/` 配 disk-full 告警(NDJSON 增长无 rotate v1)

---

## 11. 故障排查

### audit 没启动

```
docker logs cicy-code-audit-dev | grep audit
```

期望看到:
```
[audit] initialized root=... rules_version=... active_rules=10 ...
```

没有 → cicy-code 可能 crash 在 audit.Init 前。看完整 log。

### 邮件不发

1. `docker logs cicy-code-audit-dev | grep -E "mailer|resend|incident"`
2. 看 mailer 行:`mailer -> ResendMailer` 还是 FileMailer
3. FileMailer 说明凭据没读到:看 `docker exec ... ls -la /home/cicy/cicy-ai/db/email.json`
4. 看 incident dispatched 行:若没有 → trigger severity 没达标 / cooldown 起作用 / responsible_persons 空

### chain verify failed

```
FAIL /home/cicy/cicy-ai/workers/w-xxx/.cicy/history/audit.ndjson  (N events, M errors)
   event evt_xxx @ line N [hash_mismatch] recomputed=... recorded=...
```

含义:
- `hash_mismatch` → 某行的 payload 字段被改过(或者 schema 在升级,需要补 omitempty)
- `chain_break` → prev_hash 不接前一条 self_hash(中间被删行了 / 重排)
- `state_mismatch` → chain-state.json 与文件尾不一致(state 文件被手工编辑过 / 系统崩溃前丢失最后一次 fsync)

**处置**:
1. 第一反应不是修文件,是看谁有权限改它
2. 备份 ndjson + state 文件
3. 联系安全团队 — 篡改链是 incident response 而不是技术问题
4. 临时让审计继续可用:重命名旧文件(`audit.ndjson` → `audit.broken.ndjson`),新事件从空白链开始 —— 失去历史但保留前向完整性

### policy 改了没生效

```
docker logs cicy-code-audit-dev | tail | grep "policy"
```

应有:
```
[audit] policy reloaded hash=sha256:...
```

如果没:
1. 检查 JSON 语法(用 `jq` 校验)
2. 看 `policy reload failed, keeping previous` 行 —— validateError 会告诉你哪个字段错
3. 容器内 fsnotify 失效:重启容器

### Resend 报 "API key is invalid"

```
[audit] incident email send failed event=... : [ERROR]: API key is invalid
```

- host 端 `email status` 重新核对
- key 被 rotate 了 → 在 Resend dashboard 重新生成,`email config` 更新
- key 没 send 权限 → Resend 后台给 key 加 `emails:send` scope
- from_address 域名没 verify → Resend → Domains 加 DNS

---

## 12. 升级 / 数据迁移

当前(v1)无在线升级 —— 重启容器即可:
- audit pkg 改字段(omitempty)→ 老事件 chain 仍 verify
- audit pkg 加字段不带 omitempty → 老 chain verify 会 hash_mismatch(避免这样做)
- 升级前先备份 `~/cicy-ai/audit/` 整个目录 + 所有 `~/cicy-ai/workers/*/.cicy/history/`

---

## 13. 长期保留与归档(尚未自动化)

Phase 1 数据全部在本地 NDJSON,没有自动 rotate / 归档(留 Phase 5)。手动方案:

```bash
# 每月 1 号把上月 index 文件压缩备份到 OSS / S3
docker exec cicy-code-audit-dev tar czf - \
  /home/cicy/cicy-ai/audit/index/2026-04*.ndjson | \
  ossutil cp - oss://your-bucket/audit/2026-04.tar.gz
```

加 hash chain 完整性证书(Phase 5 自动化 placeholder):

```bash
# 拷出归档,计算 SHA + 用机器私钥签名
docker exec cicy-code-audit-dev sha256sum \
  /home/cicy/cicy-ai/audit/index/2026-04-15.ndjson > manifest.txt
```

— *admin-setup.md / cicy-code audit v1*
