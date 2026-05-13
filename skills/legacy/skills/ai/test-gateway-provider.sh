#!/bin/bash
set -euo pipefail

GLOBAL_JSON="${GLOBAL_JSON:-$HOME/cicy-ai/global.json}"
GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8008}"
PROVIDER=""
MODEL=""
MESSAGE="hi"
RUN_ALL=false
AGENT_ID="test-agent"

usage() {
  cat <<'EOF'
Usage:
  test-gateway-provider.sh --provider <name> [--model <model>] [--message <text>] [--gateway <url>] [--global-json <path>]
  test-gateway-provider.sh --all [--model <model>] [--message <text>] [--gateway <url>] [--global-json <path>]

Behavior:
  - Reads providers from ~/cicy-ai/global.json (or --global-json)
  - Temporarily switches ai.currentProvider to the target provider during each test
  - Calls the real cicy-code 8008 ai-gateway path
  - Supports single-provider test or all-provider sweep
  - Default message is: hi

Default models when --model is omitted:
  1) gpt-5.5
  2) claude-opus-4-7
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --provider|-p)
      PROVIDER="$2"
      shift 2
      ;;
    --model|-m)
      MODEL="$2"
      shift 2
      ;;
    --message)
      MESSAGE="$2"
      shift 2
      ;;
    --gateway)
      GATEWAY_URL="$2"
      shift 2
      ;;
    --global-json)
      GLOBAL_JSON="$2"
      shift 2
      ;;
    --all)
      RUN_ALL=true
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "Unknown arg: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 1
fi

if [[ ! -f "$GLOBAL_JSON" ]]; then
  echo "global.json not found: $GLOBAL_JSON" >&2
  exit 1
fi

API_TOKEN=$(jq -r '.api_token // empty' "$GLOBAL_JSON")
if [[ -z "$API_TOKEN" ]]; then
  echo "api_token missing in $GLOBAL_JSON" >&2
  exit 1
fi

ORIGINAL_PROVIDER=$(jq -r '.ai.currentProvider // empty' "$GLOBAL_JSON")
AUTH_HEADER="Authorization: Bearer $API_TOKEN"
restore_provider() {
  if [[ -n "$ORIGINAL_PROVIDER" ]]; then
    tmp=$(mktemp)
    jq --arg p "$ORIGINAL_PROVIDER" '.ai.currentProvider = $p' "$GLOBAL_JSON" > "$tmp" && mv "$tmp" "$GLOBAL_JSON"
  fi
}
trap restore_provider EXIT

fetch_provider_list() {
  jq -r '.ai.provider | keys[]' "$GLOBAL_JSON"
}

provider_exists() {
  jq -e --arg p "$1" '.ai.provider[$p]' "$GLOBAL_JSON" >/dev/null 2>&1
}

switch_provider() {
  local provider="$1"
  tmp=$(mktemp)
  jq --arg p "$provider" '.ai.currentProvider = $p' "$GLOBAL_JSON" > "$tmp" && mv "$tmp" "$GLOBAL_JSON"
}

call_gateway() {
  local provider="$1"
  local model="$2"
  local message="$3"
  switch_provider "$provider"

  local path body resp status content err
  if [[ "$model" == claude-* ]]; then
    path="$GATEWAY_URL/api/ai-gateway/anthropic/$AGENT_ID/v1/messages"
    body=$(jq -nc --arg model "$model" --arg message "$message" '{model:$model,max_tokens:64,messages:[{role:"user",content:$message}]}')
  else
    path="$GATEWAY_URL/api/ai-gateway/openai/$AGENT_ID/v1/responses"
    body=$(jq -nc --arg model "$model" --arg message "$message" '{model:$model,input:$message}')
  fi

  resp=$(mktemp)
  status=$(curl -sS -o "$resp" -w '%{http_code}' -X POST "$path" \
    -H "$AUTH_HEADER" \
    -H 'Content-Type: application/json' \
    -d "$body")

  if [[ "$status" == "200" ]]; then
    if [[ "$model" == claude-* ]]; then
      content=$(jq -r '.content[0].text // empty' "$resp" 2>/dev/null || true)
    else
      content=$(jq -r '.output[0].content[0].text // empty' "$resp" 2>/dev/null || true)
    fi
    echo "✅ provider=$provider model=$model status=$status response=$content"
    rm -f "$resp"
    return 0
  fi

  err=$(jq -r '.error.message // .detail // .message // .error // "unknown error"' "$resp" 2>/dev/null || true)
  if [[ -z "$err" ]]; then
    err=$(head -c 240 "$resp")
  fi
  echo "❌ provider=$provider model=$model status=$status error=$err"
  rm -f "$resp"
  return 1
}

run_provider_models() {
  local provider="$1"
  shift
  local models=("$@")
  local rc=0
  for m in "${models[@]}"; do
    call_gateway "$provider" "$m" "$MESSAGE" || rc=1
  done
  return $rc
}

MODELS=()
if [[ -n "$MODEL" ]]; then
  MODELS=("$MODEL")
else
  MODELS=("gpt-5.5" "claude-opus-4-7")
fi

if [[ "$RUN_ALL" == true ]]; then
  rc=0
  while IFS= read -r p; do
    [[ -z "$p" ]] && continue
    run_provider_models "$p" "${MODELS[@]}" || rc=1
  done < <(fetch_provider_list)
  exit $rc
fi

if [[ -z "$PROVIDER" ]]; then
  echo "--provider or --all is required" >&2
  usage >&2
  exit 1
fi

if ! provider_exists "$PROVIDER"; then
  echo "provider not found: $PROVIDER" >&2
  exit 1
fi

run_provider_models "$PROVIDER" "${MODELS[@]}"
