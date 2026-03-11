import json
import time
import redis
from mitmproxy import http

r = redis.Redis(host='127.0.0.1', port=16379, decode_responses=True)

def response(flow: http.HTTPFlow):
    auth = flow.metadata.get("proxyauth")
    if not auth:
        return
    pane_id = auth[0]

    ts = int(time.time())
    req_body = (flow.request.content or b'').decode('utf-8', errors='ignore')
    res_body = (flow.response.content or b'').decode('utf-8', errors='ignore')
    req_kb = round(len(flow.request.content or b'') / 1024, 1)
    res_kb = round(len(flow.response.content or b'') / 1024, 1)
    url = flow.request.pretty_url
    method = flow.request.method
    status = flow.response.status_code

    req_headers = dict(flow.request.headers)
    res_headers = dict(flow.response.headers)

    entry = json.dumps({
        "pane": pane_id, "method": method, "url": url,
        "req_kb": req_kb, "res_kb": res_kb, "status": status, "ts": ts,
        "req_headers": req_headers, "res_headers": res_headers,
        "req_body": req_body, "res_body": res_body,
    })
    r.lpush('kiro_http_log', entry)
    r.ltrim('kiro_http_log', 0, 9999)
    r.publish('kiro_traffic_live', entry)
