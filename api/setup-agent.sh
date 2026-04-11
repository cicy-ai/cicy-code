#!/bin/bash

# 配置 Claude Code、Codex、OpenCode 的 API Key、API URL 和默认模型
# 用法: ./setup_opencode.sh <apiKey> <apiUrl> [defaultOpencodeModel]

set -e

HOME_DIR="${HOME:-$(getent passwd "$(id -u)" 2>/dev/null | cut -d: -f6)}"
if [ -z "$HOME_DIR" ]; then
    HOME_DIR="$(python3 - <<'PY'
import os, pwd
print(os.path.expanduser("~") or pwd.getpwuid(os.getuid()).pw_dir)
PY
)"
fi

API_KEY="${1}"
API_URL="${2:-http://cicy-ai.com/v1}"
ANTHROPIC_URL="${3:-http://cicy-ai.com}"
DEFAULT_OPENCODE_MODEL="${4:-gpt-5.4}"
DEFAULT_CLAUDE_MODEL="${5:-opus[1m]}"
CODEX_MODEL="${6:-gpt-5.4}"

timestamp() {
  date '+%Y/%m/%d %H:%M:%S'
}

log() {
  printf '%s %s\n' "$(timestamp)" "$*"
}

normalize_openclaw_model() {
  case "$1" in
    gpt5.4) echo "gpt-5.4" ;;
    cicyai/claude-opus-4-6) echo "claude-opus-4-6" ;;
    cicyai/claude-sonnet-4-6) echo "claude-sonnet-4-6" ;;
    cicyai/claude-haiku-4-5-20251001) echo "claude-haiku-4-5-20251001" ;;
    shibacc/claude-opus-4-6) echo "claude-opus-4-6" ;;
    shibacc/claude-sonnet-4-6) echo "claude-sonnet-4-6" ;;
    shibacc/claude-haiku-4-5-20251001) echo "claude-haiku-4-5-20251001" ;;
    *) echo "$1" ;;
  esac
}

default_openclaw_runtime_base_url() {
  local mgr_port="${PORT:-8021}"
  echo "http://127.0.0.1:${mgr_port}/api/openclaw/provider"
}

if [ -z "$API_KEY" ]; then
    log "用法: $0 <apiKey> [apiUrl] [anthropicUrl] [defaultOpencodeModel] [defaultClaudeModel] [codexModel]"
    log "示例: $0 sk-xxx http://2000.run:6543/v1 http://2000.run:6543 gpt-5.4 opus[1m] gpt-5.4"
    exit 1
fi

log "开始配置 AI 工具..."

# ===== Claude Code =====
log "开始配置 Claude Code..."
CLAUDE_DIR="$HOME_DIR/.claude"
CLAUDE_CONFIG="$CLAUDE_DIR/settings.json"
mkdir -p "$CLAUDE_DIR"
cat > "$CLAUDE_CONFIG" << EOF
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "$API_KEY",
    "ANTHROPIC_BASE_URL": "$ANTHROPIC_URL"
  },
  "model": "$DEFAULT_CLAUDE_MODEL",
  "permissions": {
    "allow": ["*"]
  },
  "skipDangerousModePermissionPrompt": true
}
EOF
log "✓ Claude Code 配置完成 (Base URL: $ANTHROPIC_URL, Model: $DEFAULT_CLAUDE_MODEL)"

# ===== Codex =====
log "开始配置 Codex..."
CODEX_DIR="$HOME_DIR/.codex"
CODEX_CONFIG="$CODEX_DIR/config.toml"
CODEX_AUTH="$CODEX_DIR/auth.json"
mkdir -p "$CODEX_DIR"
cat > "$CODEX_CONFIG" << EOF
disable_response_storage = true
model = '$CODEX_MODEL'
model_provider = 'custom'
model_reasoning_effort = 'high'
[model_providers.custom]
base_url = '$API_URL'
name = 'shiba-cc'
requires_openai_auth = true
wire_api = 'responses'

[projects]
[projects.'$HOME_DIR']
trust_level = 'trusted'

