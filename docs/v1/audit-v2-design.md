# cicy-code audit-v2 — autonomy-first audit

> 状态:草案 v0.1
> 关联文档:`api/mgr/mitm/README.md`(MITM 模块运维指南)

## 1. 设计哲学

> "以后不要人类配置,就和以后不需要人类写代码一样"

**人不再写 policy.json**。人只写一份十几行的 `autonomy.json`(约束/守则),Agent 在约束内自己读事件、自己改 policy.json、自己向人报告。

类比:今天 AI 已经写了大部分代码,人只 review commit message 和 diff。本系统让 AI 也写 policy,人 review 决策日志。

| 角色 | 写什么 | 读什么 |
|------|--------|--------|
| **人** | `autonomy.json`(约束) | `decisions.ndjson`(Agent 历史) |
| **Agent** | `policy.json`(规则集) + `decisions.ndjson`(行动报告) | `policy.json` + audit events + `autonomy.json` |
| **审计管道** | events ndjson + 全局索引 | (事件源是 MITM/gateway 写入的) |

audit-v1(audit 分支)有 PolicyForm(818 行表单),给"安全/合规团队"用。audit-v2 没有这种表单 — 因为根本不需要人去填字段。

---

## 2. 架构

```
┌──────────────────────────────────────────────────────────────────────┐
│ 客户端 (Cursor / 桌面 App / 浏览器 / SDK)                            │
└──────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼ SOCKS5
┌──────────────────────────────────────────────────────────────────────┐
│ mihomo (路由)                                                        │
└──────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼ SOCKS5
┌──────────────────────────────────────────────────────────────────────┐
│ cicy-mitm   (TLS terminate · audit.SubmitMitmEvent · current/reply.json)
└──────────────────────────────────────────────────────────────────────┘
                                  │ Envelope
                                  ▼
┌──────────────────────────────────────────────────────────────────────┐
│ audit.Pipeline  (扫描 · NDJSON 落盘 · 全局索引)                       │
│                                                                       │
│   policy.json (read)  ◀──┐                                            │
│                          │                          ┌──────────────┐  │
│                          │            读事件 + 读policy │              │  │
│                          │           ┌────────────►│  autonomy    │  │
│                          │           │             │  goroutine   │  │
│                          └───────────┤             │  (每 N 分钟)  │  │
│                          policy.json │             │              │  │
│                          (write)     │◀────────────│  写 policy   │  │
│                                      │             │  写 decisions│  │
│                                      │             └──────────────┘  │
└──────────────────────────────────────┴───────────────────────────────┘
                                                          │
                                                          ▼
                                              ┌──────────────────────┐
                                              │ decisions.ndjson     │
                                              │ (人看的报告)         │
                                              └──────────────────────┘
                                                          │
                                                          ▼
                                              ┌──────────────────────┐
                                              │ Dashboard "Agent" tab │
                                              │ /api/audit/decisions │
                                              └──────────────────────┘
```

---

## 3. autonomy.json — 人类唯一要写的文件

路径:`~/cicy-ai/autonomy/autonomy.json`。文件不存在时 Agent 默认 disabled。

```json
{
  "enabled": true,
  "interval": "10m",
  "lookback": "24h",
  "max_changes_per_hour": 5,
  "max_changes_per_tick": 3,
  "forbidden_actions": [
    "enable_preventive_block"
  ],
  "llm": {
    "endpoint": "https://api.deepseek.com/v1/chat/completions",
    "model": "deepseek-v4-pro",
    "api_key": ""
  }
}
```

| 字段 | 默认 | 含义 |
|------|------|------|
| `enabled` | `false` | 总开关。`false` = Agent 不启动。 |
| `interval` | `10m` | 自动 tick 周期。 |
| `lookback` | `24h` | 每次 tick 看多久内的事件。 |
| `max_changes_per_hour` | `5` | 速率上限。超过 → 跳过新 tick 但记入日志。 |
| `max_changes_per_tick` | `3` | 单次 tick 最多应用几个 action。LLM 提的多余的标记 `per_tick_cap` 跳过。 |
| `forbidden_actions` | `[]` | 禁区动作。已知值:`enable_preventive_block` / `custom_rules_add` / `rules_override` / `allow_list`。 |
| `llm.endpoint` | `$AUTONOMY_LLM_ENDPOINT` / `$CICY_AI_GATEWAY_LLM_ENDPOINT` | OpenAI-compatible /chat/completions URL。 |
| `llm.model` | `deepseek-v4-pro` | 模型名。 |
| `llm.api_key` | `$AUTONOMY_LLM_API_KEY` / `$CICY_AI_GATEWAY_LLM_API_KEY` | Bearer token。 |

---

## 4. 一次 tick 做的事

`api/mgr/audit/autonomy.go:runOneTick`

