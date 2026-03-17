#!/bin/bash
# ============================================================
# 创建 CiCy 预装镜像
# 把 setup-prod.sh 里所有耗时的安装步骤预装到镜像里
# 之后 provision 只需要写配置 + 启动服务，不需要 apt/docker pull
#
# Usage: bash create-image.sh
# ============================================================
set -e

IMAGE_NAME="cicy-base-$(date +%Y%m%d)"
TEMP_VM="cicy-image-builder"
ZONE="asia-east1-b"

G='\033[0;32m'; N='\033[0m'
ok() { echo -e "${G}✅ $1${N}"; }
step() { echo -e "\n${G}[$1/$TOTAL] $2${N}"; }

TOTAL=5
gc() { env -u HTTPS_PROXY -u HTTP_PROXY -u http_proxy -u https_proxy -u ALL_PROXY gcloud "$@"; }

echo "============================================"
echo "  🔨 创建 CiCy 预装镜像: $IMAGE_NAME"
echo "============================================"

# ── 1. 创建临时 VM ──
step 1 "创建临时 VM"
gc compute instances create "$TEMP_VM" \
  --zone="$ZONE" \
  --machine-type=e2-medium \
  --image-family=ubuntu-2204-lts \
  --image-project=ubuntu-os-cloud \
  --boot-disk-size=10GB \
  --boot-disk-type=pd-ssd 2>&1 | tail -2
ok "$TEMP_VM created"

# ── 2. 等 SSH ──
step 2 "等待 SSH"
echo -n "  Waiting..."
for i in $(seq 1 30); do
  if gc compute ssh "$TEMP_VM" --zone="$ZONE" --command="echo ok" 2>/dev/null | grep -q ok; then
    echo " ready"; break
  fi
  echo -n "."; sleep 2
done

# ── 3. 安装所有依赖 ──
step 3 "安装依赖（apt + docker pull + cloudflared）"
gc compute ssh "$TEMP_VM" --zone="$ZONE" -- "sudo bash -s" << 'INSTALL'
set -e
export DEBIAN_FRONTEND=noninteractive

# 系统包
apt-get update -qq
apt-get install -y -qq docker.io docker-compose-v2 supervisor tmux curl jq xclip > /dev/null 2>&1
systemctl enable docker

# cloudflared
curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb -o /tmp/cloudflared.deb
dpkg -i /tmp/cloudflared.deb && rm /tmp/cloudflared.deb

# 预拉 Docker 镜像
systemctl start docker
docker pull mysql:8.0 -q
docker pull redis:7-alpine -q
docker pull nginx:alpine -q
docker pull cicybot/cicy-api:latest -q

# 预提取 API 二进制
docker create --name tmp cicybot/cicy-api:latest
docker cp tmp:/usr/local/bin/cicy-api /usr/local/bin/cicy-code-api
docker rm tmp
chmod +x /usr/local/bin/cicy-code-api

# tmux 配置
cat > /etc/skel/.tmux.conf << 'TMUX'
set -g default-terminal "tmux-256color"
set -g mouse on
set-option -g window-size largest
set-option -g aggressive-resize on
TMUX

# 清理缓存减小镜像
apt-get clean
docker system prune -f
rm -rf /var/lib/apt/lists/* /tmp/*

echo "INSTALL_DONE"
INSTALL
ok "所有依赖已安装"

# ── 4. 停机 + 创建镜像 ──
step 4 "停机并创建镜像"
gc compute instances stop "$TEMP_VM" --zone="$ZONE" 2>&1 | tail -1
gc compute images create "$IMAGE_NAME" \
  --source-disk="$TEMP_VM" \
  --source-disk-zone="$ZONE" \
  --family=cicy-base \
  --description="CiCy pre-installed: docker, mysql, redis, nginx, cloudflared, supervisor, tmux, cicy-code-api" \
  2>&1 | tail -2
ok "镜像 $IMAGE_NAME 创建完成"

# ── 5. 清理临时 VM ──
step 5 "清理"
gc compute instances delete "$TEMP_VM" --zone="$ZONE" --quiet 2>&1
ok "临时 VM 已删除"

echo ""
echo "============================================"
echo -e "  ${G}🎉 镜像就绪: $IMAGE_NAME${N}"
echo "============================================"
echo "  Family: cicy-base"
echo "  用法:   --image-family=cicy-base --image-project=$(gcloud config get-value project)"
echo ""
