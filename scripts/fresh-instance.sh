#!/usr/bin/env bash
# Build cicy-code and run a clean instance as a DEDICATED throwaway macOS USER —
# real isolation (own uid → own HOME, own tmux socket, own ~/cicy-ai), on
# isolated ports, ALONGSIDE your real instance on :8008.
#
# The instance is seeded with the api_token + LLM creds from YOUR real
# ~/cicy-ai/global.json (via CICY_API_TOKEN / CICY_AI_GATEWAY_LLM_* env). Reusing
# your REAL api_token (not a random one) keeps the token STABLE across runs, so a
# browser/Electron tab that cached it stays authenticated instead of 401-ing.
#
# Usage:
#   scripts/fresh-instance.sh                          # user=cicyfresh, port 8208
#   CICY_TEST_USER=cicyt2 CICY_TEST_PORT=8308 scripts/fresh-instance.sh
#   SKIP_BUILD=1 scripts/fresh-instance.sh             # reuse api/cicy-code
#
# Needs passwordless sudo (sysadminctl add/deleteUser, run-as-user).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"

TEST_USER="${CICY_TEST_USER:-cicyfresh}"
TEST_PASS="${CICY_TEST_PASS:-$TEST_USER}"
TEST_HOME="/Users/$TEST_USER"
PORT="${CICY_TEST_PORT:-8208}"
# Non-conflicting internal ports (override individually if you want).
MITM_PORT="${CICY_TEST_MITM_PORT:-8207}"
MIHOMO_PORT="${CICY_TEST_MIHOMO_PORT:-9002}"
MIHOMO_CTRL_PORT="${CICY_TEST_MIHOMO_CTRL_PORT:-19002}"
PPROF_PORT="${CICY_TEST_PPROF_PORT:-6160}"

# --- safety guards -----------------------------------------------------------
CUR="$(whoami)"
[ "$TEST_USER" != "$CUR" ] || { echo "[fresh] REFUSING: test user == current user ($CUR)"; exit 1; }
case "$TEST_USER" in
  ""|root|daemon|nobody|_*|admin) echo "[fresh] REFUSING unsafe user '$TEST_USER'"; exit 1 ;;
esac
# Never touch the real instance: the test port must NOT be 8008.
[ "$PORT" != "8008" ] || { echo "[fresh] REFUSING: port 8008 is the real instance — pick another (e.g. 8208)"; exit 1; }
# Never operate on a user we shouldn't (root / the primary console user).
if id "$TEST_USER" >/dev/null 2>&1; then
  FUID="$(id -u "$TEST_USER")"
  { [ "$FUID" = "0" ] || [ "$FUID" = "501" ]; } && { echo "[fresh] REFUSING: $TEST_USER uid=$FUID is system/primary"; exit 1; }
fi

case "$(uname -m)" in arm64) ARCH=arm64 ;; *) ARCH=amd64 ;; esac

echo "[fresh] repo=$REPO  user=$TEST_USER  home=$TEST_HOME  port=$PORT"
echo "[fresh]   ports: mitm=$MITM_PORT mihomo=$MIHOMO_PORT/$MIHOMO_CTRL_PORT pprof=$PPROF_PORT"

# 1) build the native binary (unless SKIP_BUILD=1)
if [ "${SKIP_BUILD:-0}" = "1" ]; then
  echo "[fresh] 1/5 skip build (SKIP_BUILD=1)"
else
  echo "[fresh] 1/5 build darwin/$ARCH"
  ( cd "$REPO" && ./build.sh build darwin "$ARCH" )
fi
BIN="$REPO/api/cicy-code"
[ -x "$BIN" ] || { echo "[fresh] missing binary: $BIN"; exit 1; }

