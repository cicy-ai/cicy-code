#!/bin/bash
set -e

# CiCy 一键部署脚本
# 用法: curl -sSL <url> | bash
# 或:   bash setup.sh

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[CiCy]${NC} $1"; }
warn() { echo -e "${YELLOW}[CiCy]${NC} $1"; }
err()  { echo -e "${RED}[CiCy]${NC} $1"; exit 1; }

# ============ 检查 root ============
[[ $EUID -eq 0 ]] && err "请不要用 root 运行，用普通用户（会自动 sudo）"

PROJECT_DIR="$HOME/projects/cicy-code"
GITHUB_REPO="git@github.com:cicy-dev/cicy-code.git"

# ============ 1. 系统依赖 ============
log "安装系统依赖..."
sudo apt-get update -qq
sudo apt-get install -y -qq docker.io docker-compose-plugin supervisor git curl tmux

# 当前用户加入 docker 组
sudo usermod -aG docker "$USER" 2>/dev/null || true

# ============ 2. Node.js 20 ============
if ! command -v node &>/dev/null; then
  log "安装 Node.js 20..."
  curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
  sudo apt-get install -y -qq nodejs
fi

# ============ 3. Go ============
if ! command -v go &>/dev/null; then
  log "安装 Go..."
  GO_VER=$(curl -s https://go.dev/VERSION?m=text | head -1)
  curl -sSL "https://go.dev/dl/${GO_VER}.linux-amd64.tar.gz" | sudo tar -C /usr/local -xzf -
  echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
  export PATH=$PATH:/usr/local/go/bin
fi

# ============ 4. 克隆代码 ============
if [ ! -d "$PROJECT_DIR" ]; then
  log "克隆代码..."
  mkdir -p "$HOME/projects"
  git clone "$GITHUB_REPO" "$PROJECT_DIR"
else
  warn "代码已存在: $PROJECT_DIR"
fi

cd "$PROJECT_DIR"

# ============ 5. .env ============
if [ ! -f .env ]; then
  log "创建 .env（请修改密码）..."
  cat > .env << 'EOF'
MYSQL_ROOT_PASSWORD=changeme
MYSQL_DATABASE=cicy_code
EOF
  warn "请编辑 .env 修改密码: vim $PROJECT_DIR/.env"
  warn "修改后重新运行此脚本"
  exit 0
else
  log ".env 已存在"
  source .env
fi

# ============ 6. Docker Compose ============
log "启动 Docker 服务..."
sg docker -c "docker compose up -d"

# 等 MySQL 就绪
log "等待 MySQL 就绪..."
for i in $(seq 1 30); do
  if docker exec cicy-mysql mysqladmin ping -h localhost -p"${MYSQL_ROOT_PASSWORD}" &>/dev/null; then
    break
  fi
  sleep 2
done

# ============ 7. 编译 Go API ============
log "编译 Go API..."
cd "$PROJECT_DIR/api"
CGO_ENABLED=0 go build -o cicy-code-api ./mgr/

# ============ 8. Supervisor ============
log "配置 Supervisor..."
sudo tee ${PROJECT_DIR}/cicy-api.supervisor.conf > /dev/null << EOF
[program:cicy-api]
command=${PROJECT_DIR}/api/cicy-code-api
directory=${PROJECT_DIR}/api
environment=TERM="xterm-256color",MYSQL_DSN="root:${MYSQL_ROOT_PASSWORD}@tcp(localhost:${MYSQL_PORT})/cicy_code",REDIS_ADDR="localhost:${REDIS_PORT}",PORT="${API_PORT}",HOME="${HOME}"
user=$(whoami)
autostart=true
autorestart=true
stdout_logfile=/var/log/cicy-api.log
stderr_logfile=/var/log/cicy-api.log
EOF

sudo ln -sf ${PROJECT_DIR}/cicy-api.supervisor.conf /etc/supervisor/conf.d/cicy-api.conf

sudo supervisorctl reread
sudo supervisorctl update

# ============ 9. mitmproxy CA 证书 ============
log "安装 mitmproxy CA 证书..."
for i in $(seq 1 15); do
  docker exec cicy-mitmproxy cat /home/mitmproxy/.mitmproxy/mitmproxy-ca-cert.pem > /tmp/mitmproxy-ca.crt 2>/dev/null && break
  sleep 2
done
if [ -f /tmp/mitmproxy-ca.crt ]; then
  sudo cp /tmp/mitmproxy-ca.crt /usr/local/share/ca-certificates/mitmproxy-ca.crt
  sudo update-ca-certificates
  sudo bash -c 'cat /usr/local/share/ca-certificates/mitmproxy-ca.crt >> /etc/ssl/certs/ca-certificates.crt'
  rm /tmp/mitmproxy-ca.crt
  log "✅ mitmproxy CA 证书已安装"
else
  warn "⚠️ mitmproxy CA 证书安装失败，稍后手动运行:"
  warn "  docker exec cicy-mitmproxy cat /home/mitmproxy/.mitmproxy/mitmproxy-ca-cert.pem | sudo tee /usr/local/share/ca-certificates/mitmproxy-ca.crt"
  warn "  sudo update-ca-certificates"
fi

# ============ 10. 验证 ============
log "验证服务..."
sleep 3
echo ""
for p in 3306 6379 8000 8001 8002 8003 8008; do
  CODE=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$p/" 2>/dev/null || echo "---")
  printf "  :%s  %s\n" "$p" "$CODE"
done

echo ""
log "✅ 部署完成！"
echo ""
echo "  端口说明:"
echo "    8000  Nginx (前端 dist)"
echo "    8001  Vite dev server"
echo "    8002  code-server"
echo "    8003  mitmdump 代理"
echo "    8008  Go API"
echo ""
echo "  管理命令:"
echo "    sudo supervisorctl restart cicy-api    # 重启 API"
echo "    docker compose up -d                   # 重启 Docker 服务"
echo "    tail -f /var/log/cicy-api.log          # API 日志"
echo ""
echo "  下一步:"
echo "    1. 安装 cloudflared tunnel"
echo "    2. 安装 1Panel: curl -sSL https://resource.fit2cloud.com/1panel/package/quick_start.sh | sudo bash"
