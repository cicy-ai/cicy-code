# cicy-code

`cicy-code` 是一个本地优先的多 agent 开发工作区:tmux worker + WebTTY 终端 + React 工作区 + code-server 代理 + AI 网关 + skill 市场,收在同一个仓库里,通过 npm(`npx cicy-code`)分发单二进制。

> 这份 README 只描述**当前**代码状态。约定俗成的口径不算数,以仓库为准。

## 仓库结构

```text
cicy-code/
├── app/          React + Vite 前端(入口 app/src/App.tsx,主界面 Workspace.tsx)
├── api/          Go 后端 + 终端层
│   ├── mgr/      主程序与业务路由(main.go 注册路由与启动)
│   ├── server/   WebTTY HTTP/WebSocket 服务
│   ├── webtty/   WebTTY 协议
│   ├── js/       浏览器端终端资产源码(改后需 `cd api && make asset`)
│   ├── skillcmd/ `cicy-code skill <…>` 子命令(安装/卸载/列表)
│   └── resources/ 后端静态资源
├── npm/          npm 发布包与启动器(bin/cicy-code.js + publish-all.sh + install.sh)
├── scripts/      构建/发版/测试脚本(build-image.sh / fresh-instance.sh / sync-version.py)
├── workers/      Cloudflare Workers(见下「Cloudflare Workers」)
├── build.sh      标准构建入口(唯一正确的构建/测试方式)
├── dev.py        本地开发入口
├── versions.json base 镜像 / app 资产 / ttyd 版本
└── Makefile      常用别名
```

主入口:
- 本地开发 `python3 dev.py`
- 构建/测试 `./build.sh`
- 后端 `api/mgr/main.go`,前端 `app/src/App.tsx`,主工作区 `app/src/components/Workspace.tsx`
- 版本号源头 `npm/package.json`
- cicy 状态根 `~/cicy-ai`

## 构建与测试(重要)

**必须走 `./build.sh`,不要直接 `go build` / `go test`。** `build.sh` 会在编译前准备内嵌资源;裸 `go build ./mgr/` 会跳过这些步骤、和真实流水线不等价,还会在仓库根丢一个 `./mgr` 产物。

`./build.sh` 依次:
1. 同步版本号
2. 复制 `api/resources` → `api/mgr/resources`
3. 复制 `.tmux.conf`、`.cicy_tmux.conf` → `api/mgr/`
4. 构建 `app/dist`(除非 `SKIP_NPM=1`)
5. 刷新 ttyd 内嵌资产(除非 `SKIP_TTYD_ASSET=1`)
6. 复制 `app/dist` → `api/mgr/ui`(前端嵌进二进制)
7. 编译 `api/mgr` → `api/cicy-code`

```bash
./build.sh build [os arch]     # 当前/指定平台构建
./build.sh all                 # 全平台
./build.sh test-go             # Go 测试(稳定入口)
./build.sh test-go ./mgr/...   # 单包测试
./build.sh docker <tag>        # runtime 镜像
./build.sh docker-base <tag>   # base 镜像
```

## 本地开发

```bash
cd ~/projects/cicy-code
python3 dev.py                 # 构建 + 后台起 api/cicy-code --dev --public,tail 日志
```

默认端口:API `8008`、Vite `8022`。前端源码改动:`dev.py` 默认 `SKIP_NPM=1` 复用 `app/dist`,要生效先 `cd app && npm run build`,再 `python3 dev.py`。

前后端分开调试:
```bash
cd app && npm ci && npm run dev          # 前端 HMR(:8022)
cd api && go run ./mgr/ --dev --public   # 后端把非 API 请求反代到 :8022
```
（`--dev` 仅用于本地起服务;正式产物仍必须走 `./build.sh`。）

## 发版

版本号统一由 `scripts/sync-version.py` 写入这几处:`npm/package.json`、`app/package.json`、`app/package-lock.json`、`app/src/config.ts`、`api/mgr/main.go`、`.cicy_tmux.conf`。

**正式发版(npm + R2,走 CI):**
```bash
python3 scripts/sync-version.py --set 2.3.NN   # 1) bump 所有版本文件
cd app && npm run build                        # 2) 前端 build(把 config.version 烘进 dist)
cd .. && git add <你的文件> && git commit ...   # 3) 提交(共享 checkout:只 add 自己的文件)
git push origin main
git tag v2.3.NN && git push origin v2.3.NN      # 4) 打 tag → 触发 .github/workflows/release.yml
```
CI 从 **tag** 构建各平台二进制、发布 npm 五连包 + 上传 R2 资产。

**只更新本地 Mac 桌面(不发 npm,更快):** 桌面跑 `~/.local/bin/cicy-code`(symlink → 版本化二进制)。流程见项目内约定(每次必 bump 版本 → `./build.sh build darwin amd64` → 拷成版本化名 → 原子换 symlink → 同步 `~/.local/bin/.cicy-localbin.json`)。

> 前端改动务必在 bump 之后 `npm run build`,否则 `SKIP_NPM=1` 会复用旧 dist,`membership-version` 显示的还是旧号。

## npm 分发:per-platform optionalDependencies

