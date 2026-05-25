# opencode-mitm — opencode CLI inside a container whose HTTPS traffic is
# routed through the host's cicy-mitm + mihomo stack.
#
# Use opencode (sst/opencode, OpenAI-compatible CLI with many free
# providers via OpenRouter) as the non-cooperative client that generates
# real audit events for the autonomy loop. Avoids burning Claude credits
# during development.
#
# Build:
#   docker build -t opencode-mitm skills/docker/opencode-mitm/
#
# Run (see entrypoint.sh for full env reference):
#   docker run --rm -it \
#     --network host \
#     -v /tmp/mitm-prod-ca.crt:/usr/local/share/ca-certificates/cicy-mitm.crt:ro \
#     -e OPENROUTER_API_KEY=$OPENROUTER_API_KEY \
#     -e ALL_PROXY=socks5h://127.0.0.1:9002 \
#     opencode-mitm

FROM debian:bookworm-slim

# Base tooling. nodejs 20 is required by opencode (sst/opencode).
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash ca-certificates curl wget git \
        unzip jq \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install opencode CLI. The official installer drops the binary into
# /root/.opencode/bin and appends to .bashrc; we re-export PATH so the
# rest of the image (and future ENTRYPOINT) can see it.
RUN curl -fsSL https://opencode.ai/install | bash
ENV PATH=/root/.opencode/bin:/root/.local/share/opencode/bin:$PATH
RUN ls /root/.opencode/bin || ls /root/.local/share/opencode/bin || true \
    && opencode --version 2>&1 || echo "[warn] opencode binary path differs; entrypoint will probe"

# Default working dir for opencode projects.
WORKDIR /workspace

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["opencode"]
