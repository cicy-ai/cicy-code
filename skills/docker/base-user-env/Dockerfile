FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends bash nodejs npm ca-certificates \
    && rm -rf /var/lib/apt/lists/*

ENV HOME=/root
ENV PATH=/root/.local/bin:/root/cicy-ai/skills/bin:$PATH

RUN mkdir -p \
    /root/Private \
    /root/.local/bin \
    /root/cicy-ai/skills/bin \
    /root/cicy-ai/skills/generated \
    /root/.cache

CMD ["/bin/bash"]
