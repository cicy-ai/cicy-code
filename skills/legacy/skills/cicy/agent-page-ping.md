# AgentPage Ping

测试 Agent → WebSocket → AgentPage 连通性。

## 用法

```bash
# 发送默认测试事件
python3 ~/skills/agent-page-ping.py

# 发送指定类型事件
python3 ~/skills/agent-page-ping.py test
python3 ~/skills/agent-page-ping.py open_window
```

## 示例

```bash
# 测试连通性
python3 ~/skills/agent-page-ping.py test

# 打开 Gemini 窗口
python3 ~/skills/agent-page-ping.py open_window
```

## 检查结果

打开前端 Console，应该能看到：
- `[ChatView] WS 收到消息: desktop_event`
- `[AgentPage] 收到 desktop event: test`

## 原理

```
Python Script
  → WebSocket (ws://localhost:18080/api/chat/ws)
  → {type: 'desktop_event', data: {...}}
  → ChatView.tsx 收到
  → window.dispatchEvent('agent-desktop-event')
  → AgentPage.tsx 监听并处理
```
