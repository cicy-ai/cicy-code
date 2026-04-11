#!/usr/bin/env python3
"""Upload static assets to COS with version prefix.

Usage:
  python3 cos-upload.py <target>       # target: app, ttyd, landing, or all
  python3 cos-upload.py all            # upload all targets
"""
import os, sys, json, hashlib, hmac, time, mimetypes, shutil, tempfile, requests

ROOT = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.abspath(os.path.join(ROOT, '..'))
conf = json.load(open(os.path.expanduser('~/global.json')))['tencent']
SID, SKEY = conf['secret_id'], conf['secret_key']
BUCKET, REGION = conf['bucket'], conf['region']
HOST = f"{BUCKET}.cos.{REGION}.myqcloud.com"
versions = json.load(open(os.path.join(REPO_ROOT, 'versions.json')))

TARGETS = {
    'app':     {'src': os.path.join(REPO_ROOT, 'app', 'dist', 'assets'), 'prefix': 'app',  'key': 'app',     'flat': False, 'base_dir': 'assets'},
    'ttyd':    {'prefix': 'ttyd', 'key': 'ttyd', 'flat': False, 'base_dir': ''},
    'landing': {'src': os.path.expanduser('~/projects/cicy-landing/public/assets'), 'prefix': 'landing', 'key': 'landing', 'flat': True, 'base_dir': 'assets'},
}

SESSION = requests.Session()
SESSION.trust_env = False


def first_existing(paths):
    for path in paths:
        if path and os.path.isfile(path):
            return path
    return None


def prepare_ttyd_assets():
    bundle = first_existing([
        os.path.join(REPO_ROOT, 'api/js/dist/gotty-bundle.js'),
        os.path.join(REPO_ROOT, 'api/bindata/static/js/gotty-bundle.js'),
    ])
    xterm_css = first_existing([
        os.path.join(REPO_ROOT, 'api/js/node_modules/xterm/dist/xterm.css'),
        os.path.join(REPO_ROOT, 'api/bindata/static/css/xterm.css'),
    ])

    required = {
        'index.html': os.path.join(REPO_ROOT, 'api/resources/index.html'),
        'favicon.png': os.path.join(REPO_ROOT, 'api/resources/favicon.png'),
        'css/index.css': os.path.join(REPO_ROOT, 'api/resources/index.css'),
        'css/xterm_customize.css': os.path.join(REPO_ROOT, 'api/resources/xterm_customize.css'),
        'css/xterm.css': xterm_css,
        'gotty-bundle.js': bundle,
    }
    missing = [name for name, path in required.items() if not path or not os.path.isfile(path)]
    if missing:
        raise FileNotFoundError(
            "missing ttyd assets: "
            + ", ".join(missing)
            + " (run api JS build / asset build first)"
        )

    temp_dir = tempfile.TemporaryDirectory(prefix='cicy-ttyd-assets-')
    asset_root = temp_dir.name
    copies = [
        (required['index.html'], 'index.html'),
        (required['favicon.png'], 'favicon.png'),
        (required['css/index.css'], 'css/index.css'),
        (required['css/xterm.css'], 'css/xterm.css'),
        (required['css/xterm_customize.css'], 'css/xterm_customize.css'),
        (required['gotty-bundle.js'], 'gotty-bundle.js'),
        (required['gotty-bundle.js'], 'js/gotty-bundle.js'),
    ]
    for src, rel in copies:
        dst = os.path.join(asset_root, rel)
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        shutil.copy2(src, dst)

    return temp_dir, asset_root

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
    temp_dir = None
    if target == 'ttyd':
        temp_dir, src = prepare_ttyd_assets()
    else:
        src = t['src']
    prefix = t['prefix']
    ver = 'v' + versions.get(t['key'], '1')
    try:
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
                    base_dir = t.get('base_dir', '').strip('/')
                    key_rel = f"{base_dir}/{rel}" if base_dir else rel
                    key = f"/{prefix}/{ver}/{key_rel}"
                ct = mimetypes.guess_type(f)[0] or 'application/octet-stream'
                with open(local, 'rb') as fh:
                    data = fh.read()
                r = None
                last_err = ""
                for attempt in range(3):
                    try:
                        r = SESSION.put(
                            f"https://{HOST}{key}",
                            data=data,
                            headers={'Host': HOST, 'Content-Type': ct, 'Authorization': sign('put', key)},
                            timeout=(10, 180),
                        )
                        if r.status_code in (200, 204):
                            break
                        last_err = f"{r.status_code}"
                    except Exception as e:
                        last_err = str(e)
                    if attempt < 2:
                        time.sleep(1.5 * (attempt + 1))
                if r is None:
                    print(f"  ✗ {key} ({last_err})")
                    continue
                s = '✓' if r.status_code in (200, 204) else f'✗ {r.status_code}'
                print(f"  {s} {key}")
                if r.status_code in (200, 204):
                    ok += 1
        print(f"  {ok} files → https://{HOST}/{prefix}/{ver}/\n")
        return ok
    finally:
        if temp_dir is not None:
            temp_dir.cleanup()

if __name__ == '__main__':
    if len(sys.argv) < 2 or sys.argv[1] not in list(TARGETS) + ['all']:
        print(f"Usage: {sys.argv[0]} <{'|'.join(list(TARGETS) + ['all'])}>")
        sys.exit(1)
    targets = list(TARGETS) if sys.argv[1] == 'all' else [sys.argv[1]]
    total = sum(upload(t) for t in targets)
    print(f"Total: {total} files uploaded")
