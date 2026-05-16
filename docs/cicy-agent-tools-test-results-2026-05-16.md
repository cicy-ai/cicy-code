# cicy_agent_* Tools — Full Test Run

**Date**: 2026-05-16  
**Server**: cicy-code v2.0.1 on 127.0.0.1:8008  
**Upstream**: deepseek (direct) — claude→`api.deepseek.com/anthropic`, codex/opencode→`api.deepseek.com`  
**Test panes**: w-10021 (claude), w-10022 (codex), w-10003 (opencode), driver w-10018 (claude)

## Result matrix

| # | Test | Coverage | Status | Evidence |
|---|------|----------|--------|----------|
| T01 | list configured agents | `cicy_agent_list` | ✅ PASS | claude answered "17, 8, 1, 1" matching real counts |
| T02 | tmux snapshot | `cicy_agent_tmux_ls` | ✅ PASS | codex+opencode both got 19 rows |
| T03 | capture pane state | `cicy_agent_capture` | ✅ PASS | last-line content matched on both |
| T04 | msg without callback | `cicy_agent_msg` | ✅ PASS | "T04-pong from codex/opencode" delivered to w-10018 input |
| T05 | callback no-tool fast fire | `--callback`, immediate fire | ✅ PASS | fire 17:23:54 with NO defer (codex no-tool reply) |
| T06 | callback deferred tool use | defer-then-fire | ✅ PASS | defer (tool_calls=2) at 17:24:25 → fire at 17:24:33 |
| T07 | callback `❌ failed` | failure path | ✅ PASS | broken provider → `[w-10003] ❌ failed` delivered |
| T08 | reply default (no `--full`) | `cicy_agent_get_last_reply` | ✅ PASS | `[thinking]` + `[text]`, NO `[tool_use` |
| T09 | reply `--full` includes tool_use | `--full` mode | ✅ PASS | `[tool_use name=write]` + `[tool_use name=bash]` present |
| T10 | chat_history reverse index | `cicy_agent_get_chat_history` | ✅ PASS | idx 0/1/2 correct, idx 99 → `found:false` |
| T11 | send-keys Enter recovery | stuck-input recovery | ✅ PASS | token moved input area → chat area after Enter |
| T12 | send-keys special sequences | `C-l`, `y`, `Up Up Enter`, `Escape` | ✅ PASS | all accepted (success:true) |
| T13 | 3-agent collaboration | architect→worker→reviewer chain | ✅ PASS | codex's final report cites claude impl + opencode review, `/tmp/cicy-greet.py World → Hello, World!` |
| T14 | self-callback guard | `pane==callback_to` ignored | ✅ PASS | zero `registered pane=X callback_to=X` entries ever |
| T15 | ping-pong protection | callback msg doesn't loop | ✅ PASS | register=12, fired=12 strict 1:1 across all tests |
| T16 | global-memory injection | `<global-memory>` in system | ✅ PASS | marker `7B9X3-MAGIC` appears in current.json for ALL 3 protocols |
| T17 | project-memory injection | `<project-memory>` per project key | ✅ PASS | marker `P5J7K-MAGIC` appears, global+project both present |
| T18 | workspace AGENTS.md injection | `<workspace-rules>` from file | ✅ PASS | marker `W9X1Z-MAGIC` from AGENTS.md appears |

## Key findings

- **Deferred callback** works correctly. Across tool-use scenarios (T06, T13, T07), the server delays `fired` until the receiver's LAST gateway request in the same user turn (the one with no remaining tool_calls). Intermediate finalizes increment `defer` count without sending the notification.
- **Emoji notifications**: `[<id>] ✅ work done` and `[<id>] ❌ failed` both delivered cleanly to sender's pane.
- **Cross-protocol parity**: anthropic (claude), openai chat-completions (opencode), openai responses-API (codex) all see the same injection (`<global-memory>`, `<workspace-rules>` etc.).
- **Per-protocol injection placement**:
  - anthropic: prepended as a string at `body.system[0]`
  - openai chat-completions: prepended into the first system message
  - openai responses-API: prepended into developer/system input items
- **`cicy_agent_get_chat_history`**: SQLite-backed, reverse index from latest finalized turn; out-of-range returns `{found:false}` without error.
- **`cicy_agent_send_key_event`**: passthrough to tmux send-keys, accepts `Enter`, `C-c`, `C-l`, named keys, combos, and literal characters.

## Bugs found and fixed during this run

1. **Codex `tool_calls must be followed by tool messages`**: transform `transformResponsesRequestToChatCompletions` produced each `function_call` as its own assistant message, breaking DeepSeek's ordering check. Fixed by merging consecutive `function_call` items into a single assistant message with multiple tool_calls + balanced inline tool messages.
2. **DeepSeek `/anthropic/v1/chat/completions` 404**: `shouldAdaptDeepSeekForAnthropic` only checked host, not path. When upstream URL already includes `/anthropic`, it's DeepSeek's native messages endpoint — the chat-completions translation was breaking it. Fixed by also checking `basePath` and skipping the adaptation when `/anthropic` is in the path.
3. **Early callback fire (race)**: callback hooks fired on the FIRST `status=completed` finalize, but tool-using CLIs (claude/codex/opencode) emit multiple gateway requests per user turn (model → tool_call → tool_result → … → final answer). Each intermediate request finalizes with `status=completed`. Fixed by re-queueing the pending callback when `reply.ToolCalls > 0`; the next `newReplyHooksForPane` drain picks it up, and only the truly final response (no more tool calls) actually fires.
4. **Tool injection lost via codex transform**: `injectCicyToolDefs` emits chat-completions nested format `{type,function:{name,desc,params}}`, but `transformResponsesRequestToChatCompletions` assumed flat format and read top-level `name`/`description`/`parameters`. Fixed by tolerating both nested and flat shapes in the transform.

## Cleanup performed

- Test global-memory rule disabled (content cleared, enabled=0)
- Test project-memory rule removed from `prompt_rules`
- w-10003 pane config restored to `{"runtime_ai": {"provider_name": "deepseek"}}`
- Workspace `AGENTS.md` files restored from backups

## Files referenced

- `/home/cicy/projects/cicy-code/api/mgr/gateway_cicy_tools.go` — tool definitions
- `/home/cicy/projects/cicy-code/api/mgr/gateway_reply_callback.go` — callback hook + defer logic
- `/home/cicy/projects/cicy-code/api/mgr/gateway_reply_text.go` — get_last_reply endpoint
- `/home/cicy/projects/cicy-code/api/mgr/gateway_chat_history.go` — chat_history endpoint
- `/home/cicy/projects/cicy-code/api/mgr/ai_gateway_deepseek.go` — transforms (codex + deepseek anthropic)
- `/home/cicy/projects/cicy-code/api/mgr/tmux.go` — handleSend (`callback_to` parameter)
- `/home/cicy/projects/cicy-code/api/mgr/agent_inspector.go` — prompt injection (global/project/agent/files)
- `/home/cicy/cicy-ai/skills/cicy-skills/internal/hosttools/hosttools.go` — CLI `msg --callback`, `reply [--full]`
- `/home/cicy/projects/cicy-code/skills/internal/hosttools/hosttools.go` — same (in-tree copy)
