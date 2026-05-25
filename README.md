# cicy-code

`cicy-code` 是一个本地优先的多 agent 开发工作区。

它把 tmux worker、WebTTY、React 工作区、code-server 代理、OpenClaw 网关、shared workspace API、runtime/machines API、skill 市场、npm 启动器以及一组本地 Go skills（hosttools / skill 管理 / STT / TTS）收在同一个仓库里。

## 当前结论

这份 README 只描述当前代码状态。

- 本地开发主入口：`python3 dev.py`
- 正式构建入口：`./build.sh`
- 后端主入口：`api/mgr/main.go`
- 前端主入口：`app/src/App.tsx`
- 主工作区组件：`app/src/components/Workspace.tsx`
- 版本号源头：`npm/package.json`
- cicy 状态根：`~/cicy-ai`
- 运行时日志根：`~/logs`
- Docker home 挂载根：`~/docker-homes`

## 目录

```text
cicy-code/
├── app/                    React + Vite 前端
├── api/                    Go 后端、WebTTY、ttyd 资产、manager 运行时
│   ├── mgr/                主程序与业务路由（~90 个 .go 文件）
│   ├── server/             WebTTY HTTP/WebSocket 服务
│   ├── webtty/             WebTTY 协议实现
│   ├── js/                 终端前端静态资源源码
│   ├── code-server-extension/  code-server / VS Code 扩展源码与 vsix
│   └── resources/          后端静态资源
├── npm/                    npm 发布包与启动器
├── skills/                 本地 Go skill 工具链 + 各类 SKILL.md
│   ├── cmd/                cicy-hosttools / cicy-skills / cicy-skillsd / stt / tts
│   ├── internal/           agentgen, bundle, config, hosttools, registry, voice
│   ├── cicy-code           bash CLI：调当前节点的 cicy-code 后端
│   ├── cicy-master         python CLI：管理 ~/Private/cicy-node.json 节点表
│   ├── cicy-todo/          每工作区 todo 列表 skill（前端 Todo tab 对端）
│   ├── skill-author/       生成 / 打包新 skill 的元 skill
│   ├── proxy_ssh/          ssh 子代理 skill 脚本
│   ├── us-spot-dev/, hk-spot-dev/, us-spot-proxy/  spot/dev 节点 provisioning
│   ├── cf/                 install-worker for Cloudflare Worker dev
│   ├── docker/             skill 测试用的 base/google-smoke Dockerfile
│   ├── configs/            示例配置
│   ├── migrations/         SKILL_REGISTRY / SKILL_DEV_GUIDE 等迁移文档
│   ├── legacy/             历史 markdown skill 资料
│   └── dist/               本地构建产物（cicy-hosttools 等二进制）
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

改了代码后必须重跑 `python3 dev.py` 重建（前端嵌入 + Go 编译都靠它），单跑 `go build ./mgr/` 会跳过嵌入步骤。

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

`--dev` 下，后端把非 API 请求反代到 `http://127.0.0.1:8022`。

### 3. npm 用户入口

```bash
npx cicy-code
```

npm 包在安装阶段按平台下载预编译二进制；启动器 `npm/bin/cicy-code.js`。

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

# 构建本地 skill 二进制（cicy-hosttools / cicy-skills / stt / tts ...）
cd skills && make build-local-binaries

# 安装 skill 命令到 ~/.local/bin
cd skills && make install-local-cli
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

主入口是 `api/mgr/main.go`。当前路由前缀包括：

- `/api/auth/*`
- `/api/proxy/*`、`/api/proxy-ssh/*`
- `/api/frp-server/*`（status / lifecycle / connections / clients / logs）
- `/api/panes`、`/api/tmux/*`
- `/api/chat/*`、`/api/poll`、`/api/stt`、`/api/tts`
- `/api/code-server/*`
- `/api/stats/*`、`/api/queue/*`
- `/api/agents/*`、`/api/workers/*`
- `/api/groups/*`、`/api/pair`
- `/api/nodes`、`/api/machines/*`
- `/api/runtime/*`
- `/api/shared-workspace/*`、`/api/collab/*`
- `/api/skills/*`、`/api/skill-market/*`、`/api/skill-config/*`
- `/api/settings/*`、`/api/system/*`、`/api/utils/*`
- `/api/openclaw/*`、`/api/ai-gateway/*`、`/api/providers/*`、`/api/cicy/*`
- `/api/im/*`、`/api/tg/*`、`/api/notify/*`、`/api/correctEnglish`、`/api/file-exists`
- `/api/todo/*`
- `/api/desktop/*`
- `/code/*`、`/ttyd/*`

