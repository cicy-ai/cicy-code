# cicy_agent_* Tools — Test Cases

Validation set for the gateway-injected cross-agent tools and their backing endpoints. Each case is **IM-style**: a small concrete task you send to one pane and verify by inspecting another pane / endpoint. None of these require the receiver to modify cicy-code source.

## How to run

1. Pick a sender pane and a receiver pane from the available IDs (`cicy-agent list`).
2. Send the task prompt to the sender via `cicy-agent msg <sender> "<prompt>"`.
3. Watch the gateway log `/home/cicy/logs/dev-server.log` for `[reply-callback]` events.
4. Verify with the listed acceptance criteria.

---

## T01 — list configured agents

**Tests**: `cicy_agent_list` injection, agent uses bash CLI fallback.

**Prompt** (to any agent):
```
用 cicy-agent list 列出所有 agent，告诉我有几个 claude 类型、几个 codex 类型。
```

**Pass**:
- Response contains the correct counts from `cicy-agent list | awk '$2=="claude"' | wc -l` etc.
- Response references both agent_types it found.

---

## T02 — tmux snapshot

**Tests**: `cicy_agent_tmux_ls` injection.

**Prompt**:
```
用 cicy-agent ls 列出当前 tmux 的 panes，告诉我 w-10001 当前在跑什么命令。
```

**Pass**: Response mentions the actual command shown in the `cicy-agent ls` row for `w-10001`.

---

## T03 — capture pane state

**Tests**: `cicy_agent_capture`.

**Prompt** (to A):
```
用 cicy-agent capture w-10001 抓一下主 pane 的当前终端，最后一行是什么？
```

**Pass**: Response quotes the actual last terminal line.

---

## T04 — send simple message (no callback)

**Tests**: `cicy_agent_msg` happy path.

**Prompt** (to A):
```
用 cicy-agent msg w-10003 "hello from <你的 id>"，然后告诉我 cicy-agent 返回的 JSON。
```

**Pass**:
- A reports `{"success":true,"win_id":"w-10003"}`
- `cicy-agent capture w-10003` shows the literal "hello from …" line.

---

## T05 — message + callback (simple math, no tool use)

**Tests**: `--callback` for a single-turn no-tool reply (callback fires immediately, no defer).

**Prompt** (to A):
```
用 cicy-agent msg w-10022 '用一句话告诉我 1+2+3+...+100 等于多少' --callback
然后等本 pane 收到 [w-10022] ✅ work done，再用 cicy-agent reply w-10022 把答案告诉我。
```

**Pass**:
- Gateway log: `registered pane=w-10022 → ...` then directly `fired pane=w-10022 → ... status=completed` (no `defer`).
- A's pane receives `[w-10022] ✅ work done`.
- `cicy-agent reply w-10022` text contains "5050".

---

## T06 — message + callback with tool use (deferred path)

**Tests**: deferred callback — callback must NOT fire mid-tool-use; only after the receiver's final answer.

**Prompt** (to A):
```
用 cicy-agent msg w-10021 '请用 Write 工具写 /tmp/cicy-tc06.py 内容 print("ok")，然后用 Bash 跑一下，把输出贴给我' --callback
等收到 [w-10021] ✅ work done 后，用 cicy-agent reply w-10021 --full 告诉我 claude 调了几个 tool。
```

**Pass**:
- Gateway log: one or more `defer pane=w-10021 → ... (tool_calls=N in flight)` BEFORE the eventual `fired ... status=completed`.
- Reply text (with `--full`) contains `[tool_use name=Write ...]` and `[tool_use name=Bash ...]`.
- Final `[text]` section mentions the script output `ok`.

---

## T07 — failure callback

**Tests**: callback fires `❌ failed` when receiver's turn errors out.

**Setup**: temporarily point a pane at a non-existent provider, or send a deliberately malformed request body that the upstream rejects.

**Prompt** (to A):
```
用 cicy-agent msg <broken_pane> "test" --callback，等通知。
```

**Pass**:
- Gateway log: `fired pane=<broken_pane> → ... status=failed`.
- A's pane receives `[<broken_pane>] ❌ failed`.
- `cicy-agent capture <broken_pane>` shows the upstream error banner.

---

## T08 — get_last_reply structured output

**Tests**: `cicy_agent_get_last_reply` (CLI `reply`) without `--full`.

**Prompt** (to A):
```
用 cicy-agent reply w-10003 把 opencode 最近一次回复的结构化文本给我。
```

**Pass**: JSON response contains `text` field with `[thinking]\n…\n\n[text]\n…` structure. No `[tool_use ...]` blocks (without `--full`).

---

## T09 — get_last_reply --full includes tool_use

**Tests**: `--full` mode adds tool_use entries.

**Prompt** (to A):
```
用 cicy-agent reply w-10003 --full 取 opencode 最近一次完整记录，列出它调用过哪些 tool。
```

