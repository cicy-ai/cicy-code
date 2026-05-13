#!/bin/bash
# Cloudflare Tunnel 路由管理（API 方式）
# 自动管理 g-{port}.cicy.de5.net → localhost:{port} 路由 + DNS CNAME
#
# 用法:
#   bash cf-tunnel.sh list                # 列出所有路由+端口状态
#   bash cf-tunnel.sh add 8101            # 添加路由 + DNS
#   bash cf-tunnel.sh add 8101 8102       # 批量添加
#   bash cf-tunnel.sh del 8101            # 删除路由 + DNS

set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
exec python3 "$SCRIPT_DIR/cf-tunnel.py" "$@"
