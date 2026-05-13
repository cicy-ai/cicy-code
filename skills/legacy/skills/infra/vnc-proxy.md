# vnc-proxy Service

VNC 代理服务,提供远程桌面访问功能。

## 服务信息

- **公网地址:** https://g-vnc.cicy.de5.net
- **Server 端口:** 13446
- **Frontend 端口:** 13447
- **项目路径:** ~/projects/vnc-proxy
- **管理方式:** Docker Compose

## 快速操作

### 健康检查
```bash
vnc-proxy-check                   # 自动检查服务状态
```

### 服务控制
```bash
cd ~/projects/vnc-proxy

# 启动
sudo docker compose up -d

# 重启
sudo docker compose restart

# 停止
sudo docker compose down

# 查看状态
sudo docker compose ps
```

### 查看日志
```bash
cd ~/projects/vnc-proxy
sudo docker compose logs -f server    # 后端日志
sudo docker compose logs -f frontend  # 前端日志
sudo docker compose logs -f           # 所有日志
```

### 测试访问
```bash
# 公网访问
curl https://g-vnc.cicy.de5.net

# 本地访问
curl http://localhost:13447  # Frontend
curl http://localhost:13446  # Server API
```

## 技术栈

- **后端:** Node.js + TypeScript + tsx watch (热重载)
- **前端:** Vite + HMR
- **容器:** Docker Compose
- **VNC:** X11 + Docker socket

## 开发模式

### 热重载支持
- 后端: 修改 `server/src/**/*.ts` 自动重启 (tsx watch)
- 前端: Vite HMR 即时更新
- 无需重启容器,代码修改自动生效

## 故障排查

### 服务无响应
```bash
vnc-proxy-check                   # 自动诊断
```

### 手动重启
```bash
cd ~/projects/vnc-proxy
sudo docker compose restart
```

### 查看容器状态
```bash
cd ~/projects/vnc-proxy
sudo docker compose ps
sudo docker compose logs --tail=50
```

### 端口占用检查
```bash
ss -tlnp | grep 13446
ss -tlnp | grep 13447
```

### 重新构建
```bash
cd ~/projects/vnc-proxy
sudo docker compose down
sudo docker compose up --build -d
```

## 项目结构

```
~/projects/vnc-proxy/
├── server/              # 后端服务
│   ├── src/            # TypeScript 源码
│   └── Dockerfile.dev  # 开发环境
├── frontend/           # 前端界面
│   └── Dockerfile.dev  # 开发环境
├── docker-compose.yml  # Docker 配置
└── README.md           # 项目文档
```

## 相关技能

- `cf-tunnel.sh` - Cloudflare 隧道管理
- `check-all` - 统一服务检查

## 注意事项

- ⚠️ **项目支持热重载 (tsx watch + Vite HMR),非必要不要重启容器**
- 后端和前端代码修改会自动生效,无需重启
- 遇到问题优先查看日志: `cd ~/projects/vnc-proxy && sudo docker compose logs -f`
- 服务由 Docker Compose 管理
- 需要访问 X11 和 Docker socket
- 使用 host 网络模式
