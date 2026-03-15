# 开发环境修复记录 (2026-03-15)

## 1. refreshCfgCache 缺少 ttyd_port 和 workspace

**现象:** watcher 恢复 ttyd instance 时不触发，端口不监听

**原因:** `refreshCfgCache()` 的 SQL 只查了 6 个字段，没查 `ttyd_port` 和 `workspace`，导致 `cfg["ttyd_port"]` 始终为空

**修复:** SQL 加上 `COALESCE(ttyd_port,0), COALESCE(workspace,'')`, Scan 加对应变量，写入 cfgCache

**文件:** `api/mgr/watcher.go` — `refreshCfgCache()`

---

## 2. supervisor 缺少 TERM 环境变量

**现象:** ttyd 端口在监听，前端连上后立刻断开。日志: `Connection closed by local command`

**原因:** supervisor 启动的 cicy-api 进程没有 `TERM` 环境变量，gotty 内部 spawn 的 `tmux attach` 因为没有 TERM 立刻退出

**修复:** `/etc/supervisor/conf.d/cicy-api.conf` 的 `environment` 加 `TERM="xterm-256color"`

**文件:** `/etc/supervisor/conf.d/cicy-api.conf`

---

## 3. proxy 端口和认证

**现象:** Kiro CLI 报 `dispatch failure: io error` 连不上 AWS

**原因:** 
- 旧端口 18888 → 新端口 8003，DB 里没更新
- mitmproxy 配置了 `--set proxyauth=any`，proxy URL 必须带用户名密码
- proxy URL 用户名应为 pane session 名（如 `w-10001`），用于 mitmproxy 区分流量来源

**修复:**
- DB: `UPDATE agent_config SET proxy='http://{session}:x@127.0.0.1:8003'`
- 代码: `handleCreatePane` 默认 proxy 改为 `fmt.Sprintf("http://%s:x@127.0.0.1:8003", session)`
- Master pane (w-10001) 不设 proxy，避免 Kiro CLI 流量走 mitmproxy

**文件:** `api/mgr/tmux.go` — `handleCreatePane()`

---

## 4. mitmproxy CA 证书未安装

**现象:** 通过 proxy 的 HTTPS 请求 SSL 验证失败，Kiro CLI (rustls) 连不上 AWS

**原因:** mitmproxy 做 HTTPS 中间人需要系统信任其 CA 证书。`update-ca-certificates` 把证书放到 `/etc/ssl/certs/` 目录但不追加到 `ca-certificates.crt` bundle 文件。rustls 读的是 bundle 文件。

**修复:**
```bash
sudo bash -c 'docker exec cicy-mitmproxy cat /home/mitmproxy/.mitmproxy/mitmproxy-ca-cert.pem > /usr/local/share/ca-certificates/mitmproxy-ca.crt'
sudo update-ca-certificates
sudo bash -c 'cat /usr/local/share/ca-certificates/mitmproxy-ca.crt >> /etc/ssl/certs/ca-certificates.crt'
```

已写入 setup.sh 自动执行。

---

## 5. Redis 容器端口不匹配

**现象:** API 启动后 Redis 连接失败

**原因:** 旧 docker-compose 里 Redis 映射到 16379，但新 supervisor 配置用 `REDIS_ADDR=localhost:6379`

**修复:** docker-compose 里 Redis 改为 `network_mode: host` 或端口映射 `6379:6379`，与 supervisor 的 `REDIS_ADDR` 一致

**文件:** `docker-compose.yml`, `cicy-api.supervisor.conf`

---

## 6. nodeTmux 依赖 xui（之前修复）

**现象:** 创建/恢复 tmux session 失败

**原因:** `nodeURL()` 在 DB `node_url` 为空时返回 xui 地址 `http://localhost:13431`，本地环境没有 xui

**修复:** `nodeURL()` 返回空字符串，`nodeTmux()` 和 watcher 回退到本地 `exec.Command("tmux", ...)`

**文件:** `api/mgr/node.go`, `api/mgr/watcher.go`, `api/mgr/tmux.go`

---

## 6. useEffect 无限渲染循环（之前修复）

**现象:** 前端 Workspace 页面卡死

**原因:** `useEffect([pos])` 内部调用 `setPos`，触发无限循环

**修复:** 改为 `useEffect([])` + `requestAnimationFrame`

**文件:** `ide/src/components/Workspace.tsx`
