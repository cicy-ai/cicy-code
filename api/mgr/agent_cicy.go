package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cliCommandForAgentType maps an agent_type to the shell command that must be on
// PATH for it to run. cicy lite agents run in-process (no CLI) → "". Used by the
// roster (agent_list) to report whether an agent still needs its runtime installed
// — HR reads this to ask 运维 (ops) to install claude/codex/opencode on demand.
func cliCommandForAgentType(agentType string) string {
	switch normalizeAgentType(agentType) {
	case "claude", "cicy-claude":
		return "claude"
	case "codex":
		return "codex"
	case "opencode":
		return "opencode"
	case "cursor":
		return "cursor-agent"
	case "kiro-cli":
		return "kiro-cli"
	case "copilot":
		return "copilot"
	case "hermes":
		return "hermes"
	default:
		return "" // cicy / dispatcher / unknown: no external CLI to install
	}
}

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

const cicyGatewayBase = "http://127.0.0.1:8008"

// defaultCicyCharter seeds the dispatcher's AGENTS.md at agent
// creation. The full PM working protocol lives in cicySystemPromptBase
// (compiled); this file is the per-instance customization hook — anything the
// user writes here is appended to the system prompt and takes effect on the
// next turn, no restart. Placeholders are substituted by
// writeAgentGuidanceFile.
const defaultCicyCharter = `# {{AGENT_ID}} · 产品项目经理

<!-- 这里写对这位 PM 的个性化要求(团队约定、汇报口径、偏好等),改完下一轮对话即生效。 -->
`