[projects.'$HOME_DIR/Private']
trust_level = 'trusted'

[projects.'$HOME_DIR/projects/cicy-cloud']
trust_level = 'trusted'

[projects.'$HOME_DIR/projects/cicy-code']
trust_level = 'trusted'

[projects.'$HOME_DIR/projects/cicy-code-v1']
trust_level = 'trusted'

[projects.'$HOME_DIR/projects/cicy-gateway']
trust_level = 'trusted'
EOF
cat > "$CODEX_AUTH" << EOF
{
  "OPENAI_API_KEY": "$API_KEY"
}
EOF
chmod 600 "$CODEX_AUTH"
log "✓ Codex 配置完成 (Base URL: $API_URL, Model: $CODEX_MODEL)"

# ===== OpenCode =====
log "开始配置 OpenCode..."
OPENCODE_DIR="$HOME_DIR/.config/opencode"
OPENCODE_CONFIG="$OPENCODE_DIR/opencode.json"
mkdir -p "$OPENCODE_DIR"
cat > "$OPENCODE_CONFIG" << EOF
{
  "\$schema": "https://opencode.ai/config.json",
  "model": "cicyai/$DEFAULT_OPENCODE_MODEL",
  "provider": {
    "cicyai": {
      "npm": "@ai-sdk/openai-compatible",
      "api": "openai",
      "models": {
        "claude-haiku-4-5-20251001": {
          "attachment": true,
          "modalities": {
            "input": ["text", "image", "pdf"],
            "output": ["text"]
          },
          "name": "Claude Haiku 4.5"
        },
        "claude-opus-4-6": {
          "attachment": true,
          "modalities": {
            "input": ["text", "image", "pdf"],
            "output": ["text"]
          },
          "name": "Claude Opus 4.6"
        },
        "claude-sonnet-4-6": {
          "attachment": true,
          "modalities": {
            "input": ["text", "image", "pdf"],
            "output": ["text"]
          },
          "name": "Claude Sonnet 4.6"
        },
        "gpt-5.4": {
          "name": "gpt-5.4"
        }
      },
      "name": "cicyAi",
      "options": {
        "apiKey": "$API_KEY",
        "baseURL": "$API_URL"
      }
    }
  },
  "small_model": "cicyai/claude-haiku-4-5-20251001"
}
EOF
log "✓ OpenCode 配置完成 (Base URL: $API_URL, Model: $DEFAULT_OPENCODE_MODEL)"

# ===== OpenClaw =====
log "开始配置 OpenClaw..."
OPENCLAW_DIR="$HOME_DIR/.openclaw"
OPENCLAW_CONFIG="$OPENCLAW_DIR/openclaw.json"
OPENCLAW_ENV="$OPENCLAW_DIR/.env"
OPENCLAW_AGENT_DIR="$OPENCLAW_DIR/agents/main/agent"
OPENCLAW_AUTH_STORE="$OPENCLAW_AGENT_DIR/auth-profiles.json"
OPENCLAW_PRIMARY_MODEL="$(normalize_openclaw_model "${OPENCLAW_PRIMARY_MODEL:-${CICY_OPENCLAW_MODEL:-claude-sonnet-4-6}}")"
OPENCLAW_PROVIDER_API="openai-completions"
OPENCLAW_PROVIDER_COMPAT="openai"
OPENCLAW_PROVIDER_BASE_URL="$API_URL"
OPENCLAW_PROVIDER_RUNTIME_BASE_URL="$API_URL"
if [[ "$OPENCLAW_PRIMARY_MODEL" == claude-* ]]; then
  OPENCLAW_PROVIDER_API="anthropic-messages"
  OPENCLAW_PROVIDER_COMPAT="anthropic"
  OPENCLAW_PROVIDER_BASE_URL="$ANTHROPIC_URL"
  OPENCLAW_PROVIDER_RUNTIME_BASE_URL="$ANTHROPIC_URL"
else
  OPENCLAW_PROVIDER_RUNTIME_BASE_URL="${OPENCLAW_OPENAI_RUNTIME_BASE_URL:-$(default_openclaw_runtime_base_url)}"
