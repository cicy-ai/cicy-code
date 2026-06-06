package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The dispatcher is a lightweight, non-coding agent type: a task secretary
// that records the user's needs, turns them into todos, dispatches work to
// other agents and tracks progress. It runs NO heavyweight CLI — its tmux
// pane hosts a tiny REPL (`cicy-code dispatcher-repl`, see dispatcher_repl.go)
// that forwards each input line to POST /api/dispatcher/chat below.
//
// All LLM traffic goes through the unified local AI gateway
// (/api/ai-gateway/anthropic/<agent-id>/v1/messages) so provider/model
// routing (default_model, runtime_ai overrides, model mapping), protocol
// adaptation (DeepSeek ↔ Anthropic), auditing, history and the live reply
// mirror behave exactly like every other agent.
//
// Tools are executed in-process (todo store, pane list, tmux send/capture) —
// no shell, no subprocesses beyond tmux itself.

const dispatcherGatewayBase = "http://127.0.0.1:8008"

// defaultDispatcherCharter seeds the dispatcher's AGENTS.md at agent
// creation. The full PM working protocol lives in dispatcherSystemPromptBase
// (compiled); this file is the per-instance customization hook — anything the
// user writes here is appended to the system prompt and takes effect on the
// next turn, no restart. Placeholders are substituted by
// writeAgentGuidanceFile.
const defaultDispatcherCharter = `# {{AGENT_ID}} · 产品项目经理

<!-- 这里写对这位 PM 的个性化要求(团队约定、汇报口径、偏好等),改完下一轮对话即生效。 -->
`

// dispatcherSystemPromptBase is the fixed, non-editable part of the system
// prompt: tool semantics and dispatch policy. The charter (AGENTS.md) is
// appended after it.
const dispatcherSystemPromptBase = `你是这个多 agent 工作区的产品项目经理(PM),用户的唯一接口人。

# 你的位置
团队由各类 coding agent 组成(架构师、全栈工程师、修 bug 等),各自在自己的终端里异步工作。
用户不直接管理他们——一切经过你。你自己不执行任何具体工作:不写代码、不设计架构、不做技术拆分。
架构与技术拆分由架构师产出;你的本事是读懂结论,落成任务,派给对的人,并对结果负责。

# 职责闭环
1. 需求 → 任务:听懂用户要什么,登记为一条粗粒度 todo(todo_add),不自行展开技术细节。
   需求不清就问,一次只问最关键的一个问题,绝不带着歧义往下派。
2. 分派:用 agent_list 看团队,凭 title/agent_type 判断谁合适。
   分派 = todo_update 改 owner + agent_msg 发任务简报,二者缺一不可。
   简报必须包含:todo 编号、要做什么、完成标准、做完把该 todo 标为 test。
3. 跟踪:用户问进度时,先 todo_list 看全局,有疑问再 agent_capture 看终端现场。
   汇报只给结论:谁在做什么、到哪一步、是否卡住、需不需要用户决策。
4. 收口:用户确认后 todo_update 标 done;明确放弃的标 dropped。todo 列表必须始终反映真实状态。

# 行为准则
- 默认中文。短、直接、先结论。动作完成后一句话确认(记了什么/派给了谁),不复述过程。
- 多条信息用紧凑列表或表格,不写客套话和空段落。
- 不臆造团队状态——任何关于任务/agent 的结论都必须来自工具返回,没查过就说没查。
- 技术方案、代码细节类问题不要自己答,指给对应的 agent 或建议用户派人调研。
- 同类需求重复出现时主动提醒已有相关 todo,避免重复登记。

# 工具语义
- todo 状态机: todo → test → done;dropped=废弃。所有 todo 在共享存储,id 全局唯一。
- agent_msg 是异步的:发出后对方不会立刻回复,结果要靠之后 agent_capture / todo_list 查证。
- agent_capture 返回目标终端最后 N 行,是判断"在干嘛/卡没卡"的唯一现场证据。`

// ── conversation state ──────────────────────────────────────────────────────