// cicySystemPromptBase is the fixed, non-editable part of the system
// prompt: tool semantics and dispatch policy. The charter (AGENTS.md) is
// appended after it.
const cicySystemPromptBase = `你是这个多 agent 工作区的产品项目经理(PM),用户的唯一接口人。

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

type cicySession struct {
	mu       sync.Mutex
	messages []M // anthropic-format messages, persisted to disk

	// convID is the conversation identity the audit layer keys snapshots off
	// (.cicy/history/chat/<convID>/). A random UUID — NOT the legacy fixed
	// "dispatcher-<id>" — persisted to .cicy/cicy/conversation_id so it survives
	// restarts. /clear rotates it (a clear starts a NEW conversation); /compact
	// keeps it (same conversation). Guarded by mu.
	convID string

	// Input queue: while a reply is in flight (busy), additional inputs are
	// appended to pending instead of running their own turn; the in-flight
	// handler drains pending on completion and merges them into ONE follow-up
	// turn streamed on the same connection. Guarded by qmu (separate from mu,
	// which is held for the whole duration of a turn).
	qmu     sync.Mutex
	busy    bool
	pending []string

	// 取消:turn 运行期间存一个 context cancel。用户按 Esc / 点停止 → cancelInFlight()
	// 取消它,正在跑的网关请求(走 ReverseProxy)被掐断、上游 LLM 一并中止;同时清空
	// pending,排队的输入不再续跑。guarded by cancelMu(独立于 mu/qmu,取消随时可调)。
	cancelMu sync.Mutex
	cancelFn context.CancelFunc
}

// setCancel 记下当前 turn 的 cancel(turn 开始时调)。
func (s *cicySession) setCancel(fn context.CancelFunc) {
	s.cancelMu.Lock()
	s.cancelFn = fn
	s.cancelMu.Unlock()
}

// cancelInFlight 取消正在跑的 turn(若有)并丢弃排队输入。返回是否确实有 turn 在跑。
func (s *cicySession) cancelInFlight() bool {
	s.cancelMu.Lock()
	fn := s.cancelFn
	s.cancelMu.Unlock()
	s.qmu.Lock()
	s.pending = nil // 取消即清空排队,别让后续 drain 又续上一轮
	s.qmu.Unlock()
	if fn == nil {
		return false
	}
	fn()
	return true
}

var (
	cicySessionsMu sync.Mutex
	cicySessions   = map[string]*cicySession{}
)

// cicyMaxHistoryMessages is the LAST-RESORT front-trim ceiling. With compaction
// (below) as the primary bound, this only fires if the summarizer is repeatedly
// unavailable — so it sits well above cicyCompactThreshold to give compaction
// room to act first. (Was 60 with per-turn front-trim, which busted the prompt
// cache every turn at the cap; see cicyCompactMessages.)
const cicyMaxHistoryMessages = 160
const cicyMaxToolRounds = 8

// History compaction (compact) — see cicyCompactMessages. Mirrors Claude Code's
// auto-compact: summarize the older half into one stable message, keep the recent
// tail verbatim. Strictly better than front-trim for both context preservation
// and prompt-cache stability (the summary+tail prefix stays byte-stable between
// the infrequent compactions, so the cache builds up normally in between).
const (
	cicyCompactThreshold  = 80 // compact once the window exceeds this many messages
	cicyCompactKeepRecent = 40 // messages kept verbatim after the summary
)

// cicyTrimMessages keeps history within the cap, trimming from the front,
// but NEVER lets the window start on a tool_result turn — Anthropic/DeepSeek
// reject a tool_result whose matching tool_use was trimmed away ("each
// tool_result must have a corresponding tool_use in the previous message").
// Must be applied to the OUTGOING window every turn (before building the
// request), not just on persist — otherwise a freshly-loaded over-cap window can
// be sent with an orphan tool_result at the front → gateway 400 / no reply.
func cicyTrimMessages(msgs []M) []M {
	if len(msgs) <= cicyMaxHistoryMessages {
		return msgs
	}
	start := len(msgs) - cicyMaxHistoryMessages
	for start < len(msgs) && cicyMessageHasToolResult(msgs[start]) {
		start++
	}
	return append([]M{}, msgs[start:]...)
}

// cicyCompactSummarize is the summarizer compaction uses; a package var so tests
// stub it without a live provider. The default routes through the SAME local
// gateway the main turn uses (proven creds/model routing per agent), but with a
// "compact-<id>" session id so it never pollutes the agent's own conversation.
var cicyCompactSummarize = cicySummarizeViaGateway

// cicySummarizeViaGateway sends the transcript to the agent's model through the
// local gateway and returns the assembled text. A no-op emit (we only want the
// final text, not streamed deltas); a separate session id keeps it out of the
// agent's chat audit.
func cicySummarizeViaGateway(ctx context.Context, shortID, model, transcript string) (string, error) {
	payload := M{
		"model":      model,
		"max_tokens": 1024,
		"system":     []M{{"type": "text", "text": cicyCompactSystemPrompt}},
		"messages":   []M{{"role": "user", "content": transcript}},
	}
	resp, _, err := cicyCallGateway(ctx, shortID, "compact-"+shortID, payload, func(M) {})
	if err != nil {
		return "", err
	}
	return cicyResponseText(resp), nil
}

// cicyResponseText concatenates the text blocks of an assembled gateway response.
func cicyResponseText(resp map[string]interface{}) string {
	blocks, _ := resp["content"].([]interface{})
	var b strings.Builder
	for _, bl := range blocks {
		if bm, ok := bl.(map[string]interface{}); ok {
			if t, _ := bm["type"].(string); t == "text" {
				if tx, _ := bm["text"].(string); tx != "" {
					b.WriteString(tx)
				}
			}
		}
	}
	return b.String()
}

const cicyCompactSystemPrompt = `你是一个对话历史压缩器。下面给你的是一个多 agent 项目经理(PM)与用户的对话、以及它调用工具的记录。请压缩成一段结构化中文摘要,必须完整保留:
1) 用户的原始目标与核心诉求;
2) 已做出的关键决策与结论;
3) 已派出的任务及其当前状态(谁负责、做什么、done/test/进行中/阻塞);
4) 未决事项、待办;
5) 用户明确的约束、偏好与禁令。
提炼要点、不要逐字复述,但任何任务的状态都不能遗漏。只输出摘要正文,不要前言或客套。`

const cicyCompactSummaryPrefix = "[以下是更早对话的压缩摘要,用于保持上下文连续;最近的原始对话紧随其后。]\n\n"

// cicyOutcomePrefix marks a synthetic assistant message that records a turn which
// produced no normal reply — the user cancelled it or the gateway failed after
// exhausting retries. It's a real assistant text block (keeps user/assistant
// alternation valid; a bare trailing user message would otherwise stack into a
// consecutive-user window).
//
// ⚠️ This text goes on the WIRE and into current.json (web reads the SAME snapshot —
// wire == display, they can't differ). So it MUST be a short, clean, human sentence:
// the model sees it as harmless context and COMPACTION summarizes it cleanly. The
// earlier "⟦cicy-turn-outcome⟧error\x1f{gateway 401 json…}" form leaked weird symbols
// + raw error JSON into the wire, and when dozens piled up during an outage the
// compaction baked them into a garbage summary. No machine detail / no JSON here —
// the failure reason lives in usage-log, not in the conversation. The serving layer
// (cicyTagOutcome) detects this prefix to style it + offer 重试.
const cicyOutcomePrefix = "（本轮未生成回复"

// cicyOutcomeLegacyMark is the pre-2026-06-09 marker; still detected by the display
// relabel + compaction filter so any lingering old records clean up instead of
// showing raw symbols.
const cicyOutcomeLegacyMark = "⟦cicy-turn-outcome⟧"

// cicyOutcomeMarkerText renders the clean wire/display text for a turn outcome.
func cicyOutcomeMarkerText(kind string) string {
	if kind == "cancelled" {
		return cicyOutcomePrefix + "·已停止）"
	}
	return cicyOutcomePrefix + "·生成失败）"
}

// cicyOutcomeMessage builds the synthetic assistant record for a cancelled/failed
// turn. detail (e.g. "gateway 401: …") is NOT persisted into the conversation — it
// only rides the emit/usage-log; the wire text stays clean.
func cicyOutcomeMessage(kind, detail string) M {
	return M{"role": "assistant", "content": []M{{"type": "text", "text": cicyOutcomeMarkerText(kind)}}}
}

// cicyAttachOutcomeToSnapshot appends the outcome marker to the web's committed
// snapshot (current.json) so a cancelled/failed turn shows up IMMEDIATELY, not
// only after the next successful turn re-snapshots history. The web reads
// current.json (the last wire-request body); our marker is appended post-request,
// so without this patch it would be invisible until the next request carries it.
// Idempotent: skips if the snapshot already ends with a marker. The marker is
// given an explicit id = maxID+1 so the body stays "already numbered" — that keeps
// aiGatewayWriteCurrentSnapshot's annotator from RENUMBERING the whole window (an
// id-less message forces a re-annotate → every id shifts → the web can't reconcile
// incrementally and re-pages the entire history = a jarring full-reload flash on
// every send). With a stable +1 id it reads as one normal new turn.
func cicyAttachOutcomeToSnapshot(shortID, kind, detail string) {
	current := agentInspectorLoadCurrent(shortID)
	body := aiGatewayMap(current.Body)
	if len(body) == 0 {
		return // no wire snapshot yet to attach to
	}
	msgs := aiGatewaySlice(body["messages"])
	if n := len(msgs); n > 0 {
		if cicyMessageOutcomeKind(aiGatewayMap(msgs[n-1])) != "" {
			return
		}
	}
	msgs = append(msgs, map[string]interface{}{
		"id":      aiGatewayCurrentBodyMaxHistoryID(current.Body) + 1,
		"role":    "assistant",
		"content": []interface{}{map[string]interface{}{"type": "text", "text": cicyOutcomeMarkerText(kind)}},
	})
	body["messages"] = msgs
	current.Body = body
	_ = aiGatewayWriteCurrentSnapshot(shortID, current)
}

// cicyOutcomeKindFromText returns "cancelled"/"error" if s is an outcome marker
// (new clean form OR the legacy "⟦cicy-turn-outcome⟧…" form), else "".
func cicyOutcomeKindFromText(s string) string {
	isNew := strings.HasPrefix(s, cicyOutcomePrefix)
	isOld := strings.HasPrefix(s, cicyOutcomeLegacyMark)
	if !isNew && !isOld {
		return ""
	}
	if strings.Contains(s, "已停止") || strings.Contains(s, "cancelled") {
		return "cancelled"
	}
	return "error"
}

// cicyMessageOutcomeKind returns the outcome kind ("cancelled"/"error") if msg is
// a synthetic outcome marker, else "". Handles both content shapes ([]M in-memory,
// []interface{} after a disk reload).
func cicyMessageOutcomeKind(msg M) string {
	if r, _ := msg["role"].(string); r != "assistant" {
		return ""
	}
	found := ""
	cicyForEachBlock(msg, func(bm map[string]interface{}, typ string) {
		if found != "" || typ != "text" {
			return
		}
		if s, _ := bm["text"].(string); s != "" {
			found = cicyOutcomeKindFromText(s)
		}
	})
	return found
}

// cicyCompactSplitPoint returns the index where the kept verbatim tail begins
// (everything before it is summarized), or -1 when the history should not be
// compacted yet. The boundary is advanced forward so the tail never STARTS on a
// tool_result whose matching tool_use would fall into the summarized half (which
// would orphan it → provider 400) — the same invariant cicyTrimMessages enforces.
func cicyCompactSplitPoint(msgs []M) int {
	if len(msgs) <= cicyCompactThreshold {
		return -1
	}
	keepFrom := len(msgs) - cicyCompactKeepRecent
	if keepFrom < 1 {
		return -1
	}
	for keepFrom < len(msgs) && cicyMessageHasToolResult(msgs[keepFrom]) {
		keepFrom++
	}
	if keepFrom < 1 || keepFrom >= len(msgs) {
		return -1 // nothing meaningful left to summarize, or no tail left
	}
	return keepFrom
}

// cicyToolResultText renders a tool_result's content (string or block array) to
// plain text for the compaction transcript.
func cicyToolResultText(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var parts []string
		for _, b := range c {
			if bm, ok := b.(map[string]interface{}); ok {
				if tx, _ := bm["text"].(string); tx != "" {
					parts = append(parts, tx)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		raw, _ := json.Marshal(content)
		return string(raw)
	}
}

// cicyRenderHistoryForCompaction flattens messages (text / tool calls / tool
// results) into a plain-text transcript for the summarizer — no tool blocks, so
// the summarization call itself can't trip tool-pairing constraints.
func cicyRenderHistoryForCompaction(msgs []M) string {
	var b strings.Builder
	for _, m := range msgs {
		// 失败/取消记录是 UI 噪声,绝不进压缩摘要(否则 outage 期攒的几十条会被总结成垃圾)。
		if cicyMessageOutcomeKind(m) != "" {
			continue
		}
		role, _ := m["role"].(string)
		if s, ok := m["content"].(string); ok {
			if strings.TrimSpace(s) != "" {
				b.WriteString(role + ": " + s + "\n")
			}
			continue
		}
		cicyForEachBlock(m, func(bm map[string]interface{}, t string) {
			switch t {
			case "text":
				if tx, _ := bm["text"].(string); strings.TrimSpace(tx) != "" {
					b.WriteString(role + ": " + tx + "\n")
				}
			case "tool_use":
				name, _ := bm["name"].(string)
				arg, _ := json.Marshal(bm["input"])
				b.WriteString(role + " [调用 " + name + " " + truncateForLog(string(arg), 300) + "]\n")
			case "tool_result":
				b.WriteString("工具结果: " + truncateForLog(cicyToolResultText(bm["content"]), 400) + "\n")
			}
		})
	}
	return b.String()
}

// cicyCompactMessages summarizes the older half of an over-long history into one
// stable user message, keeping the recent tail verbatim. Returns (compacted,
// true) on success; (msgs, false) when compaction isn't needed or the summary
// call fails — the caller then falls back to front-trimming so the turn always
// proceeds. Unlike front-trim this preserves intent/task-state, and it keeps the
// prompt-cache prefix stable between compactions (only the compaction turn itself
// misses cache).
func cicyCompactMessages(ctx context.Context, shortID string, msgs []M, model string) ([]M, bool) {
	keepFrom := cicyCompactSplitPoint(msgs)
	if keepFrom < 0 {
		return msgs, false
	}
	summary, err := cicyCompactSummarize(ctx, shortID, model, cicyRenderHistoryForCompaction(msgs[:keepFrom]))
	if err != nil || strings.TrimSpace(summary) == "" {
		return msgs, false
	}
	out := make([]M, 0, len(msgs)-keepFrom+1)
	out = append(out, M{"role": "user", "content": cicyCompactSummaryPrefix + strings.TrimSpace(summary)})
	out = append(out, msgs[keepFrom:]...)
	return out, true
}

func cicyConvDir(workspace string) string {
	return filepath.Join(workspace, ".cicy", "cicy")
}

// cicyLegacyConvDir is the pre-rename location (.cicy/dispatcher). Used once by
// migrateCicyStateDir to move existing conversation history to the new path.
func cicyLegacyConvDir(workspace string) string {
	return filepath.Join(workspace, ".cicy", "dispatcher")
}

func cicyHistoryPath(workspace string) string {
	return filepath.Join(cicyConvDir(workspace), "conversation.json")
}

// cicyConvIDPath holds the persisted conversation id (sibling of conversation.json).
func cicyConvIDPath(workspace string) string {
	return filepath.Join(cicyConvDir(workspace), "conversation_id")
}

// cicyNewConversationID returns a random UUIDv4-shaped conversation id, matching
// the format claude-type agents' session ids use so all conversation dirs under
// .cicy/history/chat/ look alike.
func cicyNewConversationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("conv-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// cicyLoadOrCreateConvID reads the persisted conversation id, minting and
// persisting a fresh random one when absent (first run, or pre-upgrade agents
// that used the fixed "dispatcher-<id>").
func cicyLoadOrCreateConvID(workspace string) string {
	if raw, err := os.ReadFile(cicyConvIDPath(workspace)); err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			return id
		}
	}
	id := cicyNewConversationID()
	_ = os.MkdirAll(cicyConvDir(workspace), 0755)
	_ = os.WriteFile(cicyConvIDPath(workspace), []byte(id+"\n"), 0644)
	return id
}

// migrateCicyStateDir moves a pre-rename .cicy/dispatcher dir to .cicy/cicy when
// the new one doesn't exist yet, so an agent's conversation survives the rename.
// Idempotent and best-effort: any failure leaves the legacy dir in place and the
// caller falls back to reading it.
func migrateCicyStateDir(workspace string) {
	newDir := cicyConvDir(workspace)
	oldDir := cicyLegacyConvDir(workspace)
	if _, err := os.Stat(newDir); err == nil {
		return // already migrated
	}
	if _, err := os.Stat(oldDir); err != nil {
		return // nothing to migrate
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		log.Printf("[cicy-migrate] %s → %s failed: %v", oldDir, newDir, err)
	}
}

func getCicySession(shortID, workspace string) *cicySession {
	cicySessionsMu.Lock()
	defer cicySessionsMu.Unlock()
	if s, ok := cicySessions[shortID]; ok {
		return s
	}
	migrateCicyStateDir(workspace)
	s := &cicySession{}
	// Fall back to the legacy path if migration couldn't move it (e.g. perms).
	histPath := cicyHistoryPath(workspace)
	if _, err := os.Stat(histPath); err != nil {
		if legacy := filepath.Join(cicyLegacyConvDir(workspace), "conversation.json"); legacy != histPath {
			if _, e2 := os.Stat(legacy); e2 == nil {
				histPath = legacy
			}
		}
	}
	if raw, err := os.ReadFile(histPath); err == nil {
		var msgs []M
		if json.Unmarshal(raw, &msgs) == nil {
			s.messages = msgs
		}
	}
	s.convID = cicyLoadOrCreateConvID(workspace)
	cicySessions[shortID] = s
	return s
}

// warmCicySessions pre-registers every local cicy agent's session at boot so a
// headless cicy is alive the instant the server is up — no tmux pane required to
// bring it online. For cicy, liveness == registry membership (see
// listAgentsByPane → cicySessionRegistered), so warming here is exactly what makes
// freshly-started cicy agents show online and ready to take messages in-process.
// Best-effort: a missing workspace just skips that agent.
func warmCicySessions() {
	rows, err := store.Query(
		"SELECT pane_id FROM agent_config WHERE agent_type IN ('cicy','dispatcher','secretary') AND COALESCE(machine_id,0)=0",
	)
	if err != nil {
		log.Printf("[cicy-warm] query failed: %v", err)
		return
	}
	var paneIDs []string
	for rows.Next() {
		var pid string
		if rows.Scan(&pid) == nil && strings.TrimSpace(pid) != "" {
			paneIDs = append(paneIDs, pid)
		}
	}
	rows.Close()

	n := 0
	for _, pid := range paneIDs {
		shortID := shortPaneID(normPaneID(pid))
		workspace := paneWorkspace(shortID)
		if workspace == "" {
			continue
		}
		getCicySession(shortID, workspace) // registers in cicySessions + loads history
		n++
	}
	if n > 0 {
		log.Printf("[cicy-warm] registered %d headless cicy session(s)", n)
	}
}

// cicySessionRegistered reports whether a cicy agent currently has a warmed
// server-side session — the headless liveness signal that replaces tmux session
// presence for the "online" column.
func cicySessionRegistered(shortID string) bool {
	cicySessionsMu.Lock()
	defer cicySessionsMu.Unlock()
	_, ok := cicySessions[shortID]
	return ok
}

// persistLocked writes the FULL history to disk (no auto-truncation; only
// explicit compact/clear shrink it). Caller holds s.mu.
func (s *cicySession) persistLocked(workspace string) {
	s.messages = cicyBalanceToolCalls(s.messages)
	if err := os.MkdirAll(cicyConvDir(workspace), 0755); err != nil {
		return
	}
	raw, err := json.Marshal(s.messages)
	if err != nil {
		return
	}
	_ = os.WriteFile(cicyHistoryPath(workspace), raw, 0644)
}

func cicyMessageHasToolResult(msg M) bool {
	// Two content shapes exist: []interface{} (JSON-loaded from disk) and []M
	// (appended in-memory by the tool loop). Matching only the former let the
	// trim guard land the window on an in-memory tool_result message → orphan
	// tool_result at messages[0] → gateway 400 ("must have a corresponding
	// tool_use"). Handle both.
	switch blocks := msg["content"].(type) {
	case []interface{}:
		for _, b := range blocks {
			if bm, ok := b.(map[string]interface{}); ok {
				if t, _ := bm["type"].(string); t == "tool_result" {
					return true
				}
			}
		}
	case []M:
		for _, bm := range blocks {
			if t, _ := bm["type"].(string); t == "tool_result" {
				return true
			}
		}
	}
	return false
}

// cicySyntheticToolResult is injected to pair an orphan tool_use whose
// matching tool_result never arrived (a turn interrupted by a new user message
// before the tool resolved). Without it the provider rejects the whole window
// (Anthropic/DeepSeek: "tool_use ids were found without tool_result blocks").
const cicySyntheticToolResult = "(工具结果不可用:上一回合在收到结果前被打断,系统已自动补平以保持 tool_use/tool_result 配对。)"

func cicyBlockType(b interface{}) (map[string]interface{}, string) {
	// M is an alias for map[string]interface{}, so one case covers both.
	if bm, ok := b.(map[string]interface{}); ok {
		t, _ := bm["type"].(string)
		return bm, t
	}
	return nil, ""
}

// cicyForEachBlock iterates a message's content blocks regardless of the
// two shapes that coexist: []interface{} (JSON-loaded) and []M (in-memory).
func cicyForEachBlock(msg M, fn func(bm map[string]interface{}, typ string)) {
	switch blocks := msg["content"].(type) {
	case []interface{}:
		for _, b := range blocks {
			if bm, t := cicyBlockType(b); bm != nil {
				fn(bm, t)
			}
		}
	case []M:
		for _, b := range blocks {
			if bm, t := cicyBlockType(b); bm != nil {
				fn(bm, t)
			}
		}
	}
}

func cicyToolUseIDs(msg M) []string {
	var ids []string
	cicyForEachBlock(msg, func(bm map[string]interface{}, t string) {
		if t == "tool_use" {
			if id, _ := bm["id"].(string); id != "" {
				ids = append(ids, id)
			}
		}
	})
	return ids
}

func cicyToolResultIDs(msg M) map[string]bool {
	out := map[string]bool{}
	cicyForEachBlock(msg, func(bm map[string]interface{}, t string) {
		if t == "tool_result" {
			if id, _ := bm["tool_use_id"].(string); id != "" {
				out[id] = true
			}
		}
	})
	return out
}

// cicyBalanceToolCalls heals MID-history orphan tool_use blocks: any
// tool_use whose matching tool_result is absent from the immediately-following
// message gets a synthetic tool_result injected right after it (merged into the
// next user message, or as a fresh user message when none follows). This is the
// symmetric complement to cicyTrimMessages, which only guards LEADING
// orphan tool_result. Together they guarantee a provider-valid window every
// turn, so an interrupted-turn corruption self-heals instead of bricking the
// agent. Idempotent; leaves already-balanced history untouched.
func cicyBalanceToolCalls(msgs []M) []M {
	out := make([]M, 0, len(msgs)+2)
	for i := 0; i < len(msgs); i++ {
		out = append(out, msgs[i])
		ids := cicyToolUseIDs(msgs[i])
		if len(ids) == 0 {
			continue
		}
		have := map[string]bool{}
		nextIsUser := false
		if i+1 < len(msgs) {
			have = cicyToolResultIDs(msgs[i+1])
			if r, _ := msgs[i+1]["role"].(string); r == "user" {
				nextIsUser = true
			}
		}
		var missing []string
		for _, id := range ids {
			if !have[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) == 0 {
			continue
		}
		results := make([]interface{}, 0, len(missing))
		for _, id := range missing {
			results = append(results, M{"type": "tool_result", "tool_use_id": id, "content": cicySyntheticToolResult})
		}
		if nextIsUser {
			// Merge the synthetic results into the front of the next user
			// message (preserving its original content), and skip re-appending it.
			merged := make([]interface{}, 0, len(results)+2)
			merged = append(merged, results...)
			switch c := msgs[i+1]["content"].(type) {
			case string:
				if strings.TrimSpace(c) != "" {
					merged = append(merged, M{"type": "text", "text": c})
				}
			case []interface{}:
				merged = append(merged, c...)
			case []M:
				for _, b := range c {
					merged = append(merged, b)
				}
			}
			out = append(out, M{"role": "user", "content": merged})
			i++
		} else {
			// No following user message (orphan tool_use is last, or the next
			// turn is another assistant message) → insert a fresh user turn.
			out = append(out, M{"role": "user", "content": results})
		}
	}
	return out
}

// ── tools ───────────────────────────────────────────────────────────────────

// cicyToolDefs returns the tool defs the agent's profile enables: the
// built-ins whose names are in the effective set, plus any enabled custom tools
// (declared in lite-config.json). An empty effective set yields no tools.
func cicyToolDefs(cfg liteConfig) []M {
	if len(cfg.enabledTools) == 0 {
		return nil
	}
	all := cicyAllToolDefs()
	out := make([]M, 0, len(all))
	for _, t := range all {
		if name, _ := t["name"].(string); cfg.enabledTools[name] {
			out = append(out, t)
		}
	}
	return append(out, liteCustomToolDefs(cfg)...)
}

func cicyAllToolDefs() []M {
	defs := append([]M{}, a2aLiaisonToolDefs()...) // a2a_* (liaison profile)
	return append(defs, []M{
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
		{
			"name":        "agent_online",
			"description": "把一个离线/未上线的 agent 拉进团队并上线(组队官能力)。绑定到主控 w-1001;cicy 角色直接热身上线可对话,CLI 角色(claude/codex 等)需运行时已安装才能拉起终端——未安装会返回提示,让你先找运维安装。用 agent_list 看「就绪=否」的 agent 后用本工具拉上线。",
			"input_schema": M{
				"type": "object",
				"properties": M{
					"pane_id": M{"type": "string", "description": "要拉上线的 agent 短 id,如 w-992"},
				},
				"required": []string{"pane_id"},
			},
		},
		{
			"name":        "shell",
			"description": "在本机执行一条 shell 命令(Windows 走 PowerShell,macOS/Linux 走 bash),拿到 stdout/stderr 和退出码。用于真正动手:下载并安装 Docker Desktop、启动 Docker、安装并启动 cicy-code 等。每次只跑一条、跑完看结果再决定下一步;高破坏命令先跟用户确认。",
			"input_schema": M{
				"type": "object",
				"properties": M{
					"command": M{"type": "string", "description": "要执行的命令(Windows=PowerShell 语法,Unix=bash 语法)"},
					"cwd":     M{"type": "string", "description": "可选工作目录"},
					"timeout": M{"type": "integer", "description": "可选超时秒数(默认 120,最大 1800)"},
				},
				"required": []string{"command"},
			},
		},
	}...)
}

// platformShellArgv builds the argv to run a single command string through the
// host's native shell: PowerShell on Windows (cicy-code ships as a native exe
// there, PowerShell is always present), bash elsewhere.
func platformShellArgv(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", command}
	}
	return []string{"bash", "-lc", command}
}

func cicyRunTool(selfShortID, name string, input map[string]interface{}, cfg liteConfig) string {
	enabled := cfg.enabledTools
	if !enabled[name] {
		return "error: tool " + name + " is not enabled for this agent"
	}
	// Custom tools (declared in lite-config.json) route to the guarded executor.
	if _, isCustom := cfg.customTools[name]; isCustom {
		return runLiteCustomTool(cfg, selfShortID, name, input)
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
		// Roster for HR/PM scheduling. Each row carries the metadata HR needs to
		// decide who to bring on and what setup is required:
		//   id | 名称 | type | 类型 | 就绪 | 安装 | 网关 | 模型
		// - 类型: lite = cicy headless (in-process, no CLI); CLI = needs a runtime.
		// - 就绪: cicy → server-side session warmed; CLI → tmux pane alive.
		// - 安装: cicy → —; CLI → 已装 / 需装(HR asks 运维 to install 需装 ones).
		rows, err := store.Query("SELECT pane_id, title, agent_type, COALESCE(use_custom_gateway,0), COALESCE(default_model,'') FROM agent_config ORDER BY ttyd_port DESC, pane_id")
		if err != nil {
			return "error: " + err.Error()
		}
		defer rows.Close()
		live := liveSessionSet()
		var b strings.Builder
		b.WriteString("id | 名称 | type | 类型 | 就绪 | 安装 | 网关 | 模型\n")
		for rows.Next() {
			var paneID, title, agentType, model string
			var useGateway int
			if rows.Scan(&paneID, &title, &agentType, &useGateway, &model) != nil {
				continue
			}
			short := shortPaneID(paneID)
			kind, ready, install := "CLI", "否", "需装"
			if normalizeAgentType(agentType) == "cicy" {
				kind, install = "lite", "—"
				if cicySessionRegistered(short) {
					ready = "就绪"
				}
			} else {
				if cmd := cliCommandForAgentType(agentType); cmd == "" {
					install = "—"
				} else if _, err := exec.LookPath(cmd); err == nil {
					install = "已装"
				}
				if live[short] {
					ready = "就绪"
				}
			}
			gw := "否"
			if useGateway != 0 {
				gw = "是"
			}
			if strings.TrimSpace(model) == "" {
				model = "默认"
			}
			fmt.Fprintf(&b, "%s | %s | %s | %s | %s | %s | %s | %s\n", short, title, agentType, kind, ready, install, gw, model)
		}
		out := strings.TrimRight(b.String(), "\n")
		if out == "" || !strings.Contains(out, "\n") {
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
	case "agent_online":
		pane := shortPaneID(normPaneID(str("pane_id")))
		if pane == "" {
			return "error: pane_id required"
		}
		if pane == selfShortID {
			return "error: 这是我自己,无需拉"
		}
		var agentType, workspace string
		if err := store.QueryRow(
			"SELECT COALESCE(agent_type,''), COALESCE(workspace,'') FROM agent_config WHERE pane_id=?",
			pane+":main.0",
		).Scan(&agentType, &workspace); err != nil {
			return "error: 找不到 agent " + pane + "(先用 agent_list 确认 id)"
		}
		isCicy := normalizeAgentType(agentType) == "cicy"
		// CLI gate: the runtime must be installed before a pane can launch. If it
		// isn't, stop here and tell HR to have 运维 install it — don't half-bind.
		if !isCicy {
			if cmd := cliCommandForAgentType(agentType); cmd != "" {
				if _, err := exec.LookPath(cmd); err != nil {
					return fmt.Sprintf("%s 是 %s 型 CLI,运行时 %s 还没装,无法上线。先用 agent_msg 让运维安装 %s,装好我再拉。", pane, agentType, cmd, cmd)
				}
			}
		}
		// Pull onto the team: activate + bind under master (w-1001).
		store.Exec(fmt.Sprintf("UPDATE agent_config SET active=1, updated_at=%s WHERE pane_id=? AND COALESCE(active,0)=0", store.Now()), pane+":main.0")
		ensureWorkerBoundToPrimary(pane)
		if isCicy {
			if workspace == "" {
				workspace = paneWorkspace(pane)
			}
			if workspace == "" {
				return "error: " + pane + " 无 workspace,已绑定主控但无法热身,请联系运维排查"
			}
			getCicySession(pane, workspace) // warm → register → online
			if cicySessionRegistered(pane) {
				return pane + " 已拉进团队并上线(cicy 已热身,现在可直接对话)"
			}
			return pane + " 已绑定主控,但热身未注册,请稍后重试或联系运维"
		}
		if err := ensureAgentRunningByPaneID(pane + ":main.0"); err != nil {
			return "error: 已绑定主控,但拉起终端失败:" + err.Error()
		}
		return pane + " 已拉进团队并上线(已绑定主控 + 拉起终端,现在能打开了)"
	case "shell":
		command := str("command")
		if command == "" {
			return "error: command required"
		}
		timeout := 120 * time.Second
		if v, ok := input["timeout"].(float64); ok && v > 0 {
			timeout = time.Duration(v) * time.Second
			if timeout > 30*time.Minute {
				timeout = 30 * time.Minute
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		argv := platformShellArgv(command)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		if cwd := str("cwd"); cwd != "" {
			cmd.Dir = cwd
		}
		out, err := cmd.CombinedOutput()
		log.Printf("[cicy-shell] agent=%s bytes=%d err=%v cmd=%q", selfShortID, len(out), err, command)
		s := string(out)
		const maxOut = 12000
		if len(s) > maxOut {
			s = s[:maxOut] + "\n…(输出已截断)"
		}
		if ctx.Err() == context.DeadlineExceeded {
			return "error: 命令超时\n" + s
		}
		if err != nil {
			return fmt.Sprintf("exit error: %v\n%s", err, s)
		}
		if strings.TrimSpace(s) == "" {
			return "(执行成功,无输出)"
		}
		return s
	}
	if strings.HasPrefix(name, "a2a_") {
		return a2aLiaisonRunTool(selfShortID, name, input)
	}
	return "error: unknown tool " + name
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// cicyCachedToolDefs returns the tool defs with a cache breakpoint on
// the last one (caches the whole tools prefix on Anthropic-protocol
// providers; the DeepSeek adapter ignores the extra key).
func cicyCachedToolDefs(cfg liteConfig) []M {
	tools := cicyToolDefs(cfg)
	if len(tools) > 0 {
		tools[len(tools)-1]["cache_control"] = M{"type": "ephemeral"}
	}
	return tools
}

// cicyRequestMessages returns the history with a cache breakpoint
// attached to the final message — copy-on-write so the persisted history
// itself never carries cache_control (the breakpoint must move every turn).
func cicyRequestMessages(history []M) []M {
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

func cicyModel(shortID string) string {
	var defaultModel string
	store.QueryRow("SELECT COALESCE(default_model,'') FROM agent_config WHERE pane_id=?", shortID+":main.0").Scan(&defaultModel)
	// Team-Helper mode: the 团队助手's model is operator-configurable via
	// /api/settings/global (key helper_model). It applies when the agent has no
	// explicit default_model of its own, so a fresh helper install can be pointed
	// at a model without touching agent_config.
	if helperMode && defaultModel == "" {
		if hm := helperModelSetting(); hm != "" {
			defaultModel = hm
		}
	}
	return resolveClaudeStartupModel(defaultModel, loadRuntimeAIConfig(), shortID)
}

// ── gateway round trip ──────────────────────────────────────────────────────

// cicyCallGateway makes one STREAMING Anthropic Messages call through
// the unified local AI gateway and assembles the SSE stream back into a full
// response object. Streaming matters twice: the gateway audit layer parses
// the SSE as it flows and updates reply.json + broadcasts ai_chunk chat
// events live (the web UI's token-by-token rendering), and `emit` forwards
// text deltas to the REPL's SSE channel so the terminal streams too.
// The DeepSeek adapter wraps Chat-Completions SSE back into Anthropic
// Messages SSE, so the consumption side is one format regardless of provider.
// The second return reports whether deltas were emitted (the caller must then
// not re-emit the assembled text blocks).
func cicyCallGateway(ctx context.Context, shortID, sessionID string, payload M, emit func(M)) (map[string]interface{}, bool, error) {
	payload["stream"] = true
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	url := fmt.Sprintf("%s/api/ai-gateway/anthropic/%s/v1/messages", cicyGatewayBase, shortID)
	client := &http.Client{Timeout: 10 * time.Minute}

	// Claude Code-style auto-retry: transient failures (network drops, 408/409/429,
	// any 5xx incl. 529 overloaded) retry up to cicyMaxGatewayRetries times with
	// exponential backoff (0.5·2^n capped at 8s, jittered) honoring retry-after.
	// Client errors (400/401/403/404/422) are NOT retried — retrying a bad key or a
	// wrong endpoint never helps, so surface them immediately. Retry only happens
	// BEFORE any token reaches the user: once the stream starts emitting we commit
	// to that response and never re-run (would duplicate already-shown text).
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		// ctx 可取消:取消时这个请求中断,网关侧 ReverseProxy 随之掐断上游 LLM,
		// 流读取以 error 结束 → 上层 turn 收尾。
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, false, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", "cicy-local-gateway")
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Accept", "text/event-stream")
		// Conversation id the audit layer keys off. The main turn passes the
		// session's persisted random convID (rotated by /clear, kept by /compact);
		// compaction passes "compact-<id>" so its summarization round audits to a
		// SEPARATE bucket and never pollutes the agent's own chat history / UI
		// stream. Without it the audit layer falls back to a fresh random
		// conversation_id every turn, fragmenting history.
		req.Header.Set("X-Claude-Code-Session-Id", sessionID)

		resp, err := client.Do(req)
		if err != nil {
			// Connection-level failure: nothing was emitted, so a retry is safe.
			if ctx.Err() == nil && attempt < cicyMaxGatewayRetries {
				if !cicySleepBackoff(ctx, attempt, "") {
					return nil, false, ctx.Err()
				}
				log.Printf("[cicy-retry] agent=%s attempt=%d/%d network error: %v", shortID, attempt+1, cicyMaxGatewayRetries, err)
				continue
			}
			return nil, false, err
		}

		if resp.StatusCode >= 400 {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
			retryAfter := resp.Header.Get("retry-after")
			shouldRetryHeader := resp.Header.Get("x-should-retry")
			resp.Body.Close()
			gwErr := fmt.Errorf("gateway %d: %s", resp.StatusCode, truncateForLog(string(respBody), 400))
			if attempt < cicyMaxGatewayRetries && cicyStatusRetryable(resp.StatusCode, shouldRetryHeader) {
				if !cicySleepBackoff(ctx, attempt, retryAfter) {
					return nil, false, ctx.Err()
				}
				log.Printf("[cicy-retry] agent=%s attempt=%d/%d gateway %d, retrying", shortID, attempt+1, cicyMaxGatewayRetries, resp.StatusCode)
				continue
			}
			return nil, false, gwErr
		}

		// Good response — consume it. No retry past this point (tokens may stream to
		// the user); close the body explicitly since we're inside a loop (no defer).
		if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			// Upstream ignored stream:true — parse the plain JSON response.
			respBody, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr != nil {
				return nil, false, rerr
			}
			var parsed map[string]interface{}
			if err := json.Unmarshal(respBody, &parsed); err != nil {
				return nil, false, fmt.Errorf("gateway response parse failed: %v", err)
			}
			return parsed, false, nil
		}
		result, streamed, aerr := cicyAssembleSSE(resp.Body, emit)
		resp.Body.Close()
		return result, streamed, aerr
	}
}

// cicyMaxGatewayRetries mirrors Claude Code's DEFAULT_MAX_RETRIES (10): the cap on
// transient-failure retries for one gateway round trip.
const cicyMaxGatewayRetries = 10

// cicyStatusRetryable mirrors Claude Code's shouldRetry: an explicit
// x-should-retry header wins; otherwise retry 408/409/429 and any 5xx (incl. 529
// overloaded). Everything else (400/401/403/404/422 …) is a client error a retry
// can't fix.
func cicyStatusRetryable(status int, shouldRetryHeader string) bool {
	switch shouldRetryHeader {
	case "true":
		return true
	case "false":
		return false
	}
	switch {
	case status == 408, status == 409, status == 429:
		return true
	case status >= 500:
		return true
	}
	return false
}

// cicyRetryDelay computes the wait before the next gateway retry. A retry-after
// header (seconds, capped at 60s) wins; otherwise exponential backoff
// min(0.5·2^attempt, 8s) with 0.75–1.0 jitter — the same curve as Claude Code.
func cicyRetryDelay(attempt int, retryAfter string) time.Duration {
	if s := strings.TrimSpace(retryAfter); s != "" {
		if secs, err := strconv.ParseFloat(s, 64); err == nil && secs > 0 {
			if secs > 60 {
				secs = 60
			}
			return time.Duration(secs * float64(time.Second))
		}
	}
	base := 0.5 * math.Pow(2, float64(attempt))
	if base > 8 {
		base = 8
	}
	// Jitter 0.75–1.0 derived from the clock (no rand import); spreads retries so a
	// fleet of agents doesn't hammer a recovering upstream in lockstep.
	jitter := 1 - float64(time.Now().UnixNano()%250)/1000.0
	return time.Duration(base * jitter * float64(time.Second))
}

// cicySleepBackoff waits cicyRetryDelay, but returns false immediately if ctx is
// cancelled during the wait (user pressed Esc / stop) so we abandon the retry.
func cicySleepBackoff(ctx context.Context, attempt int, retryAfter string) bool {
	t := time.NewTimer(cicyRetryDelay(attempt, retryAfter))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// cicyAssembleSSE folds an Anthropic Messages SSE stream into the
// equivalent non-stream response object ({content: blocks, stop_reason}),
// forwarding text deltas to `emit` as they arrive. The bool return reports
// whether any delta was emitted.
func cicyAssembleSSE(r io.Reader, emit func(M)) (map[string]interface{}, bool, error) {
	type blockBuf struct {
		typ        string
		id         string
		name       string
		text       strings.Builder
		inputJSON  strings.Builder
		suppressed bool // leaked DSML markup detected → stop forwarding deltas
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
						sentLen := b.text.Len()
						b.text.WriteString(t)
						if emit != nil && t != "" && !b.suppressed {
							// Leaked DSML tool-call markup is rescued post-stream
							// (cicyRescueDSML); don't let the raw markup
							// reach the consumer. Detect on the ACCUMULATED text
							// (the marker can split across deltas) and forward
							// only the prose before it.
							if mi := cicyDSMLMarkerIndex(b.text.String()); mi >= 0 {
								b.suppressed = true
								if mi > sentLen {
									emit(M{"type": "text_delta", "text": b.text.String()[sentLen:mi]})
									streamed = true
								}
							} else {
								emit(M{"type": "text_delta", "text": t})
								streamed = true
							}
						}
					}
				case "input_json_delta":
					if t, ok := d["partial_json"].(string); ok {
						b.inputJSON.WriteString(t)
					}
				case "thinking_delta":
					// 累积 thinking 正文。之前完全没处理 → 持久化进会话历史的 thinking 块是空壳,
					// 一提交 committed 就没了。留住正文,current.json → web 就能显示(折叠)。
					if t, ok := d["thinking"].(string); ok {
						b.text.WriteString(t)
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
		case "thinking":
			// thinking 块按流里的原始顺序(在 text/tool 之前)持久化,正文留住供 committed 显示。
			if b.text.Len() > 0 {
				blocks = append(blocks, map[string]interface{}{"type": "thinking", "thinking": b.text.String()})
			}
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

// ── DSML tool-call rescue ────────────────────────────────────────────────────
// DeepSeek occasionally leaks its internal DSML tool-call serialization as
// PLAIN TEXT instead of parsed tool_use blocks (provider-side parser miss):
//
//	嗨，让我看看有没有新动静。
//	<｜｜DSML｜｜tool_calls>
//	<｜｜DSML｜｜invoke name="a2a_status">
//	</｜｜DSML｜｜invoke>
//	<｜｜DSML｜｜invoke name="agent_capture">
//	<｜｜DSML｜｜parameter name="pane_id" string="true">w-1001</｜｜DSML｜｜parameter>
//	</｜｜DSML｜｜invoke>
//
// When that happens the tools never run and the raw markup reaches the user.
// Rescue: cut the markup out of the visible text and parse each invoke back
// into a real tool_use block so the normal tool loop executes it.

// cicyDSMLMarkerIndex returns the byte index of the earliest DSML
// marker in s (ASCII `<||DSML||` or fullwidth `<｜｜DSML｜｜` variant), or -1.
func cicyDSMLMarkerIndex(s string) int {
	idx := -1
	for _, marker := range []string{"<||DSML||", "<｜｜DSML｜｜"} {
		if i := strings.Index(s, marker); i >= 0 && (idx < 0 || i < idx) {
			idx = i
		}
	}
	return idx
}

var (
	dsmlInvokeRe = regexp.MustCompile(`(?s)<\|\|DSML\|\|invoke name="([^"]+)">(.*?)</\|\|DSML\|\|invoke>`)
	dsmlParamRe  = regexp.MustCompile(`(?s)<\|\|DSML\|\|parameter name="([^"]+)"(?:\s+string="(true|false)")?>(.*?)</\|\|DSML\|\|parameter>`)
)

