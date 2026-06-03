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

node_eval() {
  node -e "$1" "${@:2}"
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

ensure_legacy_home_links() {
  :
}

persist_runtime_ai_config() {
  mkdir -p "$(dirname "$GLOBAL_JSON_PATH")"
  node - "$GLOBAL_JSON_PATH" \
    "${CICY_AI_PROVIDER:-}" \
    "${CICY_API_KEY:-}" \
    "${CICY_API_URL:-}" \
    "${CICY_ANTHROPIC_URL:-}" \
    "${CICY_DEFAULT_OPENCODE_MODEL:-${CICY_DEFAULT_MODEL:-}}" \
    "${CICY_DEFAULT_CLAUDE_MODEL:-${CICY_CLAUDE_MODEL:-}}" \
    "${CICY_CODEX_MODEL:-}" \
    "${CICY_OPENCLAW_MODEL:-}" <<'NODE'
const fs = require('fs');
const path = require('path');
const [file, providerRaw, apiKey, apiUrl, anthropicUrl, defaultOpencodeModel, defaultClaudeModel, codexModel, openclawModel] = process.argv.slice(2);
const provider = String(providerRaw || '').trim() || 'cicyAi';
const dir = path.dirname(file);
const tmpFile = path.join(dir, `.${path.basename(file)}.tmp-ai-${process.pid}`);

function loadConfig() {
  if (!fs.existsSync(file)) return {};
  const raw = fs.readFileSync(file, 'utf8');
  if (!raw.trim()) return {};
  const parsed = JSON.parse(raw);
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error(`invalid global json root: ${file}`);
  }
  return parsed;
}

const cfg = loadConfig();
cfg.ai = cfg.ai && typeof cfg.ai === 'object' && !Array.isArray(cfg.ai) ? cfg.ai : {};
cfg.ai.provider = cfg.ai.provider && typeof cfg.ai.provider === 'object' && !Array.isArray(cfg.ai.provider) ? cfg.ai.provider : {};
cfg.ai.currentProvider = provider;
const current = cfg.ai.provider[provider] && typeof cfg.ai.provider[provider] === 'object' && !Array.isArray(cfg.ai.provider[provider]) ? cfg.ai.provider[provider] : {};

if (String(apiKey || '').trim()) current.apiKey = String(apiKey).trim();
if (String(apiUrl || '').trim()) current.apiUrl = String(apiUrl).trim();
if (String(anthropicUrl || '').trim()) current.anthropicUrl = String(anthropicUrl).trim();
if (String(defaultOpencodeModel || '').trim()) current.defaultOpencodeModel = String(defaultOpencodeModel).trim();
if (String(defaultClaudeModel || '').trim()) current.defaultClaudeModel = String(defaultClaudeModel).trim();
if (String(codexModel || '').trim()) current.codexModel = String(codexModel).trim();
if (String(openclawModel || '').trim()) current.openclawModel = String(openclawModel).trim();

cfg.ai.provider[provider] = current;
fs.mkdirSync(dir, { recursive: true });
fs.writeFileSync(tmpFile, `${JSON.stringify(cfg, null, 2)}\n`, 'utf8');
fs.renameSync(tmpFile, file);
NODE
}

default_teamcenter_url() {
  if [ -n "${CICY_TEAMCENTER_URL:-}" ]; then
    printf '%s' "${CICY_TEAMCENTER_URL%/}"
    return 0
  fi

  if [ -n "${CICY_API_URL:-}" ]; then
    node - "${CICY_API_URL}" <<'NODE'
const raw = String(process.argv[2] || '').trim().replace(/\/+$/, '');
if (!raw) process.exit(1);
if (/\/v1$/i.test(raw)) {
  process.stdout.write(raw.replace(/\/v1$/i, ''));
} else {
  process.stdout.write(raw);
}
NODE
    return 0
  fi

  printf '%s' "https://cicy-ai.com"
}

