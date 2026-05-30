#!/bin/bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT_DIR"
API_DIR="$ROOT_DIR/api"
APP_DIR="$ROOT_DIR/app"
DIST_DIR="$ROOT_DIR/dist"
CICY_ROOT_DIR="$HOME/cicy-ai"
GLOBAL_JSON="$CICY_ROOT_DIR/global.json"
VERSIONS_JSON="$ROOT_DIR/versions.json"
BUILD_EMBED_LOCK_DIR="$ROOT_DIR/.build-embed.lock"
BUILD_EMBED_LOCK_HELD=0

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

versions_json_value() {
  local expr="$1"
  if [ -f "$VERSIONS_JSON" ]; then
    python3 - "$VERSIONS_JSON" "$expr" <<'PY'
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

default_base_tag() {
  local versioned
  versioned="$(versions_json_value base)"
  if [ -n "$versioned" ]; then
    printf '%s' "$versioned"
    return
  fi
  printf 'latest'
}

default_base_image() {
  local images_base
  images_base="$(global_json_value images.base)"
  if [ -n "$images_base" ]; then
    case "$images_base" in
      ghcr.io/cicy-ai/cicy-code-base:*)
        printf 'cicy-code-base:%s' "${images_base##*:}"
        ;;
      *)
        printf '%s' "$images_base"
        ;;
    esac
    return
  fi
  local images_base_repo images_base_tag
  images_base_repo="$(global_json_value images.base_repository)"
  images_base_tag="$(global_json_value images.base_tag)"
  if [ -n "$images_base_repo" ] && [ -n "$images_base_tag" ]; then
    if [ "$images_base_repo" = "ghcr.io/cicy-ai/cicy-code-base" ]; then
      printf 'cicy-code-base:%s' "$images_base_tag"
    else
      printf '%s:%s' "$images_base_repo" "$images_base_tag"
    fi
    return
  fi
  local explicit
  explicit="$(global_json_value cicy-cluster.base_image)"
  if [ -n "$explicit" ]; then
    case "$explicit" in
      ghcr.io/cicy-ai/cicy-code-base:*)
        printf 'cicy-code-base:%s' "${explicit##*:}"
        ;;
      *)
        printf '%s' "$explicit"
        ;;
    esac
    return
  fi
  printf 'cicy-code-base:%s' "$(default_base_tag)"
}

cos_base_url() {
  local bucket region
  bucket="$(global_json_value tencent.bucket)"
  region="$(global_json_value tencent.region)"
  if [ -z "$bucket" ] || [ -z "$region" ]; then
    echo "❌ CDN=1 requires ${GLOBAL_JSON}.tencent.bucket and ${GLOBAL_JSON}.tencent.region" >&2
    exit 1
  fi
  printf 'https://%s.cos.%s.myqcloud.com' "$bucket" "$region"
}

configure_cdn_env() {
  export CICY_APP_CDN_PREFIX=""
  export CICY_TTYD_CDN_PREFIX=""

  if [ "${CDN:-0}" != "1" ]; then
    return
  fi

  local cos_base app_version ttyd_version
  cos_base="$(cos_base_url)"
  app_version="$(versions_json_value app)"
  ttyd_version="$(versions_json_value ttyd)"

  if [ -z "$app_version" ] || [ -z "$ttyd_version" ]; then
    echo "❌ CDN=1 requires versions.json to define app and ttyd versions" >&2
    exit 1
  fi

  export CICY_APP_CDN_PREFIX="${cos_base}/app/v${app_version}"
  export CICY_APP_VITE_BASE="https://cicy-ai.com/cos-assets/v${app_version}"
  export CICY_TTYD_CDN_PREFIX="${cos_base}/ttyd/v${ttyd_version}"

  echo "🌐 CDN=1"
  echo "   APP_CDN_PREFIX=${CICY_APP_CDN_PREFIX}"
  echo "   APP_VITE_BASE=${CICY_APP_VITE_BASE}"
  echo "   TTYD_CDN_PREFIX=${CICY_TTYD_CDN_PREFIX}"
}