// cicyRescueDSML scans assistant content blocks for leaked DSML markup.
// It returns the rewritten blocks (prose kept, markup stripped, invokes
// converted to tool_use) and whether anything was rescued.
func cicyRescueDSML(blocks []interface{}, round int) ([]interface{}, bool) {
	rescued := false
	out := make([]interface{}, 0, len(blocks))
	for bi, b := range blocks {
		bm, ok := b.(map[string]interface{})
		if !ok || bm["type"] != "text" {
			out = append(out, b)
			continue
		}
		text, _ := bm["text"].(string)
		idx := cicyDSMLMarkerIndex(text)
		if idx < 0 {
			out = append(out, b)
			continue
		}
		if prose := strings.TrimSpace(text[:idx]); prose != "" {
			out = append(out, map[string]interface{}{"type": "text", "text": prose})
		}
		// Normalize the fullwidth-pipe variant so one regex covers both.
		tail := strings.ReplaceAll(text[idx:], "｜", "|")
		for ii, m := range dsmlInvokeRe.FindAllStringSubmatch(tail, -1) {
			input := map[string]interface{}{}
			for _, pm := range dsmlParamRe.FindAllStringSubmatch(m[2], -1) {
				val := strings.TrimSpace(pm[3])
				if pm[2] == "false" {
					// Non-string parameter: number/bool/object encoded as JSON.
					var v interface{}
					if json.Unmarshal([]byte(val), &v) == nil {
						input[pm[1]] = v
						continue
					}
				}
				input[pm[1]] = val
			}
			out = append(out, map[string]interface{}{
				"type":  "tool_use",
				"id":    fmt.Sprintf("dsml_rescue_%d_%d_%d", round, bi, ii),
				"name":  m[1],
				"input": input,
			})
			rescued = true
		}
	}
	if !rescued {
		return blocks, false
	}
	return out, true
}

