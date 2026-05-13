#!/bin/bash
# eng - English grammar correction
# Usage: eng <text>
set -euo pipefail
API="http://127.0.0.1:${API_PORT:-8008}"
TOKEN=$(jq -r '.api_token' ~/cicy-ai/global.json 2>/dev/null)
TEXT="${*:?Usage: eng <text>}"
curl -sf -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "$API/api/ai/correct" -d "{\"text\":$(echo "$TEXT" | jq -Rs .)}" | jq -r '.result'
