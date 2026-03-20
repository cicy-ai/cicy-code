# 本地部署指南

## 快速开始

```bash
npx cicy-code
```

首次运行会自动：
1. 下载对应平台的二进制
2. 检测并安装 tmux、code-server（如缺失）
3. 进入交互式 AI 工具选择（选择要安装的 Agent）
4. 为选中的 Agent 创建独立 Worker（固定端口 10001-10006）
5. 启动 code-server（端口 18080）
6. 启动 API 服务（端口 18008）
7. 创建数据目录 `~/.cicy/`

后续启动时，会自动从数据库恢复已有 Worker 并拉起各自的终端服务，不再重复创建。

访问：`http://localhost:18008/?token=YOUR_TOKEN`

## 获取 Token

首次启动会自动生成 token 并存储在 `~/global.json` 中：

```bash
cat ~/global.json | grep token
```

## 命令行参数

```bash
cicy-code [options]
```

| 参数 | 说明 |
|------|------|
| `--desktop` | 桌面模式：启动 API 后自动打开 Electron 桌面客户端 |
| `--dev` | 开发模式：从文件系统加载资源，使用 COS CDN |
| `--saas` | SaaS 模式（或 `SAAS_MODE=1`） |
| `--public` | 监听 0.0.0.0（默认仅 127.0.0.1） |
| `--audit` | 启用 mitmproxy 审计模式 |
| `--cn` | 使用国内镜像（npm + GitHub 代理） |
| `--version` | 显示版本号 |

## 发布模式 vs 开发模式

### 发布模式（默认）

```bash
npx cicy-code
# 或
./cicy-code
```

- ttyd inject HTML（面板、语音等 UI）**编译进 binary**，无外部文件依赖
- CSS/JS 由内嵌 ttyd-go 直接 serve，**不依赖 COS CDN**
- 单 binary 即可运行，无需网络访问静态资源

### 开发模式

```bash
./cicy-code --dev
```

- ttyd inject HTML 从 `mgr/resources/` 文件系统实时读取，修改后自动热重载
- CSS/JS 走 COS CDN（`https://cicy-*.cos.ap-shanghai.myqcloud.com/ttyd/`）
- 方便前端开发迭代，改完 HTML 刷新浏览器即可看到效果

## 使用 Supervisor 管理进程

推荐使用 Supervisor 管理 cicy-code 进程，实现开机自启和自动重启。

配置文件位于项目 `scripts/` 目录，通过 **符号链接** 部署到 Supervisor 配置目录：

```
scripts/
├── cicy-code.supervisor.conf       # 发布模式
├── cicy-code-dev.supervisor.conf   # 开发模式
└── code-server.supervisor.conf     # code-server（独立管理时可选）
```

### 安装 Supervisor

```bash
# Ubuntu/Debian
sudo apt install supervisor

# macOS
brew install supervisor
```

### 部署配置（符号链接）

```bash
# 创建日志目录
sudo mkdir -p /var/log/cicy-code

# 发布模式 — 链接发布配置
sudo ln -sf $(pwd)/scripts/cicy-code.supervisor.conf /etc/supervisor/conf.d/cicy-code.conf

# 或 开发模式 — 链接开发配置
sudo ln -sf $(pwd)/scripts/cicy-code-dev.supervisor.conf /etc/supervisor/conf.d/cicy-code.conf

# 加载并启动
sudo supervisorctl reread && sudo supervisorctl update
sudo supervisorctl start cicy-code
```

> **原则**：配置文件始终在项目 `scripts/` 目录维护和版本管理，Supervisor 目录只存符号链接。
> 修改配置后只需 `supervisorctl reread && update`，无需复制文件。

### 切换发布/开发模式

