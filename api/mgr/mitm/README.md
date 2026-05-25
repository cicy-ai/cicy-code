# cicy-mitm

cicy-code 的中间人代理模块。捕获**非协作客户端**（不读 `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` 的二进制、桌面应用、移动端、浏览器原生应用）的 HTTPS 流量，把 plaintext 喂进现有的 `audit.Pipeline`,产出和协作模式完全相同的 `current.json` / `reply.json`。

完整设计：[../../../docs/v1/mitm-system-design.md](../../../docs/v1/mitm-system-design.md)

---

## 它解决什么问题

cicy-code AI Gateway 通过 `ANTHROPIC_BASE_URL=http://127.0.0.1:8008/api/ai-gateway/...` 让 claude-code / kiro-cli / codex 主动把请求送进审计管道。**但下面这一类客户端不读环境变量,完全绕开审计**：

- Cursor / Windsurf / Trae 等桌面 IDE 的内置 AI
- ChatGPT / Claude 桌面 App
- 移动端 App
- 把 API URL 硬编码进二进制的 SDK
- 浏览器里直接 `fetch('https://api.anthropic.com/...')` 的 Web 应用

cicy-mitm 通过动态签发 leaf cert + TLS 终止把它们拉回审计管道。

---

## 架构

```
Client (Cursor / 桌面 App / 浏览器)
  │ SOCKS5 127.0.0.1:1085
  ▼
cicy-mitm                                        ← 本模块
  │ ① SOCKS5 握手 (RFC 1928 + 1929)
  │ ② host 在 whitelist?
  │       N → raw TCP passthrough
  │       Y → 继续
  │ ③ TLS 终止 (动态 leaf,ALPN=http/1.1)
  │ ④ HTTP/1.1 解析 → 注入 trace headers
  │ ⑤ audit.StartTurn → 写 current.json
  │ ⑥ audit.PreventiveCheck → pass | redact | block
  │ ⑦ 拨上游 (direct | mihomo | chain)
  │ ⑧ 增量 SSE 解析 → 刷 reply.json
  ▼
真实上游 (api.anthropic.com / ...)
```

链式部署时把 ⑦ 的上游指向**另一个** cicy-mitm 节点的 SOCKS5 端口,每跳都独立做 TLS 终止 + 自己的策略 + 自己的审计。

---

## 快速开始（单节点）

### 1. 写配置

`~/cicy-ai/mitm/config.json`:

```json
{
  "enabled": true,
  "socks5_listen": "127.0.0.1:1085",
  "hosts": {
    "whitelist": [
      "api.anthropic.com",
      "api.openai.com",
      "api.deepseek.com"
    ]
  },
  "node": { "id": "mitm-laptop", "final_hop": true },
  "upstream": {
    "mode": "mihomo",
    "mihomo_socks5": "127.0.0.1:9001"
  }
}
```

未列出的字段全部走默认值（CA 路径 `~/cicy-ai/db/mitm-ca.crt`、leaf 缓存 1024、max hops 10…）。

### 2. 启动 cicy-code

```sh
cicy-code            # 主进程,会自动启 MITM
```

首次启动会在 `~/cicy-ai/db/` 自动生成 `mitm-ca.crt` + `mitm-ca.key`。

### 3. 把 CA 装进信任链

```sh
cicy-code mitm install-ca           # Linux: system + Chrome NSS
cicy-code mitm install-ca --scope=nss   # 只装 Chrome,不需要 sudo
cicy-code mitm install-ca --dry-run     # 看会执行什么
```

不能在系统级装信任的话(没有 sudo),`--scope=nss` 至少让 Chrome / Firefox 工作。命令行工具用 `SSL_CERT_FILE=~/cicy-ai/db/mitm-ca.crt` 或 `--cacert` 绕过。

### 4. 用客户端

把 Chrome / Cursor / 任意客户端的 SOCKS5 代理配置成 `127.0.0.1:1085`。

验证：

