#!/bin/bash
set -e

# 初始化 MySQL
if ! mysql -u root -e "SELECT 1" &>/dev/null; then
    mysqld --user=mysql --datadir=/var/lib/mysql &
    for i in $(seq 1 30); do
        mysql -u root -e "SELECT 1" &>/dev/null && break
        sleep 1
    done
fi

# 创建数据库 + 导入 schema
mysql -u root -e "CREATE DATABASE IF NOT EXISTS cicy_code" 2>/dev/null
mysql -u root cicy_code < /tmp/schema.sql 2>/dev/null
mysql -u root -e "ALTER USER 'root'@'localhost' IDENTIFIED WITH mysql_native_password BY ''; FLUSH PRIVILEGES;" 2>/dev/null

# 生成 token
TOKEN="${API_TOKEN:-$(openssl rand -hex 16)}"
if [ ! -f /home/w3c_offical/global.json ]; then
    echo "{\"api_token\": \"$TOKEN\"}" > /home/w3c_offical/global.json
    chown w3c_offical:w3c_offical /home/w3c_offical/global.json
fi
echo "=== TOKEN: $TOKEN ==="

# tmux session
su - w3c_offical -c "tmux new-session -d -s w-10001 -n main" 2>/dev/null || true

# 停掉手动启动的 mysql，让 supervisor 管理
mysqladmin -u root shutdown 2>/dev/null || true
sleep 1

# frpc: 暴露 cicy-api(8008) + ssh(22) 到 GCP server
# FRP_PORT: 30000+ for API, SSH_PORT: 2000+ (env)
if [ -n "$FRP_SERVER" ] && [ -n "$FRP_TOKEN" ] && [ -n "$FRP_PORT" ]; then
    cat >> /etc/supervisor/conf.d/all.conf << FRPEOF

[program:sshd]
command=/usr/sbin/sshd -D
autostart=true
autorestart=true

[program:frpc]
command=/usr/local/bin/frpc -c /tmp/frpc.yaml
autostart=true
autorestart=true
stdout_logfile=/dev/stdout
stdout_logfile_maxbytes=0
stderr_logfile=/dev/stderr
stderr_logfile_maxbytes=0
FRPEOF
    echo "w3c_offical:${SSH_PASS:-cicy123}" | chpasswd
    cat > /tmp/frpc.yaml << EOF
serverAddr: "$FRP_SERVER"
serverPort: 7000
auth:
  method: token
  token: "$FRP_TOKEN"
proxies:
  - name: "cicy-api"
    type: tcp
    localIP: "127.0.0.1"
    localPort: 8008
    remotePort: $FRP_PORT
  - name: "ssh"
    type: tcp
    localIP: "127.0.0.1"
    localPort: 22
    remotePort: ${SSH_PORT:-2001}
EOF
fi

# 启动所有服务
exec /usr/bin/supervisord -c /etc/supervisor/conf.d/all.conf
