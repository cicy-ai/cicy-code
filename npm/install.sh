#!/bin/sh
# cicy-code bootstrap installer — picks the fastest npm registry, then installs.
#
#   curl -fsSL https://<host>/code | sh
#
# Why a bootstrap: cicy-code ships its binary as a per-platform npm
# optionalDependency, so the ~30MB download happens during `npm install`
# itself — before any of cicy-code's own code runs. The package therefore
# can't choose its own mirror; only this script (which runs first) can.
# It latency-probes the candidate registries and installs from the nearest,
# so CN users land on npmmirror and everyone else on npmjs automatically.
set -eu

PKG="cicy-code"
# Candidate registries, probed in order. First reachable + fastest wins.
REGISTRIES="https://registry.npmmirror.com https://registry.npmjs.org"

command -v npm >/dev/null 2>&1 || {
  echo "cicy-code: npm not found. Install Node.js (>=14) first: https://nodejs.org" >&2
  exit 1
}

echo "cicy-code: probing registries..."
best=""; best_ms=999999
for r in $REGISTRIES; do
  # time_total of a tiny metadata GET is a good proxy for both the
  # metadata round-trip and the CDN that will serve the binary tarball.
  t=$(curl -o /dev/null -s -w '%{time_total}' --max-time 5 "$r/$PKG/latest" 2>/dev/null || echo "")
  if [ -z "$t" ]; then
    echo "  $r -> unreachable"
    continue
  fi
  ms=$(awk "BEGIN{printf \"%d\", $t*1000}")
  echo "  $r -> ${ms}ms"
  if [ "$ms" -lt "$best_ms" ]; then best_ms=$ms; best=$r; fi
done

[ -n "$best" ] || best="https://registry.npmjs.org"
echo "cicy-code: using $best (${best_ms}ms)"

# Resolve the authoritative latest version from npmjs, then pin it. npmmirror
# syncs new *versions* within seconds but its `latest` dist-tag lags by
# minutes — so an unpinned `npm i` against npmmirror can fetch a stale version
# right after a release. Pinning the npmjs-resolved version sidesteps that (the
# version itself is already mirrored on npmmirror, just not tagged latest yet).
ver=$(curl -fsSL --max-time 8 "https://registry.npmjs.org/$PKG/latest" 2>/dev/null \
  | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{try{process.stdout.write(String(JSON.parse(s).version||""))}catch{}})' 2>/dev/null || echo "")
spec="$PKG"; [ -n "$ver" ] && spec="$PKG@$ver"
echo "cicy-code: installing $spec"

# Global install so `cicy-code` lands on PATH. The chosen registry serves
# both the main package and the matching platform binary sub-package.
npm install -g "$spec" --registry="$best"

echo "cicy-code: installed. Run:  cicy-code --help"
