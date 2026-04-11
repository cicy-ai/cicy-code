# cicy-code

`cicy-code` 是一个本地优先的 AI Agent 协作开发环境。

它把 tmux worker、WebTTY 终端、React 工作区、code-server、AI CLI 启动逻辑、node registry、shared workspace API 和 npm 启动入口收在同一个仓库里，目标不是“聊天 UI”，而是让一个人同时操作多个可执行的 agent workspace。

## 当前状态

这份 README 以当前仓库真实状态为准，而不是历史文档：

- 当前推荐的本地开发入口是 `python3 dev.py`
- 当前正式构建入口是 `./build.sh`
- 前端开发服务器是 `app` 下的 Vite，默认 `8022`
- 后端默认监听 `8021`
- 旧的 `.env`、`docker-compose.yml`、desktop submodule、`api/Makefile.manager` 已经不再是主流程的一部分
- 仓库里仍然保留了一些历史脚本，但不应默认当成现在的推荐路径

## 核心能力

- tmux 驱动的多 pane / 多 worker 工作区
- 每个 worker 绑定独立 agent 类型、工作目录、ttyd 端口和运行状态
- React 工作区 UI，内含终端、聊天、队列、技能、团队面板、设置面板
- 内嵌式 code-server 代理
- 自带 WebTTY / ttyd-go 前后端协议实现，不依赖外部终端网关
- 本地 SQLite 默认模式，也支持 Redis / 容器运行时
- 多节点 registry，配置文件默认在 `~/Private/cicy-node.json`
- shared workspace API，用 JSON / Markdown 文件做跨 worker 协作桥接
- npm launcher，支持 `npx cicy-code`
- 可选桌面模式：复用全局安装的 `cicy-desktop`

## 目录结构

```text
cicy-code/
├── app/                  React 19 + Vite 前端
├── api/                  Go 后端、WebTTY、ttyd-go 相关实现
│   ├── mgr/              主程序，负责 tmux / panes / skills / auth / runtime / UI embed
│   ├── server/           WebTTY HTTP / WebSocket 服务层
│   ├── webtty/           WebTTY 协议与实现
│   ├── resources/        后端静态资源
│   └── js/               ttyd 前端资源构建源码
├── npm/                  npm 发布包与 `cicy-code` 启动器
├── skills/               本地 CLI helpers：`cicy-code`、`cicy-master`
├── scripts/              版本同步、安装、镜像相关脚本，部分为历史脚本
├── docs/                 当前仓库内剩余文档
├── build.sh              统一构建脚本
├── dev.py                当前推荐的本地开发入口
├── versions.json         基础镜像 / 资源版本信息
└── Makefile              常用别名
```

## 运行模型

项目由四层组成：

1. `api/mgr`
   这是核心运行时。负责启动与恢复 tmux workers，提供 `/api/*`，管理 token、pane、agent、queue、machines、shared workspace、skills、runtime 任务等能力。

2. `api/server` + `api/webtty`
   这是终端传输层。后端通过 WebSocket 把 PTY 映射到浏览器，前端协议实现位于 `api/js/src/webtty.ts`。

3. `app`
   React 工作区。主界面包括 Home、Workspace、Audit、TeamPanel、SkillPanel、Settings、Terminal、Chat 等。

4. `npm`
   发布给最终用户的入口。`npx cicy-code` 最终运行的是这个目录里的启动器和预编译二进制。

## 默认 agent 类型

当前主流程围绕这 4 类 agent：

- `openclaw`
- `codex`
- `claude`
- `opencode`

在 `--dev` 模式下，如果没有显式传 `--agents=...`，默认会按这 4 类去准备内置 worker。

当前内置 worker 端口映射也以这 4 个为准：

- `10001` -> `openclaw`
- `10002` -> `codex`
- `10003` -> `claude`
- `10004` -> `opencode`

## 依赖

最低建议环境：

- Go `1.25+`
- Node.js `20+`
- `tmux`
- `git`
- `curl`

本地模式下，程序会检查并尽量安装缺失的 AI CLI / `code-server`：

- `openclaw`
- `@openai/codex`
- `@anthropic-ai/claude-code`
- `opencode`
- `code-server`

容器 runtime 下则假设这些工具已经预装在镜像中。

## 快速开始

### 方式 1：npm 用户入口

```bash
npx cicy-code
```

