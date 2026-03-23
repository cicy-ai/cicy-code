#!/usr/bin/env bash
# OpenClaw 用户开通 Runbook
# 每次开通新用户按此步骤执行，完成后在底部记录用户信息
#
# 用时：约 30 分钟/用户
# 前提：new-api 在跑（docker ps | grep new-api）

set -e
USER_ID="${1:?Usage: bash openclaw-provision.md <user_id>}"
# 例：bash openclaw-provision.md user-alice

echo "=== 开通 OpenClaw 用户: $USER_ID ==="

# ── Step 1: new-api 创建用户账户 + API key ──────────────────────
# 手动操作：打开 https://new-api.cicy-ai.com 后台
# 1. 用户管理 → 新建用户 → 用户名: $USER_ID
# 2. 令牌管理 → 新建令牌 → 名称: openclaw-$USER_ID → 复制 key
# 3. 充值对应金额（用户已付款金额）
# 预期：后台显示用户余额正确
# 记录 API key：
API_KEY="sk-..."  # 替换为实际 key

# ── Step 2: 开通 GCP VM ────────────────────────────────────────
# 复用 cicy-code provision 脚本，机型用 e2-micro（最省钱）
MACHINE_TYPE=e2-micro bash ~/projects/cicy-code/scripts/provision.sh "openclaw-$USER_ID" asia-east1-b
# 预期：VM 创建成功，SSH 可连接
# 记录 VM IP：
VM_IP=$(gcloud compute instances describe "openclaw-$USER_ID" --zone=asia-east1-b --format='get(networkInterfaces[0].accessConfigs[0].natIP)' 2>/dev/null || echo "手动填写")
echo "VM IP: $VM_IP"

# ── Step 3: SSH 进 VM，安装 OpenClaw ──────────────────────────
# ssh w3c_offical@$VM_IP
# 在 VM 内执行：
cat << 'REMOTE_SCRIPT'
# 安装 Node.js（OpenClaw 依赖）
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt-get install -y nodejs

# 克隆 OpenClaw
git clone https://github.com/open-claw/openclaw ~/openclaw
cd ~/openclaw && npm install

# 写入配置
cat > ~/.openclaw/config.json << EOF
{
  "llm": {
    "provider": "openai",
    "base_url": "http://new-api.cicy-ai.com/v1",
    "api_key": "API_KEY_PLACEHOLDER",
    "model": "claude-opus-4-5"
  },
  "telegram": {
    "bot_token": "BOT_TOKEN_PLACEHOLDER"
  }
}
EOF
REMOTE_SCRIPT
# 手动替换 API_KEY_PLACEHOLDER 和 BOT_TOKEN_PLACEHOLDER

# ── Step 4: 配置 supervisor 自动重启 + 崩溃告警 ───────────────
cat << 'SUPERVISOR_CONF'
# /etc/supervisor/conf.d/openclaw.conf
[program:openclaw]
command=node /home/w3c_offical/openclaw/src/index.js
directory=/home/w3c_offical/openclaw
user=w3c_offical
autostart=true
autorestart=true
stdout_logfile=/var/log/openclaw.log
stderr_logfile=/var/log/openclaw.log
SUPERVISOR_CONF
# sudo cp 上面内容到 /etc/supervisor/conf.d/openclaw.conf
# sudo supervisorctl reread && sudo supervisorctl update
# sudo supervisorctl status openclaw  # 预期：RUNNING

# ── Step 5: 验证 token 计费链路 ───────────────────────────────
# 在 VM 内发一条测试消息给 Telegram bot
# 然后检查 new-api 后台：令牌使用记录 → 确认有 token 消耗
# 预期：new-api 后台显示该用户有 token 消耗记录

# ── Step 6: 配置 new-api 余额告警 ────────────────────────────
# new-api 后台 → 用户设置 → 余额告警阈值：¥20
# 告警方式：Telegram webhook（你的 bot）

# ── Step 7: 交付给用户 ────────────────────────────────────────
# 发给用户：
# "你的 AI 员工已上线 🎉
#  Telegram: @your_bot_name
#  发任何指令给它，它会帮你执行
#  当前余额：¥XXX（约 XXX 万 token）
#  余额低于 ¥20 时我会提醒你充值"

# ── 记录（每次开通后填写）────────────────────────────────────
# 用户: $USER_ID
# 开通时间:
# VM IP:
# Telegram bot:
# new-api 用户名:
# 付款金额:
# 第一个任务（用户让 AI 干的第一件事）:
# 卡点（哪里需要你帮忙）:
# 续费意愿（1-10）:
