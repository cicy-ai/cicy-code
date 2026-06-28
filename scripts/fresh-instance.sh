#!/usr/bin/env bash
# Build cicy-code, wipe + recreate a throwaway HOME, and start a clean instance
# on isolated ports — for testing a fresh install without touching your real
# ~/cicy-ai. cicy-code resolves its state dir from $HOME, so pointing HOME at a
# scratch dir gives a fully isolated instance (own global.json / db / workers /
# logs). All conflicting ports are overridden so it can run ALONGSIDE your normal
# instance on :8008.
#
# Usage:
#   scripts/fresh-instance.sh                 # default: HOME=~/cicy-test-home, port 8208
#   CICY_TEST_HOME=~/foo CICY_TEST_PORT=8308 scripts/fresh-instance.sh
#   SKIP_BUILD=1 scripts/fresh-instance.sh    # reuse the existing api/cicy-code

set -euo pipefail

# Repo root = parent of this script's dir, so the script is path-independent.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"

TEST_HOME="${CICY_TEST_HOME:-$HOME/cicy-test-home}"
PORT="${CICY_TEST_PORT:-8208}"
# Non-conflicting internal ports (override individually if you want).
MITM_PORT="${CICY_TEST_MITM_PORT:-8207}"
MIHOMO_PORT="${CICY_TEST_MIHOMO_PORT:-9002}"
MIHOMO_CTRL_PORT="${CICY_TEST_MIHOMO_CTRL_PORT:-19002}"
PPROF_PORT="${CICY_TEST_PPROF_PORT:-6160}"

# --- safety: never wipe a real home or a dangerous path ----------------------
case "$TEST_HOME" in
  "" | "/" | "$HOME" | "/Users" | "/Users/$(whoami)" | /Users/*/ )
    echo "[fresh] REFUSING to wipe TEST_HOME='$TEST_HOME' (looks like a real home)"; exit 1 ;;
esac
if [ "$TEST_HOME" = "$HOME" ]; then
  echo "[fresh] REFUSING: TEST_HOME equals \$HOME"; exit 1
fi
# Never touch the real instance: the test port must NOT be 8008.
if [ "$PORT" = "8008" ]; then
  echo "[fresh] REFUSING: port 8008 is the real instance — pick another (e.g. 8208)"; exit 1
fi

# arch for the native build (arm64 on Apple Silicon, else amd64).
case "$(uname -m)" in
  arm64) ARCH=arm64 ;;
  *)     ARCH=amd64 ;;
esac

echo "[fresh] repo=$REPO"
echo "[fresh] TEST_HOME=$TEST_HOME  port=$PORT  (mitm=$MITM_PORT mihomo=$MIHOMO_PORT/$MIHOMO_CTRL_PORT pprof=$PPROF_PORT)"

# 1) build the native binary (unless SKIP_BUILD=1)
if [ "${SKIP_BUILD:-0}" = "1" ]; then
  echo "[fresh] 1/4 skip build (SKIP_BUILD=1)"
else
  echo "[fresh] 1/4 build darwin/$ARCH"
  ( cd "$REPO" && ./build.sh build darwin "$ARCH" )
fi
BIN="$REPO/api/cicy-code"
[ -x "$BIN" ] || { echo "[fresh] missing binary: $BIN"; exit 1; }

# 2) kill the previous instance for THIS test user, free the port, wipe the home.
#    Kill the process BEFORE removing the dir so it can't recreate files mid-wipe.
echo "[fresh] 2/4 stop previous instance + wipe $TEST_HOME"
# Identify the test instance ONLY by its unique --port (never a broad
# `pkill cicy-code`, which would kill your real :8008 instance too).
# (a) the prior test cicy-code, matched by its --port in the command line
pkill -f "cicy-code --public --port ${PORT}\b" 2>/dev/null && echo "[fresh]   killed prior cicy-code --port $PORT" || true
# (b) whatever is still holding the port
for pid in $(lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -t 2>/dev/null || true); do
  echo "[fresh]   killing listener on :$PORT (pid $pid)"; kill "$pid" 2>/dev/null || true
done
sleep 0.4
rm -rf "$TEST_HOME"

# 3) recreate it
echo "[fresh] 3/4 create $TEST_HOME"
mkdir -p "$TEST_HOME"

# Pull api_token + LLM creds from YOUR real ~/cicy-ai/global.json (read here while
# HOME is still the real one) and inject via env — so the clean instance is
# logged-in and LLM-ready without re-setup. Same env contract as dev.py --docker.
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

# 4) start the clean instance with isolated HOME + ports + injected creds
echo "[fresh] 4/4 start cicy-code on http://127.0.0.1:$PORT"
ENV_ARGS=(
  HOME="$TEST_HOME"
  PATH="$PATH"
  TERM="${TERM:-xterm-256color}"
  CICY_MITM_HTTP_PORT="$MITM_PORT"
  CICY_MIHOMO_PORT="$MIHOMO_PORT"
  CICY_MIHOMO_CONTROLLER="http://127.0.0.1:$MIHOMO_CTRL_PORT"
  CICY_PPROF_PORT="$PPROF_PORT"
)
[ -n "$API_TOKEN" ] && ENV_ARGS+=( CICY_API_TOKEN="$API_TOKEN" )
[ -n "$LLM_KEY" ]   && ENV_ARGS+=( CICY_AI_GATEWAY_LLM_API_KEY="$LLM_KEY" )
[ -n "$LLM_URL" ]   && ENV_ARGS+=( CICY_AI_GATEWAY_LLM_ENDPOINT="$LLM_URL" )
exec env -i "${ENV_ARGS[@]}" "$BIN" --public --port "$PORT"
