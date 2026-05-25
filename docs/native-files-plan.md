# Native Files (替换 code-server) 计划

## 背景

当前 cicy-code 通过反代 `npm i -g code-server@latest` 提供"在浏览器看/改文件"的能力，配套一个 VSIX 扩展和注入脚本把"右键发送路径给当前 agent"挂到 vscode 上下文菜单。

存在的问题：

- code-server 体积 ~130MB，启动慢（冷启 5-10s），全局 npm 安装拖慢镜像构建
- vscode 实际只用 ~5% 能力：文件树、文件 viewer、保存、跳转 line:col、右键发送路径
- LSP / 调试器 / git 集成 / 扩展市场 / 命令面板 / settings UI / 终端面板 全部没用上
- 升级 code-server 经常带兼容噪声（locale cookie、service worker、auxiliary bar 这些）
- VSIX 扩展每次升级要重打、重装，链路长

**结论**：把 code-server 换成自研轻量 file 模块。

## 目标

- 把 cicy-code 对 code-server 的依赖整体替换成：Go 后端 fs API + 前端 CodeMirror 6 编辑器 + 自写 file explorer
- 保持现有"右键发送给当前 agent"交互不变
- 体积、首屏、内存、启动时间全面优于 code-server
- 不引入新的运行时依赖（不要 Node 进程，复用 cicy-code 后端 Go 进程）
- 提供 env switch，过渡期 code-server / native 并存

## 非目标 (v1 不做)

- LSP / 智能补全 / 跳转定义 — 后续可接 codemirror-languageserver
- 调试器 / 断点
- git 图形界面 / 提交 UI — 走 tmux 即可
- vscode 扩展市场
- 远程开发协议（SSH / WSL）
- 多人实时协作编辑（OT/CRDT）— 现有 shared-workspace 是另一条路
- 命令面板 / 设置 UI

## 范围分期

### MVP（必须）

1. 后端 fs API：`list / read / write / stat`，路径白名单限制在 workspace folder 内
2. 前端 file explorer：左侧目录树，懒加载，虚拟列表
3. CodeMirror 6 单文件编辑器：打开 / 编辑 / Cmd+S 保存
4. 右键菜单 "发送路径给当前 agent"，复用现有 `/api/code-server/send-path`
5. Workspace 加新 tab 挂载新视图，env `CICY_USE_NATIVE_FILES=1` 切换

### v1（替换前必须）

6. 多 tab（同时打开多个文件）
7. Cmd+P 文件名 fuzzy 搜索（后端走 `fd`，缺则 fallback `find`）
8. Cmd+Shift+F 全文搜索（后端走 `rg`）
9. Diff 视图：跟 git HEAD 比，`@codemirror/merge` 渲染
10. 语法高亮：常见 10+ 语言按需 import
11. 图片预览（`.png/.jpg/.svg/.webp/.gif`）
12. 二进制文件保护（>5MB 的 binary 拒绝在编辑器打开，提示走下载）
13. fsnotify 文件 watch + WS 推送，agent 改了文件 UI 自动刷新

### v2（可选增量）

14. 跳转到 line:col（URL hash + 外部命令触发）
15. 简单 git 状态指示器（文件树上的 M/A/D 标记）
16. vscode 风格快捷键（`@replit/codemirror-vscode-keymap`）
17. 文件草稿 localStorage 持久化（防刷新丢失未保存内容）
18. hex 视图（小型二进制）

### 替换阶段

19. v1 跑顺一两周，删除 code-server 相关代码 / 资源 / 安装流程
20. 删 `api/code-server-extension/`、`api/resources/cicy-code-server-bridge-*.vsix`
21. 删 `setup.go` 里 ~150 行 code-server 安装/配置代码
22. 删 `proxy.go` 里 code-server 反代分支
23. npm 全局不再装 `code-server`

## 架构

