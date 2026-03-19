#!/usr/bin/env python3
"""Upload static assets to COS with version prefix.

Usage:
  python3 cos-upload.py <target>       # target: app, ttyd, landing, or all
  python3 cos-upload.py all            # upload all targets
"""
import os, sys, json, hashlib, hmac, time, mimetypes, requests

ROOT = os.path.dirname(os.path.abspath(__file__))
conf = json.load(open(os.path.expanduser('~/global.json')))['tencent']
SID, SKEY = conf['secret_id'], conf['secret_key']
BUCKET, REGION = conf['bucket'], conf['region']
HOST = f"{BUCKET}.cos.{REGION}.myqcloud.com"
versions = json.load(open(os.path.join(ROOT, '..', 'versions.json')))

TARGETS = {
    'app':     {'src': os.path.join(ROOT, '../app-worker/public/assets'), 'prefix': 'app',  'key': 'app',     'flat': True},
    'ttyd':    {'src': os.path.join(ROOT, 'api/static'),               'prefix': 'ttyd', 'key': 'ttyd',    'flat': False},
    'landing': {'src': os.path.expanduser('~/projects/cicy-landing/public/assets'), 'prefix': 'landing', 'key': 'landing', 'flat': True},
}

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

def upload(target):
    t = TARGETS[target]
    src, prefix = t['src'], t['prefix']
    ver = 'v' + versions.get(t['key'], '1')
    if not os.path.isdir(src):
        print(f"✗ {target}: {src} not found")
        return 0

    print(f"=== {target} {ver} ===")
    ok = 0
    for root, dirs, files in os.walk(src):
        for f in files:
            local = os.path.join(root, f)
            if t['flat']:
                key = f"/{prefix}/{ver}/assets/{f}"
            else:
                rel = os.path.relpath(local, src)
                key = f"/{prefix}/{ver}/{rel}"
            ct = mimetypes.guess_type(f)[0] or 'application/octet-stream'
            data = open(local, 'rb').read()
            r = requests.put(f"https://{HOST}{key}", data=data,
                headers={'Host': HOST, 'Content-Type': ct, 'Authorization': sign('put', key)})
            s = '✓' if r.status_code in (200, 204) else f'✗ {r.status_code}'
            print(f"  {s} {key}")
            if r.status_code in (200, 204): ok += 1
    print(f"  {ok} files → https://{HOST}/{prefix}/{ver}/\n")
    return ok

if __name__ == '__main__':
    if len(sys.argv) < 2 or sys.argv[1] not in list(TARGETS) + ['all']:
        print(f"Usage: {sys.argv[0]} <{'|'.join(list(TARGETS) + ['all'])}>")
        sys.exit(1)
    targets = list(TARGETS) if sys.argv[1] == 'all' else [sys.argv[1]]
    total = sum(upload(t) for t in targets)
    print(f"Total: {total} files uploaded")
