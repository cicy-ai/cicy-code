#!/bin/bash
# cicy-code 一键启动/停止脚本
set -e

ACTION=${1:-start}
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Colors
G='\033[0;32m' R='\033[0;31m' Y='\033[0;33m' N='\033[0m'
ok() { echo -e "${G}✓${N} $1"; }
fail() { echo -e "${R}✗${N} $1"; }
info() { echo -e "${Y}→${N} $1"; }

# === Docker 基础服务 ===
docker_up() {
    info "Starting Docker services (mysql, redis, phpmyadmin)..."
    cd ~/projects/docker/docker-prod && docker compose up -d
    ok "Docker services"
}

docker_down() {
    info "Stopping Docker services..."
    cd ~/projects/docker/docker-prod && docker compose down
    ok "Docker services stopped"
}

# === cicy-code-api ===
api_start() {
    pkill -f "cicy-code-api" 2>/dev/null || true
    sleep 1
    info "Starting cicy-code-api on :14446..."
    cd "$SCRIPT_DIR/api"
    export MYSQL_DSN="root:pb200898@tcp(localhost:3306)/cicy_code"
    export PORT=14446
    setsid ./cicy-code-api >> /tmp/cicy-code-api.log 2>&1 < /dev/null &
    sleep 2
    if curl -s http://localhost:14446/health > /dev/null 2>&1; then
        ok "cicy-code-api :14446"
    else
        fail "cicy-code-api failed to start, check /tmp/cicy-code-api.log"
    fi
}

api_stop() {
    pkill -f "cicy-code-api" 2>/dev/null && ok "cicy-code-api stopped" || info "cicy-code-api not running"
}

# === mitmproxy ===
mitm_start() {
    if pgrep -f "mitmdump -p 8888" > /dev/null; then
        ok "mitmproxy already running"
        return
    fi
    info "Starting mitmproxy on :8888..."
    setsid mitmdump -p 8888 --ssl-insecure --set block_global=false --set proxyauth=any \
        -s ~/scripts/kiro_monitor.py >> /tmp/mitmproxy.log 2>&1 < /dev/null &
    sleep 1
    ok "mitmproxy :8888"
}

mitm_stop() {
    pkill -f "mitmdump -p 8888" 2>/dev/null && ok "mitmproxy stopped" || info "mitmproxy not running"
}

# === code-server ===
code_start() {
    if pgrep -f "code-server" > /dev/null; then
        ok "code-server already running"
        return
    fi
    info "Starting code-server..."
    setsid code-server >> /tmp/code-server.log 2>&1 < /dev/null &
    sleep 1
    ok "code-server"
}

code_stop() {
    pkill -f "code-server" 2>/dev/null && ok "code-server stopped" || info "code-server not running"
}

# === IDE (docker) ===
ide_start() {
    info "Starting IDE dev server..."
    cd "$SCRIPT_DIR" && docker compose up -d
    ok "IDE dev server"
}

ide_stop() {
    cd "$SCRIPT_DIR" && docker compose down
    ok "IDE stopped"
}

# === Status ===
status() {
    echo "=== Services Status ==="
    # Docker
    for svc in prod-mysql prod-redis prod-phpmyadmin; do
        if docker ps --format '{{.Names}}' | grep -q "^${svc}$"; then
            ok "$svc"
        else
            fail "$svc"
        fi
    done
    # Host services
    pgrep -f "cicy-code-api" > /dev/null && ok "cicy-code-api" || fail "cicy-code-api"
    pgrep -f "mitmdump -p 8888" > /dev/null && ok "mitmproxy" || fail "mitmproxy"
    pgrep -f "code-server" > /dev/null && ok "code-server" || fail "code-server"
}

case "$ACTION" in
    start)
        echo "=== Starting all services ==="
        docker_up
        api_start
        mitm_start
        code_start
        ide_start
        echo ""
        status
        ;;
    stop)
        echo "=== Stopping all services ==="
        api_stop
        mitm_stop
        code_stop
        ide_stop
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
        api_stop; api_start
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|status|api}"
        ;;
esac
