#!/bin/bash
# tm - Tmux manager CLI (via cicy-code API)
# Usage: tm <command> [args]
set -euo pipefail

API_PORT="${API_PORT:-8008}"
API="http://127.0.0.1:$API_PORT"
TOKEN=$(jq -r '.api_token' ~/cicy-ai/global.json 2>/dev/null || echo "")
AUTH=(-H "Authorization: Bearer $TOKEN")

post() { curl -sf "${AUTH[@]}" -H "Content-Type: application/json" -X POST "$API$1" -d "$2"; }
get()  { curl -sf "${AUTH[@]}" "$API$1"; }

case "${1:-help}" in
  ls)       get /api/tmux/panes | jq -r '.panes[]|"\(.pane_id)\t\(.role)\t\(.title)"' ;;
  status)   get "/api/tmux/status${2:+?pane=$2}" ;;
  tree)     get /api/tmux/tree ;;
  windows)  get /api/tmux/windows ;;
  capture)  post /api/tmux/capture_pane "{\"pane_id\":\"${2:?pane_id required}\"}" ;;
  msg)      post /api/tmux/send "{\"pane_id\":\"${2:?pane_id required}\",\"text\":\"${*:3}\"}" ;;
  msg_wait) post /api/tmux/send_wait "{\"pane_id\":\"${2:?pane_id required}\",\"text\":\"${3:?text required}\",\"timeout\":${4:-60}}" ;;
  send-keys) post /api/tmux/send-keys "{\"pane_id\":\"${2:?pane_id required}\",\"keys\":\"${*:3}\"}" ;;
  create)   post /api/tmux/create "{\"name\":\"${2:?name required}\"}" ;;
  restart)  post /api/tmux/restart_all '{}' ;;
  clear)    post /api/tmux/clear "{\"pane\":\"${2:?pane_id required}\"}" ;;
  *)
    echo "Usage: tm <command> [args]"
    echo "  ls                          List panes"
    echo "  status [pane]               Pane status"
    echo "  tree                        Tmux tree"
    echo "  capture <pane>              Capture pane"
    echo "  msg <pane> <text>           Send message (auto Enter)"
    echo "  msg_wait <pane> <text> [timeout]"
    echo "  send-keys <pane> <keys>     Send raw keys"
    echo "  create <name>               Create pane"
    echo "  restart                     Restart all"
    echo "  clear <pane>                Clear pane"
    ;;
esac
echo