关键文件：

- `api/mgr/setup.go`：环境检查、worker 初始化、code-server 启动、agent catalog
- `api/mgr/tmux.go`：pane 生命周期、tmux send、agent 启动脚本
- `api/mgr/chatbus.go`：聊天 WebSocket、poll 数据、client 间广播
- `api/mgr/runtime.go`：runtime instance/session/task/artifact
- `api/mgr/machines.go`：机器列表、同步、配置落盘
- `api/mgr/shared_workspace.go`、`api/mgr/collab.go`：文件式协作层
- `api/mgr/skills.go`、`api/mgr/skill_market*.go`：skill 注册表与市场 API
- `api/mgr/frp_server_handlers.go`：FRP server 抽屉式管理（脱敏，不暴露 config 原文）
- `api/mgr/gateway_reply_callback.go`、`gateway_reply_text.go`、`gateway_chat_history.go`、`gateway_cicy_tools.go`：跨 agent reply、结构化 reply/history 工具
- `api/mgr/gateway_inject_rules.go`：AI gateway 注入规则
- `api/mgr/ai_gateway_*.go`：OpenClaw / DeepSeek / Codex / Claude provider 适配
- `api/mgr/stt.go`、`api/mgr/tts.go`：语音转写 / 合成代理
- `api/mgr/todo.go`：`/api/todo/*`，对应 `cicy-todo` CLI 与前端 Todo tab
- `api/mgr/im*.go`、`im_telegram.go`、`im_wechat.go`、`im_reply_hook.go`：即时通讯桥接
- `api/mgr/proxy.go`、`api/mgr/openclaw_gateway.go`：代理与 AI 网关
- `api/mgr/ui.go`：内嵌 UI 或 Vite 反代
- `api/mgr/paths.go`：cicy 状态根 + 运行时路径常量

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
- `app/src/components/Workspace.tsx`：主工作区、agent stack、code-server、team panel、skills、Todo、WebSocket 状态
- `app/src/components/TodoPanel.tsx`：todo tab（与 `cicy-todo` skill 同源数据）
- `app/src/components/layout/FrpServerManagerDialog.tsx`：FRP server 抽屉
- `app/src/components/layout/ProxySshManagerDialog.tsx`：proxy_ssh / CiCy SSH 抽屉
- `app/src/components/audit/`、`app/src/components/dev/`、`app/src/components/im/`：审计 / dev 控制 / IM 桥
- `app/src/services/api.ts`：统一 API 客户端
- `app/src/config.ts`：前端版本号、API base、路径辅助函数

前端视图当前主要有：

- `desktop`
- `workspace`
- `audit`（部分接口仍未在 `api/mgr/main.go` 注册，属于残留面）

### skills/：本地 Go 工具链

`skills/` 现在是一个独立 Go module（`github.com/cicy-ai/cicy-skills`），构建出几个二进制：

| 二进制 | 用途 |
|---|---|
| `cicy-hosttools` | argv[0] 分发的 host 工具集合：`frp-server` / `frp-client` / `cicy-mihomo` / `cf-tunnel` / `cf` / `globalApiToken` / `email` / `aliyun-cli` / `ssh-list` / `agent-editor` / `tg` / `gpt` / `gemini-ask` / `gemini-vision` / `mysql-exec` / `cping` / `cicy-agent` 等 |
| `cicy-skills` | skill 安装 / 卸载 / 列表 CLI（前端 skill 市场后端） |
| `cicy-skillsd` | 常驻 daemon（marketplace 后台事件） |
| `stt` | 调 Cloudflare Workers AI Whisper 的语音转写 CLI |
| `tts` | 文本转语音 CLI |

- `internal/registry/`：所有 skill 用 `init()` 注册进同一张表，避免在 bundle / agentgen / 前端三处重复登记（见 `skills/migrations/SKILL_REGISTRY.md`）
- `internal/hosttools/`：每个 host 工具一个文件（`frp.go`、`mihomo.go`、`cf_tunnel.go`、`cf_api.go`、`google.go`、`ssh.go`、`email.go`、`aliyun.go`）
- `internal/bundle/`：聚合 SKILL.md / 资源到分发包
- `internal/agentgen/`：根据 SKILL.md 与 registry 生成 agent 的 skill 目录
- `internal/voice/`、`internal/voicecmd/`：语音管线公共逻辑
- `internal/skills/`：扫描已安装 skill