bootstrap_team_runtime() {
  local team_token="${CICY_TEAM_TOKEN:-}"
  local teamcenter_url
  local bootstrap_path="${CICY_TEAMCENTER_BOOTSTRAP_PATH:-/api/runtime/team/bootstrap}"
  local instance_key="${CICY_INSTANCE_KEY:-$(hostname)}"
  local instance_label="${CICY_INSTANCE_LABEL:-$instance_key}"
  local bootstrap_url payload response_file status

  if [ -z "$team_token" ]; then
    return 0
  fi

  teamcenter_url="$(default_teamcenter_url)"
  export CICY_TEAMCENTER_URL="$teamcenter_url"
  if [ -z "${CICY_RUNTIME_KIND:-}" ]; then
    export CICY_RUNTIME_KIND="container"
  fi
  bootstrap_url="${teamcenter_url%/}${bootstrap_path}"
  payload="$(
    node - "$instance_key" "$instance_label" "${PORT:-8008}" "${CICY_RUNTIME_API_TOKEN:-${CICY_API_TOKEN:-}}" "${CICY_RUNTIME_KIND:-container}" <<'NODE'
const [instanceKey, instanceLabel, port, runtimeApiToken, runtimeKind] = process.argv.slice(2);
process.stdout.write(JSON.stringify({
  instance_key: instanceKey,
  instance_label: instanceLabel,
  port: Number(port || 8008),
  api_token: runtimeApiToken || '',
  runtime_kind: runtimeKind || 'container'
}));
NODE
  )"
  response_file="$(mktemp)"
  status="$(
    curl -sS -o "$response_file" -w '%{http_code}' \
      -X POST "$bootstrap_url" \
      -H "Authorization: Bearer $team_token" \
      -H "Content-Type: application/json" \
      --data "$payload"
  )"
  if [ "${status#2}" = "$status" ]; then
    log "team bootstrap failed: ${status}"
    cat "$response_file" >&2 || true
    rm -f "$response_file"
    exit 1
  fi

  eval "$(
    node - "$response_file" <<'NODE'
