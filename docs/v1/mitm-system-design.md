# cicy-code MITM 系统设计

> 状态：草案 v0.1
> 作者：cicy-code 团队
> 关联文档：[audit-system-design.md](./audit-system-design.md)

## 1. 背景与目标

### 1.1 现状

cicy-code 已经有一套"协作式"AI 流量审计：

- 后端 `api/mgr/ai_gateway_audit.go` 暴露 `http://127.0.0.1:8008/api/ai-gateway/anthropic/<shortID>`。
- AI 客户端（claude-code、kiro-cli、codex）通过环境变量 `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` 主动把请求送进来。
- 后端解析 plaintext 请求与流式响应，落盘 `~/cicy-ai/workers/<agent>/.cicy/history/current.json` + `reply.json`，并喂入 `api/mgr/audit/pipeline.go` 做策略匹配、熔断、邮件告警。

### 1.2 缺口

下面这一批"非协作"客户端**根本不读** `*_BASE_URL`：

- Cursor / Windsurf / Trae / 类似桌面 IDE 的内置 AI
- ChatGPT / Claude 的官方桌面 App
- 移动端 App
- 把 API URL 硬编码进二进制的 SDK / CLI 工具
- 浏览器里直接调 `api.anthropic.com` 的 Web 应用

它们的流量目前**完全绕过审计**。审计系统看不到它们的 prompt，无法识别敏感数据外泄，也无法熔断。

### 1.3 目标

| 优先级 | 目标 |
|--------|------|
| P0 | 让任何走 mihomo 的 AI 流量都能产出和现有协作客户端**完全相同的** `current.json` / `reply.json` |
| P0 | 复用现有 `audit.Pipeline` —— 不引入第二份审计 |
| P0 | **支持链式 MITM**：多个 MITM 节点串联,每节点独立做 TLS 终止 + 自己的策略 + 自己的审计 |
| P1 | 接入 `audit.PreventiveCheck` 实现请求前/流式中熔断 |
| P1 | 接入 `audit.IncidentResponse` 实现高危事件邮件告警 |
| P2 | 用 LLM 把"操作员手写 policy.json"变成"操作员 review agent 建议" |

### 1.4 非目标

- **不**重写 audit 核心。audit 分支已有的 `pipeline / policy / preventive / incident / verify / chain` 全部沿用。
- **不**改 mihomo 代码。MITM 作为 mihomo 的一个 SOCKS5 上游存在，mihomo 只需要加路由规则。
- **不**支持 HTTP/3 (QUIC)。本期范围只覆盖 HTTP/1.1 over TLS（含强制 ALPN 降级）。
- **不**做 cert pinning 客户端的破解 —— pinning 失败就 fail-open passthrough，记录"mitm_skipped"。

---

## 2. 总体架构

```
┌──────────────────────────────────────────────────────────────────────┐
│  非协作客户端 (Cursor / 桌面 App / 硬编码 SDK / 浏览器)              │
│  系统/Chrome 代理: SOCKS5 127.0.0.1:20001                            │
└──────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼ TCP
┌──────────────────────────────────────────────────────────────────────┐
│  mihomo  (现有 cicy-mihomo,零代码改动)                              │
│  rules:                                                              │
│    - DOMAIN-SUFFIX,api.anthropic.com,cicy-mitm-group                 │
│    - DOMAIN-SUFFIX,api.openai.com,cicy-mitm-group                    │
│    - DOMAIN-SUFFIX,api.deepseek.com,cicy-mitm-group                  │
│    - ...原有规则                                                     │
│  proxy-group cicy-mitm-group → socks5://127.0.0.1:1085               │
└──────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼ SOCKS5
┌──────────────────────────────────────────────────────────────────────┐
│  cicy-mitm  (新模块,api/mgr/mitm/,在 cicy-code 主进程内)            │
│                                                                      │
│  ① 接 SOCKS5 (无 auth) → 拿到 host:port                             │
│  ② host 是否在白名单?                                                │
│     N → passthrough: 透明 TCP 转发,不解密                            │
│     Y → 继续                                                         │
│  ③ TLS Server: 用 CA 动态签发 host 的 leaf cert                      │
│     ALPN 仅协商 http/1.1 (h2/h3 在 v1 范围外)                        │
│  ④ HTTP/1.1 解析请求 (request line + headers + body)                 │
│  ⑤ ┌── PreventiveCheck (audit/preventive.go,已有) ──────────┐        │
│     │  block  → 合成 provider 风格的 429 返回给客户端          │        │
│     │           写 audit event status=blocked,不 dial 上游     │        │
│     │  redact → 用 ModifiedPayload 替换 body 继续              │        │
│     │  none   → 原样继续                                       │        │
│     └────────────────────────────────────────────────────────┘        │
│  ⑥ newAIGatewayAuditSession(provider, agent_id, ...) (已有)          │
│     → 落 current.json                                                │
│  ⑦ TLS Client: dial 真实上游 (可走 mihomo 9001 出口)                 │
│  ⑧ aiGatewayAuditReadCloser 包装上游 response.Body (已有)            │
│     → 流式 SSE 增量解析 → 增量刷 reply.json                          │
│  ⑨ 同步把 response 转回客户端 + 流式中熔断 (max tokens / 关键词)     │
│  ⑩ 完成后通过 audit.Pipeline.Ingest 推事件,触发后续 incident         │
└──────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼ TLS
                          下一跳 (真实上游  或  下游 MITM 节点)
```

