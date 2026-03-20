#!/bin/bash
set -e
cd "$(dirname "$0")"

cleanup() {
  rm -rf mgr/resources mgr/ui mgr/tmux.conf mgr/monitor
}
trap cleanup EXIT

# 1. resources → mgr/resources (ws.go embed)
cp -r resources mgr/resources

# 2. app dist → mgr/ui (ui.go embed)
cd ../app && npm run build --silent && cd ../api
cp -r ../app/dist mgr/ui

# 3. .tmux.conf → mgr/tmux.conf (main.go embed)
cp ../.tmux.conf mgr/tmux.conf

# 4. mitmproxy → mgr/monitor (audit.go embed)
cp -r ../mitmproxy mgr/monitor

# 5. build
go build -o cicy-code-api ./mgr
