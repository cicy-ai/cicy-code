    # cicy WebSocket（Go）发消息

    这份文档只讲当前代码里的聊天 WebSocket。

    ## 连接地址

    ```text
    ws://<host>:<port>/api/chat/ws?agent_id=<agent>&token=<token>&client_id=<client_id>
    ```

    参数说明：

    - `agent_id`：必填，`w-10001` 或 `w-10001:main.0` 都可以
    - `token`：必填，通常来自 `~/cicy-ai/global.json -> api_token`
    - `client_id`：建议显式传，便于排查
    - `electron`：可选，桌面端内部使用

    服务端会把 `agent_id` 归一化为短 pane ID，例如 `w-10001`。

    ## 消息格式

    文本帧固定是：

    ```json
    {
      "type": "事件类型",
      "data": {}
    }
    ```

    最常见的用户问题事件：

    ```json
    {
      "type": "user_q",
      "data": {
        "q": "你好，测试一下"
      }
    }
    ```

    ## 需要知道的当前行为

    - 普通事件会广播给**同一个 agent_id 下的其他客户端**
    - 发送者自己默认**不会收到回显**
    - `poll_request` 是例外：服务端会直接回一个 `poll_data`
    - `ping` 也是例外：服务端会直接回一个 `pong`

    也就是说，如果你只连了一个 WS 客户端，发 `user_q` 后没有收到同内容回包是正常的。

    ## Go 最小示例

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
        base := flag.String("base", "ws://127.0.0.1:8008", "ws base")
        agent := flag.String("agent", "w-10001", "agent_id or pane")
        token := flag.String("token", "", "api token")
        clientID := flag.String("client", fmt.Sprintf("go-%d", time.Now().Unix()), "client_id")
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

        go func() {
            for {
                var msg map[string]any
                if err := conn.ReadJSON(&msg); err != nil {
                    log.Println("read:", err)
                    return
                }
                log.Printf("recv: %#v
", msg)
            }
        }()

        if err := conn.WriteJSON(map[string]any{
            "type": "poll_request",
        }); err != nil {
            log.Fatal(err)
        }

        if err := conn.WriteJSON(map[string]any{
            "type": "user_q",
            "data": map[string]any{
                "q": "请回复：WS_OK",
            },
        }); err != nil {
            log.Fatal(err)
        }

        time.Sleep(10 * time.Second)
    }
    ```

    依赖：

    ```bash
    go get github.com/gorilla/websocket
    ```

    运行：

    ```bash
    go run main.go -base ws://127.0.0.1:8008 -agent w-10001 -token '<YOUR_TOKEN>'
    ```

    ## 排查要点

    1. `agent_id` 用短 ID 或完整 pane ID 都行
    2. 没有 token 会直接 `401`
    3. 想立即验证连接活着，用 `poll_request` 或 `ping`
    4. 想看 agent 处理结果，需要有别的客户端也连在同一个 `agent_id` 下参与转发