fi
mkdir -p "$OPENCLAW_DIR"
if [ -f "$OPENCLAW_CONFIG" ]; then
  OPENCLAW_TOKEN="$(node -e '
const fs = require("fs");
const path = process.argv[1];
try {
  const data = JSON.parse(fs.readFileSync(path, "utf8"));
  process.stdout.write((((data.gateway || {}).auth || {}).token || ""));
} catch (_) {
  process.stdout.write("");
}
' "$OPENCLAW_CONFIG"
)"
fi
if [ -z "${OPENCLAW_TOKEN:-}" ]; then
  OPENCLAW_TOKEN="$(node -e 'process.stdout.write(require("crypto").randomBytes(24).toString("hex"))'
)"
fi
cat > "$OPENCLAW_ENV" << EOF
OPENAI_API_KEY=$API_KEY
OPENAI_BASE_URL=$OPENCLAW_PROVIDER_RUNTIME_BASE_URL
ANTHROPIC_API_KEY=$API_KEY
ANTHROPIC_BASE_URL=$ANTHROPIC_URL
CICY_API_KEY=$API_KEY
OPENCLAW_GATEWAY_TOKEN=$OPENCLAW_TOKEN
EOF
chmod 600 "$OPENCLAW_ENV"
mkdir -p "$OPENCLAW_AGENT_DIR"
rm -f "$OPENCLAW_AUTH_STORE"
patch_openclaw_models() {
  local merge_source="${OPENCLAW_CONFIG_MERGE_SOURCE:-$OPENCLAW_CONFIG}"
  node - "$OPENCLAW_CONFIG" "$OPENCLAW_PROVIDER_RUNTIME_BASE_URL" "$API_KEY" "$OPENCLAW_PRIMARY_MODEL" "$OPENCLAW_PROVIDER_API" "$merge_source" <<'NODE'
const fs = require("fs");
const [configPath, baseUrl, apiKey, primaryModel, providerApi, mergeSourcePath] = process.argv.slice(2);
const cfg = JSON.parse(fs.readFileSync(configPath, "utf8"));
let mergeSourceCfg = null;
try {
  if (mergeSourcePath && fs.existsSync(mergeSourcePath)) {
    mergeSourceCfg = JSON.parse(fs.readFileSync(mergeSourcePath, "utf8"));
  }
} catch (_) {}
function mergeWeixinChannelConfig(targetCfg, sourceCfg) {
  const existingChannel = sourceCfg?.channels?.["openclaw-weixin"];
  if (!existingChannel || typeof existingChannel !== "object") return;
  targetCfg.channels ||= {};
  const currentChannel = targetCfg.channels["openclaw-weixin"];
  if (!currentChannel || typeof currentChannel !== "object") {
    targetCfg.channels["openclaw-weixin"] = existingChannel;
    return;
  }
  const nextChannel = { ...currentChannel, ...existingChannel };
  const currentAccounts = currentChannel.accounts && typeof currentChannel.accounts === "object" ? currentChannel.accounts : {};
  const existingAccounts = existingChannel.accounts && typeof existingChannel.accounts === "object" ? existingChannel.accounts : {};
  if (Object.keys(currentAccounts).length || Object.keys(existingAccounts).length) {
    nextChannel.accounts = { ...currentAccounts, ...existingAccounts };
  }
  targetCfg.channels["openclaw-weixin"] = nextChannel;
}

const claudeModels = [
  { id: "claude-opus-4-6", name: "Claude Opus 4.6", reasoning: true, input: ["text", "image"], contextWindow: 200000, maxTokens: 8192 },
  { id: "claude-sonnet-4-6", name: "Claude Sonnet 4.6", reasoning: true, input: ["text", "image"], contextWindow: 200000, maxTokens: 8192 },
  { id: "claude-haiku-4-5-20251001", name: "Claude Haiku 4.5", reasoning: false, input: ["text", "image"], contextWindow: 200000, maxTokens: 8192 },
];
const openaiModels = [
  { id: "gpt-5.4", name: "gpt-5.4", reasoning: true, input: ["text", "image"], contextWindow: 272000, maxTokens: 4096 },
  { id: "gpt-5.3-codex", name: "gpt-5.3-codex", reasoning: true, input: ["text", "image"], contextWindow: 272000, maxTokens: 4096 },
];
const wanted = providerApi === "anthropic-messages" ? claudeModels : openaiModels;

cfg.agents ||= {};
cfg.agents.defaults ||= {};
cfg.agents.defaults.model ||= {};
cfg.agents.defaults.model.primary = `cicy/${primaryModel}`;
cfg.agents.defaults.models ||= {};
cfg.agents.defaults.models = {};
cfg.agents.defaults.contextTokens = providerApi === "anthropic-messages" ? 200000 : 272000;
cfg.agents.defaults.compaction ||= {};
cfg.agents.defaults.compaction.mode = "safeguard";
cfg.agents.defaults.compaction.reserveTokensFloor = 20000;
for (const model of wanted) {
  cfg.agents.defaults.models[`cicy/${model.id}`] = { alias: model.name };
}

cfg.models ||= {};
cfg.models.mode = "merge";
cfg.models.providers ||= {};
cfg.models.providers.cicy ||= {};
cfg.models.providers.cicy.baseUrl = baseUrl;
cfg.models.providers.cicy.apiKey = apiKey;
cfg.models.providers.cicy.api = providerApi;

const existing = Array.isArray(cfg.models.providers.cicy.models) ? cfg.models.providers.cicy.models : [];
const byId = new Map(existing.map((item) => [item && item.id, item]));
for (const model of wanted) {
  byId.set(model.id, {
    ...(byId.get(model.id) || {}),
    ...model,
    api: providerApi,
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
  });
}
cfg.models.providers.cicy.models = wanted.map((model) => byId.get(model.id));

cfg.gateway ||= {};
cfg.gateway.controlUi ||= {};
cfg.gateway.controlUi.allowInsecureAuth = true;
cfg.gateway.controlUi.dangerouslyDisableDeviceAuth = true;
cfg.gateway.controlUi.dangerouslyAllowHostHeaderOriginFallback = true;
cfg.gateway.controlUi.allowedOrigins = ["*"];
mergeWeixinChannelConfig(cfg, mergeSourceCfg);

const next = JSON.stringify(cfg, null, 2);
const prev = fs.readFileSync(configPath, "utf8");
if (prev !== next) {
  fs.writeFileSync(configPath, next);
}
NODE
}
openclaw_config_ready() {
  [ -f "$OPENCLAW_CONFIG" ] || return 1
  node - "$OPENCLAW_CONFIG" "$OPENCLAW_PROVIDER_RUNTIME_BASE_URL" "$OPENCLAW_PRIMARY_MODEL" "$OPENCLAW_PROVIDER_API" "$OPENCLAW_TOKEN" <<'NODE'
const fs = require("fs");
const [configPath, baseUrl, primaryModel, providerApi, token] = process.argv.slice(2);
try {
  const cfg = JSON.parse(fs.readFileSync(configPath, "utf8"));
  const provider = cfg.models?.providers?.cicy;
  const ok =
    !!provider &&
    provider.baseUrl === baseUrl &&
    provider.api === providerApi &&
    cfg.agents?.defaults?.model?.primary === `cicy/${primaryModel}` &&
    cfg.gateway?.mode === "local" &&
    cfg.gateway?.auth?.mode === "token" &&
    cfg.gateway?.auth?.token === token &&
    Number(cfg.gateway?.port || 0) === 18789 &&
    cfg.gateway?.bind === "loopback";
  process.exit(ok ? 0 : 1);
} catch (_) {
  process.exit(1);
}
NODE
}
OPENCLAW_CONFIG_BACKUP=""
if [ -f "$OPENCLAW_CONFIG" ]; then
  OPENCLAW_CONFIG_BACKUP=$(mktemp)
  cp "$OPENCLAW_CONFIG" "$OPENCLAW_CONFIG_BACKUP"
