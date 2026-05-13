# cf-workers

Cloudflare Workers management via API and wrangler.

## Credentials

Stored in `~/cicy-ai/global.json`:
- `CLOUDFLARE_API_TOKEN_CICYBOT`
- `CLOUDFLARE_ACCOUNT_ID_CICYBOT`

## List All Workers

```bash
python3 -c "
import json, urllib.request
d = json.load(open('/home/w3c_offical/global.json'))
token = d['CLOUDFLARE_API_TOKEN_CICYBOT']
account_id = d['CLOUDFLARE_ACCOUNT_ID_CICYBOT']
req = urllib.request.Request(
    f'https://api.cloudflare.com/client/v4/accounts/{account_id}/workers/scripts',
    headers={'Authorization': f'Bearer {token}'}
)
data = json.loads(urllib.request.urlopen(req).read())
for w in data['result']:
    print(w['id'])
"
```

## Deploy via Wrangler

```bash
# Deploy worker
wrangler deploy

# Deploy with specific name
wrangler deploy --name <worker-name>

# Dev mode (local)
wrangler dev

# Tail logs
wrangler tail <worker-name>
```

## Deploy via API

```bash
python3 -c "
import json, urllib.request
d = json.load(open('/home/w3c_offical/global.json'))
token = d['CLOUDFLARE_API_TOKEN_CICYBOT']
account_id = d['CLOUDFLARE_ACCOUNT_ID_CICYBOT']
worker_name = '<worker-name>'
script = open('worker.js', 'rb').read()

req = urllib.request.Request(
    f'https://api.cloudflare.com/client/v4/accounts/{account_id}/workers/scripts/{worker_name}',
    data=script,
    headers={'Authorization': f'Bearer {token}', 'Content-Type': 'application/javascript'},
    method='PUT'
)
print(urllib.request.urlopen(req).read().decode())
"
```

## Bind Custom Domain (Route)

```bash
python3 -c "
import json, urllib.request
d = json.load(open('/home/w3c_offical/global.json'))
token = d['CLOUDFLARE_API_TOKEN_CICYBOT']
account_id = d['CLOUDFLARE_ACCOUNT_ID_CICYBOT']
worker_name = '<worker-name>'

# Add custom domain
data = json.dumps({'name': 'your-domain.com'}).encode()
req = urllib.request.Request(
    f'https://api.cloudflare.com/client/v4/accounts/{account_id}/workers/scripts/{worker_name}/domains',
    data=data,
    headers={'Authorization': f'Bearer {token}', 'Content-Type': 'application/json'},
    method='POST'
)
print(urllib.request.urlopen(req).read().decode())
"
```

Or add route via wrangler.toml:
```toml
routes = [
  { pattern = "example.com/*", zone_name = "example.com" }
]
```

## Current Workers

| Worker |
|--------|
| ai-proxy-api |
| api |
| broken-wave-3b77 |
| cloudflare-chat-demo |
| fastapi-worker |
| hello-api |
| hello-world-worker |
| ip |
| odd-snowflake-2a37 |
| proxt_test_proxy |
| proxy |
| proxy_1 |
| soul-ai |
| test_proxy |
| textsoul-ai-worker-test |
| textsoul-openapi-mock-wrangler |
