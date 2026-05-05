#!/usr/bin/env bash
set -euo pipefail

container="${1:-cicy-code-dev}"
pane_id="${PANE_ID:-w-10008:main.0}"
agent_session="${pane_id%%:*}"
worker_dir="/home/cicy/cicy-ai/workers/${agent_session}"
db_path="/home/cicy/cicy-ai/db/data.db"

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

db_value() {
  local sql="$1"
  docker exec "$container" sh -lc "python3 - '$db_path' '$sql' <<'PY'
import sqlite3, sys
db_path, sql = sys.argv[1], sys.argv[2]
conn = sqlite3.connect(db_path)
try:
    row = conn.execute(sql).fetchone()
    if row:
        print(row[0] if row[0] is not None else '', end='')
finally:
    conn.close()
PY"
}

pane_db_value() {
  local column="$1"
  docker exec "$container" sh -lc "python3 - '$db_path' '$pane_id' '$column' <<'PY'
import sqlite3, sys
db_path, pane_id, column = sys.argv[1], sys.argv[2], sys.argv[3]
allowed = {'agent_type', 'ttyd_port', 'workspace', 'title'}
if column not in allowed:
    raise SystemExit(f'unsupported column: {column}')
conn = sqlite3.connect(db_path)
try:
    row = conn.execute(f'SELECT {column} FROM agent_config WHERE pane_id=?', (pane_id,)).fetchone()
    if row:
        print(row[0] if row[0] is not None else '', end='')
finally:
    conn.close()
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

agent_type="$(pane_db_value agent_type)"
if [ "$agent_type" != "openclaw" ]; then
  echo "pane is not openclaw: pane=$pane_id agent_type=$agent_type" >&2
  exit 1
fi

echo "[test] container=$container pane=$pane_id base_url=$base_url"
echo "[test] restarting OpenClaw pane through API"
start_ts="$(date '+%Y-%m-%dT%H:%M:%S')"
curl -fsS -X POST "${base_url}/api/panes/${pane_id}/restart?token=${token}" >/dev/null

seen_gateway=0
seen_tui=0
seen_missing_config=0
seen_weixin_login=0
last_capture=""
gateway_log=""

echo "[test] waiting for OpenClaw gateway/TUI startup"
for _ in $(seq 1 1200); do
  capture="$(docker exec "$container" sh -lc "tmux capture-pane -t '$pane_id' -p -S -220" 2>/dev/null || true)"
  if printf '%s\n' "$capture" | grep -q 'OpenClaw base config missing'; then
    seen_missing_config=1
  fi
  if printf '%s\n' "$capture" | grep -q '微信未登录，正在右侧打开登录窗口\|channels login --channel openclaw-weixin'; then
    seen_weixin_login=1
  fi
  if printf '%s\n' "$capture" | grep -q 'OpenClaw gateway 已就绪'; then
    seen_gateway=1
  fi
  gateway_log="$(docker exec "$container" sh -lc "cat /tmp/openclaw-gateway-${agent_session}.log 2>/dev/null || true")"
  if printf '%s\n' "$gateway_log" | grep -q '\[gateway\].*ready'; then
    seen_gateway=1
  fi
  if printf '%s\n' "$capture" | grep -q '正在打开 OpenClaw TUI 会话'; then
    seen_tui=1
  fi
  if [ "$seen_gateway" = "1" ] && [ "$seen_tui" = "1" ]; then
    break
  fi
  last_capture="$capture"
  sleep 0.5
done

boot_script="$(docker exec "$container" sh -lc "cat '$worker_dir/.cicy/boot.sh'" 2>/dev/null || true)"
pane_count="$(docker exec "$container" sh -lc "tmux list-panes -t '${agent_session}' 2>/dev/null | wc -l" 2>/dev/null || echo 0)"
recent_logs="$(docker logs --since "$start_ts" "$container" 2>&1 | tail -n 120 || true)"
gateway_log="$(docker exec "$container" sh -lc "cat /tmp/openclaw-gateway-${agent_session}.log 2>/dev/null || true")"

echo "[test] pane_count=$pane_count"
echo "[test] recent runtime logs"
printf '%s\n' "$recent_logs"

if [ "$seen_missing_config" = "1" ]; then
  echo "[test] FAIL: OpenClaw base config missing during startup" >&2
  printf '%s\n' "$last_capture" >&2
  exit 1
fi
if [ "$seen_weixin_login" = "1" ]; then
  echo "[test] FAIL: OpenClaw started default WeChat login flow" >&2
  printf '%s\n' "$last_capture" >&2
  exit 1
fi
if [ "$seen_gateway" != "1" ]; then
  echo "[test] FAIL: OpenClaw gateway did not become ready" >&2
  printf '%s\n' "$last_capture" >&2
  exit 1
fi
if [ "$seen_tui" != "1" ]; then
  echo "[test] FAIL: OpenClaw TUI did not start" >&2
  printf '%s\n' "$last_capture" >&2
  exit 1
fi
if [ "$pane_count" != "1" ]; then
  echo "[test] FAIL: expected one tmux pane; default WeChat login may have split a pane" >&2
  printf '%s\n' "$last_capture" >&2
  exit 1
fi
if ! printf '%s\n' "$boot_script" | grep -q "export CICY_OPENCLAW_MODEL='gpt-5.5'"; then
  echo "[test] FAIL: boot.sh missing OpenClaw gpt-5.5 model export" >&2
  exit 1
fi
if ! printf '%s\n' "$gateway_log" | grep -q 'agent model: cicy/gpt-5.5'; then
  echo "[test] FAIL: gateway did not start with cicy/gpt-5.5" >&2
  printf '%s\n' "$gateway_log" >&2
  exit 1
fi

echo "[test] PASS: OpenClaw startup reached gateway/TUI without default WeChat login"
