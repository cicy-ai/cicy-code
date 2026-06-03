#!/usr/bin/env bash
# Build + publish cicy-code as a main launcher package plus four
# platform-specific binary sub-packages (esbuild-style optionalDependencies),
# then auto-trigger the npmmirror (CN) cache sync for everything published.
#
# Each sub-package carries ONE prebuilt binary and pins os/cpu, so `npm install
# cicy-code` pulls only the slice matching the user's machine (~30MB) straight
# from the registry — no GitHub, no postinstall download.
#
# Usage:
#   NPM_TOKEN=xxx ./publish-all.sh <npm-version> [gh-tag] [--main-only] [--dry-run]
# Examples:
#   NPM_TOKEN=xxx ./publish-all.sh 2.1.47 v2.1.46            # full: 4 subs + main
#   NPM_TOKEN=xxx ./publish-all.sh 2.1.49 v2.1.46 --main-only # launcher-only change
# gh-tag defaults to v<npm-version>. --main-only skips the binary sub-packages
# (their version stays whatever the main package's optionalDependencies pin).
set -euo pipefail

VERSION="${1:?usage: publish-all.sh <npm-version> [gh-tag] [--from DIR] [--main-only] [--dry-run]}"
GH_TAG="v$VERSION"; case "${2:-}" in v*) GH_TAG="$2";; esac
DRY=""; MAIN_ONLY=""; FROM_DIR=""
prev=""
for a in "$@"; do
  [ "$a" = "--dry-run" ] && DRY="--dry-run"
  [ "$a" = "--main-only" ] && MAIN_ONLY=1
  [ "$prev" = "--from" ] && FROM_DIR="$a"
  prev="$a"
done

HERE="$(cd "$(dirname "$0")" && pwd)"
BUILD="$HERE/.build"
REL="https://github.com/cicy-ai/cicy-code/releases/download/$GH_TAG"
REPO_URL="git+https://github.com/cicy-ai/cicy-code.git"
MIRROR="https://registry.npmmirror.com"

declare -A ASSET=(
  [darwin-arm64]=cicy-code-darwin-arm64
  [darwin-x64]=cicy-code-darwin-amd64
  [linux-x64]=cicy-code-linux-amd64
  [linux-arm64]=cicy-code-linux-arm64
)

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

# Auto-trigger npmmirror (CN) sync, then poll the abbreviated packument (the
# doc `npm install` actually reads) until it shows the new version. This is the
# "发布成功要自动触发 cn 缓存更新" step — without it, CN users briefly resolve
# a stale latest from npmmirror's per-edge cache after every publish.
sync_npmmirror() {
  local pkg="$1" want="$2"
  [ -n "$DRY" ] && { echo "    [dry-run] would sync $pkg on npmmirror"; return 0; }
  curl -s --max-time 20 -X PUT "$MIRROR/-/package/$pkg/syncs" >/dev/null 2>&1 || true
  for _ in $(seq 1 12); do
    sleep 10
    cur=$(curl -s --max-time 10 -H 'Accept: application/vnd.npm.install-v1+json' "$MIRROR/$pkg" \
      | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{try{console.log(JSON.parse(s)["dist-tags"].latest)}catch{console.log("?")}})' 2>/dev/null || echo "?")
    if [ "$cur" = "$want" ]; then echo "    ✓ npmmirror synced $pkg@$want"; return 0; fi
  done
  echo "    ! npmmirror sync for $pkg@$want still pending (will catch up on its own)"
}

publish_dir() {  # <dir> <pkgname> <version>
  ( cd "$1" && "${PUB[@]}" ) 2>&1 | grep -iE "^\+ |E40[0-9]|error|notice .*publishing" || true
  sync_npmmirror "$2" "$3"
}

if [ -z "$MAIN_ONLY" ]; then
  echo "==> Building sub-packages for cicy-code@$VERSION (binaries from $GH_TAG)"
  rm -rf "$BUILD"; mkdir -p "$BUILD"
  for key in "${!ASSET[@]}"; do
    os="${key%-*}"; cpu="${key#*-}"; pkg="cicy-code-$key"; dir="$BUILD/$pkg"
    mkdir -p "$dir"
    if [ -n "$FROM_DIR" ]; then
      echo "  - $pkg  (os:$os cpu:$cpu)  <- $FROM_DIR/${ASSET[$key]} (local)"
      cp "$FROM_DIR/${ASSET[$key]}" "$dir/cicy-code" || { echo "    !! missing $FROM_DIR/${ASSET[$key]}"; exit 1; }
    else
      echo "  - $pkg  (os:$os cpu:$cpu)  <- ${ASSET[$key]} (GH $GH_TAG)"
      code=$(curl -sL -o "$dir/cicy-code" -w "%{http_code}" --max-time 120 "$REL/${ASSET[$key]}")
      [ "$code" = "200" ] || { echo "    !! download failed http=$code"; exit 1; }
    fi
    chmod 755 "$dir/cicy-code"
    sz=$(stat -c%s "$dir/cicy-code" 2>/dev/null || stat -f%z "$dir/cicy-code")
    [ "$sz" -gt 1000000 ] || { echo "    !! binary too small ($sz bytes)"; exit 1; }
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
  echo "==> Publishing 4 sub-packages ${DRY:+(dry-run)} + npmmirror sync"
  for key in "${!ASSET[@]}"; do publish_dir "$BUILD/cicy-code-$key" "cicy-code-$key" "$VERSION"; done
else
  echo "==> --main-only: skipping binary sub-packages (kept at optionalDependencies pin)"
fi

echo "==> Publishing main package cicy-code@$VERSION ${DRY:+(dry-run)} + npmmirror sync"
if [ -n "$MAIN_ONLY" ]; then
  # Launcher-only patch: publish $HERE as-is (version + optionalDependencies
  # pin are whatever is committed — bump package.json before running).
  publish_dir "$HERE" "cicy-code" "$VERSION"
else
  # Full release: stage the launcher into .build so version + every
  # optionalDependency lockstep to $VERSION without mutating the repo file.
  MAIN="$BUILD/cicy-code"; mkdir -p "$MAIN/bin"
  cp "$HERE/bin/cicy-code.js" "$MAIN/bin/cicy-code.js"
  node -e '
    const fs=require("fs");
    const p=JSON.parse(fs.readFileSync(process.argv[1]));
    p.version=process.argv[2];
    for(const k of Object.keys(p.optionalDependencies||{})) p.optionalDependencies[k]=process.argv[2];
    fs.writeFileSync(process.argv[3], JSON.stringify(p,null,2)+"\n");
  ' "$HERE/package.json" "$VERSION" "$MAIN/package.json"
  publish_dir "$MAIN" "cicy-code" "$VERSION"
fi

[ -n "$NPMRC" ] && rm -f "$NPMRC"
echo "==> Done."
