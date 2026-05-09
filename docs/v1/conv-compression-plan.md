# Conversation Compression Plan

## 背景

每次请求都带着完整的 tool result 大文本（如 `Read` 文件内容、`Bash` 输出），浪费大量 token。中转站 API 没有 prompt caching，每 token 都是实打实的钱；官方直连虽然有缓存，但大文本反复刷也会 miss。

## 目标

对 tool result / tool_call_output 的大文本做压缩，用摘要替换原始内容。agent 需要细节时自己再 Read。

## 方案

在 proxy 转发请求前，扫描 messages 数组，对 tool_result 内容做截断：

- **< 500 字节** → 完整保留
- **500-2000 字节** → 保留前 200 字符 + `… (truncated, N bytes total)`
- **> 2000 字节** → 替换为 `[tool_result: tool_name (N bytes)]`

## 影响

- **上下文理解**：不影响。tool result 是"一次性信息"，agent 当时已经消费过。后续需要时可再 Read。
- **token 节省**：大部分 Read/Bash 输出在 1-50KB 之间，压缩后可减少 90%+ 历史 token。
- **缓存**：中转站无缓存场景效果最明显；官方直连也能减少缓存刷新频率。

## 实现位置

`api/mgr/proxy.go` — openClawProxy Director 里，在 body 改写后追加一轮 tool result 压缩。

## 不做的事

- 不修改 current.json 存储（保留完整历史用于 UI 查看）
- 不对 text/thinking 消息做压缩（通常较小，且是 agent 输出）
- 不按 provider 区分（统一处理，简单可靠）
