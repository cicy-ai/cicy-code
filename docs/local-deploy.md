# 本地部署指南

## 快速开始

```bash
npx cicy-code
```

首次运行会自动：
1. 下载对应平台的二进制
2. 检测并安装 tmux、code-server（如缺失）
3. 启动主控 Worker（w-10001）及其终端服务
4. 进入交互式 AI 工具选择（选择要安装的 Agent）
5. 为选中的 Agent 创建独立 Worker 并启动终端
6. 启动 code-server（端口 18080）
7. 启动 API 服务（端口 18008）
8. 创建数据目录 `~/.cicy/`

后续启动时，会自动从数据库恢复已有的 active Worker 并拉起各自的终端服务，不再重复创建。

访问：`http://localhost:18008/?token=YOUR_TOKEN`

## 获取 Token

首次启动会自动生成 token 并存储在 `~/global.json` 中：

```bash
cat ~/global.json | grep token
```

也可以手动创建：

```bash
sqlite3 ~/.cicy/data.db "INSERT INTO tokens (token, perms, note) VALUES ('$(openssl rand -hex 16)', 'admin', 'default');"
sqlite3 ~/.cicy/data.db "SELECT token FROM tokens LIMIT 1;"
```

## 预置 AI Agent

首次启动时可选择安装以下 AI 工具（Kiro CLI 为必装项）：

| 编号 | Agent | 说明 | 安装方式 |
|------|-------|------|----------|
| ✅ | Kiro CLI | 多功能 AI 助手（必装） | curl 脚本 |
| 1 | Claude Code | Anthropic 代码助手 | npm |
| 2 | GitHub Copilot CLI | GitHub AI 助手 | curl 脚本 |
| 3 | Gemini CLI | Google AI 助手 | npm |
| 4 | OpenAI Codex | 代码生成助手 | npm |
| 5 | OpenCode | 开源代码助手 | curl 脚本 |

输入编号选择（空格分隔），或输入 `a` 全选。每个选中的 Agent 会获得独立的 Worker 和终端实例。

## 端口说明

| 服务 | 端口 | 说明 |
|------|------|------|
| API | 18008 | 主服务，含嵌入式 UI |
| 主控 Worker (w-10001) | 10001 | 主控终端 (ttyd) |
| Agent Workers | 20001+ | 各 Agent 终端 (ttyd)，按创建顺序递增 |
| code-server | 18080 | 代码编辑器 |

自定义端口：
```bash
PORT=9000 npx cicy-code  # API 端口改为 9000
```

## 启动流程

```
npx cicy-code
  ↓
checkEnv()              ← 顺序执行，阻塞式
  ├─ 修复 PATH（macOS Homebrew /opt/homebrew/bin）
  ├─ 验证 tmux 已安装
  ├─ ensureMasterPane() ← 确保 w-10001 存在且 ttyd 运行
  ├─ runSetup()         ← 仅首次：交互式选 Agent → 安装 → 创建 Worker
  └─ ensureCodeServer() ← 安装并启动 code-server
  ↓
startWatcher()          ← checkEnv 完成后才启动，每 3s 同步
startTmuxHealth()       ← 每 30s 健康检查
```

**首次启动**：`runSetup()` 引导选择 Agent、安装工具、创建 Worker Pane  
**后续启动**：跳过 `runSetup()`，Watcher 从数据库读取 active 的 Pane，自动拉起各自的 ttyd 终端服务

## 数据目录

所有数据存储在 `~/.cicy/`：

```
~/.cicy/
├── data.db      # SQLite 数据库（Worker/Pane/Token 等）
└── kv.json      # 缓存文件
~/global.json    # Token 存储（本地模式）
```

## 系统要求

- **tmux** — 必须，终端复用
- **Node.js** — 需要 npm 来安装部分 Agent（claude、gemini、codex）
- **code-server** — 必须，代码编辑器（自动安装）

### macOS

```bash
brew install tmux
```

> macOS 上 Homebrew 安装的工具位于 `/opt/homebrew/bin`，程序会自动将其加入 PATH。

### Linux (Ubuntu/Debian)

```bash
sudo apt install tmux
```

### Windows

暂不支持（依赖 pty）

## 手动安装

如果不想用 npx，可以手动下载：

