#!/bin/bash
# ============================================================
# CiCy SaaS 一键部署脚本
# 目标: 新 GCP VM (Ubuntu 22.04) 跑一次就能部署完整环境
# Usage: bash setup.sh [CF_TUNNEL_TOKEN]
# ============================================================
set -e

# ── 颜色 ──
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✅ $1${NC}"; }
warn() { echo -e "${YELLOW}⚠️  $1${NC}"; }
fail() { echo -e "${RED}❌ $1${NC}"; exit 1; }
step() { echo -e "\n${GREEN}[$1/$TOTAL] $2${NC}"; }

TOTAL=8
CF_TOKEN="${1:-}"
USER="${SUDO_USER:-$(whoami)}"
HOME_DIR=$(getent passwd "$USER" | cut -d: -f6)
if [ -z "$HOME_DIR" ]; then HOME_DIR="/home/$USER"; fi
PROJECT_DIR="$HOME_DIR/projects/cicy-code"
API_BIN="$PROJECT_DIR/api/cicy-code-api"
USER_GROUP=$(id -gn "$USER")

echo "============================================"
echo "  🚀 CiCy SaaS 一键部署"
echo "  OS:   $(lsb_release -ds 2>/dev/null || cat /etc/os-release | head -1)"
echo "  User: $USER"
echo "  Home: $HOME_DIR"
echo "============================================"

# ── 1. 系统依赖 ──
step 1 "安装系统依赖"
apt-get update -qq
apt-get install -y -qq \
    docker.io docker-compose-v2 \
    supervisor tmux git curl jq \
    xclip > /dev/null 2>&1

# Docker 权限
usermod -aG docker "$USER" 2>/dev/null || true
systemctl enable --now docker

# Go (snap)
if ! command -v go &>/dev/null; then
    snap install go --classic
fi

# Node.js 20
if ! command -v node &>/dev/null; then
    curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
    apt-get install -y -qq nodejs > /dev/null 2>&1
fi

ok "docker=$(docker --version | grep -oP '[\d.]+' | head -1) go=$(go version | grep -oP '[\d.]+' | head -1) node=$(node --version)"

# ── 2. Cloudflare Tunnel ──
step 2 "配置 Cloudflare Tunnel"
if ! command -v cloudflared &>/dev/null; then
    curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb -o /tmp/cloudflared.deb
    dpkg -i /tmp/cloudflared.deb
    rm /tmp/cloudflared.deb
fi

if [ -n "$CF_TOKEN" ]; then
    # 安装为 systemd 服务
    cloudflared service install "$CF_TOKEN" 2>/dev/null || true
    systemctl enable --now cloudflared
    ok "cloudflared installed with token"
elif systemctl is-active --quiet cloudflared 2>/dev/null; then
    ok "cloudflared already running"
else
    warn "cloudflared not configured. Run later: cloudflared service install <TOKEN>"
fi

# ── 3. 克隆代码 ──
step 3 "克隆代码"
mkdir -p "$HOME_DIR/projects"
chown "$USER:$USER_GROUP" "$HOME_DIR/projects"
if [ -d "$PROJECT_DIR/.git" ]; then
    cd "$PROJECT_DIR" && sudo -u "$USER" git pull --ff-only 2>/dev/null || true
    ok "code updated"
else
    sudo -u "$USER" git clone git@github.com:cicy-dev/cicy-code.git "$PROJECT_DIR" 2>/dev/null || \
    sudo -u "$USER" git clone https://github.com/cicy-dev/cicy-code.git "$PROJECT_DIR" 2>/dev/null || {
        warn "git clone failed — 需要配置 SSH key 或仓库权限"
        mkdir -p "$PROJECT_DIR"
        chown "$USER:$USER_GROUP" "$PROJECT_DIR"
    }
    ok "code cloned"
fi

# ── 4. 配置 .env ──
step 4 "配置环境变量"
ENV_FILE="$PROJECT_DIR/.env"
if [ ! -f "$ENV_FILE" ]; then
    cat > "$ENV_FILE" << 'EOF'
# CiCy 统一配置
MYSQL_ROOT_PASSWORD=cicy-code
MYSQL_DATABASE=cicy_code
MYSQL_PORT=3306
REDIS_PORT=6379
API_PORT=8008
NGINX_PORT=8000
VITE_PORT=8001
CODE_SERVER_PORT=8002
MITMPROXY_PORT=8003
HOST_HOME=${HOME_DIR}
HOST_UID=${HOST_UID}
HOST_GID=${HOST_GID}
EOF
    sed -i "s|\${HOME_DIR}|$HOME_DIR|" "$ENV_FILE"
    sed -i "s|\${HOST_UID}|$(id -u $USER)|" "$ENV_FILE"
    sed -i "s|\${HOST_GID}|$(id -g $USER)|" "$ENV_FILE"
    chown "$USER:$USER_GROUP" "$ENV_FILE"
    ok ".env created"