```
┌─────────────────────────────────────────────────────────┐
│ Browser (app/src/components/files/)                     │
│                                                         │
│  <FileExplorer>     <EditorTabs>                       │
│    │                   │                                │
│    │ HTTP              │ HTTP                           │
│    ▼                   ▼                                │
│  /api/fs/list        /api/fs/read                       │
│                      /api/fs/write                      │
│                                                         │
│  WS /api/fs/watch  ←──── 推送变更                      │
└──────┬──────────────────────────────────────────────────┘
       │
┌──────▼──────────────────────────────────────────────────┐
│ Go backend (api/mgr/files.go)                           │
│                                                         │
│  路径白名单 (resolveSafePath)                          │
│    │                                                    │
│    ├─ os.ReadDir  ──── 列目录（单层）                  │
│    ├─ os.ReadFile ──── 读文件（带 size cap）           │
│    ├─ os.WriteFile ─── 写文件（mtime 校验）            │
│    ├─ os.Stat                                           │
│    ├─ exec(fd)    ──── 文件名搜索                       │
│    ├─ exec(rg)    ──── 全文搜索                         │
│    ├─ exec(git diff) ─ diff vs HEAD                     │
│    └─ fsnotify    ──── 变更 watch + WS 广播            │
└─────────────────────────────────────────────────────────┘
```

**所有 fs 操作都收敛到一个 `resolveSafePath(workspace, requestPath) -> abs, error`**：

1. `requestPath` 必须是相对 workspace 的相对路径（拒绝绝对路径）
2. `filepath.Clean` 后断言不含 `..` 跳出 workspace
3. 解 symlink，确认 link target 也在 workspace 内
4. workspace 来自 agent 的 `currentWorkspaceFolder`，由后端从 agent_config 读取

## 后端 API 详细规格

所有路由前缀 `/api/fs/*`，鉴权走现有 `wa(...)` 中间件，认 `Authorization: Bearer <token>` 或 cookie。

### `GET /api/fs/list`

```
query:
  path     相对 workspace 的目录路径，"" 或 "." 表示根
  hidden   "1" 显示隐藏文件，默认 0

resp 200:
  {
    "path": "src/components",
    "entries": [
      {"name":"App.tsx","is_dir":false,"size":1234,"mtime":1748123456,"mode":"-rw-r--r--"},
      {"name":"layout","is_dir":true,"size":0,"mtime":...,"mode":"drwxr-xr-x"}
    ]
  }
```

- 默认过滤 `.git/`、`node_modules/`、`dist/`、`build/`、`target/`、`.cache/`
- `.gitignore` 默认遵守（v1 简化：写死黑名单，v2 接 `go-git/go-git`）
- 隐藏文件默认隐藏（开头是 `.`），`hidden=1` 显示
- 排序：目录优先，然后按名字

### `GET /api/fs/read`

```
query:
  path     相对 workspace 的文件路径

resp 200:
  Content-Type: text/plain; charset=utf-8     # 文本文件
  Content-Type: application/octet-stream      # 二进制（base64）
  X-File-Mtime: 1748123456
  X-File-Size: 1234
  X-File-Mime: text/x-go
  body: 文件内容

resp 413: 文件超过 5MB（默认上限）
resp 404: 不存在
resp 403: 不在白名单内
```

- mime 检测走 `net/http.DetectContentType`（前 512 字节嗅探）
- 文本判定：mime 以 `text/` 开头 或 嗅探返回 `application/json/javascript/xml`
- 二进制 + 已知图片格式：返 base64，前端走 `<img src="data:...">`
- 二进制 + 未知：拒绝在编辑器打开，前端提示"二进制文件"

### `POST /api/fs/write`

```
body:
  {
    "path": "src/App.tsx",
    "content": "...",
    "expected_mtime": 1748123456    // 可选，防覆盖冲突
  }

resp 200: {"mtime": 1748123500, "size": 1245}
resp 409: {"error":"mtime_mismatch","actual_mtime":1748123480}
resp 413: 写入超过 5MB
resp 403: 不在白名单
```

- 写入前 stat 拿当前 mtime，如果跟 `expected_mtime` 不一致返 409
- 前端收 409 时弹 "磁盘上的版本已变更，是否覆盖？" 让用户选
- 原子写：先写 `<path>.cicy-tmp`，再 rename，避免 partial write
- v1 不做备份；如需保留旧版本走 git

### `GET /api/fs/stat`

```
query:
  path

resp 200:
  {"name":"App.tsx","is_dir":false,"size":1234,"mtime":...,"mime":"text/x-tsx"}
```

### `GET /api/fs/search`

文件名 fuzzy 搜索。

```
query:
  q        搜索词，空则返空
  dir      子目录限定，默认 workspace 根
  limit    默认 100，max 500

resp 200:
  {
    "matches": [
      {"path":"src/components/App.tsx","score":0.95},
      ...
    ],
    "elapsed_ms": 23
  }
```

