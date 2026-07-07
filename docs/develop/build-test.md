---
title: 构建与测试
description: 唯一正确的构建/测试方式是 ./build.sh。
---
# 构建与测试

::: warning 强制
**必须走 `./build.sh`,不要直接 `go build` / `go test`。** `build.sh` 会在编译前准备内嵌资源(ui / .tmux.conf / ttyd 资产);裸 `go build ./mgr/` 会跳过这些、和真实流水线不等价,还会在仓库根丢一个 `./mgr` 产物。
:::

```bash
./build.sh build [os arch]    # 当前 / 指定平台 → api/cicy-code
./build.sh all                # 全平台
./build.sh test-go            # Go 测试(稳定入口)
./build.sh test-go ./mgr/...  # 单包
./build.sh docker <tag>       # runtime 镜像
./build.sh docker-base <tag>  # base 镜像
```

`build.sh` 依次:同步版本号 → 复制 `api/resources` + `.tmux.conf` 到 `api/mgr/` → 构建 `app/dist`(除非 `SKIP_NPM=1`)→ 刷新 ttyd 资产 → 复制 `app/dist` → `api/mgr/ui`(前端嵌进二进制)→ 编译 `api/mgr`。
