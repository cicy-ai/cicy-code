#!/bin/bash
# tg-bot 服务健康检查和管理

PROJECT_DIR="$HOME/projects/ai-workers/tg-bot"
MANAGER_SERVICE="tg-bot-manager"

check_supervisor() {
    MANAGER_STATUS=$(sudo supervisorctl status $MANAGER_SERVICE 2>/dev/null | grep -o "RUNNING\|STOPPED\|FATAL")
    if [ "$MANAGER_STATUS" = "RUNNING" ]; then
        echo "✅"
    else
        echo "❌"
    fi
}

check_bot_instances() {
    BOT_COUNT=$(sudo supervisorctl status | grep "tg_bot_" | grep -c "RUNNING")
    echo "$BOT_COUNT"
}

get_logs() {
    sudo supervisorctl tail $MANAGER_SERVICE | tail -30
}

restart_service() {
    echo "🔄 重启 tg-bot 管理器..."
    sudo supervisorctl restart $MANAGER_SERVICE
    sleep 3
}

# 主检查逻辑
echo "🔍 检查 tg-bot 服务"
echo ""

MANAGER_STATUS=$(check_supervisor)
BOT_COUNT=$(check_bot_instances)

echo "📊 服务状态:"
echo "  管理器 ($MANAGER_SERVICE): $MANAGER_STATUS"
echo "  Bot 实例数: $BOT_COUNT 个"
echo ""

if [ "$MANAGER_STATUS" = "❌" ]; then
    echo "❌ 管理器未运行"
    echo ""
    read -p "是否重启管理器? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        restart_service
    fi
elif [ "$BOT_COUNT" = "0" ]; then
    echo "⚠️  没有运行中的 Bot 实例"
    echo ""
    echo "📋 管理器日志 (最后 30 行):"
    get_logs
    echo ""
    echo "💡 提示: 检查数据库 ttyd_config 表中是否有 tg_enable=1 的记录"
else
    echo "✅ 服务正常运行"
    echo ""
    sudo supervisorctl status | grep "tg_bot_\|tg-bot-manager"
    echo ""
    echo "💡 查看实时日志: sudo supervisorctl tail -f $MANAGER_SERVICE"
fi

# 最终状态
echo ""
echo "📋 管理命令:"
echo "  查看所有状态: sudo supervisorctl status | grep tg"
echo "  查看管理器日志: sudo supervisorctl tail -f $MANAGER_SERVICE"
echo "  重启管理器: sudo supervisorctl restart $MANAGER_SERVICE"
echo "  重启所有 Bot: sudo supervisorctl restart tg_bot_*"
echo ""
echo "📁 项目路径: $PROJECT_DIR"
