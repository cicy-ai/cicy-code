#!/bin/bash
# gpt-chat - Multi-turn AI conversation
# Usage:
#   gpt-chat <message>          Chat
#   gpt-chat --clear            Clear history
#   gpt-chat --system <text>    Set system prompt
#   gpt-chat --show-system      Show system prompt
set -euo pipefail

API="http://127.0.0.1:${API_PORT:-8008}"
TOKEN=$(jq -r '.api_token' ~/cicy-ai/global.json 2>/dev/null)
HIST=~/Private/data/gpt-chat-history.json
SYS=~/Private/data/gpt-chat-system.txt
mkdir -p ~/Private/data

case "${1:-}" in
  --clear) rm -f "$HIST"; echo "History cleared."; exit 0 ;;
  --system) shift; echo "$*" > "$SYS"; echo "System prompt set."; exit 0 ;;
  --show-system) cat "$SYS" 2>/dev/null || echo "(none)"; exit 0 ;;
  "") echo "Usage: gpt-chat <message>"; exit 1 ;;
esac

# Build messages array
MSGS="[]"
if [ -f "$SYS" ]; then
  SYS_TEXT=$(cat "$SYS" | jq -Rs .)
  MSGS=$(echo "$MSGS" | jq --argjson s "$SYS_TEXT" '. + [{"role":"system","content":($s)}]')
fi
if [ -f "$HIST" ]; then
  MSGS=$(jq -s '.[0] + .[1]' <(echo "$MSGS") "$HIST")
fi
USER_MSG=$(echo "$*" | jq -Rs .)
MSGS=$(echo "$MSGS" | jq --argjson m "$USER_MSG" '. + [{"role":"user","content":($m)}]')

# Call API
RESULT=$(curl -sf -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "$API/api/ai/chat" -d "{\"messages\":$MSGS}" | jq -r '.result')

echo "$RESULT"

# Save history
ASST_MSG=$(echo "$RESULT" | jq -Rs .)
if [ -f "$HIST" ]; then
  jq --argjson u "$USER_MSG" --argjson a "$ASST_MSG" \
    '. + [{"role":"user","content":($u)},{"role":"assistant","content":($a)}]' "$HIST" > "$HIST.tmp"
else
  jq -n --argjson u "$USER_MSG" --argjson a "$ASST_MSG" \
    '[{"role":"user","content":($u)},{"role":"assistant","content":($a)}]' > "$HIST.tmp"
fi
mv "$HIST.tmp" "$HIST"