主包 `cicy-code` 只是个 launcher(几 KB),二进制按平台拆成子包,npm 按 `os`/`cpu` **只装匹配当前机器的那个**:

```
cicy-code                  launcher(bin/cicy-code.js → require.resolve 平台子包并 exec)
├─ cicy-code-darwin-arm64
├─ cicy-code-darwin-x64
├─ cicy-code-linux-x64
└─ cicy-code-linux-arm64
```

安装:
```bash
npm install -g cicy-code        # 海外
npm install -g cicy-code --registry=https://registry.npmmirror.com   # 国内(缓存二进制,不走 GitHub)
npx cicy-code                   # 临时跑一次
```
`npm/publish-all.sh <npm-version> [gh-tag]` 从对应 GitHub release 资产打包发布 4 个平台子包 + 主包(子包先发、主包后发,保证 optionalDependencies 可解析)。CI 已自动化这条链。

## 架构

**后端 `api/mgr`** — `main.go` 注册全部路由与启动。关键文件:
- `setup.go`:环境检查、内置 worker、code-server 启动、开机 seed(dotfiles / memory 模板 / 内置 skill)
- `tmux.go`:pane 生命周期、tmux send、agent 启动脚本(boot.sh)、fork
- `chatbus.go`:聊天 WebSocket、poll、client 广播
- `agent_memory_template.go`:agent 记忆模板组装(见下)
- `newtab.go`:`/newtab` 单源浏览器起始页(chrome + electron tab 共用)
- `skills.go` / `skill_market*.go`:skill 市场 API(`/api/skill-market/*`)
- `ai_gateway_*.go` / `providers*.go`:各 provider 适配与 AI 网关
- `runtime.go` / `machines.go`:managed runtime、机器表
- `ui.go`:内嵌 UI 或 Vite 反代;`paths.go`:状态根与路径常量

**终端层** — `api/server`(HTTP/WS)、`api/webtty`(协议)、`api/js`(浏览器端资产,改后 `make asset`)。

**前端 `app`** — `App.tsx` 路由;`Workspace.tsx` 主工作区(agent stack / code-server / 团队面板 / skill 市场 / Todo / WS 状态);`services/api.ts` 统一 API 客户端;`config.ts` 版本号与 API base。

**Cloudflare Workers `workers/`**(独立 `wrangler deploy`,与主程序解耦):
- `oauth-flow` → `oauth-flow.cicy-ai.com`:Google OAuth 授权码**无状态中继**(只暂存 code,TTL 10min、一次性;永不接触 client_secret / token)
- `skills-registry` → `skills.cicy-ai.com`:**public skill 注册表 API**(`/v1/skills*`、`/v1/admin/publish`),`skill install` 打的就是它

## agent / pane 与记忆

pane 是核心运行单位,ID 形如 `w-1001` / `w-1001:main.0`。内置 agent(`api/mgr/setup.go`):`claude` / `codex` / `opencode` / `kiro-cli` / `cicy`(cicy 为内置 headless 会话)等;**非 lab 模式默认只暴露 `claude` / `codex` / `opencode`**。默认 dev agent 是 `claude`,首个内置 worker 是 `w-1001`。

**记忆/guidance 文件**:每个 agent 在其 workspace 拥有一份自包含的原生 guidance 文件(`CLAUDE.md` / `AGENTS.md` / `.kiro/steering/memory.md`)。内容在**创建时**由分层模板**组装并逐字写入**——`global`(`~/cicy-ai/memory/global.md`)+ 可选 project(`projects/<slug>.md`)+ 可选 role(`agents/<slug>/`)。**没有继承、没有网关注入**,CLI 直接原生读取该文件(`agent_memory_template.go` 组装,`tmux.go` `writeAgentGuidanceFile` 落盘)。打包默认 seed 在 `api/mgr/embed/memory-seed/`(改这里改的是发给所有人的默认)。`cicy-code reseed-memory` 可按当前模板重新生成(会备份、保留自定义标记以下内容)。

## skill 生态

- 装/卸/列表:`cicy-code skill install|remove|installed <name>`(`api/skillcmd/`)
- public 注册表:`workers/skills-registry`(`skills.cicy-ai.com`),public skill 源码在独立仓库 `cicy-skills`,tag 触发 `/v1/admin/publish`
- 规范:三类 skill(public / private / team)的落盘位置与发布约定见 `cicy-skill-spec`
- 前端市场入口 `/api/skill-market/*`,红点提示 public skill 有更新

## 配置与路径

cicy 状态根 `~/cicy-ai`:
- `~/cicy-ai/global.json`:全局配置 + api_token
- `~/cicy-ai/db/`:敏感配置(`email.json`、`cf.json`、`frps.toml`、`mihomo.yaml` 等,chmod 600)
- `~/cicy-ai/memory/`:记忆模板(`global.md`、`projects/`、`agents/`)
- `~/cicy-ai/workers/`:各 agent workspace
- `~/cicy-ai/skills/`:已装 skill(public 扁平 / `private/` / `team/<team>/`)
- `~/cicy-ai/assets/`:上传资源目录

## 许可证

Apache-2.0
