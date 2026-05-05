# cicy-code

`cicy-code` 是一个本地优先的多 agent 开发工作区。

它把 tmux worker、WebTTY、React 工作区、code-server 代理、OpenClaw 网关、shared workspace API、runtime/machines API、npm 启动器和本地 skills 收在同一个仓库里。

## 当前结论

这份 README 只描述当前代码状态。

- 本地开发主入口：`python3 dev.py`
- 正式构建入口：`./build.sh`
- 后端主入口：`api/mgr/main.go`
- 前端主入口：`app/src/App.tsx`
- 主工作区组件：`app/src/components/Workspace.tsx`
- 版本号源头：`npm/package.json`
- 本地状态根目录：`~/cicy-ai`

## 目录

```text
cicy-code/
├── app/                    React + Vite 前端
├── api/                    Go 后端、WebTTY、ttyd 资产、manager 运行时
│   ├── mgr/                主程序与业务路由
│   ├── server/             WebTTY HTTP/WebSocket 服务
│   ├── webtty/             WebTTY 协议实现
│   ├── js/                 终端前端静态资源源码
│   └── resources/          后端静态资源
├── npm/                    npm 发布包与启动器
├── skills/                 本地 CLI：cicy-code / cicy-master
│   ├── code-server-extension/ code-server / VS Code 扩展源码
├── docs/                   当前文档与历史记录
├── dev.py                  本地开发入口
├── build.sh                标准构建入口
├── versions.json           基础镜像与静态资源版本
└── Makefile                常用别名
```

## 运行方式

### 1. 本地开发

```bash
cd ~/projects/cicy-code
python3 dev.py
```

`dev.py` 当前会：

1. 读取 `~/cicy-ai/global.json`
2. 停掉占用 `8008` 的旧 `cicy-code`
3. 刷新 ttyd 嵌入资产
4. 同步版本号
5. 执行 `./build.sh build <platform>`
6. 后台启动 `api/cicy-code --public --dev`
7. tail `.dev-logs/cicy-code.log`

默认端口：

- API：`8008`
- Vite：`8022`
- code-server：`8002`

### 2. 前后端分开调试

前端：

```bash
cd app
npm ci
npm run dev
```

后端：

```bash
cd api
go run ./mgr/ --dev --public
```

`--dev` 下，后端会把非 API 请求反代到 `http://127.0.0.1:8022`。

### 3. npm 用户入口

```bash
npx cicy-code
```

npm 包会在安装阶段下载对应平台的预编译二进制；启动器位于 `npm/bin/cicy-code.js`。

### 4. 桌面模式

```bash
cicy-code --desktop
```

当前桌面模式依赖全局 `electron` 和 `cicy-desktop`。npm 启动器与后端 `--desktop` 模式都会尝试拉起它。

## 常用命令

```bash
# 本地开发
python3 dev.py

# 仅刷新 ttyd 嵌入资产
python3 dev.py --ttydAssets

# 前端热更新
cd app && npm run dev

# 后端 dev 模式
cd api && go run ./mgr/ --dev --public

# Go 测试
cd api && go test ./...

# 构建当前平台
./build.sh build

# 构建所有平台
./build.sh all

# 构建 Docker runtime 镜像
./build.sh docker <tag>

# 构建 Docker base 镜像
./build.sh docker-base <tag>
```

`make dev-api` 当前只是 `go run ./mgr/`，不带 `--dev --public`；调试 Vite 代理时请直接用上面的显式命令。

## 构建规则

标准构建入口是 `build.sh`，不是直接 `go build ./mgr/`。

`build.sh` 当前会：

1. 同步版本号
2. 复制 `api/resources` 到 `api/mgr/resources`
3. 复制 `.tmux.conf` 与 `.cicy_tmux.conf` 到 `api/mgr/`
4. 构建 `app/dist`（除非 `SKIP_NPM=1`）
5. 刷新 `api/server/asset.go`（除非 `SKIP_TTYD_ASSET=1`）
6. 复制 `app/dist` 到 `api/mgr/ui`
7. 最后再编译 `api/mgr`

