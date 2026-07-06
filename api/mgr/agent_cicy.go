// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

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
	"sort"
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

// cicyGatewayBase is this instance's local gateway origin, derived from the live
// PORT (not a hardcoded 8008) so a non-default-port instance routes its cicy
// agents' LLM calls to ITS OWN gateway instead of the host instance on 8008.
func cicyGatewayBase() string { return "http://127.0.0.1:" + runtimeAPIBasePort() }

// cicy persona/base text is NOT hardcoded here. It lives in
// ~/cicy-ai/memory/agents/ (seeded from embed/agent-roles/), the single template
// source: one universal template "assistant" is both the system-prompt base
// (resolved via resolveSystemBase) and the no-role default persona.

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

// cicyDefaultMaxToolRounds bounds the model→tool→model rounds in ONE cicy turn,
// applied only to the cicy IN-PROCESS agent (we drive its loop); claude/codex/
// opencode run their own CLI loop through the transparent gateway with NO cap by
// default (Claude Code's `maxTurns` is opt-in; absent ⇒ unbounded). We mirror its
// big-subagent default of 200 as a pure runaway backstop — high enough that real
// multi-step work (batch governance) never hits it, low enough to stop a
// degenerate model looping forever. A role's meta.yaml `max_tool_rounds`
// overrides it per role. On reaching the cap we WRAP UP gracefully (final round
// runs tool-free so the model gives an answer) rather than erroring out.
const cicyDefaultMaxToolRounds = 200

// cicyMaxRoundsFor resolves the round cap: the role's meta.yaml override if set,
// else the global default.
func cicyMaxRoundsFor(cfg liteConfig) int {
	if cfg.maxToolRounds > 0 {
		return cfg.maxToolRounds
	}
	return cicyDefaultMaxToolRounds
}

// cicyWrapUpInstruction is appended to the system prompt on the final tool round
// so the model closes out instead of being abruptly cut off.
const cicyWrapUpInstruction = "[System notice] You have used up this turn's tool-call budget. Do not call any more tools this round — give the most complete final answer you can from the information already gathered; if the task is genuinely unfinished, state how far you got and what remains."

// cicyMaybeWrapUp appends the wrap-up instruction to the system prompt on the
// final round, leaving it untouched otherwise.
func cicyMaybeWrapUp(systemPrompt string, final bool) string {
	if final {
		return systemPrompt + "\n\n" + cicyWrapUpInstruction
	}
	return systemPrompt
}

// cicyMaxTurnAutoRetries bounds how many times a turn that ended on a TRANSIENT
// "error" outcome (a gateway drop that survived the per-request retries, e.g. a
// mid-stream disconnect) is auto-re-run before giving up — so the agent doesn't
// sit stuck at the error waiting for a manual 重试. cancelled / blocked are
// terminal and never auto-retried.
const cicyMaxTurnAutoRetries = 2

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
// gateway the main turn uses (proven creds/model routing per agent), marked
// AUXILIARY so the audit layer skips snapshots: compact leaves no separate
// conversation dir — its only on-disk traces live in the current conversation's
// dir (the current.<ts>.json archive + the reseeded current.json + the ack).
var cicyCompactSummarize = cicySummarizeViaGateway

