# 部署架构

cicy-code 平台提供三种部署层级，满足从试用到生产的完整用户旅程。

## 总览

```
┌─────────────────────────────────────────────────────────┐
│                    CiCy Code 平台                        │
├──────────────┬──────────────────┬────────────────────────┤
│  🏠 本机部署   │  ☁️ Cloud Run 试用  │  🚀 PRO VM 专属实例    │
│  (Free)      │  (Trial)          │  (Paid)               │
├──────────────┼──────────────────┼────────────────────────┤
│ npx cicy-code│ 一键试用，无需安装  │ 独占 VM，持久化存储     │
│ 用户自己的机器 │ 平台自动创建/回收  │ 自定义配置，SLA 保障    │
│ SQLite 本地   │ MySQL + Redis 共享 │ MySQL + Redis 独享     │
│ 6 Agent      │ 6 Agent 预装      │ 6 Agent + 自定义扩展    │
│ 无时间限制    │ 有试用时长         │ 按月订阅               │
└──────────────┴──────────────────┴────────────────────────┘
```

## 三种模式对比

| 特性 | 🏠 本机部署 | ☁️ Cloud Run 试用 | 🚀 PRO VM |
|------|-----------|------------------|-----------|
| 目标用户 | 开发者自用 | 新用户体验 | 付费用户 |
| 启动方式 | `npx cicy-code` | 平台 API 自动创建 | 平台分配 VM |
| 运行模式 | 本地模式 | SaaS 模式 | SaaS 模式 |
| 数据库 | SQLite（本地） | MySQL（共享） | MySQL（独享/共享） |
| 缓存 | 文件 | Redis（共享） | Redis |
| 监听地址 | 127.0.0.1 | 0.0.0.0 | 0.0.0.0 |
| HTTPS | 用户自配 | Cloud Run 自动 | Nginx / LB |
| 端口 | 18008 | 8080 | 8008 |
| 进程管理 | Supervisor / launchd | Cloud Run 托管 | Supervisor / systemd |
| 数据持久化 | ✅ 本地磁盘 | ❌ 实例销毁即清理 | ✅ 磁盘持久 |
| AI 工具安装 | 首次交互式选择 | 镜像预装 | 镜像预装或自动安装 |
| 资源 | 用户硬件 | 2 vCPU / 2GB | 4+ vCPU / 8GB+ |
| 成本 | 免费 | 平台承担 | 用户订阅 |
| 时间限制 | 无 | 有（试用期） | 订阅期内无限 |

---

## 🏠 本机部署（Free）

详见 [local-deploy.md](local-deploy.md)

```bash
npx cicy-code
```

适合开发者在自己的 Mac / Linux 机器上运行，完全本地化，数据不离开机器。

---

## ☁️ Cloud Run 试用（Trial）

用户在平台点击「免费试用」后，自动创建一个 Cloud Run 实例。

### 工作流程

```
用户点击"试用"
    ↓
平台 API → gcloud run deploy
    ↓
Cloud Run 实例启动（cicy-code --saas --public）
    ↓
返回用户访问 URL + Token
    ↓
试用到期 → gcloud run services delete
```

### Dockerfile

```dockerfile
FROM golang:1.25 AS builder
WORKDIR /app
COPY . .
RUN ./build.sh build linux amd64

FROM node:20-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    tmux bash curl ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

# 预装全部 AI 工具（镜像中完成，用户零等待）
RUN npm install -g @anthropic-ai/claude-code @google/gemini-cli @openai/codex
RUN curl -fsSL https://cli.kiro.dev/install -o /tmp/kiro-install.sh && yes | bash /tmp/kiro-install.sh
RUN curl -fsSL https://opencode.ai/install | bash

COPY --from=builder /app/api/cicy-code /usr/local/bin/cicy-code

RUN useradd -m -s /bin/bash cicy
USER cicy
ENV HOME=/home/cicy
ENV PATH="/home/cicy/.local/bin:/home/cicy/.opencode/bin:${PATH}"
ENV PORT=8080

CMD ["cicy-code", "--saas", "--public"]
```

### 平台集成 API

```bash
# 创建试用实例
gcloud run deploy cicy-trial-${USER_ID} \
  --image gcr.io/${PROJECT}/cicy-code \
  --platform managed \
  --region asia-east1 \
  --no-allow-unauthenticated \
  --memory 2Gi --cpu 2 \
  --timeout 3600 \
  --max-instances 1 --min-instances 1 \
  --set-env-vars "SAAS_MODE=1,MYSQL_DSN=${MYSQL_DSN},REDIS_HOST=${REDIS_HOST}"

# 回收试用实例
gcloud run services delete cicy-trial-${USER_ID} --region asia-east1 --quiet
```

### Cloud Run 参数

| 参数 | 值 | 说明 |
|------|-----|------|
| `--memory` | 2Gi | AI CLI 工具内存需求 |
| `--cpu` | 2 | 多 Agent 并发 |
| `--timeout` | 3600 | WebSocket 长连接 |
| `--max-instances` | 1 | 单用户单实例 |
| `--min-instances` | 1 | 避免冷启动（试用期间） |

