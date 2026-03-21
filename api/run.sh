#!/bin/bash
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

ENV_FILE="$PROJECT_ROOT/.env"
if [ -f "$ENV_FILE" ]; then
    set -a; source "$ENV_FILE"; set +a
fi
export HOME="$(eval echo ~$(whoami))"
export MYSQL_DSN="${MYSQL_DSN:-root:cicy-code@tcp(localhost:3306)/cicy_code}"
export REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"
export PORT="${PORT:-8008}"
export TERM=xterm-256color
BIN="$SCRIPT_DIR/cicy-code-api"
[ -f "$BIN" ] || BIN="$SCRIPT_DIR/cicy-code"
exec "$BIN" --public --dev
