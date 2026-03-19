#!/bin/bash
# 构建 CiCy Base Image
# 预装所有软件 + 预拉镜像，不启动任何服务
# 同时支持 Pro（独立 VM）和 Free（多用户共享 VM）
#
# Usage: bash build-image.sh
set -e

IMAGE_NAME="cicy-base-$(date +%Y%m%d)"
ZONE="asia-east1-b"
TEMP_VM="cicy-image-builder"

gc() { env -u HTTPS_PROXY -u HTTP_PROXY -u http_proxy -u https_proxy -u ALL_PROXY gcloud "$@"; }

echo "=== [1/5] 创建临时 VM ==="
gc compute instances create "$TEMP_VM" \
    --zone="$ZONE" \
    --machine-type=e2-medium \
    --image-family=ubuntu-2204-lts \
    --image-project=ubuntu-os-cloud \
    --boot-disk-size=20GB \
    --boot-disk-type=pd-ssd

echo "等待 SSH..."
for i in {1..30}; do
    gc compute ssh "$TEMP_VM" --zone="$ZONE" --command="echo ok" 2>/dev/null && break
    sleep 5
done

echo "=== [2/5] 安装软件 ==="
gc compute ssh "$TEMP_VM" --zone="$ZONE" << 'REMOTE'
set -e
export DEBIAN_FRONTEND=noninteractive

# ── 基础工具 ──
sudo apt-get update
sudo apt-get install -y curl wget unzip jq tmux supervisor git

# ── Docker ──
curl -fsSL https://get.docker.com | sudo sh

# ── Cloudflared ──
curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
echo 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared jammy main' | sudo tee /etc/apt/sources.list.d/cloudflared.list
sudo apt-get update && sudo apt-get install -y cloudflared

# ── code-server ──
curl -fsSL https://code-server.dev/install.sh | sudo sh

# ── Node.js 20 (AI CLI 工具依赖) ──
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo bash -
sudo apt-get install -y nodejs

# ── kiro-cli ──
curl -fsSL https://cli.kiro.dev/install | bash

# ── AI CLI 工具 ──
sudo npm install -g @anthropic-ai/claude-cli || true    # claude
sudo npm install -g opencode || true                     # opencode
sudo npm install -g @openai/codex || true                # codex
# sudo npm install -g @google/gemini-cli || true         # gemini (待稳定)
# sudo npm install -g @githubnext/github-copilot-cli || true  # copilot (待稳定)

# ── 预拉 Docker 镜像 ──
sudo docker pull mysql:8.0
sudo docker pull redis:7-alpine
sudo docker pull cicybot/code-server:latest
sudo docker pull cicybot/mitmproxy:latest

# ── 创建默认用户 w3c_offical（Pro 模式用）──
# Free 模式会动态 useradd
sudo id w3c_offical &>/dev/null || sudo useradd -m -s /bin/bash w3c_offical
sudo usermod -aG docker w3c_offical

# ── 创建目录结构 ──
sudo su - w3c_offical -c "mkdir -p ~/projects ~/workers/w-10001"

# ── 清理 ──
sudo apt-get clean
sudo rm -rf /var/lib/apt/lists/* /tmp/*

echo "=== 安装完成 ==="
REMOTE

echo "=== [3/5] 停止 VM ==="
gc compute instances stop "$TEMP_VM" --zone="$ZONE"

echo "=== [4/5] 创建镜像 ==="
gc compute images create "$IMAGE_NAME" \
    --source-disk="$TEMP_VM" \
    --source-disk-zone="$ZONE" \
    --family=cicy-base \
    --description="CiCy base: docker, cloudflared, code-server, supervisor, tmux, kiro-cli, pre-pulled images"

echo "=== [5/5] 清理临时 VM ==="
gc compute instances delete "$TEMP_VM" --zone="$ZONE" --quiet

echo ""
echo "✅ 镜像创建完成: $IMAGE_NAME"
echo "   预装: docker, cloudflared, code-server, supervisor, tmux, kiro-cli"
echo "   预拉: mysql:8.0, redis:7-alpine, cicybot/code-server, cicybot/mitmproxy"