func truncateForLog(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ── chat endpoint (SSE) ─────────────────────────────────────────────────────

type cicySSE struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (s *cicySSE) emit(event M) {
	raw, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(s.w, "data: %s\n\n", raw)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// handleCicyChat runs one dispatcher turn: append the user message,
// loop LLM ↔ tools until the model stops, stream progress as SSE events:
//
//	{"type":"text","text":...}        assistant text block
//	{"type":"tool","name":...,"arg":...,"result":...}
//	{"type":"error","error":...}
//	{"type":"done"}
//
// Loopback-only (the REPL and other in-host callers), like the AI gateway.
// handleCicyCancel 打断某个 headless cicy agent 正在跑的 turn(浏览器按 Esc / 点停止
// 时调用)。body: {pane_id}。无在跑 turn 时返回 success:true, canceled:false(幂等)。
func handleCicyCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, 405, "POST required")
		return
	}
	var req M
	readBody(r, &req)
	paneID, _ := req["pane_id"].(string)
	if strings.TrimSpace(paneID) == "" {
		paneID, _ = req["win_id"].(string)
	}
	shortID := shortPaneID(normPaneID(strings.TrimSpace(paneID)))
	if shortID == "" {
		httpErr(w, 400, "pane_id required")
		return
	}
	canceled := cancelCicyPane(shortID)
	J(w, M{"success": true, "canceled": canceled, "pane_id": shortID})
}