else
    ok ".env exists"
fi

# ── 5. Docker 服务 ──
step 5 "启动 Docker 服务 (MySQL + Redis + Nginx)"
if [ -f "$PROJECT_DIR/docker-compose.yml" ]; then
    cd "$PROJECT_DIR"
    sudo -u "$USER" docker compose up -d mysql redis nginx 2>&1 | tail -3

    # 等待 MySQL 就绪
    echo -n "  Waiting for MySQL..."
    for i in $(seq 1 30); do
        if docker exec cicy-mysql mysqladmin ping -h localhost -p'cicy-code' &>/dev/null; then
            echo " ready"
            break
        fi
        echo -n "."
        sleep 2
    done
    ok "Docker services running"
else
    warn "docker-compose.yml not found — skipping Docker services"
fi

# ── 6. 编译 Go API ──
step 6 "编译 Go API"
if [ -f "$PROJECT_DIR/api/mgr/main.go" ]; then
    cd "$PROJECT_DIR/api"
    sudo -u "$USER" bash -c "cd $PROJECT_DIR/api && go build -o cicy-code-api ./mgr/" 2>&1
    ok "API compiled: $API_BIN"
else
    warn "API source not found at $PROJECT_DIR/api — skipping build"
fi

# ── 7. Supervisor 配置 ──
step 7 "配置 Supervisor"
if [ -f "$API_BIN" ]; then
    cat > /etc/supervisor/conf.d/cicy-api.conf << EOF
[program:cicy-api]
command=$API_BIN
directory=$PROJECT_DIR/api
environment=TERM="xterm-256color",MYSQL_DSN="root:cicy-code@tcp(localhost:3306)/cicy_code",REDIS_ADDR="localhost:6379",PORT="8008",HOME="$HOME_DIR"
user=$USER
autostart=true
autorestart=true
stdout_logfile=/var/log/cicy-api.log
stderr_logfile=/var/log/cicy-api.log
EOF

supervisorctl reread
supervisorctl update
supervisorctl restart cicy-api 2>/dev/null || supervisorctl start cicy-api
sleep 2

# 验证
if curl -sf http://localhost:8008/api/health > /dev/null 2>&1; then
    ok "API running on :8008"
else
    warn "API may still be starting, check: supervisorctl status cicy-api"
fi
else
    warn "API binary not found — skipping Supervisor setup"
fi

# ── 8. tmux + 工作区 ──
step 8 "初始化 tmux 工作区"

# tmux 配置
cat > "$HOME_DIR/.tmux.conf" << 'TMUX'
set -g default-terminal "tmux-256color"
set -g mouse on
bind-key -T copy-mode-vi MouseDragEnd1Pane send-keys -X copy-pipe-and-cancel "xclip -selection clipboard -i"
bind-key -T copy-mode MouseDragEnd1Pane send-keys -X copy-pipe-and-cancel "xclip -selection clipboard -i"
set-option -g window-size largest
set-option -g aggressive-resize on
TMUX
chown "$USER:$USER_GROUP" "$HOME_DIR/.tmux.conf"

# 创建 master agent session
sudo -u "$USER" tmux has-session -t w-10001 2>/dev/null || \
    sudo -u "$USER" tmux new-session -d -s w-10001 -n main -c "$HOME_DIR"
ok "tmux session w-10001 ready"

# ── 完成 ──
echo ""
echo "============================================"
echo -e "  ${GREEN}🎉 CiCy SaaS 部署完成！${NC}"
echo "============================================"
echo ""
echo "  服务状态:"
echo "    API:       http://localhost:8008/api/health"
echo "    MySQL:     localhost:3306"
echo "    Redis:     localhost:6379"
echo "    Nginx:     http://localhost:8000"
echo ""
echo "  管理命令:"
echo "    supervisorctl status              # 查看 API 状态"
echo "    supervisorctl restart cicy-api    # 重启 API"
echo "    docker compose ps                 # 查看 Docker 服务"
echo "    tmux attach -t w-10001            # 进入工作区"
echo ""
if ! systemctl is-active --quiet cloudflared 2>/dev/null; then
    echo -e "  ${YELLOW}⚠️  CF Tunnel 未配置，需要手动运行:${NC}"
    echo "    cloudflared service install <YOUR_TUNNEL_TOKEN>"
    echo ""
fi
echo "  域名 (配置 CF Tunnel 后):"
echo "    api.cicy-ai.com  → localhost:8008"
echo "    app.cicy-ai.com  → localhost:8000"
echo ""