### 成本控制

| 策略 | 说明 |
|------|------|
| `min-instances=0` | 空闲时缩容到 0，按使用计费 |
| 试用时长限制 | 到期自动删除实例 |
| 共享 MySQL/Redis | 多试用用户共享后端 |

预计单试用用户成本：~$0.5-2/天（按实际使用时长）。

### 注意事项

- Cloud Run WebSocket 空闲连接可能被回收，ttyd 前端会自动重连
- 试用实例无状态，数据在平台 MySQL 中，实例销毁不丢数据
- AI 工具预装在镜像里，用户打开即用

---

## 🚀 PRO VM 专属实例（Paid）

付费用户分配独立的 GCE / AWS EC2 虚拟机，资源独占，无时间限制。

### 架构

```
GCE VM（用户专属）
  ├── cicy-code --saas --public --audit
  ├── tmux + 6 Agent（预装）
  ├── code-server（端口 18080）
  ├── Nginx（反向代理 + HTTPS）
  └── Supervisor（进程管理）
```

### 初始化脚本

```bash
#!/bin/bash
# VM 启动脚本（startup-script）

# 安装依赖
apt-get update && apt-get install -y tmux supervisor nginx curl git

# 安装 Node.js
curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
apt-get install -y nodejs

# 安装 cicy-code
npm install -g cicy-code

# 安装 AI 工具
npm install -g @anthropic-ai/claude-code @google/gemini-cli @openai/codex
curl -fsSL https://cli.kiro.dev/install | bash
curl -fsSL https://opencode.ai/install | bash

# Supervisor 配置
cat > /etc/supervisor/conf.d/cicy-code.conf << 'EOF'
[program:cicy-code]
command=cicy-code --saas --public
user=cicy
autostart=true
autorestart=true
environment=SAAS_MODE="1",MYSQL_DSN="%(ENV_MYSQL_DSN)s",REDIS_HOST="%(ENV_REDIS_HOST)s",PORT="8008"
stderr_logfile=/var/log/cicy-code/error.log
stdout_logfile=/var/log/cicy-code/output.log
EOF

mkdir -p /var/log/cicy-code
supervisorctl reread && supervisorctl update
```

### Nginx 反向代理

```nginx
server {
    listen 443 ssl http2;
    server_name user123.cicy.ai;

    ssl_certificate     /etc/letsencrypt/live/user123.cicy.ai/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/user123.cicy.ai/privkey.pem;

    # API + 管理 UI
    location / {
        proxy_pass http://127.0.0.1:8008;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }

    # WebSocket（ttyd 终端）
    location /ttyd/ {
        proxy_pass http://127.0.0.1:8008;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;
    }

    # code-server
    location /code/ {
        proxy_pass http://127.0.0.1:18080/;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

### VM 规格建议

| 套餐 | 规格 | 适合 |
|------|------|------|
| Starter | 2 vCPU / 4GB / 50GB SSD | 个人开发者 |
| Pro | 4 vCPU / 8GB / 100GB SSD | 小团队 |
| Enterprise | 8 vCPU / 16GB / 200GB SSD | 重度使用 |

### 与 Cloud Run 试用的区别

| 特性 | Cloud Run 试用 | PRO VM |
|------|--------------|--------|
| 资源 | 共享，弹性 | 独占，固定 |
| 磁盘 | 临时（无状态） | 持久 SSD |
| 网络 | Cloud Run 域名 | 自定义域名 + SSL |
| 性能 | 受冷启动影响 | 始终就绪 |
| code-server | ❌ | ✅ |
| 审计模式 | ❌ | ✅（`--audit`） |
| SSH 访问 | ❌ | ✅ |
| 自定义工具 | ❌ 镜像固定 | ✅ 用户可自行安装 |

---

## 共享基础设施

三种模式共享的 SaaS 后端（Cloud Run 和 PRO VM 使用）：

```
┌────────────────────────────────┐
│         共享后端                │
├────────────────────────────────┤
│  MySQL    — 用户/Token/配置     │
│  Redis    — 状态缓存/实时通信   │
│  主站 API — 用户注册/登录/计费  │
│  Nginx LB — 域名路由           │
└────────────────────────────────┘
```

部署参考：项目根目录 `docker-compose.yml` 包含 MySQL + Redis + Nginx + phpMyAdmin 的完整编排（mitmproxy 已嵌入 Go binary，不再需要独立容器）。

## 环境变量汇总

| 变量 | 本机 | Cloud Run | PRO VM | 说明 |
|------|------|-----------|--------|------|
| `PORT` | 18008 | 8080 | 8008 | API 端口 |
| `SAAS_MODE` | - | 1 | 1 | SaaS 模式 |
| `MYSQL_DSN` | - | 共享实例 | 共享/独享 | MySQL 连接串 |
| `REDIS_HOST` | - | 共享实例 | 共享 | Redis 地址 |
| `--public` | - | ✅ | ✅ | 监听 0.0.0.0 |
| `--audit` | - | - | 可选 | mitmproxy 审计 |
| `--dev` | 可选 | - | - | 开发模式 |