acquire_build_embed_lock() {
  if [ "$BUILD_EMBED_LOCK_HELD" = "1" ]; then
    return
  fi
  while ! mkdir "$BUILD_EMBED_LOCK_DIR" 2>/dev/null; do
    local lock_pid=""
    if [ -f "$BUILD_EMBED_LOCK_DIR/pid" ]; then
      lock_pid="$(cat "$BUILD_EMBED_LOCK_DIR/pid" 2>/dev/null || true)"
    fi
    if [ -n "$lock_pid" ] && ! kill -0 "$lock_pid" 2>/dev/null; then
      echo "⚠️  Removing stale build embed lock from pid $lock_pid"
      rm -rf "$BUILD_EMBED_LOCK_DIR"
      continue
    fi
    echo "⏳ Waiting for build embed lock..."
    sleep 0.2
  done
  printf '%s\n' "$$" > "$BUILD_EMBED_LOCK_DIR/pid"
  BUILD_EMBED_LOCK_HELD=1
}

release_build_embed_lock() {
  if [ "$BUILD_EMBED_LOCK_HELD" != "1" ]; then
    return
  fi
  rm -rf "$BUILD_EMBED_LOCK_DIR"
  BUILD_EMBED_LOCK_HELD=0
}

file_hash() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$path" | awk '{print $1}'
}

# ── Sync version from npm/package.json → runtime/UI/tmux targets ──
sync_version() {
  python3 "$ROOT_DIR/scripts/sync-version.py" >/dev/null
  local ver
  ver=$(node -p "require('./npm/package.json').version")
  echo "  version: $ver"
}

# ── Embed assets into api/mgr/ for go:embed ──
restore_ui_placeholder() {
  mkdir -p "$API_DIR/mgr/ui"
  cat > "$API_DIR/mgr/ui/index.html" <<'EOF'
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>CiCy Code</title>
  </head>
  <body>
    <main style="font-family: sans-serif; padding: 24px;">
      <h1>CiCy Code UI placeholder</h1>
      <p>This placeholder exists so Go embed can compile during tests.</p>
      <p>Production builds replace <code>api/mgr/ui</code> with <code>app/dist</code>.</p>
    </main>
  </body>
</html>
EOF
}

SKILLS_DIR="$ROOT_DIR/skills"

prepare_skills_embed() {
  # Build cicy-skills binaries + pack the source tree into a single tarball
  # that setup.go //go:embed picks up. A tar avoids the "different module"
  # error you get when embedding a directory that itself has go.mod.
  local out="$API_DIR/mgr/cicy_skills_assets.tar.gz"
  if [ "${SKIP_SKILLS_EMBED:-0}" = "1" ]; then
    [ -f "$out" ] || (echo '' | gzip > "$out") # tiny placeholder so go:embed has a file
    return
  fi
  echo "[build] preparing cicy-skills embed tarball..."
  GOWORK=off make -C "$SKILLS_DIR" build-local-binaries >/dev/null
  rm -f "$out"
  tar -czf "$out" -C "$SKILLS_DIR" \
    --exclude='./node_modules' \
    --exclude='./.git' \
    --exclude='./.github' \
    --exclude='./cf' \
    --exclude='./docker' \
    --exclude='./migrations' \
    --exclude='./providers/google-node/node_modules' \
    --exclude='./.build-embed.lock' \
    .
  echo "[build]   wrote $out ($(du -h "$out" | cut -f1))"
}

prepare_embed() {
  acquire_build_embed_lock
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
  elif [ ! -d "$APP_DIR/dist" ]; then
    # SKIP_NPM reuses an existing app/dist, but on a fresh checkout it does not
    # exist yet — build it once so the unconditional `cp app/dist` below works.
    echo "[build] SKIP_NPM=1 but $APP_DIR/dist is missing; building frontend once..."
    cd $APP_DIR && npm ci --silent && npm run build --silent && cd "$ROOT_DIR"
  fi
  if [ "${SKIP_TTYD_ASSET:-0}" != "1" ]; then
    cd "$API_DIR" && make asset >/dev/null && cd "$ROOT_DIR"
  fi
  cp -r $APP_DIR/dist $API_DIR/mgr/ui
  prepare_skills_embed
}

cleanup_embed() {
  rm -rf $API_DIR/mgr/resources $API_DIR/mgr/ui $API_DIR/mgr/tmux.conf $API_DIR/mgr/monitor
  rm -f $API_DIR/mgr/cicy_skills_assets.tar.gz
  restore_ui_placeholder
  release_build_embed_lock
}
trap cleanup_embed EXIT

