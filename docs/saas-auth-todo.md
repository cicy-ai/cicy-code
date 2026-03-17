# SaaS 认证系统 - 未完成报告

## 已完成 ✅

### 1. 登录流程
- GitHub OAuth 登录 → 生成 JWT → 跳转到子域名 `u-{id8}.cicy-ai.com?token=JWT`
- mgr `/api/auth/github/callback` 回调处理
- JWT 签发和验证（HS256，7天有效期）
- `saas_users` 表：id, email, plan, backend_url

### 2. CF Worker 路由
- `app.cicy-ai.com/*` → 登录页 + API 代理到 mgr
- `*.cicy-ai.com/*` → 子域名，API 代理到用户的 backend
- 通配符 DNS `*.cicy-ai.com` 已配置
- `/api/resolve?slug=u-xxx` 接口：slug → backend_url

### 3. 前端
- `config.ts`：子域名上所有 base URL 指向当前 origin
- `ProvisionScreen.tsx`：SSE 实时显示 provision 进度（5 步）
- `AuthContext.tsx`：JWT 验证 + provisioning 状态
- Workspace UI 完全一样，无 isSaas 区分

### 4. provision.sh 改造
- 非交互式，区域从参数传入：`bash provision.sh <vm_name> [zone]`
- 默认 `asia-east1-b`（台湾）

---

## 未完成 ❌

### 1. Free 用户 VM 部署 ✅ 已决定
- **方案：全部用 VM，Free 和 Pro 架构一致**
- Free 用户：`e2-micro` + 10GB SSD + 7 天试用期
- Pro 用户：`e2-small`（或更大）+ 持久化
- `provision.go` 已改：根据 plan 设置 MACHINE_TYPE

### 2. DB backend_url 为空
- 测试用户 `1773655510414739751` 的 `backend_url` 为空
- provision 流程没有成功写入

### 3. Free 用户过期清理
- 需要定时脚本：7 天后自动销毁 Free 用户 VM
- 清理 GCP VM + CF Tunnel + DNS 记录
- 前端加倒计时提示

### ~~Cloud Run 方案~~ ❌ 已废弃
- Cloud Run 不适合有状态服务（tmux/code-server/长连接）
- 永久废弃，不再考虑

---

## 相关文件

| 文件 | 说明 |
|------|------|
| `api/mgr/oauth.go` | GitHub OAuth + JWT |
| `api/mgr/provision.go` | SSE 进度 + 调 provision |
| `api/mgr/auth.go` | handleResolve, handleAuthVerify |
| `app/worker/app-worker.js` | CF Worker 路由 |
| `app/src/config.ts` | 子域名 origin 检测 |
| `app/src/components/ProvisionScreen.tsx` | 进度 UI |
| `provision.sh` | VM 创建脚本（Pro 用） |
| `setup-prod.sh` | VM 内部署脚本 |

---

## 清理

误创建的资源需要清理：
- GCP VM: `u-17736555`
- CF Tunnel: `83717433-aff0-4ff8-b871-063c2f397c18`
- DNS: `u-17736555.cicy-ai.com`, `u-17736555-api.cicy-ai.com`