- 优先用 `fd --type f --hidden --no-ignore-vcs <regex> <dir>`
- `fd` 不存在时 fallback `find <dir> -type f -iname '*<q>*'`
- 对 `fd` 输出做轻量 fuzzy 评分（命中位置 + 长度）
- 200ms 超时，超时返已收集结果

### `GET /api/fs/grep`

全文搜索。

```
query:
  q          搜索词
  dir        子目录限定
  case       "1" 区分大小写
  regex      "1" 当作正则
  limit      默认 200 行

resp 200:
  {
    "matches": [
      {"path":"src/App.tsx","line":12,"col":5,"text":"...匹配上下文..."},
      ...
    ],
    "elapsed_ms": 87
  }
```

- 必须用 `rg --json`（ripgrep）解析其 JSON 输出
- 没装 rg 直接返 503，前端提示"主机未安装 ripgrep"
- 限制总行数 200，避免爆 UI

### `GET /api/fs/diff`

```
query:
  path
  base       "head" | "index" | "mtime"，默认 "head"

resp 200:
  text/plain unified diff   或
  {"a":"...content...","b":"...content..."}    用于前端二维 diff
```

- `base=head`：`git diff HEAD -- <path>`
- `base=index`：`git diff -- <path>`（vs index）
- `base=mtime`：返 `{a: 磁盘上内容, b: 前端发来的 buffer}`，前端自己做 diff
- 不在 git repo 内或非跟踪文件时，返 200 空 body

### `WS /api/fs/watch`

```
client → server:
  {"type":"subscribe","path":"src/components"}
  {"type":"unsubscribe","path":"src/components"}

server → client:
  {"type":"created","path":"src/components/Foo.tsx"}
  {"type":"modified","path":"src/components/App.tsx","mtime":...}
  {"type":"deleted","path":"src/components/Bar.tsx"}
  {"type":"renamed","old":"a.tsx","new":"b.tsx"}
```

- 后端用 `fsnotify.NewWatcher()`，按订阅路径维护 watcher list
- agent 写文件触发的事件也会推过来，UI 自动刷新打开的 buffer 状态（提示"已外部修改"）
- 心跳：30s ping，60s 无 pong 断开

### `POST /api/fs/send-path`

复用现有 `/api/code-server/send-path` 路由。新地址作为别名注册（保持向后兼容直到 code-server 删除）。

## 前端结构

新增 `app/src/components/files/` 目录：

```
files/
├── FilesView.tsx         顶层容器，三栏布局
├── explorer/
│   ├── FileExplorer.tsx  左侧目录树
│   ├── TreeNode.tsx      单个节点
│   ├── tree-store.ts     zustand store: 展开状态 + 子节点 cache
│   └── virtual-tree.ts   扁平化 + react-virtual 接入
├── editor/
│   ├── EditorTabs.tsx    顶部 tab 头
│   ├── CodeEditor.tsx    CodeMirror 6 单文件
│   ├── DiffView.tsx      @codemirror/merge
│   ├── ImagePreview.tsx
│   ├── BinaryPlaceholder.tsx
│   ├── language.ts       按文件扩展名 dynamic import 语言包
│   ├── theme.ts          主题(配 cicy-code 现有 dark)
│   └── editor-store.ts   打开的 tab + dirty 状态
├── search/
│   ├── QuickOpen.tsx     Cmd+P
│   ├── GlobalSearch.tsx  Cmd+Shift+F
│   └── search-api.ts
├── context-menu/
│   └── FileContextMenu.tsx  右键 → 发送给 agent / 显示路径 / 复制路径
├── api.ts                所有 /api/fs/* 客户端封装
├── path-utils.ts         workspace-relative 路径处理
└── types.ts              FsEntry / FileBuffer / EditorTab
```

### FileExplorer 性能要点

- **不递归 DOM**：内部用扁平化数组 + `level` 字段
- **虚拟列表**：`@tanstack/react-virtual`，可视范围内的节点才渲染
- **懒加载**：点目录展开才 `fetch /api/fs/list`，结果写 `tree-store` 子节点 cache
- **LRU 淘汰**：cache 超过 100 个目录或 5000 个 entry，按访问时间淘汰
- **Hover prefetch**：鼠标停在目录上 200ms，后台 `fetch` 一次（结果 cache 起来）
- **变更 watch**：当前可视范围内的目录自动 subscribe，外部改动直接刷新
- **取消请求**：用户快速切目录时 `AbortController` 取消前一个

