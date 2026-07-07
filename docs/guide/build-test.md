---
title: 构建与测试
description: 唯一正确的构建 / 测试方式是 ./build.sh,不要直接 go build / go test。
---
# 构建与测试

唯一正确的构建 / 测试方式是 `./build.sh`。

::: warning 重要
**不要直接 `go build` / `go test`。** `build.sh` 会在编译前准备内嵌资源(ui / .tmux.conf / ttyd 资产);裸 `go build ./mgr/` 会跳过这些、和真实流水线不等价,还会在仓库根丢一个 `./mgr` 产物。
:::

## 命令

```bash
./build.sh build [os arch]    # 当前 / 指定平台
./build.sh all                # 全平台
./build.sh test-go            # Go 测试(稳定入口)
./build.sh test-go ./mgr/...  # 单包
./build.sh docker <tag>       # runtime 镜像
```

## build.sh 做了什么

- 同步版本号 → 复制 `api/resources`、`.tmux.conf` 到 `api/mgr/`
- 构建 `app/dist`(除非 `SKIP_NPM=1`)+ 刷新 ttyd 资产
- 把 `app/dist` 复制进 `api/mgr/ui`(前端嵌进二进制)→ 编译 `api/mgr`
