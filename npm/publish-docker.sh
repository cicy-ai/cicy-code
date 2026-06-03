#!/usr/bin/env bash
# Upload the cicy-code runtime image to R2 as a base-environment tarball.
#
# Docker images are too big for npm (a 242 MB tarball gets HTTP 413 from the
# registry), and the image is just a slowly-changing base environment — so it
# lives on R2 and is published OCCASIONALLY (run this by hand when the env
# changes), NOT on every release. Per-version binaries ship via npm.
#
# Uploads two keys so callers can pin or float:
#   /docker/cicy-code-<version>.tar.gz   (immutable, pinnable)
#   /docker/cicy-code-latest.tar.gz      (what load.sh pulls by default)
# Also (re)uploads load.sh so `curl …/docker/load.sh | sh` stays current.
#
# Usage (needs R2 creds in env, e.g. from ~/cicy-ai/db/r2.json):
#   CLOUDFLARE_ACCOUNT_ID=.. CLOUDFLARE_API_TOKEN=.. ./publish-docker.sh <version> [image-ref]
set -euo pipefail

VERSION="${1:?usage: publish-docker.sh <version> [image-ref]}"
IMAGE="${2:-docker.io/cicybot/cicy-code:${VERSION}}"
HERE="$(cd "$(dirname "$0")" && pwd)"
BUCKET="cicy-assets-poc"
WR="npx --yes wrangler@3.114.17"
TMP="$(mktemp -d)"
TAR="$TMP/cicy-code.tar.gz"

: "${CLOUDFLARE_ACCOUNT_ID:?set CLOUDFLARE_ACCOUNT_ID}"
: "${CLOUDFLARE_API_TOKEN:?set CLOUDFLARE_API_TOKEN}"

echo "==> docker save $IMAGE | gzip"
docker save "$IMAGE" | gzip > "$TAR"
sz=$(stat -c%s "$TAR" 2>/dev/null || stat -f%z "$TAR")
[ "$sz" -gt 1000000 ] || { echo "!! tarball too small ($sz bytes)"; exit 1; }
echo "    $(awk "BEGIN{printf \"%.0f MB\", $sz/1024/1024}")"

put() {  # <local> <key> <content-type>
  echo "==> R2 put $2"
  $WR r2 object put "$BUCKET/$2" --file="$1" --content-type="$3" >/dev/null 2>&1 \
    && echo "    https://r2.deepfetch.de5.net/$2" || { echo "    !! failed"; exit 1; }
}

put "$TAR" "docker/cicy-code-${VERSION}.tar.gz" "application/gzip"
put "$TAR" "docker/cicy-code-latest.tar.gz"     "application/gzip"
put "$HERE/docker/load.sh" "docker/load.sh"     "text/x-shellscript"

rm -rf "$TMP"
echo "==> Done. Pull:  curl -fsSL https://r2.deepfetch.de5.net/docker/load.sh | sh"