type dispatcherSession struct {
	mu       sync.Mutex
	messages []M // anthropic-format messages, persisted to disk
}

var (
	dispatcherSessionsMu sync.Mutex
	dispatcherSessions   = map[string]*dispatcherSession{}
)

const dispatcherMaxHistoryMessages = 60
const dispatcherMaxToolRounds = 8

// dispatcherTrimMessages keeps history within the cap, trimming from the front,
// but NEVER lets the window start on a tool_result turn — Anthropic/DeepSeek
// reject a tool_result whose matching tool_use was trimmed away ("each
// tool_result must have a corresponding tool_use in the previous message").
// Must be applied to the OUTGOING window every turn (before building the
// request), not just on persist — otherwise a freshly-loaded over-cap window can
// be sent with an orphan tool_result at the front → gateway 400 / no reply.
func dispatcherTrimMessages(msgs []M) []M {
	if len(msgs) <= dispatcherMaxHistoryMessages {
		return msgs
	}
	start := len(msgs) - dispatcherMaxHistoryMessages
	for start < len(msgs) && dispatcherMessageHasToolResult(msgs[start]) {
		start++
	}
	return append([]M{}, msgs[start:]...)
}

func dispatcherStateDir(workspace string) string {
	return filepath.Join(workspace, ".cicy", "dispatcher")
}

func dispatcherHistoryPath(workspace string) string {
	return filepath.Join(dispatcherStateDir(workspace), "conversation.json")
}

func getDispatcherSession(shortID, workspace string) *dispatcherSession {
	dispatcherSessionsMu.Lock()
	defer dispatcherSessionsMu.Unlock()
	if s, ok := dispatcherSessions[shortID]; ok {
		return s
	}
	s := &dispatcherSession{}
	if raw, err := os.ReadFile(dispatcherHistoryPath(workspace)); err == nil {
		var msgs []M
		if json.Unmarshal(raw, &msgs) == nil {
			s.messages = msgs
		}
	}
	dispatcherSessions[shortID] = s
	return s
}

// persistLocked writes the trimmed history to disk. Caller holds s.mu.
func (s *dispatcherSession) persistLocked(workspace string) {
	s.messages = dispatcherTrimMessages(s.messages)
	if err := os.MkdirAll(dispatcherStateDir(workspace), 0755); err != nil {
		return
	}
	raw, err := json.Marshal(s.messages)
	if err != nil {
		return
	}
	_ = os.WriteFile(dispatcherHistoryPath(workspace), raw, 0644)
}

func dispatcherMessageHasToolResult(msg M) bool {
	blocks, ok := msg["content"].([]interface{})
	if !ok {
		return false
	}
	for _, b := range blocks {
		if bm, ok := b.(map[string]interface{}); ok {
			if t, _ := bm["type"].(string); t == "tool_result" {
				return true
			}
		}
	}
	return false
}

// ── tools ───────────────────────────────────────────────────────────────────

// dispatcherToolDefs returns the tool defs the agent's profile enables. An
// empty/ nil enabled set yields no tools (pure-chat roles).
func dispatcherToolDefs(enabled map[string]bool) []M {
	all := dispatcherAllToolDefs()
	if len(enabled) == 0 {
		return nil
	}
	out := make([]M, 0, len(all))
	for _, t := range all {
		if name, _ := t["name"].(string); enabled[name] {
			out = append(out, t)
		}
	}
	return out
}