// cicySummarizeViaGateway sends the transcript to the agent's model through the
// local gateway and returns the assembled text. A no-op emit (we only want the
// final text, not streamed deltas).
func cicySummarizeViaGateway(ctx context.Context, shortID, convID, model, transcript string) (string, error) {
	payload := M{
		"model": model,
		// 4096:摘要必须容得下「任务状态 + 当前工作 + 下一步」的完整收尾;1024 会把
		// 长对话的摘要拦腰截断,恰好丢掉最重要的近期内容(Claude Code 的压缩摘要
		// 同样是数千 token 量级)。
		"max_tokens": 4096,
		"system":     []M{{"type": "text", "text": cicyCompactSystemPrompt}},
		"messages":   []M{{"role": "user", "content": transcript}},
	}
	resp, _, err := cicyCallGateway(ctx, shortID, convID, "compact", payload, func(M) {})
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

// cicyCompactSystemPrompt follows Claude Code's compaction-prompt structure (see
// the cicy-claude extraction): sectioned output emphasizing errors-and-fixes /
// user corrections / current work / next step, with no assumed agent role
// (/compact and auto-compact apply to ALL cicy agents, not just the PM).
const cicyCompactSystemPrompt = `You are a conversation-history compactor. The input is a conversation between an AI agent and its user, including the agent's tool-call records. Produce a structured summary that lets the agent continue working seamlessly in a fresh context after compaction. Your summary MUST contain the following sections:

1. User goals and requests: every explicit request and intent from the user, item by item — never merge or drop any;
2. Key decisions and conclusions: settled approaches, conclusions, important findings;
3. Tasks and status: every dispatched or in-progress task (owner, what it is, done/test/in-progress/blocked) — not a single one may be missing;
4. Files and code: concrete file paths touched, key changes, important code snippets or function names (if any);
5. Errors and fixes: errors encountered and how they were resolved — pay special attention to cases where the user corrected the approach (if the user told the agent to do something differently, record it);
6. Constraints and prohibitions: the user's explicit preferences, constraints, and forbidden actions — keep key wording verbatim where possible;
7. Current work: precisely what was being worked on immediately before compaction — down to the file/step, and where it is stuck if blocked;
8. Next step: what to do immediately next. It MUST align with the user's most recent explicit request — do not invent new tasks or resurrect old completed ones.

Distill; do not replay small talk verbatim. But user requests, task status, and constraints must be complete, and recent messages take priority over earlier ones. Write the summary in the conversation's primary language. Output ONLY the summary body — no preamble, pleasantries, or explanations.`

const cicyCompactSummaryPrefix = "[Compressed summary of the earlier conversation, kept for context continuity; the most recent original messages follow.]\n\n"

// cicyCompactSummaryPrefixLegacyCN is the pre-2026-07 Chinese prefix; still
// detected (slice boundary + UI marker) so existing conversations keep working.
const cicyCompactSummaryPrefixLegacyCN = "[以下是更早对话的压缩摘要,用于保持上下文连续;最近的原始对话紧随其后。]\n\n"

// cicyIsCompactSummaryContent reports whether a message body is a compaction
// summary (current English prefix or the legacy Chinese one).
func cicyIsCompactSummaryContent(c string) bool {
	return strings.HasPrefix(c, cicyCompactSummaryPrefix) || strings.HasPrefix(c, cicyCompactSummaryPrefixLegacyCN)
}

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
const cicyOutcomePrefix = "(no reply this turn"

// cicyOutcomePrefixLegacyCN is the pre-2026-07 Chinese prefix; still detected so
// existing records keep rendering as outcomes.
const cicyOutcomePrefixLegacyCN = "（本轮未生成回复"

// cicyOutcomeLegacyMark is the pre-2026-06-09 marker; still detected by the display
// relabel + compaction filter so any lingering old records clean up instead of
// showing raw symbols.
const cicyOutcomeLegacyMark = "⟦cicy-turn-outcome⟧"

// cicyOutcomeMarkerText renders the clean wire/display text for a turn outcome.
func cicyOutcomeMarkerText(kind string) string {
	switch kind {
	case "cancelled":
		return cicyOutcomePrefix + " · cancelled)"
	case "blocked":
		return cicyOutcomePrefix + " · blocked)"
	}
	return cicyOutcomePrefix + " · generation failed)"
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
	marker := map[string]interface{}{
		"id":      aiGatewayCurrentBodyMaxHistoryID(current.Body) + 1,
		"role":    "assistant",
		"content": []interface{}{map[string]interface{}{"type": "text", "text": cicyOutcomeMarkerText(kind)}},
	}
	// detail(如 blocked 的具体拦截原因)挂在 current.json 的 marker 上作为**展示字段**,
	// 供 UI 在「已拦截」卡里 inline 显示(像余额不足卡显示原因)。这是 display-only:下一轮
	// wire 请求体由 session.messages 经 cicyRequestMessages 重新构建(marker 文本干净、无此
	// 字段),所以不上 wire、不污染压缩。reload 时 web 读 current.json → 原因仍在。
	if d := strings.TrimSpace(detail); d != "" {
		marker["cicy_outcome_detail"] = d
	}
	msgs = append(msgs, marker)
	body["messages"] = msgs
	current.Body = body
	_ = aiGatewayWriteCurrentSnapshot(shortID, current)
}

// cicyOutcomeKindFromText returns "cancelled"/"error" if s is an outcome marker
// (new clean form OR the legacy "⟦cicy-turn-outcome⟧…" form), else "".
func cicyOutcomeKindFromText(s string) string {
	isNew := strings.HasPrefix(s, cicyOutcomePrefix)
	isCN := strings.HasPrefix(s, cicyOutcomePrefixLegacyCN)
	isOld := strings.HasPrefix(s, cicyOutcomeLegacyMark)
	if !isNew && !isCN && !isOld {
		return ""
	}
	if strings.Contains(s, "已停止") || strings.Contains(s, "cancelled") {
		return "cancelled"
	}
	if strings.Contains(s, "已拦截") || strings.Contains(s, "blocked") {
		return "blocked"
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
				b.WriteString(role + " [calls " + name + " " + truncateForLog(string(arg), 300) + "]\n")
			case "tool_result":
				b.WriteString("tool result: " + truncateForLog(cicyToolResultText(bm["content"]), 400) + "\n")
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
	summary, err := cicyCompactSummarize(ctx, shortID, "", model, cicyRenderHistoryForCompaction(msgs[:keepFrom]))
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

// cicyNewConversationID returns a random UUIDv4-shaped conversation id, matching
// the format claude-type agents' session ids use so all conversation dirs under
// .cicy/history/chat/ look alike. The id is NOT persisted on its own —
// current.json's conversation_id field is the durable record; a fresh id
// crystallizes there with the first turn (or the migration/compact seed).
func cicyNewConversationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("conv-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
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

// ── snapshot-backed persistence ────────────────────────────────────────────
// The conversation store IS the gateway audit pair: current.json (the last wire
// request = full history snapshot, display ids annotated) + reply.json (the
// answer's content items). conversation.json is no longer written; restoring a
// session = current.json body messages (display ids stripped) + the reply items
// not yet folded into a request (display ids stripped, tool_id → protocol id).

// cicyStripWireAnnotations returns msg without the display-level "id" the
// snapshot annotator added, and without cache_control markers (re-added fresh at
// request build; letting restored ones accumulate would eventually exceed
// Anthropic's 4-breakpoint cap). Block-level tool_use ids are protocol data and
// are preserved.
func cicyStripWireAnnotations(msg map[string]interface{}) M {
	out := M{}
	for k, v := range msg {
		if k == "id" {
			continue
		}
		out[k] = v
	}
	if blocks, ok := out["content"].([]interface{}); ok {
		clean := make([]interface{}, len(blocks))
		for i, b := range blocks {
			if bm, ok := b.(map[string]interface{}); ok {
				if _, has := bm["cache_control"]; has {
					cp := map[string]interface{}{}
					for k, v := range bm {
						if k != "cache_control" {
							cp[k] = v
						}
					}
					clean[i] = cp
					continue
				}
			}
			clean[i] = b
		}
		out["content"] = clean
	}
	return out
}

// cicyMessagesFromCurrentBody rebuilds Anthropic-format session messages from a
// current.json body. The body shape depends on the agent's provider protocol:
// an anthropic provider stores the request natively (top-level system, block
// content), while an openai provider stores the BRIDGED Chat Completions shape
// (system inside messages[0], assistant as string + tool_calls, tool_result as
// role:"tool") — transformMessagesRequestToChatCompletions runs before the
// audit snapshot. Both must restore.
func cicyMessagesFromCurrentBody(body map[string]interface{}) []M {
	msgs := aiGatewaySlice(body["messages"])
	if len(msgs) == 0 {
		return nil
	}
	chatShape := false
	if _, hasSystem := body["system"]; !hasSystem {
		for _, raw := range msgs {
			m := aiGatewayMap(raw)
			switch aiGatewayString(m["role"]) {
			case "system", "tool":
				chatShape = true
			}
			if _, ok := m["tool_calls"]; ok {
				chatShape = true
			}
			if _, ok := m["reasoning_content"]; ok {
				chatShape = true
			}
		}
	}
	if chatShape {
		return cicyMessagesFromChatShape(msgs)
	}
	out := make([]M, 0, len(msgs))
	for _, raw := range msgs {
		m := aiGatewayMap(raw)
		if len(m) == 0 {
			continue
		}
		clean := cicyStripWireAnnotations(m)
		if cicyMessageContentEmpty(clean) {
			continue // degenerate empty-content message — never restore it
		}
		out = append(out, clean)
	}
	return out
}

// cicyMessagesFromChatShape converts Chat Completions messages back into the
// Anthropic shape the cicy session speaks: system dropped (rebuilt from config
// each turn), assistant reasoning_content → thinking block (the bridge's "."
// validator placeholder excluded), tool_calls → tool_use blocks, consecutive
// role:"tool" results merged into one user message of tool_result blocks.
func cicyMessagesFromChatShape(msgs []interface{}) []M {
	out := make([]M, 0, len(msgs))
	var pendingResults []interface{}
	flush := func() {
		if len(pendingResults) > 0 {
			out = append(out, M{"role": "user", "content": pendingResults})
			pendingResults = nil
		}
	}
	for _, raw := range msgs {
		m := aiGatewayMap(raw)
		switch aiGatewayString(m["role"]) {
		case "system":
			continue
		case "tool":
			pendingResults = append(pendingResults, map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": aiGatewayString(m["tool_call_id"]),
				"content":     aiGatewayString(m["content"]),
			})
		case "user":
			flush()
			out = append(out, M{"role": "user", "content": m["content"]})
		case "assistant":
			flush()
			var blocks []interface{}
			if rc := strings.TrimSpace(aiGatewayString(m["reasoning_content"])); rc != "" && rc != "." {
				blocks = append(blocks, map[string]interface{}{"type": "thinking", "thinking": rc, "signature": ""})
			}
			switch c := m["content"].(type) {
			case string:
				if strings.TrimSpace(c) != "" {
					blocks = append(blocks, map[string]interface{}{"type": "text", "text": c})
				}
			case []interface{}:
				for _, p := range c {
					pm := aiGatewayMap(p)
					if aiGatewayString(pm["type"]) == "text" {
						blocks = append(blocks, map[string]interface{}{"type": "text", "text": aiGatewayString(pm["text"])})
					}
				}
			}
			for _, tc := range aiGatewaySlice(m["tool_calls"]) {
				tcm := aiGatewayMap(tc)
				fn := aiGatewayMap(tcm["function"])
				var input interface{} = map[string]interface{}{}
				if args := aiGatewayString(fn["arguments"]); args != "" {
					var parsed interface{}
					if json.Unmarshal([]byte(args), &parsed) == nil {
						input = parsed
					}
				}
				blocks = append(blocks, map[string]interface{}{
					"type": "tool_use", "id": aiGatewayString(tcm["id"]),
					"name": aiGatewayString(fn["name"]), "input": input,
				})
			}
			if len(blocks) > 0 {
				out = append(out, M{"role": "assistant", "content": blocks})
			}
		}
	}
	flush()
	return out
}

// cicyAssistantFromReplyItems rebuilds the final assistant message from the
// reply items NOT yet folded into current.json. reply.Items accumulates across
// every round of a turn, but current.json (the LAST round's request) already
// carries the earlier rounds verbatim — so only the suffix after the last
// tool_use item whose tool_id already appears in the current messages is new.
// Display ids are dropped; tool_id is restored as the protocol tool_use id.
// Thinking items are intentionally NOT restored (snapshots don't carry the
// Anthropic signature; a completed turn's thinking may be omitted on passback).
func cicyAssistantFromReplyItems(items []map[string]interface{}, currentMsgs []M) (M, bool) {
	seen := map[string]bool{}
	for _, m := range currentMsgs {
		cicyForEachBlock(m, func(bm map[string]interface{}, typ string) {
			if typ == "tool_use" {
				if id := aiGatewayString(bm["id"]); id != "" {
					seen[id] = true
				}
			}
		})
	}
	start := 0
	for i := len(items) - 1; i >= 0; i-- {
		if aiGatewayString(items[i]["type"]) != "tool_use" {
			continue
		}
		if tid := aiGatewayString(items[i]["tool_id"]); tid != "" && seen[tid] {
			start = i + 1
			break
		}
	}
	var blocks []interface{}
	for _, it := range items[start:] {
		switch aiGatewayString(it["type"]) {
		case "text":
			if t := aiGatewayString(it["text"]); strings.TrimSpace(t) != "" {
				blocks = append(blocks, map[string]interface{}{"type": "text", "text": t})
			}
		case "tool_use":
			blocks = append(blocks, map[string]interface{}{
				"type": "tool_use", "id": aiGatewayString(it["tool_id"]),
				"name": aiGatewayString(it["name"]), "input": it["input"],
			})
		}
	}
	if len(blocks) == 0 {
		return nil, false
	}
	return M{"role": "assistant", "content": blocks}, true
}

// cicyRestoreSessionMessages rebuilds the session history from the snapshot
// pair. The reply is folded in only when it demonstrably answers THIS
// current.json: same conversation, completed, and the request didn't already
// end on an assistant message (an attached outcome marker).
func cicyRestoreSessionMessages(shortID, convID string) []M {
	current := agentInspectorLoadCurrent(shortID)
	if current.ConversationID != "" && convID != "" && current.ConversationID != convID {
		return nil // snapshot belongs to another conversation (post-/clear leftovers)
	}
	msgs := cicyMessagesFromCurrentBody(aiGatewayMap(current.Body))
	if len(msgs) == 0 {
		return nil
	}
	lastRole := aiGatewayString(msgs[len(msgs)-1]["role"])
	reply := agentInspectorLoadReply(shortID)
	if lastRole != "assistant" && reply.Status == "completed" && len(reply.Items) > 0 &&
		reply.TurnID != cicySlashAckTurnID &&
		(reply.ConversationID == "" || current.ConversationID == "" || reply.ConversationID == current.ConversationID) {
		if last, ok := cicyAssistantFromReplyItems(reply.Items, msgs); ok {
			msgs = append(msgs, last)
		}
	}
	return cicyBalanceToolCalls(msgs)
}

// cicySeedCurrentSnapshot updates current.json when history changes OUTSIDE a
// gateway request (one-time conversation.json migration; /compact's summary
// reset). It clones the LIVE snapshot and replaces ONLY body.messages — every
// other field (provider, model, url, headers, request ids, and the body's
// system/tools/model) carries over verbatim, so the seed is indistinguishable
// from a real wire snapshot. The annotator renumbers the messages.
func cicySeedCurrentSnapshot(shortID, convID string, msgs []M) {
	cicySeedCurrentSnapshotReq(shortID, convID, msgs, "", nil)
}

// cicySeedCurrentSnapshotReq seeds the display snapshot AND mirrors the turn's
// wire system prompt + tool defs into it. cicy owns its current.json (it does
// its own compaction, so the wire body may be a sliced sub-history that must not
// clobber the full display history — hence X-Cicy-Current-Owned). But the
// inspector reads the system/tools out of this snapshot, so without seeding them
// here cicy alone shows no prompt/tools (CLI agents get them for free from the
// gateway-captured wire body). systemPrompt is per-role (cfg.systemPrompt, from
// resolveSystemBase) so each role's own prompt shows.
func cicySeedCurrentSnapshotReq(shortID, convID string, msgs []M, systemPrompt string, tools []M) {
	now := time.Now().UTC().Format(time.RFC3339)
	snap := cicySeededSnapshot(agentInspectorLoadCurrent(shortID), shortID, convID, now, msgs, systemPrompt, tools)
	_ = aiGatewayWriteCurrentSnapshot(shortID, snap)
}

// cicySeededSnapshot is the pure clone-and-replace: take the live snapshot,
// swap ONLY body.messages, keep everything else verbatim.
func cicySeededSnapshot(snap aiGatewayCurrentSnapshot, shortID, convID, now string, msgs []M, systemPrompt string, tools []M) aiGatewayCurrentSnapshot {
	body := make([]interface{}, len(msgs))
	for i, m := range msgs {
		body[i] = map[string]interface{}(m)
	}
	if old := aiGatewayMap(snap.Body); len(old) > 0 {
		nb := map[string]interface{}{}
		for k, v := range old {
			nb[k] = v
		}
		// A chat-shaped body (openai provider bridge) keeps its persona inside
		// messages[0] role:"system" — carry it over so the seed doesn't drop it.
		if _, hasSystem := nb["system"]; !hasSystem {
			if oldMsgs := aiGatewaySlice(nb["messages"]); len(oldMsgs) > 0 {
				if m0 := aiGatewayMap(oldMsgs[0]); aiGatewayString(m0["role"]) == "system" {
					body = append([]interface{}{oldMsgs[0]}, body...)
				}
			}
		}
		nb["messages"] = body
		snap.Body = nb
	} else {
		// No prior snapshot to inherit from (fresh agent): minimal but honest.
		snap.Body = map[string]interface{}{"messages": body}
		snap.Status = "completed"
		snap.StartedAt = now
		snap.Timestamp = now
	}
	// Mirror the wire request's per-role system prompt + tool defs into the
	// snapshot body so the inspector renders cicy's 提示词/工具 identically to a
	// CLI agent. cicy's payload is ALWAYS Anthropic-shaped (top-level `system`
	// block) regardless of upstream provider, so writing it here is canonical and
	// unambiguous for the inspector (which reads body["system"]).
	if systemPrompt != "" || len(tools) > 0 {
		nb := aiGatewayMap(snap.Body)
		if nb == nil {
			nb = map[string]interface{}{}
		}
		if systemPrompt != "" {
			nb["system"] = []interface{}{map[string]interface{}{"type": "text", "text": systemPrompt}}
		}
		if len(tools) > 0 {
			ts := make([]interface{}, len(tools))
			for i, td := range tools {
				ts[i] = map[string]interface{}(td)
			}
			nb["tools"] = ts
		}
		snap.Body = nb
	}
	snap.AgentID = shortID
	snap.ConversationID = convID
	snap.UpdatedAt = now
	// Per-TURN identifiers must not leak into a seeded snapshot: the seed is not
	// a wire request, and stale turn/request ids made a cleared conversation look
	// like the old one. Conversation-scoped fields (provider/model/url/headers,
	// body system/tools) are what carries over.
	snap.TurnID = ""
	snap.RequestID = ""
	snap.RequestIDs = nil
	snap.ActiveRequestIDs = nil
	snap.ConversationIDs = []string{convID}
	snap.Status = "completed"
	return snap
}

func getCicySession(shortID, workspace string) *cicySession {
	cicySessionsMu.Lock()
	defer cicySessionsMu.Unlock()
	if s, ok := cicySessions[shortID]; ok {
		return s
	}
	migrateCicyStateDir(workspace)
	s := &cicySession{}
	// The conversation id lives in current.json — nowhere else. Missing snapshot
	// (fresh agent, or right after /clear) → mint a random one; it becomes durable
	// when the first turn (or a seed) writes current.json.
	current := agentInspectorLoadCurrent(shortID)
	s.convID = strings.TrimSpace(current.ConversationID)
	if s.convID == "" {
		s.convID = cicyNewConversationID()
	}
	// Retired sidecar from an earlier revision; the id is in current.json.
	_ = os.Remove(filepath.Join(cicyConvDir(workspace), "conversation_id"))
	// One-time migration: a pre-refactor conversation.json (current dir or the
	// legacy .cicy/dispatcher one) is the fuller record — load it, seed the
	// snapshot store from it under a fresh random conversation id, and park the
	// file as .bak so every later boot restores from current.json + reply.json.
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
		if json.Unmarshal(raw, &msgs) == nil && len(msgs) > 0 {
			s.messages = msgs
			if strings.HasPrefix(s.convID, "dispatcher-") || s.convID == "" {
				s.convID = cicyNewConversationID()
			}
			cicySeedCurrentSnapshot(shortID, s.convID, s.messages)
		}
		_ = os.Rename(histPath, histPath+".bak")
	}
	if len(s.messages) == 0 {
		s.messages = cicyRestoreSessionMessages(shortID, s.convID)
	}
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

// persistLocked normalizes the in-memory history. Durability now lives in the
// gateway snapshot pair (current.json = full request snapshot, reply.json =
// answer items) — conversation.json is NO LONGER written; restore goes through
// cicyRestoreSessionMessages. Caller holds s.mu.
func (s *cicySession) persistLocked(workspace string) {
	s.messages = cicyBalanceToolCalls(s.messages)
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
const cicySyntheticToolResult = "(tool result unavailable: the previous turn was interrupted before the result arrived; auto-filled to keep tool_use/tool_result pairing.)"

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

// cicyTextFromBlocks concatenates the text of all text-type content blocks
// (Anthropic content shape) — used to pull a human-readable notice out of a gateway
// response's content (e.g. the audit-block reason in the legacy 200+SSE path).
func cicyTextFromBlocks(content interface{}) string {
	var b strings.Builder
	cicyForEachBlock(M{"content": content}, func(bm map[string]interface{}, typ string) {
		if typ != "text" {
			return
		}
		if s, _ := bm["text"].(string); s != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(s)
		}
	})
	return b.String()
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

// cicyAllToolDefs holds the IN-PROCESS built-in tool defs. There are only two:
// `skill` (discover + read any installed skill's SKILL.md — the cicy-todo /
// cicy-agent / … ecosystem) and `shell` (run a skill's CLI, or anything else).
// No per-skill hardcoded tools: 装个 skill 即可用, 改 skill 即更新.
func cicyAllToolDefs() []M {
	return append([]M{}, []M{
		{
			"name":        "skill",
			"description": "Discover and read installed skills. Call with no name to list every installed skill with a one-line summary; call with a name to get that skill's SKILL.md (its CLI usage). Once you've read the usage, run the skill's CLI via the `shell` tool (e.g. `cicy-todo add \"...\"` to track a todo, `cicy-agent msg w-xxx \"...\"` to hand work to another agent).",
			"input_schema": M{
				"type": "object",
				"properties": M{
					"name": M{"type": "string", "description": "Skill name (e.g. cicy-todo). Leave empty to list all installed skills."},
				},
			},
		},
		{
			"name":        "shell",
			"description": "Run a single shell command on this host (PowerShell on Windows, bash on macOS/Linux) and get back its stdout/stderr and exit code. This is how you actually do things — run a skill's CLI, inspect or change the system, install and start software. Run one command at a time and read the result before deciding the next; confirm with the user before any destructive command.",
			"input_schema": M{
				"type": "object",
				"properties": M{
					"command": M{"type": "string", "description": "The command to run (PowerShell syntax on Windows, bash syntax on Unix)."},
					"cwd":     M{"type": "string", "description": "Optional working directory."},
					"timeout": M{"type": "integer", "description": "Optional timeout in seconds (default 120, max 1800)."},
				},
				"required": []string{"command"},
			},
		},
	}...)
}

// cicySkillTool implements the in-process `skill` tool. With no name it lists
// every installed skill (name + one-line description scanned from each SKILL.md
// frontmatter); with a name it returns that skill's SKILL.md (+ references/help.md)
// so the agent learns its CLI and then runs it via `shell`. Reads SKILL.md the
// same way claude's Skill tool does — zero per-skill hardcoding.
func cicySkillTool(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		entries, err := os.ReadDir(cicySkillsDir)
		if err != nil {
			return "(no skills installed)"
		}
		var lines []string
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			md, err := os.ReadFile(filepath.Join(cicySkillsDir, e.Name(), "SKILL.md"))
			if err != nil {
				continue
			}
			n, desc := cicySkillMeta(string(md))
			if n == "" {
				n = e.Name()
			}
			lines = append(lines, "- "+n+": "+desc)
		}
		if len(lines) == 0 {
			return "(no skills installed)"
		}
		sort.Strings(lines)
		return "Installed skills — call skill(name) for usage, then run its CLI via shell:\n" + strings.Join(lines, "\n")
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return "error: bad skill name"
	}
	md, err := os.ReadFile(filepath.Join(cicySkillsDir, name, "SKILL.md"))
	if err != nil {
		return "error: skill not found: " + name + " (call skill with no arguments to list installed ones)"
	}
	out := string(md)
	if help, herr := os.ReadFile(filepath.Join(cicySkillsDir, name, "references", "help.md")); herr == nil && len(help) > 0 {
		out += "\n\n===== references/help.md =====\n" + string(help)
	}
	return out
}

// cicySystemBlocks builds the `system` field: the role's system.md base, plus a
// block listing the installed skills (name + one-line description) so the agent
// knows what's available without calling the skill tool first — like claude lists
// available skills in context. Byte-stable unless skills change, so it caches.
func cicySystemBlocks(systemPrompt string) []M {
	blocks := []M{{"type": "text", "text": systemPrompt}}
	if list := cicySkillTool(""); strings.TrimSpace(list) != "" && !strings.HasPrefix(list, "(") {
		blocks = append(blocks, M{"type": "text", "text": list})
	}
	blocks[len(blocks)-1]["cache_control"] = M{"type": "ephemeral"}
	return blocks
}

// cicySkillMeta pulls name + description from a SKILL.md YAML frontmatter.
func cicySkillMeta(md string) (name, desc string) {
	for _, ln := range strings.Split(md, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "name:"):
			name = strings.TrimSpace(strings.TrimPrefix(t, "name:"))
		case strings.HasPrefix(t, "description:"):
			desc = strings.TrimSpace(strings.TrimPrefix(t, "description:"))
		case t == "---" && (name != "" || desc != ""):
			return
		}
	}
	return
}