// handleCicyRetry re-runs a cicy agent's latest cancelled/failed turn (web 点
// 「重试」时调用)。body: {pane_id}。Runs in the background; the web picks up the
// new reply through its normal live-tail polling.
func handleCicyRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, 405, "POST required")
		return
	}
	var req M
	readBody(r, &req)
	paneID, _ := req["pane_id"].(string)
	if strings.TrimSpace(paneID) == "" {
		paneID, _ = req["win_id"].(string)
	}
	shortID := shortPaneID(normPaneID(strings.TrimSpace(paneID)))
	if shortID == "" {
		httpErr(w, 400, "pane_id required")
		return
	}
	if normalizeAgentType(paneAgentType(shortID+":main.0")) != "cicy" {
		httpErr(w, 400, "agent is not a cicy lite agent")
		return
	}
	workspace := paneWorkspace(shortID)
	if workspace == "" {
		httpErr(w, 404, "agent workspace not found")
		return
	}
	started, reason := retryCicyPane(shortID, workspace)
	J(w, M{"success": started, "started": started, "reason": reason, "pane_id": shortID})
}

// clearCicyPane resets a cicy agent's conversation to empty: the LIVE in-memory
// session AND conversation.json AND the gateway snapshots (current/reply.json), all
// in one shot. Doing it through the live session avoids the race where editing
// conversation.json on disk gets clobbered by the running process persisting its
// in-memory history back.
func clearCicyPane(shortID, workspace string) {
	session := getCicySession(shortID, workspace)
	session.cancelInFlight() // stop any in-flight turn first
	session.mu.Lock()
	session.messages = nil
	// A clear starts a NEW conversation: rotate the random conversation id so the
	// next turn snapshots into a fresh chat/<convID>/ dir (the old one stays on
	// disk for scrollback).
	session.convID = cicyNewConversationID()
	_ = os.WriteFile(cicyConvIDPath(workspace), []byte(session.convID+"\n"), 0644)
	session.persistLocked(workspace) // conversation.json → empty
	session.mu.Unlock()
	session.forceRelease() // drop busy/queued so it's truly fresh
	// Empty the web's committed view too: remove the gateway snapshots (the audit
	// layer recreates them on the next turn).
	removeCicySnapshots(shortID)
}