```sh
curl --proxy socks5h://127.0.0.1:1085 \
     --cacert ~/cicy-ai/db/mitm-ca.crt \
     https://api.anthropic.com/v1/messages \
     -H "x-api-key: $ANTHROPIC_API_KEY" \
     -H "anthropic-version: 2023-06-01" \
     -H "content-type: application/json" \
     -d '{"model":"claude-3-5-sonnet-20241022","max_tokens":50,
          "messages":[{"role":"user","content":"hi"}]}'
```

随后查看 `~/cicy-ai/workers/<agent>/.cicy/history/current.json` 和 `reply.json`——和协作模式产出**字段完全一致**,UI dashboard 直接能看到（带 `source: mitm` 标记）。

---

## 链式部署

两个或更多 cicy-mitm 节点串联,每跳独立审计 + 独立策略。典型场景：本机做 PII redact → 公司网关做合规审计 → 云端总闸做 cost 控制。

### 节点 A (中间跳)

`~/cicy-ai/mitm/config.json`:

```json
{
  "enabled": true,
  "socks5_listen": "127.0.0.1:1085",
  "hosts": { "whitelist": ["api.anthropic.com", "api.openai.com"] },
  "node": { "id": "A", "final_hop": false },
  "upstream": {
    "mode": "chain",
    "chain": {
      "next_hop": "127.0.0.1:1086",
      "trust_ca": "/path/to/B-ca.crt"
    }
  }
}
```

### 节点 B (最终跳)

```json
{
  "enabled": true,
  "socks5_listen": "127.0.0.1:1086",
  "hosts": { "whitelist": ["api.anthropic.com", "api.openai.com"] },
  "node": { "id": "B", "final_hop": true },
  "upstream": {
    "mode": "mihomo",
    "mihomo_socks5": "127.0.0.1:9001"
  }
}
```

### 跨节点 audit 关联

每跳生成的 `current.json` / `reply.json` 都带同一个 `X-Cicy-Mitm-Trace-Id`。dashboard 通过 `/api/audit/events?trace_id=<uuid>` 把整条链的事件拉到一起查看。

不变量(都是 in-process + 真实链路两套测试覆盖的)：

- 任一跳 `PreventiveCheck` 返回 **block** → 终止全链,客户端收到合成 429,response header 含 `X-Cicy-Mitm-Blocked-By: <node.id>`。
- **redact 不可逆**：A 把 `sk-xxx` 改成 `[REDACTED]` 后,B 即使策略放过也看不到原文。这是设计目的(纵深防御)。
- **最终跳 strip**：`final_hop: true` 的节点把所有 `X-Cicy-Mitm-*` headers 删掉再发给真实上游,避免泄露给 API provider。
- **环路检测**：每跳进来时检查 trace 里没有自己的 node.id,有就返回 502 `loop_rejected`。

---

## 配置 reference

| 字段 | 默认值 | 说明 |
|------|--------|------|
| `enabled` | `false` | 总开关。`false` = 模块完全不启动。 |
| `socks5_listen` | `127.0.0.1:1085` | 入站 SOCKS5 监听地址。 |
| `ca.cert_path` | `~/cicy-ai/db/mitm-ca.crt` | CA 证书路径,首次启动自动生成。 |
| `ca.key_path` | `~/cicy-ai/db/mitm-ca.key` | CA 私钥,权限 0600。 |
| `ca.leaf_cache_size` | `1024` | leaf cert LRU 缓存大小。 |
| `ca.leaf_valid_years` | `1` | 签发 leaf 的有效期。 |
| `hosts.whitelist` | 见下 | 列表内 host 做 MITM,其余 passthrough。 |
| `node.id` | `mitm-<hostname>` | 链式 trace 用,默认按机器名拼。 |
| `node.final_hop` | `false` | 最终跳:strip 所有 `X-Cicy-Mitm-*` headers,信任系统 CA。 |
| `node.max_hops` | `10` | 防御性环路保护,链长超过这个值直接 502。 |
| `upstream.mode` | `direct` | `direct`/`mihomo`/`chain` 三选一。 |
| `upstream.mihomo_socks5` | — | `mode=mihomo` 时必填,指向 mihomo SOCKS5 端口。 |
| `upstream.mihomo_auth` | — | `user:pass`,可选(127.0.0.1 默认在 mihomo 的 skip-auth-prefixes 里)。 |
| `upstream.chain.next_hop` | — | `mode=chain` 时必填,`host:port` 指向下一跳。 |
| `upstream.chain.trust_ca` | — | `mode=chain` 时必填,下一跳的 CA 证书路径。 |
| `identity.rules` | 见下 | 推断 `agent_id` 的规则链,顺序匹配第一个命中。 |
| `audit.history_root` | `~/cicy-ai/workers` | `current.json` / `reply.json` 落盘根目录。 |

