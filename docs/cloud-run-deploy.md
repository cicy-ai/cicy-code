# Google Cloud Run 部署指南

cicy-code 支持以单机模式部署到 Cloud Run，适合个人或小团队快速搭建 AI 开发环境。

## 前提条件

- Google Cloud 账号，已启用 Cloud Run API
- 安装 `gcloud` CLI 并登录
- Docker（本地构建镜像用）

## 架构说明

```
Cloud Run 实例（单容器）
  ├── cicy-code binary（API + 内嵌 ttyd + 内嵌 UI）
  ├── tmux（终端复用）
  ├── Node.js + AI CLI 工具（claude / gemini / codex 等）
  └── SQLite（~/.cicy/data.db）
```

Cloud Run 适合 **本地模式**（SQLite + 文件缓存），无需 MySQL/Redis。

> ⚠️ Cloud Run 实例可能被冷启动回收，SQLite 数据会丢失。如需持久化，挂载 Cloud Storage FUSE 或使用 Cloud SQL。

## Dockerfile

```dockerfile
FROM golang:1.25 AS builder
WORKDIR /app
COPY api/go.mod api/go.sum ./
RUN go mod download
COPY api/ .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o cicy-code ./mgr/

FROM node:20-slim

# 系统依赖
RUN apt-get update && apt-get install -y --no-install-recommends \
    tmux bash curl ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

# AI CLI 工具（按需调整）
RUN npm install -g @anthropic-ai/claude-code @google/gemini-cli @openai/codex

# Kiro CLI
RUN curl -fsSL https://cli.kiro.dev/install -o /tmp/kiro-install.sh \
    && yes | bash /tmp/kiro-install.sh

# OpenCode
RUN curl -fsSL https://opencode.ai/install | bash

WORKDIR /app
COPY --from=builder /app/cicy-code /usr/local/bin/cicy-code

# 端口：Cloud Run 使用 PORT 环境变量
ENV PORT=8080
EXPOSE 8080

# 以非 root 用户运行
RUN useradd -m -s /bin/bash cicy
USER cicy
ENV HOME=/home/cicy
ENV PATH="/home/cicy/.local/bin:/home/cicy/.opencode/bin:${PATH}"

CMD ["cicy-code", "--public"]
```

## 构建与部署

### 方式一：使用 Cloud Build（推荐）

```bash
# 在项目根目录
gcloud builds submit --tag gcr.io/PROJECT_ID/cicy-code

# 部署到 Cloud Run
gcloud run deploy cicy-code \
  --image gcr.io/PROJECT_ID/cicy-code \
  --platform managed \
  --region asia-east1 \
  --allow-unauthenticated \
  --memory 2Gi \
  --cpu 2 \
  --timeout 3600 \
  --max-instances 1 \
  --min-instances 1 \
  --port 8080
```

### 方式二：本地构建推送

```bash
# 构建
docker build -f Dockerfile.cloudrun -t gcr.io/PROJECT_ID/cicy-code .

# 推送
docker push gcr.io/PROJECT_ID/cicy-code

# 部署
gcloud run deploy cicy-code \
  --image gcr.io/PROJECT_ID/cicy-code \
  --platform managed \
  --region asia-east1 \
  --allow-unauthenticated \
  --memory 2Gi \
  --cpu 2 \
  --timeout 3600 \
  --max-instances 1 \
  --min-instances 1
```

### 关键参数说明

| 参数 | 值 | 说明 |
|------|-----|------|
| `--memory` | 2Gi | AI CLI 工具需要较多内存 |
| `--cpu` | 2 | 多 Agent 并发运行 |
| `--timeout` | 3600 | WebSocket 长连接需要较长超时 |
| `--max-instances` | 1 | 单机模式只需 1 个实例 |
| `--min-instances` | 1 | 保持常驻，避免冷启动 |
| `--port` | 8080 | Cloud Run 默认端口 |

## 数据持久化

Cloud Run 容器是无状态的，重启后 SQLite 数据会丢失。

### 方案一：Cloud Storage FUSE（推荐）

```bash
# 创建 GCS bucket
gsutil mb -l asia-east1 gs://PROJECT_ID-cicy-data

# 部署时挂载
gcloud run deploy cicy-code \
  --image gcr.io/PROJECT_ID/cicy-code \
  --add-volume name=cicy-data,type=cloud-storage,bucket=PROJECT_ID-cicy-data \
  --add-volume-mount volume=cicy-data,mount-path=/home/cicy/.cicy \
  --execution-environment gen2 \
  # ... 其他参数
```

### 方案二：Cloud SQL（SaaS 模式）

如需 MySQL 后端，改用 SaaS 模式：

```bash
gcloud run deploy cicy-code \
  --set-env-vars "SAAS_MODE=1,MYSQL_DSN=user:pass@tcp(INSTANCE_IP)/cicy" \
  # ... 其他参数
```

## 自定义域名与 HTTPS

Cloud Run 自动提供 HTTPS。绑定自定义域名：

```bash
gcloud run domain-mappings create \
  --service cicy-code \
  --domain code.yourdomain.com \
  --region asia-east1
```

按提示添加 DNS 记录即可。

## WebSocket 注意事项

Cloud Run 支持 WebSocket，但有限制：

- 默认请求超时 300 秒，需设置 `--timeout 3600`
- HTTP/2 下 WebSocket 需要 gRPC 或 `--use-http2`（默认 HTTP/1.1 即可）
- 空闲连接可能被 Cloud Run 回收，ttyd 前端会自动重连

## 成本估算

| 配置 | 月费（约） |
|------|-----------|
| 1 vCPU, 2GB, min=1 | ~$50 |
| 2 vCPU, 2GB, min=1 | ~$90 |
| 2 vCPU, 4GB, min=0（按需） | ~$20-50 |

> `min-instances=0` 可节省成本，但首次访问会有 10-30 秒冷启动。

## 与本地部署的区别

| 特性 | 本地部署 | Cloud Run |
|------|---------|-----------|
| 启动命令 | `npx cicy-code` | `cicy-code --public` |
| 数据库 | SQLite（本地磁盘） | SQLite（GCS FUSE）或 Cloud SQL |
| 监听地址 | 127.0.0.1 | 0.0.0.0（`--public`） |
| HTTPS | 需自行配置 | 自动 |
| 端口 | 18008 | 8080（`PORT` 环境变量） |
| 进程管理 | Supervisor / launchd | Cloud Run 自动 |
| 冷启动 | 无 | min=0 时 10-30 秒 |
| 资源模式 | 发布模式（binary 内嵌） | 发布模式（binary 内嵌） |
