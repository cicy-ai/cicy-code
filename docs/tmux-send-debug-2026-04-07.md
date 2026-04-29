# tmux send 调试记录（2026-04-07，归档）

这是一次针对 `/api/tmux/send` 的历史调试记录摘要。

它保留结论，不再保留当天的敏感 token、临时路径和一次性环境细节。

## 调试对象

- 容器内一个 `codex` pane
- 目标是确认长文本通过 `/api/tmux/send` 后，是否真的被提交给 agent，而不是只停留在输入框里

## 当天确认过的路径

1. 发送超长文本时，代码会进入 `chunked` 发送路径
2. 发送后会先做一次或两次“回显确认”
3. 确认文本已经稳定出现在 pane UI 里后，才发送 Enter
4. Enter 后还会再抓一次 pane 内容，确认已经进入真正的对话推进状态

## 当天得到的有效结论

### 1. `chunked` 路径可用

对 `codex` 的那次测试里，长文本确实走了 `chunked` 发送路径，并且后续确认链路成功。

### 2. 预提交确认不是摆设

调试结果表明，`tmux.go` 里的“发送前确认 prompt 已稳定出现在界面上”这一步是有效的，不是日志噪音。

### 3. 提交确认依赖 agent 类型

`codex`、`claude`、`openclaw` 的“提交成功”判断逻辑并不相同。

当前代码位置：

- `waitForPromptEchoBeforeEnter`
- `submitPromptWithConfirmation`
- `promptSubmitConfirmed`

都在 `api/mgr/tmux.go`。

## 现在仍然有用的排查方法

如果今天再排查 `/api/tmux/send`，优先看：

- `api/mgr/tmux.go` 里的 ready / confirm / submit 三段逻辑
- `capture-pane` 输出
- tmux send trace 日志
- agent 当前是否处在 trust / theme / startup prompt 阶段

## 这份归档的作用

它现在只用于说明一件事：

> 这条发送链路历史上已经验证过“长文本可以被真正提交”，所以今天再出问题时，先查当前 agent 启动状态、pane ready 判断和提交确认条件，不要直接假设 `chunked send` 本身坏了。
