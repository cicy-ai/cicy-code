---
title: Docker / runtime
description: 构建 runtime 镜像、base 镜像,managed runtime。
---
# Docker / runtime

```bash
./build.sh docker <tag>        # runtime 镜像
./build.sh docker-base <tag>   # base 镜像(预装软件 + 预拉镜像)
```

Docker 只是稳定环境;应用是嵌了前端的 ~23M Go 二进制,所以生产往往只 ship 二进制:`docker cp` 进容器 → 重启 → 健康检查。`CICY_RUNTIME_MODE=api-only` 关掉 tmux/desktop-only 接口。
