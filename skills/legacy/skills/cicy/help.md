# CiCy Skills - Tool List

## Agent / Webpage

| 命令 | 说明 |
|------|------|
| `agent-page-ping [pane]` | 测试 Agent 页面连通性 |
| `ipc-ping [pane]` | 测试 Electron IPC 连通性 |
| `webpage ping [pane]` | 测试网页客户端连通性 |
| `webpage ipc-ping [pane]` | 通过 `webpage` 触发 IPC 测试 |
| `webpage exec-js '<js>' [pane]` | 在网页客户端执行 JS |
| `webpage send <type> <data_json> [pane]` | 发送自定义网页消息 |
| `webpage clients` | 查看当前网页客户端连接 |

## Tmux

| 命令 | 说明 |
|------|------|
| `tm ls` | 列出所有 pane |
| `tm status [pane]` | 查看 pane 状态 |
| `tm capture <pane>` | 获取 pane 输出 |
| `tm msg <pane> <text>` | 向 pane 发消息 |
| `tm send-keys <pane> <keys>` | 向 pane 发送按键 |
