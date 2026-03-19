#!/bin/bash
# CiCy VM 快速部署 - 镜像已预装所有依赖
# 所有配置文件从 projects/cicy-code 读取，不 hardcode
# Usage: sudo bash setup-fast.sh [CF_TUNNEL_TOKEN]
set -e

CF_TUNNEL_TOKEN="${1:-}"
USER="w3c_offical"
HOME_DIR="/home/$USER"
CICY_DIR="$HOME_DIR/projects/cicy-code"
HOST_UID=$(id -u "$USER")
HOST_GID=$(id -g "$USER")

echo "[1/6] Cloudflare Tunnel"
if [ -n "$CF_TUNNEL_TOKEN" ]; then
    cloudflared service install "$CF_TUNNEL_TOKEN" 2>/dev/null || true
    systemctl enable --now cloudflared
fi

echo "[2/6] 目录 + 配置"
mkdir -p "$HOME_DIR/workers/w-10001/projects"
ln -sfn "$CICY_DIR" "$HOME_DIR/workers/w-10001/projects/cicy-code"
ln -sf "$CICY_DIR/.tmux.conf" "$HOME_DIR/.tmux.conf"

# global.json
if [ ! -f "$HOME_DIR/global.json" ]; then
    API_TOKEN=$(openssl rand -hex 16)
    echo "{\"api_token\": \"$API_TOKEN\"}" > "$HOME_DIR/global.json"
    chmod 600 "$HOME_DIR/global.json"
fi
chown "$USER:$USER" "$HOME_DIR/global.json"

echo "[3/6] Docker"
cd "$CICY_DIR"
rm -f "$HOME_DIR/.local/share/code-server/coder.json"
HOST_UID=$HOST_UID HOST_GID=$HOST_GID HOST_HOME=$HOME_DIR \
    docker compose -f docker-compose.saas.yml up -d --build 2>&1 | tail -3

echo "[4/6] Supervisor"
cp "$CICY_DIR/cicy-api.supervisor.conf" /etc/supervisor/conf.d/cicy-api.conf
cp "$CICY_DIR/code-server.supervisor.conf" /etc/supervisor/conf.d/code-server.conf
supervisorctl reread && supervisorctl update
supervisorctl restart cicy-api 2>/dev/null || supervisorctl start cicy-api

echo "[5/6] tmux"
sudo -u "$USER" tmux kill-session -t w-10001 2>/dev/null || true
sudo -u "$USER" tmux new-session -d -s w-10001 -n main -c "$HOME_DIR"

echo "[6/6] MySQL 初始化"
for i in {1..30}; do
    docker exec cicy-mysql mysql -u root -p'cicy-code' -e "SELECT 1" &>/dev/null && break
    sleep 1
done
[ -f /tmp/schema.sql ] && docker exec -i cicy-mysql mysql -u root -p'cicy-code' cicy_code < /tmp/schema.sql 2>/dev/null

chown -R "$USER:$USER" "$HOME_DIR/projects" "$HOME_DIR/workers"

echo "=== 完成 ==="
supervisorctl status
docker ps --format 'table {{.Names}}\t{{.Status}}'