### Editor 关键决策

- **编辑器内核**：CodeMirror 6
  - `@uiw/react-codemirror` 做 React 包装
  - 按需 dynamic import `@codemirror/lang-*`，不打到主 bundle
  - 主题用 `@uiw/codemirror-themes-all` 选 vscode-dark
- **多 tab**：tab 状态在 `editor-store` (zustand)，切换 tab 不卸载编辑器实例（`EditorState` 切换即可）
- **dirty 标记**：buffer 跟服务器 mtime 比较，不一致显示 ●
- **保存**：Cmd+S → `POST /api/fs/write`，带 expected_mtime
- **冲突处理**：409 时弹 dialog "磁盘已变更，是否覆盖 / 重载 / 取消"
- **大文件**：>1MB 自动只读 + 关闭高亮（避免 CM6 解析卡顿）
- **行尾**：UTF-8 默认；其它编码 v1 不处理

### Diff 视图

- 入口：文件 tab 上一个图标 "Show diff vs HEAD"
- 实现：`MergeView` from `@codemirror/merge`，左 = `git show HEAD:<path>`，右 = 当前 buffer
- 切到 diff 模式时打开右侧只读编辑器；切回正常模式恢复
- 没 git 或非跟踪文件：按钮 disabled

### 右键菜单

- 文件树节点上：发送给当前 agent / 复制绝对路径 / 复制相对路径 / 在 tmux 里 cd 进去
- 编辑器选区上：发送选区给 agent
- 实现：自写 React 组件 + `pointerdown` 关闭，不用浏览器 contextmenu

## 接入 Workspace

`app/src/components/Workspace.tsx` 当前用 tab 模式管理 code-server / team panel / skills / Todo 等视图。新增一个：

```ts
{ id: "files", label: "Files", view: <FilesView /> }
```

env switch：

- `CICY_USE_NATIVE_FILES=1`（默认）显示 `Files` tab
- `CICY_USE_NATIVE_FILES=0` 退化到 `Code` tab（code-server）
- 两者**可并存**：v1 阶段两个 tab 都在，让用户对照体验
- 后端通过 `/api/settings/runtime` 暴露这个 flag 给前端

## 鉴权与安全

### 路径白名单

```go
func resolveSafePath(workspace, requested string) (string, error) {
    if filepath.IsAbs(requested) {
        return "", errAbsoluteForbidden
    }
    cleaned := filepath.Clean(filepath.Join(workspace, requested))
    rel, err := filepath.Rel(workspace, cleaned)
    if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
        return "", errOutsideWorkspace
    }
    // resolve symlink, ensure target also in workspace
    real, err := filepath.EvalSymlinks(cleaned)
    if err == nil {
        relReal, _ := filepath.Rel(workspace, real)
        if strings.HasPrefix(relReal, "..") {
            return "", errSymlinkEscape
        }
    }
    return cleaned, nil
}
```

所有 fs handler 第一步都过这个函数。

### Workspace 来源

- 所有 fs 路由的请求都必须带 `agent_id`(short 形态如 `w-10001` 或完整 `w-10001:main.0` 都接受)
- 后端按 `agent_id` 找该 agent 绑定的 workspace folder,fs 操作严格限制在该 workspace 内
- 缺 `agent_id` → 400 `missing_agent_id`
- agent 不存在或没绑定 workspace → 404 `agent_workspace_unavailable`

### 命名约定 (重要)

**全代码、全 API、全文档,只有一个概念:agent。`pane` 这个词在新代码、新文档里不出现。**

- 路由 query 参数:`?agent_id=w-10001`
- Go 变量、字段、错误码:`agentID` / `agent_id` / `agent_not_found` / `agent_workspace_unavailable`
- 前端类型:`interface FsRequest { agentId: string; path: string }`
- 注释、日志、错误信息一律 `agent` 不写 `pane`

fs 层暴露一个内部 helper:

```go
func agentWorkspace(agentID string) (string, error)
```

所有 fs handler 从这一个入口拿 workspace,不直接调任何带 `pane` 字眼的老函数。

### 大小上限

- 单文件 read: 5MB（可配 `CICY_FS_READ_MAX_BYTES`）
- 单文件 write: 5MB（可配 `CICY_FS_WRITE_MAX_BYTES`）
- list 单目录条数：上限 5000，超过截断并标记 `truncated:true`
- search 结果：100 条
- grep 结果：200 行

