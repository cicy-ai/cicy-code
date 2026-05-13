#!/bin/bash
# tg - Telegram CLI
# Usage:
#   tg send <message>
#   tg photo <url> [caption]
set -euo pipefail
API="http://127.0.0.1:${API_PORT:-8008}"
TOKEN=$(jq -r '.api_token' ~/cicy-ai/global.json 2>/dev/null)

case "${1:-}" in
  send)  shift; curl -sf -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
           "$API/api/tg/send" -d "{\"text\":$(echo "$*" | jq -Rs .)}" | jq -r 'if .ok then "✓ Sent." else "✗ \(.description)" end' ;;
  photo) shift; URL="${1:?url required}"; CAP="${2:-}"
         curl -sf -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
           "$API/api/tg/photo" -d "{\"photo\":\"$URL\",\"caption\":\"$CAP\"}" | jq -r 'if .ok then "✓ Sent." else "✗ \(.description)" end' ;;
  *)     echo "Usage: tg <send|photo> [args]" ;;
esac
