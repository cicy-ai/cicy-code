mitmdump -p ${MITMPROXY_PORT:-8003} --ssl-insecure --set block_global=false --set proxyauth=any -s ./kiro_monitor.py
