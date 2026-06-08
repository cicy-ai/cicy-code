# Docker 里 Agent 开发测试 SOP

适用范围：

- 运行中的 Docker 开发环境
- 默认 API：`http://127.0.0.1:8026`
- 容器名：`cicy-code-dev`
- 需要一个可用 token

本文只覆盖 4 件事：

1. 如何给 agent 发消息
2. 如何看 tmux agent 输出
3. 如何测 webpage ping/pong
4. 如何用 curl 下发 exec-js 和看 UI history

## 0. 先准备变量

```bash
export API_BASE="http://127.0.0.1:8026"
export AGENT_ID="w-10002"
```

不要把真实 token 写进文档，也不要单独把 token 打出来显示。

先确认服务是活的：

```bash
curl -fsS "$API_BASE/api/health"
```

## 1. 给 agent 发消息

发消息走 `/api/tmux/send`。

最小例子：

```bash
curl -fsS "$API_BASE/api/tmux/send" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" \
  -H 'Content-Type: application/json' \
  --data '{
    "pane_id": "'"$AGENT_ID"'",
    "text": "Reply with exactly two short bullets: one says alpha, one says beta."
  }' | jq '.'
```

说明：

- `AGENT_ID` 是 agent id，比如 `w-1001`、`w-10002`
- 这里的 `pane_id` 直接传 agent id 即可
- `text` 是要发给 agent 的 prompt
- 这个接口会把文本送进 tmux pane，并自动提交

## 2. 看 tmux agent 输出

优先走现成接口 `/api/tmux/capture`：

```bash
curl -fsS "$API_BASE/api/tmux/capture" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" \
  -H 'Content-Type: application/json' \
  --data '{
    "pane_id": "'"$AGENT_ID"':main.0",
    "lines": 200
  }' | jq -r '.output'
```

如果你手里已经是完整 pane id，比如 `w-10002:main.0`，那就直接传它，不要再拼一次。

容器内直接抓 tmux 只作为兜底：

```bash
docker exec cicy-code-dev sh -lc \
  'tmux capture-pane -t '"$AGENT_ID"':main.0 -p -S -200 | tail -n 200'
```

常用判断：

- 看 prompt 有没有真正送进去
- 看 agent 现在是不是还在 thinking / tool use
- 看最终答案有没有回到 prompt

## 3. 测 webpage ping/pong

先查当前在线网页 client：

```bash
curl -fsS "$API_BASE/api/chat/clients" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" | jq '.'
```

会看到类似：

```json
{
  "w-20005": {
    "web-xxxx": { "...": "..." }
  }
}
```

这里真正要用的是 `client_id`，不是 `master_agent_id`。

直接 ping：

```bash
export CLIENT_ID="web-mok1qd0g-on41fxjl"

curl -fsS "$API_BASE/api/chat/ping-client" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" \
  -H 'Content-Type: application/json' \
  --data '{
    "client_id": "'"$CLIENT_ID"'",
    "timeout_ms": 3000
  }' | jq '.'
```

成功时会返回：

```json
{
  "success": true,
  "mode": "direct",
  "type": "webpage_pong",
  "data": {
    "requestId": "ping-xxx",
    "version": "2.0.1"
  }
}
```

如果返回：

- `404 client not found`
  - 说明这个 `client_id` 当前不在线
- `504 ping timeout`
  - 说明消息发到了页面，但页面没有成功回 `webpage_pong`

## 4. 用 curl 下发 exec-js 和看 UI history

先看当前 client：

```bash
curl -fsS "$API_BASE/api/chat/clients" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" | jq '.'
```

下发一段最小 `exec_js`：

```bash
curl -fsS "$API_BASE/api/chat/push" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" \
  -H 'Content-Type: application/json' \
  --data '{
    "client_id": "'"$CLIENT_ID"'",
    "type": "exec_js",
    "data": {
      "requestId": "exec-title-1",
      "code": "document.title"
    }
  }' | jq '.'
```

读当前 active agent id：

```bash
curl -fsS "$API_BASE/api/chat/push" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" \
  -H 'Content-Type: application/json' \
  --data '{
    "client_id": "'"$CLIENT_ID"'",
    "type": "exec_js",
    "data": {
      "requestId": "exec-active-pane-1",
      "code": "(() => { const s = globalThis.devStore?.getSnapshot?.()?.Workspace?.state || null; return s?.activeCliPaneId || \"\"; })()"
    }
  }' | jq '.'
```

