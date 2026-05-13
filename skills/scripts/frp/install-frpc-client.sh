#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<USAGE
Usage:
  curl -fsSL https://install.cicy-ai.com/frp | bash

Options:
  --token <TOKEN>            FRP auth token. Required on first install (or set FRP_TOKEN).
  --server <HOST>            FRP server address. Required on first install (or set FRP_SERVER).
  --server-port <PORT>       FRP server port, default: 9500
  --remote-port <PORT>       Remote TCP port on server, default: 9502
  --local-port <PORT>        Local port to expose, default: 22
  --local-ip <IP>            Local IP to expose, default: 127.0.0.1
  --name <NAME>              Proxy name, default: auto by OS
  --admin-port <PORT>        Local frpc admin port, default: 7400
  --frp-version <VERSION>    FRP version, default: 0.68.1
  --service <auto|system|launchd|none>
                              Service mode, default: auto
  --github-proxy <URL>       Preferred GitHub proxy prefix, default: https://gh-proxy.com/
  -h, --help                 Show this help

Environment:
  FRP_TOKEN                  FRP auth token for non-interactive installs.
  FRP_SERVER                 FRP server address for non-interactive installs.

Examples:
  FRP_SERVER=1.2.3.4 FRP_TOKEN=xxxx curl -fsSL https://install.cicy-ai.com/frp | bash
  curl -fsSL https://install.cicy-ai.com/frp | bash -s -- --server 1.2.3.4 --token xxxx
  curl -fsSL https://install.cicy-ai.com/frp | bash -s -- --server 1.2.3.4 --token xxxx --remote-port 9503
  # rerun with no args reuses existing ~/cicy-ai/db/frpc.toml and hot-reloads
  curl -fsSL https://install.cicy-ai.com/frp | bash
USAGE
}

warn() {
  printf 'warning: %s\n' "$*" >&2
}

fail() {
  printf '%s\n' "$*" >&2
  exit 1
}

SERVER_ADDR="${FRP_SERVER:-}"
SERVER_PORT="9500"
REMOTE_PORT="9502"
LOCAL_PORT="22"
LOCAL_IP="127.0.0.1"
PROXY_NAME=""
ADMIN_PORT="7400"
FRP_VERSION="0.68.1"
TOKEN="${FRP_TOKEN:-}"
SERVICE_MODE="auto"
PREFERRED_PROXY="${GITHUB_PROXY:-https://gh-proxy.com/}"
ALT_PROXY_1="https://ghfast.top/"
ALT_PROXY_2="https://ghproxy.net/"
CONFIG_ARGS_PROVIDED=0

while [ $# -gt 0 ]; do
  case "$1" in
    --token) TOKEN="${2:-}"; CONFIG_ARGS_PROVIDED=1; shift 2 ;;
    --server) SERVER_ADDR="${2:-}"; CONFIG_ARGS_PROVIDED=1; shift 2 ;;
    --server-port) SERVER_PORT="${2:-}"; CONFIG_ARGS_PROVIDED=1; shift 2 ;;
    --remote-port) REMOTE_PORT="${2:-}"; CONFIG_ARGS_PROVIDED=1; shift 2 ;;
    --local-port) LOCAL_PORT="${2:-}"; CONFIG_ARGS_PROVIDED=1; shift 2 ;;
    --local-ip) LOCAL_IP="${2:-}"; CONFIG_ARGS_PROVIDED=1; shift 2 ;;
    --name) PROXY_NAME="${2:-}"; CONFIG_ARGS_PROVIDED=1; shift 2 ;;
    --admin-port) ADMIN_PORT="${2:-}"; CONFIG_ARGS_PROVIDED=1; shift 2 ;;
    --frp-version) FRP_VERSION="${2:-}"; shift 2 ;;
    --service) SERVICE_MODE="${2:-}"; shift 2 ;;
    --github-proxy) PREFERRED_PROXY="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage >&2; exit 1 ;;
  esac
done

