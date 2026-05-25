# opencode-mitm

opencode CLI inside a container that routes through the host's `cicy-mitm` + `mihomo` audit stack. Used to generate **real, non-cooperative AI client traffic** for the autonomy loop without burning Anthropic credits.

## Why opencode

opencode (https://opencode.ai) supports many providers natively — OpenRouter (free models), Groq, OpenAI, local Ollama, etc. Unlike `claude-code` it does not require Anthropic. Set `OPENROUTER_API_KEY=...` and pick a free model like `meta-llama/llama-3.1-8b-instruct:free`.

opencode does NOT read `ANTHROPIC_BASE_URL` — it goes straight to provider URLs (`api.openai.com`, `openrouter.ai/api/v1`, etc.). That's exactly the kind of "non-cooperative" client cicy-mitm exists to capture.

## Quick start

### 1. On the host: start dev mihomo + cicy-mitm

(See [../../api/mgr/mitm/README.md](../../../api/mgr/mitm/README.md) for the deployment recipe — dev mihomo on `127.0.0.1:9002`, MITM on `127.0.0.1:1085`.)

Confirm the MITM CA is at `/tmp/mitm-prod-ca.crt` (or wherever you set `cert_path` in `mitm.json`).

### 2. Whitelist the upstream you'll hit

`api.openai.com` is in the default whitelist. For OpenRouter (free models) add `openrouter.ai`:

```bash
# In your ~/cicy-ai/mitm/config.json (or /tmp/mitm-prod.json):
"hosts": {
  "whitelist": [
    "api.anthropic.com",
    "api.openai.com",
    "openrouter.ai"
  ]
}
```

Restart MITM so it picks up the new whitelist.

### 3. Build the image

```bash
cd /home/cicy/projects/cicy-code-audit
docker build -t opencode-mitm skills/docker/opencode-mitm/
```

First build takes ~2 minutes (apt + Node 20 + opencode installer).

### 4. Run opencode through MITM

Interactive REPL:

```bash
docker run --rm -it \
  --network host \
  -v /tmp/mitm-prod-ca.crt:/usr/local/share/ca-certificates/cicy-mitm.crt:ro \
  -v $HOME/.config/opencode:/root/.config/opencode \
  -e OPENROUTER_API_KEY=$OPENROUTER_API_KEY \
  -e ALL_PROXY=socks5h://127.0.0.1:9002 \
  -e OPENCODE_HEALTHCHECK=1 \
  opencode-mitm
```

One-shot prompt:

```bash
docker run --rm \
  --network host \
  -v /tmp/mitm-prod-ca.crt:/usr/local/share/ca-certificates/cicy-mitm.crt:ro \
  -v $HOME/.config/opencode:/root/.config/opencode \
  -e OPENROUTER_API_KEY=$OPENROUTER_API_KEY \
  -e ALL_PROXY=socks5h://127.0.0.1:9002 \
  opencode-mitm \
  opencode run --model openrouter:meta-llama/llama-3.1-8b-instruct:free "hi"
```

### 5. Verify capture

After opencode answers, two new files appear under
`~/cicy-ai/workers/mitm:openrouter.ai/.cicy/history/mitm/turn-<ts>/`:
- `current.json` — opencode's plaintext request (model + messages)
- `reply.json` — provider's full response, plaintext

And `audit.SubmitMitmEvent` ingested an `Envelope{SourceChannel: "mitm"}` into the pipeline, so the autonomy agent will see it on its next tick.

## Env reference

| Variable | Purpose |
|----------|---------|
| `ALL_PROXY` | SOCKS5 proxy URL. Required. `socks5h://` (with DNS) is mandatory so the container resolves the upstream host name on the MITM side. |
| `HTTPS_PROXY` | Alternative — HTTP CONNECT through mihomo. `http://127.0.0.1:9002`. |
| `OPENROUTER_API_KEY` | Free model gateway. Sign up at openrouter.ai/keys. |
| `OPENAI_API_KEY` | Pass through if you want to test with OpenAI directly. |
| `OPENCODE_HEALTHCHECK` | Set to `1` to fail fast at startup if proxy is unreachable. |
| `SSL_CERT_FILE` | Already set by entrypoint to `/etc/ssl/certs/ca-certificates.crt`. |
| `NODE_EXTRA_CA_CERTS` | Already set by entrypoint to the bind-mounted MITM CA. |

## Troubleshooting

### `curl: SSL certificate problem: unable to get local issuer certificate`

The bind-mount is missing or `update-ca-certificates` failed. Check the
entrypoint log for the WARN line. Confirm `/usr/local/share/ca-certificates/cicy-mitm.crt` exists inside the container.

### `connect ECONNREFUSED 127.0.0.1:9002`

`--network host` was forgotten. On Linux this is the simplest way to let
the container reach host loopback. On macOS / Windows Docker Desktop, swap
to `--add-host=host.docker.internal:host-gateway` and use
`socks5h://host.docker.internal:9002` instead.

### opencode says "authentication_error"

The proxy chain works (request reached the upstream provider) but your
API key is wrong / expired. Double-check `OPENROUTER_API_KEY` is set
inside the container.

### audit doesn't see events

mihomo's routing rules don't include the upstream host. Check `~/cicy-ai/db/mihomo.yaml`:

```yaml
rules:
  - DOMAIN-SUFFIX,openrouter.ai,cicy-mitm-group
```

Or the host isn't in the MITM `hosts.whitelist`. The MITM logs "passthrough" for
non-whitelist hosts — those don't generate events.

## Limits

- Only HTTP/1.1. opencode → OpenRouter usually negotiates h1 via ALPN, but some providers default to h2. cicy-mitm forces h1 — clients that refuse h1 fallback won't work behind it.
- `--network host` doesn't isolate the container. For real production isolation, configure a Docker network bridge and route a specific veth through mihomo via iptables. Out of scope for this skill.
