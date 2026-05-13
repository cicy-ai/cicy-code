# cf-pages

Cloudflare Pages management via API and wrangler.

## Credentials

Stored in `~/cicy-ai/global.json`:
- `CLOUDFLARE_API_TOKEN_CICYBOT`
- `CLOUDFLARE_ACCOUNT_ID_CICYBOT`
wo
## List All Projects

```bash
python3 -c "
import json, urllib.request
d = json.load(open('/home/w3c_offical/global.json'))
token = d['CLOUDFLARE_API_TOKEN_CICYBOT']
account_id = d['CLOUDFLARE_ACCOUNT_ID_CICYBOT']
req = urllib.request.Request(
    f'https://api.cloudflare.com/client/v4/accounts/{account_id}/pages/projects',
    headers={'Authorization': f'Bearer {token}'}
)
data = json.loads(urllib.request.urlopen(req).read())
for p in data['result']:
    print(p['name'], '-', p.get('subdomain',''))
"
```

## Deploy via Wrangler

```bash
# Deploy build output to Pages
wrangler pages deploy ./dist --project-name <project-name>

# Specify branch
wrangler pages deploy ./dist --project-name <project-name> --branch main

# List deployments
wrangler pages deployment list --project-name <project-name>
```

## Deploy via API (direct upload)

```bash
# Upload files via API
curl -X POST "https://api.cloudflare.com/client/v4/accounts/$ACCOUNT_ID/pages/projects/$PROJECT/deployments" \
  -H "Authorization: Bearer $TOKEN" \
  -F "manifest=@manifest.json"
```

## Bind Custom Domain

Domain must be hosted in the same Cloudflare account.

```bash
python3 -c "
import json, urllib.request
d = json.load(open('/home/w3c_offical/global.json'))
token = d['CLOUDFLARE_API_TOKEN_CICYBOT']
account_id = d['CLOUDFLARE_ACCOUNT_ID_CICYBOT']
project = '<project-name>'
domain = 'your-domain.com'

data = json.dumps({'name': domain}).encode()
req = urllib.request.Request(
    f'https://api.cloudflare.com/client/v4/accounts/{account_id}/pages/projects/{project}/domains',
    data=data,
    headers={'Authorization': f'Bearer {token}', 'Content-Type': 'application/json'},
    method='POST'
)
print(urllib.request.urlopen(req).read().decode())
"
```

If domain is external (not on CF), add CNAME manually:
```
CNAME  your-subdomain  →  <project>.pages.dev
```

## Current Projects

| Project | Domain |
|---------|--------|
| cicy-desktop | desktop-ai-7h2.pages.dev |
| electron | electron-249.pages.dev |
| electron-render | electron-render.pages.dev |
| electroncicy | electroncicy.pages.dev |
