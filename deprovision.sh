#!/bin/bash
# ============================================================
# CiCy VM 销毁 (删 VM + 删 Tunnel + 删 DNS)
# Usage: bash deprovision.sh <vm_name> [zone]
# ============================================================
set -e

VM_NAME="${1:?Usage: bash deprovision.sh <vm_name>}"
# 自动从保存的 JSON 读 zone
INFO_FILE="$HOME/cicy/vms/${VM_NAME}.json"
if [ -f "$INFO_FILE" ] && [ -z "${2:-}" ]; then
  ZONE=$(jq -r '.zone' "$INFO_FILE")
else
  ZONE="${2:-asia-east1-b}"
fi

CF_ACCOUNT=$(jq -r '.cf.prod.account_id' ~/global.json)
CF_API_TOKEN=$(jq -r '.cf.prod.api_token' ~/global.json)
CF_ZONE_ID=$(jq -r '.cf.prod.zone_id' ~/global.json)
CF_DOMAIN=$(jq -r '.cf.prod.domain' ~/global.json)
CF_API="https://api.cloudflare.com/client/v4"
CF_AUTH="Authorization: Bearer $CF_API_TOKEN"

G='\033[0;32m'; N='\033[0m'
ok() { echo -e "${G}✅ $1${N}"; }
gc() { env -u HTTPS_PROXY -u HTTP_PROXY -u http_proxy -u https_proxy -u ALL_PROXY gcloud "$@"; }

echo "🗑️  Deprovisioning: $VM_NAME"

# 删 GCP VM
gc compute instances delete "$VM_NAME" --zone="$ZONE" --quiet 2>&1 && ok "VM deleted" || echo "  VM not found"

# 找 tunnel ID
TUNNEL_ID=$(curl -sf "$CF_API/accounts/$CF_ACCOUNT/cfd_tunnel?name=$VM_NAME&is_deleted=false" \
  -H "$CF_AUTH" | jq -r '.result[0].id // empty')

if [ -n "$TUNNEL_ID" ]; then
  # 清理 tunnel 连接
  curl -sf -X DELETE "$CF_API/accounts/$CF_ACCOUNT/cfd_tunnel/$TUNNEL_ID/connections" \
    -H "$CF_AUTH" > /dev/null 2>&1
  # 删 tunnel
  curl -sf -X DELETE "$CF_API/accounts/$CF_ACCOUNT/cfd_tunnel/$TUNNEL_ID" \
    -H "$CF_AUTH" > /dev/null 2>&1
  ok "Tunnel deleted: $TUNNEL_ID"
fi

# 删 DNS 记录
for sub in "${VM_NAME}-api" "${VM_NAME}"; do
  REC_ID=$(curl -sf "$CF_API/zones/$CF_ZONE_ID/dns_records?name=${sub}.${CF_DOMAIN}" \
    -H "$CF_AUTH" | jq -r '.result[0].id // empty')
  if [ -n "$REC_ID" ]; then
    curl -sf -X DELETE "$CF_API/zones/$CF_ZONE_ID/dns_records/$REC_ID" \
      -H "$CF_AUTH" > /dev/null
    ok "DNS deleted: ${sub}.${CF_DOMAIN}"
  fi
done

# 删本地记录
rm -f "$HOME/cicy/vms/${VM_NAME}.json" 2>/dev/null

ok "Done. $VM_NAME fully cleaned up."
