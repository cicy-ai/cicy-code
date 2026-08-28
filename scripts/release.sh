#!/usr/bin/env bash
# Gate + release: refuses to tag unless type-check, frontend tests and Go
# vet/tests all pass. Usage: scripts/release.sh <version>
set -euo pipefail
V="${1:?usage: release.sh <version>}"
cd "$(dirname "$0")/.."
[ -z "$(git status --short | grep -v '^??')" ] || { echo "working tree not clean" >&2; exit 1; }
echo "== gate: tsc"; (cd app && npx tsc --noEmit)
echo "== gate: vitest"; (cd app && NODE_ENV=test npx vitest run --reporter=dot 2>&1 | tail -3)
echo "== gate: go vet + test"; (cd api && go vet ./mgr/ && go test ./mgr/ 2>&1 | tail -1)
echo "== bump $V"; python3 scripts/sync-version.py --set "$V"
git add -A .cicy_tmux.conf api/mgr/main.go app/package-lock.json app/package.json npm/package.json app/src/config.ts 2>/dev/null || true
git commit -q -m "chore(release): v$V"
git tag -a "v$V" -m "v$V"
echo "== push"
github configure --account cicy-ai . >/dev/null 2>&1 || true
git push cicy-ai main "v$V"
github configure --account cicy-dev-001 . >/dev/null 2>&1 || true
git push cicy-dev-001 main "v$V" || echo "(fork push failed — retry: git push cicy-dev-001 main v$V)"
echo "released v$V — CI publishes npm + GitHub release"
