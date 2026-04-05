#!/bin/bash

# 配置 Codex
# 用法: 
#   ./setup-codex.sh [provider]      配置 Codex
#   ./setup-codex.sh show             显示当前配置
# 示例: ./setup-codex.sh 2000Run

set -e

ACTION="${1:-}"
GLOBAL_JSON="$HOME/global.json"

show_current() {
    CONFIG_FILE="$HOME/.codex/config.toml"
    AUTH_FILE="$HOME/.codex/auth.json"
    
    if [ ! -f "$CONFIG_FILE" ]; then
        echo "错误: Codex 未配置"
        exit 1
    fi
    
    echo "=== 当前 Codex 配置 ==="
    echo ""
    
    # 获取 model_provider
    MODEL_PROVIDER=$(grep "^model_provider" "$CONFIG_FILE" | cut -d'"' -f2)
    MODEL=$(grep "^model" "$CONFIG_FILE" | head -1 | cut -d'"' -f2)
    BASE_URL=$(grep "^base_url" "$CONFIG_FILE" | head -1 | cut -d'"' -f2)
    
    echo "Provider: $MODEL_PROVIDER"
    echo "Model: $MODEL"
    echo "Base URL: $BASE_URL"
    echo ""
    echo "=== 配置文件 ==="
    echo "$CONFIG_FILE"
    echo "复制"
    cat "$CONFIG_FILE"
    echo ""
    echo "=== 认证文件 ==="
    echo "$AUTH_FILE"
    echo "复制"
    cat "$AUTH_FILE"
}

if [ "$ACTION" = "show" ]; then
    show_current
    exit 0
fi

PROVIDER="${1:-2000Run}"

if [ ! -f "$GLOBAL_JSON" ]; then
    echo "错误: 找不到 $GLOBAL_JSON"
    exit 1
fi

# 读取配置
CONFIG=$(python3 - "$PROVIDER" "$GLOBAL_JSON" <<'PY'
import json, sys
provider = sys.argv[1]
with open(sys.argv[2]) as f:
    data = json.load(f)
p = data.get("ai", {}).get("provider", {}).get(provider, {})
if not p:
    print("error: provider not found")
    sys.exit(1)
api_key = p.get("apiKey", "")
api_url = p.get("apiUrl", "")
model = p.get("codexModel", "gpt-5.4")
reasoning = p.get("codexReasoning", "xhigh")
# provider name (lowercase for config)
provider_name = provider.lower()
print(api_key + "|" + api_url + "|" + model + "|" + reasoning + "|" + provider_name)
PY
)

if [[ "$CONFIG" == "error: provider not found" ]]; then
    echo "错误: provider '$PROVIDER' 不存在"
    exit 1
fi

IFS='|' read -r API_KEY API_URL CODEX_MODEL REASONING PROVIDER_NAME <<< "$CONFIG"

if [ -z "$API_KEY" ]; then
    echo "错误: apiKey 未配置"
    exit 1
fi

echo "配置 Codex (Provider: $PROVIDER)..."

HOME_DIR="${HOME:-$(getent passwd "$(id -u)" 2>/dev/null | cut -d: -f6)}"
CODEX_DIR="$HOME_DIR/.codex"
CODEX_CONFIG="$CODEX_DIR/config.toml"
CODEX_AUTH="$CODEX_DIR/auth.json"
mkdir -p "$CODEX_DIR"

# 统一配置格式
cat > "$CODEX_CONFIG" << EOF
model = "$CODEX_MODEL"
model_reasoning_effort = "$REASONING"
disable_response_storage = true
sandbox_mode = "danger-full-access"
windows_wsl_setup_acknowledged = true
approval_policy = "never"
profile = "auto-max"
file_opener = "vscode"
web_search = "cached"
suppress_unstable_features_warning = true
model_provider = "$PROVIDER_NAME"

[history]
persistence = "save-all"

[tui]
notifications = true

[shell_environment_policy]
inherit = "all"
ignore_default_excludes = false

[sandbox_workspace_write]
network_access = true

[features]
plan_tool = true
apply_patch_freeform = true
view_image_tool = true

[profiles.auto-max]
approval_policy = "never"
sandbox_mode = "workspace-write"

[model_providers.$PROVIDER_NAME]
name = "$PROVIDER_NAME"
base_url = "$API_URL"
wire_api = "responses"
requires_openai_auth = true

[projects."$HOME_DIR/projects/new-api"]
trust_level = "trusted"
EOF

cat > "$CODEX_AUTH" << EOF
{
  "OPENAI_API_KEY": "$API_KEY"
}
EOF

chmod 600 "$CODEX_AUTH"

echo "✓ Codex 配置完成"
echo "  Provider: $PROVIDER_NAME"
echo "  Base URL: $API_URL"
echo "  Model: $CODEX_MODEL"
echo "  Reasoning: $REASONING"