OS="$(uname -s)"
ARCH="$(uname -m)"
case "$OS" in
  Darwin)
    case "$ARCH" in
      arm64) FRP_PLATFORM="darwin_arm64" ;;
      x86_64) FRP_PLATFORM="darwin_amd64" ;;
      *) fail "unsupported macOS arch: $ARCH" ;;
    esac
    [ -n "$PROXY_NAME" ] || PROXY_NAME="mac-ssh"
    ;;
  Linux)
    case "$ARCH" in
      x86_64|amd64) FRP_PLATFORM="linux_amd64" ;;
      aarch64|arm64) FRP_PLATFORM="linux_arm64" ;;
      *) fail "unsupported Linux arch: $ARCH" ;;
    esac
    [ -n "$PROXY_NAME" ] || PROXY_NAME="linux-ssh"
    ;;
  *)
    fail "unsupported OS: $OS"
    ;;
esac

CURRENT_USER="$(id -un)"
CURRENT_UID="$(id -u)"
BIN_DIR="$HOME/.local/bin"
FRP_DIR="$HOME/.local/frp"
CFG_DIR="$HOME/cicy-ai/db"
CFG_FILE="$CFG_DIR/frpc.toml"
LEGACY_CFG_FILE="$HOME/.config/frp/frpc.toml"
# Self-migrate: pull an existing legacy config forward on first install.
if [ ! -f "$CFG_FILE" ] && [ -f "$LEGACY_CFG_FILE" ]; then
  mkdir -p "$CFG_DIR"
  mv "$LEGACY_CFG_FILE" "$CFG_FILE"
  echo "  migrated config: $LEGACY_CFG_FILE -> $CFG_FILE"
fi
PID_FILE="$FRP_DIR/frpc.pid"
LOG_FILE="$FRP_DIR/frpc.log"
PLIST_LABEL="com.cicy.frpc"
PLIST_PATH="$HOME/Library/LaunchAgents/${PLIST_LABEL}.plist"
SYSTEMD_SERVICE="frpc-cicy-${CURRENT_USER}.service"
SYSTEMD_SERVICE_PATH="/etc/systemd/system/${SYSTEMD_SERVICE}"
STATUS_COMMAND="$BIN_DIR/frpc status -c \"$CFG_FILE\""
RELOAD_COMMAND="$BIN_DIR/frpc reload -c \"$CFG_FILE\""
START_COMMAND="$BIN_DIR/frpc -c \"$CFG_FILE\""
RESTART_COMMAND=""
EFFECTIVE_SERVICE_MODE=""
CONFIG_REUSED=0
ACTION_RESULT=""
VERIFY_OUTPUT=""
EFFECTIVE_SERVER_ADDR="$SERVER_ADDR"
EFFECTIVE_SERVER_PORT="$SERVER_PORT"
EFFECTIVE_REMOTE_PORT="$REMOTE_PORT"
EFFECTIVE_LOCAL_IP="$LOCAL_IP"
EFFECTIVE_LOCAL_PORT="$LOCAL_PORT"
EFFECTIVE_PROXY_NAME="$PROXY_NAME"

proxy_candidates() {
  printf '%s\n' "$PREFERRED_PROXY" "$ALT_PROXY_1" "$ALT_PROXY_2" ""
}

download_with_proxy() {
  local source_url="$1"
  local dest="$2"
  local candidate
  rm -f "$dest"
  while IFS= read -r candidate; do
    local url="$source_url"
    if [ -n "$candidate" ]; then
      url="${candidate}${source_url}"
    fi
    echo "download: $url" >&2
    if curl -fL --connect-timeout 10 --retry 2 --retry-delay 1 -o "$dest" "$url"; then
      return 0
    fi
  done < <(proxy_candidates)
  return 1
}

