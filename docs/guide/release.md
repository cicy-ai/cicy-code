---
title: 发版
description: sync-version 同步版本号,tag 触发 CI 发布 npm 五连包。
---
# 发版

版本号统一由 `scripts/sync-version.py` 写入;tag 触发 CI 发 npm。

## 正式发版(走 CI)

```bash
python3 scripts/sync-version.py --set 2.3.NN   # 1) bump 版本文件
cd app && npm run build                        # 2) 前端 build(烘 config.version)
git commit … && git push                       # 3) 提交
git tag v2.3.NN && git push origin v2.3.NN      # 4) tag → release.yml
```

CI 从 **tag** 构建各平台二进制、发布 npm 五连包(launcher + 4 平台子包)。

::: tip 前端务必先 build
前端改动要在 bump **之后** `npm run build`,否则 `SKIP_NPM=1` 会复用旧 dist,版本号显示还是旧的。
:::
