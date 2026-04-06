#!/bin/bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT_DIR"
API_DIR="$ROOT_DIR/api"
APP_DIR="$ROOT_DIR/app"
DIST_DIR="$ROOT_DIR/dist"
GLOBAL_JSON="$HOME/global.json"

global_json_value() {
  local expr="$1"
  if [ -f "$GLOBAL_JSON" ]; then
    python3 - "$GLOBAL_JSON" "$expr" <<'PY'
import json, sys
path, expr = sys.argv[1], sys.argv[2]
try:
    with open(path, "r", encoding="utf-8") as f:
        data = json.load(f)
    value = data
    for part in expr.split('.'):
        if not part:
            continue
        if isinstance(value, dict):
            value = value.get(part, "")
        else:
            value = ""
            break
    if value is None:
        value = ""
    print(value, end="")
except Exception:
    pass
PY
  fi
}

default_base_image() {
  local images_base
  images_base="$(global_json_value images.base)"
  if [ -n "$images_base" ]; then
    printf '%s' "$images_base"
    return
  fi
  local images_base_repo images_base_tag
  images_base_repo="$(global_json_value images.base_repository)"
  images_base_tag="$(global_json_value images.base_tag)"
  if [ -n "$images_base_repo" ] && [ -n "$images_base_tag" ]; then
    printf '%s:%s' "$images_base_repo" "$images_base_tag"
    return
  fi
  local explicit
  explicit="$(global_json_value cicy-cluster.base_image)"
  if [ -n "$explicit" ]; then
    printf '%s' "$explicit"
    return
  fi
  if docker image inspect cicy-code-base:latest >/dev/null 2>&1; then
    printf 'cicy-code-base:latest'
    return
  fi
  local project
  project="$(global_json_value cicy-cluster.project_id)"
  if [ -n "$project" ]; then
    printf 'gcr.io/%s/cicy-code-base:latest' "$project"
    return
  fi
  printf 'cicy-code-base:latest'
}

# ── Sync version from npm/package.json → runtime/UI/tmux targets ──
sync_version() {
  python3 "$ROOT_DIR/scripts/sync-version.py" >/dev/null
  local ver
  ver=$(node -p "require('./npm/package.json').version")
  echo "  version: $ver"
}

# ── Embed assets into api/mgr/ for go:embed ──
prepare_embed() {
  rm -rf $API_DIR/mgr/resources $API_DIR/mgr/ui $API_DIR/mgr/tmux.conf $API_DIR/mgr/monitor
  cp -r $API_DIR/resources $API_DIR/mgr/resources
  cp $ROOT_DIR/.tmux.conf $API_DIR/mgr/.tmux.conf
  cp $ROOT_DIR/.cicy_tmux.conf $API_DIR/mgr/.cicy_tmux.conf
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

# ── Build Docker image for Cloud Run ──
build_docker() {
  local tag="${1:-latest}"
  local base_image="${BASE_IMAGE:-$(default_base_image)}"
  local docker_args=()
  if [ "${NO_CACHE:-0}" = "1" ]; then
    docker_args+=(--no-cache)
  fi
  echo "📦 Building Docker image cicy-code:${tag}..."
  echo "   BASE_IMAGE=${base_image}"
  if [ ! -f "$API_DIR/cicy-code" ]; then
    echo "❌ Missing $API_DIR/cicy-code. Run ./build.sh build linux amd64 first."
    exit 1
  fi
  cp  $API_DIR/cicy-code  $API_DIR/cicy-code-docker
  cd $API_DIR && docker build -f Dockerfile.cloudrun --build-arg BASE_IMAGE="$base_image" -t cicy-code:${tag} . "${docker_args[@]}" && cd "$ROOT_DIR"
  rm -f $API_DIR/cicy-code-docker
  echo "✅ Docker image built: cicy-code:${tag}"
}

build_docker_base() {
  local tag="${1:-latest}"
  local docker_args=()
  if [ "${NO_CACHE:-0}" = "1" ]; then
    docker_args+=(--no-cache)
  fi
  echo "📦 Building base Docker image cicy-code-base:${tag}..."
  cd $API_DIR && docker build -f Dockerfile.cloudrun.base -t cicy-code-base:${tag} . "${docker_args[@]}" && cd "$ROOT_DIR"
  echo "✅ Base Docker image built: cicy-code-base:${tag}"
}

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
  docker)
    sync_version
    prepare_embed
    build_one linux amd64
    build_docker "${2:-latest}"
    ;;
  docker-base)
    build_docker_base "${2:-latest}"
    ;;
  -h|--help)
    echo "Usage: ./build.sh [command] [os] [arch]"
    echo ""
    echo "Commands:"
    echo "  build          Build for current/specified platform (default: linux/amd64)"
    echo "  all            Cross-compile all platforms to dist/"
    echo "  docker [tag]       Build app Docker image using BASE_IMAGE"
    echo "                     default BASE_IMAGE: ~/global.json cicy-cluster.base_image"
    echo "                     fallback: gcr.io/<project_id>/cicy-code-base:latest"
    echo "  docker-base [tag]  Build reusable base Docker image (default tag: latest)"
    echo "  -h, --help     Show this help message"
    echo ""
    echo "Arguments:"
    echo "  os             Target OS: linux, darwin (default: linux)"
    echo "  arch           Target arch: amd64, arm64 (default: amd64)"
    echo ""
    echo "Environment variables:"
    echo "  SKIP_NPM=1     Skip npm ci + npm run build (reuse existing app/dist)"
    echo "  BASE_IMAGE     Base image for docker command (default: cicy-code-base:latest)"
    echo "  NO_CACHE=1     Disable Docker layer cache"
    exit 0
    ;;
  *)
    echo "Usage: ./build.sh [build|all|docker|docker-base] [os] [arch]"
    echo "  build          Build for current/specified platform (default: linux/amd64)"
    echo "  all            Cross-compile all platforms to dist/"
    echo "  docker [tag]       Build app Docker image"
    echo "  docker-base [tag]  Build reusable base Docker image"
    echo ""
    echo "Env vars:"
    echo "  SKIP_NPM=1     Skip npm ci + npm run build (reuse existing app/dist)"
    echo "  BASE_IMAGE     Base image for docker command"
    echo "  NO_CACHE=1     Disable Docker layer cache"
    exit 1
    ;;
esac
