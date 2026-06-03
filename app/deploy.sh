#!/bin/bash
# App 部署: Build + R2 + Worker
set -e
cd "$(dirname "$0")"

WORKER=../app-worker
CICY_ROOT_DIR="$HOME/cicy-ai"
GLOBAL_JSON="$CICY_ROOT_DIR/global.json"

# Build frontend
echo "=== Build ==="
npm run build 2>&1 | tail -3

# Clean old assets, copy new
rm -rf "$WORKER/public/assets"/*
cp -r dist/* "$WORKER/public/"

cd "$WORKER"

VER=$(jq -r '.app' ../versions.json)
CF_ACCOUNT=$(jq -r '.cf.prod.account_id' "$GLOBAL_JSON")
CF_TOKEN=$(jq -r '.cf.prod.api_token' "$GLOBAL_JSON")

echo "=== App v$VER ==="

sed -i "s/^const VER = .*/const VER = '$VER';/" app-worker.js

echo "=== 1/2 R2 Assets ==="
python3 ../scripts/r2-upload.py app

echo "=== 2/2 Worker ==="
CLOUDFLARE_ACCOUNT_ID=$CF_ACCOUNT CLOUDFLARE_API_TOKEN=$CF_TOKEN \
NODE_TLS_REJECT_UNAUTHORIZED=0 \
npx -y wrangler deploy

echo "=== Done: App v$VER ==="
