#!/usr/bin/env bash
set -euo pipefail

BASE_IMAGE="${BASE_IMAGE:-cicy-skills-base-user-env:local}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

tar \
  --exclude='./.git' \
  --exclude='./node_modules' \
  --exclude='./providers/google-node/node_modules' \
  --exclude='./cicy-skills' \
  --exclude='./cicy-skillsd' \
  -C "$ROOT_DIR" \
  -cf - . \
  | docker run --rm -i "$BASE_IMAGE" /bin/bash -c '
      set -euo pipefail
      rm -rf /tmp/cicy-skills-src
      mkdir -p /tmp/cicy-skills-src
      tar -xf - -C /tmp/cicy-skills-src
      cd /tmp/cicy-skills-src
      /bin/bash docker/google-smoke/smoke.sh
    '