### 2.1 链式 MITM 部署示意

每个 cicy-mitm 节点都是"独立审计 + TLS 终止 + 转发给下一跳"的对等模块。3 节点链举例:

```
┌─────────────┐    ┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│  Client     │    │ mitm-A       │    │ mitm-B       │    │ mitm-C       │
│  (Cursor)   │    │ 本机         │    │ 公司网关     │    │ 云端总闸     │
│             │    │ policy: PII  │    │ policy: 合规 │    │ policy: 全部 │
│             │    │ audit: 本地  │    │ audit: 公司  │    │ audit: 中央  │
└─────────────┘    └──────────────┘    └──────────────┘    └──────────────┘
       │                  │                   │                   │
       │ SOCKS5           │ SOCKS5            │ SOCKS5            │ TLS
       │                  │ + 注入            │ + 透传            │ + strip
       │                  │   identity        │   identity        │   identity
       ▼                  ▼ headers           ▼ headers           ▼   headers
                    TLS 终止+解密       TLS 终止+解密       TLS 终止+解密
                          │                   │                   │
                          ▼                   ▼                   ▼
                    自己签的 leaf       自己签的 leaf       自己签的 leaf
                          ▼                   ▼                   ▼
                    audit pipeline      audit pipeline      audit pipeline
                          │                   │                   │
                          └─── 重新 TLS ──────┴─── 重新 TLS ──────┴─── 真实 TLS ──→ api.anthropic.com
                               (信任 mitm-B          (信任 mitm-C        (用系统 CA)
                                的 CA)                的 CA)
```

每跳:
1. **入站**: SOCKS5 server (无 auth,或简单 auth 携带 agent_id)
2. **解密**: TLS server,用本节点的 CA 动态签 leaf cert
3. **解析**: HTTP/1.1,注入或读取 `X-Cicy-Mitm-Trace` header (见 §5.10)
4. **策略**: 本节点的 `PreventiveCheck`,可以独立 block/redact/pass
5. **审计**: 本节点的 `audit.Pipeline.Ingest`,事件落本节点 store
6. **环路检测**: 检查 trace header 里没有自己的 node_id
7. **加密**: TLS client → 下一跳;**信任**下一跳的 CA (或全链共享同一 CA)
8. **最终跳**: 配置 `final_hop: true` 的节点会**剥掉** `X-Cicy-Mitm-*` headers,用系统 CA 信任真实上游

### 2.2 单节点退化

部署只有 1 个 mitm 节点时,链长度 = 1,所有"链式"概念退化为单节点 MITM(`final_hop: true`)。Phase 1 默认配置就是单节点。代码路径**完全一致**,链式相关字段在单节点配置下都是 no-op。

---

## 3. 与现有 audit 体系的关系

### 3.1 复用清单（不要重写）

| 现有文件/函数 | MITM 怎么用 |
|---------------|-------------|
| `ai_gateway_audit.go:281 newAIGatewayAuditSession` | 每个 MITM 截到的 turn 起一个 session |
| `ai_gateway_audit.go:800 aiGatewayWriteCurrentSnapshot` | session 内部自动调用,产 current.json |
| `ai_gateway_audit.go:423 aiGatewayAuditReadCloser` | 包装上游 response.Body,SSE 增量解析 |
| `ai_gateway_audit.go:2808 aiGatewayParseResponse` | 自动按 Content-Type 选流式/非流式 |
| `ai_gateway_audit.go:2828 aiGatewayParseStreamResponse` | 同时支持 OpenAI Responses / OpenAI Chat / Anthropic Messages |
| `ai_gateway_audit.go:822 aiGatewayWriteReplySnapshot` | 流式增量调用,产 reply.json |
| `audit/pipeline.go Pipeline.Ingest` | MITM 完成 turn 后推 event,触发策略匹配 |
| `audit/preventive.go PreventiveCheck` | **= 请求前熔断**,返回 block/redact/none |
| `audit/policy.go Policy.Preventive` | 已有 schema,直接复用 |
| `audit/incident.go` | 高危事件邮件 + AI remediation |

### 3.2 新增清单

