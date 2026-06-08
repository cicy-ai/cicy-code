#!/usr/bin/env bash
set -euo pipefail

container="${1:-cicy-code-dev}"
pane_id="${PANE_ID:-w-1001:main.0}"
agent_id="${pane_id%%:*}"

json_value() {
  local file_path="$1"
  local key_path="$2"
  docker exec "$container" sh -lc "python3 - '$file_path' '$key_path' <<'PY'
import json, sys
path, key_path = sys.argv[1], sys.argv[2]
with open(path, 'r', encoding='utf-8') as f:
    data = json.load(f)
value = data
for part in key_path.split('.'):
    if not part:
        continue
    value = value.get(part, '') if isinstance(value, dict) else ''
print(value or '', end='')
PY"
}

if ! docker ps --format '{{.Names}}' | grep -qx "$container"; then
  echo "container not running: $container" >&2
  exit 1
fi

host_port="$(docker port "$container" 8008/tcp | tail -n 1 | sed 's/.*://')"
if [ -z "$host_port" ]; then
  echo "cannot resolve host port for $container" >&2
  exit 1
fi
base_url="${CICY_BASE_URL:-http://127.0.0.1:${host_port}}"
token="$(json_value /home/cicy/cicy-ai/global.json api_token)"
if [ -z "$token" ]; then
  echo "cannot resolve api_token from container global.json" >&2
  exit 1
fi

page_client_id="${PAGE_CLIENT_ID:-}"
if [ -z "$page_client_id" ]; then
  clients_json="$(docker exec "$container" sh -lc "ln -sf /home/cicy/cicy-ai/global.json /home/cicy/global.json && /home/cicy/projects/cicy-skills/bin/agent-webpage clients")"
  page_client_id="$(
    CLIENTS_JSON="$clients_json" python3 - "$agent_id" <<'PY'
import json, os, sys
agent_id = sys.argv[1]
data = json.loads(os.environ["CLIENTS_JSON"])
clients = data.get(agent_id, {})
for client_id in clients:
    if client_id.endswith(':code-ext'):
        continue
    print(client_id, end='')
    break
PY
  )"
fi
if [ -z "$page_client_id" ]; then
  echo "cannot resolve webpage client for agent ${agent_id}" >&2
  exit 1
fi

wait_for_page_client() {
  for _ in $(seq 1 40); do
    if docker exec "$container" sh -lc "ln -sf /home/cicy/cicy-ai/global.json /home/cicy/global.json && /home/cicy/projects/cicy-skills/bin/agent-webpage clients" \
      | grep -q "\"${page_client_id}\""; then
      return 0
    fi
    sleep 0.5
  done
  echo "page client did not reconnect: ${page_client_id}" >&2
  return 1
}

echo "[test] container=$container pane=$pane_id base_url=$base_url client=$page_client_id"

history_api() {
  local query="$1"
  curl -fsS "${base_url}/api/agents/current-history/${agent_id}?token=${token}${query}"
}

send_prompt() {
  local text="$1"
  curl -fsS -X POST "${base_url}/api/tmux/send?token=${token}" \
    -H 'Content-Type: application/json' \
    --data "{\"pane_id\":\"${pane_id}\",\"text\":\"${text}\"}" >/dev/null
}

wait_for_prompt() {
  local prompt="$1"
  for _ in $(seq 1 150); do
    local body
    body="$(history_api '&limit=20')"
    if HISTORY_BODY_JSON="$body" python3 - "$prompt" <<'PY'
import json, os, sys
prompt = sys.argv[1]
data = json.loads(os.environ["HISTORY_BODY_JSON"])
items = data.get('items') or []
for item in items:
    if str(item.get('q') or '').strip() == prompt:
        raise SystemExit(0)
raise SystemExit(1)
PY
    then
      return 0
    fi
    sleep 0.4
  done
  echo "prompt did not appear in current-history: $prompt" >&2
  return 1
}

prefix="history-e2e-$(date +%s)"
prompts=(
  "Reply with exactly one word: ${prefix}-1"
  "Reply with exactly one word: ${prefix}-2"
  "Reply with exactly one word: ${prefix}-3"
  "Reply with exactly one word: ${prefix}-4"
  "Reply with exactly one word: ${prefix}-5"
  "Reply with exactly one word: ${prefix}-6"
)

for prompt in "${prompts[@]}"; do
  echo "[test] send: $prompt"
  send_prompt "$prompt"
  wait_for_prompt "$prompt"
done

page1="$(history_api '&limit=2')"
page2_cursor="$(
  PAGE1_JSON="$page1" python3 - <<'PY'
import json, os
data = json.loads(os.environ["PAGE1_JSON"])
print(int(data.get('next_before') or 0))
PY
)"
page1_check="$(
  PAGE1_JSON="$page1" python3 - "$prefix" <<'PY'
