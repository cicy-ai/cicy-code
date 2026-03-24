#!/bin/bash
set -euo pipefail
# 开发模式：改 api/resources/ 下的文件实时生效
cd "$(dirname "$0")"

PLATFORM=$(uname -s | tr '[:upper:]' '[:lower:]')
[[ "$PLATFORM" == "darwin" ]] || PLATFORM="linux"
SKIP_NPM=1 ./build.sh build $PLATFORM
cd api && ./cicy-code --dev