// removeCicySnapshots drops the gateway live snapshots (current/reply.json and
// their symlink targets) so the web's committed view is cleared; the audit layer
// recreates them on the next turn.
func removeCicySnapshots(shortID string) {
	dir := aiGatewayHistoryDir(shortID)
	for _, name := range []string{"current.json", "reply.json"} {
		canonical := filepath.Join(dir, name)
		if target, err := os.Readlink(canonical); err == nil {
			_ = os.Remove(filepath.Join(dir, target))
		}
		_ = os.Remove(canonical)
	}
}

// archiveCicyCurrentSnapshot copies the live current.json to a timestamped
// sibling (current.<unix>.json) inside the conversation dir before /compact
// rewrites history — so the pre-compaction wire snapshot survives for scrollback
// / rollback. Best-effort: a missing snapshot is not an error.
func archiveCicyCurrentSnapshot(shortID string) {
	dir := aiGatewayHistoryDir(shortID)
	canonical := filepath.Join(dir, "current.json")
	data, err := os.ReadFile(canonical) // follows the symlink to the real file
	if err != nil || len(data) == 0 {
		return
	}
	target := canonical
	if t, e := os.Readlink(canonical); e == nil {
		target = filepath.Join(dir, t)
	}
	archive := strings.TrimSuffix(target, ".json") + fmt.Sprintf(".%d.json", time.Now().Unix())
	_ = os.WriteFile(archive, data, 0644)
}

