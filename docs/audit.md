# Audit 模块 — mitmproxy 流量审计系统

## 概述

Audit 模块为 cicy-code-api 提供 HTTPS 流量审计能力。通过 mitmproxy 实现：

- **域名分流**：名单内域名解密监控，其余全部透传（零性能损耗）
- **内容审计**：对解密流量进行关键词/规则匹配，命中则拦截请求
- **动态 Addon**：通过 API 动态注册、更新、删除 Python addon 脚本
- **规则热更新**：修改审计规则无需重启 mitmproxy（5 秒自动重载）

## 架构

```
┌─────────────┐     ┌──────────────────────────────────────┐
│  kiro-cli   │────▶│  mitmproxy (mitmdump :8003)          │
│  或其他客户端 │     │                                      │
└─────────────┘     │  ┌─ smart_proxy.monitor.py (分流+审计)│
                    │  ├─ kiro_traffic.monitor.py (流量记录) │
                    │  └─ custom.monitor.py (用户自定义)     │
                    │                                      │
                    │  rules.json ← API 动态更新            │
                    └──────────────────────────────────────┘
                              │
                    ┌─────────▼──────────┐
                    │  cicy-code-api     │
                    │  /api/audit/*      │
                    └────────────────────┘
```

## 安装方式

### 方式 1：pip 安装（需要 Python 环境）

```bash
pip3 install mitmproxy
```

优点：addon 可以 `import` 任意 Python 包（redis 等）
缺点：依赖系统 Python，版本可能冲突

### 方式 2：官方 standalone binary（推荐）

