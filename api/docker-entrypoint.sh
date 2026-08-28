#!/bin/bash

set -euo pipefail

HOME_DIR="${HOME:-/home/cicy}"
CICY_ROOT_DIR="$HOME_DIR/cicy-ai"
STATE_DIR="$CICY_ROOT_DIR/.cicy"
LOG_DIR="$HOME_DIR/logs" # logs (supervisord) + the cicy-code launch args live here, not under cicy-ai/.cicy
GLOBAL_JSON_PATH="$CICY_ROOT_DIR/global.json"

log() {
  printf '[entrypoint] %s\n' "$*"
}

ensure_cicy_base() {
  sudo -n mkdir -p "$CICY_ROOT_DIR" "$STATE_DIR"
  sudo -n chown cicy:cicy "$CICY_ROOT_DIR" "$STATE_DIR"
  sudo -n chmod 0755 "$CICY_ROOT_DIR" "$STATE_DIR"
}

ensure_shell_init_files() {
  local tmux_line='[ -f "$HOME/.cicy_tmux.conf" ] && source "$HOME/.cicy_tmux.conf"'
  local cicy_tmux_path="$HOME_DIR/.cicy_tmux.conf"
  local tmux_conf_path="$HOME_DIR/.tmux.conf"
  local proxy_json_path="$HOME_DIR/proxy.json"
  local bashrc_path="$HOME_DIR/.bashrc"
  local profile_path="$HOME_DIR/.profile"
  local bash_profile_path="$HOME_DIR/.bash_profile"
  local profile_line='[ -n "${BASH_VERSION:-}" ] && [ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"'
  local bash_profile_line='[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"'

  touch "$bashrc_path" "$profile_path" "$bash_profile_path"
  if [ -e "$cicy_tmux_path" ]; then
    chown cicy:cicy "$cicy_tmux_path" 2>/dev/null || true
    chmod u+rw,go+r "$cicy_tmux_path" 2>/dev/null || true
  fi
  if [ -e "$tmux_conf_path" ]; then
    chown cicy:cicy "$tmux_conf_path" 2>/dev/null || true
    chmod u+rw,go+r "$tmux_conf_path" 2>/dev/null || true
  fi
  if [ -e "$proxy_json_path" ]; then
    chown cicy:cicy "$proxy_json_path" 2>/dev/null || true
    chmod u+rw,go-rwx "$proxy_json_path" 2>/dev/null || true
  fi
  chown cicy:cicy "$bashrc_path" "$profile_path" "$bash_profile_path" 2>/dev/null || true
  chmod u+rw "$bashrc_path" "$profile_path" "$bash_profile_path" 2>/dev/null || true

  grep -Fqx "$tmux_line" "$bashrc_path" || printf '\n%s\n' "$tmux_line" >>"$bashrc_path"
  grep -Fvx '[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"' "$profile_path" >"$profile_path.tmp" || true
  mv "$profile_path.tmp" "$profile_path"
  grep -Fqx "$profile_line" "$profile_path" || printf '\n%s\n' "$profile_line" >>"$profile_path"
  grep -Fvx '[ -f "$HOME/.profile" ] && . "$HOME/.profile"' "$bash_profile_path" >"$bash_profile_path.tmp" || true
  mv "$bash_profile_path.tmp" "$bash_profile_path"
  grep -Fqx "$bash_profile_line" "$bash_profile_path" || printf '\n%s\n' "$bash_profile_line" >>"$bash_profile_path"
}

ensure_runtime_api_token() {
  mkdir -p "$(dirname "$GLOBAL_JSON_PATH")"
  export CICY_RUNTIME_API_TOKEN
  export CICY_API_TOKEN
  CICY_RUNTIME_API_TOKEN="$(
    node - "$GLOBAL_JSON_PATH" "${CICY_RUNTIME_API_TOKEN:-${CICY_API_TOKEN:-}}" <<'NODE'
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const file = process.argv[2];
const envToken = String(process.argv[3] || '').trim();
const dir = path.dirname(file);
const tmpFile = path.join(dir, `.${path.basename(file)}.tmp-${process.pid}`);

try {
  fs.mkdirSync(dir, { recursive: true });
  let config = {};
  if (fs.existsSync(file)) {
    const raw = fs.readFileSync(file, 'utf8');
    if (raw.trim()) {
      config = JSON.parse(raw);
    }
  }
  if (!config || typeof config !== 'object' || Array.isArray(config)) {
    throw new Error(`invalid global json root: ${file}`);
  }
  const currentToken = typeof config.api_token === 'string' ? config.api_token.trim() : '';
  // The file wins once it has a token: CICY_API_TOKEN only seeds a fresh
  // volume, so rotating the token in global.json survives container restarts.
  const nextToken = currentToken || envToken || `cicy_${crypto.randomBytes(16).toString('hex')}`;
  if (currentToken !== nextToken) {
    config.api_token = nextToken;
    fs.writeFileSync(tmpFile, `${JSON.stringify(config, null, 2)}\n`, 'utf8');
    fs.renameSync(tmpFile, file);
  }
  process.stdout.write(nextToken);
} finally {
  try { fs.rmSync(tmpFile, { force: true }); } catch (_) {}
}
NODE
  )"
  CICY_API_TOKEN="$CICY_RUNTIME_API_TOKEN"
}