**Pass**: Returned `text` contains one or more `[tool_use name=… id=…]` blocks if the last turn had tool use; otherwise only `[thinking]` and `[text]`.

---

## T10 — get_chat_history reverse index

**Tests**: `cicy_agent_get_chat_history` returning previous turns.

**Direct check** (no agent needed):
```bash
TOKEN=$(jq -r .api_token ~/cicy-ai/global.json)
for i in 0 1 2 5; do
  curl -s -X POST http://127.0.0.1:8008/api/tmux/chat_history \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d "{\"pane_id\":\"w-10003\",\"index\":$i}" | jq '{idx:.index, found, q:.q[0:40], a:.a[0:40], turn_id}'
done
```

**Pass**:
- Index 0 returns the most recent finalized turn.
- Index N+1 is strictly older than index N (turn_id changes).
- Out-of-range index returns `{found: false}` with no error.

---

## T11 — send_key_event Enter recovery

**Tests**: `cicy_agent_send_key_event` to push Enter when text is stuck in input.

**Setup**: with `--submit=false` (bypass auto-Enter), insert text into a pane:
```bash
TOKEN=$(jq -r .api_token ~/cicy-ai/global.json)
curl -s -X POST http://127.0.0.1:8008/api/tmux/send \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"pane_id":"w-10003","text":"stuck-input-test","submit":false}'
```

Confirm text sits in input box via `cicy-agent capture w-10003`.

Push Enter:
```bash
curl -s -X POST http://127.0.0.1:8008/api/tmux/send-keys \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"pane_id":"w-10003","keys":"Enter"}'
```

**Pass**: After Enter, capture shows the prompt was submitted (no longer in input box).

---

## T12 — send_key_event control sequences

**Tests**: special key syntax (`C-c`, `C-l`, `Up Up Enter`).

**Cases**:
- `keys=C-c` to a busy pane → interrupts.
- `keys=C-l` → clears terminal.
- `keys="Up Up Enter"` → recalls history twice and submits.

**Pass**: Each behaves as the equivalent direct tmux send-keys would.

---

## T13 — three-agent collaboration (architect → worker → reviewer)

**Tests**: full integration — `msg --callback` + deferred firing + `reply` across 3 panes.

**Prompt** (to codex):
```
你是架构师。任务：写一个 /tmp/cicy-greet.py，接受一个名字参数，打印 "Hello, <name>!"。
1) 用 /home/cicy/.local/bin/cicy-agent msg w-10021 '<implement + run python3 /tmp/cicy-greet.py World>' --callback
2) 收到 [w-10021] ✅ work done 后，用 reply w-10021 取结构化输出
3) 把 claude 实现要点发给 opencode: /home/cicy/.local/bin/cicy-agent msg w-10003 '<paste code, ask short review>' --callback
4) 收到 [w-10003] ✅ work done 后，用 reply w-10003 取 review
5) 给我一段中文总结：spec、实现要点、review 建议
```

**Pass**:
- Gateway log shows 2 register→fire pairs (codex→claude, codex→opencode).
- Codex's final summary cites BOTH claude's implementation and opencode's review.
- `/tmp/cicy-greet.py` exists and runs correctly.

---

## T14 — self-callback ignored

**Tests**: server ignores callback when sender == receiver.

**Setup**: from inside pane X, send `cicy-agent msg X "..." --callback`.

**Pass**:
- Gateway log: NO `registered pane=X → X` line.
- X's pane receives the text but no `[X] ✅ work done` notification afterwards.

---

## T15 — repeat callback ping-pong protection

**Tests**: callback notification messages themselves do NOT carry `--callback`, so they cannot trigger an infinite loop.

**Verification**: any test that exercises a callback (T05, T06, T13) and observes ONE callback notification — there should NOT be a second `[receiver] ✅ work done` following the first one within the same interaction.

---

## Acceptance summary

| Tool | Covered by |
|------|-----------|
| `cicy_agent_list` | T01 |
| `cicy_agent_tmux_ls` | T02 |
| `cicy_agent_capture` | T03 |
| `cicy_agent_msg` (no cb) | T04 |
| `cicy_agent_msg` (cb, no tools) | T05 |
| `cicy_agent_msg` (cb, tool use → defer) | T06, T13 |
| `cicy_agent_msg` (cb, failure) | T07 |
| `cicy_agent_get_last_reply` (default) | T08 |
| `cicy_agent_get_last_reply` (`--full`) | T09 |
| `cicy_agent_get_chat_history` | T10 |
| `cicy_agent_send_key_event` (Enter) | T11 |
| `cicy_agent_send_key_event` (special keys) | T12 |
| Self-callback guard | T14 |
| Ping-pong guard | T15 |
| Multi-agent collaboration | T13 |