| 模块 | 行数估计 | 职责 |
|------|---------|------|
| `api/mgr/mitm/socks5_server.go` | ~150 | SOCKS5 入站(无 auth),拿 host:port |
| `api/mgr/mitm/ca.go` | ~150 | CA 自动生成/加载;leaf cert LRU 签发 |
| `api/mgr/mitm/tls_terminate.go` | ~100 | 用 leaf cert 做 TLS server,强制 ALPN=h1 |
| `api/mgr/mitm/upstream_dial.go` | ~80 | TLS client → 上游;可选走 mihomo 二级代理 |
| `api/mgr/mitm/http_pump.go` | ~250 | HTTP/1.1 req/resp 双向转发 + audit hook |
| `api/mgr/mitm/passthrough.go` | ~50 | host 不在白名单时 raw TCP 透传 |
| `api/mgr/mitm/identity.go` | ~80 | 从 SOCKS5 用户字段/入口端口/客户端 IP 推断 agent_id |
| `api/mgr/mitm/synthetic.go` | ~60 | 合成 provider 风格的 429/403 错误响应 |
| `api/mgr/mitm/config.go` | ~80 | MITM 子配置(白名单/CA 路径/端口) |
| `api/mgr/mitm/main.go` | ~50 | 模块启动入口,在 cicy-code main 注册 |
| 测试 | ~600 | 单测 + 集成测试 |
| **小计** | **~1650** | |

### 3.3 audit.RequestSpan 字段微调

`aiGatewayRequestSpan` 增加四个字段（向后兼容,所有现有客户端不受影响）：

```go
Source        string `json:"source"`         // "gateway" (协作) | "mitm" (非协作)
MitmHost      string `json:"mitm_host"`      // "api.anthropic.com"
ClientIP      string `json:"client_ip"`      // SOCKS5 握手拿到
ClientProfile string `json:"client_profile"` // 按 mihomo 入口端口推断
```

UI 加一个 `source` 过滤器（"All / Gateway / MITM"）和 `MITM` 角标即可。

---

## 4. 分阶段实施

> 设计原则：每阶段独立可验收。前一阶段不通过不进下一阶段。本期文档详写 Phase 1。

| Phase | 目标 | 估时 | 依赖 |
|-------|------|------|------|
| **Phase 1: MITM 单节点落地** | dump 出 OpenAI / Anthropic API 的 plaintext,产 current.json / reply.json | 4 天 | audit 分支已合 |
| Phase 1.5: 链式 MITM | upstream.mode=chain + 双节点本机自测通过 | 2 天 | Phase 1 |
| Phase 2: 审计接入 | MITM 事件喂 audit.Pipeline,UI 看得到 MITM 流量,跨节点联邦查询 | 2 天 | Phase 1.5 |
| Phase 3: 熔断接入 | 接入 PreventiveCheck + 流式中熔断 + 全链 block 协议 | 2 天 | Phase 2 |
| Phase 4: Policy Agent | LLM 出建议,人 review apply | 5 天 | Phase 3 |

**本文档下文详细规格仅覆盖 Phase 1**。后续阶段在另文档展开。

---

## 5. Phase 1 详细规格 — MITM 落地

### 5.1 范围

Phase 1 完成后必须满足：

- [ ] mihomo 把 `api.anthropic.com` / `api.openai.com` 路由到 cicy-mitm。
- [ ] cicy-mitm 监听 `127.0.0.1:1085` (SOCKS5,无 auth)。
- [ ] CA 首次启动自动生成,提供 `install-ca` 命令把 root 装进 OS 信任链。
- [ ] 对白名单 host 做 TLS 终止 + HTTP/1.1 解析。
- [ ] 对每个请求产出和现有 gateway **完全相同 schema** 的 `current.json` / `reply.json`,落盘到 `~/cicy-ai/workers/<agent_id>/.cicy/history/`。
- [ ] 非白名单 host 走 passthrough,不影响普通流量。
- [ ] 任意环节失败(handshake/parse/dial)都 fail-open passthrough,不能阻塞正常流量。

**不包括**：熔断、UI、audit.Pipeline 接入、HTTP/2。这些在后续阶段。

### 5.2 mihomo 配置变更

`~/cicy-ai/db/mihomo.yaml` 增加：

```yaml
proxies:
  - name: cicy_mitm
    type: socks5
    server: 127.0.0.1
    port: 1085

proxy-groups:
  - name: cicy-mitm-group
    type: select
    proxies: [cicy_mitm]

rules:
  # 必须放在 IN-NAME / SRC-IP-CIDR 之后,在 MATCH 之前
  - DOMAIN-SUFFIX,api.anthropic.com,cicy-mitm-group
  - DOMAIN-SUFFIX,api.openai.com,cicy-mitm-group
  - DOMAIN-SUFFIX,api.deepseek.com,cicy-mitm-group
  - DOMAIN-SUFFIX,generativelanguage.googleapis.com,cicy-mitm-group
```

变更通过 `cicy-mihomo reload` 热加载,零停机。

### 5.3 cicy-mitm 模块结构

