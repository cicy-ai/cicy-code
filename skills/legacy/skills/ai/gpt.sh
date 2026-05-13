#!/bin/bash
# gpt - Quick AI chat
# Usage: gpt <question>
set -euo pipefail
API="http://127.0.0.1:${API_PORT:-8008}"
TOKEN=$(jq -r '.api_token' ~/cicy-ai/global.json 2>/dev/null)
TEXT="${*:?Usage: gpt <question>}"
curl -sf -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "$API/api/ai/chat" -d "{\"text\":$(echo "$TEXT" | jq -Rs .)}" | jq -r '.result'
