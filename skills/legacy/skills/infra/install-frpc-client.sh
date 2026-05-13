#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<USAGE
Usage:
  curl -fsSL http://47.114.96.114:9600/install-frpc-client.sh | bash -s -- --token <TOKEN> [options]

Options:
  --token <TOKEN>            Required FRP auth token
  --server <HOST>            FRP server address, default: 47.114.96.114
  --server-port <PORT>       FRP server port, default: 9500
  --remote-port <PORT>       Remote TCP port on server, default: 9502
  --local-port <PORT>        Local port on Mac to expose, default: 22
  --local-ip <IP>            Local IP to expose, default: 127.0.0.1
  --name <NAME>              Proxy name, default: mac-ssh
  --admin-port <PORT>        Local frpc admin port, default: 7400
  --frp-version <VERSION>    FRP version, default: 0.68.1
  --no-launchd               Install config and binary only, do not enable launchd
  -h, --help                 Show this help

Result:
  Default behavior installs frpc, writes ~/.config/frp/frpc.toml,
  enables launchd auto-start, and exposes local ssh(22) to:

    47.114.96.114:<remote-port>

Then you can access this Mac through the server with:

    ssh -p <remote-port> <mac-user>@47.114.96.114
USAGE
}

SERVER_ADDR="47.114.96.114"
SERVER_PORT="9500"
REMOTE_PORT="9502"
LOCAL_PORT="22"
LOCAL_IP="127.0.0.1"
PROXY_NAME="mac-ssh"
ADMIN_PORT="7400"
FRP_VERSION="0.68.1"
TOKEN=""
ENABLE_LAUNCHD=1

while [ $# -gt 0 ]; do
  case "$1" in
    --token)
      TOKEN="${2:-}"
      shift 2
      ;;
    --server)
      SERVER_ADDR="${2:-}"
      shift 2
      ;;
    --server-port)
      SERVER_PORT="${2:-}"
      shift 2
      ;;
    --remote-port)
      REMOTE_PORT="${2:-}"
      shift 2
      ;;
    --local-port)
      LOCAL_PORT="${2:-}"
      shift 2
      ;;
    --local-ip)
      LOCAL_IP="${2:-}"
      shift 2
      ;;
    --name)
      PROXY_NAME="${2:-}"
      shift 2
      ;;
    --admin-port)
      ADMIN_PORT="${2:-}"
      shift 2
      ;;
    --frp-version)
      FRP_VERSION="${2:-}"
      shift 2
      ;;
    --no-launchd)
      ENABLE_LAUNCHD=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown arg: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [ -z "$TOKEN" ]; then
  echo "--token is required" >&2
  usage >&2
  exit 1
fi

if [ "$(uname -s)" != "Darwin" ]; then
  echo "this installer currently targets macOS only" >&2
  exit 1
fi

ARCH="$(uname -m)"
case "$ARCH" in
  arm64) FRP_ARCH="darwin_arm64" ;;
  x86_64) FRP_ARCH="darwin_amd64" ;;
  *) echo "unsupported macOS arch: $ARCH" >&2; exit 1 ;;
esac

BIN_DIR="$HOME/.local/bin"
FRP_DIR="$HOME/.local/frp"
CFG_DIR="$HOME/.config/frp"
PLIST="$HOME/Library/LaunchAgents/com.cicy.frpc.plist"
TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

mkdir -p "$BIN_DIR" "$FRP_DIR" "$CFG_DIR" "$HOME/Library/LaunchAgents"

DOWNLOAD_URL="https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}/frp_${FRP_VERSION}_${FRP_ARCH}.tar.gz"

echo "[1/5] download frpc: $DOWNLOAD_URL"
curl -L --fail -o "$TMP_DIR/frp.tar.gz" "$DOWNLOAD_URL"
tar -xzf "$TMP_DIR/frp.tar.gz" -C "$TMP_DIR"
install -m 0755 "$TMP_DIR/frp_${FRP_VERSION}_${FRP_ARCH}/frpc" "$BIN_DIR/frpc"

echo "[2/5] write config: $CFG_DIR/frpc.toml"
cat > "$CFG_DIR/frpc.toml" <<CFG
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

echo "[3/5] local config summary"
printf '  server: %s:%s\n' "$SERVER_ADDR" "$SERVER_PORT"
printf '  proxy : %s %s:%s -> remote %s\n' "$PROXY_NAME" "$LOCAL_IP" "$LOCAL_PORT" "$REMOTE_PORT"
printf '  config: %s\n' "$CFG_DIR/frpc.toml"
printf '  log   : %s\n' "$FRP_DIR/frpc.log"

if [ "$ENABLE_LAUNCHD" = "1" ]; then
  echo "[4/5] install launchd auto-start"
  cat > "$PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>com.cicy.frpc</string>
    <key>ProgramArguments</key>
    <array>
      <string>$BIN_DIR/frpc</string>
      <string>-c</string>
      <string>$CFG_DIR/frpc.toml</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>$FRP_DIR/frpc.log</string>
    <key>StandardErrorPath</key>
    <string>$FRP_DIR/frpc.log</string>
  </dict>
</plist>
PLIST

  launchctl bootout "gui/$(id -u)" "$PLIST" 2>/dev/null || launchctl unload "$PLIST" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$PLIST" 2>/dev/null || launchctl load -w "$PLIST"
  launchctl kickstart -k "gui/$(id -u)/com.cicy.frpc" 2>/dev/null || true
else
  echo "[4/5] skip launchd (--no-launchd)"
fi

echo "[5/5] verify"
sleep 2
"$BIN_DIR/frpc" status -c "$CFG_DIR/frpc.toml" || true

echo
echo "done"
echo
echo "Now access this Mac through the FRP server with:"
echo "  ssh -p $REMOTE_PORT <mac-user>@$SERVER_ADDR"
echo
echo "If Remote Login is disabled on macOS, enable it first:"
echo "  System Settings -> General -> Sharing -> Remote Login"
