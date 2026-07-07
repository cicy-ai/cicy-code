---
title: 下载
description: 下载 / 安装 cicy-code —— npm(npx / 全局安装),按平台自动装匹配二进制。
---
# 下载

cicy-code 通过 **npm** 分发单二进制,macOS / Linux 开箱即用。

## npm(推荐)

```bash
# 临时跑一次
npx cicy-code

# 全局安装
npm i -g cicy-code

# 国内(npmmirror 缓存二进制,不走 GitHub)
npm i -g cicy-code --registry=https://registry.npmmirror.com
```

装好后浏览器打开 `http://127.0.0.1:8008`。

## 支持平台

npm 按 `os` / `cpu` **只装匹配当前机器的那个子包**(~30 MB),其余跳过。

| 平台 | 子包 |
| --- | --- |
| macOS · Apple Silicon | `cicy-code-darwin-arm64` |
| macOS · Intel | `cicy-code-darwin-x64` |
| Linux · x64 | `cicy-code-linux-x64` |
| Linux · arm64 | `cicy-code-linux-arm64` |

## 更新

```bash
npm i -g cicy-code@latest
```

::: tip 桌面版
需要原生桌面外壳(浏览器沙箱 / 系统 Chrome 驱动)见 [CiCy AI](https://cicy-ai.com)。
:::