fi
OPENCLAW_CONFIG_MERGE_SOURCE="${OPENCLAW_CONFIG_BACKUP:-$OPENCLAW_CONFIG}"
if openclaw_config_ready; then
  patch_openclaw_models
  log "✓ OpenClaw 配置完成 (cached config)"
elif command -v openclaw >/dev/null 2>&1; then
  log "OpenClaw 正在执行 official onboard flow..."
  OPENCLAW_RESET_FLAGS=()
  if [ -f "$OPENCLAW_CONFIG" ] && grep -Eq '"provider"[[:space:]]*:[[:space:]]*"anthropic"|"api"[[:space:]]*:[[:space:]]*"openai-responses"|"providers"[[:space:]]*:[[:space:]]*\{[[:space:]]*"openai"' "$OPENCLAW_CONFIG"; then
    OPENCLAW_RESET_FLAGS=(--reset --reset-scope config+creds+sessions)
  fi
  openclaw onboard \
    --accept-risk \
    --mode local \
    --flow manual \
    --non-interactive \
    --auth-choice custom-api-key \
    --custom-provider-id cicy \
    --custom-compatibility "$OPENCLAW_PROVIDER_COMPAT" \
    --custom-base-url "$OPENCLAW_PROVIDER_BASE_URL" \
    --custom-model-id "$OPENCLAW_PRIMARY_MODEL" \
    --custom-api-key "$API_KEY" \
    --secret-input-mode plaintext \
    --gateway-bind loopback \
    --gateway-auth token \
    --gateway-token "$OPENCLAW_TOKEN" \
    --gateway-port 18789 \
    --no-install-daemon \
    --skip-channels \
    --skip-health \
    --skip-search \
    --skip-skills \
    --skip-ui \
    "${OPENCLAW_RESET_FLAGS[@]}" \
    >/tmp/openclaw-onboard.log 2>&1 || {
      log "OpenClaw onboard failed:"
      sed -n '1,160p' /tmp/openclaw-onboard.log
      exit 1
    }
  patch_openclaw_models
  log "✓ OpenClaw 配置完成 (official onboard flow)"
