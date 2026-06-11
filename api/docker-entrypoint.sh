#!/bin/bash

set -euo pipefail

HOME_DIR="${HOME:-/home/cicy}"
CICY_ROOT_DIR="$HOME_DIR/cicy-ai"
STATE_DIR="$CICY_ROOT_DIR/.cicy"
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
  const nextToken = envToken || currentToken || `cicy_${crypto.randomBytes(16).toString('hex')}`;
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

start_cloudflared() {
  local tunnel_token="${CICY_CLOUDFLARED_TOKEN:-${CF_TUNNEL_TOKEN:-${CLOUDFLARED_TOKEN:-}}}"
  local log_file="$STATE_DIR/cloudflared.log"

  if [ -z "$tunnel_token" ]; then
    return 0
  fi

  if ! command -v cloudflared >/dev/null 2>&1; then
    log "cloudflared not found"
    exit 1
  fi

  log "starting cloudflared"
  nohup cloudflared tunnel run --token "$tunnel_token" >"$log_file" 2>&1 &
  export CICY_CLOUDFLARED_PID="$!"
  sleep 2
  if ! kill -0 "$CICY_CLOUDFLARED_PID" >/dev/null 2>&1; then
    log "cloudflared exited early"
    tail -n 50 "$log_file" >&2 || true
    exit 1
  fi
}

# Install cicy-code from npm at startup so the image stays a stable base
# environment (no baked binary) and the version floats with npm. CN-friendly
# via npmmirror. Pin with CICY_CODE_VERSION; override registry with NPM_REGISTRY.
# If a `cicy-code` is already on PATH (e.g. a legacy baked image) and no version
# is pinned, reuse it.
ensure_cicy_code() {
  local reg="${NPM_REGISTRY:-https://registry.npmmirror.com}"
  local spec="cicy-code"
  if [ -n "${CICY_CODE_VERSION:-}" ]; then
    spec="cicy-code@${CICY_CODE_VERSION}"
  elif command -v cicy-code >/dev/null 2>&1; then
    log "cicy-code already on PATH; skipping install"
    return 0
  fi
  log "installing ${spec} from ${reg}"
  npm install -g "$spec" --registry="$reg"
}

build_app_argv() {
  if [ "$#" -eq 0 ]; then
    set -- --public
  fi
  case " $* " in
    *" --public "*) ;;
    *) set -- --public "$@" ;;
  esac
  # Add --agents from the CICY_AGENTS env var
  case " $* " in
    *" --agents="*) ;;
    *)
      if [ -n "${CICY_AGENTS:-}" ]; then
        set -- --agents="${CICY_AGENTS}" "$@"
      fi
      ;;
  esac
  # ENABLE_CDN=true → serve the App SPA + ttyd bundle from Cloudflare R2.
  # The R2 prefixes are baked into every binary; --cdn just activates them.
  case " $* " in
    *" --cdn "*) ;;
    *)
      case "${ENABLE_CDN:-}" in
        1|true|TRUE|True|yes|YES|on|ON) set -- --cdn "$@" ;;
      esac
      ;;
  esac
  printf '%s\0' "$@"
}

main() {
  ensure_cicy_base
  ensure_shell_init_files
  ensure_runtime_api_token
  start_cloudflared
  ensure_cicy_code

  mapfile -d '' app_argv < <(build_app_argv "$@")
  log "starting cicy-code"
  exec cicy-code "${app_argv[@]}"
}

main "$@"