```
1. 读最近 1 小时的 decisions.ndjson — 数已 applied 数量
   超过 max_changes_per_hour → 写 "rate limited" 决策日志,return.

2. 调 Query() 取 lookback 窗口内的所有事件 (上限 1000).

3. 读 globalPipeline.CurrentPolicy() — 当前生效 policy 摘要.

4. 用事件 + policy 摘要 + forbidden_actions 拼 LLM prompt.

5. POST /chat/completions  (timeout 60s, temp 0.2).

6. 解析 LLM 返回的 {"actions":[...]}.
   每条 action:
     - 严重违反 forbidden_actions → 跳过 (skipped_reason="forbidden:xxx").
     - patch 为空 → 跳过 ("empty_patch").
     - 已经应用 max_changes_per_tick 个 → 跳过 ("per_tick_cap").
     - 否则:applyPatch (合并进 policy.json, WriteGlobalPolicy 原子写).

7. 把整次 tick 的 AutonomyDecision 追加到 decisions.ndjson:
     id, timestamp, trigger, events_window_from/to, events_considered,
     llm_response_text, actions[], policy_hash_before/after, error.
```

`policy.json` 写完后 audit pipeline 的 fsnotify 监控会在 200ms 内热加载新规则 — 下一个 MITM 事件就走新规则。

---

## 5. 决策日志格式

`~/cicy-ai/autonomy/decisions.ndjson`,append-only,一行一个 JSON:

```json
{
  "id": "dec-019e3c25-3005-7090-...",
  "timestamp": "2026-05-25T19:42:49Z",
  "trigger": "interval",
  "events_window_from": "2026-05-24T19:42:49Z",
  "events_window_to":   "2026-05-25T19:42:49Z",
  "events_considered": 47,
  "llm_response_text": "{\"actions\":[...]}",
  "actions": [
    {
      "kind": "allow_list",
      "rationale": "37/40 hits on secret.bearer_token come from agent w-1001 talking to 127.0.0.1:8009 — internal dev tokens, not external secrets",
      "patch": {
        "allow_list": { "paths": ["/api/dev/*"] }
      },
      "applied": true
    },
    {
      "kind": "preventive_toggle",
      "rationale": "FP rate is 0% — safe to enable block on private keys",
      "patch": {
        "preventive": { "enabled": true }
      },
      "applied": false,
      "skipped_reason": "forbidden: enable_preventive_block"
    }
  ],
  "policy_hash_before": "sha256:DEFAULT",
  "policy_hash_after":  "sha256:abc123..."
}
```

每个字段的诚信责任:
- `policy_hash_before` / `policy_hash_after` 不一致 = 政策真的改了
- 哈希一致 + actions 全 skipped = 这一 tick 是空转
- `error` 非空 = LLM 调用挂了 / 解析失败 / Query 失败

---

## 6. HTTP / UI 表面

### 后端 HTTP

```
GET  /api/audit/decisions?limit=100         返回 ReadDecisions(newest first)
POST /api/audit/decisions/run               同步执行一次 tick (trigger="manual")
POST /api/audit/decisions/explain/<id>      LLM 用自然语言解释一次过去的决策
POST /api/audit/decisions/revert/<id>       通过 git revert 回滚某次决策
```

### 后端 CLI

`cicy-code audit autonomy <cmd>` — 不依赖 HTTP server,直接操作本机文件:

```
cicy-code audit autonomy run                同步跑一个 tick(等价于 POST /run)
cicy-code audit autonomy decisions [--limit=N]   列出最近决策(表格输出)
cicy-code audit autonomy explain <id>       同步解释,JSON 输出
cicy-code audit autonomy revert  <id>       同步回滚
cicy-code audit autonomy show-config        打印生效的 autonomy.json
```

### 前端

`AuditDashboard.tsx` 的 "Agent" tab,渲染 `DecisionsTab.tsx`:

- 顶部 stats: total / applied / skipped / errors
- "Run tick now" 按钮 — 触发 manual tick
- 列表: 每条 decision 一行,显示 trigger / 时间相对 / applied×skipped×errors 角标
- 详情:
  - error (如果有)
  - events window 时间范围
  - policy hash 变化(before → after)
  - actions 列表 — 每条带 applied/skipped 图标 + rationale + patch JSON
  - LLM 原始响应 (折叠,40 行高度上限)

四种语言全译 (en/zh-CN/fr/ja)。

---

## 7. 安全边界

人**没法**通过 UI 改 policy.json 字段 — 但通过 autonomy.json 的 `forbidden_actions` 可以**禁止** Agent 触碰特定字段。常见配置:

| 场景 | autonomy.json |
|------|---------------|
| 完全只读探索期 | `forbidden_actions: ["rules_override", "custom_rules_add", "allow_list", "enable_preventive_block"]` |
| 生产 — 不要熔断 | `forbidden_actions: ["enable_preventive_block"]` |
| 沙盒 — 允许任何 | `forbidden_actions: []` |
| 紧急停机 | `enabled: false` |

如果 Agent 做了不该做的:`decisions.ndjson` 里有完整历史,可以从 git 回滚 `policy.json` 到上一个 hash,然后把对应 action 加进 `forbidden_actions`。

**没有自动回滚**。设计上故意如此 — 不让 Agent 既能写又能撤销,避免 oscillation。撤销必须通过人 + git 操作完成。

---

## 8. 与 audit-v1 (audit 分支) 的差别

| 维度 | audit-v1 | audit-v2 |
|------|----------|----------|
| 策略作者 | 人(PolicyForm 818 行表单) | LLM(每 N 分钟自动) |
| 建议机制 | 周期生成 suggestions.json,人 review | 直接应用,decisions.ndjson 是事后报告 |
| chat UI | (设计中) | 不做 — 不需要"对话才改" |
| Policy tab UI | 有 | 没有 — 改用 Agent tab 展示决策日志 |
| FP loop | 人按按钮加 allow_list | LLM 自己看 FP 率自己加 |
| 事件来源 | gateway + MITM | 同 |
| MITM 模块 | 见 mitm/README.md | 完全一致 |

audit-v1 还在 `origin/audit`,适合喜欢手动控制的部署。audit-v2 是新方向。

---

## 9. 启动方式

```bash
# 1. 创建 autonomy.json (默认 disabled)
mkdir -p ~/cicy-ai/autonomy
cat > ~/cicy-ai/autonomy/autonomy.json <<'EOF'
{
  "enabled": true,
  "interval": "10m",
  "llm": {
    "endpoint": "https://api.deepseek.com/v1/chat/completions",
    "model": "deepseek-v4-pro",
    "api_key": "sk-..."
  }
}
EOF

# 2. 启动 cicy-code
cicy-code --audit
# 日志:
# [audit] initialized ...
# [autonomy] starting: interval=10m lookback=24h max/hr=5 max/tick=3 model=deepseek-v4-pro
```

30 秒后 Agent 跑第一次 tick。在 dashboard 的 Agent tab 看效果。

也可以手动触发一次:
```bash
curl -X POST http://127.0.0.1:8008/api/audit/decisions/run -H "Authorization: Bearer $TOKEN"
```

---

## 10. 已知限制

| 风险 | 缓解 |
|------|------|
| LLM 提的 patch 解析失败 | 整 tick 标 error,policy.json 不变。decisions.ndjson 留 raw 响应供 debug。 |
| LLM 提的 patch 过激进 | `max_changes_per_tick` + `forbidden_actions` 双重过滤。 |
| LLM 在短时间内多次提同样建议 | `max_changes_per_hour` 速率上限。 |
| 真出 bug 误删 allow_list 项 | 没有自动回滚 — git 是兜底。建议:把 policy.json 放进 dotfiles git 仓库定期 commit。 |
| 同时多个 cicy-code 进程跑 autonomy | 当前不防 — autonomy 假设单进程。多进程要加文件锁。 |

---

## 11. 已交付(从 v0.1 到现在)

- ✅ **policy.json git 自动 commit**:每个 applied tick 在 `~/cicy-ai/audit/` 做一次 `git add + commit`,SHA 写回 decision 记录。失败仅 WARN,不阻塞 autonomy。代码 `policy_git.go`。
- ✅ **"explain" 端点**:`POST /api/audit/decisions/explain/<id>` — LLM 用 forensic role 解释过去的决策,返回 `{summary, what_changed, why_now, impact, confidence}`。LLM 不可用时退化到决策自带 rationale 拼接的 stub。
- ✅ **revert HTTP 接口**:`POST /api/audit/decisions/revert/<id>` — 通过 `git revert <sha>` 回滚,fsnotify 200ms 内热加载新 policy。同时往 decisions.ndjson append 一条 `trigger=revert` 的新决策,保持时间线完整。
- ✅ **operator CLI**:`cicy-code audit autonomy {run|decisions|explain|revert|show-config}`。

## 12. 路线图(剩余)

- **多 Agent 协同**:不同 host 的 cicy-code 跑各自的 autonomy,周期性互相对账,出现分歧时 escalate 给中心节点。
- **policy.json git 远程 push**:目前 git commit 只是本地。可选 push 到 remote 形成跨 host 的策略版本仓库。
- **decisions.ndjson 滚动归档**:目前 append-only,长期会大。按月归档。
- **dangerous 操作的可选审批**:把"严重操作必须人审"作为 autonomy.json 里可配的策略,而不是 `forbidden_actions` 二选一。
