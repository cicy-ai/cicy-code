# FRP Client Installer

Short installer entrypoints for connecting a client machine to the CiCy FRP server.

## Install commands

### macOS / Linux

First install requires both server address and token. Examples:

```bash
FRP_SERVER='1.2.3.4' FRP_TOKEN='your-token' curl -fsSL https://install.cicy-ai.com/frp | bash
```

Or pass them explicitly:

```bash
curl -fsSL https://install.cicy-ai.com/frp | bash -s -- --server 1.2.3.4 --token your-token --remote-port 9503
```

Interactive terminal (prompts for missing server / token):

```bash
curl -fsSL https://install.cicy-ai.com/frp | bash
```

Rerun after first install (no args needed — reuses existing config):

```bash
curl -fsSL https://install.cicy-ai.com/frp | bash
```

Default behavior:

- installs `frpc`
- writes `~/.config/frp/frpc.toml` on first run
- if `~/.config/frp/frpc.toml` already exists, prints the config path and reuses it without overwriting
- starts `frpc` immediately
  - macOS: LaunchAgent
  - Linux: systemd service when `systemctl` + `sudo` are available
  - fallback: `--service none` background process
- rerun the installer after editing the config to hot reload; if reload is unavailable, it falls back to restart
- exposes local `127.0.0.1:22` to remote port `9502`

Useful options:

- `--service auto|launchd|system|none`
- `--remote-port <PORT>`
- `--local-port <PORT>`
- `--name <NAME>`
- `--server <HOST>`
- `--token <TOKEN>`

### Windows

Run in PowerShell:

```powershell
$u='https://install.cicy-ai.com/frp';
$p=Join-Path $env:TEMP 'install-frpc-client.ps1';
irm $u -OutFile $p; powershell -ExecutionPolicy Bypass -File $p
```

The script prompts for the FRP token and self-elevates to install the Windows service.

## Defaults

- FRP server: required (`--server` or `FRP_SERVER`); no built-in default
- FRP server port: `9500`
- remote port: `9502`
- local port: `22`
- binary path: `~/.local/bin/frpc`
- config path: `~/.config/frp/frpc.toml`
- log path: `~/.local/frp/frpc.log`

## Manage an existing install

The installer prints the exact commands for the current machine after each run, including:

- `status`
- `reload`
- `restart`
- `start` (for `--service none`)

Typical workflow after editing the config:

1. Open the printed config path.
2. Update token / ports / proxy name.
3. Rerun the installer, or use the printed reload command.
4. If reload fails, use the printed restart command.

After install, access the client machine through the server with:

```bash
ssh -p <remote-port> <your-user>@<frp-server>
```