```bash
# 切换到开发模式
sudo ln -sf $(pwd)/scripts/cicy-code-dev.supervisor.conf /etc/supervisor/conf.d/cicy-code.conf
sudo supervisorctl reread && sudo supervisorctl update && sudo supervisorctl restart cicy-code

# 切换回发布模式
sudo ln -sf $(pwd)/scripts/cicy-code.supervisor.conf /etc/supervisor/conf.d/cicy-code.conf
sudo supervisorctl reread && sudo supervisorctl update && sudo supervisorctl restart cicy-code
```

> **注意**：开发模式配置中 `directory` 指向 `api/` 目录，这样 `--dev` 模式才能从 `mgr/resources/` 读取本地 inject HTML 文件。

### macOS launchd（替代 Supervisor）

```xml
<!-- ~/Library/LaunchAgents/com.cicy.code.plist -->
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.cicy.code</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/cicy-code</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/cicy-code.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/cicy-code.log</string>
</dict>
</plist>
```

```bash
# 加载
launchctl load ~/Library/LaunchAgents/com.cicy.code.plist

# 卸载
launchctl unload ~/Library/LaunchAgents/com.cicy.code.plist

# 开发模式：修改 ProgramArguments 添加 --dev
```

### Supervisor 常用命令

```bash
# 重新加载配置
sudo supervisorctl reread && sudo supervisorctl update

# 启动/停止/重启
sudo supervisorctl start cicy-code
sudo supervisorctl stop cicy-code
sudo supervisorctl restart cicy-code

# 查看状态
sudo supervisorctl status

# 查看日志
sudo supervisorctl tail -f cicy-code

# 验证符号链接
ls -la /etc/supervisor/conf.d/cicy-code.conf
```

## 预置 AI Agent

首次启动时选择安装以下 AI 工具（Kiro CLI 和 OpenCode 为必装项）：

| 端口 | Agent | 说明 | 必装 |
|------|-------|------|------|
| 10001 | Kiro CLI | 多功能 AI 助手 | ✅ |
| 10002 | Claude Code | Anthropic 代码助手 | |
| 10003 | GitHub Copilot CLI | GitHub AI 助手 | |
| 10004 | Gemini CLI | Google AI 助手 | |
| 10005 | OpenAI Codex | 代码生成助手 | |
| 10006 | OpenCode | 开源代码助手 | ✅ |

输入编号选择（空格分隔），或输入 `a` 全选。每个选中的 Agent 获得独立的 Worker 和终端实例。

## 端口说明

| 服务 | 端口 | 说明 |
|------|------|------|
| API | 18008 | 主服务，含嵌入式管理 UI |
| 内置 Agent | 10001-10006 | 6 大 Agent 终端 (ttyd)，固定端口 |
| 用户 Worker | 20001+ | 用户自建 Worker，动态分配 |
| code-server | 18080 | 代码编辑器 |

自定义 API 端口：
```bash
PORT=9000 npx cicy-code
```

## 启动流程

```
npx cicy-code
  ↓
checkEnv()                  ← 顺序执行，阻塞式
  ├─ 修复 PATH（macOS Homebrew /opt/homebrew/bin）
  ├─ 验证 tmux 已安装
  ├─ ensureTmuxConf()       ← 检查 ~/.tmux.conf 版本，提示更新
  ├─ 首次运行 → runSetup()  ← 交互式选 Agent → 安装 → 创建 Worker
  ├─ ensureBuiltinAgents()  ← 确保所有已注册 Agent 的 tmux + ttyd 运行
  └─ ensureCodeServer()     ← 安装并启动 code-server
  ↓
startWatcher()              ← checkEnv 完成后启动，每 3s 同步
startTmuxHealth()           ← 每 30s 健康检查
```

**首次启动**：`runSetup()` 引导选择 Agent → 安装工具 → 创建 Worker（固定端口 10001-10006）
**后续启动**：跳过 `runSetup()`，`ensureBuiltinAgents()` 从数据库读取已注册的 Agent，自动拉起 tmux session 和 ttyd 终端

## 资源嵌入架构

