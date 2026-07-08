---
title: Docker / runtime
description: 构建 runtime / base 镜像;容器里的 cicy-code 版本化热更新机制(cicy-code-update、~/.local/cicy-code 版本库、只重启不重建)。
---
# Docker / runtime

## 构建镜像

```bash
./build.sh docker <tag>        # runtime 镜像
./build.sh docker-base <tag>   # base 镜像(预装软件 + 预拉镜像)
```

镜像只是**稳定环境**;应用是嵌了前端的 ~30M Go 二进制。生产往往只 ship 二进制:`docker cp` 进容器 → 重启 → 健康检查。`CICY_RUNTIME_MODE=api-only` 关掉 tmux / 桌面专属接口。

## 版本化热更新

镜像**不烤死 cicy-code 版本** —— 它跟着 npm 浮动。容器里由 `cicy-code-update.sh` 管理:

```bash
cicy-code-update            # → latest
cicy-code-update 2.3.194    # → 指定版本
```

它做四件事,**不动容器、不动隧道、不动用户 daemon**:

1. `npm view` 解析确切版本号;
2. **并排安装**到 `~/.local/cicy-code/<ver>`(`CICY_CODE_STORE` 可覆盖,已装则跳过 → 幂等);
3. 翻转 PATH 软链 `~/.local/bin/cicy-code → <ver>/bin/cicy-code`;
4. 把 `current` 写进 `~/cicy-ai/runtime/versions.json`,然后 `supervisorctl restart cicy-code` —— **只重启这一个 program**。

首启时机由 `CICY_CODE_VERSION` 决定(默认 `latest`);已 link 好且未 pin 则直接复用(快/离线启动)。旧版并排保留 → **秒回滚**(改软链指回旧 `<ver>`)。

::: tip 老镜像自愈
cicy-code 二进制比镜像更新得勤。新二进制在**老镜像**里启动时,会自动把版本库收敛到 `~/.local/cicy-code`、并就地改写老的 `cicy-code-update.sh` —— 所以不管镜像多老,只要 cicy-code 是新的,安装位置都一致。
:::

## UI 一键更新

工作区检测到 npm 有更高版本时,版本处亮**红点**并给「更新」按钮 → 走 `POST /api/cicy-update`(后台跑 `cicy-code-update` → 重启 → 前端自动刷新)。检测端点 `GET /api/cicy-update` 有 30 分钟缓存。

## 相关

- 路径/端口 → [配置与路径](/reference/config)
- 环境变量 → [环境变量](/reference/env)
- 云端多租户 → [云端 (cicy-cloud)](/deploy/cloud)
