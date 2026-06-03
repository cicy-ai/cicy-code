#!/usr/bin/env python3
"""Upload static assets to Cloudflare R2 with a version prefix.

Replaces the legacy scripts/cos-upload.py (Tencent COS): COS returns HTTP 451
to overseas IPs and freezes the bucket on arrears. R2 has a permanent free
tier, no ICP filter, and CF edge caching — see the `cicy-r2` skill.

Usage:
  python3 r2-upload.py <target>       # target: app, ttyd, landing, or all
  python3 r2-upload.py all            # upload all targets

Uploads call `wrangler r2 object put` directly (NOT `cicy-r2 put`) so we can pass
an explicit --content-type per file. This matters: `wrangler r2 object put`
does NOT infer Content-Type from the extension, so without it R2 serves assets
with an empty Content-Type — and the browser then rejects ES module scripts
("Expected a JavaScript module script but the server responded with a MIME type
of \"\""). Auth (CLOUDFLARE_API_TOKEN/ACCOUNT_ID), the bucket, and the wrangler
working dir all come from ~/cicy-ai/db/r2.json + the cicy-r2 skill, so this stays
in sync with `cicy-r2`. The R2 key layout matches the old COS layout
(app/v3/..., ttyd/v2/..., landing/v1/assets/...) so only the host changes when
migrating references — rollback stays trivial.
"""
import os, re, sys, json, time, shutil, tempfile, subprocess

ROOT = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.abspath(os.path.join(ROOT, '..'))
CICY_ROOT_DIR = os.path.expanduser('~/cicy-ai')
R2_JSON_PATH = os.path.join(CICY_ROOT_DIR, 'db', 'r2.json')

_r2conf = json.load(open(R2_JSON_PATH))
PUBLIC_URL = _r2conf.get('public_url', 'https://r2.deepfetch.de5.net')
BUCKET = _r2conf['bucket']
WRANGLER_ENV = dict(
    os.environ,
    CLOUDFLARE_API_TOKEN=_r2conf['api_token'],
    CLOUDFLARE_ACCOUNT_ID=_r2conf['account_id'],
)


def _discover_wrangler_cwd():
    """`wrangler` must run from a dir that has node_modules/wrangler. The cicy-r2
    CLI hardcodes that dir — read it back so we stay in sync; fall back to the
    known render-worker dir."""
    fallback = os.path.expanduser('~/projects/cicy-desktop/workers/render')
    cli = shutil.which('cicy-r2')
    if cli:
        try:
            m = re.search(r'WRANGLER_CWD\s*=\s*["\']([^"\']+)["\']', open(cli).read())
            if m and os.path.isdir(os.path.join(m.group(1), 'node_modules', 'wrangler')):
                return m.group(1)
        except Exception:
            pass
    return fallback


WRANGLER_CWD = _discover_wrangler_cwd()

# wrangler r2 object put does NOT set Content-Type from the extension, so map it
# ourselves. Anything not listed falls back to application/octet-stream.
CONTENT_TYPES = {
    '.js': 'text/javascript', '.mjs': 'text/javascript', '.css': 'text/css',
    '.html': 'text/html', '.json': 'application/json', '.map': 'application/json',
    '.svg': 'image/svg+xml', '.png': 'image/png', '.jpg': 'image/jpeg',
    '.jpeg': 'image/jpeg', '.gif': 'image/gif', '.webp': 'image/webp',
    '.avif': 'image/avif', '.ico': 'image/x-icon', '.txt': 'text/plain',
    '.wasm': 'application/wasm', '.xml': 'application/xml',
    '.woff': 'font/woff', '.woff2': 'font/woff2', '.ttf': 'font/ttf',
    '.otf': 'font/otf', '.eot': 'application/vnd.ms-fontobject',
}


def content_type_for(path):
    return CONTENT_TYPES.get(os.path.splitext(path)[1].lower(), 'application/octet-stream')


versions = json.load(open(os.path.join(REPO_ROOT, 'versions.json')))

TARGETS = {
    'app':     {'src': os.path.join(REPO_ROOT, 'app', 'dist'), 'prefix': 'app',  'key': 'app',     'flat': False, 'base_dir': ''},
    'ttyd':    {'prefix': 'ttyd', 'key': 'ttyd', 'flat': False, 'base_dir': ''},
    'landing': {'src': os.path.expanduser('~/projects/cicy-landing/public/assets'), 'prefix': 'landing', 'key': 'landing', 'flat': True, 'base_dir': 'assets'},
}


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


def r2_put(key, local):
    """Upload one file to R2 at `key` via `wrangler r2 object put` with an
    explicit --content-type. Returns True on success. 3 retries with backoff
    (R2/CF rate-limits bursts)."""
    ct = content_type_for(local)
    last_err = ''
    for attempt in range(3):
        try:
            r = subprocess.run(
                ['npx', 'wrangler', 'r2', 'object', 'put', f'{BUCKET}/{key}',
                 '--file', local, '--content-type', ct],
                cwd=WRANGLER_CWD, env=WRANGLER_ENV,
                capture_output=True, text=True, timeout=300,
            )
            if r.returncode == 0:
                return True
            lines = (r.stderr or r.stdout or '').strip().splitlines()
            last_err = lines[-1] if lines else ''
        except Exception as e:
            last_err = str(e)
        if attempt < 2:
            time.sleep(2 * (attempt + 1))
    print(f"  ✗ {key} ({last_err})")
    return False


def upload(target):
    t = TARGETS[target]
    temp_dir = None
    if target == 'ttyd':
        temp_dir, src = prepare_ttyd_assets()
    else:
        src = t['src']
    prefix = t['prefix']
    ver = 'v' + str(versions.get(t['key'], '1'))
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
                    key = f"{prefix}/{ver}/assets/{f}"
                else:
                    rel = os.path.relpath(local, src)
                    base_dir = t.get('base_dir', '').strip('/')
                    key_rel = f"{base_dir}/{rel}" if base_dir else rel
                    key = f"{prefix}/{ver}/{key_rel}"
                if r2_put(key, local):
                    print(f"  ✓ {key} [{content_type_for(local)}]")
                    ok += 1
        print(f"  {ok} files → {PUBLIC_URL}/{prefix}/{ver}/\n")
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