```
Binary 内嵌资源：
  ├── tmux.conf          (//go:embed, 带版本号)
  ├── ttyd inject HTML   (//go:embed, 面板/语音 UI, ~31KB)
  ├── ttyd 静态资源       (go-bindata, JS/CSS/fonts)
  └── 管理 UI             (//go:embed ui, React SPA)

--dev 模式覆盖：
  ├── mgr/resources/ttyd-inject-*.html  ← 文件系统热重载
  └── CSS/JS → COS CDN                 ← 远程加载
```

## 数据目录

```
~/.cicy/
├── data.db      # SQLite 数据库（Worker/Agent/Token 等）
└── kv.json      # 缓存文件
~/global.json    # Token 存储（本地模式）
~/.tmux.conf     # tmux 配置（由 cicy-code 管理，带版本号）
```

## 系统要求

- **tmux** — 必须，终端复用
- **Node.js** — 需要 npm 来安装部分 Agent（claude、gemini、codex）
- **code-server** — 代码编辑器（自动安装）

### macOS

```bash
brew install tmux
```

> macOS 上 Homebrew 安装的工具位于 `/opt/homebrew/bin`，程序会自动将其加入 PATH。

### Linux (Ubuntu/Debian)

```bash
sudo apt install tmux
```

### Windows (通过 WSL2)

cicy-code 依赖 tmux 和 Unix PTY，Windows 需通过 WSL2 运行。

**首次安装 WSL2**（管理员 PowerShell）：

```powershell
# 安装 WSL2 + Ubuntu（需重启电脑）
wsl --install
```

重启后打开 "Ubuntu" 应用，设置用户名密码，然后：

```bash
# 安装依赖
sudo apt update && sudo apt install -y tmux curl

# 安装 Node.js 20
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt install -y nodejs

# 一键启动
npx cicy-code
```

Windows 浏览器直接访问 `http://localhost:18008` ，WSL2 网络自动映射到 Windows 宿主机。

> **提示**：
> - WSL2 文件系统在 Windows 资源管理器中可通过 `\\wsl$\Ubuntu` 访问
> - VS Code 可通过 "Remote - WSL" 插件直接编辑 WSL 中的项目
> - WSL2 重启后 cicy-code 需要重新启动（推荐配合 Supervisor 管理）

## 手动安装

如果不想用 npx，可以手动下载：

```bash
# macOS (Apple Silicon)
curl -fsSL https://github.com/cicy-ai/cicy-code/releases/latest/download/cicy-code-darwin-arm64 -o cicy-code
chmod +x cicy-code
./cicy-code

# macOS (Intel)
curl -fsSL https://github.com/cicy-ai/cicy-code/releases/latest/download/cicy-code-darwin-amd64 -o cicy-code

# Linux (x64)
curl -fsSL https://github.com/cicy-ai/cicy-code/releases/latest/download/cicy-code-linux-amd64 -o cicy-code

# Linux (ARM64)
curl -fsSL https://github.com/cicy-ai/cicy-code/releases/latest/download/cicy-code-linux-arm64 -o cicy-code
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | 18008 | API 端口 |
| `SQLITE_PATH` | ~/.cicy/data.db | 数据库路径 |
| `KV_PATH` | ~/.cicy/kv.json | 缓存文件路径 |
| `SAAS_MODE` | - | 设为 1 启用 SaaS 模式 |

## 常见问题

详见 [terminal-clear-error.md](terminal-clear-error.md) — 终端 "terminal does not support clear" 排查指南。

### macOS 安全提示

```bash
xattr -d com.apple.quarantine ./cicy-code
```

### 端口被占用

```bash
PORT=9000 ./cicy-code
```

### 查看日志

```bash
# 直接运行时日志输出到终端
# Supervisor 模式
tail -f /var/log/cicy-code/output.log

# macOS launchd 模式
tail -f /tmp/cicy-code.log
```
