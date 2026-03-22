#!/bin/bash
# provision-free.sh — 创建 Free 试用用户
# 共享: MySQL(:3306) Redis(:6379) mitmproxy(:8003)
# 独立: cicy-api(supervisor) + code-server(docker)
# Usage: bash provision-free.sh 001
set -e

N="$1"
[ -z "$N" ] && echo "Usage: $0 <NNN> (e.g. 001)" && exit 1

# ── 标准 ──
USER="f-${N}"
UID_N=$((3000 + 10#$N))
API_PORT=$((9000 + 10#$N))
CS_PORT=$((8200 + 10#$N))
TTYD_PORT=$((10000 + 10#$N))
DOMAIN_PREFIX="u-${N}-free"
HOME_DIR="/home/${USER}"
DB_NAME="cicy_f_${N}"
REDIS_DB=$((10#$N))
WORKSPACE="w-10001"

TUNNEL_ID="f4d12416-a36a-4864-897c-534c87d5394c"
ZONE_ID=$(jq -r '.cf.prod.zone_id' ~/global.json)
CF_TOKEN=$(jq -r '.cf.prod.api_token' ~/global.json)
ACCOUNT_ID=$(jq -r '.cf.prod.account_id' ~/global.json)
MYSQL="docker exec -i cicy-mysql mysql -u root -pcicy-code"
PRO_HOME="/home/w3c_offical"
API_BINARY="${PRO_HOME}/projects/cicy-code/api/cicy-code-api"
SCHEMA="${PRO_HOME}/projects/cicy-code/schema.sql"

echo "=== Provision Free User: ${USER} ==="
echo "  UID=${UID_N} API=:${API_PORT} CS=:${CS_PORT} TTYD=:${TTYD_PORT}"
echo "  DB=${DB_NAME} Redis=db${REDIS_DB}"

# ── 1. Linux 用户 ──
if id "$USER" &>/dev/null; then
  echo "⚠️  User ${USER} exists, skipping"
else
  sudo useradd -m -u "$UID_N" -s /bin/bash "$USER"
  sudo bash -c "echo 'export PATH=/usr/local/bin:\$PATH' >> ${HOME_DIR}/.bashrc"
  echo "✅ User created"
fi

# ── 2. 目录结构 ──
sudo -u "$USER" mkdir -p \
  "${HOME_DIR}/projects/cicy-code/api" \
  "${HOME_DIR}/workers/${WORKSPACE}" \
  "${HOME_DIR}/data" \
  "${HOME_DIR}/logs"

# ── 3. API binary + resources ──
sudo cp "$API_BINARY" "${HOME_DIR}/projects/cicy-code/api/cicy-code-api"
sudo cp -r "${PRO_HOME}/projects/cicy-code/api/resources" "${HOME_DIR}/projects/cicy-code/api/resources"
sudo chown -R "$USER:$USER" "${HOME_DIR}/projects/cicy-code/api"

# ── 4. Token + global.json ──
API_TOKEN=$(openssl rand -hex 16)
sudo -u "$USER" tee "${HOME_DIR}/global.json" > /dev/null <<EOF
{"api_token": "${API_TOKEN}", "port": ${API_PORT}}
EOF

# ── 5. run.sh（跟 Pro 格式一致）──
sudo -u "$USER" tee "${HOME_DIR}/projects/cicy-code/api/run.sh" > /dev/null <<EOF
#!/bin/bash
export MYSQL_DSN="root:cicy-code@tcp(localhost:3306)/${DB_NAME}"
export REDIS_ADDR="localhost:6379"
export REDIS_DB="${REDIS_DB}"
export PORT="${API_PORT}"
export TTYD_PORT="${TTYD_PORT}"
export CS_PORT="${CS_PORT}"
export HOME="${HOME_DIR}"
export TERM=xterm-256color
${HOME_DIR}/projects/cicy-code/api/cicy-code-api
EOF
sudo chmod +x "${HOME_DIR}/projects/cicy-code/api/run.sh"

# ── 6. MySQL ──
$MYSQL -e "CREATE DATABASE IF NOT EXISTS \`${DB_NAME}\`;" 2>/dev/null
$MYSQL "$DB_NAME" < "$SCHEMA" 2>/dev/null
$MYSQL "$DB_NAME" -e "
  DELETE FROM agent_config;
  INSERT INTO agent_config (pane_id, title, ttyd_port, role, workspace)
  VALUES ('${WORKSPACE}:main.0', 'Main', ${TTYD_PORT}, 'master', '~/workers/${WORKSPACE}');
" 2>/dev/null
echo "✅ MySQL ${DB_NAME}"

# ── 7. code-server（跟 Pro 一样的方式）──
CS_NAME="cs-${USER}"
docker rm -f "$CS_NAME" 2>/dev/null
docker run -d --name "$CS_NAME" \
  --network host \
  --restart unless-stopped \
  --memory 512m --cpus 0.5 \
  --user "${UID_N}:${UID_N}" \
  -e "TERM=xterm-256color" \
  -e "HOME=${HOME_DIR}" \
  -v "${HOME_DIR}:${HOME_DIR}" \
  codercom/code-server:latest \
  --bind-addr "127.0.0.1:${CS_PORT}" --auth none
echo "✅ code-server :${CS_PORT}"

# ── 8. supervisor（跟 Pro 一样的格式）──
sudo tee "/etc/supervisor/conf.d/cicy-api-${USER}.conf" > /dev/null <<EOF
[program:cicy-api-${USER}]
command=/bin/bash ${HOME_DIR}/projects/cicy-code/api/run.sh
directory=${HOME_DIR}/projects/cicy-code/api
user=${USER}
autostart=true
autorestart=true
stdout_logfile=/var/log/cicy-api-${USER}.log
stderr_logfile=/var/log/cicy-api-${USER}.log
EOF
sudo supervisorctl reread >/dev/null
sudo supervisorctl update >/dev/null
sleep 2
echo "✅ supervisor cicy-api-${USER}"

# ── 9. tmux ──
sudo -u "$USER" tmux new-session -d -s "$WORKSPACE" -c "${HOME_DIR}/workers/${WORKSPACE}" 2>/dev/null || true
echo "✅ tmux ${WORKSPACE}"

# ── 9.5. gstack skills ──
sudo -u "$USER" bash -c "
  mkdir -p ${HOME_DIR}/.codex/skills
  if [ ! -d ${HOME_DIR}/.codex/skills/gstack ]; then
    git clone https://github.com/cicy-ai/gstack ${HOME_DIR}/.codex/skills/gstack --depth 1 -q
  fi
  mkdir -p ${HOME_DIR}/.kiro/agents
  ln -sf ${HOME_DIR}/.codex/skills/gstack/.agents/skills/* ${HOME_DIR}/.kiro/agents/ 2>/dev/null || true
"
echo "✅ gstack skills"

# ── 10. CF DNS ──
EXISTING=$(curl -s "https://api.cloudflare.com/client/v4/zones/${ZONE_ID}/dns_records?name=${DOMAIN_PREFIX}-api.cicy-ai.com" \
  -H "Authorization: Bearer ${CF_TOKEN}" | jq -r '.result[0].id // empty')
if [ -z "$EXISTING" ]; then
  curl -s -X POST "https://api.cloudflare.com/client/v4/zones/${ZONE_ID}/dns_records" \
    -H "Authorization: Bearer ${CF_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "{\"type\":\"CNAME\",\"name\":\"${DOMAIN_PREFIX}-api.cicy-ai.com\",\"content\":\"${TUNNEL_ID}.cfargotunnel.com\",\"proxied\":true}" | jq -r '.success'
fi
echo "✅ DNS ${DOMAIN_PREFIX}-api.cicy-ai.com"

# ── 11. CF Tunnel ingress ──
INGRESS=$(curl -s "https://api.cloudflare.com/client/v4/accounts/${ACCOUNT_ID}/cfd_tunnel/${TUNNEL_ID}/configurations" \
  -H "Authorization: Bearer ${CF_TOKEN}" | jq '.result.config.ingress')
if ! echo "$INGRESS" | jq -e ".[] | select(.hostname==\"${DOMAIN_PREFIX}-api.cicy-ai.com\")" >/dev/null 2>&1; then
  NEW_INGRESS=$(echo "$INGRESS" | jq --arg h "${DOMAIN_PREFIX}-api.cicy-ai.com" --arg s "http://localhost:${API_PORT}" \
    '[.[] | select(.service != "http_status:404")] + [{"hostname": $h, "service": $s}] + [{"service": "http_status:404"}]')
  curl -s -X PUT "https://api.cloudflare.com/client/v4/accounts/${ACCOUNT_ID}/cfd_tunnel/${TUNNEL_ID}/configurations" \
    -H "Authorization: Bearer ${CF_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "{\"config\":{\"ingress\":${NEW_INGRESS}}}" | jq -r '.success'
fi
echo "✅ Tunnel → localhost:${API_PORT}"

# ── 12. 验证 ──
sleep 2
HTTP=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${API_PORT}/api/health")
echo ""
echo "=== Done ==="
echo "  Frontend: https://${DOMAIN_PREFIX}-app.cicy-ai.com/?token=${API_TOKEN}"
echo "  API:      https://${DOMAIN_PREFIX}-api.cicy-ai.com/api/health"
echo "  Token:    ${API_TOKEN}"
echo "  Health:   ${HTTP}"