resolve_effective_service_mode() {
  case "$SERVICE_MODE" in
    auto)
      case "$OS" in
        Darwin)
          EFFECTIVE_SERVICE_MODE="launchd"
          ;;
        Linux)
          if command -v systemctl >/dev/null 2>&1 && command -v sudo >/dev/null 2>&1; then
            EFFECTIVE_SERVICE_MODE="system"
          else
            warn "systemctl or sudo not found; falling back to --service none"
            EFFECTIVE_SERVICE_MODE="none"
          fi
          ;;
      esac
      ;;
    system)
      [ "$OS" = "Linux" ] || fail "--service system is only supported on Linux"
      command -v systemctl >/dev/null 2>&1 || fail "systemctl not found; Linux service install requires systemd"
      command -v sudo >/dev/null 2>&1 || fail "sudo not found; Linux service install requires sudo"
      EFFECTIVE_SERVICE_MODE="system"
      ;;
    launchd)
      [ "$OS" = "Darwin" ] || fail "--service launchd is only supported on macOS"
      command -v launchctl >/dev/null 2>&1 || fail "launchctl not found"
      EFFECTIVE_SERVICE_MODE="launchd"
      ;;
    none)
      EFFECTIVE_SERVICE_MODE="none"
      ;;
    *)
      fail "unsupported service mode: $SERVICE_MODE"
      ;;
  esac

  case "$EFFECTIVE_SERVICE_MODE" in
    launchd)
      RESTART_COMMAND="launchctl kickstart -k gui/${CURRENT_UID}/${PLIST_LABEL}"
      ;;
    system)
      RESTART_COMMAND="sudo systemctl restart ${SYSTEMD_SERVICE}"
      ;;
    none)
      RESTART_COMMAND="kill \"\$(cat \"$PID_FILE\")\" 2>/dev/null || true; nohup \"$BIN_DIR/frpc\" -c \"$CFG_FILE\" >>\"$LOG_FILE\" 2>&1 &"
      ;;
  esac
}

resolve_token_if_needed() {
  if [ "$CONFIG_REUSED" = "1" ]; then
    return 0
  fi
  if [ -z "$SERVER_ADDR" ]; then
    if [ ! -t 0 ]; then
      fail "FRP server required on first install; pass --server <HOST> or set FRP_SERVER"
    fi
    printf 'Enter FRP server address: ' >&2
    read -r SERVER_ADDR
    if [ -z "$SERVER_ADDR" ]; then
      fail "FRP server cannot be empty"
    fi
  fi
  if [ -n "$TOKEN" ]; then
    return 0
  fi
  if [ ! -t 0 ]; then
    fail "FRP token required on first install; pass --token <TOKEN> or set FRP_TOKEN"
  fi
  printf 'Enter FRP token: ' >&2
  read -r -s TOKEN
  printf '\n' >&2
  if [ -z "$TOKEN" ]; then
    fail "FRP token cannot be empty"
  fi
}

write_config() {
  cat > "$CFG_FILE" <<CFG
serverAddr = "$SERVER_ADDR"
serverPort = $SERVER_PORT

auth.method = "token"
auth.token = "$TOKEN"

webServer.addr = "127.0.0.1"
webServer.port = $ADMIN_PORT

[[proxies]]
name = "$PROXY_NAME"
type = "tcp"
localIP = "$LOCAL_IP"
localPort = $LOCAL_PORT
remotePort = $REMOTE_PORT
CFG
}

parse_config_value() {
  local key="$1"
  python3 - "$CFG_FILE" "$key" <<'PY'
import re, sys
path, key = sys.argv[1], sys.argv[2]
text = open(path, 'r', encoding='utf-8').read()
patterns = [
    rf'^\s*{re.escape(key)}\s*=\s*"([^"]*)"\s*$',
    rf'^\s*{re.escape(key)}\s*=\s*([^#\n\r]+?)\s*$',
]
for pattern in patterns:
    match = re.search(pattern, text, re.MULTILINE)
    if match:
        print(match.group(1).strip())
        break
PY
}

