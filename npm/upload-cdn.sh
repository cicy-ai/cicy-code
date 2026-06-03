#!/usr/bin/env bash
# Upload the built App SPA (app/dist) to the per-release R2 directory
# /app/v<version>/ so `cicy-code --cdn` can serve that exact release's assets
# from Cloudflare R2. Each release uses its OWN path (see build.sh
# configure_cdn_env), so a fresh upload is never cache-poisoned by a prior
# version and there is no cross-version hash collision.
#
# Sets a correct Content-Type per file (R2/wrangler otherwise mis-tags .svg as
# application/xml, which Chrome ORB then blocks).
#
# Usage:
#   CLOUDFLARE_ACCOUNT_ID=.. CLOUDFLARE_API_TOKEN=.. ./upload-cdn.sh <version> [dist-dir]
set -euo pipefail

VERSION="${1:?usage: upload-cdn.sh <version> [dist-dir]}"
DIST="${2:-$(cd "$(dirname "$0")/.." && pwd)/app/dist}"
BUCKET="cicy-assets-poc"
WR="npx --yes wrangler@3.114.17"

[ -d "$DIST" ] || { echo "!! dist dir not found: $DIST" >&2; exit 1; }

mime() {
  case "${1##*.}" in
    js|mjs)  echo "text/javascript" ;;
    css)     echo "text/css" ;;
    svg)     echo "image/svg+xml" ;;
    png)     echo "image/png" ;;
    jpg|jpeg) echo "image/jpeg" ;;
    webp)    echo "image/webp" ;;
    gif)     echo "image/gif" ;;
    ico)     echo "image/x-icon" ;;
    json)    echo "application/json" ;;
    html)    echo "text/html; charset=utf-8" ;;
    woff2)   echo "font/woff2" ;;
    woff)    echo "font/woff" ;;
    ttf)     echo "font/ttf" ;;
    map)     echo "application/json" ;;
    txt)     echo "text/plain; charset=utf-8" ;;
    wasm)    echo "application/wasm" ;;
    *)       echo "application/octet-stream" ;;
  esac
}

cd "$DIST"
total=$(find . -type f | wc -l | tr -d ' ')
echo "==> Uploading $total files from $DIST -> R2 $BUCKET/app/v$VERSION/"
i=0
find . -type f | sed 's#^\./##' | while read -r rel; do
  i=$((i+1))
  ct="$(mime "$rel")"
  key="app/v$VERSION/$rel"
  if $WR r2 object put "$BUCKET/$key" --file="$rel" --content-type="$ct" >/dev/null 2>&1; then
    echo "  [$i/$total] $rel  ($ct)"
  else
    echo "  [$i/$total] !! FAILED $rel" >&2
    exit 1
  fi
done
echo "==> Done. Assets live at https://r2.deepfetch.de5.net/app/v$VERSION/"
