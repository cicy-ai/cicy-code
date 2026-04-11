# cicy WS Client（Go）发消息

这份文档只讲一件事：用 Go 通过 `cicy` 的 WebSocket 发消息。

## 1. 连接地址

`cicy-code-v1` 的 WS 入口：

```text
ws://<host>:<port>/api/chat/ws?agent_id=<agent>&token=<token>&client_id=<client_id>
```

- `agent_id` 必填。可用 `w-20016` 或 `w-20016:main.0`（服务端会归一化）。
- `token` 必填。可用 `~/global.json` 里的 `api_token`。
- `client_id` 可选。建议显式传，方便排查。

## 2. 消息格式

WS 文本帧 JSON 格式固定是：

```json
{
  "type": "事件类型",
  "data": {}
}
```

示例（发一条用户问题事件）：

```json
{
  "type": "user_q",
  "data": { "q": "你好，测试一下" }
}
```

## 3. Go 最小可运行示例（发送）

```go
package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	base := flag.String("base", "ws://127.0.0.1:8021", "cicy ws base")
	agent := flag.String("agent", "w-20016", "agent_id or pane")
	token := flag.String("token", "", "api token")
	clientID := flag.String("client", fmt.Sprintf("go-sender-%d", time.Now().Unix()), "client_id")
	flag.Parse()

	if *token == "" {
		log.Fatal("token required")
	}

	u, err := url.Parse(*base + "/api/chat/ws")
	if err != nil {
		log.Fatal(err)
	}
	q := u.Query()
	q.Set("agent_id", *agent)
	q.Set("token", *token)
	q.Set("client_id", *clientID)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	msg := map[string]interface{}{
		"type": "user_q",
		"data": map[string]interface{}{
			"q": "请回复：WS_OK",
		},
	}
	if err := conn.WriteJSON(msg); err != nil {
		log.Fatal(err)
	}
	log.Println("sent:", msg)
}
```

依赖安装：

```bash
go get github.com/gorilla/websocket
```

运行：

```bash
go run main.go -base ws://127.0.0.1:8021 -agent w-20016 -token '<YOUR_TOKEN>'
```

## 4. 关键行为（必须知道）

- 这个 WS Hub 默认是「同 `agent_id` 广播给其他客户端」，不会回显给发送者自己。
- 所以你如果只连了一个 WS 客户端，发完后看不到任何回包是正常的。
- 要看到结果，需要另一个客户端（如 Web UI 或 worker-client）也连在同一个 `agent_id`，由它处理并回发事件（如 `ai_chunk` / `ai_done`）。