服务启动后会打印访问地址。默认端口来自后端二进制本身，通常是：

```text
http://127.0.0.1:8021/?token=<token>
```

token 也会写入 `~/global.json` 的 `api_token`。

### 方式 2：本地开发主入口

```bash
cd /path/to/cicy-code
python3 dev.py
```

`dev.py` 会做这些事：

- 读取 `~/global.json` 里的 AI provider 配置
- 同步版本号
- 设置 `SKIP_NPM=1`
- 构建 `api/cicy-code`
- 杀掉占用当前 API 端口的旧 `cicy-code` 进程
- 最后执行 `api/cicy-code --public --dev`

默认端口：

- API: `8021`
- Vite: `8022`
- code-server: `8002`

### 方式 3：前后端分开调试

前端：

```bash
cd app
npm install
npm run dev
```

后端：

```bash
cd api
go run ./mgr/ --dev --public
```

在 `--dev` 模式下，后端的 UI 层会把非 API 请求代理到 `http://127.0.0.1:8022`，所以前端热更新是原生生效的。

## 常用命令

```bash
# 本地开发主入口
python3 dev.py

# 仅重建 ttyd / WebTTY 静态资源
python3 dev.py --ttydAssets

# 前端开发
make dev-app

# 后端开发
make dev-api

# 构建当前平台二进制
make build
# 等价于
./build.sh build

# 构建所有平台
make build-all
# 等价于
./build.sh all

# 构建容器运行镜像
./build.sh docker <tag>

# 构建基础镜像
./build.sh docker-base <tag>
```

## 构建说明

不要把 `go build ./mgr/` 当成标准发版流程。

标准构建入口是 [`build.sh`](build.sh)，它会先做这些准备：

- 把 `api/resources` 复制到 `api/mgr/resources`
- 把根目录 `.tmux.conf` 和 `.cicy_tmux.conf` 复制到 `api/mgr/`
- 构建 `app/dist`
- 把 `app/dist` 复制到 `api/mgr/ui`
- 如果仓库内存在 `mitmproxy/`，会一并复制到 `api/mgr/monitor`

之后才会对 `./mgr/` 做 Go build。

如果前端产物已经是最新的，可以用：

```bash
SKIP_NPM=1 ./build.sh build
```

## 配置来源

当前仓库的配置来源主要有三类：

### 1. 环境变量

最常用的包括：

- `PORT`
- `CS_PORT`
- `SQLITE_PATH`
- `CICY_API_KEY`
- `CICY_API_URL`
- `CICY_ANTHROPIC_URL`
- `CICY_DEFAULT_OPENCODE_MODEL`
- `CICY_DEFAULT_CLAUDE_MODEL`
- `CICY_CODEX_MODEL`
- `CICY_OPENCLAW_MODEL`
- `CICY_API_TOKEN`
- `CICY_RUNTIME_KIND`
- `CICY_TEAM_TOKEN`
- `CICY_TEAMCENTER_URL`
- `CICY_TEAMCENTER_BOOTSTRAP_PATH`
- `CICY_MASTER_URL`
- `CICY_MASTER_TOKEN`
- `CICY_PUBLIC_URL`
- `CICY_CLOUDFLARED_TOKEN`

其中 runtime / docker 的环境约定是：

- dev 和 prod 走同一套容器启动流程
- 容器先用 `CICY_TEAM_TOKEN` 调 `CICY_TEAMCENTER_URL + CICY_TEAMCENTER_BOOTSTRAP_PATH`
- 服务端返回 `CICY_PUBLIC_URL`、`CICY_MASTER_URL`、`CICY_MASTER_TOKEN`、`CICY_CLOUDFLARED_TOKEN`
- 容器内再启动 `cloudflared`，并把自己注册回 teamcenter

也就是说：

- VM / docker / cloudflared 流程不分 dev / prod
- 只通过 teamcenter 地址和回填域名区分环境

### 2. `~/global.json`

这是当前本地开发流最重要的外部配置文件之一。`dev.py` 会从这里读取：

- `api_token`
- `ai.currentProvider`
- `ai.provider.*`
- `cicyAiapikey`
- `2000RunApikey`
- `cicy-cluster.*`
- `images.*`

### 3. `~/Private/cicy-node.json`

这是 node registry 的默认来源，由：

- 后端 `api/mgr/machines.go`
- CLI `skills/cicy-code`
- CLI `skills/cicy-master`

