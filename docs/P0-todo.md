# P0 TODO — 一键部署 + Worker-Master 协同

> 两条线并行推进，目标：让用户 5 分钟跑起来 + 两个 agent 能自动协同。

---

## 线 1：一键部署

### 1.1 Docker 镜像发布
- [x] API 构建为 Docker 镜像 → `cicybot/cicy-api:latest` (23.8MB)
- [x] 推送到 Docker Hub
- [ ] CI/CD: GitHub Actions push → 自动 build + push 镜像

### 1.2 setup-prod.sh（用户 VM 部署脚本）
- [x] 不需要源码，从 Docker Hub 拉取二进制
- [x] API 用 supervisor 管理（宿主机进程，能访问 tmux/文件）
- [x] MySQL + Redis + Nginx 用 Docker Compose
- [x] 自动生成 docker-compose.yml + nginx.conf + .env
- [x] 接收 CF Tunnel token 参数，自动安装 cloudflared
- [x] 测试 VM 验证通过（cicy-test, 34.92.166.144）
- [ ] 前端 dist 打包到 Nginx（目前 Nginx 是空的）

### 1.3 CF Tunnel 自动分配（Prod API）
- [ ] `POST /api/workspace/provision` — 创建用户专属 tunnel
  - Prod API 调 CF API 创建 tunnel + 配路由 + 配 DNS
  - 返回 `{ tunnel_token, subdomain }`
  - 用户 VM 只拿 token，不接触 CF 密钥
- [ ] tunnel 路由配置: `{user}.ws.cicy-ai.com → :8008`
- [ ] DNS CNAME 自动创建
- [ ] 用户撤销/删除 tunnel

### 1.4 VM 自动创建（可选，云工作站用）
- [ ] GCP API 自动创建 VM
- [ ] 自动 SCP setup-prod.sh + 执行
- [ ] 自动传入 tunnel token
- [ ] 用户销毁 VM

### 1.5 首次引导
- [x] Login 页面支持 GitHub OAuth + Token 粘贴
- [x] OAuth 回调 `?token=JWT` 自动登录
- [ ] 设置密码 → 生成 token → 自动跳转主界面
- [ ] 绑定第一个 agent 的引导流程

### 1.6 部署文档
- [ ] GCP 部署指南
- [ ] 最低配置要求（2C4G 够用）
- [ ] README 重写：30 秒看懂 + 5 分钟跑起来

---

## 线 2：Worker-Master 协同

### 2.1 Agent 角色系统
- [ ] config JSON 增加 `role` 字段（worker / master / null）
- [ ] `PUT /api/agents/:id/role` — 设置角色
- [ ] 查询配对 API — 同 workspace 下的 worker/master 互查
- [ ] 前端：绑定 agent 时选角色

### 2.2 Watcher Hook
- [ ] fsnotify 检测 pane 状态变化（thinking → idle）
- [ ] idle 时查找同 workspace 的 Master pane
- [ ] 自动 `tm msg` 通知 Master："Worker {name} 已完成，请验收"

### 2.3 通知系统
- [ ] `POST /api/notify` — Agent 发通知给前端
- [ ] `GET /api/notify/stream` — SSE 推送通道
- [ ] Redis PUBLISH kiro_notify 中转
- [ ] 前端 SSE 监听 → 执行 action

### 2.4 共享文档 .cicy/
- [ ] `GET /api/cicy/files?pane={pane_id}` — 列出文件
- [ ] `GET /api/cicy/file?pane={pane_id}&name={filename}` — 读取内容
- [ ] 前端 Dashboard tab：文件列表 + todo 进度条

### 2.5 端到端验证
- [ ] 用户说需求 → Master 写 .cicy/todo.md
- [ ] Master 分配 Worker 执行
- [ ] Worker 完成 → watcher 通知 → Master 验收 → 下一项

---

## 线 3：SaaS API (cicy-api)

### 3.1 已完成
- [x] JWT auth (register/login/me)
- [x] WebSocket chatbus
- [x] Plugin system (Google, Notion registry)
- [x] Desktop apps CRUD (cloud-synced)
- [x] Docker 镜像 + Docker Hub
- [x] 三种登录方式：本机 Token / GitHub OAuth / URL Token
- [x] 统一认证：`/api/auth/verify` 自动识别 JWT vs 本机 token
- [x] 每用户独立后端：`backend_url` 字段（Cloud Run / VM）
- [x] 自动 Provision：新用户注册 → 异步创建 Cloud Run 实例 → 写入 backend_url
- [x] 前端动态 API 路由：登录后根据 backend_url 切换 API baseURL
- [x] Provisioning 等待页：backend 未就绪时轮询等待

### 3.2 待做
- [ ] `DELETE /api/workspace/{id}` — 销毁 Cloud Run 实例
- [ ] `GET /api/workspace/status` — 查询 workspace 状态
- [ ] Stripe/Paddle 计费集成
- [ ] 用户配额管理（免费 50 次/天，Pro 500 次/天）
- [ ] Pro 升级流程：付费 → 创建 VM → 迁移数据 → 更新 backend_url

---

## 清理
- [ ] 删除测试 VM: `gcloud compute instances delete cicy-test --zone=asia-east2-a`
- [ ] setup-prod.sh 提交到 cicy-code 仓库
- [ ] 旧 setup.sh 可删除（已被 setup-prod.sh 替代）

---

## 完成标准

### 线 1 完成标准
用户注册 → Prod API 自动创建 tunnel → 返回 token
→ 用户 VM 跑 `sudo bash setup-prod.sh <token>`
→ 全部服务启动 + CF Tunnel 连通 → 浏览器访问
→ 全程 < 5 分钟

### 线 2 完成标准
Master + Worker 自动协同，用户只需确认方案

### 线 3 完成标准
SaaS 平台可注册、登录、自动分配独立 Cloud Run 后端、计费

### 认证架构

```
用户类型        认证方式              API 指向
─────────────────────────────────────────────────
本机 Token      粘贴 token           当前 8008 (mgr)
试用 SaaS       GitHub OAuth → JWT   用户独立 Cloud Run
Pro SaaS        GitHub OAuth → JWT   用户独立 VM
```

```
新用户注册流程:
OAuth → findOrCreateUser → INSERT users → go ProvisionBackend()
  │                                              │
  │  前端: provisioning=true                     gcloud run deploy cicy-u-{id[:8]}
  │  "Setting up your workspace..."              │
  │  每 3s 轮询 /api/auth/verify                 UPDATE users SET backend_url=?
  │                                              │
  └─ backend 有值 → setBackend(url) → 进入 Workspace
```