load_effective_config_summary() {
  if [ ! -f "$CFG_FILE" ]; then
    return 0
  fi
  local value
  value="$(parse_config_value serverAddr || true)"
  [ -n "$value" ] && EFFECTIVE_SERVER_ADDR="$value"
  value="$(parse_config_value serverPort || true)"
  [ -n "$value" ] && EFFECTIVE_SERVER_PORT="$value"
  value="$(parse_config_value localIP || true)"
  [ -n "$value" ] && EFFECTIVE_LOCAL_IP="$value"
  value="$(parse_config_value localPort || true)"
  [ -n "$value" ] && EFFECTIVE_LOCAL_PORT="$value"
  value="$(parse_config_value remotePort || true)"
  [ -n "$value" ] && EFFECTIVE_REMOTE_PORT="$value"
  value="$(parse_config_value name || true)"
  [ -n "$value" ] && EFFECTIVE_PROXY_NAME="$value"
}

prepare_config() {
  echo "[2/5] prepare config"
  if [ -f "$CFG_FILE" ]; then
    CONFIG_REUSED=1
    load_effective_config_summary
    echo "  config : $CFG_FILE"
    echo "  reusing existing config; edit this file directly to change token/ports/name"
    echo "  reload : $RELOAD_COMMAND"
    if [ -n "$RESTART_COMMAND" ]; then
      echo "  restart: $RESTART_COMMAND"
    fi
    if [ "$CONFIG_ARGS_PROVIDED" = "1" ]; then
      echo "  note   : install-time config flags were ignored because the config already exists"
    fi
    return 0
  fi

  resolve_token_if_needed
  echo "  writing config: $CFG_FILE"
  write_config
  load_effective_config_summary
}

install_launchd() {
  mkdir -p "$HOME/Library/LaunchAgents"
  cat > "$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>$PLIST_LABEL</string>
    <key>ProgramArguments</key>
    <array>
      <string>$BIN_DIR/frpc</string>
      <string>-c</string>
      <string>$CFG_FILE</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>$LOG_FILE</string>
    <key>StandardErrorPath</key>
    <string>$LOG_FILE</string>
  </dict>
</plist>
PLIST
  launchctl bootout "gui/${CURRENT_UID}" "$PLIST_PATH" 2>/dev/null || launchctl unload "$PLIST_PATH" 2>/dev/null || true
  launchctl bootstrap "gui/${CURRENT_UID}" "$PLIST_PATH" 2>/dev/null || launchctl load -w "$PLIST_PATH"
  launchctl kickstart -k "gui/${CURRENT_UID}/${PLIST_LABEL}" 2>/dev/null || true
}

install_systemd() {
  sudo tee "$SYSTEMD_SERVICE_PATH" >/dev/null <<SERVICE
[Unit]
Description=FRP Client for CiCy (${CURRENT_USER})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${CURRENT_USER}
Environment=HOME=${HOME}
WorkingDirectory=${HOME}
ExecStart=${BIN_DIR}/frpc -c ${CFG_FILE}
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
SERVICE
  sudo systemctl daemon-reload
  sudo systemctl enable "$SYSTEMD_SERVICE" >/dev/null
  sudo systemctl restart "$SYSTEMD_SERVICE" 2>/dev/null || sudo systemctl start "$SYSTEMD_SERVICE"
}

stop_background_if_needed() {
  local pid=""
  if [ -f "$PID_FILE" ]; then
    pid="$(cat "$PID_FILE" 2>/dev/null || true)"
  fi
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    for _ in $(seq 1 20); do
      if ! kill -0 "$pid" 2>/dev/null; then
        break
      fi
      sleep 0.2
    done
    if kill -0 "$pid" 2>/dev/null; then
      kill -9 "$pid" 2>/dev/null || true
    fi
  fi
  rm -f "$PID_FILE"
}

start_background() {
  nohup "$BIN_DIR/frpc" -c "$CFG_FILE" >>"$LOG_FILE" 2>&1 &
  local pid="$!"
  echo "$pid" > "$PID_FILE"
  sleep 2
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "frpc exited during startup; recent logs:" >&2
    tail -n 50 "$LOG_FILE" >&2 || true
    exit 1
  fi
  echo "  frpc started in background (pid $pid)"
}