const fs = require('fs');
const file = process.argv[2];
const raw = JSON.parse(fs.readFileSync(file, 'utf8'));
const data = raw && typeof raw.data === 'object' ? raw.data : raw;
function pick(...keys) {
  for (const key of keys) {
    const value = data?.[key];
    if (typeof value === 'string' && value.trim()) return value.trim();
    if (typeof value === 'number') return String(value);
  }
  return '';
}
function bool(...keys) {
  for (const key of keys) {
    if (typeof data?.[key] === 'boolean') return data[key] ? 'true' : 'false';
  }
  return '';
}
const env = {
  CICY_RUNTIME_KIND: pick('runtime_kind'),
  CICY_MASTER_URL: pick('master_url', 'teamcenter_url', 'register_url'),
  CICY_MASTER_TOKEN: pick('master_token', 'register_token'),
  CICY_PUBLIC_URL: pick('public_url', 'workspace_url', 'endpoint', 'url'),
  CICY_RUNTIME_API_TOKEN: pick('api_token', 'workspace_token'),
  CICY_TRIAL_AI_API_TOKEN: pick('api_key', 'newapi_token'),
  CICY_API_TOKEN: pick('api_token', 'workspace_token'),
  CICY_API_KEY: pick('api_key', 'newapi_token'),
  CICY_API_URL: pick('api_url'),
  CICY_ANTHROPIC_URL: pick('anthropic_url'),
  CICY_AI_PROVIDER: pick('ai_provider'),
  CICY_DEFAULT_OPENCODE_MODEL: pick('default_opencode_model', 'default_model'),
  CICY_DEFAULT_MODEL: pick('default_model', 'default_opencode_model'),
  CICY_DEFAULT_CLAUDE_MODEL: pick('default_claude_model', 'claude_model'),
  CICY_CLAUDE_MODEL: pick('claude_model', 'default_claude_model'),
  CICY_CODEX_MODEL: pick('codex_model'),
  CICY_OPENCLAW_MODEL: pick('openclaw_model'),
  CICY_CLOUDFLARED_TOKEN: pick('cloudflared_token', 'cf_tunnel_token', 'tunnel_token'),
  CICY_INSTANCE_KEY: pick('instance_key'),
  CICY_INSTANCE_LABEL: pick('instance_label'),
  CICY_TEAM_ID: pick('team_id'),
  CICY_MEMBERSHIP_KIND: pick('membership_kind'),
  CICY_MEMBERSHIP_TAG: pick('membership_tag'),
  CICY_MEMBERSHIP_EXPIRES_AT: pick('membership_expires_at'),
  CICY_MEMBERSHIP_RENEW_URL: pick('renew_url'),
  CICY_MEMBERSHIP_UPGRADE_URL: pick('upgrade_url'),
  CICY_MEMBERSHIP_SHOW_RENEW: bool('show_renew'),
  CICY_MEMBERSHIP_SHOW_UPGRADE: bool('show_upgrade'),
  CICY_ALREADY_REGISTERED: bool('already_registered', 'registered'),
};
for (const [key, value] of Object.entries(env)) {
  if (!value) continue;
  if ([
    'CICY_API_KEY',
    'CICY_API_URL',
    'CICY_ANTHROPIC_URL',
    'CICY_AI_PROVIDER',
    'CICY_DEFAULT_OPENCODE_MODEL',
    'CICY_DEFAULT_MODEL',
    'CICY_DEFAULT_CLAUDE_MODEL',
    'CICY_CLAUDE_MODEL',
    'CICY_CODEX_MODEL',
    'CICY_OPENCLAW_MODEL',
  ].includes(key)) {
    continue;
  }
  process.stdout.write(`export ${key}=${JSON.stringify(value)}\n`);
}
NODE
  )"
  rm -f "$response_file"

  if [ -z "${CICY_MASTER_URL:-}" ]; then
    export CICY_MASTER_URL="$teamcenter_url"
  fi
  if [ -z "${CICY_RUNTIME_KIND:-}" ]; then
    export CICY_RUNTIME_KIND="container"
  fi
  if [ -z "${CICY_MASTER_TOKEN:-}" ]; then
    export CICY_MASTER_TOKEN="$team_token"
  fi
  if [ -z "${CICY_RUNTIME_API_TOKEN:-}" ] && [ -n "${CICY_API_TOKEN:-}" ]; then
    export CICY_RUNTIME_API_TOKEN="$CICY_API_TOKEN"
  fi
  if [ -z "${CICY_TRIAL_AI_API_TOKEN:-}" ] && [ -n "${CICY_API_KEY:-}" ]; then
    export CICY_TRIAL_AI_API_TOKEN="$CICY_API_KEY"
  fi
  if [ -n "${CICY_TEAM_ID:-}" ] && [ -z "${CICY_INSTANCE_KEY:-}" ]; then
    export CICY_INSTANCE_KEY="team-${CICY_TEAM_ID}"
  fi
  persist_runtime_ai_config

  log "team bootstrap ok"
  if [ "${CICY_ALREADY_REGISTERED:-}" = "true" ]; then
    log "runtime already registered"
  fi
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

build_app_argv() {
  if [ "$#" -eq 0 ]; then
    set -- --public
  fi
  case " $* " in
    *" --public "*) ;;
    *) set -- --public "$@" ;;
  esac
  # Add --agents from CICY_TEAM_TOKEN (all agents) or CICY_AGENTS env var
  case " $* " in
    *" --agents="*) ;;
    *)
      if [ -n "${CICY_TEAM_TOKEN:-}" ]; then
        set -- --agents=all "$@"
      elif [ -n "${CICY_AGENTS:-}" ]; then
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
  bootstrap_team_runtime
  ensure_runtime_api_token
  ensure_legacy_home_links
  start_cloudflared

  mapfile -d '' app_argv < <(build_app_argv "$@")
  log "starting cicy-code"
  exec /app/cicy-code "${app_argv[@]}"
}

main "$@"
