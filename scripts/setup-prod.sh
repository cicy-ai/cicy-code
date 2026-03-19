#!/bin/bash
# ============================================================
# CiCy SaaS 生产部署 (API 二进制 + Docker 数据服务)
# Usage: sudo bash setup-prod.sh [CF_TUNNEL_TOKEN]
# ============================================================
set -e

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✅ $1${NC}"; }
warn() { echo -e "${YELLOW}⚠️  $1${NC}"; }
step() { echo -e "\n${GREEN}[$1/$TOTAL] $2${NC}"; }

TOTAL=6
CF_TUNNEL_TOKEN="${1:-}"       # 由 prod API 生成并传入
DB_MODE="${2:-sqlite}"         # sqlite (默认) 或 mysql

USER="${SUDO_USER:-$(whoami)}"
HOME_DIR=$(getent passwd "$USER" | cut -d: -f6)
[ -z "$HOME_DIR" ] && HOME_DIR="/home/$USER"
USER_GROUP=$(id -gn "$USER")
DEPLOY_DIR="$HOME_DIR/cicy"
API_BIN="$DEPLOY_DIR/cicy-code-api"

echo "============================================"
echo "  🚀 CiCy SaaS 生产部署"
echo "  User: $USER  Home: $HOME_DIR"
echo "============================================"

# ── 1. 系统依赖 ──
step 1 "安装系统依赖"
apt-get update -qq
apt-get install -y -qq docker.io docker-compose-v2 supervisor tmux curl jq xclip > /dev/null 2>&1
usermod -aG docker "$USER" 2>/dev/null || true
systemctl enable --now docker
ok "done"

# ── 2. Cloudflare Tunnel ──
step 2 "Cloudflare Tunnel"
if ! command -v cloudflared &>/dev/null; then
    curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb -o /tmp/cloudflared.deb
    dpkg -i /tmp/cloudflared.deb && rm /tmp/cloudflared.deb
fi
if [ -n "$CF_TUNNEL_TOKEN" ]; then
    cloudflared service install "$CF_TUNNEL_TOKEN" 2>/dev/null || true
    systemctl enable --now cloudflared
    ok "cloudflared running"
elif systemctl is-active --quiet cloudflared 2>/dev/null; then
    ok "cloudflared already running"
else
    warn "No tunnel token. Usage: sudo bash setup-prod.sh <TOKEN>"
fi

# ── 3. 配置文件 ──
step 3 "生成配置文件"
mkdir -p "$DEPLOY_DIR/nginx" "$DEPLOY_DIR/initdb"
chown "$USER:$USER_GROUP" "$DEPLOY_DIR"

cat > "$DEPLOY_DIR/.env" << 'EOF'
MYSQL_ROOT_PASSWORD=cicy-code
MYSQL_DATABASE=cicy_code
EOF

# 用户专属配置 (仅用户可读)
if [ ! -f "$HOME_DIR/global.json" ]; then
    API_TOKEN=$(openssl rand -hex 16)
    cat > "$HOME_DIR/global.json" << EOF
{
  "api_token": "$API_TOKEN"
}
EOF
    chown "$USER:$USER_GROUP" "$HOME_DIR/global.json"
    chmod 600 "$HOME_DIR/global.json"
    ok "api_token generated (only $USER can read)"
else
    ok "global.json exists, skipping"
fi
# token 只存在用户 VM 上，不回传、不记录
unset API_TOKEN

# MySQL init schema
cat > "$DEPLOY_DIR/initdb/init.sql" << 'SQL'
CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(36) PRIMARY KEY,
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    plan VARCHAR(20) DEFAULT 'free',
    daily_calls INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS desktop_apps (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    label VARCHAR(100),
    emoji VARCHAR(10),
    url TEXT,
    type VARCHAR(20) DEFAULT 'icon',
    size VARCHAR(10),
    srcdoc MEDIUMTEXT,
    position INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_user (user_id)
);

CREATE TABLE IF NOT EXISTS plugin_bindings (
    user_id VARCHAR(36) NOT NULL,
    plugin_name VARCHAR(50) NOT NULL,
    access_token TEXT,
    refresh_token TEXT,
    token_expiry TIMESTAMP NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, plugin_name)
);

CREATE TABLE IF NOT EXISTS workspaces (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    vm_name VARCHAR(100) UNIQUE NOT NULL,
    zone VARCHAR(50),
    ip VARCHAR(45),
    tunnel_id VARCHAR(100),
    status VARCHAR(20) DEFAULT 'provisioning',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_user (user_id)
);

CREATE TABLE IF NOT EXISTS http_logs (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    method VARCHAR(10),
    path VARCHAR(500),
    status INT,
    duration_ms INT,
    user_id VARCHAR(36),
    ip VARCHAR(45),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_created (created_at),
    INDEX idx_user (user_id)
);
SQL

cat > "$DEPLOY_DIR/nginx/nginx.conf" << 'NGINX'
events { worker_connections 1024; }
http {
    include /etc/nginx/mime.types;
    sendfile on; gzip on;
    server {
        listen 8000;
        root /usr/share/nginx/html;
        index index.html;
        location / { try_files $uri $uri/ /index.html; }
        location /api/ { proxy_pass http://127.0.0.1:8008; proxy_http_version 1.1; proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection "upgrade"; }
    }
}
NGINX

if [ "$DB_MODE" = "mysql" ]; then
cat > "$DEPLOY_DIR/docker-compose.yml" << 'EOF'
services:
  mysql:
    image: mysql:8.0
    container_name: cicy-mysql
    restart: always
    network_mode: host
    environment:
      MYSQL_ROOT_PASSWORD: ${MYSQL_ROOT_PASSWORD}
      MYSQL_DATABASE: ${MYSQL_DATABASE}
    volumes:
      - mysql-data:/var/lib/mysql
      - ./initdb:/docker-entrypoint-initdb.d:ro
    command: --default-authentication-plugin=mysql_native_password --innodb-buffer-pool-size=256M
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "localhost", "-p${MYSQL_ROOT_PASSWORD}"]
      interval: 10s
      timeout: 5s
      retries: 10

  redis:
    image: redis:7-alpine
    container_name: cicy-redis
    restart: always
    network_mode: host
    volumes:
      - redis-data:/data
    command: redis-server --appendonly yes
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

  nginx:
    image: nginx:alpine
    container_name: cicy-nginx
    restart: always
    network_mode: host
    volumes:
      - ./nginx/nginx.conf:/etc/nginx/nginx.conf:ro

