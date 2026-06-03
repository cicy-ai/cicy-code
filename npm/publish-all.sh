#!/usr/bin/env bash
# Build + publish cicy-code as a main launcher package plus four
# platform-specific binary sub-packages (esbuild-style optionalDependencies).
#
# Each sub-package carries ONE prebuilt binary and pins os/cpu, so `npm install
# cicy-code` pulls only the slice matching the user's machine (~30MB) straight
# from the registry — no GitHub, no postinstall download.
#
# Usage:
#   NPM_TOKEN=xxx ./publish-all.sh <npm-version> [gh-tag] [--dry-run]
# Example (npm 2.1.47 wrapping binaries from GitHub release v2.1.46):
#   NPM_TOKEN=xxx ./publish-all.sh 2.1.47 v2.1.46
# gh-tag defaults to v<npm-version>.
set -euo pipefail

VERSION="${1:?usage: publish-all.sh <npm-version> [gh-tag] [--dry-run]}"
GH_TAG="${2:-v$VERSION}"
DRY=""
for a in "$@"; do [ "$a" = "--dry-run" ] && DRY="--dry-run"; done

HERE="$(cd "$(dirname "$0")" && pwd)"
BUILD="$HERE/.build"
REL="https://github.com/cicy-ai/cicy-code/releases/download/$GH_TAG"
REPO_URL="git+https://github.com/cicy-ai/cicy-code.git"

# npm platform key  ->  GitHub release asset name
declare -A ASSET=(
  [darwin-arm64]=cicy-code-darwin-arm64
  [darwin-x64]=cicy-code-darwin-amd64
  [linux-x64]=cicy-code-linux-amd64
  [linux-arm64]=cicy-code-linux-arm64
)

rm -rf "$BUILD"; mkdir -p "$BUILD"

echo "==> Building sub-packages for cicy-code@$VERSION (binaries from $GH_TAG)"
for key in "${!ASSET[@]}"; do
  os="${key%-*}"; cpu="${key#*-}"
  pkg="cicy-code-$key"
  dir="$BUILD/$pkg"
  mkdir -p "$dir"
  echo "  - $pkg  (os:$os cpu:$cpu)  <- ${ASSET[$key]}"
  code=$(curl -sL -o "$dir/cicy-code" -w "%{http_code}" --max-time 120 "$REL/${ASSET[$key]}")
  [ "$code" = "200" ] || { echo "    !! download failed http=$code"; exit 1; }
  chmod 755 "$dir/cicy-code"
  sz=$(stat -c%s "$dir/cicy-code" 2>/dev/null || stat -f%z "$dir/cicy-code")
  [ "$sz" -gt 1000000 ] || { echo "    !! binary too small ($sz bytes) — bad download"; exit 1; }
  cat > "$dir/package.json" <<JSON
{
  "name": "$pkg",
  "version": "$VERSION",
  "description": "cicy-code prebuilt binary for $key",
  "os": ["$os"],
  "cpu": ["$cpu"],
  "files": ["cicy-code"],
  "license": "MIT",
  "author": { "name": "cicybot", "email": "support@cicy-ai.com" },
  "repository": { "type": "git", "url": "$REPO_URL" }
}
JSON
done

# Auth (only when actually publishing)
NPMRC=""
if [ -z "$DRY" ]; then
  : "${NPM_TOKEN:?set NPM_TOKEN to publish (or pass --dry-run)}"
  NPMRC="$(mktemp)"; umask 077
  printf '//registry.npmjs.org/:_authToken=%s\n' "$NPM_TOKEN" > "$NPMRC"
fi
PUB=(npm publish --registry https://registry.npmjs.org --access public)
[ -n "$NPMRC" ] && PUB+=(--userconfig "$NPMRC")
[ -n "$DRY" ] && PUB+=(--dry-run)

# Publish sub-packages first so the main package's optionalDependencies resolve.
echo "==> Publishing 4 sub-packages ${DRY:+(dry-run)}"
for key in "${!ASSET[@]}"; do
  ( cd "$BUILD/cicy-code-$key" && "${PUB[@]}" ) 2>&1 | grep -iE "^\+ |E40[0-9]|error|notice .*publishing" || true
done

echo "==> Publishing main package cicy-code@$VERSION ${DRY:+(dry-run)}"
( cd "$HERE" && "${PUB[@]}" ) 2>&1 | grep -iE "^\+ |E40[0-9]|error|notice .*publishing" || true

[ -n "$NPMRC" ] && rm -f "$NPMRC"
echo "==> Done."