else
  cat > "$OPENCLAW_CONFIG" << EOF
{
  "agents": {
    "defaults": {
      "model": {
        "primary": "cicy/$OPENCLAW_PRIMARY_MODEL"
      }
    }
  },
  "models": {
    "mode": "merge",
    "providers": {
      "cicy": {
        "baseUrl": "$OPENCLAW_PROVIDER_RUNTIME_BASE_URL",
        "apiKey": "$API_KEY",
        "api": "$OPENCLAW_PROVIDER_API",
        "models": [
          {
            "id": "$OPENCLAW_PRIMARY_MODEL",
            "name": "$OPENCLAW_PRIMARY_MODEL",
            "reasoning": true,
            "input": ["text", "image"],
            "api": "$OPENCLAW_PROVIDER_API"
          }
        ]
      }
    }
  },
  "gateway": {
    "mode": "local",
    "auth": {
      "mode": "token",
      "token": "$OPENCLAW_TOKEN"
    },
    "controlUi": {
      "allowInsecureAuth": true,
      "dangerouslyDisableDeviceAuth": true,
      "dangerouslyAllowHostHeaderOriginFallback": true,
      "allowedOrigins": ["*"]
    }
  }
}
EOF
  patch_openclaw_models
  log "✓ OpenClaw 配置完成 (fallback custom provider)"
fi
if [ -n "$OPENCLAW_CONFIG_BACKUP" ]; then
  rm -f "$OPENCLAW_CONFIG_BACKUP"
fi

printf '\n'
log "=== 配置完成 ==="
log "Claude Code: $ANTHROPIC_URL | $DEFAULT_CLAUDE_MODEL"
log "Codex:       $API_URL | $CODEX_MODEL"
log "OpenCode:    $API_URL | $DEFAULT_OPENCODE_MODEL"
log "OpenClaw:    $OPENCLAW_CONFIG"
log "OpenClaw Model: cicy/$OPENCLAW_PRIMARY_MODEL"