func dispatcherAllToolDefs() []M {
	return []M{
		{
			"name":        "todo_add",
			"description": "Record a new task in the shared todo list. Optionally assign it to an agent by short pane id (e.g. w-10003).",
			"input_schema": M{
				"type": "object",
				"properties": M{
					"title":   M{"type": "string", "description": "Short imperative task title"},
					"pane_id": M{"type": "string", "description": "Optional owner agent short id"},
				},
				"required": []string{"title"},
			},
		},
		{
			"name":        "todo_list",
			"description": "List todos. Optional status filter: todo|test|done|dropped.",
			"input_schema": M{
				"type": "object",
				"properties": M{
					"status": M{"type": "string"},
				},
			},
		},
		{
			"name":        "todo_update",
			"description": "Update a todo's status (todo|test|done|dropped) and/or reassign its owner.",
			"input_schema": M{
				"type": "object",
				"properties": M{
					"id":      M{"type": "string", "description": "Todo id or unique prefix"},
					"status":  M{"type": "string"},
					"pane_id": M{"type": "string", "description": "New owner agent short id"},
				},
				"required": []string{"id"},
			},
		},
		{
			"name":        "agent_list",
			"description": "List all agents in the workspace: short id, title, agent type, active flag.",
			"input_schema": M{
				"type":       "object",
				"properties": M{},
			},
		},
		{
			"name":        "agent_msg",
			"description": "Send a task or question to another agent's terminal. The agent will act on it asynchronously.",
			"input_schema": M{
				"type": "object",
				"properties": M{
					"pane_id": M{"type": "string", "description": "Target agent short id, e.g. w-10003"},
					"text":    M{"type": "string", "description": "The message / task brief to send"},
				},
				"required": []string{"pane_id", "text"},
			},
		},
		{
			"name":        "agent_capture",
			"description": "Capture the last lines of another agent's terminal to check its progress.",
			"input_schema": M{
				"type": "object",
				"properties": M{
					"pane_id": M{"type": "string", "description": "Target agent short id"},
					"lines":   M{"type": "integer", "description": "How many trailing lines (default 40, max 200)"},
				},
				"required": []string{"pane_id"},
			},
		},
	}
}

