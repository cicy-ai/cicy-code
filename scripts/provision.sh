#!/bin/bash
# ============================================================
# CiCy 用户 VM 一键开通 (Prod 端运行)
# 创建 GCP VM + CF Tunnel + 部署全部服务
#
# Usage: bash provision.sh <vm_name> [zone]
# Example: bash provision.sh user-abc123 asia-east1-b
# ============================================================
set -e

# ── 参数 ──
VM_NAME="${1:?Usage: bash provision.sh <vm_name> [zone]}"
ZONE="${2:-asia-east1-b}"

# zone → region name
case "$ZONE" in
  asia-east2-b)       REGION_NAME="Hong Kong" ;;
  asia-east1-b)       REGION_NAME="Taiwan" ;;
  asia-northeast1-b)  REGION_NAME="Tokyo" ;;
  asia-southeast1-b)  REGION_NAME="Singapore" ;;
  us-central1-b)      REGION_NAME="Iowa" ;;
  us-west1-b)         REGION_NAME="Oregon" ;;
  us-east4-b)         REGION_NAME="Virginia" ;;
  europe-west4-b)     REGION_NAME="Netherlands" ;;
  europe-west2-b)     REGION_NAME="London" ;;
  *)                  REGION_NAME="$ZONE" ;;
esac

MACHINE_TYPE="${MACHINE_TYPE:-e2-small}"
DISK_SIZE="${DISK_SIZE:-20GB}"

# ── CF 配置 (从 global.json 读取) ──
CF_ACCOUNT=$(jq -r '.cf.prod.account_id' ~/global.json)
CF_API_TOKEN=$(jq -r '.cf.prod.api_token' ~/global.json)
CF_ZONE_ID=$(jq -r '.cf.prod.zone_id' ~/global.json)
CF_DOMAIN=$(jq -r '.cf.prod.domain' ~/global.json)
CF_API="https://api.cloudflare.com/client/v4"
CF_AUTH="Authorization: Bearer $CF_API_TOKEN"

# ── 颜色 ──
G='\033[0;32m'; Y='\033[1;33m'; R='\033[0;31m'; N='\033[0m'
ok()   { echo -e "${G}✅ $1${N}"; }
warn() { echo -e "${Y}⚠️  $1${N}"; }
fail() { echo -e "${R}❌ $1${N}"; exit 1; }
step() { echo -e "\n${G}[$1/$TOTAL] $2${N}"; }

TOTAL=5
SETUP_FAST="$HOME/projects/cicy-code/setup-fast.sh"
SETUP_PROD="$HOME/projects/cicy-code/setup-prod.sh"
# 优先用 fast 版（配合预装镜像）
if [ -f "$SETUP_FAST" ]; then
    SETUP_SCRIPT="$SETUP_FAST"
else
    SETUP_SCRIPT="$SETUP_PROD"
fi
[ -f "$SETUP_SCRIPT" ] || fail "setup script not found"

# strip proxy for gcloud
gc() { env -u HTTPS_PROXY -u HTTP_PROXY -u http_proxy -u https_proxy -u ALL_PROXY gcloud "$@"; }

echo "============================================"
echo "  🚀 CiCy VM 开通"
echo "  VM:     $VM_NAME"
echo "  Region: $REGION_NAME ($ZONE)"
echo "  Type:   $MACHINE_TYPE"
echo "============================================"

PROVISION_START=$(date +%s)
elapsed() { echo "$(( $(date +%s) - $1 ))s"; }

# ── 1. 创建 CF Tunnel ──
T1=$(date +%s)
step 1 "创建 Cloudflare Tunnel"
TUNNEL_SECRET=$(openssl rand -base64 32)
RESP=$(curl -sf "$CF_API/accounts/$CF_ACCOUNT/cfd_tunnel" \
  -H "$CF_AUTH" -H "Content-Type: application/json" \
  -d "{\"name\":\"$VM_NAME\",\"tunnel_secret\":\"$TUNNEL_SECRET\"}")

TUNNEL_ID=$(echo "$RESP" | jq -r '.result.id // empty')
TUNNEL_TOKEN=$(echo "$RESP" | jq -r '.result.token // empty')
[ -z "$TUNNEL_ID" ] && fail "Tunnel creation failed: $(echo "$RESP" | jq -r '.errors[0].message // "unknown"')"

# 配置路由
curl -sf -X PUT "$CF_API/accounts/$CF_ACCOUNT/cfd_tunnel/$TUNNEL_ID/configurations" \
  -H "$CF_AUTH" -H "Content-Type: application/json" \
  -d "{\"config\":{\"ingress\":[
    {\"hostname\":\"${VM_NAME}-api.${CF_DOMAIN}\",\"service\":\"http://localhost:8008\"},
    {\"service\":\"http_status:404\"}
  ]}}" > /dev/null

# 创建 DNS (只需要 API)
for sub in "${VM_NAME}-api"; do
  curl -sf -X POST "$CF_API/zones/$CF_ZONE_ID/dns_records" \
    -H "$CF_AUTH" -H "Content-Type: application/json" \
    -d "{\"type\":\"CNAME\",\"name\":\"${sub}.${CF_DOMAIN}\",\"content\":\"${TUNNEL_ID}.cfargotunnel.com\",\"proxied\":true}" > /dev/null 2>&1
done

ok "tunnel=$TUNNEL_ID"
ok "API: https://${VM_NAME}-api.${CF_DOMAIN}"
ok "⏱ Step 1: $(elapsed $T1)"