try_hot_reload() {
  local output
  if output="$("$BIN_DIR/frpc" reload -c "$CFG_FILE" 2>&1)"; then
    if [ -n "$output" ]; then
      printf '%s\n' "$output"
    else
      echo "frpc reloaded"
    fi
    ACTION_RESULT="hot reloaded"
    return 0
  fi
  if [ -n "$output" ]; then
    warn "hot reload failed; will restart instead"
    printf '%s\n' "$output" >&2
  fi
  return 1
}

start_or_reload_frpc() {
  echo "[3/5] start or reload frpc"

  if [ "$CONFIG_REUSED" = "1" ]; then
    if try_hot_reload; then
      return 0
    fi
  fi

  case "$EFFECTIVE_SERVICE_MODE" in
    launchd)
      install_launchd
      ACTION_RESULT="started via launchd"
      ;;
    system)
      install_systemd
      ACTION_RESULT="started via systemd"
      ;;
    none)
      stop_background_if_needed
      start_background
      ACTION_RESULT="started in background"
      ;;
  esac
}

verify_status() {
  echo "[4/5] verify"
  sleep 2
  VERIFY_OUTPUT="$($BIN_DIR/frpc status -c "$CFG_FILE" 2>&1 || true)"
  if [ -n "$VERIFY_OUTPUT" ]; then
    printf '%s\n' "$VERIFY_OUTPUT"
  else
    warn "frpc status returned no output"
  fi
}

print_summary() {
  echo "[5/5] summary"
  printf '  mode   : %s\n' "$EFFECTIVE_SERVICE_MODE"
  printf '  action : %s\n' "$ACTION_RESULT"
  printf '  server : %s:%s\n' "$EFFECTIVE_SERVER_ADDR" "$EFFECTIVE_SERVER_PORT"
  printf '  proxy  : %s %s:%s -> remote %s\n' "$EFFECTIVE_PROXY_NAME" "$EFFECTIVE_LOCAL_IP" "$EFFECTIVE_LOCAL_PORT" "$EFFECTIVE_REMOTE_PORT"
  printf '  config : %s\n' "$CFG_FILE"
  printf '  log    : %s\n' "$LOG_FILE"
  if [ "$EFFECTIVE_SERVICE_MODE" = "none" ]; then
    printf '  pid    : %s\n' "$PID_FILE"
  fi
  printf '  status : %s\n' "$STATUS_COMMAND"
  printf '  reload : %s\n' "$RELOAD_COMMAND"
  if [ -n "$RESTART_COMMAND" ]; then
    printf '  restart: %s\n' "$RESTART_COMMAND"
  fi
  if [ "$EFFECTIVE_SERVICE_MODE" = "none" ]; then
    printf '  start  : %s\n' "$START_COMMAND"
  fi
  echo
  echo "done"
  echo "edit config then rerun this installer or use the reload command above"
  echo "connect with: ssh -p $EFFECTIVE_REMOTE_PORT <your-user>@$EFFECTIVE_SERVER_ADDR"
  echo "if SSH is not enabled on the client machine, enable it first"
}

TMP_DIR="$(mktemp -d)"
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

mkdir -p "$BIN_DIR" "$FRP_DIR" "$CFG_DIR"
if [ -f "$CFG_FILE" ]; then
  CONFIG_REUSED=1
fi

resolve_effective_service_mode
resolve_token_if_needed

ARCHIVE="frp_${FRP_VERSION}_${FRP_PLATFORM}.tar.gz"
SOURCE_URL="https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}/${ARCHIVE}"

echo "[1/5] install frpc"
download_with_proxy "$SOURCE_URL" "$TMP_DIR/$ARCHIVE"
tar -xzf "$TMP_DIR/$ARCHIVE" -C "$TMP_DIR"
install -m 0755 "$TMP_DIR/frp_${FRP_VERSION}_${FRP_PLATFORM}/frpc" "$BIN_DIR/frpc"

prepare_config
start_or_reload_frpc
verify_status
print_summary
