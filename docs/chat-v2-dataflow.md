# Chat V2 数据流

## 发送消息 (Send Q)

```
User types prompt → Click Send
  │
  ├─1. Write to local state history (setChatData append new turn)
  ├─2. Update IndexedDB (idbAdd)
  ├─3. Update conversation UI (optimistic: show user bubble + "Waiting...")
  ├─4. sendCommandToTmux (HTTP POST → API → tmux send-keys → Kiro CLI)
  │
  └─ User sees message immediately, no waiting
```

## 接收 AI 回复

```
Kiro CLI processes prompt → calls AWS API
  │
  ├─5. mitmproxy captures AI response (AWS Event Stream)
  ├─6. mitmproxy parses events → POST /api/chat/webhook
  ├─7. chatHub broadcasts to WS clients (notify frontend)
  │
  └─ Frontend receives WS notification
       │
       ├─8. HTTP GET /api/stats/chat (fetch latest turns)
       ├─9. Update local state history (setChatData with server data)
       ├─10. Update IndexedDB (idbPutAll)
       └─11. UI refreshes with AI response
```

## 时序图

```
User          CommandPanel      ChatMiddleView     API        mitmproxy      WS Hub
 │                │                  │              │              │            │
 │──type+send────>│                  │              │              │            │
 │                │──chat-q-sent────>│              │              │            │
 │                │                  │─setChatData  │              │            │
 │                │                  │─idbAdd       │              │            │
 │                │                  │─render UI    │              │            │
 │                │──sendToTmux─────>│──────────────>│             │            │
 │                │                  │              │──tmux keys──>│            │
 │                │                  │              │              │            │
 │                │                  │              │   Kiro CLI calls AWS      │
 │                │                  │              │              │            │
 │                │                  │              │<──AI stream──│            │
 │                │                  │              │  (captured)  │            │
 │                │                  │              │              │            │
 │                │                  │              │<─webhook POST│            │
 │                │                  │              │──broadcast──>│───ws msg──>│
 │                │                  │<─────────────────────────────────────────│
 │                │                  │─GET /chat────>│             │            │
 │                │                  │<─turns────────│             │            │
 │                │                  │─setChatData  │              │            │
 │                │                  │─idbPutAll    │              │            │
 │                │                  │─render UI    │              │            │
 │<──────────────────sees AI reply───│              │              │            │
```

## 关键原则

- **WS 只做通知**，不传数据，数据全走 HTTP
- **Optimistic update**：发消息立即显示，不等服务端确认
- **IndexedDB 双写**：发送时写一次，收到服务端数据再写一次
- **mitmproxy 是桥梁**：拦截 AI 响应 → webhook → WS → 前端刷新
