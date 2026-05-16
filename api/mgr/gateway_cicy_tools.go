package main

import (
	"fmt"
	"strings"
)

// cicyGatewayTool is a tool the gateway injects into every request.
type cicyGatewayTool struct {
	Name        string
	Description string
	Parameters  map[string]interface{} // JSON Schema
}

var cicyGatewayTools = []cicyGatewayTool{
	{
		Name: "cicy_agent_tmux_ls",
		Description: `List every tmux pane that the cicy runtime is currently managing on this host.

Use this to discover which panes exist right now and what is actually running inside each one. Each row corresponds to a single agent pane (one CLI agent per pane). The output is tab-separated with a header row, columns:
  pane_id      Short pane identifier (e.g. "w-10001"). This is the canonical handle you pass to other cicy_agent_* tools.
  agent_type   The CLI runtime in that pane: claude / codex / opencode / cursor / copilot / hermes / openclaw / cicy-claude / kiro-cli.
  title        Human-friendly label set by the operator (may be Chinese).

When to use:
  - You need to find another agent to talk to and don't yet know its pane_id.
  - You want to verify a pane is actually alive before sending it a message.
  - You want a quick inventory of the multi-agent environment.

Equivalent shell fallback if this tool fails: ` + "`cicy-agent ls`" + ` (same content; tabular).

Returns plain text with the header + one row per pane. No arguments.`,
		Parameters: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"required":             []interface{}{},
			"additionalProperties": false,
		},
	},
	{
		Name: "cicy_agent_list",
		Description: `List all configured cicy agents on this host with their pane_id, agent_type, title and workspace path.

This is the structured "who is who" view of the multi-agent setup. Unlike cicy_agent_tmux_ls (which is a tmux-level snapshot), this reflects the agent_config database — the authoritative source of truth for cicy panes. Each row is one agent.

Columns (tab-separated, with header):
  id          Short pane_id, e.g. "w-10003". This is the handle for cicy_agent_msg / cicy_agent_capture.
  agent_type  Which CLI runtime the pane runs (claude / codex / opencode / …).
  title       Operator-facing label.
  workspace   Absolute path of the agent's working directory (e.g. /home/cicy/cicy-ai/workers/w-10003).

When to use:
  - You need to map a pane_id to its agent_type before crafting a message (codex talks differently from claude).
  - You need the workspace path of another agent (e.g. to compare CLAUDE.md / AGENTS.md).
  - You're orchestrating multiple agents and need the full roster.

Conventions:
  - The primary/master pane is typically w-10001.
  - cicy-claude is a separate agent_type from claude (different runtime).
  - Hidden / system panes are NOT included; this returns only configured agents.

Equivalent shell fallback: ` + "`cicy-agent list`" + `. No arguments.`,
		Parameters: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"required":             []interface{}{},
			"additionalProperties": false,
		},
	},
	{
		Name: "cicy_agent_msg",
		Description: `Send a chat message to another cicy agent pane and have it processed by that agent's CLI (claude / codex / opencode / …).

This delivers the text to the target pane the same way an operator typing in the chat UI would. The remote agent receives the message as a user turn and starts working on it. By default this tool does NOT wait for a reply — set callback=true to be notified when the receiver finishes; otherwise poll yourself via cicy_agent_capture / cicy_agent_get_last_reply.

Typical workflow for cross-agent collaboration:
  1. Resolve the target pane via cicy_agent_list.
  2. Send your prompt via cicy_agent_msg with callback=true.
  3. Wait. A single line lands in your own pane (because the next user-turn in your own conversation will arrive carrying it):
       [<receiver_id>] ✅ work done    → receiver's turn finished cleanly
       [<receiver_id>] ❌ failed       → receiver's turn errored / upstream HTTP >= 400
  4. Read the actual answer based on which notification you got:
       • "✅ work done": call cicy_agent_get_last_reply(pane_id=<receiver_id>) — this is the cheap, accurate path. It returns the receiver's structured reply (thinking + final text, optionally tool_use). DO NOT scrape the terminal in this case.
       • "❌ failed": call cicy_agent_capture(pane_id=<receiver_id>) — there may be no reply.json content for this turn yet (e.g. validation error before any tokens stream), or the CLI may have shown an error banner in the pane. Capture is more forgiving here.
  5. Decide whether to follow up. When you reply or send a follow-up, generally callback=false — otherwise you'll trigger another callback in response to your own response, ping-ponging until somebody breaks the chain.

About callback (when true):
  - The server queues your pane (auto-resolved from $X_AGENT_SHORT_ID, which every cicy pane sets in its boot script) onto the receiver's pending list.
  - The next reply turn the receiver starts will attach a one-shot hook; when that turn finalizes (status=completed or failed), the hook sends one line into your pane and unregisters itself. The hook is bound to that specific turn — there is no time race with turns that were already in flight when you registered.
  - The callback message itself does NOT carry callback=true, so no loops.

Cautions:
  - Don't message yourself — the server ignores the callback when receiver == sender, but the message text still gets delivered to your own input.
  - Each agent has its own context; the receiver does not see your conversation history. Spell out everything they need to answer.
  - If the receiver pane is busy with an unrelated turn, your message queues; the callback fires on the NEW turn that processes your message, not the in-flight one.
  - On "failed": don't blindly retry — capture first, read the error, then decide.

Equivalent shell fallback: ` + "`cicy-agent msg <pane_id> \"<text>\" [--callback]`" + ` (CLI reads $X_AGENT_SHORT_ID itself).`,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pane_id": map[string]interface{}{
					"type":        "string",
					"description": "Short pane id of the target agent, e.g. \"w-10003\". Get this from cicy_agent_list.",
				},
				"text": map[string]interface{}{
					"type":        "string",
					"description": "The message body to deliver to the target agent. Should be a complete prompt/question — it will be submitted as one user turn.",
				},
				"callback": map[string]interface{}{
					"type":        "boolean",
					"description": "When true, the server notifies your own pane (resolved from $X_AGENT_SHORT_ID) once the receiver's next reply turn finalizes. You'll get one line: \"[<receiver_id>] ✅ work done\" or \"[<receiver_id>] ❌ failed\". On ✅ use cicy_agent_get_last_reply; on ❌ use cicy_agent_capture. Default false. DON'T set true when answering an incoming callback notification (avoids ping-pong).",
				},
			},
			"required":             []interface{}{"pane_id", "text"},
			"additionalProperties": false,
		},
	},
	{
		Name: "cicy_agent_get_last_reply",
		Description: `Read another cicy agent's MOST RECENT gateway reply as structured text.

This is the right way to pick up another agent's answer after they finish — especially after you receive a "[<receiver>] work done" callback from cicy_agent_msg. It reads the receiver's reply.json (written by the gateway when the upstream model finishes) and renders it into a single block of plain text with labeled sections. Unlike cicy_agent_capture, it does NOT scrape terminal output, so spinners / ANSI escapes / scrolled-off content cannot corrupt the result.

Returned text layout (sections appear in the order they were produced):
  [thinking]
  <model's reasoning text — concatenation of any "thinking" items in this turn>

  [text]
  <model's final answer text — what would render to the user>

  # only when full=true:
  [tool_use name=<tool_name> id=<call_id>]
  <JSON-encoded input>

When to use full vs default:
  - Default (full=false): you only want the answer the receiver gave. Cheaper to parse, no tool noise. Use this 95% of the time.
  - full=true: you also need to know HOW the receiver got there — which tools they called and with what arguments. Useful when debugging why an agent gave a weird answer, or when chaining tool-driven workflows.

Return structure (JSON):
  {
    "pane_id":       "<receiver>",
    "status":        "completed" | "failed" | ...,
    "turn_id":       "<id of this turn>",
    "started_at":    "RFC3339 timestamp",
    "updated_at":    "RFC3339 timestamp",
    "input_tokens":  N,
    "output_tokens": N,
    "total_tokens":  N,
    "full":          bool,
    "text":          "<rendered structured text — primary field to read>"
  }

When to NOT use this:
  - The receiver's turn FAILED (status=failed). reply.json may be empty or missing for that turn; call cicy_agent_capture instead — terminal output typically shows the error banner.
  - You want the full multi-turn history of the conversation (this returns only the most recent turn).

Equivalent shell fallback: ` + "`cicy-agent reply <pane_id> [--full]`" + `.`,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pane_id": map[string]interface{}{
					"type":        "string",
					"description": "Short pane id whose last reply to fetch, e.g. \"w-10003\".",
				},
				"full": map[string]interface{}{
					"type":        "boolean",
					"description": "When true, include tool_use items (name + JSON input) in the rendered text. When false (default), only thinking + final answer text.",
				},
			},
			"required":             []interface{}{"pane_id"},
			"additionalProperties": false,
		},
	},
	{
		Name: "cicy_agent_send_key_event",
		Description: `Send a raw tmux key event (Enter, Ctrl+C, etc.) to another agent's pane.

This is a lower-level escape hatch for when cicy_agent_msg's normal text-submit flow isn't enough. Use it when:
  - The receiver's CLI is showing an interactive prompt (permission ask, choice list, "press any key") that won't be cleared by another chat message.
  - You sent text via cicy_agent_msg but capture shows the text still sitting in the input box unsubmitted — push another "Enter" to confirm.
  - You need to interrupt a stuck pane: send "C-c" to send SIGINT to the foreground process.
  - You want to dismiss a Claude Code permission menu by sending "1" then "Enter".

Key syntax follows tmux's send-keys convention:
  - Single key names: "Enter", "Escape", "Tab", "Space", "BSpace", "Up", "Down", "Left", "Right", "PageUp", "PageDown", "Home", "End"
  - Control modifiers: "C-c" (Ctrl+C), "C-d" (Ctrl+D), "C-l" (Ctrl+L)
  - Combos: send each key as a separate call, or space-separated in one string e.g. "Up Up Enter".
  - Literal characters: pass them directly, e.g. "y" or "1".

Cautions:
  - This is a raw input event — it does NOT go through the chat-submit confirmation logic that cicy_agent_msg uses. Use sparingly.
  - On agents like claude-code, "C-c" twice exits the CLI. Don't send unless you mean to kill the session.
  - There is no callback — the result is fire-and-forget. Use cicy_agent_capture after to verify the effect.

Equivalent shell fallback: ` + "`cicy-agent send-keys <pane_id> <keys>`" + `.`,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pane_id": map[string]interface{}{
					"type":        "string",
					"description": "Short pane id of the target, e.g. \"w-10003\".",
				},
				"key": map[string]interface{}{
					"type":        "string",
					"description": "Key or key sequence in tmux send-keys syntax. Examples: \"Enter\", \"C-c\", \"Escape\", \"y\", \"1\", \"Up Up Enter\".",
				},
			},
			"required":             []interface{}{"pane_id", "key"},
			"additionalProperties": false,
		},
	},
	{
		Name: "cicy_agent_get_chat_history",
		Description: `Read a past turn from another cicy agent's chat history, indexed from the most recent.

The agent's per-pane SQLite at <workspace>/.cicy/history/history.db keeps every completed turn (one row per question+answer pair). This tool retrieves ONE specific row by reverse-chronological index:
  index=0  → the most recent finalized turn
  index=1  → the second most recent
  index=2  → the third most recent
  index=N  → counted from the end

Use this when you need:
  - The receiver's previous answer to a different question (cicy_agent_get_last_reply only gives the latest reply, not historical ones).
  - To see how a conversation evolved across multiple turns.
  - To pick up an older answer after several follow-ups have happened.

Returned shape:
  {
    "pane_id":       "<receiver>",
    "index":         <N>,
    "found":         true | false,
    "conversation_id": "...",
    "turn_id":       "...",        # turn_key in storage
    "q":             "<the user question of that turn>",
    "a":             "<the assistant final answer>",
    "thinking":      "<the assistant thinking, if any>",
    "model":         "<model name>",
    "status":        "completed" | "failed" | ...,
    "q_time":        "<RFC3339>",
    "a_time":        "<RFC3339>",
    "created_at":    "<RFC3339>"
  }

If index is out of range (history shorter than index+1), the response has "found": false and empty fields — not an error.

When to NOT use this:
  - You want the CURRENT in-flight reply: use cicy_agent_get_last_reply (reads reply.json, not history.db; reply.json reflects the latest turn whether or not it's been committed to history yet).
  - You want to see the live terminal state: use cicy_agent_capture.

Equivalent shell fallback: ` + "`cicy-agent history <pane_id> <index>`" + `.`,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pane_id": map[string]interface{}{
					"type":        "string",
					"description": "Short pane id of the target, e.g. \"w-10003\".",
				},
				"index": map[string]interface{}{
					"type":        "integer",
					"minimum":     0,
					"description": "Reverse-chronological index (0=most recent finalized turn, 1=second most recent, …).",
				},
			},
			"required":             []interface{}{"pane_id", "index"},
			"additionalProperties": false,
		},
	},
	{
		Name: "cicy_agent_capture",
		Description: `Capture the current visible terminal output of another cicy agent pane.

Returns whatever is currently rendered in the target pane's terminal — including the active CLI's prompt, conversation history, status indicators (e.g. "Thinking", "Build · …"), and any in-progress response. This is the only way to read raw pane state.

Prefer cicy_agent_get_last_reply when you just want the receiver's last ANSWER (cleaner output, no terminal escapes, structured sections). Reach for capture in three cases:
  1. The receiver's last turn FAILED (e.g. you got a "[<id>] failed" callback) — reply.json may be empty or absent for that turn, so terminal scrollback is the more reliable source for the error message / banner.
  2. You need to check live state (is it still spinning? did it crash? did it hit a permission prompt?). reply.json only updates on finalize, not while a turn is in flight.
  3. You want to see operator input or other panes' tool calls that aren't part of the reply itself.

Interpreting capture output:
  - The output is the raw terminal scrollback (tail of the pane). Older content may already be scrolled off.
  - Active spinner characters like ⠋ ⠙ ⠹ ⠸ indicate the agent is still working.
  - A clean prompt line (e.g. "❯" for claude, "›" for codex/opencode) means the agent is idle.
  - ANSI/terminal-drawing characters may appear; agents typically tolerate them in further analysis.
  - For long replies, capture shows only the TAIL — get_last_reply gives you the full answer.

Performance: cheap; safe to call repeatedly when polling.

Equivalent shell fallback: ` + "`cicy-agent capture <pane_id>`" + `.`,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pane_id": map[string]interface{}{
					"type":        "string",
					"description": "Short pane id whose terminal output to read, e.g. \"w-10003\". Get this from cicy_agent_list.",
				},
			},
			"required":             []interface{}{"pane_id"},
			"additionalProperties": false,
		},
	},
}