func dispatcherRunTool(selfShortID, name string, input map[string]interface{}, enabled map[string]bool) string {
	if !enabled[name] {
		return "error: tool " + name + " is not enabled for this agent"
	}
	str := func(key string) string {
		v, _ := input[key].(string)
		return strings.TrimSpace(v)
	}
	switch name {
	case "todo_add":
		title := str("title")
		if title == "" {
			return "error: title required"
		}
		ws := masterWorkspaceForTodo()
		if ws == "" {
			return "error: master workspace unavailable"
		}
		todoMu.Lock()
		defer todoMu.Unlock()
		todos, err := loadTodos(ws)
		if err != nil {
			return "error: " + err.Error()
		}
		// Default the owner to the dispatcher's own pane: an empty PaneID is
		// invisible in every pane-filtered view (UI lists by pane), so an
		// "unassigned" todo would silently disappear until reassigned.
		owner := shortPaneID(normPaneID(str("pane_id")))
		if owner == "" {
			owner = selfShortID
		}
		now := time.Now().UTC().Truncate(time.Second)
		item := Todo{
			ID:        nextTodoID(todos),
			Title:     title,
			Status:    "todo",
			PaneID:    owner,
			CreatorID: selfShortID,
			CreatedAt: now,
			UpdatedAt: now,
		}
		todos = append(todos, item)
		if err := saveTodos(ws, todos); err != nil {
			return "error: " + err.Error()
		}
		return fmt.Sprintf("created todo #%s: %s (owner=%s)", item.ID, item.Title, orDash(item.PaneID))
	case "todo_list":
		ws := masterWorkspaceForTodo()
		if ws == "" {
			return "error: master workspace unavailable"
		}
		todoMu.Lock()
		todos, err := loadTodos(ws)
		todoMu.Unlock()
		if err != nil {
			return "error: " + err.Error()
		}
		filter := str("status")
		sortTodos(todos)
		var b strings.Builder
		count := 0
		for _, t := range todos {
			if filter != "" && t.Status != filter {
				continue
			}
			fmt.Fprintf(&b, "#%s [%s] %s (owner=%s)\n", t.ID, t.Status, t.Title, orDash(t.PaneID))
			count++
		}
		if count == 0 {
			return "no todos" + map[bool]string{true: " with status " + filter, false: ""}[filter != ""]
		}
		return strings.TrimRight(b.String(), "\n")
	case "todo_update":
		ws := masterWorkspaceForTodo()
		if ws == "" {
			return "error: master workspace unavailable"
		}
		todoMu.Lock()
		defer todoMu.Unlock()
		todos, err := loadTodos(ws)
		if err != nil {
			return "error: " + err.Error()
		}
		idx, err := resolveTodoID(todos, str("id"))
		if err != nil {
			return "error: " + err.Error()
		}
		changed := false
		if status := str("status"); status != "" {
			if !todoValidStatus[status] {
				return "error: invalid status " + status
			}
			todos[idx].Status = status
			changed = true
		}
		if pane := str("pane_id"); pane != "" {
			todos[idx].PaneID = shortPaneID(normPaneID(pane))
			changed = true
		}
		if !changed {
			return "error: nothing to update (pass status and/or pane_id)"
		}
		todos[idx].UpdatedAt = time.Now()
		if err := saveTodos(ws, todos); err != nil {
			return "error: " + err.Error()
		}
		return fmt.Sprintf("updated todo #%s: [%s] %s (owner=%s)", todos[idx].ID, todos[idx].Status, todos[idx].Title, orDash(todos[idx].PaneID))
	case "agent_list":
		rows, err := store.Query("SELECT pane_id, title, agent_type, COALESCE(active,0) FROM agent_config ORDER BY pane_id")
		if err != nil {
			return "error: " + err.Error()
		}
		defer rows.Close()
		var b strings.Builder
		for rows.Next() {
			var paneID, title, agentType string
			var active int
			if rows.Scan(&paneID, &title, &agentType, &active) != nil {
				continue
			}
			state := "inactive"
			if active != 0 {
				state = "active"
			}
			fmt.Fprintf(&b, "%s | %s | %s | %s\n", shortPaneID(paneID), title, agentType, state)
		}
		out := strings.TrimRight(b.String(), "\n")
		if out == "" {
			return "no agents"
		}
		return out
	case "agent_msg":
		pane := shortPaneID(normPaneID(str("pane_id")))
		text := str("text")
		if pane == "" || text == "" {
			return "error: pane_id and text required"
		}
		if pane == selfShortID {
			return "error: refusing to message myself"
		}
		if err := sendTextToPane(pane+":main.0", text, true); err != nil {
			return "error: " + err.Error()
		}
		return "dispatched to " + pane
	case "agent_capture":
		pane := shortPaneID(normPaneID(str("pane_id")))
		if pane == "" {
			return "error: pane_id required"
		}
		lines := 40
		if v, ok := input["lines"].(float64); ok && int(v) > 0 {
			lines = int(v)
		}
		if lines > 200 {
			lines = 200
		}
		out, err := runTmux("capture-pane", "-t", pane+":main.0", "-p", "-S", fmt.Sprintf("-%d", lines))
		if err != nil {
			return "error: " + err.Error()
		}
		out = strings.TrimRight(out, "\n ")
		if out == "" {
			return "(terminal empty)"
		}
		return out
	}
	return "error: unknown tool " + name
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// dispatcherCachedToolDefs returns the tool defs with a cache breakpoint on
// the last one (caches the whole tools prefix on Anthropic-protocol
// providers; the DeepSeek adapter ignores the extra key).
func dispatcherCachedToolDefs(enabled map[string]bool) []M {
	tools := dispatcherToolDefs(enabled)
	if len(tools) > 0 {
		tools[len(tools)-1]["cache_control"] = M{"type": "ephemeral"}
	}
	return tools
}

// dispatcherRequestMessages returns the history with a cache breakpoint
// attached to the final message — copy-on-write so the persisted history
// itself never carries cache_control (the breakpoint must move every turn).
func dispatcherRequestMessages(history []M) []M {
	if len(history) == 0 {
		return history
	}
	out := make([]M, len(history))
	copy(out, history)
	last := out[len(out)-1]
	role, _ := last["role"].(string)
	cc := M{"type": "ephemeral"}
	var blocks []interface{}
	switch c := last["content"].(type) {
	case string:
		blocks = []interface{}{M{"type": "text", "text": c, "cache_control": cc}}
	case []interface{}:
		if len(c) == 0 {
			return out
		}
		blocks = append([]interface{}{}, c...)
		if bm, ok := blocks[len(blocks)-1].(map[string]interface{}); ok {
			cp := map[string]interface{}{}
			for k, v := range bm {
				cp[k] = v
			}
			cp["cache_control"] = cc
			blocks[len(blocks)-1] = cp
		}
	case []M:
		if len(c) == 0 {
			return out
		}
		blocks = make([]interface{}, len(c))
		for i, bm := range c {
			blocks[i] = bm
		}
		cp := M{}
		for k, v := range c[len(c)-1] {
			cp[k] = v
		}
		cp["cache_control"] = cc
		blocks[len(blocks)-1] = cp
	default:
		return out
	}
	out[len(out)-1] = M{"role": role, "content": blocks}
	return out
}

// ── system prompt + model resolution ───────────────────────────────────────

func dispatcherModel(shortID string) string {
	var defaultModel string
	store.QueryRow("SELECT COALESCE(default_model,'') FROM agent_config WHERE pane_id=?", shortID+":main.0").Scan(&defaultModel)
	return resolveClaudeStartupModel(defaultModel, loadRuntimeAIConfig(), shortID)
}

// ── gateway round trip ──────────────────────────────────────────────────────

// dispatcherCallGateway makes one STREAMING Anthropic Messages call through
// the unified local AI gateway and assembles the SSE stream back into a full
// response object. Streaming matters twice: the gateway audit layer parses
// the SSE as it flows and updates reply.json + broadcasts ai_chunk chat
// events live (the web UI's token-by-token rendering), and `emit` forwards
// text deltas to the REPL's SSE channel so the terminal streams too.
// The DeepSeek adapter wraps Chat-Completions SSE back into Anthropic
// Messages SSE, so the consumption side is one format regardless of provider.
// The second return reports whether deltas were emitted (the caller must then
// not re-emit the assembled text blocks).
func dispatcherCallGateway(shortID string, payload M, emit func(M)) (map[string]interface{}, bool, error) {
	payload["stream"] = true
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	url := fmt.Sprintf("%s/api/ai-gateway/anthropic/%s/v1/messages", dispatcherGatewayBase, shortID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "cicy-local-gateway")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "text/event-stream")
	// Stable conversation id: the dispatcher is one long-lived conversation per
	// agent. Without this the audit layer has no session field in the body and
	// falls back to a fresh random conversation_id every turn (a sprawl of
	// one-turn conversation dirs). The audit layer reads this header.
	req.Header.Set("X-Claude-Code-Session-Id", "dispatcher-"+shortID)
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return nil, false, fmt.Errorf("gateway %d: %s", resp.StatusCode, truncateForLog(string(respBody), 400))
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		// Upstream ignored stream:true — parse the plain JSON response.
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, false, err
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return nil, false, fmt.Errorf("gateway response parse failed: %v", err)
		}
		return parsed, false, nil
	}
	return dispatcherAssembleSSE(resp.Body, emit)
}