`hosts.whitelist` 默认：

```
api.anthropic.com
api.openai.com
api.deepseek.com
generativelanguage.googleapis.com
```

`identity.rules` 默认：

```json
[
  { "kind": "socks5_username" },
  { "kind": "fallback", "value": "mitm:{host}" }
]
```

支持的 `kind`：

- `socks5_username` — 从 SOCKS5 RFC 1929 username 字段取
- `port_map` — 按入站端口查表 `map[port]agent_id`
- `client_ip` — 按客户端 IP 查表 `map[ip]agent_id`
- `fallback` — 占位字符串,支持 `{host}` 模板

**注:** dev 环境 mihomo 监听 `127.0.0.1:9002`,prod 是 `9001`。例子里写 9001 是生产端口。

---

## CLI

```sh
cicy-code mitm install-ca                # 安装 CA 到 OS + Chrome NSS DB
cicy-code mitm install-ca --scope=system # 只装 OS 信任链 (需要 sudo)
cicy-code mitm install-ca --scope=nss    # 只装 Chrome NSS (无需 sudo)
cicy-code mitm install-ca --dry-run      # 看会执行什么命令,不实际跑
cicy-code mitm uninstall-ca              # 反向操作
cicy-code mitm show-ca                   # 打印 CA 路径 + 状态
cicy-code mitm help                      # 完整帮助
```

平台支持：

- Linux: `update-ca-certificates` (system) + `certutil` (NSS DB for Chrome/Firefox)
- macOS: `security add-trusted-cert` (System.keychain)
- 其他: 用 `--scope=nss` 或手动 `SSL_CERT_FILE=...`

---

## audit 接入

MITM 截到的 turn 通过 `mitm.AuditHook` 接口喂给 `api/mgr/mitm_audit_adapter.go`,后者调用 `newAIGatewayAuditSession` 复用现有的:

- provider 解析 (anthropic / openai / deepseek 自动识别 host)
- 流式 SSE 增量解析 (`aiGatewayParseStreamResponse`)
- thinking / text / tool_use 归一化 (`reply.items[]`)
- token / cost 提取 (`aiGatewayMergeUsage`)
- `current.json` / `reply.json` 原子落盘

唯一差别:`current.json` 多一个字段 `"source": "mitm"`,审计事件通过 `audit.SubmitMitmEvent` 而不是 `SubmitGatewayOutbound`,UI 用这个字段做过滤和角标。

---

## 熔断

预防性策略(`audit.PreventiveCheck`)在 `StartTurn` 之后、拨上游之前调用。三种决策:

- **pass** — 原样转发。
- **redact** — 用 `ModifiedPayload` 替换 request body 再转发(同步写预脱敏归档供事后审查)。
- **block** — 不拨上游,返回 provider 风格 429 给客户端:
  - Anthropic: `{"type":"error","error":{"type":"rate_limit_error","message":"..."}}`
  - OpenAI: `{"error":{"type":"rate_limit_exceeded","code":"cicy_mitm_blocked","message":"..."}}`
  - Unknown: `{"error":"rate_limit_exceeded","code":"cicy_mitm_blocked","message":"..."}`

