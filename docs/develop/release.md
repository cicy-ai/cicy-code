---
title: 发版
description: sync-version 同步版本号,tag 触发 CI 发 npm;本地桌面 localbin 快速更新。
---
# 发版

版本号统一由 `scripts/sync-version.py` 写入:`npm/package.json`、`app/package.json`、`app/package-lock.json`、`app/src/config.ts`、`api/mgr/main.go`、`.cicy_tmux.conf`。

## 正式发版(走 CI)

```bash
python3 scripts/sync-version.py --set 2.3.NN   # 1) bump 版本文件
cd app && npm run build                        # 2) 前端 build(烘 config.version)
cd .. && git add <你的文件> && git commit ...   # 3) 提交(共享 checkout:只 add 自己的)
git push origin main
git tag v2.3.NN && git push origin v2.3.NN      # 4) tag → release.yml
```

CI 从 **tag** 构建各平台二进制、发布 npm 五连包(launcher + 4 平台子包)。文档站也在同一 tag 上由 `docs-deploy.yml` 自动更新到 Cloudflare Pages。

::: tip 前端务必先 build
前端改动要在 bump **之后** `npm run build`,否则 `SKIP_NPM=1` 复用旧 dist,版本号显示还是旧的。
:::

## 本地 Mac 桌面(不发 npm,更快)

桌面跑 `~/.local/bin/cicy-code`(symlink → 版本化二进制)。每次必 bump 版本 → `./build.sh build darwin amd64` → 拷成版本化名 → 原子换 symlink → 同步 `~/.local/bin/.cicy-localbin.json`。
