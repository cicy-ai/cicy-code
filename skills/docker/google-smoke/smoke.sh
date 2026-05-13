#!/usr/bin/env bash
set -euo pipefail

if [[ ! -f /.dockerenv && "${SMOKE_ALLOW_HOST:-}" != "1" ]]; then
  echo "refusing to run smoke.sh on host; use docker/google-smoke/run-in-base.sh or set SMOKE_ALLOW_HOST=1" >&2
  exit 1
fi

SMOKE_HOME="${SMOKE_HOME:-}"
if [[ -z "$SMOKE_HOME" ]]; then
  SMOKE_HOME="$(mktemp -d "${TMPDIR:-/tmp}/cicy-skills-smoke-home.XXXXXX")"
  trap 'rm -rf "$SMOKE_HOME"' EXIT
fi

export HOME="$SMOKE_HOME"
export PATH="$HOME/.local/bin:$PATH"

mkdir -p "$HOME/.local/bin" "$HOME/Private"

cat > "$HOME/cicy-ai/global.json" <<'JSON'
{
  "GMAIL_CLIENT_ID": "dummy-client-id",
  "GMAIL_CLIENT_SECRET": "dummy-client-secret",
  "GMAIL_REFRESH_TOKEN": "dummy-refresh-token"
}
JSON

if [[ ! -x ./dist/cicy-skills || ! -x ./dist/cicy-skillsd || ! -x ./dist/cicy-hosttools ]]; then
  echo "missing host-built binaries in ./dist; run make build-local-binaries first" >&2
  exit 1
fi

cp ./dist/cicy-skills /tmp/cicy-skills
cp ./dist/cicy-skillsd /tmp/cicy-skillsd
cp ./dist/cicy-hosttools /tmp/cicy-hosttools

/tmp/cicy-skills config-path >/tmp/cicy-skills-config-path.txt
if ! /tmp/cicy-skills init-config >/tmp/cicy-skills-init.txt 2>/tmp/cicy-skills-init.err; then
  grep -q 'config already exists' /tmp/cicy-skills-init.err
fi
/tmp/cicy-skills install all

test -L "./bin/google"
test ! -e "./bin/gmail"
test -L "$HOME/.local/bin/google"
test ! -e "$HOME/.local/bin/gmail"
test -L "$HOME/.local/bin/gpt"

google | grep -q 'Available services:'
google gmail help | grep -q 'Usage: google gmail <list|read|read-all|send|watch>'

echo "google-node smoke test passed"
