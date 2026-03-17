#!/usr/bin/env python3
"""Upload only static assets to COS with version prefix"""
import os, json, hashlib, hmac, time, mimetypes, requests

conf = json.load(open(os.path.expanduser('~/global.json')))['tencent']
SID, SKEY = conf['secret_id'], conf['secret_key']
BUCKET, REGION = conf['bucket'], conf['region']
HOST = f"{BUCKET}.cos.{REGION}.myqcloud.com"
SRC = os.path.expanduser('~/projects/cicy-web/assets')
VER = 'v1'

def sign(method, path):
    now = int(time.time())
    key_time = f"{now};{now+3600}"
    sign_key = hmac.new(SKEY.encode(), key_time.encode(), hashlib.sha1).hexdigest()
    http_str = f"{method.lower()}\n{path}\n\n\n"
    sha = hashlib.sha1(http_str.encode()).hexdigest()
    str_to_sign = f"sha1\n{key_time}\n{sha}\n"
    sig = hmac.new(sign_key.encode(), str_to_sign.encode(), hashlib.sha1).hexdigest()
    return (f"q-sign-algorithm=sha1&q-ak={SID}&q-sign-time={key_time}"
            f"&q-key-time={key_time}&q-header-list=&q-url-param-list=&q-signature={sig}")

ok = 0
for f in os.listdir(SRC):
    local = os.path.join(SRC, f)
    if not os.path.isfile(local): continue
    key = f"/landing/{VER}/assets/{f}"
    ct = mimetypes.guess_type(f)[0] or 'application/octet-stream'
    data = open(local, 'rb').read()
    r = requests.put(f"https://{HOST}{key}", data=data,
        headers={'Host': HOST, 'Content-Type': ct, 'Authorization': sign('put', key)})
    s = '✓' if r.status_code in (200, 204) else f'✗ {r.status_code}'
    print(f"{s} {key}")
    if r.status_code in (200, 204): ok += 1

print(f"\n{ok} files → https://{HOST}/landing/{VER}/assets/")
