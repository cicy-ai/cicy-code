#!/usr/bin/env bash
set -euo pipefail

container="${1:-cicy-code-dev}"
pane_id="${PANE_ID:-w-10005:main.0}"
mode="${MODE:-${2:-allow-all-actions}}"
restore_after="${RESTORE_AFTER:-0}"
agent_session="${pane_id%%:*}"
worker_dir="/home/cicy/cicy-ai/workers/${agent_session}"
db_path="/home/cicy/cicy-ai/db/data.db"
original_allow=""

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

echo "[test] container=$container pane=$pane_id base_url=$base_url"

case "$mode" in
  allow-all-actions)
    scenario_name="allow-all-actions"
    scenario_allow="1"
    ;;
  default-permissions)
    scenario_name="default-permissions"
    scenario_allow="0"
    ;;
  *)
    echo "unsupported mode: $mode" >&2
    echo "supported modes: allow-all-actions, default-permissions" >&2
    exit 1
    ;;
esac

case "$restore_after" in
  0|1)
    ;;
  *)
    echo "unsupported RESTORE_AFTER: $restore_after" >&2
    echo "supported values: 0, 1" >&2
    exit 1
    ;;
esac

cleanup_copilot_state() {
  docker exec "$container" sh -lc "
set -e
rm -rf /home/cicy/.copilot
rm -f /home/cicy/.npm-global/bin/copilot
rm -rf /home/cicy/.npm-global/lib/node_modules/@github/copilot
"
}

set_allow_all_actions() {
  local value="$1"
  docker exec "$container" sh -lc "python3 - '$db_path' '$pane_id' '$value' <<'PY'
import sqlite3
import sys

db_path, pane_id, value = sys.argv[1], sys.argv[2], int(sys.argv[3])
conn = sqlite3.connect(db_path)
try:
    cur = conn.cursor()
    cur.execute(
        'UPDATE agent_config SET allow_all_actions=?, updated_at=CURRENT_TIMESTAMP WHERE pane_id=?',
        (value, pane_id),
    )
    if cur.rowcount != 1:
        raise SystemExit(f'pane not found or duplicated: {pane_id}')
    conn.commit()
finally:
    conn.close()
PY"
}

get_allow_all_actions() {
  docker exec "$container" sh -lc "python3 - '$db_path' '$pane_id' <<'PY'
import sqlite3
import sys

db_path, pane_id = sys.argv[1], sys.argv[2]
conn = sqlite3.connect(db_path)
try:
    cur = conn.cursor()
    cur.execute('SELECT COALESCE(allow_all_actions, 0) FROM agent_config WHERE pane_id=?', (pane_id,))
    row = cur.fetchone()
    if not row:
        raise SystemExit(f'pane not found: {pane_id}')
    print(int(row[0]), end='')
finally:
    conn.close()
PY"
}

restore_original_state() {
  if [ -z "${original_allow:-}" ]; then
    return
  fi
  if [ "$restore_after" != "1" ]; then
    return
  fi
  echo
  echo "[test] restoring allow_all_actions=${original_allow}"
  set_allow_all_actions "$original_allow"
  echo "[test] restarting pane to restore original state"
  curl -fsS -X POST "${base_url}/api/panes/${pane_id}/restart?token=${token}" >/dev/null || true
}

original_allow="$(get_allow_all_actions)"
trap restore_original_state EXIT

run_scenario() {
  local name="$1"
  local allow_value="$2"
  local start_ts
  local seen_ready=0
  local last_capture=""
  local recent_logs=""
  local boot_script=""

  echo
  echo "[test] scenario=${name} allow_all_actions=${allow_value}"
  echo "[test] cleaning Copilot install and runtime state"
  cleanup_copilot_state
  echo "[test] updating allow_all_actions=${allow_value}"
  set_allow_all_actions "$allow_value"

  start_ts="$(date '+%Y-%m-%dT%H:%M:%S')"
  echo "[test] restarting pane through API"
  curl -fsS -X POST "${base_url}/api/panes/${pane_id}/restart?token=${token}" >/dev/null

  echo "[test] waiting for Copilot startup to reach login screen"
  for _ in $(seq 1 900); do
    capture="$(docker exec "$container" sh -lc "tmux capture-pane -t '$pane_id' -p -S -160" 2>/dev/null || true)"
    if printf '%s\n' "$capture" | grep -q 'GitHub Copilot'; then
      if printf '%s\n' "$capture" | grep -q 'Please use /login to sign in to use Copilot\|You must be logged in to send messages'; then
        seen_ready=1
        break
      fi
    fi
    last_capture="$capture"
    sleep 0.2
  done

  boot_script="$(docker exec "$container" sh -lc "cat '$worker_dir/.cicy/boot.sh'" 2>/dev/null || true)"
  recent_logs="$(docker logs --since "$start_ts" "$container" 2>&1 | tail -n 80 || true)"
  echo "[test] recent runtime logs"
  printf '%s\n' "$recent_logs"

  if [ "$seen_ready" != "1" ]; then
    echo "[test] FAIL (${name}): pane did not reach Copilot login screen" >&2
    printf '%s\n' "$last_capture" >&2
    exit 1
  fi
  if ! printf '%s\n' "$boot_script" | grep -q "copilot --yolo"; then
    echo "[test] FAIL (${name}): boot.sh missing copilot --yolo" >&2
    exit 1
  fi
  if ! printf '%s\n' "$boot_script" | grep -q "trustedFolders"; then
    echo "[test] FAIL (${name}): boot.sh missing trustedFolders setup" >&2
    exit 1
  fi

  echo "[test] PASS (${name})"
}

run_scenario "$scenario_name" "$scenario_allow"

echo
echo "[test] PASS: Copilot startup scenario completed"
if [ "$restore_after" != "1" ]; then
  echo "[test] restore skipped (RESTORE_AFTER=0)"
fi
