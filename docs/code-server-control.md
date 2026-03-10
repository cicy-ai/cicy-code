# Code-Server Remote Control

## 原理

code-server 底层是 VS Code，支持通过 Unix IPC socket 与已运行实例通信。

## 核心机制

```
VSCODE_IPC_HOOK_CLI=/tmp/vscode-ipc-xxx.sock  code-server <file>
```

- Socket 路径: `/tmp/vscode-ipc-*.sock` (取最新的)
- Remote CLI: `/usr/lib/code-server/lib/vscode/bin/remote-cli/code-linux.sh`

## API 调用

### 打开文件
```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:14445/api/notify \
  -d '{"action":"open_file","file":"/path/to/file","message":"📄 打开文件"}'
```

后端自动:
1. 找最新 IPC socket (`ls -t /tmp/vscode-ipc-*.sock | head -1`)
2. 执行 `remote-cli --reuse-window --goto file:1:1`
3. SSE 通知前端打开 Drawer Code tab

### 打开指定行
```bash
# 后端 openInCodeServer() 支持 file:line:col 格式
# 目前默认 :1:1，可扩展
```

## 其他 Drawer 控制

```bash
# 打开 Drawer 到指定 tab
curl -X POST ... -d '{"action":"open_drawer","tab":"Traffic","message":"..."}'

# 关闭 Drawer
curl -X POST ... -d '{"action":"close_drawer"}'

# 切换 tab (不改变 Drawer 开关状态)
curl -X POST ... -d '{"action":"switch_tab","tab":"Code"}'

# Toggle Drawer
curl -X POST ... -d '{"action":"toggle_drawer"}'

# 刷新 code-server iframe
curl -X POST ... -d '{"action":"refresh_code"}'
```

## 注意事项

- IPC socket 有很多 stale 的，必须按修改时间排序取最新
- 侧边栏: 用户手动 `Ctrl+B` 关一次，code-server 会记住状态
- code-server 的 `--goto` 只在 remote CLI 里支持，`code-server` 主命令不支持
- hook 系统: `~/projects/code-server-hook/hook.js` 可注入 custom.js 到 workbench.html

## 文件路径

| 文件 | 说明 |
|------|------|
| `ttyd-manager/mgr/stats.go` | `openInCodeServer()` 函数 |
| `ide/src/MainApp.tsx` | SSE 监听 + Drawer 控制 |
| `code-server-hook/custom.js` | 注入到 code-server 的自定义脚本 |
| `code-server-hook/hook.js` | 注入工具 |