只直接执行 `go build ./mgr/` 会跳过这些嵌入步骤。

## 架构

### 后端：`api/mgr`

主入口是 `api/mgr/main.go`。当前路由面包括：

- `/api/auth/*`
- `/api/panes` 与 `/api/tmux/*`
- `/api/chat/*`
- `/api/stats/*`
- `/api/queue/*`
- `/api/agents/*`
- `/api/groups/*`
- `/api/nodes` / `/api/machines/*`
- `/api/runtime/*`
- `/api/shared-workspace/*`
- `/api/skills/*`
- `/api/settings/*`
- `/api/openclaw/*`
- `/code/*`
- `/ttyd/*`

关键文件：

- `api/mgr/setup.go`：环境检查、安装、worker 初始化、code-server 启动
- `api/mgr/tmux.go`：pane 生命周期、tmux send、agent 启动脚本
- `api/mgr/chatbus.go`：聊天 WebSocket、poll 数据、client 间广播
- `api/mgr/runtime.go`：runtime instance/session/task/artifact
- `api/mgr/machines.go`：机器列表、同步、配置落盘
- `api/mgr/shared_workspace.go`：文件式协作层
- `api/mgr/proxy.go` / `api/mgr/openclaw_gateway.go`：代理与 AI 网关
- `api/mgr/ui.go`：内嵌 UI 或 Vite 反代

### 终端层

- `api/server`：终端 HTTP/WebSocket 服务
- `api/webtty`：协议层
- `api/js`：浏览器端终端资产源码

如果改的是 `api/js/src/*`，还需要：

```bash
cd api && make asset
```

### 前端：`app`

- `app/src/App.tsx`：入口与 hash 路由切换
- `app/src/components/Workspace.tsx`：主工作区、agent stack、code-server 面板、team panel、skills、WebSocket 状态
- `app/src/services/api.ts`：统一 API 客户端
- `app/src/config.ts`：前端版本号、API base、路径辅助函数

前端视图当前主要有：

- `desktop`
- `workspace`
- `audit`（前端仍有残留，见下方“已知差异”）

### npm 与扩展

- `npm/bin/cicy-code.js`：npm 启动器
- `npm/scripts/install.js`：按平台下载二进制
- `api/code-server-extension/`：发送文件路径给当前 agent 的扩展源码

## worker 与 agent

pane 是核心运行单位，典型 ID 形态：

- `w-10001`
- `w-10001:main.0`

当前内置 agent 目录由 `api/mgr/setup.go` 定义：

- `claude`
- `codex`
- `opencode`
- `kiro-cli`
- `copilot`
- `cicy-wechat`
- `cicy-feishu`
- `openclaw`
- `hermes`
- `cicy-claude`

当前事实：

- `python3 dev.py` 这条主路径下，默认内置 agent 是 `claude`
- 首个内置 worker 仍然是 `w-10001`
- `--agents=all` 时，端口按上面列表顺序从 `10001` 递增分配
- 前端创建对话框也以这组 agent 类型为准

`App.tsx` 里仍保留了一个前端兜底：如果登录后发现 `w-10001` 不存在，会尝试创建一个 `hermes` pane。它不是当前主启动链路的真实默认值，只是 UI 侧补救逻辑。

## 配置与路径

### 全局状态目录

当前代码以 `~/cicy-ai` 为根：

- `~/cicy-ai/global.json`
- `~/cicy-ai/.cicy/`
- `~/cicy-ai/workers/`
- `~/cicy-ai/projects/`
- `~/cicy-ai/shared-workspace/`
- `~/cicy-ai/cicy-node.json`

`api/mgr/paths.go` 还保留了 `/cicy/...` 到 `~/cicy-ai/...` 的运行时路径映射，所以某些 API 或 agent 看到的绝对路径可能仍是 `/cicy/...` 形态。