```
api/mgr/mitm/
├── main.go              # Start(ctx, config) — 在 main 里注册
├── config.go            # Config (port, ca_path, whitelist, audit_dir)
├── socks5_server.go     # SOCKS5 服务端 (RFC 1928, NO_AUTH only)
├── ca.go                # CA 生成/加载 + leaf cert LRU 签发
├── tls_terminate.go     # TLS server,ALPN=["http/1.1"]
├── upstream_dial.go     # TLS client,出口可选 mihomo 二级代理
├── http_pump.go         # HTTP/1.1 双向转发 + audit hook
├── passthrough.go       # 非白名单 raw TCP 透传
├── identity.go          # agent_id 推断
├── synthetic.go         # 合成错误响应 (Phase 3 才用,Phase 1 留空)
└── *_test.go
```

### 5.4 配置 schema

新增 `~/cicy-ai/db/mitm.yaml`（或合并进 cicy-code 的主配置）：

```yaml
mitm:
  enabled: true
  socks5_listen: 127.0.0.1:1085
  ca:
    cert_path: ~/cicy-ai/db/mitm-ca.crt
    key_path:  ~/cicy-ai/db/mitm-ca.key
    leaf_cache_size: 1024
  hosts:
    # 白名单:列表内的 host 做 MITM,其余 passthrough
    whitelist:
      - api.anthropic.com
      - api.openai.com
      - api.deepseek.com
      - generativelanguage.googleapis.com
  node:
    # 本节点身份,用于链式 trace + 环路检测
    id: "mitm-laptop-w10001"
    # 是否最后一跳:最后一跳 strip X-Cicy-Mitm-* headers,用系统 CA 信任真实上游
    final_hop: true
  upstream:
    # 上游出口模式
    #   direct = 本机直连真实 host
    #   mihomo = 走 mihomo SOCKS5(继续翻墙到真实 host)
    #   chain  = 走下一个 mitm 节点
    #
    # 注:生产环境 mihomo 监听 9001 / 外部控制器 19001;
    #    dev 测试环境 mihomo 监听 9002 / 外部控制器 19002,
    #    避免与生产实例冲突,可在同机并行调试。
    mode: mihomo
    mihomo_socks5: 127.0.0.1:9002      # dev: 9002,prod: 9001
    mihomo_auth: "w-10001:MsZTKFsSCWrQC25d"
    # chain 模式专用
    chain:
      next_hop: "mitm-corp.internal:1085"  # 下一跳 SOCKS5 地址
      auth: "agent-w10001:secret123"        # 可选,SOCKS5 user/pass 携带 agent_id
      trust_ca: ~/cicy-ai/db/mitm-corp-ca.crt  # 下一跳的 CA,本节点要信任
      timeout: 30s
  identity:
    # agent_id 推断策略,从上到下优先匹配
    rules:
      - kind: socks5_username    # 客户端在 SOCKS5 username 里带 agent-id
      - kind: port_map           # 按入口端口推断 (mihomo 端口 → agent)
        map:
          20001: w-10001
          20002: w-10002
      - kind: client_ip          # 按 client IP 推断
        map:
          127.0.0.1: w-10001
      - kind: fallback           # 全都没匹配
        value: "mitm:{host}"
  audit:
    history_root: ~/cicy-ai/workers
```

### 5.5 CA 管理

#### 5.5.1 自动生成

首次启动时若 `cert_path` 不存在:

```go
// 生成一个 ECDSA P-256 自签 root CA
// Subject: "CN=cicy-mitm CA <hostname> <date>"
// 有效期: 10 年
// 私钥: PEM 编码,权限 0600
```

#### 5.5.2 leaf 签发

每个目标 host 第一次访问时即时签:

- key 用 root key 派生(同 root 一致的 ECDSA P-256,**不**为每个 leaf 单独生成 key,避免 CPU 开销)
- SAN 含 host + 通配 `*.host`
- 有效期 1 年
- LRU 缓存大小 `leaf_cache_size`,命中复用

#### 5.5.3 install-ca 命令

新增 `cicy-code mitm install-ca` 子命令:

| 平台 | 动作 |
|------|------|
| Linux (system) | `cp mitm-ca.crt /usr/local/share/ca-certificates/` + `update-ca-certificates` (需要 sudo) |
| Linux (Chrome/Firefox NSS) | `certutil -d sql:~/.pki/nssdb -A -t "C,," -n "cicy-mitm" -i mitm-ca.crt` |
| macOS | `security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain mitm-ca.crt` (需要 sudo) |
| Go binary (SSL_CERT_FILE) | 提示用户 `export SSL_CERT_FILE=...` |

不能信任 CA 的客户端(cert pinning)→ 在该 host 上 MITM 必然失败,我们在 5.7 处理。

### 5.6 TLS 终止与请求处理流程