### 写权限

- 不允许写到 `.git/`、`node_modules/`、`.cicy-tmp` 后缀
- 不允许 chmod / chown / 改 symlink target

### 黑盒文件

- 不允许 read / write socket / device / fifo
- `os.Stat` 后检查 `Mode().IsRegular()` 或 `IsDir()`，其它一律拒绝

## 依赖

### 前端新增 (gzip 估算)

```
@uiw/react-codemirror              ~50KB
@codemirror/state @codemirror/view ~40KB
@codemirror/lang-* (按需,各 5-30KB)
@codemirror/merge                  ~30KB
@uiw/codemirror-themes-all         按需,各 ~3KB
@tanstack/react-virtual            ~10KB
zustand                            ~3KB
@replit/codemirror-vscode-keymap   ~5KB (v2)
```

主 bundle 增加 ~150KB gzip，按需加载部分另算。

### 后端新增

```go
github.com/fsnotify/fsnotify   // 文件 watch
```

`fd` 和 `rg` 用 `os/exec` 调用，不引 Go 依赖。Cicy-code 主机镜像里已经有 `rg`，需要确认下 `fd` 是否预装。如缺，加进 setup.go 安装清单。

## 工作量与里程碑

| 阶段 | 内容 | 估时 |
|---|---|---|
| Phase 0 | 文档（本文） | 0.5h |
| Phase 1 | 后端 fs API：list/read/write/stat + 路径白名单 + 单元测试 | 0.5d |
| Phase 2 | 前端 file explorer：虚拟列表 + 懒加载 + 右键菜单 | 0.5d |
| Phase 3 | 前端 editor：CM6 单 tab + 高亮 + Cmd+S 保存 | 0.5d |
| Phase 4 | Workspace 挂 tab + env switch + 跑通 MVP | 0.3d |
| Phase 5 | 多 tab + Cmd+P 文件名搜索 + Cmd+Shift+F 全文搜索 | 0.5d |
| Phase 6 | Diff 视图 + 图片预览 + watch | 0.5d |
| Phase 7 | 联调 / 性能调优 / 边角 bug | 0.5d |
| Phase 8 | 删 code-server 相关代码（替换稳了之后） | 0.3d |

总约 **3.5 ~ 4 工作日**。

## 风险与对策

| 风险 | 严重度 | 缓解 |
|---|---|---|
| CM6 在大文件（>1MB）卡 | 中 | >1MB 自动只读 + 关高亮；>5MB 拒绝 |
| 路径越权 | **高** | 集中走 `resolveSafePath`，加单测覆盖 `..`/symlink/绝对路径 |
| 写入冲突丢数据 | 中 | mtime 校验 + 原子写 + 409 让用户决策 |
| 主机没 fd 或 rg | 中 | fd 缺回退 find；rg 缺直接 503 提示 |
| 文件名/内容含中文 / 二进制 | 中 | 后端 UTF-8 强制；非 UTF-8 文本 v1 不支持，走二进制路径 |
| fsnotify 在大目录性能差 | 中 | 只 subscribe 当前可视目录，不递归全 workspace |
| 用户依赖 vscode 快捷键习惯 | 中 | 装 vscode keymap 包；保留 code-server tab 一段时间 |
| node_modules 误展开 | 高 | 默认黑名单 + 用户主动点"显示隐藏"才展开 |
| 二进制大文件读爆内存 | 高 | size cap 在 stream 前检查；超大直接拒绝 |
| 编辑器和 agent 同时写同一文件 | 中 | mtime 校验 + watch 推送 + UI 自动刷新提示 |
| 保存触发 agent 误 watch（重复执行） | 低 | 写文件时不动 .cicy-tmp 之外的 sibling，避免污染 watcher |

## 删除清单（v1 跑顺后）

确认 native files 替换稳定后删除：

- `api/code-server-extension/`（整目录，~6KB src + node_modules）
- `api/resources/cicy-code-server-bridge-*.vsix`
- `api/resources/code-server-inject.html`
- `api/resources/code-server-inject.js`
- `api/mgr/setup.go` 中：
  - `codeServerInstallCmd`（~10 行）
  - `installCodeServerExtension` / `embeddedCodeServerBridgeVSIX`（~80 行）
  - `setupCodeServerSettings` 整个函数（~80 行）
  - `code-server` deps 项