读当前 master agent id：

```bash
curl -fsS "$API_BASE/api/chat/push" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" \
  -H 'Content-Type: application/json' \
  --data '{
    "client_id": "'"$CLIENT_ID"'",
    "type": "exec_js",
    "data": {
      "requestId": "exec-master-pane-1",
      "code": "(() => { const s = globalThis.devStore?.getSnapshot?.()?.Workspace?.state || null; return s?.masterAgentId || \"\"; })()"
    }
  }' | jq '.'
```

抓当前 UI 的 current-history 文本：

```bash
curl -fsS "$API_BASE/api/chat/push" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" \
  -H 'Content-Type: application/json' \
  --data '{
    "client_id": "'"$CLIENT_ID"'",
    "type": "exec_js",
    "data": {
      "requestId": "exec-history-text-1",
      "code": "(() => { const el = document.querySelector(\"这\"); return el ? el.innerText.trim() : \"current-history-list not found\"; })()"
    }
  }' | jq '.'
```

如果只想看 turn 数量：

```bash
curl -fsS "$API_BASE/api/chat/push" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" \
  -H 'Content-Type: application/json' \
  --data '{
    "client_id": "'"$CLIENT_ID"'",
    "type": "exec_js",
    "data": {
      "requestId": "exec-history-count-1",
      "code": "(() => document.querySelectorAll(\"[data-id=\\\"current-history-turn\\\"]\").length)()"
    }
  }' | jq '.'
```

如果想把每个 turn 文本抓成数组：

```bash
curl -fsS "$API_BASE/api/chat/push" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" \
  -H 'Content-Type: application/json' \
  --data '{
    "client_id": "'"$CLIENT_ID"'",
    "type": "exec_js",
    "data": {
      "requestId": "exec-history-turns-1",
      "code": "(() => Array.from(document.querySelectorAll(\"[data-id=\\\"current-history-turn\\\"]\")).map((el, i) => ({ index: i, text: (el.innerText || \"\").trim() })))()"
    }
  }' | jq '.'
```

说明：

- `/api/chat/push` 这里只保证消息已经发到目标 `client_id`
- 当前没有现成的 HTTP 同步等待 `exec_js_result` 接口
- 如果你需要同步拿到 `exec_js_result` 回包，再用 `agent-webpage exec-js`

同步拿回包时，才用：

```bash
agent-webpage exec-js '
(() => {
  const el = document.querySelector("[data-id=\"current-history-list\"]");
  return el ? el.innerText.trim() : "current-history-list not found";
})()
' "$CLIENT_ID"
```

## 常用组合

### A. 发消息后看 tmux

```bash
curl -fsS "$API_BASE/api/tmux/send" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" \
  -H 'Content-Type: application/json' \
  --data '{
    "pane_id": "'"$AGENT_ID"'",
    "text": "Reply with exactly one word: hello."
  }' >/dev/null

docker exec cicy-code-dev sh -lc \
  'tmux capture-pane -t '"$AGENT_ID"':main.0 -p -S -120 | tail -n 120'
```

更推荐直接走接口：

```bash
curl -fsS "$API_BASE/api/tmux/capture" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" \
  -H 'Content-Type: application/json' \
  --data '{
    "pane_id": "'"$AGENT_ID"':main.0",
    "lines": 120
  }' | jq -r '.output'
```

### B. 发消息后看 current-history UI

```bash
curl -fsS "$API_BASE/api/tmux/send" \
  -H "Authorization: Bearer $(jq -r '.api_token' /home/cicy/cicy-ai/global.json)" \
  -H 'Content-Type: application/json' \
  --data '{
    "pane_id": "'"$AGENT_ID"'",
    "text": "Reply with exactly one word: world."
  }' >/dev/null

sleep 3

agent-webpage exec-js '
(() => {
  const el = document.querySelector("[data-id=\"current-history-list\"]");
  return el ? el.innerText.trim() : "current-history-list not found";
})()
' "$CLIENT_ID"
```

## 已知现象

- Claude 的 current-history 可能会落后一轮，这是当前行为，不一定是发送失败
- 如果连续非常快地往同一个 Claude pane 塞消息，tmux 侧可能把输入黏在一起
- 页面通信现在只认 `client_id` 点对点，不要再按 broadcast 思路测