```go
// 伪代码,真实实现参考 net/http.Server.Serve
func handleConn(conn net.Conn, host string, identity Identity) error {
    // 1. 包一层 tls.Server,leaf cert 通过 GetCertificate 动态拿
    tlsConn := tls.Server(conn, &tls.Config{
        GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
            return ca.SignLeaf(hello.ServerName), nil
        },
        NextProtos: []string{"http/1.1"},  // 强制 h1
    })
    if err := tlsConn.Handshake(); err != nil {
        return fmt.Errorf("client handshake failed (possible cert pinning): %w", err)
    }

    // 2. 解析 HTTP/1.1 请求
    reader := bufio.NewReader(tlsConn)
    req, err := http.ReadRequest(reader)
    if err != nil { return err }
    req.URL.Scheme = "https"
    req.URL.Host   = host
    body, _ := io.ReadAll(req.Body)
    req.Body = io.NopCloser(bytes.NewReader(body))

    // 3. provider 识别
    provider := detectProvider(host)  // "anthropic"|"openai"|"deepseek"|"google"|...

    // 4. 起 audit session — 复用现有函数
    session := newAIGatewayAuditSession(
        provider,
        identity.AgentID,
        req.URL,
        req.URL.Path,
        req.Method,
        req.Header,
        body,
    )
    if err := session.writeStartSnapshots(); err != nil {
        log.Warnln("[mitm] write current.json failed: %v", err)
    }

    // 5. 拨上游 — 走 mihomo 9001 出口,继续享受翻墙
    upstream, err := upstreamDial(req.URL.Host)
    if err != nil {
        session.completeFromError(err)
        return err
    }
    defer upstream.Close()

    // 6. 把请求按原样发给上游
    if err := req.Write(upstream); err != nil {
        session.completeFromError(err)
        return err
    }

    // 7. 读上游响应,包 audit reader 做增量解析
    upstreamReader := bufio.NewReader(upstream)
    resp, err := http.ReadResponse(upstreamReader, req)
    if err != nil { return err }
    auditReader := newAIGatewayAuditReadCloser(
        resp.Body, session,
        resp.StatusCode, resp.Header, resp.ContentLength,
    )
    resp.Body = auditReader

    // 8. 把响应原样写回客户端
    return resp.Write(tlsConn)
}
```

**关键不变量**：`req.Body` 在第 2 步被 `ReadAll` 消耗后必须**重新挂回**(伪代码第 12 行),否则第 6 步上游收到的 body 是空的。这是最容易踩的坑。

### 5.7 错误兜底（fail-open）

任何一步失败都不能阻塞客户端流量。决策表：

| 失败环节 | 处理 |
|---------|------|
| CA 生成失败 | mitm 模块整体禁用,启动日志 ERROR,mihomo 路由还会去 1085 但 1085 不存在 → 客户端连接失败。建议启动时校验,失败则不监听 1085,让 mihomo 直接走默认路径 |
| 客户端 TLS handshake 失败(cert pinning) | 检测 `tls.RecordHeaderError` / "unknown CA" / "bad_certificate" alert → 写 audit event source=mitm status=mitm_skipped → 关连接(客户端会自己重试,但 mihomo 不会再选这个 group,需要 fallback;Phase 1 暂时让客户端报错,Phase 2 再做 fallback 规则) |
| 上游 dial 失败 | session.completeFromError,客户端收到 502 |
| 上游 TLS handshake 失败 | 同上 |
| 上游响应非 200 | 正常透传,audit 记录 status_code |
| 解析 SSE 失败 | audit_reader 内部已处理,降级到"原样转发,不增量解析",最终 reply.json 用 final body 一次性解析 |

### 5.8 测试矩阵

#### 5.8.1 单元测试

| 模块 | 测试点 |
|------|--------|
| ca.go | root 生成幂等;leaf SAN 含 host;leaf 缓存命中;不同 host 产不同 leaf |
| socks5_server.go | 无 auth 握手;CONNECT 拿到 host:port;非 CONNECT 拒绝 |
| http_pump.go | body 透传完整(SHA256 一致);header 透传完整;Content-Length 处理;chunked transfer-encoding 处理 |
| identity.go | 各种 rule 顺序匹配;fallback 模板替换 |

#### 5.8.2 集成测试

| 场景 | 验收 |
|------|------|
| 通过 SOCKS5 调 `api.anthropic.com/v1/messages` (curl) | 客户端收到正确响应 + 产出 current.json/reply.json,内容与协作模式一致 |
| 调 `api.openai.com/v1/chat/completions` 流式 SSE | reply.json 增量更新,最终 items[] 含 thinking/text/tool_use |
| 调 `api.deepseek.com` | provider 识别正确,token 字段正确填充 |
| 非白名单 host (如 `github.com`) | passthrough,客户端拿到原始证书(不是 cicy-mitm CA),不写 audit |
| 客户端不信任 CA | TLS handshake 失败,客户端报 cert error,mitm 写一条 source=mitm status=mitm_skipped 的 audit event |
| 上游主动断连 | 客户端收到 502,audit 记 status=error |
| 高并发(100 conn) | 无 race,leaf cache 命中率 > 90%,CPU < 200% (4 核机) |