# ── Build Docker image for container runtime ──
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
  cd $API_DIR && docker build -f Dockerfile.runtime \
    --build-arg BASE_IMAGE="$base_image" \
    -t cicy-code:${tag} . "${docker_args[@]}" && cd "$ROOT_DIR"
  rm -f $API_DIR/cicy-code-docker
  echo "✅ Docker image built: cicy-code:${tag}"
}

build_docker_base() {
  local tag="${1:-$(default_base_tag)}"
  local docker_args=()
  local base_dockerfile_hash
  if [ "${NO_CACHE:-0}" = "1" ]; then
    docker_args+=(--no-cache)
  fi
  base_dockerfile_hash="$(file_hash "$API_DIR/Dockerfile.runtime.base")"
  echo "📦 Building base Docker image cicy-code-base:${tag}..."
  echo "   BASE_DOCKERFILE_HASH=${base_dockerfile_hash:0:12}"
  cd $API_DIR && docker build -f Dockerfile.runtime.base \
    --build-arg BASE_DOCKERFILE_HASH="$base_dockerfile_hash" \
    -t cicy-code-base:${tag} . "${docker_args[@]}" && cd "$ROOT_DIR"
  echo "✅ Base Docker image built: cicy-code-base:${tag}"
}

# ── Embed the platform-matching mihomo proxy binary (gzip) into api/mgr ──
# So cicy-code's first startup decompresses mihomo from itself instead of
# downloading ~29MB from gh-proxy.com (which blocked the API bind). The
# committed mihomo_bin.gz is a ~20-byte placeholder; we overwrite it per
# platform here, and restore the placeholder after build_all. Set
# SKIP_MIHOMO_EMBED=1 to keep the placeholder (falls back to runtime download).
MIHOMO_EMBED_VERSION="${CICY_MIHOMO_VERSION:-v1.10.3}"
prepare_mihomo_embed() {
  local os=$1 arch=$2
  local embed="$API_DIR/mgr/mihomo_bin.gz"
  if [ "${SKIP_MIHOMO_EMBED:-0}" = "1" ]; then
    [ -s "$embed" ] || (printf '' | gzip > "$embed")
    return 0
  fi
  local cache="$ROOT_DIR/dist/.mihomo-cache"
  mkdir -p "$cache"
  local raw="$cache/mihomo-${os}-${arch}-${MIHOMO_EMBED_VERSION}"
  if [ ! -s "$raw" ]; then
    local rel="https://github.com/cicy-ai/cicy-mihomo/releases/download/${MIHOMO_EMBED_VERSION}/mihomo-${os}-${arch}"
    # Try direct GitHub first (fast on CI / GitHub's network), then the
    # gh-proxy mirror (for CN dev boxes where direct is slow/blocked).
    local fetched=0
    for url in "$rel" "${GITHUB_PROXY:-https://gh-proxy.com/}${rel}"; do
      echo "⬇️  fetching mihomo to embed: $url"
      if curl -fsSL --max-time 600 -o "$raw.tmp" "$url"; then fetched=1; mv "$raw.tmp" "$raw"; break; fi
    done
    if [ "$fetched" != "1" ]; then
      echo "⚠️  mihomo fetch failed — embedding placeholder (runtime will download)"
      printf '' | gzip > "$embed"
      return 0
    fi
  fi
  gzip -c "$raw" > "$embed"
  echo "📦 embedded mihomo-${os}-${arch} → mihomo_bin.gz ($(du -h "$embed" | cut -f1))"
}

restore_mihomo_placeholder() {
  printf '' | gzip > "$API_DIR/mgr/mihomo_bin.gz"
}

# ── Build single binary ──
build_one() {
  local os=${1:-linux} arch=${2:-amd64} out=${3:-$API_DIR/cicy-code}
  prepare_mihomo_embed "$os" "$arch"
  local ldflags="-s -w"
  if [ -n "${CICY_APP_CDN_PREFIX:-}" ]; then
    ldflags="$ldflags -X main.BuiltAppCDNPrefix=${CICY_APP_CDN_PREFIX}"
  fi
  if [ -n "${CICY_TTYD_CDN_PREFIX:-}" ]; then
    ldflags="$ldflags -X ttyd-go/server.BuiltTTYDCDNPrefix=${CICY_TTYD_CDN_PREFIX}"
  fi
  cd $API_DIR && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -buildvcs=false -ldflags="$ldflags" -o "$out" ./mgr/ && cd "$ROOT_DIR"
  echo "✅ $out (${os}/${arch})"
}

