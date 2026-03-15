# cicy-code 部署文档

## 架构

```
API (Go)          → localhost:8008  → https://g-8008.cicy.de5.net
Frontend (Vite)   → localhost:6905   → https://ide.cicy.de5.net
MySQL             → localhost:13306  (Docker: cicy-code-mysql)
Redis             → localhost:16379  (Docker: cicy-code-redis)
mitmproxy         → localhost:18888  (proxy) / 18889 (web ui)
code-server       → localhost:18080  (Docker: cicy-code-server)
phpMyAdmin        → localhost:12222  (Docker: cicy-code-pma)
Redis Commander   → localhost:18379  (Docker: cicy-code-redis-admin)
```

## 前置条件

- Docker & Docker Compose
- Go 1.21+
- tmux
- Cloudflare Tunnel (cloudflared 已运行)

## 1. 启动 Docker 服务

```bash
cd /home/w3c_offical/projects/cicy-code
docker compose up -d mysql redis mitmproxy code-server phpmyadmin redis-commander ide-dev
```

等待 MySQL 初始化完成（约 15s）：
```bash
docker exec -i cicy-code-mysql mysql -uroot -p'cicy-code' -e "SELECT 1" 2>/dev/null
```

## 2. 初始化数据库

首次部署需要导入 schema：
```bash
docker exec -i cicy-code-mysql mysql -uroot -p'cicy-code' cicy_code < schema.sql
```

从 prod-mysql 迁移数据：
```bash
docker exec -i prod-mysql mysqldump -uroot -p'cicy-code' cicy_code 2>/dev/null | \
  docker exec -i cicy-code-mysql mysql -uroot -p'cicy-code' cicy_code
```

## 3. 安装 mitmproxy CA 证书

```bash
mkdir -p ~/certs
docker exec cicy-code-mitmproxy cat /home/mitmproxy/.mitmproxy/mitmproxy-ca-cert.pem > ~/certs/mitmproxy-ca.pem
sudo cp ~/certs/mitmproxy-ca.pem /usr/local/share/ca-certificates/mitmproxy.crt
sudo update-ca-certificates --fresh
```

## 4. 编译 & 启动 API

```bash
cd /home/w3c_offical/projects/cicy-code/api
GOROOT=/usr/lib/go CGO_ENABLED=0 go build -o cicy-code-api ./mgr/
```

在 tmux 中启动：
```bash
tmux send-keys -t w-10001:cicy-api \
  "HOME=/home/w3c_offical PORT=8008 \
   MYSQL_DSN='root:cicy-code@tcp(localhost:13306)/cicy_code' \
   REDIS_ADDR='localhost:16379' \
   ./cicy-code-api" Enter
```

## 5. 暴露端口 (Cloudflare Tunnel)

```bash
bash ~/skills/cf-tunnel.sh add 8008    # API
bash ~/skills/cf-tunnel.sh add 18379    # Redis Commander
# 6905 已通过 ide.cicy.de5.net 暴露
```

code-server 和 mitmproxy Web UI 通过 API 代理访问，不直接暴露：
- code-server: `https://g-8008.cicy.de5.net/code/?token=<TOKEN>`
- mitmproxy:   `https://g-8008.cicy.de5.net/mitm/?token=<TOKEN>`

## 6. 配置 Workers

### 设置 agent_config

```sql
-- 启用 worker
UPDATE agent_config SET active=1, agent_type='kiro-cli chat',
  config='{"proxy":{"enable":true}}'
  WHERE pane_id='w-20147:main.0';

-- 设置模型
UPDATE agent_config SET default_model='claude-3-5-sonnet-20241022'
  WHERE pane_id='w-20147:main.0';

-- 设置 master
UPDATE agent_config SET role='master', active=1
  WHERE pane_id='w-10001:main.0';
```

### 重启 Workers

```bash
curl -s -X POST -H "Authorization: Bearer <TOKEN>" \
  "http://localhost:8008/api/tmux/panes/w-20147:main.0/restart"
```

重启时会自动：
1. 设置 `X_PANE_ID` 环境变量
2. 如果 `config.proxy.enable=true`，设置 `HTTPS_PROXY` 指向 mitmproxy
3. 切换到 workspace 目录
4. 执行 init_script
5. 启动 agent_type（如 `kiro-cli chat`）

## 服务端口一览

| 服务 | 端口 | 容器名 | 说明 |
|------|------|--------|------|
| API | 8008 | 本地进程 | Go API 服务 |
| Frontend | 6905 | cicy-code-ide-dev-1 | Vite dev server |
| MySQL | 13306 | cicy-code-mysql | 密码: cicy-code |
| Redis | 16379 | cicy-code-redis | |
| mitmproxy | 18888 | cicy-code-mitmproxy | 代理端口 (proxyauth=any) |
| mitmweb | 18889 | cicy-code-mitmproxy | Web UI |
| code-server | 18080 | cicy-code-server | 通过 API /code/ 代理 |
| phpMyAdmin | 12222 | cicy-code-pma | |
| Redis Commander | 18379 | cicy-code-redis-admin | admin/cicy-code |

## 访问地址

| 服务 | URL |
|------|-----|
| IDE | https://ide.cicy.de5.net |
| API | https://g-8008.cicy.de5.net |
| code-server | https://g-8008.cicy.de5.net/code/?token=TOKEN |
| mitmproxy | https://g-8008.cicy.de5.net/mitm/?token=TOKEN |
| phpMyAdmin | https://g-12222.cicy.de5.net |
| Redis Commander | https://g-18379.cicy.de5.net |

## 重启 API

```bash
tmux send-keys -t w-10001:cicy-api C-c
sleep 1
cd /home/w3c_offical/projects/cicy-code/api
GOROOT=/usr/lib/go CGO_ENABLED=0 go build -o cicy-code-api ./mgr/
tmux send-keys -t w-10001:cicy-api \
  "HOME=/home/w3c_offical PORT=8008 \
   MYSQL_DSN='root:cicy-code@tcp(localhost:13306)/cicy_code' \
   REDIS_ADDR='localhost:16379' \
   ./cicy-code-api" Enter
```

## mitmproxy 流量监控

Workers 通过代理用户名标识流量来源：
```
HTTPS_PROXY=http://w-20147:x@127.0.0.1:18888
```

addon (`kiro_monitor.py`) 记录到 Redis:
- Key: `kiro_http_log` (List, 最多 10000 条)
- Channel: `kiro_traffic_live` (Pub/Sub 实时推送)

查看流量：
```bash
redis-cli -p 16379 LRANGE kiro_http_log 0 10
```
