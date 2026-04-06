#!/bin/bash

# 测试 API Provider
# 用法: ./test-provider.sh [provider]

PROVIDER="${1:-}"
GLOBAL_JSON="$HOME/global.json"

# 读取配置
CONFIG=$(python3 - "$PROVIDER" "$GLOBAL_JSON" <<'PY'
import json, sys
requested = (sys.argv[1] or "").strip()
with open(sys.argv[2]) as f:
    data = json.load(f)

aliases = {
    "200run": "2000Run",
    "2000run": "2000Run",
    "cicyai": "cicyAi",
}

def canonical(name):
    value = str(name or "").strip()
    if not value:
        return ""
    return aliases.get(value.lower(), value)

ai = data.get("ai", {}) if isinstance(data.get("ai", {}), dict) else {}
provider_map = ai.get("provider", {}) if isinstance(ai.get("provider", {}), dict) else {}
provider = canonical(requested or ai.get("currentProvider") or "cicyAi") or "cicyAi"

cicy_ai_base = str(data.get("cicyAiUrl", "") or "").strip().rstrip("/")
defaults = {
    "2000Run": {
        "apiKey": data.get("2000RunApikey", ""),
        "apiUrl": "http://2000.run:6543/v1",
    },
    "cicyAi": {
        "apiKey": data.get("cicyAiapikey", ""),
        "apiUrl": (cicy_ai_base + "/v1") if cicy_ai_base else "https://cicy-ai.com/v1",
    },
}

config = dict(defaults.get(provider, {}))
for key, value in provider_map.items():
    if canonical(key) == provider and isinstance(value, dict):
        config.update({k: v for k, v in value.items() if v not in ("", None)})

if not config:
    print("error: provider not found")
    sys.exit(1)

api_key = config.get("apiKey", "")
api_url = config.get("apiUrl", "")
print(api_key + "|" + api_url + "|" + provider)
PY
)

if [[ "$CONFIG" == "error: provider not found" ]]; then
    echo "错误: provider '$PROVIDER' 不存在"
    exit 1
fi

IFS='|' read -r API_KEY API_URL PROVIDER_LABEL <<< "$CONFIG"

PROVIDER="${PROVIDER_LABEL:-$PROVIDER}"
API_BASE="${API_URL%/}"
if [[ "$API_BASE" == */v1 ]]; then
    MODELS_URL="$API_BASE/models"
    RESPONSES_URL="$API_BASE/responses"
else
    MODELS_URL="$API_BASE/v1/models"
    RESPONSES_URL="$API_BASE/v1/responses"
fi

echo "=== 测试 Provider: $PROVIDER ==="
echo ""

echo "API Key: ${API_KEY:0:10}..."
echo "API URL: $API_URL"
echo ""

# 测试 1: GET /v1/models
echo "1. 测试 /v1/models..."
RESPONSE=$(curl -s -w "\n%{http_code}" -H "Authorization: Bearer $API_KEY" "$MODELS_URL" 2>&1)
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | head -n -1)

if [ "$HTTP_CODE" = "200" ]; then
    echo "✓ /v1/models OK (HTTP $HTTP_CODE)"
    MODEL_COUNT=$(echo "$BODY" | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d.get('data',[])))" 2>/dev/null || echo "N/A")
    echo "  可用模型: $MODEL_COUNT 个"
else
    echo "✗ /v1/models 失败 (HTTP $HTTP_CODE)"
    echo "  $BODY" | head -3
fi
echo ""

# 测试 2: POST /v1/responses
echo "2. 测试 /v1/responses..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$RESPONSES_URL" \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"max_tokens":10}' 2>&1)
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | head -n -1)

if [ "$HTTP_CODE" = "200" ]; then
    echo "✓ /v1/responses OK (HTTP $HTTP_CODE)"
elif [ "$HTTP_CODE" = "502" ]; then
    echo "✗ /v1/responses 失败 (HTTP $HTTP_CODE) - Bad Gateway"
elif [ "$HTTP_CODE" = "401" ]; then
    echo "✗ /v1/responses 失败 (HTTP $HTTP_CODE) - 未授权"
else
    echo "✗ /v1/responses 失败 (HTTP $HTTP_CODE)"
    echo "  $BODY" | head -3
fi
echo ""

echo "=== 测试完成 ==="
