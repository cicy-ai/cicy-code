#!/bin/bash
# cicy-code 一键启动/停止脚本
set -e

ACTION=${1:-start}
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Load .env
[ -f "$SCRIPT_DIR/.env" ] && export $(grep -v '^#' "$SCRIPT_DIR/.env" | xargs)

# Ports from .env
API_PORT=${API_PORT:-8008}
MYSQL_PORT=${MYSQL_PORT:-3306}
REDIS_PORT=${REDIS_PORT:-6379}
MITMPROXY_PORT=${MITMPROXY_PORT:-8003}
CODE_SERVER_PORT=${CODE_SERVER_PORT:-8002}
NGINX_PORT=${NGINX_PORT:-8000}
VITE_PORT=${VITE_PORT:-8001}

# Colors
G='\033[0;32m' R='\033[0;31m' Y='\033[0;33m' N='\033[0m'
ok() { echo -e "${G}✓${N} $1"; }
fail() { echo -e "${R}✗${N} $1"; }
info() { echo -e "${Y}→${N} $1"; }

# === Docker 服务 ===
docker_up() {
    info "Starting Docker services..."
    cd "$SCRIPT_DIR" && docker compose up -d
    ok "Docker services"
}

docker_down() {
    info "Stopping Docker services..."
    cd "$SCRIPT_DIR" && docker compose down
    ok "Docker services stopped"
}

# === API (supervisor) ===
api_start() {
    info "Starting cicy-code-api on :${API_PORT}..."
    sudo supervisorctl start cicy-api 2>/dev/null || sudo supervisorctl restart cicy-api
    sleep 2
    if curl -s "http://localhost:${API_PORT}/health" > /dev/null 2>&1; then
        ok "cicy-code-api :${API_PORT}"
    else
        fail "cicy-code-api failed, check: sudo supervisorctl tail cicy-api stderr"
    fi
}

api_stop() {
    sudo supervisorctl stop cicy-api 2>/dev/null && ok "cicy-code-api stopped" || info "cicy-code-api not running"
}

# === Status ===
status() {
    echo "=== Services Status ==="
    for name_port in "cicy-mysql:${MYSQL_PORT}" "cicy-redis:${REDIS_PORT}" "cicy-mitmproxy:${MITMPROXY_PORT}" "cicy-code-server:${CODE_SERVER_PORT}" "cicy-nginx:${NGINX_PORT}" "cicy-ide-dev:${VITE_PORT}"; do
        name="${name_port%%:*}"
        port="${name_port##*:}"
        if docker ps --format '{{.Names}}' | grep -q "^${name}$"; then
            ok "$name :$port"
        else
            fail "$name"
        fi
    done
    sudo supervisorctl status cicy-api 2>/dev/null | grep -q RUNNING && ok "cicy-code-api :${API_PORT}" || fail "cicy-code-api"
}

case "$ACTION" in
    start)
        echo "=== Starting all services ==="
        docker_up
        api_start
        echo ""
        status
        ;;
    stop)
        echo "=== Stopping all services ==="
        api_stop
        docker_down
        ;;
    restart)
        $0 stop
        sleep 2
        $0 start
        ;;
    status)
        status
        ;;
    api)
        api_stop; sleep 1; api_start
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|status|api}"
        ;;
esac
