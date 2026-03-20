#!/bin/bash
# Run mitmdump with all *.monitor.py addons
ADDONS=""
for f in "$(dirname "$0")"/*.monitor.py; do
  [ -f "$f" ] && ADDONS="$ADDONS -s $f"
done
exec mitmdump -p ${MITMPROXY_PORT:-8003} --ssl-insecure --set block_global=false --set proxyauth=any $ADDONS
