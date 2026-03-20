# Cursor 集成方案

cicy-code 作为指挥官，将任务派发给 Cursor 执行。Cursor 作为第 7 个 Agent（编辑器级 Agent）。

## 架构

```
cicy-code (指挥官, port 18008)
  │
  ├─ Agent 1-6: kiro/claude/copilot/gemini/codex/opencode (tmux + ttyd)
  │     └─ 终端级 Agent，擅长 CLI 任务
  │
  └─ Agent 7: Cursor (Electron + Extension)
        └─ 编辑器级 Agent，擅长代码重构、多文件编辑、UI 开发
```

## 方案对比

| 方案 | 合规性 | 能力 | 工作量 | 推荐 |
|------|--------|------|--------|------|
| **Cursor Extension**（官方） | ✅ 合规 | Extension API | 3-5 天 | ✅ 产品对外 |
| **CDP Hook**（调试端口） | ⚠️ 灰色 | 完全控制 | 1-2 天 | 仅内部工具 |
| 文件协议 | ✅ 合规 | 有限 | 1 天 | 过渡方案 |

---

## 方案一：Cursor Extension（推荐，对外发布）

### 原理

开发一个 Cursor/VS Code 插件，上架 Marketplace。用户主动安装并授权连接 cicy-code。

```
Cursor Extension ←──WebSocket──→ cicy-code API (port 18008)
      │
      ├─ 接收任务 → 调用 Cursor AI Composer
      ├─ 监听文件变更 → 回传结果
      └─ 上报状态 → cicy-code 仪表盘显示进度
```

### Extension 功能设计

```
cicy-code-connector/
├── package.json          # Extension manifest
├── src/
│   ├── extension.ts      # 入口：激活/注销
│   ├── bridge.ts         # WebSocket 桥接 cicy-code API
│   ├── taskRunner.ts     # 接收任务 → 调用 Cursor AI
│   └── reporter.ts       # 文件变更/完成状态回传
└── README.md
```

### 核心流程

```typescript
// bridge.ts — 连接 cicy-code
const ws = new WebSocket('ws://localhost:18008/ws/cursor?token=xxx');

ws.onmessage = (msg) => {
    const task = JSON.parse(msg.data);
    switch (task.type) {
        case 'edit':
            // 打开文件 + 调用 Cursor AI
            vscode.commands.executeCommand('vscode.open', vscode.Uri.file(task.file));
            vscode.commands.executeCommand('editor.action.triggerSuggest');
            break;
        case 'composer':
            // 调用 Composer（如果有公开 API）
            vscode.commands.executeCommand('composer.startSession', {
                prompt: task.prompt
            });
            break;
        case 'refactor':
            // 选中代码 → 触发 AI 重构
            break;
    }
};

// reporter.ts — 回传结果
vscode.workspace.onDidSaveTextDocument(doc => {
    ws.send(JSON.stringify({
        type: 'file_saved',
        file: doc.uri.fsPath,
        content: doc.getText()
    }));
});
```

### cicy-code 侧 API

新增 `/ws/cursor` WebSocket 端点：

```go
// ws.go — Cursor Agent WebSocket
func handleCursorWS(w http.ResponseWriter, r *http.Request) {
    conn, _ := upgrader.Upgrade(w, r, nil)
    
    // 注册为 Agent
    cursorAgent = conn
    
    // 发送任务
    conn.WriteJSON(Task{
        Type:   "composer",
        Prompt: "重构 auth 模块，添加 JWT token 刷新",
        Files:  []string{"src/auth.ts", "src/middleware.ts"},
    })
    
    // 接收结果
    for {
        var result TaskResult
        conn.ReadJSON(&result)
        // 更新任务状态、通知前端
    }
}
```

### 合规优势

- ✅ 用户主动安装，主动输入 cicy-code 地址
- ✅ 走官方 Extension API，不逆向 Cursor
- ✅ 可上架 Cursor / VS Code Marketplace
- ✅ 插件本身也是获客渠道

---

## 方案二：CDP Hook（仅限内部开发/调试）

### ⚠️ 法律风险

| 行为 | 风险 |
|------|------|
| 用户自己 hook 自己的 Cursor | ✅ 无风险 |
| 产品自动化 hook 用户的 Cursor | ⚠️ 可能违反 Cursor TOS |
| 调用 Cursor 内部私有 API | ⚠️ 逆向工程条款 |
| 绕过付费限制 | ❌ 违法 |

**结论**：CDP 方案仅用于内部开发和测试，不对外发布。

### 原理

