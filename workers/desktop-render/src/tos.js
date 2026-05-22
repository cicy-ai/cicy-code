// Terms of Service — shown as a blocking overlay on first launch and any
// time the version bumps. Replace TOS_TEXT with the canonical legal text
// once it's lawyer-reviewed; bump TOS_VERSION to re-prompt all users.

export const TOS_VERSION = "2026-05-18";

export const TOS_TEXT = `# CiCy Desktop 使用条款 / Terms of Service

**版本 / Version:** ${"2026-05-18"}

---

## 1. 软件能力 / What this software does

CiCy Desktop 是一个本地客户端,运行在你自己的电脑上。它会:

- 启动并管理一个本地 cicy-code 引擎(npx / Docker)
- 允许你连接的 cicy-code 后端(本地或云端)向客户端发送指令
- 通过 Electron 内嵌的 Chrome 实例执行网页自动化
- 在你明确授权后访问剪贴板、文件、屏幕信息等系统能力

CiCy Desktop is a local client that runs on your own device. It runs a
local cicy-code engine, accepts commands from cicy-code backends you
have configured, and — with your explicit consent — interacts with your
operating system (clipboard, files, screen) and a bundled Chrome session.

## 2. 你的责任 / Your responsibilities

使用本软件,即表示你确认:

- 你**对本设备拥有完全控制权或合法授权**
- 你**仅在自己拥有 / 合法授权的设备上**安装和使用本软件
- 你**理解并同意** cicy-code 后端在你授权范围内可以读取本设备的剪贴板、屏幕、文件和浏览器状态
- 你**不会将本软件用于**:监控他人未经同意的设备、入侵他人系统、批量分发到未经授权的终端

By using this software you confirm you control or are authorised to
control this device, that you will only install it on devices you own or
are licensed to use, and that you will not use it to access devices you
are not authorised to use.

## 3. 数据收集 / Data collection

CiCy Desktop 本身不向开发者上传任何使用数据。所有 agent 指令、剪贴板内
容、屏幕截图都只在你和你配置的后端之间传输。云端 backend 的数据收集
策略由该后端 (cicy-ai.com 或第三方) 单独披露。

CiCy Desktop itself does not phone home. Your interactions are between
the client and the cicy-code backends you choose to connect to.

## 4. 取消授权 / Revocation

你可以随时关闭本软件、在 Homepage 上断开任意 backend、或在系统里卸载
CiCy Desktop。一旦断开 / 退出,该后端不再具备控制能力。

You can quit the app, disconnect a backend, or uninstall at any time.
Disconnection takes effect immediately.

## 5. 免责 / Disclaimer

本软件按"现状"提供,不作任何明示或默示担保。在适用法律允许的最大范围
内,开发者不对因使用本软件而产生的任何直接或间接损失负责。

This software is provided "AS IS" without warranty of any kind.

---

点击下方 **"我已阅读并接受"** 即表示你同意以上条款。如果你不接受,请关闭
本软件并卸载。

Click "I have read and accept" to agree. If you do not accept, close
and uninstall the software.
`;