### 5.10 链式 MITM 规格

#### 5.10.1 身份与轨迹 header

在 TLS 终止后、转发给下一跳前,**注入或更新**以下 HTTP headers:

| Header | 类型 | 由谁写 | 含义 |
|--------|------|--------|------|
| `X-Cicy-Mitm-Trace-Id` | UUID v7 | 第一跳生成,后续跳不改 | 全链 turn 唯一 ID,用于跨节点 audit event 关联 |
| `X-Cicy-Mitm-Trace` | 逗号列表 | 每跳 append 自己的 `node.id` | 例: `mitm-laptop,mitm-corp` |
| `X-Cicy-Mitm-Agent` | string | 第一跳推断后写入,后续跳保留 | 原始 agent_id,用于上游审计归属 |
| `X-Cicy-Mitm-Client-IP` | string | 第一跳写入 | 原始客户端 IP,用于追溯 |
| `X-Cicy-Mitm-Hop-Count` | int | 每跳 +1 | 防御性,超过 `max_hops`(默认 10) → reject |

**最后一跳(`final_hop: true`)的特殊行为**：

- **strip** 上述全部 `X-Cicy-Mitm-*` headers,避免泄露给真实上游 provider。
- 用系统 CA pool 信任上游(不是自签 CA)。

#### 5.10.2 环路检测

每跳处理请求时:

```go
trace := req.Header.Get("X-Cicy-Mitm-Trace")
hops  := strings.Split(trace, ",")
for _, hop := range hops {
    if hop == config.Node.ID {
        return errLoopDetected  // 写 audit event status=loop_rejected
    }
}
if len(hops) >= config.Node.MaxHops {
    return errTooManyHops
}
req.Header.Set("X-Cicy-Mitm-Trace", trace + "," + config.Node.ID)
req.Header.Set("X-Cicy-Mitm-Hop-Count", strconv.Itoa(len(hops)+1))
```

#### 5.10.3 CA 信任模型

两种模式,运维选一种:

**模式 A:全链共享 CA**(简单)

- 部署一次性把同一份 root CA + key 拷贝到每个节点。
- 所有节点签的 leaf 都用这份 root,所以默认互信。
- 缺点:任何节点泄露 key 等于全链失守;不适合跨组织部署。

**模式 B:每节点独立 CA + 显式信任列表**(推荐)

- 每个节点 `cicy-code mitm install-ca` 时生成自己的 root。
- 节点 A 的配置 `upstream.chain.trust_ca` 指向节点 B 的 root.crt。
- A 拨 B 时用这个 trust_ca 校验,而不是系统 CA。
- 优点:每节点 key 独立;某节点 compromise 可单独换 CA 而不影响其他节点。

代码上 `upstream_dial.go` 的 TLS client 配置:

```go
caPool := x509.NewCertPool()
if config.Upstream.Mode == "chain" {
    pem, _ := os.ReadFile(config.Upstream.Chain.TrustCA)
    caPool.AppendCertsFromPEM(pem)
} else {
    caPool, _ = x509.SystemCertPool()
}
tls.Config{RootCAs: caPool, ServerName: targetHost}
```

#### 5.10.4 链式 audit 事件关联

每跳的 audit event 都带 `trace_id`(来自第一跳生成的 `X-Cicy-Mitm-Trace-Id`)。

`audit_handlers.go` 提供新接口:

```
GET /api/audit/events?trace_id=<uuid>
```

返回该 trace_id 在所有可访问节点上的 events,按时间排序。这样可以在 dashboard 上展开一次请求的"全链审计视图"——看到 mitm-A 命中了 PII redact、mitm-B 命中了模型白名单、mitm-C 顺利转发的完整链条。

跨节点查询的实现(Phase 2 详化):

- 每个节点暴露 `/api/audit/events?trace_id=...` 只读接口。
- 第一跳节点(通常是用户最熟的本机 dashboard)在响应中**联邦**调用下游节点的同名接口,合并展示。
- 联邦调用用 HMAC 签名鉴权(复用 `audit/ack.go` 的 HMAC 套件)。

#### 5.10.5 链式策略组合

每节点的 `policy.json` 独立。但有两条**全链不变量**必须确保:

1. **最严策略胜出**: 任何一跳 `PreventiveCheck` 返回 `block` → 终止全链,不再向下传。返回给客户端的合成错误带 `X-Cicy-Mitm-Blocked-By: <node.id>` header,便于追溯。
2. **redact 一旦发生不可逆**: mitm-A 把 `sk-xxx` redact 成 `[REDACTED]` 后,mitm-B 即使策略允许也看不到原文。这是设计目的(纵深防御),不是 bug。

#### 5.10.6 链式部署的运维要点

