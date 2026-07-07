#!/bin/bash
# Hot-update cicy-code WITHOUT bouncing the container, the cloudflared tunnel,
# or any user daemons. The cicy-desktop-mac model: install the new version
# side-by-side, repoint one symlink, restart only the supervisor program.
#
#   cicy-code-update            # → latest
#   cicy-code-update 2.3.16     # → a pinned version
#
# Layout (version store lives under ~/.local, next to the on-PATH symlink):
#   ~/.local/cicy-code/<ver>/bin/cicy-code            (npm prefix install)
#   ~/.local/bin/cicy-code  ->  …/<ver>/bin/cicy-code  ← THE symlink we swap
#   ~/cicy-ai/runtime/versions.json  { "cicy-code": { "current": "<ver>" } }
# versions.json stays under cicy-ai/runtime — it's the SHARED pointer file the
# mihomo runtime store & the Go server (setup.go) also read; only cicy-code's
# binary tree moved to ~/.local. Override the store dir with CICY_CODE_STORE.
set -euo pipefail

HOME_DIR="${HOME:-/home/cicy}"
REG="${NPM_REGISTRY:-https://registry.npmmirror.com}"
RT="${CICY_CODE_STORE:-$HOME_DIR/.local/cicy-code}"
LINK="$HOME_DIR/.local/bin/cicy-code"
VERSIONS="$HOME_DIR/cicy-ai/runtime/versions.json"
SVCTL="supervisorctl -c /etc/supervisor/supervisord.conf"

want="${1:-latest}"
log() { printf '[cicy-code-update] %s\n' "$*"; }

# Resolve the concrete version number (so the install dir is version-named and
# re-runs are idempotent / cacheable).
ver="$(npm view "cicy-code@${want}" version --registry "$REG" | tail -n1)"
[ -n "$ver" ] || { log "could not resolve cicy-code@${want}"; exit 1; }
dest="$RT/$ver"

if [ -x "$dest/bin/cicy-code" ]; then
  log "v$ver already installed → repointing"
else
  log "installing v$ver from $REG"
  rm -rf "$dest"
  mkdir -p "$dest"
  npm install -g "cicy-code@$ver" --prefix "$dest" --registry "$REG"
fi

mkdir -p "$(dirname "$LINK")"
ln -sfn "$dest/bin/cicy-code" "$LINK"
# Keep the npm-global bin (first on PATH) following the canonical link too, so
# interactive `cicy-code` matches what supervisor runs.
ln -sfn "$LINK" "$HOME_DIR/.npm-global/bin/cicy-code" 2>/dev/null || true

# Record current pointer (merge into the shared versions.json).
mkdir -p "$(dirname "$VERSIONS")"
tmp="$(mktemp)"
if [ -s "$VERSIONS" ] && jq -e . "$VERSIONS" >/dev/null 2>&1; then
  jq --arg v "$ver" '.["cicy-code"] = ((.["cicy-code"] // {}) + {current: $v})' "$VERSIONS" > "$tmp"
else
  jq -n --arg v "$ver" '{"cicy-code": {current: $v}}' > "$tmp"
fi
mv "$tmp" "$VERSIONS"

log "symlink → $(readlink -f "$LINK")"

# Reload: restart only this program (no-op gracefully if supervisord isn't up,
# e.g. when called as part of first-boot setup before supervisord starts).
if $SVCTL pid >/dev/null 2>&1; then
  log "restarting via supervisor"
  $SVCTL restart cicy-code
else
  log "supervisord not running yet; symlink set, will start on boot"
fi
log "done → v$ver"
