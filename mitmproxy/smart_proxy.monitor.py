# smart_proxy.monitor.py — 域名分流 + 审计拦截
# 名单内域名解密监控，其余全部透传
import json
import os
import ipaddress
import time
import threading
from mitmproxy import http, tls, ctx

RULES_FILE = os.path.join(os.path.expanduser("~"), ".cicy", "monitor", "rules.json")
_rules_mtime = 0
_rules = {"monitor_domains": [], "blocked_patterns": []}


def _load_rules():
    global _rules, _rules_mtime
    try:
        st = os.stat(RULES_FILE)
        if st.st_mtime == _rules_mtime:
            return
        with open(RULES_FILE) as f:
            _rules = json.load(f)
        _rules_mtime = st.st_mtime
        ctx.log.info(f"[audit] rules reloaded: {len(_rules.get('monitor_domains', []))} domains, {len(_rules.get('blocked_patterns', []))} patterns")
    except Exception:
        pass


def _match_monitor(host):
    domains = _rules.get("monitor_domains", [])
    return any(host == d or host.endswith("." + d) for d in domains)


class SmartProxy:
    def __init__(self):
        _load_rules()
        # Reload rules every 5s in background
        def _reload_loop():
            while True:
                time.sleep(5)
                _load_rules()
        threading.Thread(target=_reload_loop, daemon=True).start()

    def tls_clienthello(self, data: tls.ClientHelloData):
        _load_rules()
        sni = data.context.client.sni or ""
        if not _match_monitor(sni):
            data.ignore_connection = True

    def request(self, flow: http.HTTPFlow):
        patterns = _rules.get("blocked_patterns", [])
        if not patterns:
            return
        try:
            content = (flow.request.content or b"").decode("utf-8", errors="ignore")
            for p in patterns:
                if p in content:
                    ctx.log.warn(f"[audit] BLOCKED: matched '{p}' in {flow.request.pretty_url}")
                    flow.response = http.Response.make(
                        403,
                        json.dumps({"error": "blocked by audit policy", "rule": p}),
                        {"Content-Type": "application/json"},
                    )
                    return
        except Exception:
            pass


addons = [SmartProxy()]
