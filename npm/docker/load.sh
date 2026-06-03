#!/bin/sh
# Load the cicy-code base-environment Docker image from R2 (CN-friendly: CF
# edge, no Docker Hub pull, no ICP filter). The image is a slowly-changing
# base env — published to R2 occasionally with publish-docker.sh, NOT on every
# cicy-code release. Per-version binaries ship via npm (`npx cicy-code`).
#
#   curl -fsSL https://r2.deepfetch.de5.net/docker/load.sh | sh
#   CICY_DOCKER_URL=<url> curl ... | sh    # pin a specific version
set -eu

URL="${CICY_DOCKER_URL:-https://r2.deepfetch.de5.net/docker/cicy-code-latest.tar.gz}"

command -v docker >/dev/null 2>&1 || { echo "cicy-code: docker not found" >&2; exit 1; }
docker version >/dev/null 2>&1 || { echo "cicy-code: docker daemon not running" >&2; exit 1; }

echo "cicy-code: loading base image from $URL"
# docker load auto-decompresses gzip; stream it straight in (no temp file).
curl -fsSL --max-time 900 "$URL" | docker load
echo "cicy-code: done — see \`docker images | grep cicy-code\`"