# Make cicy-code runnable through the STABLE symlink ~/.local/bin/cicy-code,
# which is what supervisor execs. Updates only repoint this link (see
# cicy-code-update.sh) — the cicy-desktop-mac hot-patch model. The image stays a
# stable BASE environment: no version is baked, it floats with npm (npmmirror,
# CN-friendly). Pin with CICY_CODE_VERSION; override registry with NPM_REGISTRY.
ensure_cicy_code() {
  local link="$HOME_DIR/.local/bin/cicy-code"
  local baked="$HOME_DIR/.npm-global/bin/cicy-code"
  mkdir -p "$(dirname "$link")"

  # Dev image: build.sh docker bakes a locally-built binary as a real file at
  # ~/.npm-global/bin/cicy-code. Point the canonical symlink at it, skip npm.
  if [ -f "$baked" ] && [ ! -L "$baked" ]; then
    log "using baked dev cicy-code binary"
    ln -sfn "$baked" "$link"
    return 0
  fi

  # Already linked and no pin requested → reuse (fast/offline boot).
  if [ -z "${CICY_CODE_VERSION:-}" ] && [ -x "$link" ]; then
    log "cicy-code already linked → $(readlink -f "$link")"
    return 0
  fi

  # First boot / pinned version: install into the versioned runtime store and
  # set the symlink via the same path used for runtime hot-updates.
  /usr/local/bin/cicy-code-update.sh "${CICY_CODE_VERSION:-latest}"
}

# Prepare the SSH server: per-container host keys (baked ones were stripped at
# build so each container is distinct), the privilege-separation dir, and the
# cicy user's authorized_keys. Provide an RSA public key via CICY_SSH_PUBKEY
# (or SSH_PUBKEY / AUTHORIZED_KEYS); multiple keys may be newline-separated.
# Auth policy (RSA pubkey only, no passwords) is in sshd_config.d/cicy.conf.
ensure_ssh() {
  sudo -n mkdir -p /run/sshd
  sudo -n chmod 0755 /run/sshd
  # ssh-keygen -A only creates the host keys that are missing → idempotent.
  sudo -n ssh-keygen -A >/dev/null 2>&1 || log "ssh host-key generation failed"

  local ssh_dir="$HOME_DIR/.ssh"
  local ak="$ssh_dir/authorized_keys"
  mkdir -p "$ssh_dir"
  touch "$ak"
  chmod 700 "$ssh_dir"
  chmod 600 "$ak"

  local keys="${CICY_SSH_PUBKEY:-${SSH_PUBKEY:-${AUTHORIZED_KEYS:-}}}"
  if [ -n "$keys" ]; then
    while IFS= read -r key; do
      [ -z "$key" ] && continue
      grep -qxF "$key" "$ak" || printf '%s\n' "$key" >>"$ak"
    done <<<"$keys"
    log "authorized_keys updated"
  fi
  chown -R cicy:cicy "$ssh_dir" 2>/dev/null || true
}

main() {
  ensure_cicy_base
  ensure_shell_init_files
  ensure_runtime_api_token
  ensure_cicy_code
  ensure_ssh

  mkdir -p "$LOG_DIR" "$CICY_ROOT_DIR/supervisor"

  # Capture any extra container args for the cicy-code wrapper. The env-derived
  # flags (--public/--cdn) are added by the wrapper itself; these are the
  # positional extras that used to be forwarded via `exec cicy-code "$@"`.
  # Lives under ~/logs (kept in sync with ARGS_FILE in cicy-code-run.sh).
  : >"$LOG_DIR/cicy-code.args"
  for arg in "$@"; do printf '%s\n' "$arg" >>"$LOG_DIR/cicy-code.args"; done

  log "starting supervisord (cicy-code + cron + sshd + user daemons)"
  exec supervisord -c /etc/supervisor/supervisord.conf
}

main "$@"
