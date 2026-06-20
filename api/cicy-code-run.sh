#!/bin/bash
# supervisor → cicy-code launcher.
#
# Execs the STABLE symlink (not a versioned path) so updates only have to
# repoint the link + `supervisorctl restart cicy-code`. Builds argv from env,
# mirroring the historical build_app_argv contract:
#   CICY_PUBLIC=1  → --public   (opt in to a 0.0.0.0 bind; default is loopback)
#   ENABLE_CDN=1   → --cdn       (serve SPA/ttyd from R2)
# plus any extra container args the entrypoint captured.
set -euo pipefail

HOME_DIR="${HOME:-/home/cicy}"
BIN="$HOME_DIR/.local/bin/cicy-code"
ARGS_FILE="$HOME_DIR/cicy-ai/.cicy/cicy-code.args"

args=()
case " ${CICY_PUBLIC:-} " in *" 1 "*|*" true "*|*" TRUE "*|*" True "*|*" yes "*|*" YES "*|*" on "*|*" ON "*) args+=(--public);; esac
case " ${ENABLE_CDN:-} "  in *" 1 "*|*" true "*|*" TRUE "*|*" True "*|*" yes "*|*" YES "*|*" on "*|*" ON "*) args+=(--cdn);; esac

if [ -f "$ARGS_FILE" ]; then
  while IFS= read -r line; do
    [ -n "$line" ] && args+=("$line")
  done < "$ARGS_FILE"
fi

exec "$BIN" "${args[@]}"