# ── Build all platforms ──
build_all() {
  rm -rf $DIST_DIR && mkdir -p $DIST_DIR
  build_one linux   amd64  $DIST_DIR/cicy-code-linux-amd64
  build_one linux   arm64  $DIST_DIR/cicy-code-linux-arm64
  build_one darwin  amd64  $DIST_DIR/cicy-code-darwin-amd64
  build_one darwin  arm64  $DIST_DIR/cicy-code-darwin-arm64
  restore_mihomo_placeholder  # don't leave a 13MB gz in the tracked tree
  echo ""; ls -lh $DIST_DIR/
}

build_assets() {
  SKIP_NPM=0
  SKIP_TTYD_ASSET=0
  prepare_embed
  echo "✅ Assets prepared"
  echo "   app_dist=$APP_DIR/dist"
  echo "   ttyd_assets_refreshed=1"
}

test_go() {
  local pkg="${1:-./mgr/...}"
  shift || true
  prepare_embed
  cd "$API_DIR" && go test "$pkg" "$@" && cd "$ROOT_DIR"
}

# ── Main ──
case "${1:-build}" in
  assets)
    sync_version
    configure_cdn_env
    build_assets
    ;;
  build)
    sync_version
    configure_cdn_env
    prepare_embed
    build_one "${2:-linux}" "${3:-amd64}"
    ;;
  all)
    sync_version
    configure_cdn_env
    prepare_embed
    build_all
    ;;
  docker)
    sync_version
    configure_cdn_env
    prepare_embed
    build_one linux amd64
    build_docker "${2:-latest}"
    ;;
  docker-base)
    build_docker_base "${2:-latest}"
    ;;
  test-go)
    test_go "${@:2}"
    ;;
  -h|--help)
    echo "Usage: ./build.sh [command] [os] [arch]"
    echo ""
    echo "Commands:"
    echo "  assets         Build app dist and ttyd static assets only"
    echo "  build          Build for current/specified platform (default: linux/amd64)"
    echo "  all            Cross-compile all platforms to dist/"
    echo "  test-go [pkg] [go test flags...]"
    echo "                 Run Go tests through the repo embed-prep pipeline"
    echo "                 default pkg: ./mgr/..."
    echo "  docker [tag]       Build app Docker image using BASE_IMAGE"
    echo "                     default BASE_IMAGE: ${GLOBAL_JSON} images.base / cicy-cluster.base_image"
    echo "                     fallback: cicy-code-base:\$(versions.json.base)"
    echo "  docker-base [tag]  Build reusable base Docker image"
    echo "                     default tag: versions.json base (fallback: latest)"
    echo "  -h, --help     Show this help message"
    echo ""
    echo "Arguments:"
    echo "  os             Target OS: linux, darwin (default: linux)"
    echo "  arch           Target arch: amd64, arm64 (default: amd64)"
    echo ""
    echo "Environment variables:"
    echo "  SKIP_NPM=1     Skip npm ci + npm run build (reuse existing app/dist)"
    echo "  SKIP_TTYD_ASSET=1  Skip ttyd bindata refresh (reuse existing api/server/asset.go)"
    echo "  CDN=1          Build app/ttyd asset URLs with COS CDN prefix from ${GLOBAL_JSON}.tencent"
    echo "  BASE_IMAGE     Base image for docker command"
    echo "  NO_CACHE=1     Disable Docker layer cache"
    exit 0
    ;;
  *)
    echo "Usage: ./build.sh [build|all|test-go|docker|docker-base] [os] [arch]"
    echo "  assets         Build app dist and ttyd static assets only"
    echo "  build          Build for current/specified platform (default: linux/amd64)"
    echo "  all            Cross-compile all platforms to dist/"
    echo "  test-go [pkg] [go test flags...]"
    echo "  docker [tag]       Build app Docker image"
    echo "  docker-base [tag]  Build reusable base Docker image"
    echo ""
    echo "Env vars:"
    echo "  SKIP_NPM=1     Skip npm ci + npm run build (reuse existing app/dist)"
    echo "  SKIP_TTYD_ASSET=1  Skip ttyd bindata refresh (reuse existing api/server/asset.go)"
    echo "  CDN=1          Build app/ttyd asset URLs with COS CDN prefix from ${GLOBAL_JSON}.tencent"
    echo "  BASE_IMAGE     Base image for docker command"
    echo "  NO_CACHE=1     Disable Docker layer cache"
    exit 1
    ;;
esac
