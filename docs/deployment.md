# Deployment Guide

## Prerequisites

- Linux VPS (Ubuntu 22.04+ recommended)
- Docker & Docker Compose
- Git

## Quick Deploy

```bash
git clone https://github.com/cicy-dev/cicy-code.git
cd cicy-code
./setup.sh            # auto-generates .env (HOST_HOME, HOST_UID, HOST_GID)
docker compose up -d  # start all services
```

Done. Access via browser:
- IDE: `http://<your-ip>:6902`
- API: `http://<your-ip>:14446`
- code-server: `http://<your-ip>:14446/code`

## What setup.sh Does

Auto-detects and writes to `.env`:
```
HOST_HOME=/home/youruser
HOST_UID=1000
HOST_GID=1000
```

No manual configuration needed.

## Services

| Service | Port | Description |
|---------|------|-------------|
| ide-dev | 6902 | React frontend (Vite dev server) |
| api | 14446 | Go backend (API + WebSocket proxy) |
| code-server | 18080 | VS Code in browser (proxied via API) |
| mysql | 13306 | MySQL database |
| redis | 16379 | Redis cache |
| phpmyadmin | 18081 | DB admin UI |

## Expose via Cloudflare Tunnel (Optional)

For accessing from China or behind firewalls:

```bash
# Install cloudflared
curl -sL https://pkg.cloudflare.com/cloudflared-linux-amd64.deb -o /tmp/cf.deb
sudo dpkg -i /tmp/cf.deb

# Login & create tunnel
cloudflared tunnel login
cloudflared tunnel create cicy-code

# Route your domain
cloudflared tunnel route dns cicy-code your-domain.com
```

Configure `~/.cloudflared/config.yml`:
```yaml
tunnel: <tunnel-id>
ingress:
  - hostname: ide.your-domain.com
    service: http://localhost:6902
  - hostname: g-14446.your-domain.com
    service: http://localhost:14446
  - service: http_status:404
```

```bash
cloudflared tunnel run cicy-code
```

## Directory Structure

```
$HOST_HOME/
├── projects/     # mounted into code-server
├── workers/      # agent workspaces
├── skills/       # CLI tools & scripts
├── .kiro/        # kiro-cli config
└── .cicy-code-server/
    ├── data/     # code-server extensions & themes
    └── config/   # code-server config
```

## Troubleshooting

**code-server "Workspace does not exist"**
- Check that `HOST_HOME` in `.env` matches your actual home directory
- Run `docker exec cicy-code-server ls $HOST_HOME/workers/` to verify mounts

**Permission denied on files**
- Verify `HOST_UID`/`HOST_GID` in `.env` match your user: `id -u && id -g`
- Re-run `./setup.sh` and `docker compose up -d code-server`
