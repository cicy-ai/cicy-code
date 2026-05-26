#!/usr/bin/env bash
# audit-v2 end-to-end demo.
#
# Runs the whole audit-v2 stack on the local machine and walks through:
#
#   1. Start dev mihomo on 9002 (routes whitelisted hosts to MITM).
#   2. Start cicy-mitm on 1085 (TLS terminate + audit submit).
#   3. Fire a HTTPS request through the chain — verified via curl.
#   4. Optionally: route an opencode container's traffic through the
#      same chain (skipped if image not built / no OPENROUTER_API_KEY).
#   5. Run `cicy-code audit autonomy run` once (skipped if LLM env
#      not set).
#   6. List recent decisions.
#   7. (Optional) Revert the last applied decision.
#
# Idempotent: previous demo state under /tmp/audit-v2-demo/ is cleaned
# at startup. Backend audit state under ~/cicy-ai/ is NOT touched
# (that's the long-lived per-host data).
#
# Each step has a SKIP_<NAME>=1 escape hatch. Set DEMO_TEARDOWN=0 to
# leave the dev mihomo + MITM running for further manual experiments.

set -euo pipefail

DEMO_ROOT=${DEMO_ROOT:-/tmp/audit-v2-demo}
MIHOMO_BIN=${MIHOMO_BIN:-$HOME/.local/bin/mihomo}
SMOKE_BIN=${SMOKE_BIN:-/tmp/mitm-smoke-bin}
DEMO_TEARDOWN=${DEMO_TEARDOWN:-1}

REPO_DIR=$(cd "$(dirname "$0")/.." && pwd)

c_blue() { printf "\033[1;34m%s\033[0m\n" "$*"; }
c_grey() { printf "\033[2m%s\033[0m\n" "$*"; }
c_red()  { printf "\033[1;31m%s\033[0m\n" "$*"; }
c_yel()  { printf "\033[1;33m%s\033[0m\n" "$*"; }

step() { c_blue "[$1/$2] $3"; }

# Make sure mitm-smoke binary is built (Phase 1+1.5 driver).
ensure_smoke_bin() {
  if [ ! -x "$SMOKE_BIN" ]; then
    c_grey "  building mitm-smoke binary → $SMOKE_BIN"
    ( cd "$REPO_DIR/api" && go build -o "$SMOKE_BIN" ./mgr/mitm/cmd/mitm-smoke )
  fi
}

# Make sure cicy-code binary exists for the autonomy CLI step.
ensure_cicy_code() {
  if [ ! -x "$DEMO_ROOT/cicy-code" ]; then
    c_grey "  building cicy-code binary → $DEMO_ROOT/cicy-code"
    ( cd "$REPO_DIR/api" && go build -o "$DEMO_ROOT/cicy-code" ./mgr )
  fi
}

cleanup() {
  if [ "$DEMO_TEARDOWN" = "1" ]; then
    c_grey "tearing down…"
    pkill -f "$SMOKE_BIN" 2>/dev/null || true
    pkill -f "$MIHOMO_BIN -f $DEMO_ROOT/mihomo-dev.yaml" 2>/dev/null || true
  else
    c_yel "DEMO_TEARDOWN=0 — leaving mihomo + mitm running"
    c_yel "  ports: 9002 (mihomo), 1085 (mitm)"
    c_yel "  kill manually with: pkill -f $SMOKE_BIN && pkill -f mihomo-dev.yaml"
  fi
}
trap cleanup EXIT

mkdir -p "$DEMO_ROOT"
rm -f "$DEMO_ROOT/mihomo-dev.log" "$DEMO_ROOT/mitm.log" "$DEMO_ROOT/mitm-ca."*

# ────────────────────────────────────────────────────────────────────
step 1 7 "starting dev mihomo on 127.0.0.1:9002"
# ────────────────────────────────────────────────────────────────────
if [ -z "${SKIP_MIHOMO:-}" ]; then
  cat > "$DEMO_ROOT/mihomo-dev.yaml" <<EOF
mixed-port: 9002
allow-lan: false
bind: 127.0.0.1
mode: rule
log-level: info
external-controller: 127.0.0.1:19002
skip-auth-prefixes:
  - 127.0.0.1/32
  - ::1/128
proxies:
  - name: cicy_mitm
    type: socks5
    server: 127.0.0.1
    port: 1085
proxy-groups:
  - name: cicy-mitm-group
    type: select
    proxies: [cicy_mitm]
  - name: default
    type: select
    proxies: [DIRECT]
rules:
  - DOMAIN-SUFFIX,api.myip.com,cicy-mitm-group
  - DOMAIN-SUFFIX,api.anthropic.com,cicy-mitm-group
  - DOMAIN-SUFFIX,api.openai.com,cicy-mitm-group
  - DOMAIN-SUFFIX,openrouter.ai,cicy-mitm-group
  - MATCH,default
EOF
  "$MIHOMO_BIN" -f "$DEMO_ROOT/mihomo-dev.yaml" > "$DEMO_ROOT/mihomo-dev.log" 2>&1 &
  sleep 2
  ss -tlnp 2>/dev/null | grep ":9002" >/dev/null && c_grey "  mihomo 9002 OK" || { c_red "  mihomo did not bind 9002"; exit 1; }
fi