// dispatcherAssembleSSE folds an Anthropic Messages SSE stream into the
// equivalent non-stream response object ({content: blocks, stop_reason}),
// forwarding text deltas to `emit` as they arrive. The bool return reports
// whether any delta was emitted.
func dispatcherAssembleSSE(r io.Reader, emit func(M)) (map[string]interface{}, bool, error) {
	type blockBuf struct {
		typ       string
		id        string
		name      string
		text      strings.Builder
		inputJSON strings.Builder
	}
	var bufs []*blockBuf
	stopReason := ""
	streamed := false
	blockAt := func(evt map[string]interface{}) *blockBuf {
		idx, ok := evt["index"].(float64)
		if !ok || idx < 0 || idx > 1024 {
			return nil
		}
		for len(bufs) <= int(idx) {
			bufs = append(bufs, &blockBuf{})
		}
		return bufs[int(idx)]
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var evt map[string]interface{}
		if json.Unmarshal([]byte(data), &evt) != nil {
			continue
		}
		switch evt["type"] {
		case "content_block_start":
			b := blockAt(evt)
			if b == nil {
				continue
			}
			if cb, ok := evt["content_block"].(map[string]interface{}); ok {
				b.typ, _ = cb["type"].(string)
				b.id, _ = cb["id"].(string)
				b.name, _ = cb["name"].(string)
				if t, ok := cb["text"].(string); ok {
					b.text.WriteString(t)
				}
			}
		case "content_block_delta":
			b := blockAt(evt)
			if b == nil {
				continue
			}
			if d, ok := evt["delta"].(map[string]interface{}); ok {
				switch d["type"] {
				case "text_delta":
					if b.typ == "" {
						b.typ = "text"
					}
					if t, ok := d["text"].(string); ok {
						b.text.WriteString(t)
						if emit != nil && t != "" {
							emit(M{"type": "text_delta", "text": t})
							streamed = true
						}
					}
				case "input_json_delta":
					if t, ok := d["partial_json"].(string); ok {
						b.inputJSON.WriteString(t)
					}
				}
			}
		case "content_block_stop":
			// Close the line after a streamed text block so the next event
			// (tool chip / prompt) starts on a fresh line in the terminal.
			if b := blockAt(evt); b != nil && b.typ == "text" && streamed && emit != nil {
				emit(M{"type": "text_end"})
			}
		case "message_delta":
			if d, ok := evt["delta"].(map[string]interface{}); ok {
				if sr, ok := d["stop_reason"].(string); ok && sr != "" {
					stopReason = sr
				}
			}
		case "error":
			msg := data
			if em, ok := evt["error"].(map[string]interface{}); ok {
				if m, ok := em["message"].(string); ok && m != "" {
					msg = m
				}
			}
			return nil, streamed, fmt.Errorf("gateway stream error: %s", truncateForLog(msg, 400))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, streamed, fmt.Errorf("gateway stream read failed: %v", err)
	}

	blocks := make([]interface{}, 0, len(bufs))
	for _, b := range bufs {
		switch b.typ {
		case "text":
			if b.text.Len() > 0 {
				blocks = append(blocks, map[string]interface{}{"type": "text", "text": b.text.String()})
			}
		case "tool_use":
			raw := strings.TrimSpace(b.inputJSON.String())
			if raw == "" {
				raw = "{}"
			}
			var input map[string]interface{}
			if json.Unmarshal([]byte(raw), &input) != nil || input == nil {
				input = map[string]interface{}{}
			}
			blocks = append(blocks, map[string]interface{}{"type": "tool_use", "id": b.id, "name": b.name, "input": input})
		}
	}
	return map[string]interface{}{"content": blocks, "stop_reason": stopReason}, streamed, nil
}

func truncateForLog(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ── chat endpoint (SSE) ─────────────────────────────────────────────────────

type dispatcherSSE struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (s *dispatcherSSE) emit(event M) {
	raw, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(s.w, "data: %s\n\n", raw)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// handleDispatcherChat runs one dispatcher turn: append the user message,
// loop LLM ↔ tools until the model stops, stream progress as SSE events:
//
//	{"type":"text","text":...}        assistant text block
//	{"type":"tool","name":...,"arg":...,"result":...}
//	{"type":"error","error":...}
//	{"type":"done"}
//
// Loopback-only (the REPL and other in-host callers), like the AI gateway.
func handleDispatcherChat(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		httpErr(w, 403, "dispatcher_chat_loopback_only")
		return
	}
	if r.Method != http.MethodPost {
		httpErr(w, 405, "POST required")
		return
	}
	var req struct {
		AgentID string `json:"agent_id"`
		Text    string `json:"text"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "invalid body")
		return
	}
	shortID := shortPaneID(normPaneID(strings.TrimSpace(req.AgentID)))
	text := strings.TrimSpace(req.Text)
	if shortID == "" || text == "" {
		httpErr(w, 400, "agent_id and text required")
		return
	}
	if paneAgentType(shortID+":main.0") != "dispatcher" {
		httpErr(w, 400, "agent is not a dispatcher")
		return
	}
	workspace := paneWorkspace(shortID)
	if workspace == "" {
		httpErr(w, 404, "agent workspace not found")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	sse := &dispatcherSSE{w: w, flusher: flusher}

	session := getDispatcherSession(shortID, workspace)
	session.mu.Lock()
	defer session.mu.Unlock()

	session.messages = append(session.messages, M{"role": "user", "content": text})
	// Trim BEFORE building the request so the outgoing window is always within the
	// cap and never starts on an orphan tool_result (heals an over-cap window loaded
	// from disk; otherwise the first turn after load could 400 with "tool_result has
	// no corresponding tool_use" → no reply).
	session.messages = dispatcherTrimMessages(session.messages)
	model := dispatcherModel(shortID)
	cfg := resolveLiteConfig(shortID, workspace)

	for round := 0; round < dispatcherMaxToolRounds; round++ {
		payload := M{
			"model":      model,
			"max_tokens": 2048,
			// Cache-first: system as a block with an explicit breakpoint, plus
			// one on the last tool def and one on the last history message. The
			// prefix is byte-stable across turns (append-only history, fixed
			// system+tools), so Anthropic-protocol providers hit explicit
			// caching and DeepSeek hits its implicit prefix cache; the gateway's
			// DeepSeek adapter flattens/drops cache_control harmlessly.
			"system": []M{{
				"type": "text", "text": cfg.systemPrompt,
				"cache_control": M{"type": "ephemeral"},
			}},
			"messages": dispatcherRequestMessages(session.messages),
		}
		// Pure-chat roles (assistant/support/sales) enable no tools — omit the
		// field entirely (an empty tools array is rejected by some upstreams).
		if tools := dispatcherCachedToolDefs(cfg.enabledTools); len(tools) > 0 {
			payload["tools"] = tools
		}
		resp, streamed, err := dispatcherCallGateway(shortID, payload, sse.emit)
		if err != nil {
			sse.emit(M{"type": "error", "error": err.Error()})
			// Drop the failed exchange tail so history stays consistent.
			session.persistLocked(workspace)
			sse.emit(M{"type": "done"})
			return
		}

		blocks, _ := resp["content"].([]interface{})
		stopReason, _ := resp["stop_reason"].(string)

		// Record the assistant turn verbatim.
		session.messages = append(session.messages, M{"role": "assistant", "content": blocks})

		var toolResults []M
		for _, b := range blocks {
			bm, ok := b.(map[string]interface{})
			if !ok {
				continue
			}
			switch bm["type"] {
			case "text":
				// Already delivered as text_delta events when streaming; only
				// emit the assembled block on the non-stream fallback path.
				if t, _ := bm["text"].(string); !streamed && strings.TrimSpace(t) != "" {
					sse.emit(M{"type": "text", "text": t})
				}
			case "tool_use":
				name, _ := bm["name"].(string)
				toolID, _ := bm["id"].(string)
				input, _ := bm["input"].(map[string]interface{})
				result := dispatcherRunTool(shortID, name, input, cfg.enabledTools)
				argJSON, _ := json.Marshal(input)
				sse.emit(M{"type": "tool", "name": name, "arg": string(argJSON), "result": truncateForLog(result, 600)})
				toolResults = append(toolResults, M{
					"type":        "tool_result",
					"tool_use_id": toolID,
					"content":     result,
				})
			}
		}

		if len(toolResults) == 0 || stopReason != "tool_use" {
			session.persistLocked(workspace)
			sse.emit(M{"type": "done"})
			return
		}
		session.messages = append(session.messages, M{"role": "user", "content": toolResults})
	}

	sse.emit(M{"type": "error", "error": fmt.Sprintf("tool loop exceeded %d rounds, stopping", dispatcherMaxToolRounds)})
	session.persistLocked(workspace)
	sse.emit(M{"type": "done"})
}
