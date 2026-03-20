# "open terminal failed: terminal does not support clear" 排查指南

## 错误描述

在浏览器访问 cicy-code 终端时，出现以下错误之一：

```
open terminal failed: terminal does not support clear
```

```
tput: unknown terminal "unknown"
```

或 AI Agent（Claude Code、Gemini CLI 等）启动时直接报错退出。

---

## 根因分析

该错误涉及 **三层 TERM 环境变量传递链**，任一层出问题都会导致终端功能异常。

### 架构图

```
浏览器 (xterm.js)
    │
    ▼
ttyd-go HTTP 服务 ──── server.Options.Term = "xterm"
    │
    ▼
PTY 子进程 ──────────── cmd.Env 中的 TERM（继承自 cicy-code 父进程）
    │
    ▼
tmux attach -t <pane> ── tmux session 内的 TERM（由 default-terminal 决定）
```

### 第一层：PTY 子进程的 TERM（最常见原因）

**问题**：`local_command.go` 中 `exec.Command("tmux", "attach", ...)` 创建子进程时，如果未显式设置 `cmd.Env`，则继承父进程（cicy-code 二进制）的环境变量。

当 cicy-code 通过 `nohup` 或 `systemd` 启动时，父进程的 TERM 通常是空的或 `dumb`：

```bash
# nohup 启动时 TERM 通常为空
nohup ./cicy-code > /tmp/cicy-code.log 2>&1 &
# 此时子进程的 TERM = "" 或 "dumb"
```

**修复**（v0.2.3+已修复）：

```go
// api/backend/localcommand/local_command.go
cmd := exec.Command(command, argv...)
cmd.Env = append(os.Environ(), "TERM=xterm-256color")  // 显式设置
pty, err := pty.Start(cmd)
```

### 第二层：tmux 的 default-terminal

**问题**：tmux session 内部的 TERM 由 `~/.tmux.conf` 中的 `default-terminal` 决定。如果未设置，默认是 `screen`，某些工具不完全兼容。

**修复**：

```bash
# ~/.tmux.conf
set -g default-terminal "xterm-256color"
```

v0.2.3+ 会自动管理 `~/.tmux.conf`（内嵌在二进制中，带版本号，启动时检测并提示更新）。

### 第三层：tmux send-keys 竞态条件

**问题**：早期版本通过 `tmux send-keys` 异步发送 `export TERM=xterm-256color`，但 send-keys 只是把按键放入输入缓冲区，不等命令执行完。如果紧接着运行 `clear` 或 AI 工具，TERM 可能还没生效。

**现状**：v0.2.3+ 通过第一层和第二层的修复，已不再依赖 send-keys 设置 TERM。

---

## 排查步骤

### 1. 确认版本

```bash
# 查看当前安装版本
cat ~/.nvm/versions/node/$(node -v)/lib/node_modules/cicy-code/package.json | grep version

# v0.2.3+ 已包含 TERM 修复
```

### 2. 检查 tmux session 内的 TERM

```bash
# 连接到有问题的 worker
tmux attach -t w-20001:main.0

# 检查 TERM
echo $TERM
# 预期输出: xterm-256color

# 如果不是，手动修复
export TERM=xterm-256color
```

### 3. 检查 tmux.conf

```bash
head -3 ~/.tmux.conf
# 应看到:
# # cicy-code tmux.conf v1
# set -g default-terminal "xterm-256color"
```

如果不是，重启 cicy-code 会自动提示更新。或手动修改后：

```bash
tmux source-file ~/.tmux.conf     # 重新加载
tmux kill-server                   # 或重启 tmux
```

### 4. 检查 terminfo 数据库

```bash
# 验证 xterm-256color terminfo 存在
infocmp xterm-256color >/dev/null 2>&1 && echo "OK" || echo "MISSING"
```

如果输出 `MISSING`（极少见，常见于精简 Docker 镜像）：

```bash
# macOS
brew install ncurses

# Ubuntu/Debian
apt-get install ncurses-term

# Alpine
apk add ncurses-terminfo-base
```

### 5. 检查 cicy-code 父进程 TERM

```bash
# 找到 cicy-code 进程
ps aux | grep cicy-code

# 检查其环境变量
cat /proc/<PID>/environ | tr '\0' '\n' | grep TERM
# macOS 上无 /proc，改用:
ps -E -p <PID> | grep TERM
```

如果 TERM 为空或 `dumb`，说明是通过 `nohup` 或 `systemd` 启动。v0.2.3+ 已在代码层面修复此问题。

---

## 快速修复（临时）

如果无法立即更新版本，在已运行的 worker 中手动修复：

```bash
# 方式一：在 tmux session 中手动 export
tmux send-keys -t w-20001:main.0 "export TERM=xterm-256color" Enter

# 方式二：重启 cicy-code 前设置环境变量
export TERM=xterm-256color
nohup ./cicy-code > /tmp/cicy-code.log 2>&1 &
```

---

## 版本修复记录

| 版本 | 修复内容 |
|------|----------|
| v0.2.2 | 在 tmux.conf 中设置 `default-terminal "xterm-256color"` |
| v0.2.3 | PTY 子进程显式设置 `TERM=xterm-256color`；内嵌 tmux.conf 带版本管理 |

---

## 相关文件

- `api/backend/localcommand/local_command.go` — PTY 创建，TERM 设置
- `api/mgr/instance.go` — ttyd 服务启动，server.Options.Term
- `api/mgr/tmux.conf` — 内嵌 tmux 配置（通过 `//go:embed` 打入二进制）
- `api/mgr/main.go` — `ensureTmuxConf()` 版本检测与更新逻辑