- **节点发现**: 没有自动发现,`upstream.chain.next_hop` 是硬配。这是有意的——审计基础设施不应该有动态拓扑,变更必须走 ops 流程。
- **健康检查**: 每节点暴露 `GET /healthz` 返回 200。父节点周期探活,失败时 audit log WARN 但不影响转发(SOCKS5 connect 会失败,客户端会收到 502)。
- **版本兼容**: trace header 加新字段时,中间节点必须**透传**未识别的 `X-Cicy-Mitm-*` header,不能 drop。

### 5.11 验收 checklist

实施完成时,以下每一条必须可演示：

**单节点(默认部署):**

```
[ ] 配 Chrome SOCKS = 127.0.0.1:20001,打开 console.anthropic.com 能正常登录(passthrough 工作)
[ ] curl --proxy socks5://127.0.0.1:20001 https://api.anthropic.com/v1/messages \
     -H "x-api-key: $KEY" -H "anthropic-version: 2023-06-01" \
     -d '{"model":"claude-3-5-sonnet","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}'
    返回 200 且生成 current.json + reply.json
[ ] reply.json 的 items[] 含 text 块,token 字段非零
[ ] 同一进程 30 分钟跑 100 次请求,内存稳定不泄漏
[ ] kill cicy-code → mihomo 走默认路径,客户端不挂(可能报错但不影响其他流量)
[ ] cicy-code mitm install-ca && 重启 Chrome → 访问 api.anthropic.com 无红锁
```

**双节点链式(本机起两个实例):**

```
[ ] 本机起两个 cicy-mitm 实例:
      mitm-A: 1085, node.id=A, upstream.mode=chain, chain.next_hop=127.0.0.1:1086, final_hop=false
      mitm-B: 1086, node.id=B, upstream.mode=mihomo,                                final_hop=true
    互相信任对方 CA。
[ ] curl 通过 mitm-A,请求到 api.anthropic.com 成功返回。
[ ] mitm-A 的 current.json 含 X-Cicy-Mitm-Trace=A;
    mitm-B 的 current.json 含 X-Cicy-Mitm-Trace=A,B。
[ ] 两个节点的 audit DB 都有同一 trace_id 的 event,可通过 /api/audit/events?trace_id=... 联邦查询。
[ ] 构造环路 (mitm-A.next_hop = mitm-A 自己) → 客户端收到 502,audit 记 status=loop_rejected。
[ ] 链长度 > max_hops=10 时 → too_many_hops error。
[ ] mitm-A 的 policy.preventive 设 "redact sk-xxx" → mitm-B 看到的 current.json 已是 [REDACTED]。
[ ] mitm-A block → mitm-B 看不到 event,客户端收到 mitm-A 的合成 429,
    response header 含 X-Cicy-Mitm-Blocked-By: A。
```

---

## 6. 后续阶段简介

### Phase 2: 审计接入（1 天）

MITM 完成 turn 后,把 `requestSpan` 喂 `audit.Pipeline.Ingest`。AuditDashboard 加 `source` 过滤 + `MITM` 角标。

### Phase 3: 熔断接入（2 天）

- 5.6 流程第 4 步前插一次 `audit.PreventiveCheck(payload)`:
  - `block` → 调 `synthetic.NewBlockResponse(provider, reason)` 写回客户端,不 dial 上游,audit 记 `status=blocked`。
  - `redact` → 用 `ModifiedPayload` 替换 body 再继续。
  - `none` → 继续。
- 流式中熔断: 在 `http_pump.go` 的响应转发循环里检查累计 output_tokens 与 stream_regex,触发时 abort 上游 + 给客户端注入 `data: [DONE]`。

### Phase 4: Policy Agent（5 天）

详见 [policy-agent-design.md](./policy-agent-design.md)（待写）。核心:

- `audit/policy_suggester.go` — 复用 `ai_remediation.go` 的 LLM 调用框架,读最近 N 天 events + 当前 policy,产出 PolicyPatch 写到 `~/cicy-ai/audit/policy.suggestions.json`。
- `audit_handlers.go` 加 suggestions list / apply / dismiss 接口。
- `AuditDashboard.tsx` 加 "Suggestions" tab,每条建议附支撑事件 + diff 视图。
- 建议分级 safe / moderate / dangerous,后两类必须人显式确认。

---

## 7. 风险与限制