response 带 `X-Cicy-Mitm-Blocked-By: <node.id>` + `X-Cicy-Mitm-Block-Rule: <rule_id>` 用于追溯。被拦截的请求**仍然写 audit**(status=blocked),UI 能看到"被熔断的尝试"。

策略本身在 `~/cicy-ai/audit/policy.json` 的 `preventive` 字段里配置(默认 `enabled: false`)。

---

## 故障排查

### 客户端 TLS 失败 / red lock icon

CA 没装进信任链。跑 `cicy-code mitm show-ca` 看路径,再 `install-ca`。

某些客户端(移动 App、企业级桌面应用、有些 Go binary)做了 **cert pinning** — 它们硬编码上游 CA 的公钥指纹,无视 OS 信任链。这种 host 上 MITM 必然失败,只能加到 whitelist **之外**走 passthrough,或承认这部分流量审计不到。

MITM 日志里会标 `tls terminate ... pinning=true`。

### 客户端走了 HTTP/2 失败

cicy-mitm 强制 ALPN = `http/1.1`。大多数客户端在 h2 协商失败时会 fallback 到 h1。极个别(主要是某些 grpc-go 客户端)会拒绝 h1,这种 host 也要从 whitelist 移出。

### IP 不对(应该是 US 出口结果显示是本机)

`upstream.mode: direct` 让 cicy-mitm 直接拨 host,跳过了 mihomo 的代理隧道。改成:

```json
"upstream": {
  "mode": "mihomo",
  "mihomo_socks5": "127.0.0.1:9001"
}
```

### 链式部署 TLS 握手失败

下一跳的 `trust_ca` 配错了。**每个**节点用自己的 CA 签 leaf,父节点必须显式把子节点的 CA 装到 `chain.trust_ca`(不是系统 CA 池)。验证:

```sh
openssl x509 -in /path/to/B-ca.crt -text -noout | head -5
```

### MITM 进程挂了客户端就废了

mihomo 这边的规则把 `cicy-mitm-group` 当作普通 proxy 上游。MITM 挂了 → 那个 SOCKS5 端口拒接 → mihomo 那条路由失败 → 客户端报错。生产部署用 systemd 自动重启 cicy-code,并在 mihomo 那边考虑配 fallback proxy-group。

---

## 测试

包内单测 + 集成测试:

```sh
go test ./api/mgr/mitm/        # CA, identity, SOCKS5, chain, breaker (17 个)
go test ./api/mgr/audit/       # 含 policy suggester 端到端 (4 个新增)
```

手动 smoke test:

```sh
# 单节点
go build -o /tmp/mitm-smoke ./api/mgr/mitm/cmd/mitm-smoke
/tmp/mitm-smoke --config /tmp/mitm-smoke.json --history-root /tmp/smoke-out

curl --proxy socks5h://127.0.0.1:1085 \
     --cacert /tmp/mitm-smoke-ca.crt \
     https://api.myip.com
# → 应返回 {"ip":"...","country":"...","cc":"..."}
# → /tmp/smoke-out/<agent>/<turn>/{current,reply}.json 出现
```

```sh
# 链式 A → B(→ mihomo → US exit)
/tmp/mitm-smoke --config /tmp/mitm-B.json --history-root /tmp/mitm-B &
/tmp/mitm-smoke --config /tmp/mitm-A.json --history-root /tmp/mitm-A &

curl --proxy socks5h://127.0.0.1:1085 \
     --cacert /tmp/mitm-A-ca.crt \
     https://api.myip.com
# → 返回 US IP,A 和 B 的 reply.json 都有解密后的 plaintext,
#    且共享同一 X-Cicy-Mitm-Trace-Id
```

`api/mgr/mitm/cmd/mitm-smoke/main.go` 是独立 driver,用简化的 file-writing audit hook,
不需要完整的 cicy-code 启动栈。用来验证 Phase 1 + 1.5 + 协议解析的隔离场景。
