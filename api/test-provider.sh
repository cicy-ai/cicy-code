#!/bin/bash

# 测试 API Provider
# 用法: ./test-provider.sh [provider]

PROVIDER="${1:-FHL}"
GLOBAL_JSON="$HOME/global.json"

echo "=== 测试 Provider: $PROVIDER ==="
echo ""

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
print(api_key + "|" + api_url)
PY
)

if [[ "$CONFIG" == "error: provider not found" ]]; then
    echo "错误: provider '$PROVIDER' 不存在"
    exit 1
fi

IFS='|' read -r API_KEY API_URL <<< "$CONFIG"

echo "API Key: ${API_KEY:0:10}..."
echo "API URL: $API_URL"
echo ""

# 测试 1: GET /v1/models
echo "1. 测试 /v1/models..."
RESPONSE=$(curl -s -w "\n%{http_code}" -H "Authorization: Bearer $API_KEY" "$API_URL/v1/models" 2>&1)
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
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$API_URL/v1/responses" \
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
