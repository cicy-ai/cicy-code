# SaaS 部署架构

## 两套方案

| | Free (试用) | Pro (付费) |
|---|---|---|
| 机器 | 共享 VM | 独立 VM |
| 隔离 | Linux 用户 | 整台 VM |
| MySQL/Redis | 共享实例 | 独立 Docker 容器 |
| code-server | 独立容器 | 独立容器 |
| cicy-api | 独立进程，动态端口 | 独占 :8008 |
| CF Tunnel | 共享一个 tunnel | 独立 tunnel |
| 开通速度 | 秒级 | 分钟级 |
| 成本 | 几十用户/台 | 1 用户/台 |

---

## 域名规则

| 用途 | Pro | Free |
|------|-----|------|
| 前端 | `u-xxx-app.cicy-ai.com` | `u-xxx-free-app.cicy-ai.com` |
| API | `u-xxx-api.cicy-ai.com` | `u-xxx-free-api.cicy-ai.com` |

- 前端：通配符 DNS → CF Worker → SPA 静态资源
- API：CNAME → CF Tunnel → cicy-api 端口
- 浏览器直连 API 域名，Worker 不代理 API/WebSocket

---

## Free 方案（共享 VM）

### 架构

```
共享 VM (当前 cicy-prod, 35.241.97.128)
│
├── 主用户 (w3c_offical, Pro)
│   ├── cicy-api :8008 (supervisor)
│   ├── docker-compose (mysql/redis/code-server/mitmproxy)
│   └── CF Tunnel ingress: u-xxx-api → localhost:8008
│
├── 公共服务
│   ├── MySQL :3306 — 每 Free 用户一个 database (cicy_f_001, cicy_f_002...)
│   ├── Redis :6379 — 每 Free 用户一个 db number (db1, db2...)
│   └── cloudflared (tunnel f4d12416-...)
│
├── f-001 (uid=3001, Free 用户 1)
│   ├── /home/f-001/
│   │   ├── projects/cicy-code/   — api binary + run.sh (symlink)
│   │   ├── workers/w-10001/      — workspace
│   │   └── global.json           — token + config
│   ├── cicy-api :9001 (supervisor: cicy-api-f-001)
│   ├── code-server 容器 (cicybot/code-server, :8201)
│   └── tmux session (f-001)
│
├── f-002 (uid=3002, Free 用户 2)
│   ├── cicy-api :9002 (supervisor: cicy-api-f-002)
│   ├── code-server 容器 (:8202)
│   └── ...同上
│
└── CF Tunnel ingress:
    ├── u-001-free-api.cicy-ai.com → localhost:9001
    ├── u-002-free-api.cicy-ai.com → localhost:9002
    └── ...
```

### 命名与编号标准

| 项目 | 规则 | 示例 |
|------|------|------|
| 用户 ID | `f-{NNN}` (3 位，从 001 开始) | f-001, f-002 |
| Linux 用户名 | 同用户 ID | f-001 |
| UID | 3000 + N | 3001, 3002 |
| 域名前缀 | `u-{NNN}-free` | u-001-free |
| 前端域名 | `u-{NNN}-free-app.cicy-ai.com` | u-001-free-app.cicy-ai.com |
| API 域名 | `u-{NNN}-free-api.cicy-ai.com` | u-001-free-api.cicy-ai.com |

### 端口分配

| 服务 | 规则 | 示例 (N=1) | 示例 (N=2) |
|------|------|------------|------------|
| cicy-api | 9000 + N | 9001 | 9002 |
| code-server | 8200 + N | 8201 | 8202 |
| gotty/ttyd | 10000 + N | 10001 | 10002 |

保留端口：8000-8099 (Pro/系统), 9000 (保留)

### 目录结构

```
/home/f-001/
├── projects/
│   └── cicy-code/          — api binary (symlink 或 copy)
│       ├── api/cicy-code-api
│       ├── run.sh
│       └── schema.sql
├── workers/
│   └── w-10001/            — workspace
├── global.json             — {"api_token": "xxx", "port": 9001, ...}
└── data/                   — 用户数据
```

### 资源限制

| 资源 | 限制 |
|------|------|
| code-server 容器 | mem_limit: 512m, cpus: 0.5 |
| MySQL database | 共享，无单独限制 |
| Redis db | 共享，无单独限制 |
| 磁盘 | /home/f-xxx quota (TODO) |

### Provision 流程 (provision-free.sh)

```bash
provision-free.sh 001   # 创建 f-001 用户
```

1. 创建 Linux 用户 `f-001` (uid=3001, home=/home/f-001)
2. 初始化目录结构 + global.json
3. 创建 MySQL database `cicy_f_001` + 导入 schema.sql
4. 启动 code-server 容器 (docker run)
5. 创建 supervisor 配置 + 启动 cicy-api
6. 创建 tmux session
7. CF API: 添加 DNS CNAME `u-001-free-api`
8. CF API: 更新 tunnel ingress 添加 `u-001-free-api → localhost:9001`
9. 验证 health

### Deprovision 流程 (deprovision-free.sh)

```bash
deprovision-free.sh 001   # 删除 f-001 用户
```

1. 停 supervisor 进程
2. 停 + 删 code-server 容器
3. 删 tmux session
4. CF API: 删 DNS + 更新 tunnel ingress
5. 删 MySQL database
6. 删 Linux 用户 + home 目录

---

## Pro 方案（独立 VM）

### 架构

```
独立 GCP VM
├── docker-compose.saas.yml
│   ├── mysql       (:3306)
│   ├── redis       (:6379)
│   ├── mitmproxy   (cicybot/mitmproxy)
│   └── code-server (cicybot/code-server)
├── cicy-api (:8008) — supervisor
├── cloudflared — 独立 tunnel
└── tmux session (w-10001)
```

### Provision (provision.sh)

1. 创建 CF Tunnel + DNS
2. 创建 GCP VM (从 cicy-base image)
3. SCP + SSH setup-fast.sh
4. 验证 health

### Deprovision (deprovision.sh)

1. 删 GCP VM
2. 删 CF Tunnel + DNS

---

## CF Worker (app-worker.js)

路由 `*.cicy-ai.com/*`：

- `u-xxx-api` / `u-xxx-free-api` → passthrough (fetch 到 origin，走 Tunnel)
- `u-xxx-app` / `u-xxx-free-app` → SPA 静态资源
- Worker 不代理 API/WebSocket

---

## Docker 镜像

| 镜像 | 用途 |
|------|------|
| cicybot/code-server | code-server 容器 (Pro + Free) |
| cicybot/mitmproxy | mitmproxy 容器 (Pro only) |
| mysql:8.0 | Pro 独立 MySQL |
| redis:7-alpine | Pro 独立 Redis |

---

## 关键文件

```
cicy-code/
├── app/worker/app-worker.js      — CF Worker
├── app/src/config.ts             — 前端配置（自动判断 Pro/Free）
├── provision-free.sh             — Free 用户开通
├── deprovision-free.sh           — Free 用户删除
├── provision.sh                  — Pro 开通
├── deprovision.sh                — Pro 删除
├── setup-fast.sh                 — Pro VM 部署
├── build-image.sh                — GCP base image
├── dump-schema.sh                — 导出 schema.sql
├── schema.sql                    — 表结构 + seed data
└── docs/saas-architecture.md     — 本文档
```