# 2) pull api_token + LLM creds from YOUR real ~/cicy-ai/global.json (read as the
#    real user, before we drop into the throwaway one). Injected via env below so
#    the clean instance is logged-in + LLM-ready, with a STABLE api_token.
REAL_GLOBAL="$HOME/cicy-ai/global.json"
API_TOKEN=""; LLM_KEY=""; LLM_URL=""
if [ -f "$REAL_GLOBAL" ]; then
  { read -r API_TOKEN; read -r LLM_KEY; read -r LLM_URL; } < <(python3 - "$REAL_GLOBAL" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    d = {}
tok = str(d.get("api_token", "") or "")
key = url = ""
items = ((d.get("providers") or {}).get("items") or []) if isinstance(d.get("providers"), dict) else []
for want in ("defaultAnthropic", "defaultOpenAi"):
    for it in items:
        if isinstance(it, dict) and it.get("key") == want and str(it.get("apiKey", "") or "").strip():
            key = str(it.get("apiKey", "")).strip(); url = str(it.get("url", "") or "").strip(); break
    if key:
        break
print(tok); print(key); print(url)
PY
)
  echo "[fresh]   from real global.json: api_token=$([ -n "$API_TOKEN" ] && echo yes || echo no), llm_key=$([ -n "$LLM_KEY" ] && echo yes || echo no)"
fi

# 3) stop the prior test instance (by unique port, NEVER a broad pkill cicy-code),
#    then DELETE + RECREATE the throwaway macOS user for genuine fresh isolation.
echo "[fresh] 3/5 stop prior instance + delete/recreate user $TEST_USER"
sudo -n pkill -u "$TEST_USER" -f "cicy-code" 2>/dev/null || true
pkill -f "cicy-code --public --port ${PORT}\b" 2>/dev/null || true
for pid in $(lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -t 2>/dev/null || true); do
  echo "[fresh]   killing listener on :$PORT (pid $pid)"; kill "$pid" 2>/dev/null || true
done
sleep 0.5
if id "$TEST_USER" >/dev/null 2>&1; then
  OLDUID="$(id -u "$TEST_USER")"
  sudo -n sysadminctl -deleteUser "$TEST_USER" >/dev/null 2>&1 || true
  sudo -n rm -rf "$TEST_HOME" "/tmp/tmux-$OLDUID" 2>/dev/null || true
fi
sudo -n sysadminctl -addUser "$TEST_USER" -fullName "$TEST_USER" -password "$TEST_PASS" >/dev/null 2>&1 || true
sleep 1
id "$TEST_USER" >/dev/null 2>&1 || { echo "[fresh] failed to create user $TEST_USER"; exit 1; }

# 4) deploy the freshly built binary into the throwaway user's home.
echo "[fresh] 4/5 deploy binary to $TEST_HOME/cicy-code"
sudo -n mkdir -p "$TEST_HOME"
sudo -n cp "$BIN" "$TEST_HOME/cicy-code"
sudo -n chown "$TEST_USER:staff" "$TEST_HOME/cicy-code"
sudo -n chmod 755 "$TEST_HOME/cicy-code"

# 5) start as the throwaway user under a clean env -i with isolated ports + creds.
echo "[fresh] 5/5 start cicy-code on http://127.0.0.1:$PORT as $TEST_USER"
NODE_DIR="$(dirname "$(command -v node 2>/dev/null || echo /usr/local/bin/node)")"
ENV_ARGS=(
  HOME="$TEST_HOME"
  PATH="$NODE_DIR:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
  TERM="${TERM:-xterm-256color}"
  CICY_MITM_HTTP_PORT="$MITM_PORT"
  CICY_MIHOMO_PORT="$MIHOMO_PORT"
  CICY_MIHOMO_CONTROLLER="http://127.0.0.1:$MIHOMO_CTRL_PORT"
  CICY_PPROF_PORT="$PPROF_PORT"
)
[ -n "$API_TOKEN" ] && ENV_ARGS+=( CICY_API_TOKEN="$API_TOKEN" )
[ -n "$LLM_KEY" ]   && ENV_ARGS+=( CICY_AI_GATEWAY_LLM_API_KEY="$LLM_KEY" )
[ -n "$LLM_URL" ]   && ENV_ARGS+=( CICY_AI_GATEWAY_LLM_ENDPOINT="$LLM_URL" )
exec sudo -n -u "$TEST_USER" env -i "${ENV_ARGS[@]}" "$TEST_HOME/cicy-code" --public --port "$PORT"
