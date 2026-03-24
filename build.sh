#!/bin/bash
set -e
ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT_DIR"
API_DIR="$ROOT_DIR/api"
APP_DIR="$ROOT_DIR/app"
DIST_DIR="$ROOT_DIR/dist"

# ── Sync version from npm/package.json → api/mgr/main.go ──
sync_version() {
  local ver=$(node -p "require('./npm/package.json').version")
  if [[ "$(uname)" == "Darwin" ]]; then
    sed -i '' "s/const version = \".*\"/const version = \"$ver\"/" $API_DIR/mgr/main.go
  else
    sed -i "s/const version = \".*\"/const version = \"$ver\"/" $API_DIR/mgr/main.go
  fi
  echo "  version: $ver"
}

# ── Embed assets into api/mgr/ for go:embed ──
prepare_embed() {
  rm -rf $API_DIR/mgr/resources $API_DIR/mgr/ui $API_DIR/mgr/tmux.conf $API_DIR/mgr/monitor
  cp -r $API_DIR/resources $API_DIR/mgr/resources
  cp $ROOT_DIR/.tmux.conf $API_DIR/mgr/tmux.conf
  if [ -d "$ROOT_DIR/mitmproxy" ]; then
    cp -r $ROOT_DIR/mitmproxy $API_DIR/mgr/monitor
  fi
  if [ "${SKIP_NPM:-0}" != "1" ]; then
    # Build frontend
    cd $APP_DIR && npm ci --silent && npm run build --silent && cd "$ROOT_DIR"
  fi
  cp -r $APP_DIR/dist $API_DIR/mgr/ui
}

cleanup_embed() {
  rm -rf $API_DIR/mgr/resources $API_DIR/mgr/ui $API_DIR/mgr/tmux.conf $API_DIR/mgr/monitor
}
trap cleanup_embed EXIT

# ── Build single binary ──
build_one() {
  local os=${1:-linux} arch=${2:-amd64} out=${3:-$API_DIR/cicy-code}
  cd $API_DIR && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -ldflags="-s -w" -o "$out" ./mgr/ && cd "$ROOT_DIR"
  echo "✅ $out (${os}/${arch})"
}

# ── Build all platforms ──
build_all() {
  rm -rf $DIST_DIR && mkdir -p $DIST_DIR
  build_one linux   amd64  $DIST_DIR/cicy-code-linux-amd64
  build_one linux   arm64  $DIST_DIR/cicy-code-linux-arm64
  build_one darwin  amd64  $DIST_DIR/cicy-code-darwin-amd64
  build_one darwin  arm64  $DIST_DIR/cicy-code-darwin-arm64
  echo ""; ls -lh $DIST_DIR/
}

# ── Main ──
case "${1:-build}" in
  build)
    sync_version
    prepare_embed
    build_one "${2:-linux}" "${3:-amd64}"
    ;;
  all)
    sync_version
    prepare_embed
    build_all
    ;;
  *)
    echo "Usage: ./build.sh [build|all] [os] [arch]"
    echo "  build          Build for current/specified platform (default: linux/amd64)"
    echo "  all            Cross-compile all platforms to dist/"
    echo ""
    echo "Env vars:"
    echo "  SKIP_NPM=1     Skip npm ci + npm run build (reuse existing app/dist)"
    exit 1
    ;;
esac
