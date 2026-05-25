#!/bin/bash
# Container entrypoint for opencode-mitm.
#
# Responsibilities:
#   1. Refresh the OS CA trust store so the cicy-mitm CA is trusted.
#   2. Verify the mihomo proxy is reachable (host loopback via --network host).
#   3. Hand off to opencode (or any argv override).
#
# Required mounts / env at container start:
#   /usr/local/share/ca-certificates/cicy-mitm.crt   the MITM CA (bind-mount RO)
#   ALL_PROXY=socks5h://127.0.0.1:9002               or HTTPS_PROXY=http://127.0.0.1:9002
#   OPENROUTER_API_KEY=...                           (or any opencode-supported key)
#
# Optional:
#   OPENCODE_HEALTHCHECK=1   probes mihomo + an HTTPS host before launch.

set -e

# 1. Install the cicy-mitm CA into the system trust store. Two delivery
#    paths supported, in priority order:
#
#    a) CICY_MITM_CA_PEM_B64 env var (base64-encoded cert content).
#       Use this when bind-mounts can't cross the docker namespace
#       boundary (rootless docker, remote daemon, etc.). On the host:
#       export CICY_MITM_CA_PEM_B64="$(base64 -w0 /tmp/mitm-prod-ca.crt)"
#
#    b) Bind-mounted file at /usr/local/share/ca-certificates/cicy-mitm.crt.
#       Works for native single-host docker.
CA_TARGET=/usr/local/share/ca-certificates/cicy-mitm.crt
if [ -n "$CICY_MITM_CA_PEM_B64" ]; then
    echo "$CICY_MITM_CA_PEM_B64" | base64 -d > "$CA_TARGET" 2>/dev/null \
        || { echo "[opencode-mitm] FATAL: CICY_MITM_CA_PEM_B64 is not valid base64"; exit 2; }
    chmod 0644 "$CA_TARGET"
fi
if [ -s "$CA_TARGET" ] && head -1 "$CA_TARGET" | grep -q "BEGIN CERTIFICATE"; then
    update-ca-certificates --fresh >/dev/null 2>&1 || \
        echo "[opencode-mitm] WARN: update-ca-certificates failed; cicy-mitm CA may not be trusted"
else
    echo "[opencode-mitm] WARN: no cicy-mitm CA available — HTTPS to whitelisted hosts will fail"
    echo "[opencode-mitm]       Option A (recommended): -e CICY_MITM_CA_PEM_B64=\"\$(base64 -w0 /tmp/mitm-prod-ca.crt)\""
    echo "[opencode-mitm]       Option B (native docker only): -v /tmp/mitm-prod-ca.crt:$CA_TARGET:ro"
fi

# Make sure Go / Node respect /etc/ssl/certs (which update-ca-certificates
# manages). Some opencode subcommands shell out to other binaries that
# read NODE_EXTRA_CA_CERTS / SSL_CERT_FILE directly.
export SSL_CERT_FILE=${SSL_CERT_FILE:-/etc/ssl/certs/ca-certificates.crt}
export NODE_EXTRA_CA_CERTS=${NODE_EXTRA_CA_CERTS:-/usr/local/share/ca-certificates/cicy-mitm.crt}

# 2. Proxy reachability check.
if [ -n "$OPENCODE_HEALTHCHECK" ]; then
    PROXY_ARG=""
    if [ -n "$ALL_PROXY" ]; then PROXY_ARG="--proxy $ALL_PROXY"; fi
    if [ -n "$HTTPS_PROXY" ]; then PROXY_ARG="--proxy $HTTPS_PROXY"; fi

    echo "[opencode-mitm] preflight: GET https://api.myip.com via $PROXY_ARG"
    if ! curl --max-time 8 -fsS $PROXY_ARG https://api.myip.com >/dev/null 2>&1; then
        echo "[opencode-mitm] FATAL: proxy unreachable. Make sure"
        echo "                  - mihomo on host port 9002 is running"
        echo "                  - cicy-mitm on host port 1085 is running"
        echo "                  - container uses --network host (or host.docker.internal mapped)"
        exit 1
    fi
    echo "[opencode-mitm] preflight OK"
fi

# 3. Default to launching opencode if no argv override. Useful invocations:
#     docker run ... opencode-mitm                       (interactive REPL)
#     docker run ... opencode-mitm opencode run "task"   (one-shot)
#     docker run ... opencode-mitm bash                  (debug shell)
exec "$@"
