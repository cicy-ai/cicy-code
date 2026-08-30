---
title: 下载与安装
description: 下载 cicy-code —— 桌面版 cicy-desktop(macOS / Windows)+ 移动端开箱即用;服务器用 Docker 自托管;npm 供 headless / 进阶。
---
# 下载与安装

## 桌面版(推荐)

多数人用 **cicy-desktop** —— 打包好 cicy-code 的原生桌面外壳(内置浏览器沙箱 + 系统 Chrome 驱动),下载即用、免配置。

### macOS · 12 Monterey 或更新

- [`.pkg` · Apple Silicon](https://r2.deepfetch.de5.net/releases/cicy-desktop-mac-arm64-latest.pkg)
- [`.pkg` · Intel](https://r2.deepfetch.de5.net/releases/cicy-desktop-mac-x64-latest.pkg)

### Windows · 10 / 11 · x64

- [`.exe` Installer (64-bit)](https://r2.deepfetch.de5.net/releases/cicy-desktop-latest.exe)

### 移动端 · iOS / Android

- **Web App**(免安装,iPhone / iPad / Android):[m.cicy-ai.com](https://m.cicy-ai.com)
- iOS · [IPA(Sideloadly 侧载)](https://r2.deepfetch.de5.net/cicy-mobile/cicy-latest.ipa)
- Android · [APK](https://r2.deepfetch.de5.net/cicy-mobile/cicy-latest.apk)

## Docker(服务器 / 自托管)

在服务器上跑一个团队工作区。发布镜像 `cicybot/cicy-code`(Docker Hub),或自己 `./build.sh docker <tag>` 构建。

> 容器默认绑 `127.0.0.1`(含容器内)—— 对外暴露走 Cloudflare 隧道或 `--public`。镜像构建、版本化热更新、`CICY_RUNTIME_MODE=api-only` 等**运行细节见 [Docker / runtime](/deploy/docker)**。

## npm(headless / 进阶)

只要引擎、不要桌面外壳(接进自己的脚本 / CI / 服务器):

```bash
npx cicy-code                       # 临时跑一次
npm i -g cicy-code                  # 全局安装
npm i -g cicy-code --registry=https://registry.npmmirror.com   # 国内(缓存二进制,不走 GitHub)
```

单二进制,npm 按 `os`/`cpu` **只装匹配当前机器的子包**(~30 MB),浏览器开 `http://127.0.0.1:8008`。

| 平台 | 子包 |
| --- | --- |
| macOS · Apple Silicon | `cicy-code-darwin-arm64` |
| macOS · Intel | `cicy-code-darwin-x64` |
| Linux · x64 | `cicy-code-linux-x64` |
| Linux · arm64 | `cicy-code-linux-arm64` |

## 更新

- **桌面版**:应用内更新(cicy-desktop hot-patch)。
- **Docker / headless**:`cicy-code-update`(见 [Docker / runtime](/deploy/docker)),或 `npm i -g cicy-code@latest`。