- `api/mgr/proxy.go` 中：
  - `code-server` 反代分支（~100 行）
  - `serveCodeServerInjectJS`
- `api/mgr/main.go` 中：
  - `/code-server-inject.js` 路由
  - `/api/code-server/page-context`（如不再用）
- `npm` 全局 `code-server@latest` 安装
- `app/src/services/api.ts` 中 code-server 相关 client（如有）
- 文档/README 里 `:8002` / code-server 相关章节

保留：

- `/api/code-server/send-path` 路由（注册别名 `/api/fs/send-path`，平滑过渡）
- 端口 8002 让出，可删 `CS_PORT` 环境变量

## 验收标准

MVP 验收：

- [ ] 在 cicy-code workspace 里能列目录、点开文件、修改、Cmd+S 保存
- [ ] 大目录（如 node_modules）默认折叠，显式打开后不卡 UI
- [ ] 路径白名单单元测试覆盖 `..` / symlink / 绝对路径 / 跨 workspace
- [ ] 右键文件能发送路径给当前 agent，agent 收到事件
- [ ] env switch 能让 native / code-server 互切

v1 验收：

- [ ] Cmd+P 在 100k 文件 workspace 下 < 100ms 出结果
- [ ] Cmd+Shift+F 全文搜索能定位到行 / 列
- [ ] Diff 视图能正确显示 vs HEAD 的改动
- [ ] 图片直接预览，二进制不在编辑器里打开
- [ ] agent 修改文件时 UI 自动刷新或提示已外部修改
- [ ] 主 bundle 增量 < 200KB gzip
- [ ] 启动 cicy-code 时 native files 视图就绪 < 1s（无 npm install）

性能基线（在 cicy-code spot 实例上测）：

- 首屏根目录列出 < 50ms
- 点击目录展开（cache 冷） < 100ms
- 切换打开文件（< 1MB） < 80ms
- Cmd+P 100k 文件 fuzzy < 100ms
- 全文搜索 < 200ms（中等 workspace）

## 实施步骤（按 Phase 落 commit）

1. **commit: docs(native-files): plan**
   - 本文档
2. **commit: feat(fs): backend list/read/write/stat with path whitelist**
   - `api/mgr/files.go` + `api/mgr/files_test.go`
   - main.go 注册路由
3. **commit: feat(files): file explorer + tree store**
   - `app/src/components/files/explorer/*`
   - `app/src/components/files/api.ts`
4. **commit: feat(files): codemirror 6 editor + tabs + save**
   - `app/src/components/files/editor/*`
5. **commit: feat(workspace): mount FilesView, add env switch**
   - `app/src/components/Workspace.tsx`
   - 后端 settings 暴露 `use_native_files`
6. **commit: feat(fs): search/grep/diff/watch endpoints**
   - 后端补完，前端加 QuickOpen / GlobalSearch / DiffView
7. **commit: feat(files): image preview + binary guard + watch sync**
8. **commit: chore(code-server): retire after native files stable**
   - 按上面的删除清单清理

每个 commit 完成后跑 `python3 dev.py` 重建 + 在 UI 里冒烟测一遍。

## 配置项

新增环境变量：

| 变量 | 默认 | 说明 |
|---|---|---|
| `CICY_USE_NATIVE_FILES` | `1` | 启用 native files 视图 |
| `CICY_FS_READ_MAX_BYTES` | `5242880` | 单文件 read 上限（5MB） |
| `CICY_FS_WRITE_MAX_BYTES` | `5242880` | 单文件 write 上限 |
| `CICY_FS_LIST_MAX_ENTRIES` | `5000` | 单目录列出上限 |
| `CICY_FS_DEFAULT_BLACKLIST` | `.git,node_modules,dist,build,target,.cache` | 默认折叠目录 |

## 待决问题

- [ ] 是否要在 v1 就支持 .gitignore 解析？（暂定否，写死黑名单先用着）
- [ ] 是否做 vscode 风格 minimap？（暂定否，CM6 没原生支持）
- [ ] 文件 buffer 在 agent 切换时怎么处理？跟 agent 绑定还是全局共享？（暂定按 workspace 绑定，agent 切换时保留打开列表）
- [ ] 是否做"最近打开"列表持久化？（暂定走 localStorage，per-workspace）