// cicySudoRe matches sudo in COMMAND position — start of line/script, or right
// after a separator (; & | && || $( ` ). An argument mention like `grep sudo f`
// stays allowed; `x && sudo y` / `echo p | sudo -S y` are caught.
var cicySudoRe = regexp.MustCompile("(?m)(^|[;&|(`]|\\$\\()\\s*sudo\\b")

// platformShellArgv builds the argv to run a single command string through the
// host's native shell: PowerShell on Windows (cicy-code ships as a native exe
// there, PowerShell is always present), bash elsewhere.
func platformShellArgv(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", command}
	}
	return []string{"bash", "-lc", command}
}

// cicyRunTool executes one built-in / custom tool call. turnCtx is the TURN's
// cancellable context — the user's 停止 press cancels it, and every subprocess
// here derives from it so a cancel actually kills the running command instead
// of letting it run out its own timeout while the turn hangs (that hang was
// exactly the "点取消停不下来" bug: the shell tool used context.Background()).
func cicyRunTool(turnCtx context.Context, selfShortID, name string, input map[string]interface{}, cfg liteConfig) string {
	if turnCtx == nil {
		turnCtx = context.Background()
	}
	enabled := cfg.enabledTools
	if !enabled[name] {
		return "error: tool " + name + " is not enabled for this agent"
	}
	// Custom tools (declared in lite-config.json) route to the guarded executor.
	if _, isCustom := cfg.customTools[name]; isCustom {
		return runLiteCustomTool(turnCtx, cfg, selfShortID, name, input)
	}
	str := func(key string) string {
		v, _ := input[key].(string)
		return strings.TrimSpace(v)
	}
	switch name {
	case "skill":
		return cicySkillTool(str("name"))
	case "shell":
		command := str("command")
		if command == "" {
			return "error: command required"
		}
		// sudo in a headless agent has no tty to read the password from — it
		// just hangs until the timeout (a real w-1001 turn sat 30+ min on
		// `sudo pmset`, unkillable from the UI before the ctx fix). Refuse it
		// up front so the model reroutes instead of wedging the turn.
		if cicySudoRe.MatchString(command) {
			return "error: sudo is not available to a headless agent (no tty for the password prompt — it would hang until timeout). Re-run without sudo, or ask the user to run the privileged step themselves."
		}
		timeout := 120 * time.Second
		if v, ok := input["timeout"].(float64); ok && v > 0 {
			timeout = time.Duration(v) * time.Second
			if timeout > 30*time.Minute {
				timeout = 30 * time.Minute
			}
		}
		// Derive from the TURN ctx (not Background): a user cancel kills the
		// command immediately instead of the turn hanging until the command's own
		// timeout elapses.
		ctx, cancel := context.WithTimeout(turnCtx, timeout)
		defer cancel()
		argv := platformShellArgv(command)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		// bash -lc spawns grandchildren that inherit the output pipe; killing only
		// the direct child would leave CombinedOutput blocked on the open pipe
		// forever. WaitDelay forces Wait to return shortly after ctx ends anyway.
		cmd.WaitDelay = 5 * time.Second
		if cwd := str("cwd"); cwd != "" {
			cmd.Dir = cwd
		}
		out, err := cmd.CombinedOutput()
		log.Printf("[cicy-shell] agent=%s bytes=%d err=%v cmd=%q", selfShortID, len(out), err, command)
		s := string(out)
		const maxOut = 12000
		if len(s) > maxOut {
			s = s[:maxOut] + "\n…(output truncated)"
		}
		if turnCtx.Err() != nil {
			return "error: command cancelled by user\n" + s
		}
		if ctx.Err() == context.DeadlineExceeded {
			return "error: command timed out\n" + s
		}
		if err != nil {
			return fmt.Sprintf("exit error: %v\n%s", err, s)
		}
		if strings.TrimSpace(s) == "" {
			return "(command succeeded, no output)"
		}
		return s
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
// cicyCompactSliceStart returns the index of the LAST compact-summary message
// (the wire boundary): requests are sent from there onward, while the full
// history (everything before it included) stays in current.json for display.
// 0 when the conversation has never been compacted.
func cicyCompactSliceStart(history []M) int {
	for i := len(history) - 1; i >= 0; i-- {
		if r, _ := history[i]["role"].(string); r != "user" {
			continue
		}
		if c, ok := history[i]["content"].(string); ok && cicyIsCompactSummaryContent(c) {
			return i
		}
	}
	return 0
}

// cicyMessageContentEmpty reports a degenerate message (content "" or []) —
// upstreams reject these with "all messages must have non-empty content".
func cicyMessageContentEmpty(m M) bool {
	switch c := m["content"].(type) {
	case string:
		return strings.TrimSpace(c) == ""
	case []interface{}:
		return len(c) == 0
	case []M:
		return len(c) == 0
	case nil:
		return true
	}
	return false
}

func cicyRequestMessages(history []M) []M {
	// Claude-style boundary slice: the model gets [summary, …]; the display
	// snapshot keeps the whole conversation.
	history = history[cicyCompactSliceStart(history):]
	if len(history) == 0 {
		return history
	}
	out := make([]M, 0, len(history))
	// API boundary: display-level ids (snapshot annotation) never go upstream —
	// the id is a UI identifier, the protocol has none — and degenerate
	// empty-content messages (a failed round's empty response, historically
	// appended unguarded) are dropped: upstreams 400 on them. Non-destructive:
	// the in-memory history is left untouched.
	for _, m := range history {
		if cicyMessageContentEmpty(m) {
			continue
		}
		if _, has := m["id"]; has {
			out = append(out, cicyStripWireAnnotations(m))
		} else {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return out
	}
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

// cicyInjectRoleContext prepends the agent's role (its AGENTS.md body) to the
// FIRST user message as a leading <role> context block — the same way a CLI
// agent carries its AGENTS.md/CLAUDE.md into the conversation rather than the
// system prompt. The system prompt stays the shared system.md base; the role is
// message context. Wire-only: msgs maps are cloned so the persisted session
// history is untouched. No-op when the role is empty or there's no user message.
func cicyInjectRoleContext(msgs []M, roleContext string) []M {
	roleContext = strings.TrimSpace(roleContext)
	if roleContext == "" || len(msgs) == 0 {
		return msgs
	}
	block := M{"type": "text", "text": "<role>\n" + roleContext + "\n</role>"}
	out := make([]M, len(msgs))
	copy(out, msgs)
	for i, m := range out {
		if role, _ := m["role"].(string); role != "user" {
			continue
		}
		nm := M{}
		for k, v := range m {
			nm[k] = v
		}
		switch c := m["content"].(type) {
		case string:
			nm["content"] = []interface{}{block, M{"type": "text", "text": c}}
		case []interface{}:
			nm["content"] = append([]interface{}{block}, c...)
		case []M:
			merged := make([]interface{}, 0, len(c)+1)
			merged = append(merged, block)
			for _, b := range c {
				merged = append(merged, b)
			}
			nm["content"] = merged
		default:
			nm["content"] = []interface{}{block}
		}
		out[i] = nm
		break
	}
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
	// default_model 空 → 用 CICY agent-type 的默认 provider 的 model(providers.default["cicy"],
	// 默认 defaultAnthropic→deepseek-v4-pro,与 claude 同链路)。否则 resolveClaudeStartupModel 的兜底写死按 "claude" 取默认
	// provider,cicy agent 会拿到 claude 链路(defaultAnthropic)的 model、跟自己实际的 provider
	// (cicy 链路)对不上 → 网关 model↔provider 不一致 → 401。这样所有 cicy agent 即便没显式
	// 选过 model,也自动用对的默认 model,不必逐个重选。
	if strings.TrimSpace(defaultModel) == "" {
		if pk := loadDefaultProviderKeyForAgentType("cicy"); pk != "" {
			if pc, ok := loadProviderByKey(pk); ok && pc != nil {
				if m := providerDefaultModelForAgentType(pc, "cicy"); m != "" {
					return m
				}
			}
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
func cicyCallGateway(ctx context.Context, shortID, sessionID, auxKind string, payload M, emit func(M)) (map[string]interface{}, bool, error) {
	payload["stream"] = true
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	url := fmt.Sprintf("%s/api/ai-gateway/anthropic/%s/v1/messages", cicyGatewayBase(), shortID)
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
		// Auxiliary calls (compact summarizer) are audited for usage but must not
		// touch the conversation's current/reply snapshots — no separate dir.
		if auxKind != "" {
			req.Header.Set("X-Cicy-Aux", auxKind)
		} else {
			// Main turns: current.json (full display history) is seeded by the
			// cicy runtime itself; the audit layer must keep it instead of
			// overwriting it with the (possibly compact-sliced) wire body.
			req.Header.Set("X-Cicy-Current-Owned", "1")
		}

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
			auditBlocked := resp.Header.Get("X-Cicy-Audit-Blocked")
			resp.Body.Close()
			// 出站审计拦截(契约 B-plus):非 2xx(如 403)+ X-Cicy-Audit-Blocked 头 →
			// 不是普通网关错误,是终态 blocked。不 retry、不卡(走错误路径天然立即返回)。
			// body.message 作为具体原因透传给 caller 的 blocked 分支渲染。按 header 识别而非
			// 状态码,兼容网关从旧的 200+SSE 切到 403 的过渡期(两路径都认)。
			if auditBlocked != "" {
				var jb map[string]interface{}
				_ = json.Unmarshal(respBody, &jb)
				msg, _ := jb["message"].(string)
				return map[string]interface{}{
					"_cicy_audit_blocked": auditBlocked,
					"_cicy_audit_rules":   resp.Header.Get("X-Cicy-Audit-Rules"),
					"_cicy_audit_message": msg,
				}, false, nil
			}
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
			// 非流式 block 响应(网关自适应:非 stream 请求返单个 Anthropic JSON)也带审计头 →
			// 同样透传,否则非 SSE 路径会把拦截 JSON 当正常答案 commit。
			if parsed != nil {
				if ab := resp.Header.Get("X-Cicy-Audit-Blocked"); ab != "" {
					parsed["_cicy_audit_blocked"] = ab
					parsed["_cicy_audit_rules"] = resp.Header.Get("X-Cicy-Audit-Rules")
				}
			}
			return parsed, false, nil
		}
		// 出站审计拦截(网关契约 A):命中 block 时网关返 200 + 合成 SSE,并带这两个头。
		// 透传给 caller(塞进 result),让它记成 blocked 终态、不把拦截提示 commit 进历史。
		auditBlocked := resp.Header.Get("X-Cicy-Audit-Blocked")
		auditRules := resp.Header.Get("X-Cicy-Audit-Rules")
		result, streamed, aerr := cicyAssembleSSE(resp.Body, emit)
		resp.Body.Close()
		if result != nil && auditBlocked != "" {
			result["_cicy_audit_blocked"] = auditBlocked
			result["_cicy_audit_rules"] = auditRules
		}
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
	// No in-flight turn (e.g. the server restarted and the in-memory session is
	// gone) but reply.json is stuck non-terminal from before — the UI shows a
	// forever-busy turn that cancel can't reach. Treat the user's 停止 as "make
	// it stop": finalize the stale snapshot so the UI unlocks immediately.
	if !canceled {
		if reply := aiGatewayLoadReplySnapshot(shortID); reply.Status != "" && !isAIGatewayReplyTerminal(reply.Status) {
			cicyWriteTerminalReply(shortID, reply.ConversationID)
			canceled = true
		}
	}
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
// session AND the gateway snapshots (current/reply.json — the conversation
// store), all in one shot. Doing it through the live session avoids racing a
// turn that is concurrently rewriting the snapshots.
func clearCicyPane(shortID, workspace string) {
	session := getCicySession(shortID, workspace)
	session.cancelInFlight() // stop any in-flight turn first
	session.mu.Lock()
	session.messages = nil
	// A clear starts a NEW conversation: rotate the random conversation id so the
	// next turn snapshots into a fresh chat/<convID>/ dir (the old one stays on
	// disk for scrollback). The id becomes durable when that turn writes
	// current.json — no sidecar file.
	session.convID = cicyNewConversationID()
	session.persistLocked(workspace)
	newConv := session.convID
	session.mu.Unlock()
	session.forceRelease() // drop busy/queued so it's truly fresh
	// The conversation store must keep existing: seed an EMPTY current.json under
	// the new conversation id (provider/model fields cloned) so the UI lands on a
	// clean empty conversation — never a missing file — and the rotated id is
	// immediately durable. The old conversation's dir stays for scrollback; the
	// stale reply.json is dropped (the slash ack replaces it).
	cicySeedCurrentSnapshot(shortID, newConv, []M{})
	// Repoint the canonical reply.json symlink onto the NEW conversation with a
	// terminal EMPTY reply — /clear just opens a fresh empty conversation, so the UI
	// lands on it clean with NOTHING to render (no "✅ Conversation cleared" ack) and
	// the composer unlocked. The old conversation keeps its own current.json/reply.json
	// for scrollback; nothing is deleted.
	cicyWriteTerminalReply(shortID, newConv)
}

// cicySlashAckTurnID marks a synthetic reply.json written as the visible
// acknowledgment of a slash command. The web UI polls reply.json for the
// answer bubble — the queue/UI delivery path has no live stream, so without
// this the command would execute silently. Restore skips it (it is feedback,
// not conversation content).
const cicySlashAckTurnID = "slash-ack"

func cicyWriteSlashAck(shortID, convID, text string) {
	cicyWriteSlashAckStatus(shortID, convID, text, "completed")
}

// cicyWriteSlashAckStatus also powers the IN-PROGRESS state: /compact writes a
// status="working" reply while the summarizer runs (the UI poll shows the busy
// indicator, Claude-style), then overwrites it with the completed/failed ack.
// Every working write MUST be followed by a terminal one on all paths.
func cicyWriteSlashAckStatus(shortID, convID, text, status string) {
	now := time.Now().UTC().Format(time.RFC3339)
	historyID := int64(1)
	if current := agentInspectorLoadCurrent(shortID); current.ConversationID == convID {
		historyID = int64(aiGatewayCurrentBodyMaxHistoryID(current.Body)) + 1
	}
	_ = aiGatewayWriteReplySnapshot(shortID, aiGatewayReplySnapshot{
		TurnID:         cicySlashAckTurnID,
		ConversationID: convID,
		HistoryID:      historyID,
		Status:         status,
		StartedAt:      now,
		UpdatedAt:      now,
		Answer:         text,
		Items: []map[string]interface{}{
			{"id": 1, "type": "text", "text": text},
		},
	})
}

// cicyResetReplyForNewTurn wipes reply.json to a fresh working placeholder at the
// new turn's answer slot (current.json maxID + 1). A new user turn means the
// previous reply is stale — especially a FAILED turn's ⚠️ error that the new q just
// overwrote (dropTrailingFailedTurnLocked + id reuse). Without this, the web's
// reply.json poll keeps serving the OLD error as the new q's answer until the first
// new token streams in（表现为"新 q 的 a 先显示旧 error,等后端推真 a 才覆盖"）。
// Reset to working/empty → UI shows thinking immediately, then the live stream fills a.
func cicyResetReplyForNewTurn(shortID, convID string) {
	now := time.Now().UTC().Format(time.RFC3339)
	historyID := int64(1)
	if current := agentInspectorLoadCurrent(shortID); current.ConversationID == convID {
		historyID = int64(aiGatewayCurrentBodyMaxHistoryID(current.Body)) + 1
	}
	_ = aiGatewayWriteReplySnapshot(shortID, aiGatewayReplySnapshot{
		ConversationID: convID,
		HistoryID:      historyID,
		Status:         "working",
		StartedAt:      now,
		UpdatedAt:      now,
		Items:          []map[string]interface{}{},
	})
}

// cicyWriteTerminalReply finalizes reply.json to a terminal (completed) empty state.
// Used by the blocked path: the gateway returned 200 (synthetic SSE) so there's no
// normal answer, but cicyResetReplyForNewTurn seeded a "working" placeholder at turn
// start. If left working, the web poll treats it as an in-flight live tail → 永久
// Thinking + spinner 不解锁(且 historyID 可能 > committed marker 而被当 live tail)。
// completed + 空 answer → replyInFlight=false 解锁、hasContent=false 不 attach;拦截态
// 由 current.json 的 blocked marker(cicy_outcome=blocked)渲染红色「已拦截」徽标。
func cicyWriteTerminalReply(shortID, convID string) {
	now := time.Now().UTC().Format(time.RFC3339)
	historyID := int64(1)
	if current := agentInspectorLoadCurrent(shortID); current.ConversationID == convID {
		historyID = int64(aiGatewayCurrentBodyMaxHistoryID(current.Body)) + 1
	}
	_ = aiGatewayWriteReplySnapshot(shortID, aiGatewayReplySnapshot{
		ConversationID: convID,
		HistoryID:      historyID,
		Status:         "completed",
		StartedAt:      now,
		UpdatedAt:      now,
		Answer:         "",
		Items:          []map[string]interface{}{},
	})
}

// archiveCicySnapshots copies the live current.json AND reply.json to
// timestamped siblings (current.<unix>.json / reply.<unix>.json, same stamp)
// inside the conversation dir before /compact rewrites history — so the
// pre-compaction wire snapshot and the final answer both survive for
// scrollback / rollback. Best-effort: a missing snapshot is not an error.
func archiveCicySnapshots(shortID string) {
	dir := aiGatewayHistoryDir(shortID)
	ts := time.Now().Unix()
	for _, name := range []string{"current.json", "reply.json"} {
		canonical := filepath.Join(dir, name)
		data, err := os.ReadFile(canonical) // follows the symlink to the real file
		if err != nil || len(data) == 0 {
			continue
		}
		target := canonical
		if t, e := os.Readlink(canonical); e == nil {
			target = filepath.Join(dir, t)
		}
		archive := strings.TrimSuffix(target, ".json") + fmt.Sprintf(".%d.json", ts)
		_ = os.WriteFile(archive, data, 0644)
	}
}

// compactCicyPane is the manual /compact: it summarizes the WHOLE conversation
// into a single message and resets history to just that summary, keeping the same
// conversation (unlike /clear, which wipes). Unlike the auto-path cicyCompactMessages
// it is NOT gated on a length threshold and keeps NO verbatim tail — a full fold.
// The pre-compaction current.json is archived first so nothing is lost.
func compactCicyPane(ctx context.Context, session *cicySession, shortID, workspace string, emit func(M)) {
	// Never compact mid-turn — it would corrupt the in-flight history.
	if !session.tryOwnTurn() {
		emit(M{"type": "system", "text": "A reply is in flight — try /compact again in a moment."})
		emit(M{"type": "done"})
		return
	}
	defer session.forceRelease()

	session.mu.Lock()
	msgs := append([]M(nil), session.messages...)
	session.mu.Unlock()
	if len(msgs) == 0 {
		emit(M{"type": "system", "text": "Conversation is empty — nothing to compact."})
		emit(M{"type": "done"})
		return
	}

	archiveCicySnapshots(shortID) // best-effort rollback source (current + reply)

	session.mu.Lock()
	convID := session.convID
	session.mu.Unlock()
	// PURELY ADDITIVE: /compact must never write an answer-slot reply — a working
	// or ✅ ack in reply.json overwrites the user's previous answer (which may
	// still ride the live tail). Progress ("压缩中…") is a FRONTEND marker; the
	// backend summarizes silently, then appends the summary (the UI renders it as
	// the ✨已压缩 marker). reply.json is only finalized to a terminal empty state.
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	summary, err := cicyCompactSummarize(cctx, shortID, session.convID, cicyModel(shortID), cicyRenderHistoryForCompaction(msgs))
	if err != nil || strings.TrimSpace(summary) == "" {
		// Failure: leave the conversation EXACTLY as it was — no overwriting ack.
		// The frontend live marker clears via its timeout / terminal reply.
		cicyWriteTerminalReply(shortID, convID)
		emit(M{"type": "done"})
		return
	}

	// Claude-style: history is NEVER cleared. The summary is APPENDED (its id
	// continues the sequence — the UI reconciles incrementally, no renumbering),
	// and only the WIRE request slices from it (cicyCompactSliceStart).
	session.mu.Lock()
	session.messages = append(session.messages, M{"role": "user", "content": cicyCompactSummaryPrefix + strings.TrimSpace(summary)})
	session.persistLocked(workspace)
	cicySeedCurrentSnapshot(shortID, session.convID, session.messages)
	session.mu.Unlock()
	cicyWriteTerminalReply(shortID, convID)
	emit(M{"type": "done"})
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

	if runCicySlashCommand(r.Context(), session, shortID, workspace, text, sse.emit) {
		return
	}

	// Input queueing: if a reply is already in flight for this session, queue
	// this input instead of running a second turn. The in-flight handler drains
	// the queue on completion and merges all queued inputs into ONE follow-up
	// turn (streamed on its own connection). This request returns immediately.
	if session.enqueueIfBusy(text) {
		sse.emit(M{"type": "queued", "text": "Queued — will be handled after the current reply completes."})
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
	autoRetries := 0
	for {
		session.mu.Lock()
		var ok bool
		if first && retry {
			// Pop the failed turn's outcome marker and re-run that same user turn.
			if !session.dropTrailingOutcomeLocked() {
				session.mu.Unlock()
				emit(M{"type": "error", "error": "no turn to retry"})
				emit(M{"type": "done"})
				return // defer clears busy
			}
			ok = cicyRunWindowLocked(ctx, session, shortID, workspace, emit)
		} else {
			ok = runCicyTurnLocked(ctx, session, shortID, workspace, cur, emit)
		}
		first = false
		session.mu.Unlock()

		// Auto-retry a TRANSIENT gateway failure so the agent doesn't get stuck at
		// the error waiting for a manual 重试. A failed turn ends on an "error"
		// outcome marker (cancelled/blocked are terminal — never retried). Dropping
		// the marker re-runs the SAME user turn; because the failed round appended no
		// assistant message (only the marker), the re-run resumes cleanly — even
		// mid-turn after a tool_result. Bounded + backed off; cancel breaks out.
		for !ok && autoRetries < cicyMaxTurnAutoRetries && ctx.Err() == nil {
			session.mu.Lock()
			retryable := session.lastOutcomeKindLocked() == "error"
			session.mu.Unlock()
			if !retryable {
				break
			}
			autoRetries++
			emit(M{"type": "flush", "text": fmt.Sprintf("Error — auto-retrying %d/%d…", autoRetries, cicyMaxTurnAutoRetries)})
			if !cicySleepBackoff(ctx, autoRetries-1, "") {
				break // cancelled during backoff
			}
			session.mu.Lock()
			if session.dropTrailingOutcomeLocked() {
				ok = cicyRunWindowLocked(ctx, session, shortID, workspace, emit)
			}
			session.mu.Unlock()
		}

		if !ok {
			emit(M{"type": "done"})
			return // defer clears busy
		}
		autoRetries = 0
		// Drain inputs queued while this turn ran; merge into one follow-up turn.
		merged, more := session.drainPending()
		if !more {
			released = true
			break
		}
		cur = merged
		emit(M{"type": "flush", "text": "Processing queued input…"})
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
	// Slash commands act here too — the web UI / IM / dispatch queue all deliver
	// through this entry, and a command must never reach the LLM as chat. The
	// result is visible through the snapshots themselves (/clear → empty view,
	// /compact → summary as message #1), so the no-op emit loses nothing.
	if runCicySlashCommand(context.Background(), session, shortID, workspace, text, func(M) {}) {
		go broadcastPollData(shortID)
		return true
	}
	if session.enqueueIfBusy(text) {
		return false // merged into the in-flight owner's drain
	}
	// emit sink is a no-op: the turn persists via the gateway snapshots (the
	// source of truth headless callers read back), so there's no stream to
	// forward here.
	runCicyOwnedTurns(session, shortID, workspace, text, func(M) {})
	go broadcastPollData(shortID)
	return true
}

// runCicySlashCommand intercepts conversation-management commands at EVERY cicy
// input entry (SSE chat endpoint, web UI / IM / queue delivery) so they are
// executed instead of being sent to the LLM as a message. Returns true when the
// text was a command and has been handled.
//
//	/clear   → wipe the conversation (in-memory + snapshots), rotate the
//	           conversation id (a clear starts a NEW conversation)
//	/compact → archive current.json, summarize the WHOLE history into one
//	           message, reset history to just that summary (same conversation)
func runCicySlashCommand(ctx context.Context, session *cicySession, shortID, workspace, text string, emit func(M)) bool {
	cmd := strings.TrimSpace(text)
	// Agent-to-agent mail arrives wrapped as "📮 [w-xxxx] <text>" — strip the
	// attribution so a relayed /clear、/compact still acts as a command.
	if strings.HasPrefix(cmd, "📮") {
		if i := strings.Index(cmd, "] "); i >= 0 {
			cmd = strings.TrimSpace(cmd[i+2:])
		}
	}
	// Conversation-management commands must NEVER run mid-turn: /clear would rotate
	// the conversation under an in-flight reply, /compact would archive/reseed
	// snapshots the running turn still writes to. The composer queues them
	// client-side until idle (dispatcher-chat-queue); this is the backend backstop
	// for raw API / relayed deliveries.
	if cmd == "/clear" || cmd == "/compact" {
		if session.isBusy() {
			log.Printf("[cicy %s] %s refused while a reply is in flight (queue it and retry when idle)", shortID, cmd)
			emit(M{"type": "error", "error": cmd + " can only run when the agent is idle — it was NOT executed; resend after the current reply finishes"})
			emit(M{"type": "done"})
			return true
		}
	}
	switch cmd {
	case "/clear":
		clearCicyPane(shortID, workspace)
		// No ack message — /clear just opens a fresh empty conversation. The reply.json
		// repoint (cicyWriteTerminalReply) lands the UI on the clean new conversation.
		emit(M{"type": "done"})
		return true
	case "/compact":
		compactCicyPane(ctx, session, shortID, workspace, emit)
		return true
	}
	return false
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

// isBusy reports whether a turn is currently in flight for this session.
func (s *cicySession) isBusy() bool {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	return s.busy
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

// lastOutcomeKindLocked returns the outcome kind of the trailing message
// ("error"/"cancelled"/"blocked") or "" if the last message isn't an outcome
// marker. Used to decide whether a failed turn is auto-retryable (only "error").
func (s *cicySession) lastOutcomeKindLocked() string {
	if len(s.messages) == 0 {
		return ""
	}
	return cicyMessageOutcomeKind(s.messages[len(s.messages)-1])
}

// dropTrailingFailedTurnLocked overwrites a trailing FAILED turn: when the last
// message is an "error" outcome marker, it removes that marker AND the user q that
// produced it (back to and including the last user message). A failed turn yields
// no real answer, so accumulating "q→失败 / q→失败 / …" only pollutes the window
// and compaction — the next q should REPLACE the failed attempt, not stack on it.
// Cancelled turns are left intact (the user explicitly stopped them and may 重试).
// Caller holds session.mu.
func (s *cicySession) dropTrailingFailedTurnLocked() {
	n := len(s.messages)
	if n == 0 || cicyMessageOutcomeKind(s.messages[n-1]) != "error" {
		return
	}
	cut := n - 1 // fallback: at least drop the marker
	for i := n - 1; i >= 0; i-- {
		if r, _ := s.messages[i]["role"].(string); r == "user" {
			cut = i
			break
		}
	}
	s.messages = s.messages[:cut]
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
		return false, "no turn to retry"
	}
	if !session.tryOwnTurn() {
		return false, "still generating, please wait"
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
	// 上一轮若是「生成失败」,新 q 直接覆盖它(丢掉失败的 q + 标记),失败不堆叠。
	session.dropTrailingFailedTurnLocked()
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

	// 新一轮开始:先把当前窗口(含刚追加的 q)落 current.json,再把 reply.json 重置成
	// working 空占位 —— 清掉上一轮残留(尤其被覆盖的失败轮 ⚠️error),让 web 立刻显示
	// thinking 而不是旧 error,新 reply 流进来再逐字填充 a。
	cicySeedCurrentSnapshot(shortID, session.convID, session.messages)
	cicyResetReplyForNewTurn(shortID, session.convID)

	maxRounds := cicyMaxRoundsFor(cfg)
	for round := 0; round < maxRounds; round++ {
		// Last allowed round → wrap up gracefully: run TOOL-FREE so the model has to
		// produce a final answer from what it has, instead of being hard-stopped
		// mid-task with an error.
		final := round == maxRounds-1
		// 用户已取消 → 立刻收尾:持久化已有内容,不再发下一轮网关请求。
		if ctx.Err() != nil {
			session.messages = append(session.messages, cicyOutcomeMessage("cancelled", "cancelled"))
			emit(M{"type": "error", "error": "cancelled"})
			session.persistLocked(workspace)
			cicyAttachOutcomeToSnapshot(shortID, "cancelled", "cancelled")
			// reply.json 收尾成终态——否则起手的 working 占位 + 半截答案会被前端当 live tail
			// 永久盖在上面,current.json 的「已停止」marker 显示不出来(与 blocked 路径同理)。
			cicyWriteTerminalReply(shortID, session.convID)
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
			"system":   cicySystemBlocks(cicyMaybeWrapUp(cfg.systemPrompt, final)),
			"messages": cicyInjectRoleContext(cicyRequestMessages(session.messages), cfg.roleContext),
		}
		// Pure-chat roles (assistant/support/sales) enable no tools — omit the
		// field entirely (an empty tools array is rejected by some upstreams). On
		// the final round we also omit tools so the model can only answer (wrap-up).
		if !final {
			if tools := cicyCachedToolDefs(cfg); len(tools) > 0 {
				payload["tools"] = tools
			}
		}
		// current.json (display) = the FULL conversation including the new q —
		// seeded by us, NOT by the audit layer (the wire body below may be a
		// post-compact slice and must not clobber the display history). Seed the
		// per-role system prompt + tool defs too so the inspector's 提示词/工具
		// panels match a CLI agent's.
		cicySeedCurrentSnapshotReq(shortID, session.convID, session.messages, cfg.systemPrompt, cicyCachedToolDefs(cfg))
		resp, streamed, err := cicyCallGateway(ctx, shortID, session.convID, "", payload, emit)
		if err != nil {
			// A mid-flight cancel surfaces here as a ctx error — record it as a
			// cancellation, not a failure. Anything else is a genuine gateway error
			// that already survived auto-retry, so it's terminal for this turn.
			kind, detail := "error", err.Error()
			if ctx.Err() != nil {
				kind, detail = "cancelled", "cancelled"
			}
			session.messages = append(session.messages, cicyOutcomeMessage(kind, detail))
			emit(M{"type": "error", "error": detail})
			session.persistLocked(workspace)
			cicyAttachOutcomeToSnapshot(shortID, kind, detail)
			// reply.json 收尾成终态,同上:让 UI 解锁并改渲染 current.json 的 outcome marker
			// (取消→已停止 / 错误→生成失败),而不是把半截答案当 live tail 永久挂着。
			cicyWriteTerminalReply(shortID, session.convID)
			return false
		}

		// 出站审计拦截(契约 A):网关已返 200 + 合成 SSE(已流式收尾,不卡 Thinking),并带
		// X-Cicy-Audit-Blocked 头。这里把它记成 blocked 终态:**不把拦截提示 commit 进历史**
		// (免污染上下文/下轮回发模型),只追加一条干净的「已拦截」标记;UI 据此渲染红色徽标、
		// 不给重试(重试只会再次命中)。
		if blockedID, _ := resp["_cicy_audit_blocked"].(string); blockedID != "" {
			rules, _ := resp["_cicy_audit_rules"].(string)
			// 具体原因优先级:① 403 JSON body.message(契约 B-plus)② 旧 200+SSE 的人类
			// 可读文案(resp content)③ rules+事件ID 兜底。这段只作为「已拦截」卡的展示
			// detail(挂在 current.json 的 marker 上,见 cicyAttachOutcomeToSnapshot),
			// 不进 session.messages/wire、不污染上下文。
			reason, _ := resp["_cicy_audit_message"].(string)
			reason = strings.TrimSpace(reason)
			if reason == "" {
				reason = strings.TrimSpace(cicyTextFromBlocks(resp["content"]))
			}
			if reason == "" {
				reason = "blocked by audit rule: " + rules
				if blockedID != "" {
					reason += " (event " + blockedID + ")"
				}
			}
			session.messages = append(session.messages, cicyOutcomeMessage("blocked", reason))
			emit(M{"type": "blocked", "rules": rules, "event_id": blockedID, "reason": reason})
			session.persistLocked(workspace)
			cicyAttachOutcomeToSnapshot(shortID, "blocked", reason)
			// reply.json 收尾成终态,否则起手的 working 占位会让 UI 永久 Thinking 不解锁。
			cicyWriteTerminalReply(shortID, session.convID)
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

		// Record the assistant turn verbatim — unless the round produced NOTHING
		// (degenerate empty response): an empty-content assistant message bakes a
		// permanent upstream 400 into the history.
		if len(blocks) > 0 {
			session.messages = append(session.messages, M{"role": "assistant", "content": blocks})
		}

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
				var result string
				if ctx.Err() != nil {
					// User cancelled mid-round: don't start further tools; still emit
					// a tool_result for each remaining tool_use so the history keeps
					// no orphan tool_use (which would 400 every later request).
					result = "error: cancelled by user"
				} else {
					result = cicyRunTool(ctx, shortID, name, input, cfg)
				}
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

	// Unreachable in normal flow: the final round runs tool-free, so it ends on a
	// text answer (returns true above). This is a defensive fallback only — persist
	// and return success so the partial work + last answer aren't dropped.
	emit(M{"type": "flush", "text": fmt.Sprintf("Tool-round limit (%d) reached — wrapping up.", maxRounds)})
	session.persistLocked(workspace)
	return true
}