// compactCicyPane is the manual /compact: it summarizes the WHOLE conversation
// into a single message and resets history to just that summary, keeping the same
// conversation (unlike /clear, which wipes). Unlike the auto-path cicyCompactMessages
// it is NOT gated on a length threshold and keeps NO verbatim tail — a full fold.
// The pre-compaction current.json is archived first so nothing is lost.
func compactCicyPane(ctx context.Context, session *cicySession, shortID, workspace string, sse *cicySSE) {
	// Never compact mid-turn — it would corrupt the in-flight history.
	if !session.tryOwnTurn() {
		sse.emit(M{"type": "system", "text": "正在回复中,稍后再 /compact。"})
		sse.emit(M{"type": "done"})
		return
	}
	defer session.forceRelease()

	session.mu.Lock()
	msgs := append([]M(nil), session.messages...)
	session.mu.Unlock()
	if len(msgs) == 0 {
		sse.emit(M{"type": "system", "text": "当前会话为空,无需压缩。"})
		sse.emit(M{"type": "done"})
		return
	}

	archiveCicyCurrentSnapshot(shortID) // best-effort rollback source

	sse.emit(M{"type": "system", "text": "正在压缩会话…"})
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	summary, err := cicyCompactSummarize(cctx, shortID, cicyModel(shortID), cicyRenderHistoryForCompaction(msgs))
	if err != nil || strings.TrimSpace(summary) == "" {
		sse.emit(M{"type": "system", "text": "压缩失败(摘要为空),会话未改动。"})
		sse.emit(M{"type": "done"})
		return
	}

	// Reset to just the summary — same conversation, summary becomes message #1.
	session.mu.Lock()
	session.messages = []M{{"role": "user", "content": cicyCompactSummaryPrefix + strings.TrimSpace(summary)}}
	session.persistLocked(workspace)
	session.mu.Unlock()
	removeCicySnapshots(shortID) // next turn rebuilds current.json from the summary

	sse.emit(M{"type": "system", "text": "✅ 已压缩。摘要:" + truncateForLog(strings.TrimSpace(summary), 200)})
	sse.emit(M{"type": "done"})
}

// handleCicyClear wipes a cicy agent's conversation (headless reset). body: {pane_id}.
func handleCicyClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, 405, "POST required")
		return
	}
	var req M
	readBody(r, &req)
	paneID, _ := req["pane_id"].(string)
	if strings.TrimSpace(paneID) == "" {
		paneID, _ = req["win_id"].(string)
	}
	shortID := shortPaneID(normPaneID(strings.TrimSpace(paneID)))
	if shortID == "" {
		httpErr(w, 400, "pane_id required")
		return
	}
	if normalizeAgentType(paneAgentType(shortID+":main.0")) != "cicy" {
		httpErr(w, 400, "agent is not a cicy lite agent")
		return
	}
	workspace := paneWorkspace(shortID)
	if workspace == "" {
		httpErr(w, 404, "agent workspace not found")
		return
	}
	clearCicyPane(shortID, workspace)
	J(w, M{"success": true, "pane_id": shortID})
}

func handleCicyChat(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		httpErr(w, 403, "cicy_chat_loopback_only")
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
	if normalizeAgentType(paneAgentType(shortID+":main.0")) != "cicy" {
		httpErr(w, 400, "agent is not a cicy lite agent")
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
	sse := &cicySSE{w: w, flusher: flusher}

	session := getCicySession(shortID, workspace)

	// Slash commands are intercepted here and NEVER sent to the LLM as a turn.
	//   /clear   → wipe the conversation (in-memory + conversation.json + snapshots)
	//   /compact → archive current.json, summarize the WHOLE history into one
	//              message, reset history to just that summary (same conversation)
	switch text {
	case "/clear":
		clearCicyPane(shortID, workspace)
		sse.emit(M{"type": "system", "text": "✅ 会话已清空。"})
		sse.emit(M{"type": "done"})
		return
	case "/compact":
		compactCicyPane(r.Context(), session, shortID, workspace, sse)
		return
	}

	// Input queueing: if a reply is already in flight for this session, queue
	// this input instead of running a second turn. The in-flight handler drains
	// the queue on completion and merges all queued inputs into ONE follow-up
	// turn (streamed on its own connection). This request returns immediately.
	if session.enqueueIfBusy(text) {
		sse.emit(M{"type": "queued", "text": "已加入队列,当前回复完成后一起处理。"})
		sse.emit(M{"type": "done"})
		return
	}
	// We own the turn — run it (plus any inputs queued mid-flight) and stream to
	// this connection. runCicyOwnedTurns owns the terminal "done" and busy-release.
	runCicyOwnedTurns(session, shortID, workspace, text, sse.emit)
}

// handleCicyHistory returns a cicy agent's conversation (conversation.json) as
// JSON — the read-back path for headless agents, in place of tmux capture (which
// a pane-less agent has none of). Backs `cicy-agent history`; the console chat
// view will read it too. GET /api/cicy/history?agent_id=<id>. Loopback-only, like
// the chat endpoint.
func handleCicyHistory(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		httpErr(w, 403, "cicy_history_loopback_only")
		return
	}
	shortID := shortPaneID(normPaneID(strings.TrimSpace(r.URL.Query().Get("agent_id"))))
	if shortID == "" {
		httpErr(w, 400, "agent_id required")
		return
	}
	if normalizeAgentType(paneAgentType(shortID+":main.0")) != "cicy" {
		httpErr(w, 400, "agent is not a cicy lite agent")
		return
	}
	workspace := paneWorkspace(shortID)
	if workspace == "" {
		httpErr(w, 404, "agent workspace not found")
		return
	}
	session := getCicySession(shortID, workspace)
	session.mu.Lock()
	msgs := append([]M{}, session.messages...)
	session.mu.Unlock()
	J(w, M{"agent_id": shortID, "messages": msgs})
}

// runCicyOwnedTurns runs the turn(s) the caller owns: the initial text, then any
// inputs that queued while it ran (drained + merged into ONE follow-up turn).
// Every stream event goes through emit; the terminal {"type":"done"} is emitted
// here. Releases the busy flag before returning (safety-net forceRelease covers an
// abnormal exit). Caller must already own the turn (enqueueIfBusy returned false)
// and must NOT hold session.mu.
//
// This is the single shared driver for BOTH transports: handleCicyChat (HTTP SSE)
// and deliverCicyMessage (headless, in-process). Decoupling it from the
// ResponseWriter is what lets a cicy agent run with no tmux pane at all.
func runCicyOwnedTurns(session *cicySession, shortID, workspace, text string, emit func(M)) {
	runCicyOwnedTurnsCore(session, shortID, workspace, text, false, emit)
}

// retryCicyOwnedTurns re-runs the latest failed/cancelled turn: it drops the
// trailing outcome marker and re-executes that user turn (no new user message
// appended). Caller must already own the turn (busy acquired).
func retryCicyOwnedTurns(session *cicySession, shortID, workspace string, emit func(M)) {
	runCicyOwnedTurnsCore(session, shortID, workspace, "", true, emit)
}

func runCicyOwnedTurnsCore(session *cicySession, shortID, workspace, text string, retry bool, emit func(M)) {
	released := false
	// 整段 owned-turns(含 drain 出来的后续轮)共用一个可取消 ctx:用户取消 → 当前网关
	// 请求被掐断、排队清空,这里收尾。
	ctx, cancel := context.WithCancel(context.Background())
	session.setCancel(cancel)
	defer func() {
		cancel()
		session.setCancel(nil)
		if !released {
			session.forceRelease()
		}
	}()

	cur := text
	first := true
	for {
		session.mu.Lock()
		var ok bool
		if first && retry {
			// Pop the failed turn's outcome marker and re-run that same user turn.
			if !session.dropTrailingOutcomeLocked() {
				session.mu.Unlock()
				emit(M{"type": "error", "error": "没有可重试的回合"})
				emit(M{"type": "done"})
				return // defer clears busy
			}
			ok = cicyRunWindowLocked(ctx, session, shortID, workspace, emit)
		} else {
			ok = runCicyTurnLocked(ctx, session, shortID, workspace, cur, emit)
		}
		first = false
		session.mu.Unlock()
		if !ok {
			emit(M{"type": "done"})
			return // defer clears busy
		}
		// Drain inputs queued while this turn ran; merge into one follow-up turn.
		merged, more := session.drainPending()
		if !more {
			released = true
			break
		}
		cur = merged
		emit(M{"type": "flush", "text": "处理排队的输入…"})
	}
	emit(M{"type": "done"})
}