### 机器注册表：有两套默认路径

这是当前仓库里最容易混淆的一点：

1. **后端 runtime 默认文件**：`~/cicy-ai/cicy-node.json`
   - 来自 `api/mgr/paths.go`
   - `api/mgr/machines.go` 会读写它

2. **skills CLI 默认文件**：`~/Private/cicy-node.json`
   - 来自 `skills/cicy-code` 与 `skills/cicy-master`
   - 可用 `CICY_NODES_FILE` 改写

如果你希望 skills 与后端读写同一份文件，显式设置：

```bash
export CICY_NODES_FILE=~/cicy-ai/cicy-node.json
```

### 常用环境变量

常见变量包括：

- `PORT`
- `CS_PORT`
- `SQLITE_PATH`
- `CICY_API_TOKEN`
- `CICY_API_KEY`
- `CICY_API_URL`
- `CICY_ANTHROPIC_URL`
- `CICY_DEFAULT_OPENCODE_MODEL`
- `CICY_DEFAULT_CLAUDE_MODEL`
- `CICY_CODEX_MODEL`
- `CICY_OPENCLAW_MODEL`
- `CICY_RUNTIME_KIND`
- `CICY_RUNTIME_MODE`
- `CICY_RUNTIME_API_ONLY`
- `CICY_PUBLIC_URL`
- `CICY_MASTER_URL`
- `CICY_MASTER_TOKEN`
- `CICY_TEAM_TOKEN`
- `CICY_TEAMCENTER_URL`

其中：

- `CICY_RUNTIME_MODE=api-only` 或 `CICY_RUNTIME_API_ONLY=1` 会让部分 tmux/desktop 接口返回 `not_supported_in_api_only_runtime`
- `CICY_MASTER_URL` / `CICY_MASTER_TOKEN` / `CICY_PUBLIC_URL` 会触发 managed runtime 的自注册路径

## managed runtime / Docker

当前仓库仍保留完整 Docker 与 managed runtime 能力：

- `api/Dockerfile.runtime`
- `api/Dockerfile.runtime.base`
- `./build.sh docker <tag>`
- `./build.sh docker-base <tag>`
- `python3 dev.py --docker`
- `python3 dev.py --dockerBuild`
- `python3 dev.py --cloudRun`
- `python3 dev.py --cloudRunList`

`python3 dev.py --dockerBuild` 当前还会先做 CDN 资产构建与 COS 上传，再 push runtime 镜像，并更新 `~/cicy-ai/global.json -> images.runtime*`。

## code-server 扩展

`api/code-server-extension` 提供两个用户动作：

- 资源管理器右键：发送路径给当前 agent
- 编辑器右键：发送当前文档/选区给当前 agent

它通过：

- `POST /api/code-server/send-path`

与当前页面所属的 `cicy-code` 后端通信。

当前只保留 `api/code-server-extension/` 这一套扩展源码与打包产物。

## 已知差异

当前代码与历史文档相比，至少有这几处需要按代码为准：

- 全局状态根目录是 `~/cicy-ai`，不是 `/cicy`
- 默认 dev agent 是 `claude`，不是一组固定的四 agent 启动模版
- 机器配置文件存在“后端一份、skills 一份”的默认路径差异
- 前端仍有 audit 页面和 `/api/audit/*` 调用，但 `api/mgr/main.go` 当前并没有注册这些 audit 路由；它属于未接完的残留面

## 当前建议工作流

1. 本地开发先用 `python3 dev.py`
2. 需要前端热更新时，再开一个 `cd app && npm run dev`
3. 改 `api/js` 后执行 `cd api && make asset`
4. 正式构建走 `./build.sh`
5. 节点注册表管理走 `skills/cicy-master`
6. 节点 API 调用走 `skills/cicy-code`

## 许可证

MIT
