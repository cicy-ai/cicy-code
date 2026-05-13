# Cloudflare Tunnel 路由管理技能

## 用途
通过 Cloudflare API 管理 Tunnel 路由，自动创建 g-{port}.cicy.de5.net → localhost:{port} 映射 + DNS CNAME。

## 使用方法

```bash
# 列出所有路由 + 本地端口状态（默认 prod）
bash ./cf-tunnel.sh list

# 添加路由（自动创建 DNS CNAME，默认 prod）
bash ./cf-tunnel.sh add 8101

# 批量添加
bash ./cf-tunnel.sh add 8101 8102 8103

# 删除路由 + DNS
bash ./cf-tunnel.sh del 8101
```

```result
```

## 功能特点
- ✅ 通过 Cloudflare API 操作（不需要 cloudflared CLI）
- ✅ 自动创建 DNS CNAME 记录
- ✅ 检查本地端口是否在监听
- ✅ 批量添加支持
- ✅ 域名格式：g-{port}.cicy.de5.net

## 依赖
- Python 3 + requests
- 环境变量 CLOUDFLARE_API_TOKEN_TUNNEL（已配置在 ~/personal/startup）

## 测试环境联调用法

如果要给 `cicy-cloud` / `new-api` 临时开测试域名，可以直接映射本地端口：

- `5174` → `cicy-cloud` 前端
- `8010` → `cicy-cloud` 后端 / OAuth Provider
- `13000` → `new-api`

示例：

```bash
# 先查看当前路由
python3 ~/skills/infra/cf-tunnel.py list

# 给测试链路开 3 个入口
python3 ~/skills/infra/cf-tunnel.py add 5174 8010 13000
```

添加后会得到类似域名：

- `g-5174.cicy.de5.net`
- `g-8010.cicy.de5.net`
- `g-13000.cicy.de5.net`

适合 OAuth 联调的对应关系：

- `cicy-cloud` 前端：`https://g-5174.cicy.de5.net`
- `cicy-cloud` OAuth 端点：`https://g-8010.cicy.de5.net/oauth/authorize`
- `new-api`：`https://g-13000.cicy.de5.net`
- `new-api` OAuth 回调：`https://g-13000.cicy.de5.net/oauth/cicy`

删除测试入口：

```bash
python3 ~/skills/infra/cf-tunnel.py del 5174 8010 13000
```

如果要使用 dev 环境配置：

```bash
CF_ENV=dev python3 ~/skills/infra/cf-tunnel.py add 5174 8010 13000
```

## 注意事项
- 只管理路由和 DNS，不管理 cloudflared 进程本身
- cloudflared 进程由系统管理，❌ 绝对不能 kill