```bash
# macOS (Apple Silicon)
curl -fsSL https://github.com/cicy-dev/cicy-code/releases/latest/download/cicy-code-darwin-arm64 -o cicy-code
chmod +x cicy-code
./cicy-code

# macOS (Intel)
curl -fsSL https://github.com/cicy-dev/cicy-code/releases/latest/download/cicy-code-darwin-amd64 -o cicy-code

# Linux (x64)
curl -fsSL https://github.com/cicy-dev/cicy-code/releases/latest/download/cicy-code-linux-amd64 -o cicy-code

# Linux (ARM64)
curl -fsSL https://github.com/cicy-dev/cicy-code/releases/latest/download/cicy-code-linux-arm64 -o cicy-code
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | 18008 | API 端口 |
| `SQLITE_PATH` | ~/.cicy/data.db | 数据库路径 |
| `KV_PATH` | ~/.cicy/kv.json | 缓存文件路径 |
| `SAAS_MODE` | - | 设为 1 启用 SaaS 模式 |
| `MYSQL_DSN` | - | MySQL 连接串（SaaS 模式） |
| `REDIS_HOST` | - | Redis 地址（SaaS 模式） |

## 两种运行模式

### 本地模式（默认）

```bash
npx cicy-code
```

- 端口：18008
- 数据库：SQLite (`~/.cicy/data.db`)
- 缓存：文件 (`~/.cicy/kv.json`)
- 自动启动 code-server
- 交互式 Agent 选择与安装

### SaaS 模式

```bash
MYSQL_DSN=user:pass@tcp(host:3306)/db SAAS_MODE=1 ./cicy-code
```

- 端口：8008
- 数据库：MySQL
- 缓存：Redis
- 跳过依赖检查和交互式 Setup

## 常见问题

### macOS 安全提示

从网上下载的二进制可能被 Gatekeeper 拦截：

```bash
xattr -d com.apple.quarantine ./cicy-code
```

或在「系统设置 → 隐私与安全」中允许。

### 端口被占用

```bash
PORT=9000 ./cicy-code
```

### 启动时创建了重复 Worker

v0.2.2 已修复此问题。确保使用最新版本：

```bash
npx cicy-code@latest
```

如果仍有残留重复 Worker，可以清理数据库：

```bash
sqlite3 ~/.cicy/data.db "SELECT pane_id, title, ttyd_port, agent_type FROM agent_config ORDER BY ttyd_port;"
# 删除多余的记录
sqlite3 ~/.cicy/data.db "DELETE FROM agent_config WHERE pane_id = 'w-2000x:main.0';"
```

### open terminal failed: terminal does not support clear

**现象**：AI Agent（如 Claude、Gemini）启动时报 `open terminal failed: terminal does not support clear` 或终端功能异常。

**根因**：这是一个 **TERM 环境变量竞态问题**。启动流程中：

1. tmux 创建新 session 时，shell 默认 `TERM=screen`（或为空）
2. 程序通过 `tmux send-keys` 异步发送 `export TERM=xterm-256color`
3. 紧接着又发送 `clear` 或启动 AI 工具
4. 但 `export TERM` 命令可能还没执行完，shell 仍在用旧的 TERM 值
5. 此时 `clear` 或 AI 工具调用 `tput clear` 失败 → 报错

**本质**：`tmux send-keys` 是异步的——只是把按键发到 tmux 输入缓冲区，不等命令实际执行完成。多条 `send-keys` 之间没有同步机制。

**解决方案**：

方式一：在 tmux 配置中全局设置 TERM（推荐）
```bash
# ~/.tmux.conf
set -g default-terminal "xterm-256color"
```
```bash
tmux kill-server  # 重启 tmux 使配置生效
```

方式二：手动修复（已启动的 session）
```bash
# 进入对应的 tmux session
tmux attach -t w-20001
# 手动设置
export TERM=xterm-256color
# 重新启动 AI 工具
```

方式三：确保 terminfo 数据库完整（macOS）
```bash
# 检查 xterm-256color 是否存在
infocmp xterm-256color >/dev/null 2>&1 && echo "OK" || echo "MISSING"

# 如果缺失，安装 ncurses
brew install ncurses
```

**注意**：macOS 自带的 terminfo 数据库位于 `/usr/share/terminfo/`，Homebrew 安装的 ncurses 位于 `/usr/local/opt/ncurses/share/terminfo/`。如果系统 terminfo 中缺少 `xterm-256color`，需要设置 `TERMINFO_DIRS` 或用方式一全局指定。

### gotty/终端服务无法启动

v0.2.2 已修复 macOS 上 PATH 找不到 tmux 的问题。如仍有问题，确认 tmux 已安装：

```bash
which tmux  # 应输出路径
tmux -V     # 确认版本
```

### 查看日志

```bash
# API 日志直接输出到终端
# code-server 日志
tail -f /usr/local/var/log/code-server.log  # macOS
tail -f ~/.local/share/code-server/logs/    # Linux
```