共同使用。

## 前端概览

前端主入口在 [`app/src/App.tsx`](app/src/App.tsx)，当前主要有三种视图：

- `desktop`
- `workspace`
- `audit`

关键模块：

- `components/Workspace.tsx`
  主工作区。包含终端、聊天、team panel、skill panel、settings、code-server 浮窗等。

- `components/CreateAgentDialog.tsx`
  创建工作实例，当前支持 `openclaw`、`codex`、`claude`、`opencode`，并支持：
  - 启动时允许所有操作
  - 默认中文回复

- `components/chat/ChatView.tsx`
  聊天和 agent 事件展示。

- `components/terminal/*`
  命令面板、终端 frame、窗口管理。

- `services/api.ts`
  所有前端 API 调用都从这里出。

## 后端概览

后端主入口在 [`api/mgr/main.go`](api/mgr/main.go)。

主要能力分布如下：

- `setup.go`
  环境检查、依赖安装、内置 worker 恢复与初始化

- `tmux.go`
  worker 启动、tmux 指令、agent 启动脚本拼装、自动确认逻辑

- `db.go`
  SQLite schema 与 migration

- `machines.go`
  node registry、机器同步、实例视图

- `runtime.go`
  runtime instance / session / task / artifact API

- `shared_workspace.go`
  work item、artifact、handoff、event 的文件式共享协作层

- `skills.go`
  skills API

- `proxy.go`
  code-server / mitm / 其他代理能力

- `ui.go`
  SPA 静态资源或 Vite dev server 代理

## API 能力面

从当前 `main.go` 暴露的路由来看，项目已经不只是“tmux 管理器”，而是一个完整的协作 runtime：

- `/api/auth/*`
- `/api/tmux/*`
- `/api/chat/*`
- `/api/stats/*`
- `/api/queue/*`
- `/api/agents/*`
- `/api/groups/*`
- `/api/machines/*`
- `/api/runtime/*`
- `/api/shared-workspace/*`
- `/api/skills/*`
- `/api/settings/*`
- `/api/openclaw/*`
- `/code/*`
- `/ttyd/*`

## Skills 与节点工具

仓库自带两个实用 CLI：

### `skills/cicy-code`

面向节点 API 调用，支持：

- 选择默认或指定 node
- 请求 runtime / session / task API
- 请求 shared workspace API
- 请求 tmux / panes API

### `skills/cicy-master`

面向本地 node registry 管理，支持：

- 规范化 `~/Private/cicy-node.json`
- 原子写入
- 文件锁
- `register`
- `sync`
- `ping`
- `health`

## 桌面模式

npm 启动器支持：

```bash
cicy-code --desktop
```

它会：

- 启动 `cicy-code` API
- 检查全局 `electron`
- 检查全局 `cicy-desktop`
- 如果存在则打开桌面模式

桌面模式现在依赖全局安装的 `cicy-desktop`，仓库内部已经不再内置 desktop submodule。

## Docker / Container Runtime

当前仓库仍然保留了这部分底层能力：

- `api/Dockerfile.runtime.base`
- `api/Dockerfile.runtime`
- `build.sh docker`
- `build.sh docker-base`
- `dev.py --docker`
- `dev.py --dockerBuild`

但高层部署脚本正在清理中，因此 README 不再把它们当成本地开发主流程。更准确地说：

- 镜像构建能力还在
- 自动化部署脚本并不稳定，是否可用要以当前工作区文件状态为准

## 当前推荐工作流

如果你是在这个仓库里继续开发，建议默认这样做：

1. 用 `python3 dev.py` 跑本地 API
2. 需要热更新 UI 时，再开一个 `cd app && npm run dev`
3. 正式构建或出二进制时，用 `./build.sh`
4. 管理 node registry 时，用 `skills/cicy-master`
5. 调用远端或本地 runtime API 时，用 `skills/cicy-code`

## 需要注意的历史残留

仓库正在从旧的部署方式收敛到当前的本地优先开发流，所以你会看到一些历史文件或脚本痕迹。当前判断标准应该是：

- 以 `dev.py` 和 `build.sh` 为准
- 以 `api/mgr/main.go` 暴露的真实路由为准
- 以 `app/package.json` 和 `npm/package.json` 的脚本为准
- 不要默认相信旧的 `.env` / `docker-compose` / 旧 supervisor 文档

## 许可证

MIT
