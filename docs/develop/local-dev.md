---
title: 本地开发
description: dev.py 本地起服务;前后端分开调试。
---
# 本地开发

```bash
cd ~/projects/cicy-code
python3 dev.py         # 构建 + 后台起 api/cicy-code --dev --public,tail 日志
```

默认端口:API `8008`、Vite `8022`。前端源码改动:`dev.py` 默认 `SKIP_NPM=1` 复用 `app/dist`,要生效先 `cd app && npm run build` 再 `python3 dev.py`。

前后端分开:

```bash
cd app && npm ci && npm run dev          # 前端 HMR(:8022)
cd api && go run ./mgr/ --dev --public   # 后端把非 API 请求反代到 :8022
```

`--dev` 仅用于本地起服务;**正式产物必须走 `./build.sh`**(见 [构建与测试](/develop/build-test))。