volumes:
  mysql-data:
  redis-data:
EOF
else
cat > "$DEPLOY_DIR/docker-compose.yml" << 'EOF'
services:
  redis:
    image: redis:7-alpine
    container_name: cicy-redis
    restart: always
    network_mode: host
    volumes:
      - redis-data:/data
    command: redis-server --appendonly yes

  nginx:
    image: nginx:alpine
    container_name: cicy-nginx
    restart: always
    network_mode: host
    volumes:
      - ./nginx/nginx.conf:/etc/nginx/nginx.conf:ro

volumes:
  redis-data:
EOF
fi

chown -R "$USER:$USER_GROUP" "$DEPLOY_DIR"
ok "$DEPLOY_DIR"

# ── 4. Docker 数据服务 + API 二进制 ──
step 4 "启动服务"
cd "$DEPLOY_DIR"
sudo -u "$USER" docker compose pull -q 2>&1
sudo -u "$USER" docker compose up -d 2>&1 | tail -3

# 等 MySQL healthy
echo -n "  Waiting for MySQL..."
for i in $(seq 1 30); do
    if docker exec cicy-mysql mysqladmin ping -h localhost -p'cicy-code' &>/dev/null 2>&1; then
        echo " ready"; break
    fi
    echo -n "."; sleep 2
done

# 从 Docker Hub 提取 API 二进制
if [ ! -f "$API_BIN" ] || [ "${FORCE_UPDATE:-}" = "1" ]; then
    docker pull cicybot/cicy-api:latest -q 2>&1
    docker create --name cicy-api-tmp cicybot/cicy-api:latest 2>/dev/null
    docker cp cicy-api-tmp:/usr/local/bin/cicy-code-api "$API_BIN"
    docker rm cicy-api-tmp > /dev/null
    chmod +x "$API_BIN"
    chown "$USER:$USER_GROUP" "$API_BIN"
    ok "API binary extracted from cicybot/cicy-api:latest"
else
    ok "API binary exists"
fi

# Supervisor
if [ "$DB_MODE" = "mysql" ]; then
  API_ENV='PORT="8008",DB_DRIVER="mysql",DB_DSN="root:cicy-code@tcp(127.0.0.1:3306)/cicy_code?parseTime=true",REDIS_ADDR="127.0.0.1:6379",HOME="'"$HOME_DIR"'"'
else
  API_ENV='PORT="8008",DB_DRIVER="sqlite",DB_DSN="'"$DEPLOY_DIR/cicy.db"'",REDIS_ADDR="127.0.0.1:6379",HOME="'"$HOME_DIR"'"'
fi

cat > /etc/supervisor/conf.d/cicy-api.conf << SUPEOF
[program:cicy-api]
command=$API_BIN
directory=$DEPLOY_DIR
environment=$API_ENV
user=$USER
autostart=true
autorestart=true
stdout_logfile=/var/log/cicy-api.log
stderr_logfile=/var/log/cicy-api.log
SUPEOF

supervisorctl reread > /dev/null
supervisorctl update > /dev/null
supervisorctl restart cicy-api 2>/dev/null || supervisorctl start cicy-api
sleep 2
ok "API managed by supervisor"

# ── 5. tmux ──
step 5 "tmux 工作区"
cat > "$HOME_DIR/.tmux.conf" << 'TMUX'
set -g default-terminal "tmux-256color"
set -g mouse on
set-option -g window-size largest
set-option -g aggressive-resize on
TMUX
chown "$USER:$USER_GROUP" "$HOME_DIR/.tmux.conf"
sudo -u "$USER" tmux has-session -t w-10001 2>/dev/null || \
    sudo -u "$USER" tmux new-session -d -s w-10001 -n main -c "$HOME_DIR"
ok "tmux w-10001"

# ── 6. 验证 ──
step 6 "验证"
echo ""
for svc in cicy-mysql cicy-redis cicy-nginx; do
    st=$(docker inspect -f '{{.State.Status}}' "$svc" 2>/dev/null || echo "missing")
    printf "  %-14s %s\n" "$svc" "$st"
done
api_st=$(supervisorctl status cicy-api 2>/dev/null | awk '{print $2}')
printf "  %-14s %s (supervisor)\n" "cicy-api" "$api_st"

echo ""
if curl -sf http://localhost:8008/api/health > /dev/null 2>&1; then
    ok "API health check passed"
else
    warn "API not ready — check: supervisorctl status cicy-api"
fi

echo ""
echo "============================================"
echo -e "  ${GREEN}🎉 部署完成！${NC}"
echo "============================================"
echo "  API:   supervisor → localhost:8008"
echo "  MySQL: docker     → localhost:3306"
echo "  Redis: docker     → localhost:6379"
echo "  Nginx: docker     → localhost:8000"
echo ""
echo "  更新 API: docker pull cicybot/cicy-api:latest"
echo "            FORCE_UPDATE=1 sudo bash setup-prod.sh"
echo ""
