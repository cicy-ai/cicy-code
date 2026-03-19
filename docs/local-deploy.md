# 本地部署指南

本文档介绍如何在本地部署 cicy-code 的 API 和前端应用。

## 系统要求

- **Go 1.21+** (API 后端)
- **Node.js 18+** (前端构建)
- **SQLite** (默认数据库，Go 内置) 或 **MySQL 8.0+** (可选)

## 快速开始

### 1. 克隆项目

```bash
git clone https://github.com/cicy-dev/cicy-code.git
cd cicy-code
```

### 2. 数据库配置

**选项 A: SQLite (推荐)**
```bash
# 无需安装，使用默认配置即可
# 数据库文件会自动创建为 ./cicy.db
```

**选项 B: MySQL (可选)**
```bash
# 创建数据库
mysql -u root -p -e "CREATE DATABASE cicy_code;"

# 导入表结构
mysql -u root -p cicy_code < schema.sql
```

### 3. 配置环境变量

复制配置模板：
```bash
cp .env.example .env
```

编辑 `.env` 文件：
```bash
# 数据库配置 (二选一)
# SQLite (推荐)
SQLITE_PATH=./cicy.db

# MySQL (可选)
# MYSQL_DSN=root:password@tcp(localhost:3306)/cicy_code?parseTime=true

# API 配置
API_PORT=8080
JWT_SECRET=your_jwt_secret_here

# 前端配置
VITE_API_URL=http://localhost:8080
```

### 4. 启动 API 后端

```bash
cd api
go mod download
go run main.go
```

API 将在 `http://localhost:8080` 启动。

### 5. 启动前端应用

新开终端：
```bash
cd app
npm install
npm run dev
```

前端将在 `http://localhost:5173` 启动。

## 平台特定说明

### macOS

安装依赖：
```bash
# 使用 Homebrew
brew install go node

# SQLite 已内置在 Go 中，无需额外安装
# 如需 MySQL: brew install mysql && brew services start mysql
```

### Windows

1. **Go**: 从 [golang.org](https://golang.org/dl/) 下载安装
2. **Node.js**: 从 [nodejs.org](https://nodejs.org/) 下载安装  
3. **SQLite**: Go 内置支持，无需额外安装
4. **MySQL** (可选): 从 [mysql.com](https://dev.mysql.com/downloads/mysql/) 下载安装

使用 PowerShell 或 Git Bash 执行命令。

### Linux (Ubuntu/Debian)

```bash
# 安装依赖
sudo apt update
sudo apt install golang-go nodejs npm

# SQLite 已内置在 Go 中，无需额外安装
# 如需 MySQL: sudo apt install mysql-server && sudo systemctl start mysql
```

## 开发模式

### API 热重载

```bash
cd api
go install github.com/cosmtrek/air@latest
air
```

### 前端热重载

```bash
cd app
npm run dev
```

访问 `http://localhost:5173` 即可看到应用。

## 生产构建

### 构建前端

```bash
cd app
npm run build
```

构建产物在 `app/dist/` 目录。

### 构建 API

```bash
cd api
go build -o cicy-api main.go
./cicy-api
```

## 常见问题

### 端口冲突

如果默认端口被占用，修改 `.env` 文件中的端口配置：
```bash
API_PORT=8081
```

前端开发服务器端口在 `app/vite.config.ts` 中修改：
```typescript
export default defineConfig({
  server: {
    port: 3000
  }
})
```

### 数据库连接失败

**SQLite**:
1. 确认目录有写权限
2. 检查 `SQLITE_PATH` 环境变量

**MySQL**:
1. 确认 MySQL 服务已启动
2. 检查 `MYSQL_DSN` 连接字符串格式
3. 确认数据库用户有足够权限

### 跨域问题

开发环境下，前端代理配置在 `app/vite.config.ts`：
```typescript
export default defineConfig({
  server: {
    proxy: {
      '/api': 'http://localhost:8080'
    }
  }
})
```

## 目录结构

```
cicy-code/
├── api/           # Go 后端
├── app/           # React 前端
├── schema.sql     # 数据库表结构
├── .env.example   # 环境变量模板
└── docs/          # 文档
```