#### FRP server / client 配置硬编码

`frp-server` / `frp-client` 包装器现在只看 `~/cicy-ai/db/frps.toml` / `~/cicy-ai/db/frpc.toml`，不再支持 `FRP_SERVER_CONFIG` / `FRP_CLIENT_CONFIG` 环境变量（保留 `--config <path>` 显式覆写）。日志统一进 `~/logs/frps.log` / `~/logs/frpc.log`。

### npm 与扩展

- `npm/bin/cicy-code.js`：npm 启动器
- `npm/scripts/install.js`：按平台下载二进制
- `api/code-server-extension/`：发送文件路径给当前 agent 的扩展源码，配套 `cicy-code-server-bridge-0.0.4.vsix`

## worker 与 agent

pane 是核心运行单位，典型 ID 形态：

- `w-10001`
- `w-10001:main.0`

`builtinAgents` catalog（来自 `api/mgr/setup.go`）：

- `claude` — Claude
- `codex` — Codex
- `opencode` — OpenCode
- `cursor` — Cursor
- `kiro-cli` — Kiro CLI
- `copilot` — GitHub Copilot
- `openclaw` — OpenClaw
- `hermes` — Hermes Agent
- `cicy-claude` — CiCy

**非 lab 模式**（默认）只允许 `claude` / `codex` / `opencode`（`nonLabAllowedBuiltinAgents`），lab 模式才会暴露整套 catalog。

当前事实：

- `python3 dev.py` 默认 dev agent 是 `claude`
- 首个内置 worker 是 `w-10001`（`primaryWorkerPaneID = w-10001:main.0`）
- `--agents=all` 时按 builtinAgents 顺序从端口 `10001` 起递增分配
- 前端创建对话框以非 lab 允许列表为准

`App.tsx` 仍保留前端兜底：登录后若 `w-10001` 不存在，会尝试创建一个 `hermes` pane，是 UI 侧补救逻辑，不是主启动链路默认值。

## 配置与路径

### cicy 状态根

`~/cicy-ai` 仅保留 cicy 自身状态和内置 skill：

- `~/cicy-ai/global.json`
- `~/cicy-ai/db/`（敏感配置：`frps.toml`、`frpc.toml`、`mihomo.yaml`、`google.json`、`cf.json` 等，均 chmod 600）
- `~/cicy-ai/.cicy/`
- `~/cicy-ai/workers/`
- `~/cicy-ai/projects/`
- `~/cicy-ai/skills/`（含 `cicy-skills/skill-author/`）
- `~/cicy-ai/shared-workspace/`
- `~/cicy-ai/cicy-node.json`

### 运行时分离的根目录

`commit 7b9cffa` 之后，运行时产物搬出 `~/cicy-ai`，分开管理：

- **`~/logs/`** — 所有运行时日志（skills install、code-server、tmux send/trace、agent install 日志、`frps.log`、translate-cache 等）
- **`~/docker-homes/`** — Docker home mount 目标（之前是 `~/cicy-ai/docker-homes`）

### 机器注册表：仍有两套默认路径

1. **后端 runtime 默认文件**：`~/cicy-ai/cicy-node.json`
   - 来自 `api/mgr/paths.go`
   - `api/mgr/machines.go` 读写
2. **skills CLI 默认文件**：`~/Private/cicy-node.json`
   - `skills/cicy-code` 与 `skills/cicy-master` 默认值
   - 可用 `CICY_NODES_FILE` 改写

如果希望 skills 与后端同一份：

```bash
export CICY_NODES_FILE=~/cicy-ai/cicy-node.json
```

### 常用环境变量

- `PORT`、`CS_PORT`、`SQLITE_PATH`
- `CICY_API_TOKEN`、`CICY_API_KEY`、`CICY_API_URL`、`CICY_ANTHROPIC_URL`
- `CICY_DEFAULT_OPENCODE_MODEL`、`CICY_DEFAULT_CLAUDE_MODEL`、`CICY_CODEX_MODEL`、`CICY_OPENCLAW_MODEL`
- `CICY_RUNTIME_KIND`、`CICY_RUNTIME_MODE`、`CICY_RUNTIME_API_ONLY`
- `CICY_PUBLIC_URL`、`CICY_MASTER_URL`、`CICY_MASTER_TOKEN`
- `CICY_TEAM_TOKEN`、`CICY_TEAMCENTER_URL`