| 风险 | 影响 | 缓解 |
|------|------|------|
| Cert pinning 客户端 | 该客户端在该 host 上无法 MITM | fail-open passthrough + 在 audit 标记;Phase 2 加 fallback 规则把 pinned host 排出白名单 |
| HTTP/2 客户端 | 强制 ALPN h1 可能让某些客户端走 fallback 但损失性能,极少数客户端可能拒绝 h1 | 监控 audit 失败率,如某 host 失败 > 50% 则手工把它移出白名单;Phase 5 再加 h2 支持 |
| HTTP/3 客户端 (QUIC) | mihomo 拦不住 UDP 出 443 | mihomo 加 reject UDP/443 规则,强制客户端 fallback 到 TCP;承担少量 OpenAI/Anthropic 客户端连不上的代价 |
| CA 私钥泄露 | 攻击者可签发任意 host 证书 | `mitm-ca.key` 文件权限 0600;不进 git;考虑硬件 KMS(后续) |
| 性能瓶颈 | TLS 握手 + HTTP 解析 + 双向 IO,CPU 占用 | leaf cert LRU 缓存;passthrough 路径零开销;基准 100 并发应 < 200% CPU |
| 单点失败 | cicy-code 挂了影响所有 AI 流量 | mihomo 健康检查 fallback;cicy-code 用 systemd 自动重启 |
| 法律/合规 | 解密员工 AI 流量需要合规审查 | 部署文档明确指出"仅用于本机/受控环境,不得对未授权用户开启";`hosts.whitelist` 显式列举 |
| 链路证书链失配 | 某节点的 CA 没装到下游节点的 trust_ca → TLS 失败,整条链断 | 部署 checklist 强制要求"安装下一跳 CA";`/healthz` 返回里上报 TLS 校验失败计数 |
| 链中某节点劫持/篡改 | 任一节点都有解密能力,内鬼风险倍增 | 每节点 audit chain hash 不可篡改(已有 `audit/chain.go`);跨节点 event 通过 trace_id 比对 request hash 不一致即告警 |
| 链路时延累加 | 每跳多一次 TLS 握手 → 增加首字节延迟 | 长连接复用(`Connection: keep-alive`),Phase 1 同节点保持上游连接池;实测每跳 +20ms 可接受 |
| 链路扩散 LLM cost | 链式部署里上游节点也会触发自己的 ai_remediation LLM 调用 → 重复消耗 | 只在 final_hop = true 节点开 ai_remediation;中间节点配置默认关闭 |

---

## 8. 决策记录

### 8.1 为什么 MITM 模块放 cicy-code 而不是 mihomo

- mihomo 是流量基础设施,不应该沾业务(provider 协议解析、agent_id 推断)。
- mihomo 经常合 upstream,fork 里加 MITM 会让每次 rebase 都很痛苦。
- cicy-code 已有 audit pipeline / provider 解析器 / LLM 集成,MITM 复用这些零成本。

### 8.2 为什么用 SOCKS5 而不是 HTTP CONNECT

- mihomo 内部已经在用 SOCKS5(`proxy_local`, `vpn_us` 都是 socks5 上游),协议成熟。
- SOCKS5 握手能拿到 `domain:port`,**不依赖** SNI 嗅探,更可靠。
- HTTP CONNECT 同样可行,但 SOCKS5 username 字段还能顺便携带 `agent_id`。

### 8.3 为什么只支持 HTTP/1.1

- HTTP/2 实现成本(ALPN + h2 frame parsing + stream multiplexing)显著大于 h1。
- 实测 Anthropic / OpenAI 服务端都支持 h1,客户端在 ALPN 协商失败时也会 fallback。
- HTTP/3 (QUIC) 是 UDP,mihomo 当前只代理 TCP,自然过滤掉。

### 8.4 链式 MITM 为什么选 HTTP header 注入而不是 SOCKS5 username

考虑过用 SOCKS5 username 字段(RFC 1929)携带 trace 信息:

- 优点: 在 TLS 之前就传完,中间路径不需要解密就能看到。
- 缺点: SOCKS5 username 单字段最长 255 字节,装不下完整 trace + agent + uuid;且很多 SOCKS5 client 实现把它当作 auth 字段会做格式校验。

最终选 HTTP header 注入(TLS 终止后):

- header 数量/长度无硬性限制。
- 链中每跳都已经做 TLS 终止 → 有机会读写 header,零额外开销。
- 跟现有 cicy-code 协作模式一致(`X-Codex-Turn-Metadata` / `X-Client-Request-Id` 都是 header)。
- 最后一跳 strip 一次即可,不会泄露给真实上游。

唯一缺点: SOCKS5 username 仍然用于"第一跳"的 agent_id 推断(因为客户端到第一跳之前没有 HTTP 层),这一段保留 username 携带 agent_id 的能力(见 §5.4 `identity.rules` 的 `socks5_username` 种类)。

### 8.5 为什么 policy-agent 不让 LLM 直接改 policy.json

- policy 涉及"是否阻断流量"这种高影响动作,LLM 错误代价大。
- "agent 出建议 + 人 review" 是工业级 AI 落地的标准模式(类比 Copilot / Cursor 的 diff review)。
- 人在环上是合规底线,任何线下审计都需要"谁批准了这次变更"的记录。

---

## 9. 参考

- 现有审计设计: [audit-system-design.md](./audit-system-design.md)
- 策略编写文档: [../audit/policy-authoring.md](../audit/policy-authoring.md)
- mihomo 配置文档: `~/cicy-ai/db/mihomo.yaml` + cicy-mihomo README
- 现有 AI 解析器: `api/mgr/ai_gateway_audit.go`