# ── 2. 创建 GCP VM ──
T2=$(date +%s)
step 2 "创建 GCP VM"
gc compute instances create "$VM_NAME" \
  --zone="$ZONE" \
  --machine-type="$MACHINE_TYPE" \
  --image-family=cicy-base \
  --boot-disk-size="$DISK_SIZE" \
  --boot-disk-type=pd-ssd 2>&1 | tail -2

VM_IP=$(gc compute instances describe "$VM_NAME" --zone="$ZONE" --format='get(networkInterfaces[0].accessConfigs[0].natIP)' 2>/dev/null)
ok "VM=$VM_NAME IP=$VM_IP Zone=$ZONE"
ok "⏱ Step 2: $(elapsed $T2)"

# ── 3. 等待 SSH + 传脚本 ──
T3=$(date +%s)
step 3 "传输部署脚本"
echo -n "  Waiting for SSH..."
for i in $(seq 1 30); do
  if gc compute ssh "$VM_NAME" --zone="$ZONE" --command="echo ok" 2>/dev/null | grep -q ok; then
    echo " ready"; break
  fi
  echo -n "."; sleep 2
done

CICY_SRC="$HOME/projects/cicy-code"
REMOTE_CICY="~/projects/cicy-code"

gc compute ssh "$VM_NAME" --zone="$ZONE" -- "mkdir -p $REMOTE_CICY/api $REMOTE_CICY/mitmproxy" 2>/dev/null
gc compute scp "$SETUP_SCRIPT" "$VM_NAME:~/setup.sh" --zone="$ZONE" 2>/dev/null
gc compute scp "$CICY_SRC/api/cicy-code-api" "$VM_NAME:$REMOTE_CICY/api/cicy-code-api" --zone="$ZONE" 2>/dev/null
gc compute scp "$CICY_SRC/api/run.sh" "$VM_NAME:$REMOTE_CICY/api/run.sh" --zone="$ZONE" 2>/dev/null
gc compute scp "$CICY_SRC/docker-compose.saas.yml" "$VM_NAME:$REMOTE_CICY/docker-compose.saas.yml" --zone="$ZONE" 2>/dev/null
gc compute scp "$CICY_SRC/cicy-api.supervisor.conf" "$VM_NAME:$REMOTE_CICY/cicy-api.supervisor.conf" --zone="$ZONE" 2>/dev/null
gc compute scp "$CICY_SRC/code-server.supervisor.conf" "$VM_NAME:$REMOTE_CICY/code-server.supervisor.conf" --zone="$ZONE" 2>/dev/null
gc compute scp "$CICY_SRC/.tmux.conf" "$VM_NAME:$REMOTE_CICY/.tmux.conf" --zone="$ZONE" 2>/dev/null
gc compute scp "$CICY_SRC/mitmproxy/kiro_monitor.py" "$VM_NAME:$REMOTE_CICY/mitmproxy/kiro_monitor.py" --zone="$ZONE" 2>/dev/null
gc compute scp "$CICY_SRC/schema.sql" "$VM_NAME:/tmp/schema.sql" --zone="$ZONE" 2>/dev/null
ok "cicy-code project files uploaded"
ok "⏱ Step 3: $(elapsed $T3)"

# ── 4. 执行部署 ──
T4=$(date +%s)
step 4 "执行一键部署"
gc compute ssh "$VM_NAME" --zone="$ZONE" -- "sudo bash ~/setup.sh '$TUNNEL_TOKEN' '$VM_NAME'" 2>&1 | \
  sed 's/\x1B\[[0-9;]*m//g' | grep -E "^\[|✅|⚠️|❌|🎉|running|RUNNING|passed"
ok "⏱ Step 4: $(elapsed $T4)"

# ── 5. 验证 ──
T5=$(date +%s)
step 5 "验证公网访问"
sleep 5
API_URL="https://${VM_NAME}-api.${CF_DOMAIN}/api/health"

API_RESP=$(curl -sf "$API_URL" 2>/dev/null || echo "failed")

if echo "$API_RESP" | grep -q "ok"; then
  ok "API: $API_URL → $API_RESP"
else
  warn "API not ready: $API_URL → $API_RESP"
fi

# ── 保存信息 ──
INFO_FILE="$HOME/cicy/vms/${VM_NAME}.json"
mkdir -p "$(dirname "$INFO_FILE")"
cat > "$INFO_FILE" << EOF
{
  "vm_name": "$VM_NAME",
  "zone": "$ZONE",
  "region_name": "$REGION_NAME",
  "ip": "$VM_IP",
  "machine_type": "$MACHINE_TYPE",
  "tunnel_id": "$TUNNEL_ID",
  "api_url": "https://${VM_NAME}-api.${CF_DOMAIN}",
  "created_at": "$(date -Iseconds)"
}
EOF

echo ""
echo "============================================"
echo -e "  ${G}🎉 开通完成！${N}"
echo "============================================"
echo "  VM:     $VM_NAME ($VM_IP)"
echo "  API:    https://${VM_NAME}-api.${CF_DOMAIN}"
echo "  Info:   $INFO_FILE"
ok "⏱ Step 5: $(elapsed $T5)"
echo ""
echo "  ⏱ 耗时明细:"
echo "    Step 1 (CF Tunnel):   $(elapsed $T1)"
echo "    Step 2 (GCP VM):      $(elapsed $T2)"
echo "    Step 3 (SSH+Upload):  $(elapsed $T3)"
echo "    Step 4 (Deploy):      $(elapsed $T4)"
echo "    Step 5 (Verify):      $(elapsed $T5)"
echo "    Total:                $(elapsed $PROVISION_START)"
echo ""
echo "  管理:"
echo "    SSH:  gcloud compute ssh $VM_NAME --zone=$ZONE"
echo "    删除: bash deprovision.sh $VM_NAME $ZONE"
echo ""