其中：

- `CICY_RUNTIME_MODE=api-only` 或 `CICY_RUNTIME_API_ONLY=1` 会让部分 tmux/desktop 接口返回 `not_supported_in_api_only_runtime`
- `CICY_MASTER_URL` / `CICY_MASTER_TOKEN` / `CICY_PUBLIC_URL` 会触发 managed runtime 的自注册路径

## skill 市场

`/api/skill-market/*` 是 UI 侧的 skill 安装/卸载入口；`/api/skill-config/*` 暴露每个 skill 的状态卡。

注册侧靠 `skills/internal/registry/`，每个 skill 在自己的 `init()` 里 `Register(Skill{...})`，bundle、agent SKILL.md 生成、前端列表都从这一张表读。生产链路（`feat(skill-market): wire production install/uninstall flow`）：

1. UI 选 skill → `/api/skill-market/install`
2. 后端调 `cicy-skills install <name>`
3. `cicy-skills` 把所需文件 link 到 `~/.local/bin/`、`~/.claude/skills/<name>/` 等
4. agent 启动时 `agentgen` 按当前已安装的 skill 列表生成 SKILL.md

已落地的 skill 示例：`google`（多用户 OAuth）、`email`（Resend）、`aliyun-cli`、`frp-server` / `frp-client`、`cf` / `cf-tunnel`、`cicy-mihomo`、`proxy_ssh`（重命名 CiCy SSH）、`ssh-list`、`cicy-todo`、`skill-author`。

## managed runtime / Docker

仓库仍保留完整 Docker 与 managed runtime 能力：

- `api/Dockerfile.runtime`
- `api/Dockerfile.runtime.base`
- `./build.sh docker <tag>`
- `./build.sh docker-base <tag>`
- `python3 dev.py --docker`
- `python3 dev.py --dockerBuild`
- `python3 dev.py --cloudRun`
- `python3 dev.py --cloudRunList`

`python3 dev.py --dockerBuild` 当前还会先做 CDN 资产构建与 COS 上传，再 push runtime 镜像，并更新 `~/cicy-ai/global.json -> images.runtime*`。

Docker 容器的 host home 现在挂到 `~/docker-homes/<container-name>`（默认值，见 `dev.py:1518`）。

## code-server 扩展

`api/code-server-extension` 提供两个用户动作：

- 资源管理器右键：发送路径给当前 agent
- 编辑器右键：发送当前文档 / 选区给当前 agent

它通过：

- `POST /api/code-server/send-path`

与当前页面所属的 `cicy-code` 后端通信。

## 已知差异

当前代码与历史文档相比，至少有这几处需要按代码为准：

- 状态根分裂：`~/cicy-ai`（状态 + skills）、`~/logs`（运行时日志）、`~/docker-homes`（Docker home mount）
- builtinAgents 是 9 个；非 lab 模式只暴露 `claude / codex / opencode` 这 3 个
- 默认 dev agent 是 `claude`，不是一组固定的四 agent 启动模版
- 机器配置文件后端 (`~/cicy-ai/cicy-node.json`) 与 skills (`~/Private/cicy-node.json`) 默认路径仍不一致
- FRP server / client 配置已硬编码到 `~/cicy-ai/db/`；老的 `FRP_*_CONFIG` 环境变量已删
- skills 是独立 Go module，构建 / 安装走 `skills/Makefile`，不是 `api/` 那条链
- 前端仍保留 audit 页面与 `/api/audit/*` 调用，但 `api/mgr/main.go` 没有注册这些路由

## 当前建议工作流

1. 本地开发先用 `python3 dev.py`
2. 需要前端热更新时，再开一个 `cd app && npm run dev`
3. 改 `api/js` 后执行 `cd api && make asset`
4. 改 `skills/`（hosttools / SKILL.md）后 `cd skills && make build-local-binaries`
5. 正式构建走 `./build.sh`
6. 节点注册表管理走 `skills/cicy-master`
7. 节点 API 调用走 `skills/cicy-code`

## 许可证

MIT