import json, os, sys
prefix = sys.argv[1]
data = json.loads(os.environ["PAGE1_JSON"])
items = data.get('items') or []
qs = [str(item.get('q') or '') for item in items]
ok = {
    'len': len(items),
    'has_more': bool(data.get('has_more')),
    'match_count': sum(1 for q in qs if prefix in q),
}
print(json.dumps(ok))
if ok['len'] != 2 or not ok['has_more'] or ok['match_count'] != 2:
    raise SystemExit(1)
PY
)"
echo "[test] page1=${page1_check}"

page2="$(history_api "&limit=2&before=${page2_cursor}")"
page2_check="$(
  PAGE2_JSON="$page2" python3 - "$prefix" <<'PY'
import json, os, sys
prefix = sys.argv[1]
data = json.loads(os.environ["PAGE2_JSON"])
items = data.get('items') or []
qs = [str(item.get('q') or '') for item in items]
ok = {
    'len': len(items),
    'match_count': sum(1 for q in qs if prefix in q),
    'questions': qs,
}
print(json.dumps(ok, ensure_ascii=False))
if ok['len'] < 2 or ok['match_count'] < 2:
    raise SystemExit(1)
PY
)"
echo "[test] page2=${page2_check}"

docker exec "$container" sh -lc "/home/cicy/projects/cicy-skills/bin/agent-webpage exec-js '(() => { location.reload(); return \"reloading\"; })()' '${page_client_id}'" >/dev/null
sleep 2
wait_for_page_client

docker exec "$container" sh -lc "/home/cicy/projects/cicy-skills/bin/agent-webpage exec-js '(() => { const btn = document.querySelector(\"[data-id=\\\"cli-content-tab-history\\\"]\"); if (btn) btn.click(); return JSON.stringify({clicked: !!btn}); })()' '${page_client_id}'" >/dev/null
sleep 2

frontend_state_1="$(
  docker exec "$container" sh -lc "/home/cicy/projects/cicy-skills/bin/agent-webpage exec-js 'JSON.stringify({hasView: !!document.querySelector(\"[data-id=\\\"current-history-view\\\"]\"), turns: document.querySelectorAll(\"[data-id=\\\"current-history-turn\\\"]\").length, hasLoadMore: !!document.querySelector(\"[data-id=\\\"current-history-load-more\\\"]\"), text: document.querySelector(\"[data-id=\\\"current-history-list\\\"]\")?.innerText || \"\"})' '${page_client_id}'"
)"
FRONTEND_STATE_1_JSON="$frontend_state_1" python3 - "$prefix" <<'PY'
import json, os, sys
prefix = sys.argv[1]
data = json.loads(os.environ["FRONTEND_STATE_1_JSON"])
if not data.get('hasView'):
    raise SystemExit('history view not visible')
if int(data.get('turns') or 0) < 2:
    raise SystemExit('history turn count too small')
if not data.get('hasLoadMore'):
    raise SystemExit('load more button missing')
if prefix not in str(data.get('text') or ''):
    raise SystemExit('history text missing prefix on first render')
print("[test] frontend_page1=" + json.dumps({
    'turns': data.get('turns'),
    'hasLoadMore': data.get('hasLoadMore'),
}, ensure_ascii=False))
PY

docker exec "$container" sh -lc "/home/cicy/projects/cicy-skills/bin/agent-webpage exec-js '(() => { const btn = document.querySelector(\"[data-id=\\\"current-history-load-more\\\"]\"); if (btn) btn.click(); return JSON.stringify({clicked: !!btn}); })()' '${page_client_id}'" >/dev/null
sleep 2

frontend_state_2="$(
  docker exec "$container" sh -lc "/home/cicy/projects/cicy-skills/bin/agent-webpage exec-js 'JSON.stringify({turns: document.querySelectorAll(\"[data-id=\\\"current-history-turn\\\"]\").length, text: document.querySelector(\"[data-id=\\\"current-history-list\\\"]\")?.innerText || \"\"})' '${page_client_id}'"
)"
FRONTEND_STATE_2_JSON="$frontend_state_2" python3 - "$prefix" <<'PY'
import json, os, sys
prefix = sys.argv[1]
data = json.loads(os.environ["FRONTEND_STATE_2_JSON"])
text = str(data.get('text') or '')
for suffix in ('-1', '-2', '-3', '-4', '-5', '-6'):
    if f'{prefix}{suffix}' not in text:
        raise SystemExit(f'missing prompt {prefix}{suffix} after load more')
if int(data.get('turns') or 0) < 6:
    raise SystemExit('history turn count too small after load more')
print("[test] frontend_page2=" + json.dumps({'turns': data.get('turns')}, ensure_ascii=False))
PY

echo "[test] PASS: current-history pagination and frontend rendering"
