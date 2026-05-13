#!/bin/bash
# vnc-proxy 服务健康检查和管理

PROJECT_DIR="$HOME/projects/vnc-proxy"
SERVER_PORT=13446
FRONTEND_PORT=13447
DOMAIN="g-vnc.cicy.de5.net"

check_ports() {
    SERVER_OK=$(ss -tlnp | grep -q ":$SERVER_PORT " && echo "✅" || echo "❌")
    FRONTEND_OK=$(ss -tlnp | grep -q ":$FRONTEND_PORT " && echo "✅" || echo "❌")
}

check_docker() {
    cd "$PROJECT_DIR"
    SERVER_STATUS=$(sudo docker compose ps server --format json 2>/dev/null | jq -r '.State' 2>/dev/null)
    FRONTEND_STATUS=$(sudo docker compose ps frontend --format json 2>/dev/null | jq -r '.State' 2>/dev/null)
    
    if [ "$SERVER_STATUS" = "running" ] && [ "$FRONTEND_STATUS" = "running" ]; then
        echo "✅"
    else
        echo "❌"
    fi
}

get_logs() {
    cd "$PROJECT_DIR"
    echo "=== Server 日志 ==="
    sudo docker compose logs --tail=15 server
    echo ""
    echo "=== Frontend 日志 ==="
    sudo docker compose logs --tail=15 frontend
}

restart_service() {
    echo "🔄 重启 vnc-proxy 服务..."
    cd "$PROJECT_DIR"
    sudo docker compose restart
    sleep 5
}

start_service() {
    echo "🚀 启动 vnc-proxy 服务..."
    cd "$PROJECT_DIR"
    sudo docker compose up -d
    sleep 5
}

# 主检查逻辑
echo "🔍 检查 vnc-proxy 服务"
echo ""

check_ports
DOCKER_STATUS=$(check_docker)

echo "📊 服务状态:"
echo "  Docker 容器: $DOCKER_STATUS"
echo "  Server 端口 $SERVER_PORT: $SERVER_OK"
echo "  Frontend 端口 $FRONTEND_PORT: $FRONTEND_OK"
echo ""

if [ "$DOCKER_STATUS" = "❌" ]; then
    echo "⚠️  Docker 容器未运行"
    echo ""
    read -p "是否启动服务? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        start_service
    fi
elif [ "$SERVER_OK" = "❌" ] || [ "$FRONTEND_OK" = "❌" ]; then
    echo "⚠️  端口未监听"
    echo ""
    echo "📋 最近日志:"
    get_logs
    echo ""
    echo "💡 提示: 项目支持热重载 (tsx watch + Vite HMR),代码修改会自动生效"
    echo "   建议先查看日志排查问题,非必要不要重启"
    echo ""
    read -p "确定要重启服务? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        restart_service
    fi
else
    echo "✅ 服务正常运行"
    echo ""
    echo "💡 查看实时日志: cd ~/projects/vnc-proxy && sudo docker compose logs -f"
fi

# 最终状态
echo ""
echo "🌐 访问地址:"
echo "  公网: https://$DOMAIN"
echo "  本地 Frontend: http://localhost:$FRONTEND_PORT"
echo "  本地 Server: http://localhost:$SERVER_PORT"
echo ""
echo "📋 管理命令:"
echo "  查看日志: cd ~/projects/vnc-proxy && sudo docker compose logs -f"
echo "  重启: cd ~/projects/vnc-proxy && sudo docker compose restart"
echo "  停止: cd ~/projects/vnc-proxy && sudo docker compose down"