# ────────────────────────────────────────────────────────────────────
step 2 7 "starting cicy-mitm on 127.0.0.1:1085"
# ────────────────────────────────────────────────────────────────────
if [ -z "${SKIP_MITM:-}" ]; then
  ensure_smoke_bin
  cat > "$DEMO_ROOT/mitm.json" <<EOF
{
  "enabled": true,
  "socks5_listen": "127.0.0.1:1085",
  "ca": {
    "cert_path": "$DEMO_ROOT/mitm-ca.crt",
    "key_path":  "$DEMO_ROOT/mitm-ca.key",
    "leaf_cache_size": 64
  },
  "hosts": {
    "whitelist": ["api.myip.com", "api.anthropic.com", "api.openai.com", "openrouter.ai"]
  },
  "node": { "id": "demo", "final_hop": true },
  "upstream": { "mode": "direct", "dial_timeout": "10s", "tls_timeout": "10s" },
  "identity": {
    "rules": [
      { "kind": "socks5_username" },
      { "kind": "fallback", "value": "demo:{host}" }
    ]
  }
}
EOF
  "$SMOKE_BIN" --config "$DEMO_ROOT/mitm.json" --history-root "$DEMO_ROOT/audit-history" > "$DEMO_ROOT/mitm.log" 2>&1 &
  sleep 2
  ss -tlnp 2>/dev/null | grep ":1085" >/dev/null && c_grey "  mitm 1085 OK" || { c_red "  mitm did not bind 1085"; exit 1; }
fi

# ────────────────────────────────────────────────────────────────────
step 3 7 "curl through chain → api.myip.com"
# ────────────────────────────────────────────────────────────────────
RESP=$(curl --proxy socks5h://127.0.0.1:9002 --cacert "$DEMO_ROOT/mitm-ca.crt" --max-time 10 -s https://api.myip.com 2>&1) || RESP="(curl exited non-zero — check $DEMO_ROOT/mitm.log)"
echo "  upstream returned: $RESP"

# ────────────────────────────────────────────────────────────────────
step 4 7 "opencode container request (optional)"
# ────────────────────────────────────────────────────────────────────
if [ -z "${SKIP_OPENCODE:-}" ] && [ -n "${OPENROUTER_API_KEY:-}" ] && docker image inspect opencode-mitm >/dev/null 2>&1; then
  CA_B64=$(base64 -w0 "$DEMO_ROOT/mitm-ca.crt")
  docker run --rm --network host \
    -e CICY_MITM_CA_PEM_B64="$CA_B64" \
    -e ALL_PROXY=socks5h://127.0.0.1:9002 \
    -e OPENROUTER_API_KEY="$OPENROUTER_API_KEY" \
    opencode-mitm \
    opencode run --model openrouter:meta-llama/llama-3.1-8b-instruct:free \
      "What is 2+2? Reply with just the digit." 2>&1 | tail -5
else
  c_grey "  skipped: set OPENROUTER_API_KEY + docker build -t opencode-mitm skills/docker/opencode-mitm/"
fi

# ────────────────────────────────────────────────────────────────────
step 5 7 "list captured audit turns under $DEMO_ROOT/audit-history"
# ────────────────────────────────────────────────────────────────────
find "$DEMO_ROOT/audit-history" -name current.json 2>/dev/null | head -5
echo

# ────────────────────────────────────────────────────────────────────
step 6 7 "run one autonomy tick (CLI, optional)"
# ────────────────────────────────────────────────────────────────────
if [ -z "${SKIP_AUTONOMY:-}" ] && [ -n "${CICY_AI_GATEWAY_LLM_ENDPOINT:-}" ] && [ -n "${CICY_AI_GATEWAY_LLM_API_KEY:-}" ]; then
  ensure_cicy_code
  # Bootstrap autonomy.json from env so the CLI run is self-contained.
  mkdir -p "$HOME/cicy-ai/autonomy"
  cat > "$HOME/cicy-ai/autonomy/autonomy.json" <<EOF
{
  "enabled": true,
  "interval": "10m",
  "lookback": "1h",
  "max_changes_per_hour": 5,
  "max_changes_per_tick": 3,
  "forbidden_actions": ["enable_preventive_block"],
  "llm": {
    "endpoint": "${CICY_AI_GATEWAY_LLM_ENDPOINT}",
    "model":    "${CICY_AI_GATEWAY_LLM_MODEL:-deepseek-v4-pro}",
    "api_key":  "${CICY_AI_GATEWAY_LLM_API_KEY}"
  }
}
EOF
  "$DEMO_ROOT/cicy-code" audit autonomy run 2>&1 | head -40
else
  c_grey "  skipped: set CICY_AI_GATEWAY_LLM_ENDPOINT + _MODEL + _API_KEY"
fi

# ────────────────────────────────────────────────────────────────────
step 7 7 "show recent decisions"
# ────────────────────────────────────────────────────────────────────
if [ -x "$DEMO_ROOT/cicy-code" ]; then
  "$DEMO_ROOT/cicy-code" audit autonomy decisions --limit=5 2>&1 || c_grey "  (no decisions yet — autonomy step skipped)"
else
  c_grey "  cicy-code binary not built — skipping decisions list"
fi

c_blue "──────────────────────────────"
c_blue "demo complete"
c_blue "  MITM history: $DEMO_ROOT/audit-history/"
c_blue "  CA:           $DEMO_ROOT/mitm-ca.crt"
c_blue "  mihomo log:   $DEMO_ROOT/mihomo-dev.log"
c_blue "  mitm log:     $DEMO_ROOT/mitm.log"
c_blue "  decisions:    ~/cicy-ai/autonomy/decisions.ndjson"
