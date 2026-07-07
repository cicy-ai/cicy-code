---
title: 单机 / 本地
description: 单机跑 cicy-code,绑定 Cloudflare 隧道对外。
---
# 单机 / 本地

最简单:一台机器 `npx cicy-code`,浏览器开 `127.0.0.1:8008`。

## 对外暴露

默认只绑 `127.0.0.1`(含容器内)。要对外:

- `--public` 绑 `0.0.0.0`(配强 api_token);
- `--cft` 走 Cloudflare **quick tunnel**(随机 `*.trycloudflare.com`);
- `--cft-token <TOKEN>` 走 **named tunnel**(域名稳定),`--cft-host <FQDN>` 报告域名。也可读 `CICY_CFT_TOKEN` / `~/cicy-ai/db/cft.json`。
