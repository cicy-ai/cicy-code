# Chat History Feature

## Overview

ChatMiddleView displays AI conversation history by parsing intercepted HTTP traffic between Kiro CLI and AWS API.

## Architecture

```
Kiro CLI  →  mitmproxy  →  http_log (MySQL)  →  API  →  ChatMiddleView (SSE)
```

1. **mitmproxy** intercepts Kiro CLI traffic (requests to `q.us-east-1`)
2. Request/response pairs are stored in `http_log` table with `pane_id`
3. Go API parses the raw data into structured chat turns
4. Frontend receives data via REST + SSE for real-time updates

## API Endpoints

### GET `/api/stats/chat?pane={pane_id}`

Returns chat history for a pane.

**Response:**
```json
{
  "data": [
    {
      "q": "user message",
      "a": "AI response text",
      "tools": [
        { "name": "fs_read", "arg": "/path/to/file" },
        { "name": "execute_bash", "arg": "ls -la" }
      ],
      "credit": 0.015,
      "first_ms": 1200,
      "status": "text",
      "ts": 1710200000
    }
  ]
}
```

**Fields:**
- `q` — User message (extracted from `USER MESSAGE BEGIN/END` markers). Empty string for continuation turns (multi-round tool use).
- `a` — AI text response (only present when `status=text`)
- `tools` — Tool calls with name and primary argument
- `credit` — API cost in USD
- `status` — `"text"` (final response) or `"tool_use"` (tool call round)
- `ts` — Unix timestamp

### GET `/api/stats/chat/stream?pane={pane_id}&token={token}`

SSE stream. Polls every 2s, pushes full chat array when data changes.

```
data: [{"q":"hello","a":"Hi!","status":"text","ts":1710200000}]
```

## Data Parsing Logic

### User Message Extraction
From request body: `conversationState.currentMessage.userInputMessage.content`
Extracts text between `--- USER MESSAGE BEGIN ---` and `--- USER MESSAGE END ---`.

### AI Response Extraction
From response body: matches `assistantResponseEvent` content chunks, concatenates them.

### Tool Extraction
- Current turn: detects `toolUseEvent` in response → `status: "tool_use"`
- Tool details (name + args): extracted from the **next** request's `conversationState.history` (which contains the previous turn's full tool use info)
- Supported arg extraction: `path`, `file_path`, `command`, `pattern`, `query`, `url`, `symbol_name`, and `operations[0].path` for fs_read/fs_write

## Frontend (ChatMiddleView)

### Message Grouping
Turns are grouped by user message (`q` field). A group = 1 user message + N AI rounds (tool_use → tool_use → ... → text).

### Display
- **User bubble**: Blue gradient, right-aligned
- **AI card**: Shows tool summary (read/write/search/bash counts) + Markdown response
- **Running indicator**: Pulsing dot when last round is `tool_use` (still processing)
- **Pending**: Listens to `chat-q-sent` CustomEvent to show "Waiting..." before SSE update arrives

### Built-in Terminal Drawer
Right-side sliding panel with ttyd iframe. Toggle via `toggle-ttyd-drawer` CustomEvent.

## Database

```sql
-- http_log table (populated by mitmproxy)
SELECT id, req_kb, res_kb, ts, data
FROM http_log
WHERE pane_id = ? AND url LIKE '%q.us-east-1%'
  AND CAST(data AS CHAR) LIKE '%GenerateAssistantResponse%'
ORDER BY id DESC LIMIT 50
```