从 [mitmproxy.org](https://mitmproxy.org/) 下载预编译二进制：

```bash
# Linux x86_64
curl -L https://downloads.mitmproxy.org/11.1.3/mitmproxy-11.1.3-linux-x86_64.tar.gz | tar xz -C /usr/local/bin/

# 验证
mitmdump --version
```

优点：
- 不依赖系统 Python，自带完整 Python 运行时
- 体积约 30MB，开箱即用
- addon 里的 `import` 仍然可用（使用内置 Python）

缺点：
- addon 如果需要额外 Python 包（如 redis），需要用 `--set scripts_packages=redis` 或在 addon 里用 urllib 替代
- 内置 Python 版本固定，不能随意升级

### 方式 3：uvx（推荐用于需要额外包的场景）

```bash
# 安装 uv
curl -LsSf https://astral.sh/uv/install.sh | sh

# 运行 mitmproxy（自动管理虚拟环境）
uvx --with redis mitmproxy
```

## 启动

```bash
# 带 --audit 参数启动 API
cicy-code-api --saas --public --audit
```

启动时自动执行：
1. 检查 `mitmdump` 是否在 PATH 中
2. 未找到则尝试 `pip3 install mitmproxy`
3. 创建 `~/.cicy/monitor/` 目录
4. 如果 `*.monitor.py` 不存在，从二进制内嵌资源写出默认 addon
5. 安装 Python 依赖（redis）
6. 启动 `mitmdump` 进程，加载所有 `*.monitor.py`

## 内嵌 Addon

Monitor 脚本源码位于项目根目录 `mitmproxy/`，构建时由 `build.sh` 复制到 `api/mgr/monitor/`，通过 `go:embed monitor` 打包进二进制：

| 文件 | 用途 |
|------|------|
| `smart_proxy.monitor.py` | 域名分流 + 审计拦截（核心） |
| `kiro_traffic.monitor.py` | Kiro AI 流量记录（推 Redis + DB） |

### 为什么不用 .pyc？

mitmproxy 的 `-s` 参数通过 `importlib` 加载脚本，要求 `.py` 源文件。
`.pyc` 是 CPython 字节码缓存，有以下问题：
- **版本绑定**：Python 3.11 的 pyc 在 3.12 上无法运行（magic number 不同）
- **mitmproxy 不支持**：`-s` 只接受 `.py` 文件
- **无意义的混淆**：addon 代码不含敏感信息，无需隐藏

因此内嵌的 addon 始终以 `.py` 源码形式写出。

## Monitor 目录结构

```
~/.cicy/monitor/
├── smart_proxy.monitor.py    # 域名分流 + 审计（内嵌默认）
├── kiro_traffic.monitor.py   # Kiro 流量记录（内嵌默认）
├── custom.monitor.py         # 用户通过 API 添加的自定义 addon
└── rules.json                # 审计规则（API 动态更新）
```

### 命名规范

所有 addon 文件必须以 `.monitor.py` 结尾。启动时 mitmproxy 会加载目录下所有 `*.monitor.py` 文件。

### rules.json 格式

```json
{
  "monitor_domains": [
    "codewhisperer.us-east-1.amazonaws.com",
    "q.us-east-1.amazonaws.com"
  ],
  "blocked_patterns": [
    "secret_key",
    "password="
  ]
}
```

- `monitor_domains`：需要解密监控的域名列表，不在列表中的域名全部透传
- `blocked_patterns`：请求内容中命中任一关键词则返回 403 拦截

规则文件每 5 秒自动重载，修改后无需重启 mitmproxy。

## API 接口

所有接口需要 Bearer Token 认证。

### 状态查询

```
GET /api/audit/status
```

响应：
```json
{
  "success": true,
  "data": {
    "running": true,
    "pid": 12345,
    "port": "8003",
    "addons": ["smart_proxy.monitor.py", "kiro_traffic.monitor.py"],
    "dir": "/home/user/.cicy/monitor"
  }
}
```

### 启停控制

```
POST /api/audit/start      # 启动 mitmproxy
POST /api/audit/stop       # 停止 mitmproxy
POST /api/audit/restart    # 重启（重新加载所有 addon）
```

### Addon 管理

```
GET /api/audit/addons                    # 列出所有 addon（含源码）
POST /api/audit/addons                   # 创建/更新 addon
DELETE /api/audit/addons?name=xxx        # 删除 addon
```

创建 addon 示例：
```bash
curl -X POST http://localhost:8008/api/audit/addons \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "log_headers",
    "code": "from mitmproxy import http, ctx\n\ndef request(flow: http.HTTPFlow):\n    ctx.log.info(str(dict(flow.request.headers)))\n"
  }'
```

注意：
- `name` 不需要带 `.monitor.py` 后缀，会自动补全
- 新增/删除 addon 后需要调用 `/api/audit/restart` 才能生效
- 更新已有 addon 的代码后也需要 restart

### 规则管理

```
GET /api/audit/rules       # 获取当前规则
POST /api/audit/rules      # 更新规则（立即生效，无需重启）
```

更新规则示例：
```bash
curl -X POST http://localhost:8008/api/audit/rules \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "monitor_domains": [
      "codewhisperer.us-east-1.amazonaws.com",
      "q.us-east-1.amazonaws.com",
      "api.example.com"
    ],
    "blocked_patterns": [
      "BEGIN RSA PRIVATE KEY",
      "AKIA",
      "password="
    ]
  }'
```

## 流量分流原理

```
客户端请求
    │
    ▼
mitmproxy 收到 TLS ClientHello
    │
    ├─ SNI 在 monitor_domains 中？
    │   ├─ YES → 解密（MITM）→ 审计检查 → 放行或拦截
    │   └─ NO  → 透传（ignore_connection）→ 原样转发，不碰内容
    │
    ▼
目标服务器
```

透传模式下：
- mitmproxy 只做 TCP 转发，不拆 TLS
- 客户端看到的是原始服务器证书
- 不需要客户端信任 mitmproxy CA
- 性能开销接近零

## 自定义 Addon 开发

### 基本模板

```python
# my_addon.monitor.py
from mitmproxy import http, ctx

class MyAddon:
    def request(self, flow: http.HTTPFlow):
        # 请求阶段
        ctx.log.info(f"Request: {flow.request.pretty_url}")

    def response(self, flow: http.HTTPFlow):
        # 响应阶段
        ctx.log.info(f"Response: {flow.response.status_code}")

addons = [MyAddon()]
```

### 可用 Hook 点

| Hook | 触发时机 | 常见用途 |
|------|----------|----------|
| `tls_clienthello` | TLS 握手开始 | 域名分流、透传控制 |
| `request` | 收到完整请求 | 审计、修改请求、拦截 |
| `responseheaders` | 收到响应头 | 启用流式处理 |
| `response` | 收到完整响应 | 记录、修改响应 |
| `error` | 连接错误 | 错误处理 |

### 读取共享规则

addon 之间可以通过 `rules.json` 共享配置：

```python
import json, os

RULES_FILE = os.path.join(os.path.expanduser("~"), ".cicy", "monitor", "rules.json")

def load_rules():
    try:
        with open(RULES_FILE) as f:
            return json.load(f)
    except:
        return {}
```

### 与 API 通信

addon 可以通过 HTTP 调用 cicy-code-api：

```python
import urllib.request, json, os

API_PORT = os.environ.get("API_PORT", "8008")

def notify_api(event, data):
    body = json.dumps({"event": event, "data": data}).encode()
    req = urllib.request.Request(
        f"http://127.0.0.1:{API_PORT}/api/chat/webhook",
        data=body,
        headers={"Content-Type": "application/json"}
    )
    try:
        urllib.request.urlopen(req, timeout=3)
    except:
        pass
```

## 客户端配置

### kiro-cli

在 pane 的 agent_config 中设置 proxy 字段：

```
http_proxy=http://user:pass@127.0.0.1:8003
https_proxy=http://user:pass@127.0.0.1:8003
```

其中 `user` 是 pane_id，用于 mitmproxy 识别流量来源。

### 信任 CA 证书

解密模式下客户端需要信任 mitmproxy CA：

```bash
# CA 证书位置（mitmproxy 首次启动自动生成）
~/.mitmproxy/mitmproxy-ca-cert.pem

# 系统级信任（Ubuntu/Debian）
sudo cp ~/.mitmproxy/mitmproxy-ca-cert.pem /usr/local/share/ca-certificates/mitmproxy.crt
sudo update-ca-certificates

# Node.js 应用
export NODE_EXTRA_CA_CERTS=~/.mitmproxy/mitmproxy-ca-cert.pem
```

## 注意事项

1. **透传域名不需要 CA**：只有 `monitor_domains` 中的域名需要客户端信任 CA，其余透传域名完全无感
2. **addon 文件命名**：必须以 `.monitor.py` 结尾，否则不会被加载
3. **规则热更新 vs addon 热更新**：`rules.json` 修改后 5 秒内自动生效；addon 文件增删需要 restart
4. **standalone binary 的额外包**：如果用官方二进制而非 pip 安装，addon 中 `import redis` 等第三方包会失败，需要改用 `urllib` 等标准库替代，或改用 pip/uvx 安装方式
5. **不要修改请求签名**：审计模块只做只读检查 + 拦截，不修改请求内容，避免触发 AWS SigV4 签名校验失败
