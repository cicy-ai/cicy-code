#!/usr/bin/env bash
# Package the cicy-code runtime Docker image as an npm package and publish it,
# then auto-trigger the npmmirror (CN) cache sync. Lets CN users get the image
# via `npx cicy-code-docker` (fetched from npmmirror — fast, no Docker Hub
# pull) which `docker load`s it locally.
#
# Usage:
#   NPM_TOKEN=xxx ./publish-docker.sh <version> [image-ref] [--dry-run]
# image-ref defaults to docker.io/cicybot/cicy-code:<version>.
set -euo pipefail

VERSION="${1:?usage: publish-docker.sh <version> [image-ref] [--dry-run]}"
IMAGE="docker.io/cicybot/cicy-code:${VERSION}"
case "${2:-}" in ""|--dry-run) ;; *) IMAGE="$2" ;; esac
DRY=""; for a in "$@"; do [ "$a" = "--dry-run" ] && DRY="--dry-run"; done

HERE="$(cd "$(dirname "$0")" && pwd)"
BUILD="$HERE/.build-docker/cicy-code-docker"
MIRROR="https://registry.npmmirror.com"
REPO_URL="git+https://github.com/cicy-ai/cicy-code.git"
PKG="cicy-code-docker"

rm -rf "$HERE/.build-docker"; mkdir -p "$BUILD"
cp "$HERE/docker/load.js" "$BUILD/load.js"

echo "==> docker save $IMAGE | gzip -> cicy-code.tar.gz"
docker save "$IMAGE" | gzip > "$BUILD/cicy-code.tar.gz"
sz=$(stat -c%s "$BUILD/cicy-code.tar.gz" 2>/dev/null || stat -f%z "$BUILD/cicy-code.tar.gz")
[ "$sz" -gt 1000000 ] || { echo "    !! tarball too small ($sz bytes) — bad save"; exit 1; }
echo "    tarball: $(awk "BEGIN{printf \"%.0f MB\", $sz/1024/1024}")"

cat > "$BUILD/package.json" <<JSON
{
  "name": "$PKG",
  "version": "$VERSION",
  "description": "cicy-code runtime Docker image as a docker-load tarball — pull via npm/npmmirror (CN-friendly, no Docker Hub). Run: npx cicy-code-docker",
  "bin": { "cicy-code-docker": "load.js" },
  "files": ["load.js", "cicy-code.tar.gz"],
  "keywords": ["cicy", "docker", "image", "cn", "npmmirror"],
  "license": "MIT",
  "author": { "name": "cicybot", "email": "support@cicy-ai.com" },
  "repository": { "type": "git", "url": "$REPO_URL" }
}
JSON

# Auth
NPMRC=""
if [ -z "$DRY" ]; then
  : "${NPM_TOKEN:?set NPM_TOKEN to publish (or pass --dry-run)}"
  NPMRC="$(mktemp)"; umask 077
  printf '//registry.npmjs.org/:_authToken=%s\n' "$NPM_TOKEN" > "$NPMRC"
fi
PUB=(npm publish --registry https://registry.npmjs.org --access public)
[ -n "$NPMRC" ] && PUB+=(--userconfig "$NPMRC")
[ -n "$DRY" ] && PUB+=(--dry-run)

echo "==> Publishing $PKG@$VERSION ${DRY:+(dry-run)} ($(awk "BEGIN{printf \"%.0f MB\", $sz/1024/1024}"))"
( cd "$BUILD" && "${PUB[@]}" ) 2>&1 | grep -iE "^\+ |E40[0-9]|error|notice .*publishing|Tarball" || true

# Auto-trigger npmmirror sync (success = version installable on npmmirror).
if [ -z "$DRY" ]; then
  curl -s --max-time 20 -X PUT "$MIRROR/-/package/$PKG/syncs" >/dev/null 2>&1 || true
  for _ in $(seq 1 15); do
    sleep 10
    code=$(curl -s --max-time 10 -o /dev/null -w '%{http_code}' "$MIRROR/$PKG/$VERSION" 2>/dev/null || echo 000)
    if [ "$code" = "200" ]; then echo "    ✓ npmmirror has $PKG@$VERSION"; break; fi
  done
fi

[ -n "$NPMRC" ] && rm -f "$NPMRC"
echo "==> Done. Pull: npx cicy-code-docker@$VERSION   (CN: --registry=https://registry.npmmirror.com)"