// deliverCicyMessage runs a cicy turn IN-PROCESS — no HTTP round-trip, no tmux
// pane, no send-keys. The delivery layer (dispatchQueue) calls this for headless
// cicy agents: the message is fed straight to the server-side runtime, the reply
// is persisted to conversation.json by the turn itself, and a poll broadcast
// nudges any attached console to refresh. Returns false when the input was queued
// behind an in-flight reply (it will be merged into that reply's follow-up turn).
//
// Read-back of the reply is via the history endpoint / `cicy-agent history`, never
// tmux capture — a headless agent has no pane to capture.
func deliverCicyMessage(shortID, workspace, text string) bool {
	session := getCicySession(shortID, workspace)
	if session.enqueueIfBusy(text) {
		return false // merged into the in-flight owner's drain
	}
	// emit sink is a no-op: the turn persists to conversation.json (the source of
	// truth headless callers read back), so there's no stream to forward here.
	runCicyOwnedTurns(session, shortID, workspace, text, func(M) {})
	go broadcastPollData(shortID)
	return true
}

// cancelCicyPane 取消某个 cicy agent 正在跑的 turn(headless 取消入口)。只对已存在
// 的会话生效;没有会话或没有在跑的 turn 时返回 false。
func cancelCicyPane(shortID string) bool {
	cicySessionsMu.Lock()
	session := cicySessions[shortID]
	cicySessionsMu.Unlock()
	if session == nil {
		return false
	}
	return session.cancelInFlight()
}

// enqueueIfBusy returns true (and queues text) when a reply is already in flight
// for this session; otherwise it marks the session busy and returns false,
// meaning the caller now owns the turn(s). Concurrency-safe.
func (s *cicySession) enqueueIfBusy(text string) bool {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	if s.busy {
		s.pending = append(s.pending, text)
		return true
	}
	s.busy = true
	return false
}

// drainPending is called by the turn owner after a turn completes. If inputs
// queued during it, it returns them merged (newline-joined) into ONE follow-up
// turn with more=true. If nothing queued, it releases busy and returns
// more=false. Concurrency-safe.
func (s *cicySession) drainPending() (merged string, more bool) {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	if len(s.pending) == 0 {
		s.busy = false
		return "", false
	}
	merged = strings.Join(s.pending, "\n")
	s.pending = nil
	return merged, true
}

// forceRelease clears busy on an abnormal exit so the session never wedges.
func (s *cicySession) forceRelease() {
	s.qmu.Lock()
	s.busy = false
	s.qmu.Unlock()
}

// tryOwnTurn marks the session busy and returns true ONLY if it was idle — the
// caller then owns the turn(s). Unlike enqueueIfBusy it never queues; a retry
// while a reply is in flight is simply rejected. Concurrency-safe.
func (s *cicySession) tryOwnTurn() bool {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	if s.busy {
		return false
	}
	s.busy = true
	return true
}

// dropTrailingOutcomeLocked pops a trailing outcome marker (cancelled/failed turn
// record) so a retry re-runs the user turn it recorded. Returns true when the
// window then ends on a user message (i.e. there is a turn to retry). Caller holds
// session.mu.
func (s *cicySession) dropTrailingOutcomeLocked() bool {
	if len(s.messages) > 0 {
		if cicyMessageOutcomeKind(s.messages[len(s.messages)-1]) != "" {
			s.messages = s.messages[:len(s.messages)-1]
		}
	}
	if len(s.messages) == 0 {
		return false
	}
	r, _ := s.messages[len(s.messages)-1]["role"].(string)
	return r == "user"
}

// retryCicyPane re-runs the latest cancelled/failed turn for a cicy agent (web 点
// 「重试」入口)。It runs the turn in the background and returns immediately; the
// web surfaces the result through its normal reply.json live-tail polling. reason
// is non-empty only when started is false (nothing to retry / busy).
func retryCicyPane(shortID, workspace string) (started bool, reason string) {
	session := getCicySession(shortID, workspace)
	session.mu.Lock()
	hasOutcome := len(session.messages) > 0 &&
		cicyMessageOutcomeKind(session.messages[len(session.messages)-1]) != ""
	session.mu.Unlock()
	if !hasOutcome {
		return false, "没有可重试的回合"
	}
	if !session.tryOwnTurn() {
		return false, "正在生成中,请稍候"
	}
	go func() {
		retryCicyOwnedTurns(session, shortID, workspace, func(M) {})
		broadcastPollData(shortID)
	}()
	return true, ""
}

// runCicyTurnLocked runs ONE user turn (append text → tool loop → persist),
// streaming text/tool/error events through emit. It does NOT emit the terminal
// "done" — the caller owns that, so multiple drained turns can stream on one
// connection. Returns false on a gateway error or tool-loop overflow (caller
// stops draining). Caller must hold session.mu.
//
// emit is the only output sink: an HTTP caller passes cicySSE.emit (streams to a
// browser/REPL); a headless in-process caller (deliverCicyMessage) passes a sink
// that just persists/broadcasts. The runtime itself is transport-agnostic — no
// tmux, no ResponseWriter dependency.
func runCicyTurnLocked(ctx context.Context, session *cicySession, shortID, workspace, text string, emit func(M)) bool {
	session.messages = append(session.messages, M{"role": "user", "content": text})
	return cicyRunWindowLocked(ctx, session, shortID, workspace, emit)
}

// cicyRunWindowLocked bounds the window, then runs the tool loop on session.messages
// AS-IS (the trailing user message is assumed already present). runCicyTurnLocked
// reaches it after appending a fresh user turn; the retry path reaches it after
// dropping a failed turn's outcome marker — so a retry re-runs the same user turn
// without duplicating it. Caller must hold session.mu.
func cicyRunWindowLocked(ctx context.Context, session *cicySession, shortID, workspace string, emit func(M)) bool {
	model := cicyModel(shortID)
	// 绝不自动截断:完整历史每轮原样发出 → current.json = 完整的 q1 a1 q2 …,
	// 于是 history_id 就是数组顺序 1..N、reply = N+1。压缩只在显式 compact、
	// 清空只在显式 clear 时发生,这里既不自动 compact 也不自动 front-trim。
	// 仍修复历史中段的孤儿 tool_use(被打断的轮次),否则坏窗口会让每轮 provider 400。
	session.messages = cicyBalanceToolCalls(session.messages)
	cfg := resolveLiteConfig(shortID, workspace)

	for round := 0; round < cicyMaxToolRounds; round++ {
		// 用户已取消 → 立刻收尾:持久化已有内容,不再发下一轮网关请求。
		if ctx.Err() != nil {
			session.messages = append(session.messages, cicyOutcomeMessage("cancelled", "已取消"))
			emit(M{"type": "error", "error": "已取消"})
			session.persistLocked(workspace)
			cicyAttachOutcomeToSnapshot(shortID, "cancelled", "已取消")
			return false
		}
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
			"messages": cicyRequestMessages(session.messages),
		}
		// Pure-chat roles (assistant/support/sales) enable no tools — omit the
		// field entirely (an empty tools array is rejected by some upstreams).
		if tools := cicyCachedToolDefs(cfg); len(tools) > 0 {
			payload["tools"] = tools
		}
		resp, streamed, err := cicyCallGateway(ctx, shortID, session.convID, payload, emit)
		if err != nil {
			// A mid-flight cancel surfaces here as a ctx error — record it as a
			// cancellation, not a failure. Anything else is a genuine gateway error
			// that already survived auto-retry, so it's terminal for this turn.
			kind, detail := "error", err.Error()
			if ctx.Err() != nil {
				kind, detail = "cancelled", "已取消"
			}
			session.messages = append(session.messages, cicyOutcomeMessage(kind, detail))
			emit(M{"type": "error", "error": detail})
			session.persistLocked(workspace)
			cicyAttachOutcomeToSnapshot(shortID, kind, detail)
			return false
		}

		blocks, _ := resp["content"].([]interface{})
		stopReason, _ := resp["stop_reason"].(string)

		// Leaked DSML tool-call markup in the text? Parse it back into real
		// tool_use blocks (and strip it from the visible/persisted text) so the
		// tools actually run instead of the raw markup reaching the user.
		if rescuedBlocks, ok := cicyRescueDSML(blocks, round); ok {
			blocks = rescuedBlocks
			stopReason = "tool_use"
		}

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
					emit(M{"type": "text", "text": t})
				}
			case "tool_use":
				name, _ := bm["name"].(string)
				toolID, _ := bm["id"].(string)
				input, _ := bm["input"].(map[string]interface{})
				result := cicyRunTool(shortID, name, input, cfg)
				argJSON, _ := json.Marshal(input)
				emit(M{"type": "tool", "name": name, "arg": string(argJSON), "result": truncateForLog(result, 600)})
				toolResults = append(toolResults, M{
					"type":        "tool_result",
					"tool_use_id": toolID,
					"content":     result,
				})
			}
		}

		if len(toolResults) == 0 || stopReason != "tool_use" {
			session.persistLocked(workspace)
			return true
		}
		session.messages = append(session.messages, M{"role": "user", "content": toolResults})
	}

	emit(M{"type": "error", "error": fmt.Sprintf("tool loop exceeded %d rounds, stopping", cicyMaxToolRounds)})
	session.persistLocked(workspace)
	return false
}
