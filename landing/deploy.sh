#!/bin/bash
# 落地页部署: Worker + COS
set -e
cd "$(dirname "$0")"

VER=$(jq -r '.landing' ../versions.json)
CF_ACCOUNT=$(jq -r '.cf.prod.account_id' ~/global.json)
CF_TOKEN=$(jq -r '.cf.prod.api_token' ~/global.json)

echo "=== Landing v$VER ==="

# Inject version
sed -i "s/^const VER = .*/const VER = '$VER';/" app-proxy.js
sed -i "s/^VER = .*/VER = 'v$VER'/" cos-upload.py

echo "=== 1/2 COS Assets ==="
python3 cos-upload.py

echo "=== 2/2 Worker ==="
CLOUDFLARE_ACCOUNT_ID=$CF_ACCOUNT CLOUDFLARE_API_TOKEN=$CF_TOKEN \
NODE_TLS_REJECT_UNAUTHORIZED=0 \
npx wrangler deploy

echo "=== Done: Landing v$VER ==="
