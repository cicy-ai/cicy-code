# 本地部署指南

## 快速开始

```bash
npx cicy-code
```

首次运行会自动：
1. 下载对应平台的二进制
2. 检测并安装 tmux、code-server（如缺失）
3. 启动 code-server (端口 18080)
4. 启动 API 服务 (端口 18008)
5. 创建数据目录 `~/.cicy/`

访问：`http://localhost:18008/?token=YOUR_TOKEN`

## 获取 Token

首次启动后需要创建 token：

```bash
sqlite3 ~/.cicy/data.db "INSERT INTO tokens (token, perms, note) VALUES ('$(openssl rand -hex 16)', 'admin', 'default');"
sqlite3 ~/.cicy/data.db "SELECT token FROM tokens LIMIT 1;"
```

## 端口说明

| 服务 | 端口 | 说明 |
|------|------|------|
| API | 18008 | 主服务，含嵌入式 UI |
| code-server | 18080 | 代码编辑器 |

自定义端口：
```bash
PORT=9000 npx cicy-code  # API 端口改为 9000
```

## 数据目录

所有数据存储在 `~/.cicy/`：

```
~/.cicy/
├── data.db    # SQLite 数据库
└── kv.json    # 缓存文件
```

## 系统要求

- **tmux** — 必须，终端复用
- **code-server** — 必须，代码编辑器（自动安装）

### macOS

```bash
brew install tmux
```

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

### SaaS 模式

```bash
MYSQL_DSN=user:pass@tcp(host:3306)/db SAAS_MODE=1 ./cicy-code
```

- 端口：8008
- 数据库：MySQL
- 缓存：Redis
- 跳过依赖检查

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

### 查看日志

```bash
# API 日志直接输出到终端
# code-server 日志
tail -f /usr/local/var/log/code-server.log  # macOS
tail -f ~/.local/share/code-server/logs/    # Linux
```
