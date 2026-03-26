#!/bin/bash
set -euo pipefail
# 开发模式：改 api/resources/ 下的文件实时生效
cd "$(dirname "$0")"

PORT="${PORT:-8008}"
existing_pid="$(lsof -tiTCP:${PORT} -sTCP:LISTEN 2>/dev/null | head -n 1 || true)"
if [[ -n "${existing_pid}" ]]; then
  cmd="$(ps -p "${existing_pid}" -o command= 2>/dev/null || true)"
  if [[ "${cmd}" == *"cicy-code"* || "${cmd}" == *"cicy-code-api"* ]]; then
    echo "[dev] stop existing cicy process on :${PORT} (pid=${existing_pid})"
    kill "${existing_pid}" 2>/dev/null || true
    for _ in $(seq 1 30); do
      if ! lsof -tiTCP:${PORT} -sTCP:LISTEN >/dev/null 2>&1; then
        break
      fi
      sleep 0.2
    done
  else
    echo "[dev] port ${PORT} is in use by non-cicy process: ${cmd}"
    exit 1
  fi
fi

PLATFORM=$(uname -s | tr '[:upper:]' '[:lower:]')
[[ "$PLATFORM" == "darwin" ]] || PLATFORM="linux"
SKIP_NPM=1 ./build.sh build $PLATFORM
cd api && ./cicy-code --dev