// injectCicyToolDefs appends cicy tool definitions to payload["tools"].
// provider "anthropic" → {name, description, input_schema}
// anything else (openai)  → {type:"function", function:{name, description, parameters}}
func injectCicyToolDefs(payload map[string]interface{}, provider string) map[string]interface{} {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	existing, _ := payload["tools"].([]interface{})
	prepend := make([]interface{}, 0, len(cicyGatewayTools))
	for _, t := range cicyGatewayTools {
		if provider == "anthropic" {
			prepend = append(prepend, map[string]interface{}{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.Parameters,
			})
		} else {
			prepend = append(prepend, map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			})
		}
	}
	payload["tools"] = append(prepend, existing...)
	return payload
}

func cicyToolTmuxLs() string {
	rows, err := store.Query(
		`SELECT pane_id, title, agent_type FROM agent_config ORDER BY pane_id`)
	if err != nil {
		return fmt.Sprintf("error querying panes: %v", err)
	}
	defer rows.Close()
	var lines []string
	lines = append(lines, "pane_id\tagent_type\ttitle")
	lines = append(lines, "-------\t----------\t-----")
	for rows.Next() {
		var paneID, title, agentType string
		if err := rows.Scan(&paneID, &title, &agentType); err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", shortPaneID(paneID), agentType, title))
	}
	return strings.Join(lines, "\n")
}

func cicyToolAgentList() string {
	rows, err := store.Query(
		`SELECT pane_id, agent_type, title, workspace FROM agent_config ORDER BY pane_id`)
	if err != nil {
		return fmt.Sprintf("error querying agents: %v", err)
	}
	defer rows.Close()
	var lines []string
	lines = append(lines, "id\tagent_type\ttitle\tworkspace")
	lines = append(lines, "--\t----------\t-----\t---------")
	for rows.Next() {
		var paneID, agentType, title, workspace string
		if err := rows.Scan(&paneID, &agentType, &title, &workspace); err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s", shortPaneID(paneID), agentType, title, workspace))
	}
	return strings.Join(lines, "\n")
}