Cursor 基于 Electron，支持 Chromium 远程调试协议（CDP）。启动时开启调试端口，通过 WebSocket 注入 JS 控制 Cursor 内部行为。

```
cicy-code ──CDP WebSocket──→ Cursor Electron (port 9222) ──→ JS Runtime
```

### 启动 Cursor（开调试端口）

```bash
# macOS
/Applications/Cursor.app/Contents/MacOS/Cursor --remote-debugging-port=9222

# Linux
cursor --remote-debugging-port=9222
```

### 连接 CDP

```bash
# 获取可调试页面列表
curl http://127.0.0.1:9222/json

# 返回 WebSocket URL
# ws://127.0.0.1:9222/devtools/page/xxx
```

### 注入 JS 执行任务

```go
// cdp_bridge.go
func sendTaskToCursor(prompt string) error {
    // 1. 获取 CDP WebSocket URL
    resp, _ := http.Get("http://127.0.0.1:9222/json")
    var pages []CDPPage
    json.NewDecoder(resp.Body).Decode(&pages)
    
    // 2. 连接目标页面
    ws, _ := websocket.Dial(pages[0].WebSocketDebuggerUrl)
    
    // 3. 注入 JS
    js := fmt.Sprintf(`
        (async () => {
            const vscode = require('vscode');
            
            // 调用 Composer AI
            await vscode.commands.executeCommand('composer.startSession', {
                prompt: %q
            });
            
            // Hook 文件保存，回传结果
            vscode.workspace.onDidSaveTextDocument(doc => {
                fetch('http://localhost:18008/api/cursor/callback', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        file: doc.uri.fsPath,
                        content: doc.getText()
                    })
                });
            });
        })()
    `, prompt)
    
    // 4. Runtime.evaluate
    ws.WriteJSON(CDPCommand{
        Method: "Runtime.evaluate",
        Params: map[string]interface{}{
            "expression": js,
        },
    })
    
    return nil
}
```

### CDP 可用能力

| 能力 | CDP 方法 | 用途 |
|------|----------|------|
| 执行 JS | `Runtime.evaluate` | 调用内部 API |
| 监听事件 | `Runtime.consoleAPICalled` | 捕获 AI 输出 |
| DOM 操作 | `DOM.querySelector` | 读取 UI 状态 |
| 截图 | `Page.captureScreenshot` | 任务进度快照 |
| 键盘模拟 | `Input.dispatchKeyEvent` | 触发快捷键 |

---

## 方案三：文件协议（过渡方案）

最简单的集成，零代码依赖，用文件系统作为通信通道。

### 原理

```
cicy-code 写任务文件 → 共享 workspace → Cursor 检测到变更 → .cursorrules 引导 AI 执行
```

### 实现

**cicy-code 侧**：写任务到 workspace

```go
// 写任务文件
task := `## Task: 重构 auth 模块
- 添加 JWT token 刷新逻辑
- 修改 src/auth.ts 和 src/middleware.ts
- 完成后删除本文件`

os.WriteFile(workspace+"/.cicy/task.md", []byte(task), 0644)
```

**Cursor 侧**：`.cursorrules` 引导

```
# .cursorrules
当 .cicy/task.md 存在时，优先读取并执行其中的任务。
完成后将结果写入 .cicy/result.md 并删除 task.md。
```

**cicy-code 侧**：监听结果

```go
// fsnotify 监听 .cicy/result.md
watcher.Add(workspace + "/.cicy/")
for event := range watcher.Events {
    if event.Name == "result.md" && event.Op == fsnotify.Create {
        result, _ := os.ReadFile(event.Name)
        // 处理结果，更新任务状态
    }
}
```

### 局限

- 依赖用户在 Cursor 中主动触发 AI（不能自动执行）
- .cursorrules 只是"建议"，AI 不一定遵循
- 无法监听实时进度

---

## 路线图

```
Phase 1（现在）: 文件协议 — 验证 cicy-code → Cursor 任务流
Phase 2（下一版）: Cursor Extension — 正式双向通信，上架 Marketplace
Phase 3（未来）: CDP Bridge — 内部高级自动化工具（不对外）
```

## cicy-code 侧改动清单

| 改动 | 文件 | 说明 |
|------|------|------|
| `/ws/cursor` 端点 | `mgr/ws.go` | Cursor Extension WebSocket 通道 |
| `/api/cursor/callback` | `mgr/tmux.go` | 接收 Cursor 完成回调 |
| 任务模型 | `mgr/store` | `cursor_tasks` 表：任务队列 |
| 前端面板 | inject HTML | 显示 Cursor Agent 状态 |
| Extension 项目 | `extensions/cursor/` | 独立子项目 |
