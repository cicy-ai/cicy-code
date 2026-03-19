#!/bin/bash
# App 部署: Build + COS + Worker
set -e
cd "$(dirname "$0")"

# Build frontend
echo "=== Build ==="
npm run build 2>&1 | tail -3

# Clean old assets, copy new
rm -rf worker/public/assets/*
cp -r dist/* worker/public/

cd worker

VER=$(jq -r '.app' ../../versions.json)
CF_ACCOUNT=$(jq -r '.cf.prod.account_id' ~/global.json)
CF_TOKEN=$(jq -r '.cf.prod.api_token' ~/global.json)

echo "=== App v$VER ==="

# Inject version
sed -i "s/^const VER = .*/const VER = '$VER';/" app-worker.js

echo "=== 1/2 COS Assets ==="
python3 ../../cos-upload.py app

echo "=== 2/2 Worker ==="
CLOUDFLARE_ACCOUNT_ID=$CF_ACCOUNT CLOUDFLARE_API_TOKEN=$CF_TOKEN \
NODE_TLS_REJECT_UNAUTHORIZED=0 \
npx -y wrangler deploy

echo "=== Done: App v$VER ==="
