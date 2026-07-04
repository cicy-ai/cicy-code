package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/klauspost/compress/zstd"

	"ttyd-go/mgr/audit"
)

type aiGatewayToolCall struct {
	ToolID    string `json:"tool_id"`
	ToolName  string `json:"tool_name"`
	Arguments string `json:"arguments"`
}

// aiGatewayBuildTurnAuditUnit assembles the per-turn audit payload scanned by
// the audit pipeline (audit-v2): the outbound question, then each reply tool
// call's name + arguments. The builtin rules (secrets / PII / dangerous tool
// use) run over this combined text. Returns nil when there is nothing worth
// scanning (no question and no tool calls).
// aiGatewayBuildBehaviorToolCalls converts the normalised gateway tool calls
// into the audit package's ToolCall shape (provider + name + arguments) for the
// behaviour-layer scanner. Provider is carried so the scanner can resolve
// provider-specific tool naming (claude vs codex). Empty-named calls are dropped.
func aiGatewayBuildBehaviorToolCalls(provider string, toolCalls []aiGatewayToolCall) []audit.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]audit.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if strings.TrimSpace(tc.ToolName) == "" {
			continue
		}
		out = append(out, audit.ToolCall{
			Provider:  provider,
			ToolName:  tc.ToolName,
			Arguments: tc.Arguments,
		})
	}
	return out
}

func aiGatewayBuildTurnAuditUnit(question string, toolCalls []aiGatewayToolCall) []byte {
	q := strings.TrimSpace(question)
	if q == "" && len(toolCalls) == 0 {
		return nil
	}
	var b strings.Builder
	if q != "" {
		b.WriteString("【出站 q】\n")
		b.WriteString(q)
		b.WriteString("\n")
	}
	for _, tc := range toolCalls {
		b.WriteString("\n【tool_call】")
		b.WriteString(tc.ToolName)
		b.WriteString("\n")
		b.WriteString(tc.Arguments)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

type aiGatewayStatusItem struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Active bool   `json:"active"`
	Count  int    `json:"count"`
}

type aiGatewayStatusMap struct {
	Primary string                `json:"primary"`
	Items   []aiGatewayStatusItem `json:"items"`
}

type aiGatewayCodexTurnMetadata struct {
	SessionID           string `json:"session_id"`
	TurnID              string `json:"turn_id"`
	ThreadSource        string `json:"thread_source"`
	TurnStartedAtUnixMS int64  `json:"turn_started_at_unix_ms"`
}

type aiGatewayRequestSpan struct {
	TurnID          string                 `json:"turn_id"`
	ConversationID  string                 `json:"conversation_id"`
	RequestID       string                 `json:"request_id"`
	Provider        string                 `json:"provider"`
	Model           string                 `json:"model"`
	URL             string                 `json:"url"`
	Method          string                 `json:"method"`
	RequestHeaders  map[string][]string    `json:"request_headers"`
	RequestBody     interface{}            `json:"request_body"`
	Status          string                 `json:"status"`
	StatusCode      *int                   `json:"status_code"`
	StartedAt       string                 `json:"started_at"`
	UpdatedAt       string                 `json:"updated_at"`
	LatencyMS       *int                   `json:"latency_ms"`
	ThinkingPreview string                 `json:"thinking_preview"`
	AnswerPreview   string                 `json:"answer_preview"`
	Usage           map[string]interface{} `json:"usage"`
	InputTokens     int                    `json:"input_tokens"`
	OutputTokens    int                    `json:"output_tokens"`
	TotalTokens     int                    `json:"total_tokens"`
	CostCredit      float64                `json:"cost_credit"`
	ToolCallCount   int                    `json:"tool_call_count"`
	Auxiliary       bool                   `json:"auxiliary"`
}

type aiGatewayCurrentSnapshot struct {
	TurnID           string              `json:"turn_id"`
	AgentID          string              `json:"agent_id"`
	ConversationID   string              `json:"conversation_id"`
	RequestID        string              `json:"request_id"`
	Provider         string              `json:"provider"`
	Model            string              `json:"model"`
	URL              string              `json:"url"`
	Method           string              `json:"method"`
	Headers          map[string][]string `json:"headers"`
	Body             interface{}         `json:"body"`
	MaxHistoryID     int                 `json:"max_history_id,omitempty"`
	Timestamp        string              `json:"timestamp"`
	Status           string              `json:"status"`
	StartedAt        string              `json:"started_at"`
	UpdatedAt        string              `json:"updated_at"`
	RequestIDs       []string            `json:"request_ids"`
	ActiveRequestIDs []string            `json:"active_request_ids"`
	ConversationIDs  []string            `json:"conversation_ids"`
	// Prompts is the clean, de-noised list of REAL human questions in this
	// snapshot — extracted at write time from the (already history-id-annotated)
	// body, so each entry's ID matches the body's positional history id and the
	// list is always internally consistent with THIS snapshot (no cross-snapshot
	// id drift). The prompts-only history view reads this directly instead of
	// re-deriving questions in the frontend. See aiGatewayBuildCurrentPrompts.
	Prompts []aiGatewayUserPrompt `json:"prompts,omitempty"`
}

// aiGatewayUserPrompt is one real human question: its positional history id (so
// the answer = first assistant turn after it, read from the SAME snapshot), the
// time it first appeared, and the sanitized content.
type aiGatewayUserPrompt struct {
	ID      int    `json:"id"`
	TS      string `json:"ts"`
	Content string `json:"content"`
}

type aiGatewayReplySnapshot struct {
	TurnID                   string                   `json:"turn_id"`
	ConversationID           string                   `json:"conversation_id,omitempty"` // self-describing: which conversation this reply belongs to (history view rebinds on change)
	HistoryID                int64                    `json:"history_id,omitempty"`      // self-describing answer slot = current.json maxID + 1 (= q_last id + 1), pinned at write time so readers don't re-derive it
	Status                   string                   `json:"status"`
	StartedAt                string                   `json:"started_at"`
	UpdatedAt                string                   `json:"updated_at"`
	Thinking                 string                   `json:"thinking"`
	Answer                   string                   `json:"answer"`
	ToolCalls                []aiGatewayToolCall      `json:"tool_calls"`
	Items                    []map[string]interface{} `json:"items"` // accumulated content blocks across all API calls
	HTTPRequests             []aiGatewayRequestSpan   `json:"http_requests"`
	Usage                    map[string]interface{}   `json:"usage"`
	InputTokens              int                      `json:"input_tokens"`
	OutputTokens             int                      `json:"output_tokens"`
	CacheCreationInputTokens int                      `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int                      `json:"cache_read_input_tokens"`
	TotalTokens              int                      `json:"total_tokens"`
	CostCredit               float64                  `json:"cost_credit"`
	LastUsage                map[string]interface{}   `json:"last_usage,omitempty"`
	LastInputTokens          int                      `json:"last_input_tokens,omitempty"`
	LastOutputTokens         int                      `json:"last_output_tokens,omitempty"`
	LastTotalTokens          int                      `json:"last_total_tokens,omitempty"`
	LastCostCredit           float64                  `json:"last_cost_credit,omitempty"`
	Models                   []string                 `json:"models"`
	Model                    string                   `json:"model,omitempty"` // primary model, persisted in lite snapshot for round-trip
	RequestCount             int                      `json:"request_count"`
	StatusMap                aiGatewayStatusMap       `json:"status_map"`
	// LastStopReason is the terminal reason of the most recently finalized HTTP
	// round (Anthropic stop_reason / OpenAI finish_reason / Responses inferred).
	// Persisted so the inter-round gap — tool running client-side, no live HTTP —
	// keeps reading "working" instead of falsely flipping to "completed". Recomputed
	// each round; the round that ends on end_turn/stop clears it back to a done state.
	LastStopReason string `json:"last_stop_reason,omitempty"`
}

type aiGatewayMessageRecord struct {
	Q         string                     `json:"q,omitempty"`
	A         string                     `json:"a,omitempty"`
	QTime     string                     `json:"qTime,omitempty"`
	ATime     string                     `json:"aTime,omitempty"`
	Model     string                     `json:"model,omitempty"`
	Thinking  string                     `json:"thinking,omitempty"`
	ToolCalls []aiGatewayMessageToolCall `json:"tool_calls,omitempty"`
}

type aiGatewayMessageToolCall struct {
	Name   string `json:"name,omitempty"`
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
	Index  int    `json:"index,omitempty"`
}

type aiGatewayParsedResponse struct {
	Thinking  string
	Answer    string
	ToolCalls []aiGatewayToolCall
	Usage     map[string]interface{}
	// StopReason is the response's terminal reason (Anthropic stop_reason /
	// OpenAI finish_reason / Responses inferred). "tool_use"/"tool_calls"/
	// "function_call" mean the agent loop will continue with a tool round, so the
	// turn is NOT done even though this HTTP request just completed. Carried so
	// the inter-round gap (tool running client-side, no live HTTP) stays "working"
	// instead of falsely reading "completed".
	StopReason string
}

type aiGatewayStreamDelta struct {
	Kind      string
	Content   string
	ToolID    string
	ToolName  string
	ToolIndex *int
}

type aiGatewayAuditSession struct {
	mu              sync.Mutex
	finalized       bool
	agentID         string
	provider        string
	model           string
	turnID          string
	requestID       string
	conversationID  string
	question        string
	// requestBody is the RAW outbound request bytes (the full prompt the agent
	// sends to the model). Scanned at turn completion as the OUTBOUND audit
	// payload — this is the real interception point, same as the MITM adapter.
	requestBody     []byte
	// auditSourceChannel tags which path this session came from for audit
	// events: "" / "gateway" (default) for the cooperative AI gateway, "mitm"
	// when the mitm adapter created it. Drives Envelope.SourceChannel.
	auditSourceChannel string
	startedAt       time.Time
	// firstByteAt is stamped when the first upstream response byte arrives
	// (≈ reply start / time-to-first-token for streaming). Zero until then.
	firstByteAt     time.Time
	replyEventIndex int
	current         aiGatewayCurrentSnapshot
	reply           aiGatewayReplySnapshot
	replyHooks      []aiGatewayReplyHook
	lastStatusPush  string
	// auxiliary 标记：当前请求是 SUGGESTION MODE / 标题生成等辅助调用，
	// 不应该污染 current.json / reply.json，但仍然写 mirror（用于诊断）。
	auxiliary bool
	// auxKind 进一步区分辅助调用类型：""=主请求，"suggestion"=下一步预测，
	// "title"=会话标题生成（单独落到 title.json，见 completeFromResponse）。
	auxKind string
	// pendingItem 是流式过程中正在累积的 reply.Items 候选（thinking / text / tool_use）。
	// 当 stream_kind 或 tool 切换时，旧 pending 会被 flush 到 reply.Items 并立刻刷盘。
	// 这让前端 / IM 推送能在每个 item 完成时实时看到新内容（而不是等整次 HTTP 完成）。
	pendingItem *aiGatewayPendingItem
	// pushedItems 记录已经通过 reply hooks（IM 推送等）发出去的 reply.Items 数量。
	// 流式实时 flush（flushPendingItemLocked）每发一个 item 就把它推到这个水位；
	// completeFromResponse 在 HTTP 结束时把"还没推过"的 items（例如非流式响应、
	// SSE 解析失败而走 fallback 一次性补全的 items）补推一遍，确保 IM 一定收到回复。
	pushedItems int
	// comp* 是请求时算出的"缓存前缀指纹"，用于缓存命中诊断（见 agent_cache_diag.go）。
	compSystemHash   string
	compToolsHash    string
	compFirstMsgHash string
	compToolsCount   int
	compMsgCount     int
	compSystemEst    int
	compToolsEst     int
	compHistoryEst   int
}

// aiGatewayPendingItem 描述一个尚未 flush 到 reply.Items 的流中 item。
// 不区分 provider —— 通过 stream_kind / tool 切换识别 block 边界。
type aiGatewayPendingItem struct {
	Kind          string // "thinking" | "text" | "tool_use"
	Thinking      string // for thinking
	Text          string // for text
	ToolID        string // stream 累积 key（Codex 用 fc_xxx；Anthropic / OpenAI Chat 用 call_xxx）
	OutputToolID  string // reply.items 输出用的 tool_id（跟 current.json 中的 tool_use_id 对齐，Codex=call_xxx）
	ToolName      string // for tool_use
	Arguments     string // for tool_use (累积的 raw JSON 字符串)
}

type aiGatewayAuditReadCloser struct {
	inner      io.ReadCloser
	audit      *aiGatewayAuditSession
	statusCode int
	headers    http.Header
	buf        bytes.Buffer
	ssePending  string
	isStream    bool
	startMarked bool
	once        sync.Once
	// gzip-encoded SSE (api.anthropic.com via MITM honors the client's
	// Accept-Encoding): the bytes forwarded to the client must stay compressed,
	// so the live SSE parse runs on a side-channel streaming gunzip. gzW feeds
	// raw bytes to the gunzip goroutine; gzDone closes when it has flushed the
	// final events. In gzip mode ONLY that goroutine touches ssePending.
	gzW    *io.PipeWriter
	gzDone chan struct{}
}

type aiGatewayStreamAccumulator struct {
	thinkingParts []string
	answerParts   []string
	toolOrder     []string
	toolCalls     map[string]*aiGatewayToolCall
	usage         map[string]interface{}
	autoIndex     int
	// stopReason captures the stream's terminal reason (Anthropic message_delta
	// stop_reason / OpenAI finish_reason). Empty for Responses, where it's
	// inferred from trailing tool calls.
	stopReason string
	// contentBlocks accumulates raw content blocks in native format (Claude/OpenAI)
	contentBlocks []map[string]interface{}
}

var aiGatewayLiveReplySnapshots = struct {
	mu    sync.RWMutex
	items map[string]aiGatewayReplySnapshot
}{
	items: map[string]aiGatewayReplySnapshot{},
}

func aiGatewayStoreLiveReplySnapshot(agentID string, reply aiGatewayReplySnapshot) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	aiGatewayLiveReplySnapshots.mu.Lock()
	defer aiGatewayLiveReplySnapshots.mu.Unlock()
	aiGatewayLiveReplySnapshots.items[agentID] = reply
}

func aiGatewayDeleteLiveReplySnapshot(agentID string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	aiGatewayLiveReplySnapshots.mu.Lock()
	defer aiGatewayLiveReplySnapshots.mu.Unlock()
	delete(aiGatewayLiveReplySnapshots.items, agentID)
}

func aiGatewayGetLiveReplySnapshot(agentID string) (aiGatewayReplySnapshot, bool) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return aiGatewayReplySnapshot{}, false
	}
	aiGatewayLiveReplySnapshots.mu.RLock()
	defer aiGatewayLiveReplySnapshots.mu.RUnlock()
	reply, ok := aiGatewayLiveReplySnapshots.items[agentID]
	return reply, ok
}

func aiGatewayMergeUsage(base map[string]interface{}, extra map[string]interface{}) map[string]interface{} {
	if len(base) == 0 && len(extra) == 0 {
		return map[string]interface{}{}
	}
	out := aiGatewayCloneAnyMap(base)
	if out == nil {
		out = map[string]interface{}{}
	}
	for key, raw := range extra {
		// Preserve nested detail maps (e.g. input_tokens_details.cached_tokens
		// for OpenAI Responses) — they carry the cache-hit count and would
		// otherwise be dropped here, defeating aiGatewayNormalizeOpenAIUsage.
		if m := aiGatewayMap(raw); m != nil {
			out[key] = aiGatewayCloneAnyMap(m)
			continue
		}
		if key == "total_tokens" || key == "totalTokens" {
			if value := aiGatewayInt(raw); value > 0 {
				out[key] = value
			}
			continue
		}
		if value := aiGatewayInt(raw); value > 0 {
			current := aiGatewayInt(out[key])
			out[key] = current + value
		}
	}
	return out
}

var (
	systemReminderBlockRe      = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	environmentContextBlockRe  = regexp.MustCompile(`(?s)<environment_context>.*?</environment_context>`)
	openClawForwardedHeaderRe  = regexp.MustCompile("(?s)^Sender \\(untrusted metadata\\):\\s*```json\\s*.*?```\\s*")
	openClawLeadingTimestampRe = regexp.MustCompile(`^\[[^\]\n]+\]\s*`)
	// Harness/CLI scaffolding that rides in a role=user message but is NOT a
	// human prompt: slash-command wrappers (/compact, /clear, …) and the
	// auto-compaction continuation preamble. Stripped so the real-prompt list
	// (aiGatewayBuildCurrentPrompts) doesn't surface them as questions.
	localCommandBlockRe = regexp.MustCompile(`(?s)<local-command-caveat>.*?</local-command-caveat>|<command-name>.*?</command-name>|<command-message>.*?</command-message>|<command-args>.*?</command-args>|<local-command-stdout>.*?</local-command-stdout>|<local-command-stderr>.*?</local-command-stderr>`)
	compactionPreambleRe = regexp.MustCompile(`(?s)^\s*This session is being continued from a previous conversation.*`)
	// The /loop wakeup "recap" prompt is harness-injected as a role=user message
	// ("The user stepped away and is coming back. Recap in under N words…"), not a
	// human question — drop it from the prompts list.
	recapScaffoldRe = regexp.MustCompile(`(?is)^\s*The user (stepped away|is back|has returned).{0,80}?\bRecap\b`)
	// <task-notification>…</task-notification> is a background-task completion
	// notice injected as a role=user message, not a human prompt — strip the block.
	taskNotificationRe = regexp.MustCompile(`(?s)<task-notification>.*?</task-notification>`)
	// Exact harness markers that ride in a role=user message but are NOT human
	// prompts: the /compact "continue" resume line, and the deferred-tool load
	// echo. Matched on the FULLY-sanitized text (whole message = just the marker).
	harnessMarkerRe = regexp.MustCompile(`^(?:Continue from where you left off\.?|Tool loaded\.?)$`)
	// cicy-agent inter-agent notification (cicy-agent msg --notify / callback):
	// "🔔 [w-10131] msg adeba3ca → done". cicy injects it into the agent's tmux as
	// if typed, so even the transcript labels it promptSource=typed — pattern is the
	// only way to strip it. Stripped (not whole-message dropped) because it can be
	// spliced INTO a real prompt the user was typing ("清"+bell+"空" → "清空").
	cicyNotifyRe = regexp.MustCompile(`🔔\s*\[[^\]]*\]\s*msg\s+\S+\s*(?:→|->)\s*(?:done|idle|working|failed|completed|running|error|ok)\b`)
	// Claude Code prepends this marker to the user message when a tool call is
	// interrupted (ESC). It's not part of the human prompt — strip it; whatever the
	// user actually typed after it is kept.
	requestInterruptedRe = regexp.MustCompile(`(?m)^\s*\[Request interrupted by user[^\]]*\]\s*$`)
	// Skill invocation injects the skill's SKILL.md as a role=user message starting
	// with "Base directory for this skill:". That's harness scaffolding, not a
	// human question — drop the whole message.
	skillInjectionRe = regexp.MustCompile(`(?s)^\s*Base directory for this skill:`)
)

func newAIGatewayAuditSession(provider, agentID string, targetBase *url.URL, suffix string, method string, requestHeaders http.Header, requestBody []byte) *aiGatewayAuditSession {
	startedAt := time.Now().UTC()
	startedAtISO := startedAt.Format(time.RFC3339)
	prevReply := aiGatewayLoadReplySnapshot(agentID)
	prevInputTokens, prevOutputTokens, prevTotalTokens := aiGatewayReplyTotals(prevReply)
	prevCostCredit := aiGatewayReplyCostCredit(prevReply)
	// Carry the previous turn's cache split too. We already carry input/output
	// for display continuity during the (multi-second for large contexts)
	// prefill window before message_start arrives; carrying cache keeps the
	// hit-rate consistent instead of showing a misleading "huge input, 0% cache"
	// until the real numbers stream in and overwrite these.
	prevCacheRead := prevReply.CacheReadInputTokens
	prevCacheCreate := prevReply.CacheCreationInputTokens
	payloadValue := interface{}(M{})
	var payloadMap map[string]interface{}
	trimmedBody := bytes.TrimSpace(requestBody)
	if len(trimmedBody) > 0 {
		if err := json.Unmarshal(trimmedBody, &payloadValue); err != nil {
			payloadValue = string(trimmedBody)
		}
		if m, ok := payloadValue.(map[string]interface{}); ok {
			payloadMap = m
		}
	}
	model := aiGatewayString(payloadMap["model"])
	codexMeta := aiGatewayExtractCodexTurnMetadata(requestHeaders)
	conversationID := aiGatewayFirstNonEmpty(
		aiGatewayExtractSessionIDFromHeaders(requestHeaders, codexMeta),
		aiGatewayExtractSessionIDFromBody(payloadMap),
		aiGatewayExtractConversationID(payloadMap),
		aiGatewayShortID(),
	)
	requestID := aiGatewayFirstNonEmpty(aiGatewayString(payloadMap["request_id"]), aiGatewayString(payloadMap["id"]), aiGatewayShortID())
	question := aiGatewayExtractQuestion(payloadMap)
	turnID := aiGatewayFirstNonEmpty(codexMeta.TurnID, aiGatewayString(payloadMap["turn_id"]), aiGatewayShortID())
	targetURL := *targetBase
	targetURL.Path = resolveOpenClawProviderTargetPath(targetBase.Path, suffix)
	targetURL.RawPath = ""
	if strings.Contains(targetURL.String(), "api.deepseek.com") && model != "" {
		model = "deepseek-v4-pro"
	}

	requestSpan := aiGatewayRequestSpan{
		TurnID:          turnID,
		ConversationID:  conversationID,
		RequestID:       requestID,
		Provider:        provider,
		Model:           model,
		URL:             targetURL.String(),
		Method:          method,
		RequestHeaders:  map[string][]string{},
		RequestBody:     payloadValue,
		Status:          "streaming",
		StatusCode:      nil,
		StartedAt:       startedAtISO,
		UpdatedAt:       startedAtISO,
		LatencyMS:       nil,
		ThinkingPreview: "",
		AnswerPreview:   "",
		Usage:           map[string]interface{}{},
		InputTokens:     0,
		OutputTokens:    0,
		TotalTokens:     0,
		CostCredit:      0,
		ToolCallCount:   0,
		Auxiliary:       provider == "unknown",
	}
	annotatedBody := aiGatewayAnnotateCurrentBodyHistoryIDs(agentID, aiGatewayCloneJSONValue(payloadValue))
	// cicy main turns own their current.json: the runtime seeds the FULL
	// conversation right before the request, while the wire body may be a
	// post-compact slice. Keep the seeded display snapshot as s.current (status
	// and request ids still update) instead of the wire-derived body.
	if strings.TrimSpace(requestHeaders.Get("X-Cicy-Current-Owned")) == "1" {
		if disk := agentInspectorLoadCurrent(agentID); disk.ConversationID == conversationID && len(aiGatewayMap(disk.Body)) > 0 {
			annotatedBody = disk.Body
		}
	}
	current := aiGatewayCurrentSnapshot{
		TurnID:           turnID,
		AgentID:          agentID,
		ConversationID:   conversationID,
		RequestID:        requestID,
		Provider:         provider,
		Model:            model,
		URL:              targetURL.String(),
		Method:           method,
		Headers:          aiGatewayCloneHeader(requestHeaders),
		Body:             annotatedBody,
		MaxHistoryID:     aiGatewayCurrentBodyMaxHistoryID(annotatedBody),
		Timestamp:        startedAtISO,
		Status:           "thinking",
		StartedAt:        startedAtISO,
		UpdatedAt:        startedAtISO,
		RequestIDs:       []string{requestID},
		ActiveRequestIDs: []string{requestID},
		ConversationIDs:  []string{conversationID},
	}
	// reply.Items 是否继承前一次 reply：完全用语义判断（不用时间窗口）。
	// 只有当当前请求是 agent 内部的 tool 续传时才继承；用户主动发新 q 一律开新 turn 并清空 Items。
	isContinuation := aiGatewayIsToolContinuation(payloadMap)
	var prevItems []map[string]interface{}
	if prevReply.TurnID != "" && isContinuation {
		prevItems = prevReply.Items
		turnID = prevReply.TurnID
		// This continuation request carries the tool_result blocks for the
		// tool_use items we just inherited; fold the outputs back onto them so
		// reply.json shows each tool call's result.
		prevItems = aiGatewayInjectToolResultsIntoItems(prevItems, payloadMap)
	}
	if prevItems == nil {
		prevItems = []map[string]interface{}{}
	}
	// Token/cost totals accumulate WITHIN a turn (the tool loop re-calls the API
	// many times, each producing independent output). They must NOT bleed across
	// turns, or output/cost grow without bound over the whole conversation (this
	// is what made a single turn report ~562k output / $40+). Inherit them only
	// for tool-continuation requests; a fresh user turn starts its counters at
	// zero. Input/cache are still carried for display continuity during the
	// prefill window — they're a single-request snapshot that message_start
	// overwrites within seconds, so they can't accumulate.
	inheritOutput, inheritTotal, inheritCost := 0, prevInputTokens, 0.0
	if isContinuation {
		inheritOutput, inheritTotal, inheritCost = prevOutputTokens, prevTotalTokens, prevCostCredit
	}
	// Self-describing answer slot for reply.json: the answer occupies current.json's
	// maxID + 1 (= q_last's id + 1). Pinned here at request capture so readers can
	// take it straight off reply.json instead of re-deriving maxID+1. Re-computed each
	// round, so a tool continuation (current.json grew by tool_use+tool_result) tracks
	// correctly instead of freezing at the original q+1. 0 for an empty agent.
	answerSlot := int64(0)
	if current.MaxHistoryID > 0 {
		answerSlot = int64(current.MaxHistoryID) + 1
	}
	reply := aiGatewayReplySnapshot{
		TurnID:           turnID,
		ConversationID:   conversationID,
		HistoryID:        answerSlot,
		Status:           "thinking",
		StartedAt:        startedAtISO,
		UpdatedAt:        startedAtISO,
		Thinking:         "",
		Answer:           "",
		ToolCalls:        []aiGatewayToolCall{},
		Items:            prevItems,
		HTTPRequests:     []aiGatewayRequestSpan{requestSpan},
		Usage:            map[string]interface{}{},
		InputTokens:      prevInputTokens,
		OutputTokens:     inheritOutput,
		TotalTokens:      inheritTotal,
		CostCredit:       inheritCost,
		CacheReadInputTokens:     prevCacheRead,
		CacheCreationInputTokens: prevCacheCreate,
		LastUsage:        aiGatewayCloneAnyMap(prevReply.LastUsage),
		LastInputTokens:  prevReply.LastInputTokens,
		LastOutputTokens: prevReply.LastOutputTokens,
		LastTotalTokens:  prevReply.LastTotalTokens,
		LastCostCredit:   prevReply.LastCostCredit,
		Models:           aiGatewayOptionalStringList(model),
		RequestCount:     0,
		StatusMap: aiGatewayStatusMap{
			Primary: "thinking",
			Items: []aiGatewayStatusItem{
				{Kind: "thinking", Label: "Thinking 思考中", Active: true, Count: 1},
				{Kind: "tool_call", Label: "Working 工作中", Active: false, Count: 0},
				{Kind: "http", Label: "HTTP", Active: true, Count: 1},
				{Kind: "streaming", Label: "Working 工作中", Active: false, Count: 0},
			},
		},
	}
	// 识别辅助调用（SUGGESTION MODE / 标题生成 / compact 总结等）：这种请求不应污染主
	// reply.json / current.json。显式 X-Cicy-Aux 头优先（本机内部调用自标），其余靠启发式。
	auxKind := strings.TrimSpace(requestHeaders.Get("X-Cicy-Aux"))
	if auxKind == "" {
		auxKind = aiGatewayAuxiliaryKind(question, payloadMap)
	}
	// 缓存前缀指纹（用于缓存命中诊断）：在请求时对 system / tools / 首消息做轻量 hash。
	compMessages, _ := payloadMap["messages"].([]interface{})
	var firstMsg interface{}
	if len(compMessages) > 0 {
		firstMsg = compMessages[0]
	}
	return &aiGatewayAuditSession{
		agentID:        agentID,
		provider:       provider,
		model:          model,
		turnID:         turnID,
		requestID:      requestID,
		conversationID: conversationID,
		question:       question,
		requestBody:    append([]byte(nil), trimmedBody...),
		startedAt:      startedAt,
		current:        current,
		reply:          reply,
		// 继承自上一次续传的 items 已经被上一个 session 推送过，本 session 只推本次 HTTP
		// 新产生的 items（见 completeFromResponse 的 backstop）。
		pushedItems:    len(prevItems),
		replyHooks:     newReplyHooksForPane(agentID, isContinuation),
		auxiliary:      auxKind != "",
		auxKind:        auxKind,
		compSystemHash:   hashJSON(payloadMap["system"]),
		compToolsHash:    hashJSON(payloadMap["tools"]),
		compFirstMsgHash: hashJSON(firstMsg),
		compToolsCount:   aiGatewayLen(payloadMap["tools"]),
		compMsgCount:     len(compMessages),
		compSystemEst:    estJSONTokens(payloadMap["system"]),
		compToolsEst:     estJSONTokens(payloadMap["tools"]),
		compHistoryEst:   estJSONTokens(payloadMap["messages"]),
	}
}

func aiGatewayLen(v interface{}) int {
	if arr, ok := v.([]interface{}); ok {
		return len(arr)
	}
	return 0
}

func newAIGatewayAuditReadCloser(inner io.ReadCloser, audit *aiGatewayAuditSession, statusCode int, headers http.Header, contentLength int64) *aiGatewayAuditReadCloser {
	rc := &aiGatewayAuditReadCloser{
		inner:      inner,
		audit:      audit,
		statusCode: statusCode,
		headers:    headers,
		isStream:   strings.Contains(strings.ToLower(strings.TrimSpace(headers.Get("Content-Type"))), "text/event-stream"),
	}
	if contentLength > 0 && contentLength <= 8*1024*1024 {
		rc.buf.Grow(int(contentLength))
	}
	// gzip SSE → side-channel streaming gunzip for the live event parse.
	// (Found 2026-06-05: MITM-audited api.anthropic.com turns stream gzip, so
	// the inline parse saw compressed bytes — zero ai_chunk/thinking_chunk and
	// an empty live reply.json, while the at-rest parse still worked because
	// completeFromResponse gunzips the full buffer.)
	if rc.isStream && strings.EqualFold(strings.TrimSpace(headers.Get("Content-Encoding")), "gzip") {
		if audit != nil {
			log.Printf("[audit] gzip SSE side-channel armed agent=%s", audit.agentID)
		}
		pr, pw := io.Pipe()
		rc.gzW = pw
		rc.gzDone = make(chan struct{})
		go func() {
			defer close(rc.gzDone)
			gz, err := gzip.NewReader(pr)
			if err != nil {
				log.Printf("[audit] gzip side-channel reader init failed: %v", err)
				_, _ = io.Copy(io.Discard, pr)
				return
			}
			chunk := make([]byte, 32*1024)
			for {
				n, err := gz.Read(chunk)
				if n > 0 {
					rc.ssePending += string(chunk[:n])
					rc.flushSSEEvents(false)
				}
				if err != nil {
					if err != io.EOF {
						log.Printf("[audit] gzip side-channel mid-stream error agent=%s: %v", func() string {
							if audit != nil {
								return audit.agentID
							}
							return "?"
						}(), err)
					}
					rc.flushSSEEvents(true)
					_, _ = io.Copy(io.Discard, pr)
					return
				}
			}
		}()
	}
	return rc
}

// closeGzipSideChannel ends the streaming-gunzip side channel (if any) and
// waits briefly for it to flush its final events, so completion snapshots
// don't race ahead of the last deltas.
func (r *aiGatewayAuditReadCloser) closeGzipSideChannel() {
	if r.gzW == nil {
		return
	}
	_ = r.gzW.Close()
	select {
	case <-r.gzDone:
	case <-time.After(3 * time.Second):
	}
	r.gzW = nil
}

func (r *aiGatewayAuditReadCloser) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		if !r.startMarked {
			r.startMarked = true
			r.audit.markResponseStarted()
		}
		_, _ = r.buf.Write(p[:n])
		if r.isStream {
			if r.gzW != nil {
				// Compressed stream: hand raw bytes to the gunzip side channel;
				// it owns ssePending/flush in this mode.
				_, _ = r.gzW.Write(p[:n])
			} else {
				r.ssePending += string(p[:n])
				r.flushSSEEvents(err == io.EOF)
			}
		}
	}
	if err == io.EOF {
		if r.isStream {
			if r.gzW != nil {
				r.closeGzipSideChannel()
			} else {
				r.flushSSEEvents(true)
			}
		}
		r.finish(nil)
	}
	return n, err
}

func (r *aiGatewayAuditReadCloser) Close() error {
	err := r.inner.Close()
	r.closeGzipSideChannel()
	r.finish(err)
	return err
}

func (r *aiGatewayAuditReadCloser) finish(closeErr error) {
	r.once.Do(func() {
		if r.audit == nil {
			return
		}
		r.audit.completeFromResponse(r.statusCode, r.headers, append([]byte(nil), r.buf.Bytes()...), closeErr)
	})
}

func (r *aiGatewayAuditReadCloser) flushSSEEvents(flushAll bool) {
	for {
		idx := strings.IndexByte(r.ssePending, '\n')
		if idx < 0 {
			break
		}
		line := r.ssePending[:idx]
		r.ssePending = r.ssePending[idx+1:]
		r.handleSSELine(line)
	}
	if flushAll && strings.TrimSpace(r.ssePending) != "" {
		r.handleSSELine(r.ssePending)
		r.ssePending = ""
	}
}

func (r *aiGatewayAuditReadCloser) handleSSELine(line string) {
	if r.audit == nil {
		return
	}
	eventType, payload := aiGatewayParseSSELine(line)
	if eventType != "data" || payload == nil {
		return
	}
	r.audit.emitReplyStreamPayload(payload)
}

// markResponseStarted stamps the first-byte time once (≈ reply start). Safe to
// call repeatedly; only the first non-zero stamp sticks.
func (s *aiGatewayAuditSession) markResponseStarted() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.firstByteAt.IsZero() {
		s.firstByteAt = time.Now()
	}
	s.mu.Unlock()
}

func (s *aiGatewayAuditSession) recordOutboundRequest(req *http.Request) {
	if s == nil || req == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reply.HTTPRequests) == 0 {
		return
	}
	s.current.URL = req.URL.String()
	s.current.Method = req.Method
	s.current.Headers = aiGatewayCloneHeader(req.Header)
	s.reply.HTTPRequests[0].URL = req.URL.String()
	s.reply.HTTPRequests[0].Method = req.Method
	s.reply.HTTPRequests[0].RequestHeaders = aiGatewayCloneHeader(req.Header)
	_ = s.writeReplyEventLocked("request", M{
		"url":             s.current.URL,
		"method":          s.current.Method,
		"headers":         aiGatewayCloneHeader(req.Header),
		"body":            aiGatewayCloneJSONValue(s.current.Body),
		"request_id":      s.current.RequestID,
		"conversation_id": s.current.ConversationID,
	})
}

func (s *aiGatewayAuditSession) writeStartSnapshots() error {
	if s == nil {
		return nil
	}
	if s.auxiliary {
		// 辅助调用（SUGGESTION MODE 等）：不清空主 reply 目录，不覆盖 current / reply 主快照。
		return nil
	}
	s.mu.Lock()
	if err := s.resetReplyDirLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := aiGatewayWriteCurrentSnapshot(s.agentID, s.current); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := aiGatewayWriteReplySnapshot(s.agentID, s.reply); err != nil {
		s.mu.Unlock()
		return err
	}
	statusEvent := s.broadcastStatusLocked()
	currentUpdatedData := M{
		"agent_id":        s.agentID,
		"conversation_id": s.current.ConversationID,
		"turn_id":         s.current.TurnID,
		"updated_at":      s.current.UpdatedAt,
		"history_id":      int64(s.current.MaxHistoryID),
		"status":          s.current.Status,
		"answer":          s.reply.Answer,
		"thinking":        s.reply.Thinking,
		"question":        aiGatewaySanitizeUserQuestion(aiGatewayFirstNonEmpty(s.question, aiGatewayCurrentQuestion(s.current))),
	}
	currentUpdatedEvent := ChatEvent{Type: "current_updated", Data: currentUpdatedData}
	s.mu.Unlock()
	if statusEvent != nil {
		hub.publishAgent(s.agentID, *statusEvent)
	}
	hub.publishAgent(s.agentID, currentUpdatedEvent)
	return nil
}

func (s *aiGatewayAuditSession) completeFromError(err error) {
	if s == nil {
		return
	}
	s.completeFromResponse(502, http.Header{"Content-Type": []string{"application/json"}}, nil, err)
}

// aiGatewayMaybeGunzip returns the decompressed body when the peer sent
// Content-Encoding: gzip/zstd (detected by header OR magic bytes). Used so the
// audit parser sees plaintext; the original bytes still reach the client. Falls
// back to the input on any error so a bad/partial stream never loses the body.
//
// zstd matters for request bodies: codex (Rust/reqwest) zstd-compresses its
// request to chatgpt.com/backend-api/codex/responses, so without this the audit
// gate json.Unmarshal's compressed bytes, fails, and never opens a session
// (no current.json/reply.json). gzip covers the Node-CLI / upstream case.
func aiGatewayMaybeGunzip(headers http.Header, body []byte) []byte {
	if len(body) < 4 {
		return body
	}
	enc := strings.ToLower(headers.Get("Content-Encoding"))
	// zstd magic: 28 B5 2F FD
	if strings.Contains(enc, "zstd") || (body[0] == 0x28 && body[1] == 0xb5 && body[2] == 0x2f && body[3] == 0xfd) {
		dec, err := zstd.NewReader(nil)
		if err != nil {
			return body
		}
		defer dec.Close()
		out, err := dec.DecodeAll(body, nil)
		if err != nil || len(out) == 0 {
			return body
		}
		return out
	}
	isGzip := strings.Contains(enc, "gzip") || (body[0] == 0x1f && body[1] == 0x8b)
	if !isGzip {
		return body
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return body
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil || len(out) == 0 {
		return body
	}
	return out
}

// aiGatewayReplyItemsText flattens reply.Items content blocks of one kind
// ("text" / "thinking") into a single string. Each item is a whole block in
// Anthropic content-block shape ({type, text} / {type, thinking}), so the field
// key equals the kind. Used to surface reply text when a provider (codex /
// OpenAI Responses) only populated the structured items.
//
// `exclude` (trimmed text → true) drops blocks already shown elsewhere. codex
// agentic turns emit intermediate assistant text that gets committed mid-turn
// into current.json while reply.Items still holds that same block plus the
// continuation; passing the committed assistant texts here prevents the live
// tail from repeating a paragraph the committed list already renders.
func aiGatewayReplyItemsText(items []map[string]interface{}, kind string, exclude map[string]bool) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		if aiGatewayString(it["type"]) != kind {
			continue
		}
		v := strings.TrimSpace(aiGatewayString(it[kind]))
		if v == "" {
			continue
		}
		if exclude != nil && exclude[v] {
			continue
		}
		parts = append(parts, v)
	}
	return strings.Join(parts, "\n\n")
}

// aiGatewayDedupShadowToolCallItems collapses codex's tool-call double-record.
// codex (Responses) reports ONE call as two output items: the real one keyed by
// call id (call_…) with the true tool name, and a SHADOW keyed by item id
// (fc_… / ctc_…) carrying the same input but an EMPTY name (function_call, e.g.
// exec_command) or the literal "custom_tool_call" (custom tools like
// apply_patch). The shadow is dropped when a sibling tool_use has the same input
// AND a real name.
//
// Safety: only tool_use items whose name is empty or exactly "custom_tool_call"
// are ever removed, and only when a real-named sibling with the same input
// exists. Real tool names (apply_patch, exec_command, function names, …) are
// never touched, so gateway / function_call / anthropic paths are no-ops (they
// don't emit nameless shadows). Repeated real calls with identical input keep
// all their real-named records — only the nameless shadows are dropped.
func aiGatewayDedupShadowToolCallItems(items []map[string]interface{}) []map[string]interface{} {
	isShadowName := func(name string) bool { return name == "" || name == "custom_tool_call" }
	realInputs := map[string]bool{}
	for _, it := range items {
		if aiGatewayString(it["type"]) != "tool_use" {
			continue
		}
		if isShadowName(strings.TrimSpace(aiGatewayString(it["name"]))) {
			continue
		}
		if in := aiGatewayToolInputKey(it["input"]); in != "" {
			realInputs[in] = true
		}
	}
	if len(realInputs) == 0 {
		return items
	}
	out := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		if aiGatewayString(it["type"]) == "tool_use" && isShadowName(strings.TrimSpace(aiGatewayString(it["name"]))) {
			if in := aiGatewayToolInputKey(it["input"]); in != "" && realInputs[in] {
				continue // nameless/generic shadow of a real-named call → drop
			}
		}
		out = append(out, it)
	}
	return out
}

// aiGatewayToolInputKey produces a stable equality key for a tool_use input so
// codex's real/shadow double-records can be matched. It must NOT use
// aiGatewayFlattenPromptValue: that helper only pulls text/content/input/output/
// thinking, so a real exec_command input ({"cmd": "...", "workdir": "...", …})
// flattens to "" and the shadow is never matched (the bug it replaced). Canonical
// JSON is exact and deterministic — Go sorts map keys when marshaling, and any
// JSON-string argument is re-marshaled so a map and its stringified form collapse
// to the same key.
func aiGatewayToolInputKey(input interface{}) string {
	switch v := input.(type) {
	case nil:
		return ""
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return ""
		}
		var parsed interface{}
		if json.Unmarshal([]byte(trimmed), &parsed) == nil {
			if b, err := json.Marshal(parsed); err == nil {
				return string(b)
			}
		}
		return trimmed
	default:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return strings.TrimSpace(aiGatewayFlattenPromptValue(v))
	}
}

func (s *aiGatewayAuditSession) completeFromResponse(statusCode int, headers http.Header, responseBody []byte, responseErr error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.finalized {
		s.mu.Unlock()
		return
	}
	s.finalized = true

	updatedAt := time.Now().UTC().Format(time.RFC3339)
	// Upstreams that honor the client's Accept-Encoding return a gzip body. The
	// audit pipeline passes those bytes through to the client untouched, but here
	// we must parse PLAINTEXT — otherwise the gzip blob lands in reply.json's item
	// text and renders as mojibake. Decompress for parsing only.
	responseBody = aiGatewayMaybeGunzip(headers, responseBody)
	parsed := aiGatewayParseResponse(headers, responseBody)
	failed := responseErr != nil || statusCode >= 400
	status := "completed"
	if failed {
		status = "failed"
	}

	if len(s.reply.HTTPRequests) == 0 {
		s.reply.HTTPRequests = append(s.reply.HTTPRequests, aiGatewayRequestSpan{
			TurnID:         s.turnID,
			ConversationID: s.conversationID,
			RequestID:      s.requestID,
			Provider:       s.provider,
			Model:          s.model,
			Usage:          map[string]interface{}{},
		})
	}
	requestSpan := &s.reply.HTTPRequests[0]
	requestSpan.Status = status
	requestSpan.UpdatedAt = updatedAt
	requestSpan.StatusCode = aiGatewayIntPtr(statusCode)
	latencyMS := int(time.Since(s.startedAt).Milliseconds())
	requestSpan.LatencyMS = &latencyMS
	requestSpan.ThinkingPreview = aiGatewayPreviewText(parsed.Thinking, 180)
	requestSpan.AnswerPreview = aiGatewayPreviewText(parsed.Answer, 220)
	requestSpan.ToolCallCount = len(parsed.ToolCalls)
	requestSpan.Usage = aiGatewayCloneAnyMap(parsed.Usage)
	requestSpan.InputTokens, requestSpan.OutputTokens, requestSpan.TotalTokens = aiGatewayUsageTotals(parsed.Usage)
	requestSpan.CostCredit = aiGatewayEstimateCostCredit(s.model, parsed.Usage)

	s.current.UpdatedAt = updatedAt
	s.current.ActiveRequestIDs = []string{}
	s.reply.UpdatedAt = updatedAt
	s.reply.Thinking = parsed.Thinking
	s.reply.Answer = parsed.Answer
	s.reply.ToolCalls = parsed.ToolCalls
	// Stamp this round's terminal reason so the inter-round tool gap (no live HTTP)
	// keeps reading "working" — see aiGatewayBuildStatusMap. A round that ends on a
	// non-tool stop (end_turn/stop) clears it back to a terminal-eligible state.
	s.reply.LastStopReason = parsed.StopReason
	// reply.Items 由流式过程实时 flush（applyStreamEventsLocked → flushPendingItemLocked）。
	// HTTP 响应完成时只需 flush 残留的 pendingItem（最后一个 thinking/text/tool_use）。
	// 字段约定（统一用 Anthropic content block 风格）：
	//   thinking: {seq, type:"thinking", thinking}
	//   text:     {seq, type:"text", text}
	//   tool_use: {seq, type:"tool_use", id, name, input}
	itemsBeforeFlush := len(s.reply.Items)
	s.flushPendingItemLocked()
	// 兜底：若本次 HTTP 流过程中一个 item 都没 flush（比如 non-stream 响应、SSE 解析失败等），
	// 从 parsed 一次性补全，避免 reply.json 残缺。
	if len(s.reply.Items) == itemsBeforeFlush {
		if parsed.Thinking != "" {
			s.reply.Items = append(s.reply.Items, map[string]interface{}{
				"id":       len(s.reply.Items) + 1,
				"type":     "thinking",
				"thinking": parsed.Thinking,
			})
		}
		// 失败时 parsed.Answer 就是上游的错误响应体(如 429 的 JSON)。绝不能把它当作
		// 正文 text item 落盘——否则它会和下面失败分支追加的「⚠️ 生成失败（HTTP …）」格式化
		// 块一起渲染,UI 上同一个错误显示两遍(原始体 + 带抬头的格式化体)。错误的展示由
		// 失败分支独占;这里只在成功时把答案补成 item。
		if !failed && parsed.Answer != "" {
			s.reply.Items = append(s.reply.Items, map[string]interface{}{
				"id":   len(s.reply.Items) + 1,
				"type": "text",
				"text": parsed.Answer,
			})
		}
		for _, tc := range parsed.ToolCalls {
			if tc.ToolID == "" && tc.ToolName == "" {
				continue
			}
			var input interface{}
			if tc.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
					input = tc.Arguments
				}
			}
			s.reply.Items = append(s.reply.Items, map[string]interface{}{
				"id":      len(s.reply.Items) + 1,
				"type":    "tool_use",
				"tool_id": tc.ToolID,
				"name":    tc.ToolName,
				"input":   input,
			})
		}
	}
	// Collapse the codex apply_patch double-record. One custom_tool_call carries
	// BOTH an item id (ctc_…) and a call id (call_…): the input-stream events key
	// it by item id with a generic "custom_tool_call" name, the output-item events
	// key it by call id with the real name. When those land in different SSE flush
	// batches the live path emits TWO tool_use items for one call. Drop the
	// generic "custom_tool_call"-named copy when a sibling tool_use has the same
	// input — "custom_tool_call" is the item *type*, never a real tool name, so
	// gateway / function_call / anthropic items (apply_patch, exec_command, real
	// function names, …) are never matched.
	s.reply.Items = aiGatewayDedupShadowToolCallItems(s.reply.Items)
	// Keep the provider's raw usage object verbatim for auditability (the UI's
	// "raw usage" panel reads it). Don't let an empty parse wipe the usage we
	// already captured live from message_start during streaming.
	if len(parsed.Usage) > 0 {
		s.reply.Usage = aiGatewayCloneAnyMap(parsed.Usage)
	}
	// input_tokens: use latest (each request includes full context, so don't accumulate)
	// output_tokens: accumulate (each request produces independent output)
	// cache tokens: use latest (same as input_tokens)
	if requestSpan.InputTokens > 0 {
		s.reply.InputTokens = requestSpan.InputTokens
	}
	cacheCreate, cacheRead := aiGatewayCacheTokens(parsed.Usage)
	if cacheCreate > 0 || cacheRead > 0 {
		s.reply.CacheCreationInputTokens = cacheCreate
		s.reply.CacheReadInputTokens = cacheRead
	}
	s.reply.OutputTokens += requestSpan.OutputTokens
	s.reply.TotalTokens = s.reply.InputTokens + s.reply.OutputTokens
	s.reply.CostCredit += requestSpan.CostCredit
	if len(parsed.Usage) > 0 || requestSpan.InputTokens > 0 || requestSpan.OutputTokens > 0 || requestSpan.TotalTokens > 0 || requestSpan.CostCredit > 0 {
		s.reply.LastUsage = aiGatewayCloneAnyMap(parsed.Usage)
		s.reply.LastInputTokens = requestSpan.InputTokens
		s.reply.LastOutputTokens = requestSpan.OutputTokens
		s.reply.LastTotalTokens = requestSpan.TotalTokens
		s.reply.LastCostCredit = requestSpan.CostCredit
	}
	s.reply.RequestCount = len(aiGatewayFilterPrimaryRequests(s.reply.HTTPRequests))
	_ = s.writeReplyEventLocked("request_complete", aiGatewayCloneJSONValue(requestSpan))

	// Per-request usage log (backs the 分析 → 用量 UI tab). Skip auxiliary calls
	// (suggestion / title) so the table maps 1:1 to real conversation requests.
	if !s.auxiliary {
		totalTokens := requestSpan.TotalTokens
		if totalTokens == 0 {
			totalTokens = requestSpan.InputTokens + requestSpan.OutputTokens
		}
		usageStatus := "completed"
		if failed {
			usageStatus = "failed"
		}
		// reply 开始 = 首字节时刻；非流式/未打点时回退到总耗时。
		replyStartMS := latencyMS
		if !s.firstByteAt.IsZero() {
			replyStartMS = int(s.firstByteAt.Sub(s.startedAt).Milliseconds())
		}
		aiGatewayAppendUsageLog(s.agentID, agentUsageLogRecord{
			TS:                       updatedAt,
			ConversationID:           s.conversationID,
			TurnID:                   s.turnID,
			RequestID:                s.requestID,
			Provider:                 s.provider,
			Model:                    s.model,
			Status:                   usageStatus,
			StatusCode:               statusCode,
			ReplyStartMS:             replyStartMS,
			LatencyMS:                latencyMS,
			InputTokens:              requestSpan.InputTokens,
			OutputTokens:             requestSpan.OutputTokens,
			CacheReadInputTokens:     cacheRead,
			CacheCreationInputTokens: cacheCreate,
			TotalTokens:              totalTokens,
			CostCredit:               requestSpan.CostCredit,
		})
		// Cache-diagnosis fingerprint: request-time prefix hashes + this
		// request's actual cache outcome (see agent_cache_diag.go).
		hitRate := 0.0
		if requestSpan.InputTokens > 0 {
			hitRate = float64(cacheRead) / float64(requestSpan.InputTokens)
		}
		aiGatewayAppendComposition(s.agentID, agentCompositionRecord{
			TS:                   updatedAt,
			RequestID:            s.requestID,
			SystemHash:           s.compSystemHash,
			ToolsHash:            s.compToolsHash,
			FirstMsgHash:         s.compFirstMsgHash,
			ToolsCount:           s.compToolsCount,
			MsgCount:             s.compMsgCount,
			SystemEst:            s.compSystemEst,
			ToolsEst:             s.compToolsEst,
			HistoryEst:           s.compHistoryEst,
			InputTokens:          requestSpan.InputTokens,
			CacheReadInputTokens: cacheRead,
			HitRate:              hitRate,
		})
	}

	if failed {
		errorText := aiGatewayExtractErrorText(responseBody, responseErr)
		// Cap so a huge raw error body doesn't bloat reply.json / the UI.
		if r := []rune(errorText); len(r) > 1200 {
			errorText = strings.TrimSpace(string(r[:1200])) + "…"
		}
		if s.reply.Answer == "" {
			s.reply.Answer = errorText
			requestSpan.AnswerPreview = aiGatewayPreviewText(errorText, 220)
		}
		// Persist the failure DETAIL as a visible item (HTTP <statusCode> + message)
		// so the history view shows WHY it failed, not just a bare "failed" status.
		// Plain text (no cicy_outcome tag) → renders as normal markdown content.
		if errorText != "" {
			detail := fmt.Sprintf("⚠️ 生成失败（HTTP %d）\n\n%s", statusCode, errorText)
			s.reply.Items = append(s.reply.Items, map[string]interface{}{
				"id":   len(s.reply.Items) + 1,
				"type": "text",
				"text": detail,
			})
		}
		s.current.Status = "failed"
		s.reply.Status = "failed"
	} else {
		s.current.Status = ""
		s.reply.Status = ""
	}
	statusMap := aiGatewayBuildStatusMap(s.current, s.reply)
	s.current.Status = statusMap.Primary
	s.reply.Status = statusMap.Primary
	s.reply.StatusMap = statusMap

	if !s.auxiliary {
		if err := aiGatewayWriteReplySnapshot(s.agentID, s.reply); err != nil {
			log.Printf("[ai-gateway] write reply snapshot failed for %s: %v", s.agentID, err)
		}
	} else if s.auxKind == "title" {
		// 标题生成请求：不碰主 reply.json，单独落到 title.json。
		aiGatewayWriteTitleFile(s.agentID, s.turnID, s.model, status, parsed.Answer, updatedAt)
	}
	replySnapshot := s.reply
	replyHooks := s.replyHooks
	// 补推 backstop：流式实时 flush 走 flushPendingItemLocked → onItems，但非流式
	// 响应（或 SSE 解析失败而走 fallback 一次性补全 items 的 turn）不经过那条路径，
	// 这些 items 从没推给 reply hooks。这里把 pushedItems 水位之后的 items 补推一遍，
	// 保证 IM（TG / WeChat）一定能收到 agent 回复。clone 出来在解锁后再发，避免持锁做网络 I/O。
	var pendingHookItems []map[string]interface{}
	if !s.auxiliary && len(replyHooks) > 0 {
		if s.pushedItems < 0 {
			s.pushedItems = 0
		}
		if s.pushedItems > len(s.reply.Items) {
			s.pushedItems = len(s.reply.Items)
		}
		for _, it := range s.reply.Items[s.pushedItems:] {
			pendingHookItems = append(pendingHookItems, aiGatewayCloneAnyMap(it))
		}
		s.pushedItems = len(s.reply.Items)
	}
	statusEvent := s.broadcastStatusLocked()
	currentUpdatedData := M{
		"agent_id":        s.agentID,
		"conversation_id": s.current.ConversationID,
		"turn_id":         s.current.TurnID,
		"updated_at":      s.current.UpdatedAt,
		"history_id":      int64(s.current.MaxHistoryID),
		"status":          s.current.Status,
		"answer":          s.reply.Answer,
		"thinking":        s.reply.Thinking,
		"question":        aiGatewaySanitizeUserQuestion(aiGatewayFirstNonEmpty(s.question, aiGatewayCurrentQuestion(s.current))),
	}
	currentUpdatedEvent := ChatEvent{Type: "current_updated", Data: currentUpdatedData}
	log.Printf("[ai-gateway] complete agent=%s status=%s status_code=%d answer_len=%d thinking_len=%d tools=%d reply_hooks=%d",
		s.agentID, replySnapshot.Status, statusCode, len([]rune(replySnapshot.Answer)), len([]rune(replySnapshot.Thinking)), len(replySnapshot.ToolCalls), len(replyHooks))
	s.mu.Unlock()
	if statusEvent != nil {
		hub.publishAgent(s.agentID, *statusEvent)
	}
	hub.publishAgent(s.agentID, currentUpdatedEvent)
	notifyWorkerReplyFinished(s.agentID, replySnapshot.Status)
	// Audit at the interception point: scan the REAL traffic that passed through
	// the gateway this turn — the agent's full OUTBOUND request (what it sent to
	// the model: prompt, context, file contents it read, pasted secrets) and the
	// model's full INBOUND response. This is the ONLY audit trigger for the
	// gateway path, mirroring the MITM adapter; outbound uses the incremental
	// payload so accumulated history is never re-scanned (no flood). Runs even on
	// failed turns (e.g. 402) — a leak is a leak regardless of the LLM result.
	// Aux/internal calls (X-Cicy-Aux: suggestion/title) carry no real turn.
	if !s.auxiliary {
		// Diagnostic: record this round's FULL outbound request body into
		// outbound.json (accumulated across the conversation's rounds), so we can
		// inspect exactly what is sent to the model each round. Raw body, unfiltered.
		aiGatewayAppendOutbound(s.agentID, s.conversationID, s.turnID, s.current.RequestID, s.requestBody, time.Now())
		// 出站审 agent 发给 LLM 的全部新内容 —— q + tool_use + tool_result,一个都不丢。
		// 出站拦截点在「转发给 LLM 之前」,所以无论是哪种,数据此刻都还没到模型 =
		// 未发生的 exfil,都可拦:
		//   - q           用户粘进 prompt 的密钥(少见)
		//   - tool_use    args 里 `curl -H "auth: TOKEN"` 把 token 当参数外发
		//   - tool_result agent read 到的敏感文件内容,正发给 LLM
		// 只靠 IncrementalOutboundPayload 去重(每个 message block 按内容哈希扫一次,
		// 不重扫历史 → 不 flood);不再按内容类型过滤(旧 q-only 把真正的泄漏面丢了)。
		if inc := audit.IncrementalOutboundPayload(s.agentID, s.requestBody); len(inc) > 0 {
			audit.SubmitGatewayOutbound(s.agentID, "", "", "", s.turnID, s.conversationID, s.provider, s.model, inc)
		}
		// inbound: scan the model's ASSEMBLED reply (answer + thinking + tool
		// calls), NOT the raw SSE stream — a secret in a streamed answer is split
		// across `data:` deltas in the raw bytes and would never match contiguously.
		var rb strings.Builder
		rb.WriteString(replySnapshot.Answer)
		if replySnapshot.Thinking != "" {
			rb.WriteByte('\n')
			rb.WriteString(replySnapshot.Thinking)
		}
		for _, tc := range replySnapshot.ToolCalls {
			rb.WriteByte('\n')
			rb.WriteString(tc.ToolName)
			rb.WriteByte(' ')
			rb.WriteString(tc.Arguments)
		}
		if rb.Len() > 0 {
			audit.SubmitGatewayInbound(s.agentID, "", "", "", s.turnID, s.conversationID, s.provider, s.model, []byte(rb.String()))
		}
	}
	// 先补推没推过的 items（见上面 backstop 注释），再 finalize 收尾。
	if len(pendingHookItems) > 0 {
		log.Printf("[im] reply backstop push agent=%s items=%d (live flush missed)", s.agentID, len(pendingHookItems))
		for _, h := range replyHooks {
			h.onItems(pendingHookItems)
		}
	}
	for _, h := range replyHooks {
		h.finalize(replySnapshot)
	}
	// 调试/数据收集：当 CICY_GATEWAY_REPLY_MIRROR=1 时把本次完整的请求+响应+解析结果
	// 镜像写到 reply_mirror/ 目录，供后续分析。完全不影响主路径。
	aiGatewayWriteReplyMirror(s, statusCode, headers, responseBody, parsed, replySnapshot)
}

// aiGatewayHistoryDir resolves where an agent's conversation store lives:
// <workspace>/.cicy/history — the agent's CONFIGURED workspace when set (it can
// differ from workers/<id>: w-1001 works out of workers/w-10001), falling back
// to the id-derived default. History previously ignored the configured
// workspace; when the two differ, the old id-derived dir is moved wholesale
// (one-time rename; the snapshot symlinks inside are relative and survive).
func aiGatewayHistoryDir(agentID string) string {
	base := builtinWorkerWorkspace(agentID)
	if store != nil {
		if ws := strings.TrimSpace(paneWorkspace(agentID)); ws != "" {
			base = ws
		}
	}
	newDir := filepath.Join(workspaceRuntimeDir(base), "history")
	if idDir := filepath.Join(builtinWorkerRuntimeDir(agentID), "history"); idDir != newDir {
		if _, err := os.Stat(newDir); os.IsNotExist(err) {
			if _, err2 := os.Stat(idDir); err2 == nil {
				_ = os.MkdirAll(filepath.Dir(newDir), 0755)
				_ = os.Rename(idDir, newDir)
			}
		}
	}
	legacyDirs := []string{
		filepath.Join(builtinWorkerWorkspace(agentID), ".history"),
		filepath.Join(builtinWorkerWorkspace(agentID), "history"),
	}
	for _, oldDir := range legacyDirs {
		if oldDir == newDir {
			continue
		}
		if _, err := os.Stat(oldDir); err != nil {
			continue
		}
		if err := os.MkdirAll(newDir, 0755); err != nil {
			break
		}
		for _, name := range []string{"current.json", "system_prompt.txt"} {
			oldPath := filepath.Join(oldDir, name)
			newPath := filepath.Join(newDir, name)
			if _, err := os.Stat(oldPath); err != nil {
				continue
			}
			if _, err := os.Stat(newPath); err == nil {
				continue
			}
			if body, err := os.ReadFile(oldPath); err == nil {
				_ = os.WriteFile(newPath, body, 0644)
			}
		}
	}
	return newDir
}

func aiGatewayWriteJSONAtomic(path string, value interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmpPath := fmt.Sprintf("%s.tmp.%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, body, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func aiGatewayCurrentSnapshotPath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "current.json")
}

func aiGatewayReplySnapshotPath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "reply.json")
}

// sanitizeConvID makes a conversation id safe as a directory name; empty → "_nil".
func sanitizeConvID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "_nil"
	}
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, id)
}

// aiGatewayConversationDir is the per-conversation snapshot dir:
//   .cicy/history/chat/<conversation_id>/
func aiGatewayConversationDir(agentID, convID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "chat", sanitizeConvID(convID))
}

// ensureRelSymlink makes linkPath a symlink to relTarget (relative to linkPath's
// dir), replacing whatever is there atomically. Returns an error if the platform
// can't create symlinks, so the caller can fall back to a plain file write.
func ensureRelSymlink(linkPath, relTarget string) error {
	if cur, err := os.Readlink(linkPath); err == nil && cur == relTarget {
		return nil
	}
	tmp := fmt.Sprintf("%s.lnktmp.%d", linkPath, time.Now().UnixNano())
	_ = os.Remove(tmp)
	if err := os.Symlink(relTarget, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, linkPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// aiGatewayWriteSnapshotFile writes a per-conversation snapshot under
// chat/<conv>/<name> and repoints the canonical history/<name> symlink to it, so
// every (unchanged) reader of the canonical path transparently sees the active
// conversation. Conversations no longer overwrite each other. On platforms
// without symlink support it falls back to writing the canonical path directly.
func aiGatewayWriteSnapshotFile(agentID, name, convID string, value interface{}) error {
	canonical := filepath.Join(aiGatewayHistoryDir(agentID), name)
	// One-time migration: if the canonical path is still a real file (pre-split
	// layout), relocate it into its own conversation dir so its snapshot isn't
	// lost when we replace it with a symlink. Cheap no-op once it's a symlink.
	if fi, err := os.Lstat(canonical); err == nil && fi.Mode().IsRegular() {
		if data, e := os.ReadFile(canonical); e == nil {
			var probe struct {
				ConversationID string `json:"conversation_id"`
			}
			_ = json.Unmarshal(data, &probe)
			legacy := filepath.Join(aiGatewayConversationDir(agentID, probe.ConversationID), name)
			if _, e2 := os.Stat(legacy); os.IsNotExist(e2) {
				_ = os.MkdirAll(filepath.Dir(legacy), 0755)
				_ = os.WriteFile(legacy, data, 0644)
			}
		}
	}
	realPath := filepath.Join(aiGatewayConversationDir(agentID, convID), name)
	if err := aiGatewayWriteJSONAtomic(realPath, value); err != nil {
		return err
	}
	relTarget := filepath.Join("chat", sanitizeConvID(convID), name)
	if err := ensureRelSymlink(canonical, relTarget); err != nil {
		// Symlink unsupported (e.g. Windows without privilege): keep every reader
		// working by writing the canonical path directly (legacy behavior).
		return aiGatewayWriteJSONAtomic(canonical, value)
	}
	return nil
}

func aiGatewayWriteCurrentSnapshot(agentID string, current aiGatewayCurrentSnapshot) error {
	current.Body = aiGatewayAnnotateCurrentBodyHistoryIDs(agentID, aiGatewayCloneJSONValue(current.Body))
	current.MaxHistoryID = aiGatewayCurrentBodyMaxHistoryID(current.Body)
	current.Prompts = aiGatewayBuildCurrentPrompts(agentID, current.ConversationID, current.Body, current.Timestamp)
	return aiGatewayWriteSnapshotFile(agentID, "current.json", current.ConversationID, current)
}

// aiGatewayBuildCurrentPrompts extracts the clean list of REAL human questions
// from an already-annotated current.json body. It walks the full ordered
// message array (position = history id) and keeps only role=user messages that
// carry genuine typed text — dropping tool_result-only turns, system/harness
// scaffolding (system-reminder / environment_context / slash-command echoes /
// compaction preamble), and the /loop recap wakeup. Duplicate text blocks within
// one message and consecutive identical prompts are collapsed.
//
// Each prompt's TS is preserved across writes by CONTENT (not id — ids drift on
// compaction): a prompt seen in the prior snapshot keeps its first-seen time, a
// newly-appearing one is stamped with this snapshot's timestamp. This is the
// per-turn write path (writeStartSnapshots), so the one prior-file read is cheap.
// aiGatewayNormPrompt strips ALL whitespace so transcript text and the
// (sanitized) current.json text compare cleanly across wrapping/formatting diffs.
func aiGatewayNormPrompt(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// aiGatewayTranscriptTypedRow is a minimal projection of a transcript JSONL row.
// Only the three fields the typed-set needs are decoded; Message.Content stays a
// RawMessage (no deep parse) and is only decoded for the few rows that actually
// qualify. This replaces the old map[string]interface{} decode of EVERY line —
// which dominated backend allocation (objectInterface/unquote, ~47% of total).
type aiGatewayTranscriptTypedRow struct {
	Type         string `json:"type"`
	PromptSource string `json:"promptSource"`
	Message      struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// aiGatewayTypedSetEntry caches, per conversation id, the typed-prompt set built
// so far plus the byte offset consumed. lastOffset marks the end of the last
// NEWLINE-terminated line processed, so a trailing partial write (a turn mid-
// flush) is re-read next time rather than skipped.
type aiGatewayTypedSetEntry struct {
	path       string
	lastOffset int64
	set        map[string]bool
}

var (
	aiGatewayTypedSetMu    sync.Mutex
	aiGatewayTypedSetCache = map[string]*aiGatewayTypedSetEntry{}
)

// aiGatewayTranscriptTypedSet reads the Claude Code session transcript for a
// conversation and returns the set of NORMALIZED prompts the human actually
// entered — every JSONL row with type=user and promptSource ∈ {typed, queued}.
// Claude Code labels these itself, so this is the authoritative "real prompt"
// signal — no role=user noise (tool results, task-notifications, /compact
// continuations, recap wakeups, inter-agent bells) ever carries that label.
// Returns nil when there is no transcript (non-Claude backends) → caller falls
// back to the regex-filtered current.json path.
//
// The transcript is append-only and grows to MB over a long session, and this
// runs once PER TURN — a full re-parse each time is O(N) per turn = O(N²) total
// (pprof: 73-84% of backend allocation). So results are cached per cid with an
// incremental byte offset: each call stat()s the file and, when the path is
// unchanged and it only grew, seeks to lastOffset and scans ONLY the new lines,
// merging into the cached set. A shrink / missing file / path change (e.g.
// /clear mints a new cid → new file → cache miss) forces a full rebuild. /compact
// only appends, so the cache stays valid across it.
func aiGatewayTranscriptTypedSet(conversationID string) map[string]bool {
	cid := strings.TrimSpace(conversationID)
	if cid == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", cid+".jsonl"))
	if len(matches) == 0 {
		return nil
	}
	path := matches[0]
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	size := fi.Size()

	aiGatewayTypedSetMu.Lock()
	defer aiGatewayTypedSetMu.Unlock()

	entry := aiGatewayTypedSetCache[cid]
	// Incremental only when the same file grew (or is unchanged); anything else
	// (first sight, path changed, file shrank/rotated) → full rebuild from 0.
	if entry == nil || entry.path != path || size < entry.lastOffset {
		entry = &aiGatewayTypedSetEntry{path: path, set: map[string]bool{}}
		aiGatewayTypedSetCache[cid] = entry
	}
	if size == entry.lastOffset {
		return aiGatewayTypedSetResult(entry.set) // nothing new appended
	}

	f, err := os.Open(path)
	if err != nil {
		return aiGatewayTypedSetResult(entry.set)
	}
	defer f.Close()
	if entry.lastOffset > 0 {
		if _, err := f.Seek(entry.lastOffset, io.SeekStart); err != nil {
			// Seek failed → rebuild from the top rather than risk a gap.
			entry.set = map[string]bool{}
			entry.lastOffset = 0
			_, _ = f.Seek(0, io.SeekStart)
		}
	}

	r := bufio.NewReaderSize(f, 1024*1024)
	consumed := entry.lastOffset
	for {
		line, readErr := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			consumed += int64(len(line)) // advance only past complete lines
			var row aiGatewayTranscriptTypedRow
			if json.Unmarshal(bytes.TrimRight(line, "\n"), &row) == nil &&
				row.Type == "user" &&
				(row.PromptSource == "typed" || row.PromptSource == "queued") {
				var content interface{}
				if len(row.Message.Content) > 0 {
					_ = json.Unmarshal(row.Message.Content, &content)
				}
				norm := aiGatewayNormPrompt(aiGatewaySanitizeUserQuestion(aiGatewayPromptTextFromUserMessage(content)))
				if norm != "" {
					entry.set[norm] = true
				}
			}
		}
		if readErr != nil {
			break // io.EOF (or real error); a trailing partial line is left for next time
		}
	}
	entry.lastOffset = consumed
	return aiGatewayTypedSetResult(entry.set)
}

// aiGatewayTypedSetResult returns a COPY of the cached set (or nil when empty),
// so a caller reading it can never observe a later turn merging into the live
// cache map. The copy is O(unique prompts) — tiny next to the MB re-parse it
// replaces.
func aiGatewayTypedSetResult(set map[string]bool) map[string]bool {
	if len(set) == 0 {
		return nil
	}
	out := make(map[string]bool, len(set))
	for k := range set {
		out[k] = true
	}
	return out
}

// aiGatewayMatchesTyped reports whether a sanitized current.json prompt matches
// one the human actually typed (per the transcript set). Exact normalized match,
// or containment either way when the shorter side is ≥6 runes (covers a prompt
// the gateway merged with trailing harness text, or minor sanitize diffs) —
// the length guard stops a 1-char "清" from matching an injected bell line.
func aiGatewayMatchesTyped(set map[string]bool, clean string) bool {
	nc := aiGatewayNormPrompt(clean)
	if nc == "" {
		return false
	}
	if set[nc] {
		return true
	}
	ncLen := len([]rune(nc))
	for t := range set {
		tLen := len([]rune(t))
		minLen := ncLen
		if tLen < minLen {
			minLen = tLen
		}
		if minLen >= 6 && (strings.Contains(nc, t) || strings.Contains(t, nc)) {
			return true
		}
	}
	return false
}

func aiGatewayBuildCurrentPrompts(agentID string, conversationID string, body interface{}, ts string) []aiGatewayUserPrompt {
	mapped := aiGatewayMap(body)
	if len(mapped) == 0 {
		return nil
	}
	// Authoritative allowlist from the Claude Code transcript (nil → not a Claude
	// session; fall back to the regex noise filters below).
	typedSet := aiGatewayTranscriptTypedSet(conversationID)
	items := aiGatewaySlice(mapped["messages"])
	if len(items) == 0 {
		items = aiGatewaySlice(mapped["input"])
	}
	if len(items) == 0 {
		return nil
	}
	stamp := strings.TrimSpace(ts)
	if stamp == "" {
		stamp = time.Now().UTC().Format(time.RFC3339)
	}
	// content → first-seen ts, from the prior snapshot (drift-proof by content).
	priorTS := map[string]string{}
	if prev, err := aiGatewayReadCurrentSnapshot(agentID); err == nil {
		for _, p := range prev.Prompts {
			if p.Content != "" && p.TS != "" {
				priorTS[p.Content] = p.TS
			}
		}
	}
	out := []aiGatewayUserPrompt{}
	lastContent := ""
	for _, raw := range items {
		m := aiGatewayMap(raw)
		if len(m) == 0 {
			continue
		}
		if strings.ToLower(strings.TrimSpace(aiGatewayString(m["role"]))) != "user" {
			continue
		}
		text := aiGatewayPromptTextFromUserMessage(m["content"])
		if strings.TrimSpace(text) == "" {
			continue // tool_result-only turn, or no human text
		}
		clean := aiGatewaySanitizeUserQuestion(text)
		if clean == "" {
			continue
		}
		// Drop obvious harness / skill-injection / recap scaffolding FIRST —
		// unconditionally, even when a transcript exists. These blocks are never
		// human prompts, but a skill-injection block echoes the ARGUMENTS that
		// triggered it (a real typed command), so the typedSet containment match
		// below would otherwise misclassify the whole block as a prompt.
		if recapScaffoldRe.MatchString(clean) || skillInjectionRe.MatchString(clean) || harnessMarkerRe.MatchString(clean) {
			continue
		}
		if typedSet != nil {
			// Authoritative: keep ONLY what the human actually typed/queued.
			if !aiGatewayMatchesTyped(typedSet, clean) {
				continue
			}
		}
		if clean == lastContent {
			continue // consecutive duplicate
		}
		when := stamp
		if prev, ok := priorTS[clean]; ok {
			when = prev
		}
		out = append(out, aiGatewayUserPrompt{ID: aiGatewayInt(m["id"]), TS: when, Content: clean})
		lastContent = clean
	}
	return out
}

// aiGatewayPromptTextFromUserMessage pulls the human-typed text out of a
// role=user message's content, ignoring tool_result blocks (returns "" when the
// message is tool_result-only). Adjacent identical text blocks are de-duplicated
// — some clients emit the same text twice in separate blocks, which would
// otherwise render the question doubled.
func aiGatewayPromptTextFromUserMessage(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		parts := []string{}
		lastKept := ""
		hasText := false
		for _, raw := range c {
			blk := aiGatewayMap(raw)
			if len(blk) == 0 {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(aiGatewayString(blk["type"]))) {
			case "text", "":
				hasText = true
				t := aiGatewayString(blk["text"])
				if strings.TrimSpace(t) == "" || strings.TrimSpace(t) == strings.TrimSpace(lastKept) {
					continue
				}
				parts = append(parts, t)
				lastKept = t
			}
		}
		if !hasText {
			return ""
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// aiGatewayReplySnapshotLite is a simplified version for reply.json
// Contains native content blocks format (like body.messages in current.json)
type aiGatewayReplySnapshotLite struct {
	TurnID                   string                   `json:"turn_id"`
	ConversationID           string                   `json:"conversation_id,omitempty"`
	HistoryID                int64                    `json:"history_id,omitempty"` // answer slot = current.json maxID + 1 (= q_last id + 1)
	Status                   string                   `json:"status"`
	StartedAt                string                   `json:"started_at"`
	UpdatedAt                string                   `json:"updated_at"`
	Items                    []map[string]interface{} `json:"items"`
	Model                    string                   `json:"model,omitempty"`
	InputTokens              int                      `json:"input_tokens"`
	OutputTokens             int                      `json:"output_tokens"`
	CacheCreationInputTokens int                      `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int                      `json:"cache_read_input_tokens"`
	TotalTokens              int                      `json:"total_tokens"`
	CostCredit               float64                  `json:"cost_credit"`
	LastStopReason           string                   `json:"last_stop_reason,omitempty"` // see aiGatewayReplySnapshot.LastStopReason — keeps the tool-run gap "working"
}

// aiGatewaySweepStaleReplies finalizes reply.json snapshots left in a
// non-terminal status (working/streaming/thinking/…) by a previous server
// process. A stuck snapshot keeps `complete=false` on
// /api/agents/current-reply forever — the TeamPanel shows that agent
// permanently busy (yellow) and its live tail never settles. Called once at
// boot, synchronously BEFORE the HTTP listener starts: only this daemon ever
// writes reply.json, so at that point no live turn can be racing the sweep.
func aiGatewaySweepStaleReplies() {
	if store == nil {
		return
	}
	rows, err := store.Query("SELECT pane_id FROM agent_config")
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var paneID string
		if rows.Scan(&paneID) == nil {
			if id := shortPaneID(strings.TrimSpace(paneID)); id != "" {
				ids = append(ids, id)
			}
		}
	}
	rows.Close()
	for _, id := range ids {
		reply := agentInspectorLoadReply(id)
		status := strings.ToLower(strings.TrimSpace(reply.Status))
		if status == "" || status == "idle" || status == "done" || isAIGatewayReplyTerminal(status) {
			continue
		}
		reply.Status = "failed"
		reply.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := aiGatewayWriteReplySnapshot(id, reply); err != nil {
			log.Printf("[ai-gateway] stale reply sweep agent=%s write failed: %v", id, err)
			continue
		}
		log.Printf("[ai-gateway] stale reply swept agent=%s turn=%s %s→failed", id, reply.TurnID, status)
	}
}

func aiGatewayWriteReplySnapshot(agentID string, reply aiGatewayReplySnapshot) error {
	aiGatewayStoreLiveReplySnapshot(agentID, reply)
	lite := aiGatewayReplySnapshotLite{
		TurnID:                   reply.TurnID,
		ConversationID:           reply.ConversationID,
		HistoryID:                reply.HistoryID,
		Status:                   reply.Status,
		StartedAt:                reply.StartedAt,
		UpdatedAt:                reply.UpdatedAt,
		Items:                    reply.Items,
		Model:                    aiGatewayReplyPrimaryModel(reply),
		InputTokens:              reply.InputTokens,
		OutputTokens:             reply.OutputTokens,
		CacheCreationInputTokens: reply.CacheCreationInputTokens,
		CacheReadInputTokens:     reply.CacheReadInputTokens,
		TotalTokens:              reply.TotalTokens,
		CostCredit:               reply.CostCredit,
		LastStopReason:           reply.LastStopReason,
	}
	err := aiGatewayWriteSnapshotFile(agentID, "reply.json", reply.ConversationID, lite)
	// MEMORY LEAK FIX: the in-memory live-snapshot cache only needs replies that
	// are still STREAMING. Once a reply is terminal, the on-disk reply.json is the
	// source of truth (aiGatewayLoadReplySnapshot reads the file first), so drop
	// the in-memory copy. Without this, aiGatewayDeleteLiveReplySnapshot was never
	// called anywhere, so every agent/fork ever seen kept its full parsed reply
	// (Items []map[string]interface{}) resident forever — an unbounded heap leak
	// (observed ~700MB/hr under load). Only delete after a successful write so a
	// write failure keeps the in-memory copy as a fallback.
	if err == nil && isAIGatewayReplyTerminal(strings.ToLower(strings.TrimSpace(reply.Status))) {
		aiGatewayDeleteLiveReplySnapshot(agentID)
	}
	return err
}

func aiGatewayReadReplySnapshotFile(agentID string) (aiGatewayReplySnapshot, error) {
	reply := aiGatewayReplySnapshot{}
	body, err := os.ReadFile(aiGatewayReplySnapshotPath(agentID))
	if err != nil {
		return reply, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return reply, nil
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return aiGatewayReplySnapshot{}, err
	}
	return reply, nil
}

func aiGatewaySystemPromptPath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "system_prompt.txt")
}

func aiGatewayWriteTextAtomic(path string, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, []byte(text), 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func aiGatewayBuildMessageRecord(agentID string, current aiGatewayCurrentSnapshot, question string, reply aiGatewayReplySnapshot) (aiGatewayMessageRecord, bool, error) {
	events, err := aiGatewayReadReplyEvents(agentID)
	if err != nil {
		return aiGatewayMessageRecord{}, false, err
	}
	turnEvents := aiGatewayFilterEventsByTurn(events, reply.TurnID)
	sanitizedQuestion := aiGatewaySanitizeUserQuestion(question)
	if sanitizedQuestion == "" {
		if strings.TrimSpace(current.TurnID) == strings.TrimSpace(reply.TurnID) {
			sanitizedQuestion = aiGatewayCurrentQuestion(current)
		}
	}
	classifiedQuestion := sanitizedQuestion
	if classifiedQuestion == "" {
		classifiedQuestion = question
	}
	source, kind := aiGatewayClassifyQuestion(classifiedQuestion)
	if aiGatewayShouldSkipMessageRecord(current, sanitizedQuestion, source, kind, reply, reply.HTTPRequests) {
		return aiGatewayMessageRecord{}, false, nil
	}
	if prompt := aiGatewayExtractMessageSystemPrompt(current, reply.TurnID, turnEvents); prompt != "" {
		if err := aiGatewayWriteTextAtomic(aiGatewaySystemPromptPath(agentID), prompt); err != nil {
			return aiGatewayMessageRecord{}, false, err
		}
	}
	record := aiGatewayMessageRecord{
		Q:         sanitizedQuestion,
		A:         aiGatewaySanitizeMessageAnswer(reply.Answer),
		QTime:     aiGatewayMessageQuestionTime(current, reply),
		ATime:     aiGatewayMessageAnswerTime(reply),
		Model:     aiGatewayFirstNonEmpty(aiGatewayReplyPrimaryModel(reply), strings.TrimSpace(current.Model)),
		Thinking:  strings.TrimSpace(reply.Thinking),
		ToolCalls: aiGatewayBuildMessageToolCalls(turnEvents, reply),
	}
	if currentRecords := agentInspectorBuildCurrentOnlyRecords(current); len(currentRecords) > 0 {
		candidate := currentRecords[len(currentRecords)-1]
		if strings.TrimSpace(candidate.Q) != "" {
			record.Q = candidate.Q
		}
		if strings.TrimSpace(candidate.A) != "" {
			record.A = candidate.A
		}
		if strings.TrimSpace(candidate.QTime) != "" {
			record.QTime = candidate.QTime
		}
		if strings.TrimSpace(candidate.ATime) != "" {
			record.ATime = candidate.ATime
		}
		if strings.TrimSpace(candidate.Model) != "" {
			record.Model = candidate.Model
		}
		if strings.TrimSpace(candidate.Thinking) != "" {
			record.Thinking = strings.TrimSpace(aiGatewayJoinUniqueText([]string{candidate.Thinking, record.Thinking}))
		}
		if len(candidate.ToolCalls) > 0 {
			record.ToolCalls = candidate.ToolCalls
		}
	}
	return record, true, nil
}

func aiGatewayAppendMessageRecord(agentID string, current aiGatewayCurrentSnapshot, question string, reply aiGatewayReplySnapshot) (int64, error) {
	record, ok, err := aiGatewayBuildMessageRecord(agentID, current, question, reply)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	return agentHistoryUpsertRecord(agentID, current, reply, record)
}

func aiGatewaySyncLatestCurrentHistory(agentID string) (int64, error) {
	current, err := aiGatewayReadCurrentSnapshot(agentID)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	turns := agentInspectorBuildPersistedTurnsFromFullCurrent(current)
	if len(turns) <= 1 {
		return 0, nil
	}
	reply := aiGatewayLoadReplySnapshot(agentID)
	syncCurrent := current
	syncCurrent.TurnID = ""
	syncReply := reply
	syncReply.TurnID = ""
	flushableTurns := turns[:len(turns)-1]
	db, err := agentHistoryOpen(agentID)
	if err != nil {
		return 0, err
	}
	var flushedTurnCount int
	if err := db.QueryRow(`SELECT COALESCE(flushed_turn_count, 0) FROM conversations WHERE id = ? LIMIT 1`, strings.TrimSpace(current.ConversationID)).Scan(&flushedTurnCount); err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	if flushedTurnCount < 0 {
		flushedTurnCount = 0
	}
	if flushedTurnCount > len(flushableTurns) {
		flushedTurnCount = len(flushableTurns)
	}
	pendingTurns := flushableTurns[flushedTurnCount:]
	if len(pendingTurns) == 0 {
		return 0, nil
	}
	var latestHistoryID int64
	for _, turn := range pendingTurns {
		question := strings.TrimSpace(aiGatewayString(turn["q"]))
		if question == "" {
			continue
		}
		answer := strings.TrimSpace(aiGatewayString(turn["a"]))
		qTime := agentHistoryFirstNonEmpty(current.Timestamp, current.StartedAt, current.UpdatedAt)
		aTime := agentHistoryFirstNonEmpty(current.UpdatedAt, current.Timestamp, current.StartedAt)
		if startTS := agentInspectorHistoryUnix(turn, "start_ts"); startTS > 0 {
			qTime = time.Unix(startTS, 0).UTC().Format(time.RFC3339)
		}
		if endTS := agentInspectorHistoryUnix(turn, "ts"); endTS > 0 {
			aTime = time.Unix(endTS, 0).UTC().Format(time.RFC3339)
		}
		record := aiGatewayMessageRecord{
			Q:     question,
			A:     answer,
			QTime: qTime,
			ATime: aTime,
			Model: strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(turn["model"]), current.Model, aiGatewayReplyPrimaryModel(reply))),
		}
		historyID, err := agentHistoryUpsertRecordWithItemAndCount(agentID, syncCurrent, syncReply, record, turn, &flushedTurnCount)
		if err != nil {
			return 0, err
		}
		if historyID > 0 {
			latestHistoryID = historyID
		}
		flushedTurnCount++
	}
	return latestHistoryID, nil
}

func aiGatewayMessageQuestionTime(current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot) string {
	if strings.TrimSpace(current.TurnID) == strings.TrimSpace(reply.TurnID) {
		if ts := strings.TrimSpace(current.Timestamp); ts != "" {
			return ts
		}
		if ts := strings.TrimSpace(current.StartedAt); ts != "" {
			return ts
		}
	}
	return strings.TrimSpace(reply.StartedAt)
}

func aiGatewayMessageAnswerTime(reply aiGatewayReplySnapshot) string {
	if ts := strings.TrimSpace(reply.UpdatedAt); ts != "" {
		return ts
	}
	return strings.TrimSpace(reply.StartedAt)
}

func aiGatewayFilterEventsByTurn(events []map[string]interface{}, turnID string) []map[string]interface{} {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" || len(events) == 0 {
		return events
	}
	filtered := make([]map[string]interface{}, 0, len(events))
	for _, event := range events {
		eventTurnID := strings.TrimSpace(aiGatewayString(event["turn_id"]))
		if eventTurnID != "" && eventTurnID != turnID {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered
}

func aiGatewayCompactMessageToolCalls(items []aiGatewayMessageToolCall) []aiGatewayMessageToolCall {
	if len(items) == 0 {
		return nil
	}
	out := make([]aiGatewayMessageToolCall, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		input := strings.TrimSpace(item.Input)
		output := strings.TrimSpace(item.Output)
		if name == "" {
			continue
		}
		key := strings.Join([]string{name, aiGatewayCompactText(input, 256), aiGatewayCompactText(output, 256)}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, aiGatewayMessageToolCall{
			Name:   name,
			Input:  input,
			Output: output,
			Index:  item.Index,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func aiGatewayShouldSkipInternalPrompt(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	switch {
	case lower == "":
		return false
	case strings.Contains(lower, "generate a concise, sentence-case title"):
		return true
	case strings.Contains(lower, "return json with a single \"title\" field"):
		return true
	case strings.Contains(lower, "the messages above are a conversation to summarize"):
		return true
	case strings.Contains(lower, "<previous-summary>"):
		return true
	default:
		return false
	}
}

func aiGatewayShouldSkipMessageRecord(current aiGatewayCurrentSnapshot, question, source, kind string, reply aiGatewayReplySnapshot, requests []aiGatewayRequestSpan) bool {
	if source != "user" {
		return true
	}
	if strings.TrimSpace(question) == "" {
		return true
	}
	if strings.TrimSpace(aiGatewaySanitizeMessageAnswer(reply.Answer)) == "" {
		return true
	}
	if body := aiGatewayMap(current.Body); len(body) > 0 {
		if aiGatewayShouldSkipInternalPrompt(aiGatewayBuildSystemPrompt(body)) {
			return true
		}
	}
	if kind == "suggestion_mode" {
		return true
	}
	_ = kind
	_ = reply
	_ = requests
	return false
}

func aiGatewayClassifyQuestion(question string) (string, string) {
	trimmed := strings.TrimSpace(question)
	lower := strings.ToLower(trimmed)
	switch {
	case trimmed == "":
		return "internal", "empty"
	case strings.HasPrefix(lower, "perform a web search for the query:"):
		return "internal", "tool_prompt"
	case strings.Contains(trimmed, "Sender (untrusted metadata):"):
		return "user", "forwarded_user"
	case strings.Contains(lower, "the messages above are a conversation to summarize"):
		return "internal", "summary"
	case strings.Contains(lower, "<previous-summary>"):
		return "internal", "summary_update"
	case strings.Contains(lower, "system-reminder"):
		return "system", "system_prompt"
	case strings.Contains(lower, "[suggestion mode:"):
		return "internal", "suggestion_mode"
	case strings.Contains(lower, "reply with only the suggestion"):
		return "internal", "suggestion_mode"
	case strings.Contains(lower, "would they think \"i was just about to type that\""):
		return "internal", "suggestion_mode"
	case strings.TrimSpace(lower) == "reply in chinese":
		return "auto", "bootstrap"
	default:
		return "user", "prompt"
	}
}

func aiGatewayReadReplyEvents(agentID string) ([]map[string]interface{}, error) {
	return []map[string]interface{}{}, nil
}

func aiGatewayExtractThinkingSteps(events []map[string]interface{}) []string {
	steps := []string{}
	for _, event := range events {
		if aiGatewayString(event["kind"]) != "sse" {
			continue
		}
		payload := aiGatewayMap(event["payload"])
		if aiGatewayString(payload["stream_kind"]) != "thinking" {
			continue
		}
		text := strings.TrimSpace(aiGatewayString(payload["content"]))
		if text != "" {
			steps = append(steps, text)
		}
	}
	return steps
}

func aiGatewayExtractToolSteps(events []map[string]interface{}) []map[string]interface{} {
	steps := []map[string]interface{}{}
	for _, event := range events {
		kind := aiGatewayString(event["kind"])
		switch kind {
		case "tool_call", "web_search", "request", "request_complete":
			steps = append(steps, aiGatewayCloneAnyMap(event))
		}
	}
	return steps
}

func aiGatewayExtractSystemPrompt(events []map[string]interface{}) string {
	best := ""
	for _, event := range events {
		if aiGatewayString(event["kind"]) != "request" {
			continue
		}
		payload := aiGatewayMap(event["payload"])
		body := aiGatewayMap(payload["body"])
		if len(body) == 0 {
			continue
		}
		if prompt := aiGatewayBuildSystemPrompt(body); prompt != "" {
			if len([]rune(prompt)) > len([]rune(best)) {
				best = prompt
			}
		}
	}
	return best
}

func aiGatewayBuildSystemPrompt(body map[string]interface{}) string {
	parts := []string{}
	if instructions := strings.TrimSpace(aiGatewayString(body["instructions"])); instructions != "" {
		parts = append(parts, instructions)
	}
	if system := body["system"]; system != nil {
		if text := strings.TrimSpace(aiGatewayFlattenPromptValue(system)); text != "" {
			parts = append(parts, text)
		}
	}
	for _, item := range aiGatewayExtractMessages(body) {
		role := aiGatewayString(item["role"])
		if role != "system" && role != "developer" {
			continue
		}
		if text := strings.TrimSpace(aiGatewayFlattenPromptValue(item["content"])); text != "" {
			parts = append(parts, text)
		}
	}
	for _, raw := range aiGatewayExtractInputItems(body) {
		item := aiGatewayMap(raw)
		if len(item) == 0 {
			continue
		}
		role := aiGatewayString(item["role"])
		if role != "system" && role != "developer" {
			continue
		}
		if text := strings.TrimSpace(aiGatewayFlattenPromptValue(item["content"])); text != "" {
			parts = append(parts, text)
		}
	}
	return aiGatewayCompactText(aiGatewayJoinUniqueText(parts), 24000)
}

func aiGatewayExtractToolTimeline(events []map[string]interface{}) []map[string]interface{} {
	steps := []map[string]interface{}{}
	for _, event := range events {
		kind := aiGatewayString(event["kind"])
		payload := aiGatewayMap(event["payload"])
		switch kind {
		case "tool_call":
			step := M{
				"kind":      "tool_call",
				"tool_id":   aiGatewayString(payload["tool_id"]),
				"tool_name": aiGatewayString(payload["tool_name"]),
				"input":     aiGatewayCompactText(aiGatewayString(payload["arguments"]), 12000),
			}
			if index := aiGatewayInt(event["index"]); index > 0 {
				step["index"] = index
			}
			if ts := aiGatewayString(event["timestamp"]); ts != "" {
				step["timestamp"] = ts
			}
			steps = append(steps, step)
		case "web_search":
			step := M{
				"kind":      "tool_call",
				"tool_id":   aiGatewayFirstNonEmpty(aiGatewayString(payload["item_id"]), aiGatewayString(payload["tool_id"])),
				"tool_name": aiGatewayFirstNonEmpty(aiGatewayString(payload["tool_name"]), "web_search_call"),
				"input":     aiGatewayCompactText(aiGatewayWebSearchQuery(aiGatewayMap(payload["payload"])), 12000),
			}
			if status := aiGatewayString(payload["status"]); status != "" {
				step["status"] = status
			}
			if index := aiGatewayInt(event["index"]); index > 0 {
				step["index"] = index
			}
			if ts := aiGatewayString(event["timestamp"]); ts != "" {
				step["timestamp"] = ts
			}
			steps = append(steps, step)
		case "request":
			steps = append(steps, aiGatewayExtractToolResultsFromRequest(payload)...)
		}
	}
	return aiGatewayDedupToolTimeline(steps)
}

func aiGatewayExtractToolResultsFromRequest(payload map[string]interface{}) []map[string]interface{} {
	body := aiGatewayMap(payload["body"])
	if len(body) == 0 {
		return nil
	}
	steps := []map[string]interface{}{}
	appendStep := func(step map[string]interface{}) {
		if len(step) == 0 {
			return
		}
		if requestID := aiGatewayString(payload["request_id"]); requestID != "" {
			step["request_id"] = requestID
		}
		if ts := aiGatewayString(payload["timestamp"]); ts != "" {
			step["timestamp"] = ts
		}
		steps = append(steps, step)
	}
	for _, item := range aiGatewayCurrentTurnMessages(aiGatewayExtractMessages(body)) {
		for _, step := range aiGatewayToolCallsFromMessage(item) {
			appendStep(step)
		}
		for _, step := range aiGatewayToolResultsFromMessage(item) {
			appendStep(step)
		}
	}
	for _, raw := range aiGatewayCurrentTurnInputItems(aiGatewayExtractInputItems(body)) {
		item := aiGatewayMap(raw)
		if len(item) == 0 {
			continue
		}
		for _, step := range aiGatewayToolCallsFromInputItem(item) {
			appendStep(step)
		}
		for _, step := range aiGatewayToolResultsFromInputItem(item) {
			appendStep(step)
		}
	}
	return steps
}

// aiGatewayInjectToolResultsIntoItems folds the tool RESULTS carried in a
// tool-continuation request body back onto the inherited tool_use items, so
// reply.json shows each tool call's output next to the call.
//
// Why here: a tool_use produced in turn N's response has no result yet — the
// CLI runs the tool and sends the output back in the *next* request of the same
// turn (a continuation, role=user with tool_result blocks / role=tool message /
// function_call_output input item). newAIGatewayAuditSession inherits the prior
// turn's Items on a continuation; this matches each tool_result to its tool_use
// by tool_id and attaches the output there. Shared by the gateway proxy and the
// MITM adapter — both construct the session through newAIGatewayAuditSession.
//
// Items are mutated in place (they come from a freshly-loaded prevReply). The
// pass is idempotent: a tool_use that already has a non-empty output is left
// alone, so repeated continuations don't clobber an earlier result.
func aiGatewayInjectToolResultsIntoItems(items []map[string]interface{}, body map[string]interface{}) []map[string]interface{} {
	if len(items) == 0 || len(body) == 0 {
		return items
	}
	results := map[string]interface{}{}
	for _, step := range aiGatewayExtractToolResultsFromRequest(M{"body": body}) {
		if aiGatewayString(step["kind"]) != "tool_result" {
			continue
		}
		id := strings.TrimSpace(aiGatewayString(step["tool_id"]))
		if id == "" {
			continue
		}
		if _, dup := results[id]; !dup {
			results[id] = step["output"]
		}
	}
	if len(results) == 0 {
		return items
	}
	for _, item := range items {
		if aiGatewayString(item["type"]) != "tool_use" {
			continue
		}
		id := strings.TrimSpace(aiGatewayString(item["tool_id"]))
		if id == "" {
			continue
		}
		output, ok := results[id]
		if !ok {
			continue
		}
		if existing := strings.TrimSpace(aiGatewayFlattenPromptValue(item["output"])); existing != "" {
			continue
		}
		item["output"] = output
	}
	return items
}

func aiGatewayToolCallsFromMessage(item map[string]interface{}) []map[string]interface{} {
	if aiGatewayString(item["role"]) != "assistant" {
		return nil
	}
	steps := []map[string]interface{}{}
	for _, raw := range aiGatewaySlice(item["content"]) {
		part := aiGatewayMap(raw)
		if len(part) == 0 {
			continue
		}
		switch aiGatewayString(part["type"]) {
		case "tool_use":
			steps = append(steps, M{
				"kind":      "tool_call",
				"tool_id":   aiGatewayString(part["id"]),
				"tool_name": aiGatewayString(part["name"]),
				"input":     aiGatewayCompactText(aiGatewayJSONString(part["input"]), 12000),
			})
		case "function_call", "tool_call":
			steps = append(steps, M{
				"kind":      "tool_call",
				"tool_id":   aiGatewayFirstNonEmpty(aiGatewayString(part["call_id"]), aiGatewayString(part["id"])),
				"tool_name": aiGatewayString(part["name"]),
				"input":     aiGatewayCompactText(aiGatewayFirstNonEmpty(aiGatewayString(part["arguments"]), aiGatewayJSONString(part["arguments"])), 12000),
			})
		case "custom_tool_call", "web_search_call", "shell_call", "apply_patch_call":
			input := part["arguments"]
			if input == nil {
				input = part["input"]
			}
			if input == nil {
				input = part["action"]
			}
			if input == nil {
				input = part["operation"]
			}
			steps = append(steps, M{
				"kind":      "tool_call",
				"tool_id":   aiGatewayFirstNonEmpty(aiGatewayString(part["call_id"]), aiGatewayString(part["id"])),
				"tool_name": aiGatewayFirstNonEmpty(aiGatewayString(part["name"]), aiGatewayString(part["type"])),
				"input":     aiGatewayCompactText(aiGatewayFlattenPromptValue(input), 12000),
			})
		}
	}
	return steps
}

func aiGatewayToolResultsFromMessage(item map[string]interface{}) []map[string]interface{} {
	results := []map[string]interface{}{}
	role := aiGatewayString(item["role"])
	if role == "tool" || role == "function" {
		return []map[string]interface{}{M{
			"kind":      "tool_result",
			"tool_id":   aiGatewayFirstNonEmpty(aiGatewayString(item["tool_call_id"]), aiGatewayString(item["call_id"])),
			"tool_name": aiGatewayFirstNonEmpty(aiGatewayString(item["name"]), aiGatewayString(item["tool_name"])),
			"output":    aiGatewayCompactText(aiGatewayFlattenPromptValue(item["content"]), 12000),
		}}
	}
	for _, raw := range aiGatewaySlice(item["content"]) {
		part := aiGatewayMap(raw)
		if len(part) == 0 {
			continue
		}
		if aiGatewayString(part["type"]) != "tool_result" {
			continue
		}
		results = append(results, M{
			"kind":      "tool_result",
			"tool_id":   aiGatewayFirstNonEmpty(aiGatewayString(part["tool_use_id"]), aiGatewayString(part["tool_id"])),
			"tool_name": aiGatewayFirstNonEmpty(aiGatewayString(part["name"]), aiGatewayString(part["tool_name"])),
			"output":    aiGatewayCompactText(aiGatewayFlattenPromptValue(part["content"]), 12000),
		})
	}
	return results
}

func aiGatewayToolCallsFromInputItem(item map[string]interface{}) []map[string]interface{} {
	switch aiGatewayString(item["type"]) {
	case "function_call", "tool_call":
		return []map[string]interface{}{M{
			"kind":      "tool_call",
			"tool_id":   aiGatewayFirstNonEmpty(aiGatewayString(item["call_id"]), aiGatewayString(item["id"])),
			"tool_name": aiGatewayString(item["name"]),
			"input":     aiGatewayCompactText(aiGatewayFirstNonEmpty(aiGatewayString(item["arguments"]), aiGatewayJSONString(item["arguments"])), 12000),
		}}
	case "custom_tool_call", "web_search_call", "shell_call", "apply_patch_call":
		input := item["arguments"]
		if input == nil {
			input = item["input"]
		}
		if input == nil {
			input = item["action"]
		}
		if input == nil {
			input = item["operation"]
		}
		return []map[string]interface{}{M{
			"kind":      "tool_call",
			"tool_id":   aiGatewayFirstNonEmpty(aiGatewayString(item["call_id"]), aiGatewayString(item["id"])),
			"tool_name": aiGatewayFirstNonEmpty(aiGatewayString(item["name"]), aiGatewayString(item["type"])),
			"input":     aiGatewayCompactText(aiGatewayFlattenPromptValue(input), 12000),
		}}
	}
	return nil
}

func aiGatewayToolResultsFromInputItem(item map[string]interface{}) []map[string]interface{} {
	itemType := aiGatewayString(item["type"])
	switch itemType {
	case "function_call_output":
		return []map[string]interface{}{M{
			"kind":      "tool_result",
			"tool_id":   aiGatewayFirstNonEmpty(aiGatewayString(item["call_id"]), aiGatewayString(item["tool_call_id"])),
			"tool_name": aiGatewayFirstNonEmpty(aiGatewayString(item["name"]), aiGatewayString(item["tool_name"])),
			"output":    aiGatewayCompactText(aiGatewayFlattenPromptValue(item["output"]), 12000),
		}}
	case "tool_result":
		return []map[string]interface{}{M{
			"kind":      "tool_result",
			"tool_id":   aiGatewayFirstNonEmpty(aiGatewayString(item["tool_use_id"]), aiGatewayString(item["tool_id"])),
			"tool_name": aiGatewayFirstNonEmpty(aiGatewayString(item["name"]), aiGatewayString(item["tool_name"])),
			"output":    aiGatewayCompactText(aiGatewayFlattenPromptValue(item["content"]), 12000),
		}}
	}
	if role := aiGatewayString(item["role"]); role == "tool" || role == "function" {
		return []map[string]interface{}{M{
			"kind":      "tool_result",
			"tool_id":   aiGatewayFirstNonEmpty(aiGatewayString(item["tool_call_id"]), aiGatewayString(item["call_id"])),
			"tool_name": aiGatewayFirstNonEmpty(aiGatewayString(item["name"]), aiGatewayString(item["tool_name"])),
			"output":    aiGatewayCompactText(aiGatewayFlattenPromptValue(item["content"]), 12000),
		}}
	}
	return nil
}

func aiGatewayCurrentTurnMessages(messages []map[string]interface{}) []map[string]interface{} {
	start := aiGatewayLastUserPromptIndexMessages(messages)
	if start < 0 {
		return messages
	}
	return messages[start:]
}

func aiGatewayCurrentTurnInputItems(items []interface{}) []interface{} {
	start := aiGatewayLastUserPromptIndexItems(items)
	if start < 0 {
		return items
	}
	return items[start:]
}

func aiGatewayLastUserPromptIndexMessages(messages []map[string]interface{}) int {
	for i := len(messages) - 1; i >= 0; i-- {
		item := messages[i]
		if len(item) == 0 || aiGatewayString(item["role"]) != "user" {
			continue
		}
		if aiGatewayItemIsToolResult(item) {
			continue
		}
		if strings.TrimSpace(aiGatewayFlattenPromptValue(item["content"])) == "" {
			continue
		}
		return i
	}
	return -1
}

func aiGatewayLastUserPromptIndexItems(items []interface{}) int {
	for i := len(items) - 1; i >= 0; i-- {
		item := aiGatewayMap(items[i])
		if len(item) == 0 || aiGatewayString(item["role"]) != "user" {
			continue
		}
		if aiGatewayItemIsToolResult(item) {
			continue
		}
		if strings.TrimSpace(aiGatewayFlattenPromptValue(item["content"])) == "" && strings.TrimSpace(aiGatewayContentPartToText(item)) == "" {
			continue
		}
		return i
	}
	return -1
}

func aiGatewayDedupToolTimeline(steps []map[string]interface{}) []map[string]interface{} {
	if len(steps) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(steps))
	seen := map[string]struct{}{}
	for _, step := range steps {
		key := strings.Join([]string{
			aiGatewayString(step["kind"]),
			aiGatewayString(step["tool_id"]),
			aiGatewayString(step["tool_name"]),
			aiGatewayCompactText(aiGatewayString(step["input"]), 256),
			aiGatewayCompactText(aiGatewayString(step["output"]), 256),
		}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, step)
	}
	return out
}

func aiGatewayFlattenPromptValue(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := strings.TrimSpace(aiGatewayFlattenPromptValue(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]interface{}:
		if text := strings.TrimSpace(aiGatewayContentPartToText(v)); text != "" {
			return text
		}
		parts := []string{}
		for _, key := range []string{"text", "content", "input", "output", "thinking"} {
			if text := strings.TrimSpace(aiGatewayFlattenPromptValue(v[key])); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return aiGatewayJSONOrString(value)
	}
}

func aiGatewayJoinUniqueText(items []string) string {
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return strings.Join(out, "\n\n")
}

func aiGatewayCompactToolCalls(items []aiGatewayToolCall) []aiGatewayToolCall {
	if len(items) == 0 {
		return nil
	}
	out := make([]aiGatewayToolCall, 0, len(items))
	for _, item := range items {
		out = append(out, aiGatewayToolCall{
			ToolID:    strings.TrimSpace(item.ToolID),
			ToolName:  strings.TrimSpace(item.ToolName),
			Arguments: aiGatewayCompactText(item.Arguments, 8000),
		})
	}
	return out
}

func aiGatewayCompactRequestSpans(items []aiGatewayRequestSpan) []aiGatewayRequestSpan {
	if len(items) == 0 {
		return nil
	}
	out := make([]aiGatewayRequestSpan, 0, len(items))
	for _, item := range items {
		next := item
		next.RequestHeaders = aiGatewaySanitizeHeaders(item.RequestHeaders)
		next.RequestBody = aiGatewaySummarizeRequestBody(item.RequestBody)
		next.ThinkingPreview = aiGatewayCompactText(item.ThinkingPreview, 240)
		next.AnswerPreview = aiGatewayCompactText(item.AnswerPreview, 320)
		next.Usage = aiGatewayCloneAnyMap(item.Usage)
		out = append(out, next)
	}
	return out
}

func aiGatewayNormalizeMessageEvents(events []map[string]interface{}) []map[string]interface{} {
	if len(events) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(events))
	for _, event := range events {
		if normalized := aiGatewayNormalizeMessageEvent(event); len(normalized) > 0 {
			if aiGatewayReplaceDuplicateEvent(out, normalized) {
				continue
			}
			out = append(out, normalized)
		}
	}
	return out
}

func aiGatewayReplaceDuplicateEvent(events []map[string]interface{}, normalized map[string]interface{}) bool {
	kind := aiGatewayString(normalized["kind"])
	if kind != "request" && kind != "request_complete" {
		return false
	}
	payload := aiGatewayMap(normalized["payload"])
	key := strings.Join([]string{
		kind,
		aiGatewayString(payload["request_id"]),
		aiGatewayString(payload["method"]),
		aiGatewayString(payload["url"]),
	}, "|")
	if key == "|||" {
		return false
	}
	for i := len(events) - 1; i >= 0; i-- {
		prev := events[i]
		if aiGatewayString(prev["kind"]) != kind {
			continue
		}
		prevPayload := aiGatewayMap(prev["payload"])
		prevKey := strings.Join([]string{
			kind,
			aiGatewayString(prevPayload["request_id"]),
			aiGatewayString(prevPayload["method"]),
			aiGatewayString(prevPayload["url"]),
		}, "|")
		if prevKey != key {
			continue
		}
		events[i] = normalized
		return true
	}
	return false
}

func aiGatewayNormalizeMessageEvent(event map[string]interface{}) map[string]interface{} {
	if len(event) == 0 {
		return nil
	}
	kind := aiGatewayString(event["kind"])
	payload := aiGatewayMap(event["payload"])
	item := M{
		"kind": kind,
	}
	if index := aiGatewayInt(event["index"]); index > 0 {
		item["index"] = index
	}
	if turnID := aiGatewayString(event["turn_id"]); turnID != "" {
		item["turn_id"] = turnID
	}
	if ts := aiGatewayString(event["timestamp"]); ts != "" {
		item["timestamp"] = ts
	}

	switch kind {
	case "request":
		bodySummary := aiGatewaySummarizeRequestBody(payload["body"])
		historySummary := aiGatewaySummarizeRequestHistory(payload["history"])
		nextPayload := M{
			"request_id":      aiGatewayString(payload["request_id"]),
			"conversation_id": aiGatewayString(payload["conversation_id"]),
			"method":          aiGatewayString(payload["method"]),
			"url":             aiGatewayString(payload["url"]),
			"source":          aiGatewayString(payload["source"]),
			"headers":         aiGatewaySanitizeHeaders(aiGatewayHeaderMap(payload["headers"])),
		}
		if bodySummary != nil {
			nextPayload["body"] = bodySummary
		}
		if historySummary != nil {
			nextPayload["history"] = historySummary
		}
		item["payload"] = nextPayload
	case "request_complete":
		nextPayload := M{
			"request_id":       aiGatewayString(payload["request_id"]),
			"conversation_id":  aiGatewayString(payload["conversation_id"]),
			"provider":         aiGatewayString(payload["provider"]),
			"model":            aiGatewayString(payload["model"]),
			"method":           aiGatewayString(payload["method"]),
			"url":              aiGatewayString(payload["url"]),
			"status":           aiGatewayString(payload["status"]),
			"status_code":      aiGatewayInt(payload["status_code"]),
			"latency_ms":       aiGatewayInt(payload["latency_ms"]),
			"tool_call_count":  aiGatewayInt(payload["tool_call_count"]),
			"input_tokens":     aiGatewayInt(payload["input_tokens"]),
			"output_tokens":    aiGatewayInt(payload["output_tokens"]),
			"total_tokens":     aiGatewayInt(payload["total_tokens"]),
			"cost_credit":      aiGatewayFloat(payload["cost_credit"]),
			"thinking_preview": aiGatewayCompactText(aiGatewayString(payload["thinking_preview"]), 240),
			"answer_preview":   aiGatewayCompactText(aiGatewayString(payload["answer_preview"]), 320),
		}
		usage := aiGatewayCloneAnyMap(aiGatewayMap(payload["usage"]))
		if len(usage) > 0 {
			nextPayload["usage"] = usage
		}
		item["payload"] = nextPayload
	case "tool_call":
		item["payload"] = M{
			"tool_id":   aiGatewayString(payload["tool_id"]),
			"tool_name": aiGatewayString(payload["tool_name"]),
			"arguments": aiGatewayCompactText(aiGatewayString(payload["arguments"]), 8000),
		}
	case "web_search":
		nextPayload := M{
			"item_id":   aiGatewayFirstNonEmpty(aiGatewayString(payload["item_id"]), aiGatewayString(payload["tool_id"])),
			"tool_name": aiGatewayFirstNonEmpty(aiGatewayString(payload["tool_name"]), "web_search_call"),
			"status":    aiGatewayString(payload["status"]),
			"type":      aiGatewayString(payload["type"]),
		}
		raw := aiGatewayMap(payload["payload"])
		if query := aiGatewayCompactText(aiGatewayWebSearchQuery(raw), 320); query != "" {
			nextPayload["query"] = query
		}
		item["payload"] = nextPayload
	default:
		return nil
	}
	return item
}

func aiGatewaySummarizeRequestBody(value interface{}) interface{} {
	body := aiGatewayMap(value)
	if len(body) == 0 {
		if value == nil {
			return nil
		}
		return aiGatewayCompactValue(value, 320)
	}
	summary := M{}
	for _, key := range []string{"model", "stream", "temperature", "tool_choice", "parallel_tool_calls", "store"} {
		if body[key] != nil {
			summary[key] = aiGatewayCompactScalar(body[key])
		}
	}
	if maxTokens := aiGatewayInt(body["max_tokens"]); maxTokens > 0 {
		summary["max_tokens"] = maxTokens
	}
	if maxOutputTokens := aiGatewayInt(body["max_output_tokens"]); maxOutputTokens > 0 {
		summary["max_output_tokens"] = maxOutputTokens
	}
	if instructions := aiGatewayString(body["instructions"]); instructions != "" {
		summary["instructions_preview"] = aiGatewayCompactText(instructions, 240)
	}
	if inputItems := aiGatewayExtractInputItems(body); len(inputItems) > 0 {
		summary["input_count"] = len(inputItems)
		if roles := aiGatewayRolesFromItems(inputItems); len(roles) > 0 {
			summary["roles"] = roles
		}
	}
	if messages := aiGatewayExtractMessages(body); len(messages) > 0 {
		summary["message_count"] = len(messages)
	}
	if lastUser := aiGatewayCompactText(aiGatewayExtractQuestion(body), 240); lastUser != "" {
		summary["last_user_preview"] = lastUser
	}
	if tools := aiGatewaySlice(body["tools"]); len(tools) > 0 {
		summary["tool_count"] = len(tools)
		if names := aiGatewayToolNames(tools); len(names) > 0 {
			summary["tool_names"] = names
		}
	}
	if include := aiGatewayStringList(aiGatewaySlice(body["include"]), 8); len(include) > 0 {
		summary["include"] = include
	}
	if len(summary) == 0 {
		return aiGatewayCompactValue(value, 320)
	}
	return summary
}

func aiGatewaySummarizeRequestHistory(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	items := aiGatewaySlice(value)
	if len(items) == 0 {
		return aiGatewayCompactValue(value, 240)
	}
	summary := M{
		"count": len(items),
	}
	if roles := aiGatewayRolesFromItems(items); len(roles) > 0 {
		summary["roles"] = roles
	}
	if lastUser := aiGatewayCompactText(aiGatewayQuestionFromItems(items), 240); lastUser != "" {
		summary["last_user_preview"] = lastUser
	}
	return summary
}

func aiGatewaySanitizeHeaders(header map[string][]string) map[string][]string {
	if len(header) == 0 {
		return map[string][]string{}
	}
	out := map[string][]string{}
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values := header[key]
		lower := strings.ToLower(strings.TrimSpace(key))
		switch {
		case strings.Contains(lower, "authorization"),
			strings.Contains(lower, "api-key"),
			strings.Contains(lower, "token"),
			strings.Contains(lower, "cookie"):
			out[key] = []string{"[REDACTED]"}
		default:
			next := make([]string, 0, len(values))
			for _, value := range values {
				next = append(next, aiGatewayCompactText(value, 160))
			}
			out[key] = next
		}
	}
	return out
}

func aiGatewayHeaderMap(value interface{}) map[string][]string {
	switch header := value.(type) {
	case map[string][]string:
		return aiGatewayCloneHeader(header)
	case map[string]interface{}:
		out := map[string][]string{}
		for key, raw := range header {
			if list := aiGatewaySlice(raw); len(list) > 0 {
				values := make([]string, 0, len(list))
				for _, item := range list {
					if text := aiGatewayCompactText(aiGatewayJSONOrString(item), 160); text != "" {
						values = append(values, text)
					}
				}
				if len(values) > 0 {
					out[key] = values
				}
				continue
			}
			if text := aiGatewayCompactText(aiGatewayJSONOrString(raw), 160); text != "" {
				out[key] = []string{text}
			}
		}
		return out
	default:
		return nil
	}
}

func aiGatewayRolesFromItems(items []interface{}) []string {
	seen := map[string]struct{}{}
	roles := []string{}
	for _, raw := range items {
		role := ""
		if item := aiGatewayMap(raw); len(item) > 0 {
			role = aiGatewayString(item["role"])
		}
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	return roles
}

func aiGatewayQuestionFromItems(items []interface{}) string {
	for i := len(items) - 1; i >= 0; i-- {
		item := aiGatewayMap(items[i])
		if len(item) == 0 || aiGatewayString(item["role"]) != "user" {
			continue
		}
		if aiGatewayItemIsToolResult(item) {
			continue
		}
		if content := aiGatewayString(item["content"]); content != "" {
			return strings.TrimSpace(content)
		}
		if contentParts := aiGatewaySlice(item["content"]); len(contentParts) > 0 {
			textParts := []string{}
			for _, part := range contentParts {
				if text := strings.TrimSpace(aiGatewayContentPartToText(part)); text != "" {
					textParts = append(textParts, text)
				}
			}
			if len(textParts) > 0 {
				return strings.Join(textParts, "\n")
			}
		}
		if text := strings.TrimSpace(aiGatewayContentPartToText(item)); text != "" {
			return text
		}
	}
	return ""
}

func aiGatewayItemIsToolResult(item map[string]interface{}) bool {
	if len(item) == 0 {
		return false
	}
	itemType := aiGatewayString(item["type"])
	if itemType == "tool_result" || itemType == "function_call_output" {
		return true
	}
	if aiGatewayString(item["tool_use_id"]) != "" || aiGatewayString(item["tool_call_id"]) != "" || aiGatewayString(item["call_id"]) != "" {
		return true
	}
	contentParts := aiGatewaySlice(item["content"])
	if len(contentParts) == 0 {
		return false
	}
	hasPlainText := false
	for _, raw := range contentParts {
		part := aiGatewayMap(raw)
		if len(part) == 0 {
			if strings.TrimSpace(aiGatewayJSONOrString(raw)) != "" {
				hasPlainText = true
			}
			continue
		}
		partType := aiGatewayString(part["type"])
		if partType == "tool_result" || partType == "function_call_output" {
			return true
		}
		if aiGatewayString(part["tool_use_id"]) != "" || aiGatewayString(part["tool_call_id"]) != "" || aiGatewayString(part["call_id"]) != "" {
			return true
		}
		if strings.TrimSpace(aiGatewayContentPartToText(part)) != "" {
			hasPlainText = true
		}
	}
	return !hasPlainText
}

func aiGatewayQuestionFromHistoryValue(value interface{}) string {
	if text := strings.TrimSpace(aiGatewayString(value)); text != "" {
		return text
	}
	items := aiGatewaySlice(value)
	if len(items) == 0 {
		return ""
	}
	return aiGatewayQuestionFromItems(items)
}

func aiGatewayQuestionFromMessages(messages []map[string]interface{}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		item := messages[i]
		if len(item) == 0 || aiGatewayString(item["role"]) != "user" {
			continue
		}
		if aiGatewayItemIsToolResult(item) {
			continue
		}
		if content := aiGatewayString(item["content"]); content != "" {
			return strings.TrimSpace(content)
		}
		if contentParts := aiGatewaySlice(item["content"]); len(contentParts) > 0 {
			textParts := []string{}
			for _, part := range contentParts {
				if text := strings.TrimSpace(aiGatewayContentPartToText(part)); text != "" {
					textParts = append(textParts, text)
				}
			}
			if len(textParts) > 0 {
				return strings.Join(textParts, "\n")
			}
		}
		if text := strings.TrimSpace(aiGatewayContentPartToText(item)); text != "" {
			return text
		}
	}
	return ""
}

func aiGatewaySanitizeUserQuestion(question string) string {
	question = systemReminderBlockRe.ReplaceAllString(question, "")
	question = environmentContextBlockRe.ReplaceAllString(question, "")
	question = strings.ReplaceAll(question, "<system-reminder>", "")
	question = strings.ReplaceAll(question, "</system-reminder>", "")
	question = strings.ReplaceAll(question, "<environment_context>", "")
	question = strings.ReplaceAll(question, "</environment_context>", "")
	// Drop harness scaffolding so it never reads as a human prompt: the
	// auto-compaction preamble is the whole message → blank it; slash-command
	// wrapper tags are stripped, leaving only real text (if any) behind.
	question = compactionPreambleRe.ReplaceAllString(question, "")
	question = localCommandBlockRe.ReplaceAllString(question, "")
	question = requestInterruptedRe.ReplaceAllString(question, "")
	question = taskNotificationRe.ReplaceAllString(question, "")
	question = cicyNotifyRe.ReplaceAllString(question, "")
	question = strings.TrimSpace(question)
	if strings.HasPrefix(question, "Sender (untrusted metadata):") {
		question = openClawForwardedHeaderRe.ReplaceAllString(question, "")
		question = strings.TrimSpace(question)
		question = openClawLeadingTimestampRe.ReplaceAllString(question, "")
	}
	return strings.TrimSpace(question)
}

func aiGatewayReplyPrimaryModel(reply aiGatewayReplySnapshot) string {
	if len(reply.Models) > 0 && strings.TrimSpace(reply.Models[0]) != "" {
		return strings.TrimSpace(reply.Models[0])
	}
	for _, req := range reply.HTTPRequests {
		if strings.TrimSpace(req.Model) != "" {
			return strings.TrimSpace(req.Model)
		}
	}
	// Persisted in lite snapshot for round-trip (no Models/HTTPRequests on disk)
	if strings.TrimSpace(reply.Model) != "" {
		return strings.TrimSpace(reply.Model)
	}
	return ""
}

func aiGatewayToolNames(items []interface{}) []string {
	names := []string{}
	seen := map[string]struct{}{}
	for _, raw := range items {
		item := aiGatewayMap(raw)
		if len(item) == 0 {
			continue
		}
		name := aiGatewayString(item["name"])
		if name == "" {
			if fn := aiGatewayMap(item["function"]); len(fn) > 0 {
				name = aiGatewayString(fn["name"])
			}
		}
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
		if len(names) >= 12 {
			break
		}
	}
	return names
}

func aiGatewayStringList(items []interface{}, limit int) []string {
	out := []string{}
	for _, raw := range items {
		if limit > 0 && len(out) >= limit {
			break
		}
		if text := aiGatewayCompactText(aiGatewayJSONOrString(raw), 120); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func aiGatewayCompactValue(value interface{}, limit int) interface{} {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case string:
		return aiGatewayCompactText(v, limit)
	case bool, int, int64, float64, float32:
		return v
	default:
		return aiGatewayCompactText(aiGatewayJSONOrString(value), limit)
	}
}

func aiGatewayCompactScalar(value interface{}) interface{} {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return aiGatewayCompactText(v, 120)
	case bool, int, int64, float64, float32:
		return v
	default:
		return aiGatewayCompactText(aiGatewayJSONOrString(value), 120)
	}
}

func aiGatewayCompactText(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if limit <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return strings.TrimSpace(string(runes[:limit])) + "... [truncated]"
}

func aiGatewayWebSearchQuery(payload map[string]interface{}) string {
	if len(payload) == 0 {
		return ""
	}
	for _, key := range []string{"query", "search_query", "q"} {
		if value := aiGatewayString(payload[key]); value != "" {
			return value
		}
	}
	if action := aiGatewayMap(payload["action"]); len(action) > 0 {
		for _, key := range []string{"query", "search_query", "q"} {
			if value := aiGatewayString(action[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func aiGatewayConversationIDForTurn(agentID, turnID string) string {
	body, err := os.ReadFile(filepath.Join(aiGatewayHistoryDir(agentID), "current.json"))
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	var current aiGatewayCurrentSnapshot
	if err := json.Unmarshal(body, &current); err != nil {
		return ""
	}
	if strings.TrimSpace(current.TurnID) != strings.TrimSpace(turnID) {
		return ""
	}
	return strings.TrimSpace(current.ConversationID)
}

func aiGatewayReadCurrentSnapshot(agentID string) (aiGatewayCurrentSnapshot, error) {
	current := aiGatewayCurrentSnapshot{}
	body, err := os.ReadFile(filepath.Join(aiGatewayHistoryDir(agentID), "current.json"))
	if err != nil {
		return current, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return current, nil
	}
	if err := json.Unmarshal(body, &current); err != nil {
		return aiGatewayCurrentSnapshot{}, err
	}
	return current, nil
}

func aiGatewaySyncCurrentSnapshotToHistoryDB(agentID string) (int64, error) {
	current, err := aiGatewayReadCurrentSnapshot(agentID)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if strings.TrimSpace(current.AgentID) == "" {
		current.AgentID = agentID
	}
	return int64(current.MaxHistoryID), nil
}

func aiGatewayCurrentQuestion(current aiGatewayCurrentSnapshot) string {
	if body := aiGatewayMap(current.Body); len(body) > 0 {
		if question := aiGatewayExtractQuestion(body); question != "" {
			return aiGatewaySanitizeUserQuestion(question)
		}
	}
	return ""
}

func aiGatewayExtractMessageSystemPrompt(current aiGatewayCurrentSnapshot, turnID string, events []map[string]interface{}) string {
	if prompt := aiGatewayExtractSystemPrompt(events); prompt != "" {
		return prompt
	}
	if strings.TrimSpace(current.TurnID) != strings.TrimSpace(turnID) {
		return ""
	}
	if body := aiGatewayMap(current.Body); len(body) > 0 {
		return aiGatewayBuildSystemPrompt(body)
	}
	return ""
}

func aiGatewaySanitizeMessageAnswer(answer string) string {
	answer = strings.TrimSpace(answer)
	answer = strings.TrimPrefix(answer, "[[reply_to_current]]")
	return strings.TrimSpace(answer)
}

func aiGatewayBuildMessageToolCalls(events []map[string]interface{}, reply aiGatewayReplySnapshot) []aiGatewayMessageToolCall {
	steps := aiGatewayExtractToolTimeline(events)
	if len(steps) == 0 && len(reply.ToolCalls) == 0 {
		return nil
	}

	out := make([]aiGatewayMessageToolCall, 0, len(steps)+len(reply.ToolCalls))
	byID := map[string]*aiGatewayMessageToolCall{}
	mergeField := func(current, next string) string {
		current = strings.TrimSpace(current)
		next = strings.TrimSpace(next)
		switch {
		case current == "":
			return next
		case next == "":
			return current
		case current == next:
			return current
		default:
			return aiGatewayCompactText(aiGatewayJoinUniqueText([]string{current, next}), 12000)
		}
	}
	appendCall := func(id, name, input, output string, index int) *aiGatewayMessageToolCall {
		id = strings.TrimSpace(id)
		name = strings.TrimSpace(name)
		input = strings.TrimSpace(input)
		output = strings.TrimSpace(output)
		if id != "" {
			if existing := byID[id]; existing != nil {
				if existing.Name == "" {
					existing.Name = name
				}
				existing.Input = mergeField(existing.Input, input)
				existing.Output = mergeField(existing.Output, output)
				if existing.Index == 0 && index > 0 {
					existing.Index = index
				}
				return existing
			}
		}
		out = append(out, aiGatewayMessageToolCall{
			Name:   name,
			Input:  input,
			Output: output,
			Index:  index,
		})
		call := &out[len(out)-1]
		if id != "" {
			byID[id] = call
		}
		return call
	}

	for _, step := range steps {
		id := aiGatewayString(step["tool_id"])
		name := aiGatewayString(step["tool_name"])
		input := aiGatewayCompactText(aiGatewayString(step["input"]), 12000)
		output := aiGatewayCompactText(aiGatewayString(step["output"]), 12000)
		index := aiGatewayInt(step["index"])
		_ = appendCall(id, name, input, output, index)
	}

	for _, item := range reply.ToolCalls {
		_ = appendCall(item.ToolID, item.ToolName, aiGatewayCompactText(item.Arguments, 12000), "", 0)
	}

	// Backfill outputs from reply.Items: tool_use items now carry the result that
	// arrived in the continuation request (aiGatewayInjectToolResultsIntoItems).
	// The event-based timeline (steps) is disabled, so this is the live source of
	// tool outputs. Only fill outputs — inputs/names already came from the loops
	// above — to avoid duplicating tool calls.
	for _, item := range reply.Items {
		if aiGatewayString(item["type"]) != "tool_use" {
			continue
		}
		output := aiGatewayCompactText(aiGatewayFlattenPromptValue(item["output"]), 12000)
		if strings.TrimSpace(output) == "" {
			continue
		}
		id := strings.TrimSpace(aiGatewayString(item["tool_id"]))
		if existing := byID[id]; existing != nil {
			existing.Output = mergeField(existing.Output, output)
			continue
		}
		_ = appendCall(id, aiGatewayString(item["name"]), aiGatewayCompactText(aiGatewayJSONString(item["input"]), 12000), output, 0)
	}

	clean := make([]aiGatewayMessageToolCall, 0, len(out))
	seen := map[string]struct{}{}
	for _, item := range out {
		item.Name = strings.TrimSpace(item.Name)
		item.Input = strings.TrimSpace(item.Input)
		item.Output = strings.TrimSpace(item.Output)
		if item.Name == "" {
			continue
		}
		key := strings.Join([]string{
			item.Name,
			aiGatewayCompactText(item.Input, 256),
			aiGatewayCompactText(item.Output, 256),
		}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		clean = append(clean, item)
	}
	if len(clean) == 0 {
		return nil
	}
	return clean
}

func aiGatewayRequestIDsFromReply(reply aiGatewayReplySnapshot) []string {
	seen := map[string]struct{}{}
	ids := []string{}
	for _, item := range reply.HTTPRequests {
		id := strings.TrimSpace(item.RequestID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func (s *aiGatewayAuditSession) resetReplyDirLocked() error {
	if s == nil {
		return nil
	}
	_ = os.RemoveAll(aiGatewayReplyDir(s.agentID))
	s.replyEventIndex = 0
	return nil
}

func (s *aiGatewayAuditSession) writeReplyEventLocked(kind string, payload interface{}) error {
	_ = kind
	_ = payload
	return nil
}

func (s *aiGatewayAuditSession) streamChatEvents(events []aiGatewayReplyEvent) []ChatEvent {
	if s == nil || len(events) == 0 {
		return nil
	}
	out := make([]ChatEvent, 0, len(events))
	for _, event := range events {
		payload := aiGatewayMap(event.Payload)
		switch event.Kind {
		case "sse":
			streamKind := aiGatewayString(payload["stream_kind"])
			// content 必须用 RawString —— SSE chunk 内容前后的空格是有意义的，
			// 不能 trim（不然 " user"+ " has" 拼成 "userhas"）。
			content := aiGatewayRawString(payload["content"])
			if content == "" {
				continue
			}
			switch streamKind {
			case "answer":
				out = append(out, ChatEvent{Type: "ai_chunk", Data: M{"delta": content, "agent_id": s.agentID, "conversation_id": s.current.ConversationID, "turn_id": s.current.TurnID, "history_id": int64(s.current.MaxHistoryID)}})
			case "thinking":
				out = append(out, ChatEvent{Type: "status_change", Data: M{"status": "thinking", "agent_id": s.agentID, "conversation_id": s.current.ConversationID, "turn_id": s.current.TurnID, "history_id": int64(s.current.MaxHistoryID)}})
				// Streamed thinking text for subscribers that render a live
				// thinking block (mobile). Same shape as ai_chunk; delta is
				// RawString — whitespace significant, append verbatim.
				out = append(out, ChatEvent{Type: "thinking_chunk", Data: M{"delta": content, "agent_id": s.agentID, "conversation_id": s.current.ConversationID, "turn_id": s.current.TurnID, "history_id": int64(s.current.MaxHistoryID)}})
			}
		case "tool_call", "web_search":
			toolName := strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(payload["tool_name"]), event.Kind))
			if toolName == "" {
				continue
			}
			out = append(out, ChatEvent{Type: "status_change", Data: M{"status": "tool_use", "tool_name": toolName, "agent_id": s.agentID, "conversation_id": s.current.ConversationID, "turn_id": s.current.TurnID, "history_id": int64(s.current.MaxHistoryID)}})
		}
	}
	return out
}

func (s *aiGatewayAuditSession) emitReplyStreamPayload(payload map[string]interface{}) {
	if s == nil || len(payload) == 0 {
		return
	}
	events := aiGatewayReplyEventsFromStreamPayload(payload)
	s.mu.Lock()
	if s.finalized {
		s.mu.Unlock()
		return
	}
	for _, event := range events {
		_ = s.writeReplyEventLocked(event.Kind, event.Payload)
	}
	// Real-time cache/input usage (from message_start) → reply.json immediately.
	usageUpdated := s.applyStreamUsageLocked(payload)
	statusEvent := s.applyStreamEventsLocked(events)
	// applyStreamEventsLocked only flushes reply.json on a CONTENT change; if the
	// only thing that changed this chunk was usage (e.g. the message_start event),
	// flush here so the cache numbers land in reply.json without waiting.
	if usageUpdated {
		s.writeLiveReplySnapshotLocked()
	}
	chatEvents := s.streamChatEvents(events)
	replyHooks := s.replyHooks
	s.mu.Unlock()
	for _, evt := range chatEvents {
		hub.publishAgent(s.agentID, evt)
	}
	if statusEvent != nil {
		hub.publishAgent(s.agentID, *statusEvent)
	}
	for _, h := range replyHooks {
		h.handleEvents(events)
	}
}

// flushPendingItemLocked 把 pendingItem append 到 reply.Items 并立刻刷盘 reply.json。
// caller 必须持有 s.mu。
func (s *aiGatewayAuditSession) flushPendingItemLocked() {
	if s == nil || s.pendingItem == nil {
		return
	}
	pi := s.pendingItem
	s.pendingItem = nil
	switch pi.Kind {
	case "thinking":
		if strings.TrimSpace(pi.Thinking) == "" {
			return
		}
		s.reply.Items = append(s.reply.Items, map[string]interface{}{
			"id":       len(s.reply.Items) + 1,
			"type":     "thinking",
			"thinking": pi.Thinking,
		})
	case "text":
		if strings.TrimSpace(pi.Text) == "" {
			return
		}
		s.reply.Items = append(s.reply.Items, map[string]interface{}{
			"id":   len(s.reply.Items) + 1,
			"type": "text",
			"text": pi.Text,
		})
	case "tool_use":
		if pi.ToolID == "" && pi.ToolName == "" {
			return
		}
		var input interface{}
		if pi.Arguments != "" {
			if err := json.Unmarshal([]byte(pi.Arguments), &input); err != nil {
				input = pi.Arguments
			}
		}
		// 优先用 OutputToolID（=current.json 中的 tool_use_id；Codex=call_xxx），
		// 否则 fallback 到 ToolID（stream 累积 key；Codex=fc_xxx）。
		exposedToolID := pi.OutputToolID
		if exposedToolID == "" {
			exposedToolID = pi.ToolID
		}
		s.reply.Items = append(s.reply.Items, map[string]interface{}{
			"id":      len(s.reply.Items) + 1,
			"type":    "tool_use",
			"tool_id": exposedToolID,
			"name":    pi.ToolName,
			"input":   input,
		})
	default:
		return
	}
	if !s.auxiliary {
		if err := aiGatewayWriteReplySnapshot(s.agentID, s.reply); err != nil {
			log.Printf("[ai-gateway] flush reply item write failed for %s: %v", s.agentID, err)
		}
		// 立即把这个新 item 通知给 reply hooks（IM 推送等），auxiliary 不通知。
		// 每个 item flush 一次 = 一次 IM 消息（不再 streaming edit 同一条消息）。
		if len(s.replyHooks) > 0 && len(s.reply.Items) > 0 {
			latest := s.reply.Items[len(s.reply.Items)-1]
			items := []map[string]interface{}{aiGatewayCloneAnyMap(latest)}
			for _, h := range s.replyHooks {
				h.onItems(items)
			}
		}
		// 标记：到此为止的 items 都已通过 hooks 推送过，避免 completeFromResponse 补推时重复。
		s.pushedItems = len(s.reply.Items)
	}
}

// pendingItemAsMapLocked 把"进行中"的 pendingItem 渲染成一个临时 item map(形态与
// flushPendingItemLocked 一致),用于在 block 收尾前把 partial 文本写进 reply.json。
// 不清空 pendingItem。空内容返回 nil。caller 必须持有 s.mu。
func (s *aiGatewayAuditSession) pendingItemAsMapLocked(nextID int) map[string]interface{} {
	pi := s.pendingItem
	if pi == nil {
		return nil
	}
	switch pi.Kind {
	case "thinking":
		if strings.TrimSpace(pi.Thinking) == "" {
			return nil
		}
		return map[string]interface{}{"id": nextID, "type": "thinking", "thinking": pi.Thinking}
	case "text":
		if strings.TrimSpace(pi.Text) == "" {
			return nil
		}
		return map[string]interface{}{"id": nextID, "type": "text", "text": pi.Text}
	case "tool_use":
		if pi.ToolID == "" && pi.ToolName == "" {
			return nil
		}
		var input interface{}
		if pi.Arguments != "" {
			if err := json.Unmarshal([]byte(pi.Arguments), &input); err != nil {
				input = pi.Arguments
			}
		}
		exposedToolID := pi.OutputToolID
		if exposedToolID == "" {
			exposedToolID = pi.ToolID
		}
		return map[string]interface{}{"id": nextID, "type": "tool_use", "tool_id": exposedToolID, "name": pi.ToolName, "input": input}
	}
	return nil
}

// writeLiveReplySnapshotLocked 流式写盘:把已完成 Items + 进行中 pendingItem(作为
// 临时末尾 item)一起写进 reply.json,这样 partial 文本逐 delta 可见(前端流式)。
// 关键:只在写出的副本里追加 provisional item,绝不污染 s.reply.Items(下次 flush
// 会用同一个 id append 真正定稿的 item,文件整体覆盖,不会重复)。两条 SSE 路径
// (网关 deepseek / 非网关 MITM)都经 applyStreamEventsLocked 调用本函数。
func (s *aiGatewayAuditSession) writeLiveReplySnapshotLocked() {
	if s.auxiliary {
		return
	}
	live := s.reply
	if item := s.pendingItemAsMapLocked(len(s.reply.Items) + 1); item != nil {
		items := make([]map[string]interface{}, len(s.reply.Items), len(s.reply.Items)+1)
		copy(items, s.reply.Items)
		live.Items = append(items, item)
	}
	if err := aiGatewayWriteReplySnapshot(s.agentID, live); err != nil {
		log.Printf("[ai-gateway] live reply snapshot (pending) failed for %s: %v", s.agentID, err)
	}
}

// switchPendingItemLocked 在 stream_kind 切换或 tool 切换时调用：
// 如果 pendingItem 跟新 kind 不一致（或同 kind 但确实是不同的 tool），先 flush 旧的，开一个新的。
// 注意：Anthropic content_block_delta / OpenAI Chat tool_calls delta 中后续 chunks 不带 ToolID，
// 所以 toolID 为空时视作当前 pending tool 的延续，不切换。
func (s *aiGatewayAuditSession) switchPendingItemLocked(kind, toolID, toolName string) {
	if s.pendingItem != nil && s.pendingItem.Kind == kind {
		switch kind {
		case "thinking", "text":
			return
		case "tool_use":
			// 空 toolID = delta 续传，视为当前 tool。
			// 同 toolID = 同一个 tool。
			// 不同非空 toolID = 真切换。
			if toolID == "" || toolID == s.pendingItem.ToolID {
				return
			}
		}
	}
	if s.pendingItem != nil {
		s.flushPendingItemLocked()
	}
	s.pendingItem = &aiGatewayPendingItem{Kind: kind, ToolID: toolID, ToolName: toolName}
}

// aiGatewayStreamUsageMap pulls the usage object out of a streaming SSE payload.
// Anthropic puts it on message_start as message.usage (input + cache tokens are
// known up-front) and on message_delta as usage (output tokens). OpenAI-style
// streams carry a top-level usage on the final chunk.
func aiGatewayStreamUsageMap(payload map[string]interface{}) map[string]interface{} {
	if msg, ok := payload["message"].(map[string]interface{}); ok {
		if u, ok := msg["usage"].(map[string]interface{}); ok {
			return u
		}
	}
	if u, ok := payload["usage"].(map[string]interface{}); ok {
		return u
	}
	return nil
}

// applyStreamUsageLocked records input + cache tokens from a streaming usage
// payload onto s.reply IN REAL TIME, so reply.json (and anything that reads it,
// like the usage-analysis report) reflects cache hits the moment message_start
// arrives — instead of only at turn completion. Output tokens are intentionally
// left to completeFromResponse (it accumulates them) to avoid double counting.
// Returns true if anything changed. Caller must hold s.mu.
func (s *aiGatewayAuditSession) applyStreamUsageLocked(payload map[string]interface{}) bool {
	u := aiGatewayStreamUsageMap(payload)
	if u == nil {
		return false
	}
	inTok, _, _ := aiGatewayUsageTotals(u)
	cacheCreate, cacheRead := aiGatewayCacheTokens(u)
	changed := false
	if inTok > 0 && inTok != s.reply.InputTokens {
		s.reply.InputTokens = inTok
		changed = true
	}
	if (cacheCreate > 0 || cacheRead > 0) && (cacheCreate != s.reply.CacheCreationInputTokens || cacheRead != s.reply.CacheReadInputTokens) {
		s.reply.CacheCreationInputTokens = cacheCreate
		s.reply.CacheReadInputTokens = cacheRead
		changed = true
	}
	if changed {
		s.reply.TotalTokens = s.reply.InputTokens + s.reply.OutputTokens
		s.reply.Usage = aiGatewayCloneAnyMap(u)
		s.reply.LastUsage = aiGatewayCloneAnyMap(u)
	}
	return changed
}

func (s *aiGatewayAuditSession) applyStreamEventsLocked(events []aiGatewayReplyEvent) *ChatEvent {
	if s == nil || len(events) == 0 {
		return nil
	}
	changed := false
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	for _, event := range events {
		payload := aiGatewayMap(event.Payload)
		switch event.Kind {
		case "sse":
			streamKind := aiGatewayString(payload["stream_kind"])
			// content 必须用 RawString —— SSE chunk 内容前后的空格是有意义的，
			// 不能 trim（不然 " user"+ " has" 拼成 "userhas"）。
			content := aiGatewayRawString(payload["content"])
			if content == "" {
				continue
			}
			switch streamKind {
			case "thinking":
				s.reply.Thinking += content
				s.switchPendingItemLocked("thinking", "", "")
				s.pendingItem.Thinking += content
				changed = true
			case "answer":
				s.reply.Answer += content
				s.switchPendingItemLocked("text", "", "")
				s.pendingItem.Text += content
				changed = true
			}
		case "tool_call", "web_search":
			toolID := strings.TrimSpace(aiGatewayString(payload["tool_id"]))
			// toolName 直接用 payload 中的真实值，不要用 event.Kind 作 fallback
			// （event.Kind 是 cicy 内部分类 "tool_call" / "web_search"，不是真实工具名）。
			toolName := strings.TrimSpace(aiGatewayString(payload["tool_name"]))
			if toolName == "" && event.Kind == "web_search" {
				toolName = "web_search_call"
			}
			// arguments 必须用 RawString — partial_json 中前后空格是 JSON 语法的一部分。
			arguments := aiGatewayRawString(payload["arguments"])
			if aiGatewayUpsertStreamToolCall(&s.reply.ToolCalls, aiGatewayToolCall{
				ToolID:    toolID,
				ToolName:  toolName,
				Arguments: arguments,
			}) {
				changed = true
			}
			// 同步累积到 pendingItem（区分 tool by toolID；空 toolID 时按 toolName 区分）
			s.switchPendingItemLocked("tool_use", toolID, toolName)
			// 收到真实 toolName 时总是更新（解决 Codex 第一个 delta 不带 name、
			// 后续 output_item.added 才带 name 的情况）。
			if toolName != "" {
				s.pendingItem.ToolName = toolName
			}
			if toolID != "" && s.pendingItem.ToolID == "" {
				s.pendingItem.ToolID = toolID
			}
			// output_tool_id（=current.json 中的 tool_use_id；Codex=call_xxx）一旦有值就记录。
			if outputToolID := aiGatewayString(payload["output_tool_id"]); outputToolID != "" && s.pendingItem.OutputToolID == "" {
				s.pendingItem.OutputToolID = outputToolID
			}
			// .done 事件携带的 arguments 是已累积的 final 值，需要 replace 而非 append。
			eventType := aiGatewayString(payload["type"])
			isCumulative := strings.HasSuffix(eventType, ".done")
			if isCumulative {
				if arguments != "" {
					s.pendingItem.Arguments = arguments
				}
			} else {
				s.pendingItem.Arguments += arguments
			}
		}
	}
	if !changed {
		return nil
	}
	if len(s.reply.HTTPRequests) > 0 {
		requestSpan := &s.reply.HTTPRequests[0]
		requestSpan.UpdatedAt = updatedAt
		requestSpan.ThinkingPreview = aiGatewayPreviewText(s.reply.Thinking, 180)
		requestSpan.AnswerPreview = aiGatewayPreviewText(s.reply.Answer, 220)
		requestSpan.ToolCallCount = len(s.reply.ToolCalls)
	}
	s.current.UpdatedAt = updatedAt
	s.reply.UpdatedAt = updatedAt
	statusMap := aiGatewayBuildStatusMap(s.current, s.reply)
	s.current.Status = statusMap.Primary
	s.reply.Status = statusMap.Primary
	s.reply.StatusMap = statusMap
	// 含进行中 pendingItem 的 partial → reply.json 逐 delta 增长(流式)。
	s.writeLiveReplySnapshotLocked()
	return s.broadcastStatusLocked()
}

func aiGatewayUpsertStreamToolCall(items *[]aiGatewayToolCall, call aiGatewayToolCall) bool {
	if items == nil {
		return false
	}
	toolID := strings.TrimSpace(call.ToolID)
	toolName := strings.TrimSpace(call.ToolName)
	arguments := call.Arguments
	if toolID == "" && toolName == "" && strings.TrimSpace(arguments) == "" {
		return false
	}
	for idx := range *items {
		existing := &(*items)[idx]
		if toolID != "" && strings.TrimSpace(existing.ToolID) == toolID {
			changed := false
			if toolName != "" && existing.ToolName != toolName {
				existing.ToolName = toolName
				changed = true
			}
			if arguments != "" && existing.Arguments != arguments {
				existing.Arguments = arguments
				changed = true
			}
			return changed
		}
	}
	*items = append(*items, call)
	return true
}

func (s *aiGatewayAuditSession) broadcastStatusLocked() *ChatEvent {
	if s == nil {
		return nil
	}
	status := strings.TrimSpace(aiGatewayFirstNonEmpty(s.reply.StatusMap.Primary, s.reply.Status, s.current.Status))
	if status == "" {
		status = "thinking"
	}
	if status == s.lastStatusPush {
		return nil
	}
	s.lastStatusPush = status
	return &ChatEvent{
		Type: "status_change",
		Data: M{
			"agent_id":     s.agentID,
			"status":       status,
			"status_label": agentInspectorStatusLabel(status),
			"status_map":   s.reply.StatusMap,
			"updated_at":   aiGatewayFirstNonEmpty(s.reply.UpdatedAt, s.current.UpdatedAt, time.Now().UTC().Format(time.RFC3339)),
		},
	}
}

type aiGatewayReplyEvent struct {
	Kind    string
	Payload interface{}
}

func aiGatewayReplyEventsFromStreamPayload(payload map[string]interface{}) []aiGatewayReplyEvent {
	events := []aiGatewayReplyEvent{}
	payloadType := aiGatewayString(payload["type"])
	if strings.HasPrefix(payloadType, "response.web_search_call.") {
		events = append(events, aiGatewayReplyEvent{
			Kind: "web_search",
			Payload: M{
				"type":    payloadType,
				"item_id": aiGatewayFirstNonEmpty(aiGatewayString(payload["item_id"]), aiGatewayString(payload["call_id"])),
				"status":  payloadType[strings.LastIndex(payloadType, ".")+1:],
				"payload": aiGatewayCloneJSONValue(payload),
			},
		})
	}
	// Codex Responses API：output_item.added 携带 function_call 的真实 name，
	// 但后续的 function_call_arguments.delta 不带 name。emit 一个 tool_call event
	// 让 stream live 处理（applyStreamEventsLocked）能拿到真实 tool name。
	//
	// tool_id 优先用 item.id (fc_xxx) 与 function_call_arguments.delta 的 item_id 对齐，
	// 否则同一个 tool 会因为 added/delta/done 用不同 ID（call_id vs item_id）被分裂成多条。
	if payloadType == "response.output_item.added" || payloadType == "response.output_item.done" {
		item := aiGatewayMap(payload["item"])
		itemType := aiGatewayString(item["type"])
		if itemType == "function_call" || itemType == "tool_use" || itemType == "custom_tool_call" {
			events = append(events, aiGatewayReplyEvent{
				Kind: "tool_call",
				Payload: M{
					"type":            payloadType,
					"tool_id":         aiGatewayFirstNonEmpty(aiGatewayString(item["id"]), aiGatewayString(item["call_id"])),
					"output_tool_id":  aiGatewayString(item["call_id"]),
					"tool_name":       aiGatewayString(item["name"]),
					"arguments":       aiGatewayString(item["arguments"]),
				},
			})
		}
	}
	if payloadType == "response.function_call_arguments.done" || payloadType == "response.custom_tool_call_input.done" {
		toolName := aiGatewayString(payload["name"])
		if payloadType == "response.custom_tool_call_input.done" && toolName == "" {
			toolName = "custom_tool_call"
		}
		arguments := aiGatewayString(payload["arguments"])
		if arguments == "" {
			arguments = aiGatewayJSONOrString(payload["input"])
		}
		events = append(events, aiGatewayReplyEvent{
			Kind: "tool_call",
			Payload: M{
				"type":            payloadType,
				"tool_id":         aiGatewayFirstNonEmpty(aiGatewayString(payload["item_id"]), aiGatewayString(payload["call_id"])),
				"output_tool_id":  aiGatewayString(payload["call_id"]),
				"tool_name":       toolName,
				"arguments":       arguments,
			},
		})
	}
	for _, delta := range aiGatewayExtractStreamDeltas(payload) {
		switch delta.Kind {
		case "thinking", "answer":
			events = append(events, aiGatewayReplyEvent{
				Kind: "sse",
				Payload: M{
					"type":        payloadType,
					"stream_kind": delta.Kind,
					"content":     delta.Content,
				},
			})
		case "tool_call":
			kind := "tool_call"
			if delta.ToolName == "web_search_call" {
				kind = "web_search"
			}
			events = append(events, aiGatewayReplyEvent{
				Kind: kind,
				Payload: M{
					"type":      payloadType,
					"tool_id":   delta.ToolID,
					"tool_name": delta.ToolName,
					"arguments": delta.Content,
				},
			})
		}
	}
	return events
}

func aiGatewayReplyDir(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "reply")
}

func aiGatewayOptionalStringList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	return []string{strings.TrimSpace(value)}
}

func aiGatewayBuildStatusMap(current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot) aiGatewayStatusMap {
	activeRequestCount := 0
	for _, request := range reply.HTTPRequests {
		if !request.Auxiliary && request.Status == "streaming" {
			activeRequestCount++
		}
	}
	hasCurrentActive := len(current.ActiveRequestIDs) > 0
	toolCount := len(reply.ToolCalls)
	hasThinking := strings.TrimSpace(reply.Thinking) != ""
	hasAnswer := strings.TrimSpace(reply.Answer) != ""
	failed := current.Status == "failed" || reply.Status == "failed"
	primary := "thinking"
	if failed {
		primary = "failed"
	} else if activeRequestCount > 0 || hasCurrentActive {
		if hasAnswer {
			primary = "streaming"
		} else if toolCount > 0 {
			primary = "working"
		} else if hasThinking {
			primary = "thinking"
		} else {
			primary = "thinking"
		}
	} else if aiGatewayStopReasonExpectsToolRound(reply.LastStopReason) {
		// No live HTTP, but the last round ended on a tool_use stop: the agent is
		// running that tool client-side and will be back with the result. The turn
		// is NOT done — keep it yellow ("working") through the gap instead of
		// flashing "completed" (green) until the next round opens.
		primary = "working"
	} else {
		primary = "completed"
	}
	return aiGatewayStatusMap{
		Primary: primary,
		Items: []aiGatewayStatusItem{
			{Kind: "thinking", Label: "Thinking 思考中", Active: hasThinking && (primary == "thinking" || primary == "working" || primary == "streaming"), Count: aiGatewayBoolInt(hasThinking)},
			{Kind: "tool_call", Label: "Working 工作中", Active: toolCount > 0 && (activeRequestCount > 0 || hasCurrentActive || primary == "working") && (primary == "working" || primary == "streaming"), Count: toolCount},
			{Kind: "http", Label: "HTTP", Active: activeRequestCount > 0, Count: activeRequestCount},
			{Kind: "streaming", Label: "Working 工作中", Active: hasAnswer && activeRequestCount > 0 && primary == "streaming", Count: aiGatewayBoolInt(hasAnswer && activeRequestCount > 0 && primary == "streaming")},
		},
	}
}

// aiGatewayStopReasonExpectsToolRound reports whether a response's terminal
// reason means the agent loop will continue with a tool round (so the turn is
// NOT done). Covers Anthropic (tool_use), OpenAI chat (tool_calls) and the
// Responses-inferred marker. Anything else (end_turn / stop / length / "") is a
// genuine end-of-turn.
func aiGatewayStopReasonExpectsToolRound(stopReason string) bool {
	switch strings.ToLower(strings.TrimSpace(stopReason)) {
	case "tool_use", "tool_calls", "function_call":
		return true
	default:
		return false
	}
}

func aiGatewayBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func aiGatewayFilterPrimaryRequests(items []aiGatewayRequestSpan) []aiGatewayRequestSpan {
	out := make([]aiGatewayRequestSpan, 0, len(items))
	for _, item := range items {
		if !item.Auxiliary {
			out = append(out, item)
		}
	}
	return out
}

func aiGatewayCloneHeader(header http.Header) map[string][]string {
	if len(header) == 0 {
		return map[string][]string{}
	}
	cloned := make(map[string][]string, len(header))
	for key, values := range header {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func aiGatewayParseResponse(headers http.Header, body []byte) aiGatewayParsedResponse {
	contentType := strings.ToLower(strings.TrimSpace(headers.Get("Content-Type")))
	trimmed := bytes.TrimSpace(body)
	// SSE detection: codex's ChatGPT-backend stream begins with an "event:" line
	// (not "data:") and the Content-Type may be absent through MITM, so match
	// either prefix in addition to the content type. Without this the whole SSE
	// is json.Unmarshal'd as one blob, fails, and usage/answer are lost.
	if strings.Contains(contentType, "text/event-stream") ||
		bytes.HasPrefix(trimmed, []byte("data:")) ||
		bytes.HasPrefix(trimmed, []byte("event:")) {
		return aiGatewayParseStreamResponse(body)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		text := strings.TrimSpace(string(body))
		if text == "" {
			return aiGatewayParsedResponse{Usage: map[string]interface{}{}}
		}
		return aiGatewayParsedResponse{Answer: text, Usage: map[string]interface{}{}}
	}
	parsed := aiGatewayExtractNonStreamResponse(payload)
	if len(parsed.Usage) == 0 {
		parsed.Usage = aiGatewayCloneAnyMap(aiGatewayMap(payload["usage"]))
	}
	parsed.Usage = aiGatewayNormalizeOpenAIUsage(parsed.Usage)
	return parsed
}

func aiGatewayParseStreamResponse(body []byte) aiGatewayParsedResponse {
	acc := &aiGatewayStreamAccumulator{
		toolCalls: map[string]*aiGatewayToolCall{},
		usage:     map[string]interface{}{},
	}
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	for _, line := range lines {
		eventType, payload := aiGatewayParseSSELine(line)
		if eventType != "data" || payload == nil {
			continue
		}
		// Extract deltas first - this may modify payload["usage"] for message_start/message_delta events
		deltas := aiGatewayExtractStreamDeltas(payload)
		// Now read usage after aiGatewayExtractStreamDeltas has processed the payload
		if usage := aiGatewayMap(payload["usage"]); len(usage) > 0 {
			acc.usage = aiGatewayMergeUsage(acc.usage, usage)
		}
		// Terminal reason: Anthropic carries it on message_delta.delta.stop_reason;
		// OpenAI on the final chunk's choices[].finish_reason. A "tool_use"/
		// "tool_calls" here means the agent loop continues with a tool round.
		if aiGatewayString(payload["type"]) == "message_delta" {
			if sr := strings.TrimSpace(aiGatewayString(aiGatewayMap(payload["delta"])["stop_reason"])); sr != "" {
				acc.stopReason = sr
			}
		}
		if choices := aiGatewaySlice(payload["choices"]); len(choices) > 0 {
			if fr := strings.TrimSpace(aiGatewayString(aiGatewayMap(choices[0])["finish_reason"])); fr != "" {
				acc.stopReason = fr
			}
		}
		if acc.handleResponsesEvent(payload) {
			continue
		}
		for _, delta := range deltas {
			switch delta.Kind {
			case "thinking":
				if delta.Content != "" {
					acc.thinkingParts = append(acc.thinkingParts, delta.Content)
				}
			case "answer":
				if delta.Content != "" {
					acc.answerParts = append(acc.answerParts, delta.Content)
				}
			case "tool_call":
				toolCall := acc.updateToolCall(delta.ToolID, delta.ToolName, delta.Content, delta.ToolIndex, false)
				if delta.ToolName != "" {
					toolCall.ToolName = delta.ToolName
				}
			}
		}
	}
	toolCalls := acc.toolCallsInOrder()
	stopReason := acc.stopReason
	// Responses (Codex) streams emit no stop_reason/finish_reason. Infer a tool
	// round from trailing tool calls with no final text answer — the model asked
	// for tools and will be back, so the turn isn't done.
	if stopReason == "" && len(toolCalls) > 0 && strings.TrimSpace(strings.Join(acc.answerParts, "")) == "" {
		stopReason = "tool_use"
	}
	return aiGatewayParsedResponse{
		Thinking:   strings.Join(acc.thinkingParts, ""),
		Answer:     strings.Join(acc.answerParts, ""),
		ToolCalls:  toolCalls,
		Usage:      aiGatewayNormalizeOpenAIUsage(aiGatewayCloneAnyMap(acc.usage)),
		StopReason: stopReason,
	}
}

// aiGatewayNormalizeOpenAIUsage flattens OpenAI-style cached-token reporting into
// the canonical Anthropic-shaped fields the rest of the pipeline (cache stats,
// hit-rate, display) understands. OpenAI Responses puts the cache hit at
// usage.input_tokens_details.cached_tokens (Chat: prompt_tokens_details), and
// its input_tokens already INCLUDES those cached tokens. Anthropic instead
// reports input_tokens = uncached-only + a separate cache_read_input_tokens. So
// we lift cached → cache_read_input_tokens and subtract it from input_tokens,
// making input_tokens uncached-only like Anthropic. No-op for Anthropic usage.
func aiGatewayNormalizeOpenAIUsage(usage map[string]interface{}) map[string]interface{} {
	if len(usage) == 0 {
		return usage
	}
	cached := 0
	if d := aiGatewayMap(usage["input_tokens_details"]); d != nil {
		cached = aiGatewayInt(d["cached_tokens"])
	}
	if cached == 0 {
		if d := aiGatewayMap(usage["prompt_tokens_details"]); d != nil {
			cached = aiGatewayInt(d["cached_tokens"])
		}
	}
	if cached <= 0 {
		return usage
	}
	// Don't double-apply if a canonical cache field is already present.
	if aiGatewayInt(usage["cache_read_input_tokens"]) > 0 {
		return usage
	}
	usage["cache_read_input_tokens"] = cached
	in := aiGatewayInt(usage["input_tokens"])
	if in == 0 {
		in = aiGatewayInt(usage["prompt_tokens"])
	}
	if in > cached {
		usage["input_tokens"] = in - cached
	}
	return usage
}

func (a *aiGatewayStreamAccumulator) handleResponsesEvent(payload map[string]interface{}) bool {
	payloadType := aiGatewayString(payload["type"])
	if !strings.HasPrefix(payloadType, "response.") {
		return false
	}
	switch payloadType {
	case "response.completed":
		response := aiGatewayMap(payload["response"])
		if len(response) > 0 {
			if usage := aiGatewayMap(response["usage"]); len(usage) > 0 {
				a.usage = aiGatewayMergeUsage(a.usage, usage)
			}
			a.mergeSnapshot(aiGatewayExtractNonStreamResponse(response))
		}
		return true
	case "response.output_item.added", "response.output_item.done":
		item := aiGatewayMap(payload["item"])
		if len(item) == 0 {
			return true
		}
		a.mergeOutputItem(item, strings.HasSuffix(payloadType, ".done"))
		return true
	case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
		toolName := aiGatewayString(payload["name"])
		if payloadType == "response.custom_tool_call_input.done" && toolName == "" {
			toolName = "custom_tool_call"
		}
		arguments := aiGatewayString(payload["arguments"])
		if arguments == "" {
			arguments = aiGatewayJSONOrString(payload["input"])
		}
		a.updateToolCall(aiGatewayFirstNonEmpty(aiGatewayString(payload["item_id"]), aiGatewayString(payload["call_id"])), toolName, arguments, aiGatewayOptionalInt(payload["output_index"]), true)
		return true
	case "response.web_search_call.in_progress", "response.web_search_call.searching", "response.web_search_call.completed":
		status := payloadType[strings.LastIndex(payloadType, ".")+1:]
		a.updateToolCall(
			aiGatewayFirstNonEmpty(aiGatewayString(payload["item_id"]), aiGatewayString(payload["call_id"])),
			"web_search_call",
			aiGatewayJSONString(M{"status": status}),
			aiGatewayOptionalInt(payload["output_index"]),
			true,
		)
		return true
	default:
		return false
	}
}

func (a *aiGatewayStreamAccumulator) mergeOutputItem(item map[string]interface{}, preferReplace bool) {
	itemType := aiGatewayString(item["type"])
	switch itemType {
	case "function_call", "tool_call", "custom_tool_call", "web_search_call", "shell_call", "apply_patch_call":
		snapshot := aiGatewayExtractNonStreamResponse(M{"output": []interface{}{item}})
		for _, toolCall := range snapshot.ToolCalls {
			index := (*int)(nil)
			a.updateToolCall(toolCall.ToolID, toolCall.ToolName, toolCall.Arguments, index, preferReplace)
		}
		return
	}
	snapshot := aiGatewayExtractNonStreamResponse(M{"output": []interface{}{item}})
	if strings.TrimSpace(snapshot.Thinking) != "" && strings.TrimSpace(strings.Join(a.thinkingParts, "")) == "" {
		a.thinkingParts = append(a.thinkingParts, snapshot.Thinking)
	}
	if strings.TrimSpace(snapshot.Answer) != "" && strings.TrimSpace(strings.Join(a.answerParts, "")) == "" {
		a.answerParts = append(a.answerParts, snapshot.Answer)
	}
}

func (a *aiGatewayStreamAccumulator) mergeSnapshot(snapshot aiGatewayParsedResponse) {
	if strings.TrimSpace(snapshot.Thinking) != "" && strings.TrimSpace(strings.Join(a.thinkingParts, "")) == "" {
		a.thinkingParts = append(a.thinkingParts, snapshot.Thinking)
	}
	if strings.TrimSpace(snapshot.Answer) != "" && strings.TrimSpace(strings.Join(a.answerParts, "")) == "" {
		a.answerParts = append(a.answerParts, snapshot.Answer)
	}
	for _, toolCall := range snapshot.ToolCalls {
		a.updateToolCall(toolCall.ToolID, toolCall.ToolName, toolCall.Arguments, nil, true)
	}
	if len(snapshot.Usage) > 0 && len(a.usage) == 0 {
		a.usage = aiGatewayCloneAnyMap(snapshot.Usage)
	}
}

func (a *aiGatewayStreamAccumulator) updateToolCall(toolID string, toolName string, arguments string, toolIndex *int, replaceArguments bool) *aiGatewayToolCall {
	// 协议差异：
	//  - Anthropic content_block_start 携带 ToolID + Index；后续 content_block_delta
	//    只带 Index、不带 ToolID。
	//  - OpenAI Chat Completions tool_calls 第一段 chunk 携带 ToolID + Index + ToolName；
	//    后续 chunks 只带 Index 和 arguments delta。
	// 两种协议都要求"带 ToolID 的 chunk"和"只带 Index 的 chunk"合并到同一条 entry。
	// 旧实现 key=toolID 优先，后续 chunks 无 ToolID 退到 key="index:N"，造成同一工具
	// 被分裂为两条 entry（一条只有 ToolID/ToolName / 一条只有 arguments）。
	//
	// 新策略：先按 ToolID 找、再按 "index:N" 找；任一 hit 即复用，并把另一种 key 注册为
	// 别名指向同一 entry，让后续事件用任一标识都能找回。
	var idKey string
	if toolID != "" {
		idKey = toolID
	}
	var indexKey string
	if toolIndex != nil {
		indexKey = fmt.Sprintf("index:%d", *toolIndex)
	}

	var (
		toolCall    *aiGatewayToolCall
		existingKey string
	)
	if idKey != "" {
		if tc, ok := a.toolCalls[idKey]; ok {
			toolCall = tc
			existingKey = idKey
		}
	}
	if toolCall == nil && indexKey != "" {
		if tc, ok := a.toolCalls[indexKey]; ok {
			toolCall = tc
			existingKey = indexKey
		}
	}

	if toolCall == nil {
		// 没找到，新建。primary key 优先用 ToolID（更稳定），其次 Index，最后 auto。
		primary := idKey
		if primary == "" {
			primary = indexKey
		}
		if primary == "" {
			primary = fmt.Sprintf("auto:%d", a.autoIndex)
			a.autoIndex++
		}
		toolCall = &aiGatewayToolCall{ToolID: toolID, ToolName: toolName, Arguments: ""}
		a.toolCalls[primary] = toolCall
		a.toolOrder = append(a.toolOrder, primary)
		existingKey = primary
	}

	// 把另一种标识注册为别名（指向同一 entry），便于后续事件用任一 key 找到。
	if idKey != "" && idKey != existingKey {
		if _, ok := a.toolCalls[idKey]; !ok {
			a.toolCalls[idKey] = toolCall
		}
	}
	if indexKey != "" && indexKey != existingKey {
		if _, ok := a.toolCalls[indexKey]; !ok {
			a.toolCalls[indexKey] = toolCall
		}
	}

	// 字段补全
	if toolID != "" && toolCall.ToolID == "" {
		toolCall.ToolID = toolID
	}
	if toolName != "" {
		toolCall.ToolName = toolName
	}
	if arguments == "" {
		return toolCall
	}
	if replaceArguments || toolCall.Arguments == "" {
		toolCall.Arguments = arguments
		return toolCall
	}
	if toolCall.Arguments != arguments {
		toolCall.Arguments += arguments
	}
	return toolCall
}

func (a *aiGatewayStreamAccumulator) toolCallsInOrder() []aiGatewayToolCall {
	items := make([]aiGatewayToolCall, 0, len(a.toolOrder))
	for _, key := range a.toolOrder {
		if toolCall := a.toolCalls[key]; toolCall != nil {
			items = append(items, *toolCall)
		}
	}
	return items
}

func aiGatewayParseSSELine(line string) (string, map[string]interface{}) {
	line = strings.TrimRight(line, "\r")
	if line == "" || strings.HasPrefix(line, ":") {
		return "ping", nil
	}
	if !strings.HasPrefix(line, "data:") {
		return "unknown", nil
	}
	payloadText := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payloadText == "[DONE]" {
		return "done", nil
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		return "data", nil
	}
	return "data", payload
}

func aiGatewayExtractStreamDeltas(payload map[string]interface{}) []aiGatewayStreamDelta {
	if len(payload) == 0 {
		return nil
	}
	payloadType := aiGatewayString(payload["type"])
	switch payloadType {
	case "message_start":
		if message := aiGatewayMap(payload["message"]); len(message) > 0 {
			if usage := aiGatewayMap(message["usage"]); len(usage) > 0 {
				payload["usage"] = aiGatewayMergeUsage(aiGatewayMap(payload["usage"]), usage)
			}
		}
		return nil
	case "message_delta":
		if usage := aiGatewayMap(payload["usage"]); len(usage) > 0 {
			payload["usage"] = aiGatewayMergeUsage(map[string]interface{}{}, usage)
		}
		return nil
	case "response.output_text.delta":
		if text := aiGatewayRawString(payload["delta"]); text != "" {
			return []aiGatewayStreamDelta{{Kind: "answer", Content: text}}
		}
		return nil
	case "response.reasoning_summary_text.delta":
		if text := aiGatewayRawString(payload["delta"]); text != "" {
			return []aiGatewayStreamDelta{{Kind: "thinking", Content: text}}
		}
		return nil
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		toolName := aiGatewayString(payload["name"])
		if payloadType == "response.custom_tool_call_input.delta" && toolName == "" {
			toolName = "custom_tool_call"
		}
		return []aiGatewayStreamDelta{{
			Kind:      "tool_call",
			Content:   aiGatewayRawString(payload["delta"]),
			ToolID:    aiGatewayFirstNonEmpty(aiGatewayString(payload["item_id"]), aiGatewayString(payload["call_id"])),
			ToolName:  toolName,
			ToolIndex: aiGatewayOptionalInt(payload["output_index"]),
		}}
	case "content_block_start":
		// payload.index 是 Anthropic content block 索引，必须传到 ToolIndex —
		// 后续 content_block_delta 只带 index 不带 ToolID，靠它把同一 tool_use 的
		// 多个 input_json_delta chunks 合并到同一个 tool entry。
		blockIndex := aiGatewayOptionalInt(payload["index"])
		contentBlock := aiGatewayMap(payload["content_block"])
		if aiGatewayString(contentBlock["type"]) == "tool_use" {
			return []aiGatewayStreamDelta{{
				Kind:      "tool_call",
				ToolID:    aiGatewayString(contentBlock["id"]),
				ToolName:  aiGatewayString(contentBlock["name"]),
				ToolIndex: blockIndex,
			}}
		}
		return nil
	case "content_block_delta":
		blockIndex := aiGatewayOptionalInt(payload["index"])
		delta := aiGatewayMap(payload["delta"])
		switch aiGatewayString(delta["type"]) {
		case "thinking_delta":
			return []aiGatewayStreamDelta{{Kind: "thinking", Content: aiGatewayRawString(delta["thinking"])}}
		case "text_delta":
			return []aiGatewayStreamDelta{{Kind: "answer", Content: aiGatewayRawString(delta["text"])}}
		case "input_json_delta":
			// Anthropic 协议字段是 partial_json；input_json 是历史 fallback。
			// 必须带 blockIndex，否则各 chunk 没法关联到同一 tool_use。
			content := aiGatewayRawString(delta["partial_json"])
			if content == "" {
				content = aiGatewayRawString(delta["input_json"])
			}
			return []aiGatewayStreamDelta{{Kind: "tool_call", Content: content, ToolIndex: blockIndex}}
		default:
			return nil
		}
	}

	choices := aiGatewaySlice(payload["choices"])
	if len(choices) == 0 {
		return nil
	}
	firstChoice := aiGatewayMap(choices[0])
	delta := aiGatewayMap(firstChoice["delta"])
	deltas := []aiGatewayStreamDelta{}
	if reasoning := aiGatewayRawString(delta["reasoning_content"]); reasoning != "" {
		deltas = append(deltas, aiGatewayStreamDelta{Kind: "thinking", Content: reasoning})
	}
	if content := aiGatewayRawString(delta["content"]); content != "" {
		deltas = append(deltas, aiGatewayStreamDelta{Kind: "answer", Content: content})
	}
	for _, rawToolCall := range aiGatewaySlice(delta["tool_calls"]) {
		toolCall := aiGatewayMap(rawToolCall)
		function := aiGatewayMap(toolCall["function"])
		deltas = append(deltas, aiGatewayStreamDelta{
			Kind:      "tool_call",
			Content:   aiGatewayRawString(function["arguments"]),
			ToolID:    aiGatewayString(toolCall["id"]),
			ToolName:  aiGatewayString(function["name"]),
			ToolIndex: aiGatewayOptionalInt(toolCall["index"]),
		})
	}
	return deltas
}

func aiGatewayExtractNonStreamResponse(payload map[string]interface{}) aiGatewayParsedResponse {
	result := aiGatewayParsedResponse{
		Thinking:  "",
		Answer:    "",
		ToolCalls: []aiGatewayToolCall{},
		Usage:     aiGatewayCloneAnyMap(aiGatewayMap(payload["usage"])),
	}

	choices := aiGatewaySlice(payload["choices"])
	if len(choices) > 0 {
		message := aiGatewayMap(aiGatewayMap(choices[0])["message"])
		result.Thinking = aiGatewayString(message["reasoning_content"])
		result.Answer = aiGatewayString(message["content"])
		result.StopReason = strings.TrimSpace(aiGatewayString(aiGatewayMap(choices[0])["finish_reason"]))
		for _, rawToolCall := range aiGatewaySlice(message["tool_calls"]) {
			toolCall := aiGatewayMap(rawToolCall)
			function := aiGatewayMap(toolCall["function"])
			result.ToolCalls = append(result.ToolCalls, aiGatewayToolCall{
				ToolID:    aiGatewayString(toolCall["id"]),
				ToolName:  aiGatewayString(function["name"]),
				Arguments: aiGatewayString(function["arguments"]),
			})
		}
		return result
	}

	contentBlocks := aiGatewaySlice(payload["content"])
	if len(contentBlocks) > 0 {
		for _, rawBlock := range contentBlocks {
			block := aiGatewayMap(rawBlock)
			switch aiGatewayString(block["type"]) {
			case "thinking":
				result.Thinking += aiGatewayString(block["thinking"])
			case "text":
				result.Answer += aiGatewayString(block["text"])
			case "tool_use":
				result.ToolCalls = append(result.ToolCalls, aiGatewayToolCall{
					ToolID:    aiGatewayString(block["id"]),
					ToolName:  aiGatewayString(block["name"]),
					Arguments: aiGatewayJSONString(block["input"]),
				})
			}
		}
		if strings.TrimSpace(result.Thinking) != "" || strings.TrimSpace(result.Answer) != "" || len(result.ToolCalls) > 0 {
			result.StopReason = strings.TrimSpace(aiGatewayString(payload["stop_reason"]))
			return result
		}
	}

	outputItems := aiGatewaySlice(payload["output"])
	if len(outputItems) == 0 {
		return result
	}
	thinkingParts := []string{}
	answerParts := []string{}
	for _, rawItem := range outputItems {
		item := aiGatewayMap(rawItem)
		itemType := aiGatewayString(item["type"])
		if itemType == "reasoning" {
			for _, part := range aiGatewaySlice(item["summary"]) {
				if text := strings.TrimSpace(aiGatewayContentPartToText(part)); text != "" {
					thinkingParts = append(thinkingParts, text)
				}
			}
		}
		for _, rawPart := range aiGatewaySlice(item["content"]) {
			part := aiGatewayMap(rawPart)
			partType := aiGatewayString(part["type"])
			text := strings.TrimSpace(aiGatewayContentPartToText(part))
			if (partType == "output_text" || partType == "text") && text != "" {
				answerParts = append(answerParts, text)
			}
		}
		switch itemType {
		case "function_call", "tool_call":
			result.ToolCalls = append(result.ToolCalls, aiGatewayToolCall{
				ToolID:    aiGatewayFirstNonEmpty(aiGatewayString(item["call_id"]), aiGatewayString(item["id"])),
				ToolName:  aiGatewayString(item["name"]),
				Arguments: aiGatewayString(item["arguments"]),
			})
		case "custom_tool_call":
			result.ToolCalls = append(result.ToolCalls, aiGatewayToolCall{
				ToolID:    aiGatewayFirstNonEmpty(aiGatewayString(item["call_id"]), aiGatewayString(item["id"])),
				ToolName:  aiGatewayFirstNonEmpty(aiGatewayString(item["name"]), "custom_tool_call"),
				Arguments: aiGatewayFirstNonEmpty(aiGatewayString(item["input"]), aiGatewayString(item["arguments"])),
			})
		case "web_search_call", "shell_call", "apply_patch_call":
			arguments := item["arguments"]
			if arguments == nil {
				arguments = item["input"]
			}
			if arguments == nil {
				arguments = item["action"]
			}
			if arguments == nil {
				arguments = item["operation"]
			}
			if arguments == nil {
				arguments = item["query"]
			}
			result.ToolCalls = append(result.ToolCalls, aiGatewayToolCall{
				ToolID:    aiGatewayFirstNonEmpty(aiGatewayString(item["call_id"]), aiGatewayString(item["id"])),
				ToolName:  itemType,
				Arguments: aiGatewayJSONOrString(arguments),
			})
		}
	}
	if len(thinkingParts) > 0 {
		result.Thinking = strings.Join(thinkingParts, "\n")
	}
	if len(answerParts) > 0 {
		result.Answer = strings.Join(answerParts, "\n")
	}
	// Responses (Codex) has no stop_reason field; trailing tool calls with no
	// final text mean a tool round is coming, so the turn isn't done.
	if result.StopReason == "" && len(result.ToolCalls) > 0 && strings.TrimSpace(result.Answer) == "" {
		result.StopReason = "tool_use"
	}
	return result
}

func aiGatewayContentPartToText(part interface{}) string {
	switch value := part.(type) {
	case string:
		return value
	case map[string]interface{}:
		if text := aiGatewayRawString(value["input_text"]); text != "" {
			return text
		}
		if text := aiGatewayRawString(value["output_text"]); text != "" {
			return text
		}
		if text := aiGatewayRawString(value["text"]); text != "" {
			return text
		}
		if text := aiGatewayRawString(value["content"]); text != "" {
			return text
		}
		if nested := aiGatewaySlice(value["content"]); len(nested) > 0 {
			parts := make([]string, 0, len(nested))
			for _, item := range nested {
				if text := aiGatewayContentPartToText(item); text != "" {
					parts = append(parts, text)
				}
			}
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

func aiGatewayExtractQuestion(body map[string]interface{}) string {
	messages := aiGatewayExtractMessages(body)
	if question := aiGatewayQuestionFromMessages(messages); question != "" {
		return aiGatewaySanitizeUserQuestion(question)
	}

	inputItems := aiGatewayExtractInputItems(body)
	if question := aiGatewayQuestionFromItems(inputItems); question != "" {
		return aiGatewaySanitizeUserQuestion(question)
	}

	if rawInput := body["input"]; rawInput != nil {
		if text := strings.TrimSpace(aiGatewayString(rawInput)); text != "" {
			return aiGatewaySanitizeUserQuestion(text)
		}
	}
	for _, key := range []string{"prompt", "question", "query", "message"} {
		if text := strings.TrimSpace(aiGatewayString(body[key])); text != "" {
			return aiGatewaySanitizeUserQuestion(text)
		}
	}
	return ""
}

// aiGatewayIsToolContinuation 判断当前请求是不是 agent 内部的 tool 续传（vs 用户主动新 q）。
// 通过看 body 里 messages/input 的最后一条记录的语义判断：
//   - Anthropic: 最后一条 role=user 且 content 数组里含 tool_result block → 续传
//   - OpenAI Chat: 最后一条 role=tool → 续传
//   - Codex Responses: 最后一条 input item type=function_call_output / tool_result / computer_call_output → 续传
// 其余情况都视为用户新 q（包括最后一条 role=user 是纯文本、第一条请求等）。
func aiGatewayIsToolContinuation(body map[string]interface{}) bool {
	if body == nil {
		return false
	}
	// messages 数组（Anthropic / OpenAI Chat 共用）
	if messages := aiGatewayExtractMessages(body); len(messages) > 0 {
		last := messages[len(messages)-1]
		role := strings.ToLower(strings.TrimSpace(aiGatewayString(last["role"])))
		switch role {
		case "tool", "function":
			// OpenAI Chat 风格：tool_result 是单独的 message，role=tool
			return true
		case "user":
			// Anthropic 风格：tool_result 装在 role=user 的 content 数组里
			if content, ok := last["content"].([]interface{}); ok {
				for _, block := range content {
					if m := aiGatewayMap(block); len(m) > 0 {
						switch strings.ToLower(strings.TrimSpace(aiGatewayString(m["type"]))) {
						case "tool_result", "tool_use_result", "function_call_output":
							return true
						}
					}
				}
			}
		}
		return false
	}
	// Codex Responses input 数组
	if items := aiGatewayExtractInputItems(body); len(items) > 0 {
		last := aiGatewayMap(items[len(items)-1])
		if len(last) == 0 {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(aiGatewayString(last["type"]))) {
		case "function_call_output", "tool_result", "tool_use_result", "computer_call_output",
			"shell_call_output", "local_shell_call_output", "apply_patch_call_output",
			// codex emits apply_patch (and other built-in tools) as a
			// `custom_tool_call`; its result is `custom_tool_call_output`. Without
			// this case the continuation request after an apply_patch is misread as
			// a NEW turn, wiping the accumulated reply.Items mid-turn (the items
			// then re-accumulate only from that point → lost/“reordered” history).
			"custom_tool_call_output":
			return true
		}
		return false
	}
	return false
}

// aiGatewayIsAuxiliaryRequest 判断当前请求是否是辅助调用（不应污染主 reply.json）。
func aiGatewayIsAuxiliaryRequest(question string, body map[string]interface{}) bool {
	return aiGatewayAuxiliaryKind(question, body) != ""
}

// aiGatewayAuxiliaryKind 识别 SUGGESTION MODE / 标题生成等辅助调用并返回其类型。
// 这类调用不应该污染主 reply.json / current.json；其中 "title" 还会单独落到 title.json。
//
// 返回值：
//   - ""           主请求（用户可见的那一轮）
//   - "suggestion" Claude Code 的 next-step 预测 / metadata 标记为 suggestion
//   - "title"      CLI（opencode 等）在新会话首条消息后另发的"生成会话标题"请求
func aiGatewayAuxiliaryKind(question string, body map[string]interface{}) string {
	if q := strings.TrimSpace(question); q != "" {
		if strings.HasPrefix(strings.ToUpper(q), "[SUGGESTION MODE") {
			return "suggestion"
		}
	}
	if body == nil {
		return ""
	}
	// Claude Code's quota / availability health-check: the CLI fires a 1-token
	// ping (the literal "quota" prompt) to probe Claude Max quota. max_tokens==1
	// is never a real conversation turn — without this gate the probe lands in
	// current/reply.json and renders as a stray "quota" turn in history.
	if _, ok := body["max_tokens"]; ok && aiGatewayInt(body["max_tokens"]) == 1 {
		return "probe"
	}
	if meta := aiGatewayMap(body["metadata"]); len(meta) > 0 {
		for _, key := range []string{"purpose", "kind", "category"} {
			val := strings.ToLower(strings.TrimSpace(aiGatewayString(meta[key])))
			if strings.Contains(val, "suggestion") {
				return "suggestion"
			}
		}
	}
	if aiGatewayLooksLikeTitleRequest(body) {
		return "title"
	}
	return ""
}

// aiGatewayLooksLikeTitleRequest 识别"会话标题生成"请求。opencode 等 CLI 在新会话
// 首条消息后会另发一个独立请求，其 system 指令就是 "You are a title generator..."。
// 这类请求和主回答请求并发，共享同一 agent 的 current.json / reply.json 会互相覆盖，
// 所以单独识别出来落到 title.json。
//
// 关键：只在 **system 指令**上做前缀锚定匹配，绝不扫 messages/user 内容。否则一段
// 正常对话只要文本里**提到**"you are a title generator"（比如在讨论这个功能本身），
// 就会被误判成标题请求，导致它的主 reply.json / current.json 被跳过——这正是之前
// 把网关下的 current.json/reply.json"改坏"的根因。
func aiGatewayLooksLikeTitleRequest(body map[string]interface{}) bool {
	if body == nil {
		return false
	}
	sys := strings.ToLower(strings.TrimSpace(aiGatewayTitleSystemInstruction(body)))
	return strings.HasPrefix(sys, "you are a title generator")
}

// aiGatewayTitleSystemInstruction 取出请求的 system 指令文本（仅 system，不含对话内容）。
// 兼容 Anthropic 顶层 system（string 或 block 数组）与 OpenAI chat 的首条 system 消息。
func aiGatewayTitleSystemInstruction(body map[string]interface{}) string {
	if v, ok := body["system"]; ok && v != nil {
		if t := strings.TrimSpace(aiGatewayFlattenPromptValue(v)); t != "" {
			return t
		}
	}
	for _, m := range aiGatewayExtractMessages(body) {
		if strings.EqualFold(strings.TrimSpace(aiGatewayString(m["role"])), "system") {
			return strings.TrimSpace(aiGatewayFlattenPromptValue(m["content"]))
		}
	}
	return ""
}

// aiGatewayWriteTitleFile 把会话标题生成的结果落到 agent 的 title.json（独立于
// 主 reply.json）。title 取自解析出的 answer 文本。
func aiGatewayWriteTitleFile(agentID, turnID, model, status, title, updatedAt string) {
	title = strings.TrimSpace(title)
	payload := M{
		"turn_id":    turnID,
		"model":      model,
		"status":     status,
		"title":      title,
		"updated_at": updatedAt,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(aiGatewayHistoryDir(agentID), "title.json")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		log.Printf("[ai-gateway] write title.json failed for %s: %v", agentID, err)
	}
}

func aiGatewayExtractRequestHistory(body map[string]interface{}) (string, interface{}) {
	if body == nil {
		return "", nil
	}
	if messages, ok := body["messages"]; ok {
		if list := aiGatewaySlice(messages); len(list) > 0 {
			return "messages", messages
		}
	}
	if inputItems, ok := body["input"]; ok {
		if list := aiGatewaySlice(inputItems); len(list) > 0 {
			return "input", inputItems
		}
		if inputItems != nil {
			return "input", inputItems
		}
	}
	return "", nil
}

func aiGatewayExtractMessages(body map[string]interface{}) []map[string]interface{} {
	items := aiGatewaySlice(body["messages"])
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		if m := aiGatewayMap(item); len(m) > 0 {
			out = append(out, m)
		}
	}
	return out
}

func aiGatewayExtractInputItems(body map[string]interface{}) []interface{} {
	if body == nil {
		return nil
	}
	inputItems := body["input"]
	if inputItems == nil {
		return nil
	}
	if list := aiGatewaySlice(inputItems); len(list) > 0 {
		return list
	}
	return []interface{}{inputItems}
}

func aiGatewayCurrentHistoryItems(current aiGatewayCurrentSnapshot) []interface{} {
	body := aiGatewayMap(current.Body)
	if len(body) == 0 {
		return nil
	}
	if messages := body["messages"]; messages != nil {
		if list := aiGatewaySlice(messages); len(list) > 0 {
			return list
		}
	}
	if input := body["input"]; input != nil {
		if list := aiGatewaySlice(input); len(list) > 0 {
			return list
		}
		return []interface{}{input}
	}
	return nil
}

func aiGatewayHeaderFirst(headers http.Header, key string) string {
	if len(headers) == 0 {
		return ""
	}
	for _, value := range headers.Values(key) {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func aiGatewayExtractCodexTurnMetadata(headers http.Header) aiGatewayCodexTurnMetadata {
	raw := aiGatewayHeaderFirst(headers, "X-Codex-Turn-Metadata")
	if raw == "" {
		return aiGatewayCodexTurnMetadata{}
	}
	var meta aiGatewayCodexTurnMetadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return aiGatewayCodexTurnMetadata{}
	}
	meta.SessionID = strings.TrimSpace(meta.SessionID)
	meta.TurnID = strings.TrimSpace(meta.TurnID)
	meta.ThreadSource = strings.TrimSpace(meta.ThreadSource)
	return meta
}

func aiGatewayExtractSessionIDFromHeaders(headers http.Header, meta aiGatewayCodexTurnMetadata) string {
	if meta.SessionID != "" {
		return meta.SessionID
	}
	for _, key := range []string{"X-Claude-Code-Session-Id", "Session_id", "Session-Id", "X-Window-Id", "X-Codex-Window-Id"} {
		value := strings.TrimSpace(aiGatewayHeaderFirst(headers, key))
		if value == "" {
			continue
		}
		if key == "X-Codex-Window-Id" {
			if head, _, ok := strings.Cut(value, ":"); ok && strings.TrimSpace(head) != "" {
				return strings.TrimSpace(head)
			}
		}
		return value
	}
	return ""
}

func aiGatewayExtractSessionIDFromBody(body map[string]interface{}) string {
	if len(body) == 0 {
		return ""
	}
	if metadata := aiGatewayMap(body["metadata"]); len(metadata) > 0 {
		if value := strings.TrimSpace(aiGatewayString(metadata["session_id"])); value != "" {
			return value
		}
		if rawUserID := strings.TrimSpace(aiGatewayString(metadata["user_id"])); rawUserID != "" {
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(rawUserID), &payload); err == nil {
				if value := strings.TrimSpace(aiGatewayString(payload["session_id"])); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func aiGatewayExtractConversationID(body map[string]interface{}) string {
	for _, key := range []string{"conversation_id", "session_id", "thread_id", "chat_id"} {
		if value := aiGatewayString(body[key]); value != "" {
			return value
		}
	}
	return ""
}

func aiGatewayExtractErrorText(responseBody []byte, responseErr error) string {
	if responseErr != nil {
		return strings.TrimSpace(responseErr.Error())
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(responseBody, &payload); err == nil {
		if detail := aiGatewayString(payload["detail"]); detail != "" {
			return detail
		}
		if errField := aiGatewayMap(payload["error"]); len(errField) > 0 {
			if message := aiGatewayString(errField["message"]); message != "" {
				return message
			}
		}
	}
	return strings.TrimSpace(string(responseBody))
}

func aiGatewayPreviewText(value string, limit int) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	compact := strings.Join(strings.Fields(value), " ")
	runes := []rune(compact)
	if len(runes) <= limit {
		return compact
	}
	return strings.TrimSpace(string(runes[:limit])) + "..."
}

func aiGatewayEstimateCostCredit(model string, usage map[string]interface{}) float64 {
	if strings.TrimSpace(model) == "" || len(usage) == 0 {
		return 0
	}
	// Price each input bucket at its own tier (cache reads are ~10× cheaper than
	// fresh input). `input_tokens` (raw key) is the FRESH count in Anthropic
	// usage, with cache read/write reported separately — see estimateModelCostTokens.
	fresh := aiGatewayTokenValue(usage, "input_tokens", "prompt_tokens", "inputTokens")
	cacheRead := aiGatewayTokenValue(usage, "cache_read_input_tokens", "cacheReadInputTokens")
	cacheWrite := aiGatewayTokenValue(usage, "cache_creation_input_tokens", "cacheCreationInputTokens")
	output := aiGatewayOutputTokenValue(usage)
	cost, _ := estimateModelCostTokens(model, fresh, cacheRead, cacheWrite, output)
	return cost
}

func aiGatewayLoadReplySnapshot(agentID string) aiGatewayReplySnapshot {
	if reply, err := aiGatewayReadReplySnapshotFile(agentID); err == nil {
		aiGatewayStoreLiveReplySnapshot(agentID, reply)
		reply.StatusMap = aiGatewayBuildStatusMap(aiGatewayCurrentSnapshot{}, reply)
		return reply
	}
	if reply, ok := aiGatewayGetLiveReplySnapshot(agentID); ok {
		return reply
	}
	reply := aiGatewayBuildReplySnapshotFromCurrent(agentID)
	if strings.TrimSpace(reply.TurnID) == "" &&
		strings.TrimSpace(reply.Status) == "" &&
		strings.TrimSpace(reply.Answer) == "" &&
		strings.TrimSpace(reply.Thinking) == "" &&
		len(reply.ToolCalls) == 0 {
		return reply
	}
	reply.StatusMap = aiGatewayBuildStatusMap(aiGatewayCurrentSnapshot{}, reply)
	return reply
}

func aiGatewayBuildReplySnapshotFromCurrent(agentID string) aiGatewayReplySnapshot {
	current, err := aiGatewayReadCurrentSnapshot(agentID)
	if err != nil {
		return aiGatewayReplySnapshot{}
	}
	if turns := agentInspectorBuildCurrentOnlyPersistedTurns(current); len(turns) > 0 {
		for i := len(turns) - 1; i >= 0; i-- {
			if !agentInspectorTurnHasAssistantActivity(turns[i]) {
				continue
			}
			reply := aiGatewayBuildReplySnapshotFromTurn(current, turns[i])
			if strings.TrimSpace(reply.TurnID) == "" {
				reply.TurnID = strings.TrimSpace(current.TurnID)
			}
			reply.StatusMap = aiGatewayBuildStatusMap(current, reply)
			return reply
		}
	}
	page, err := agentHistoryLoadPage(agentID, strings.TrimSpace(current.ConversationID), 1, 0)
	if err == nil && len(page.Items) > 0 {
		reply := aiGatewayBuildReplySnapshotFromTurn(current, page.Items[len(page.Items)-1])
		if strings.TrimSpace(reply.TurnID) == "" {
			reply.TurnID = strings.TrimSpace(current.TurnID)
		}
		reply.StatusMap = aiGatewayBuildStatusMap(current, reply)
		return reply
	}
	reply := aiGatewayReplySnapshot{
		TurnID:    strings.TrimSpace(current.TurnID),
		Status:    strings.TrimSpace(current.Status),
		StartedAt: strings.TrimSpace(current.StartedAt),
		UpdatedAt: strings.TrimSpace(current.UpdatedAt),
		Models:    aiGatewayOptionalStringList(current.Model),
	}
	reply.StatusMap = aiGatewayBuildStatusMap(current, reply)
	return reply
}

func aiGatewayBuildReplySnapshotFromTurn(current aiGatewayCurrentSnapshot, turn M) aiGatewayReplySnapshot {
	reply := aiGatewayReplySnapshot{
		TurnID:    strings.TrimSpace(current.TurnID),
		StartedAt: strings.TrimSpace(current.StartedAt),
		UpdatedAt: strings.TrimSpace(current.UpdatedAt),
		Models:    aiGatewayOptionalStringList(aiGatewayFirstNonEmpty(aiGatewayString(turn["model"]), current.Model)),
	}
	switch strings.ToLower(strings.TrimSpace(aiGatewayString(turn["status"]))) {
	case "thinking", "streaming", "working", "tool_call", "tool_use":
		reply.Status = strings.TrimSpace(aiGatewayString(turn["status"]))
	case "pending":
		reply.Status = "thinking"
	case "", "done", "text", "completed", "idle":
		reply.Status = "completed"
	default:
		reply.Status = strings.TrimSpace(aiGatewayString(turn["status"]))
	}
	if startTS := agentInspectorHistoryUnix(turn, "start_ts"); startTS > 0 {
		reply.StartedAt = time.Unix(startTS, 0).UTC().Format(time.RFC3339)
	}
	if ts := agentInspectorHistoryUnix(turn, "ts"); ts > 0 {
		reply.UpdatedAt = time.Unix(ts, 0).UTC().Format(time.RFC3339)
	}
	if credit := aiGatewayFloat(turn["credit"]); credit > 0 {
		reply.CostCredit = credit
	}
	reply.Answer = strings.TrimSpace(aiGatewayString(turn["a"]))
	var steps []M
	switch raw := turn["steps"].(type) {
	case []M:
		steps = raw
	case []interface{}:
		steps = make([]M, 0, len(raw))
		for _, item := range raw {
			if step := aiGatewayMap(item); len(step) > 0 {
				steps = append(steps, step)
			}
		}
	}
	for _, step := range steps {
		switch strings.ToLower(strings.TrimSpace(aiGatewayString(step["type"]))) {
		case "thinking":
			if text := strings.TrimSpace(aiGatewayString(step["text"])); text != "" {
				reply.Thinking = strings.TrimSpace(aiGatewayJoinUniqueText([]string{reply.Thinking, text}))
			}
		case "tool":
			for _, rawTool := range aiGatewaySlice(step["tools"]) {
				tool := aiGatewayMap(rawTool)
				if len(tool) == 0 {
					continue
				}
				reply.ToolCalls = append(reply.ToolCalls, aiGatewayToolCall{
					ToolID:    strings.TrimSpace(aiGatewayString(tool["tool_id"])),
					ToolName:  strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(tool["tool_name"]), aiGatewayString(tool["name"]))),
					Arguments: strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(tool["input"]), aiGatewayString(tool["arg"]))),
				})
			}
		}
	}
	reply.RequestCount = 1
	return reply
}

func aiGatewayReplyTotals(reply aiGatewayReplySnapshot) (int, int, int) {
	if reply.InputTokens > 0 || reply.OutputTokens > 0 || reply.TotalTokens > 0 {
		return reply.InputTokens, reply.OutputTokens, reply.TotalTokens
	}
	inputTokens, outputTokens, totalTokens := aiGatewayUsageTotals(reply.Usage)
	if inputTokens > 0 || outputTokens > 0 || totalTokens > 0 {
		return inputTokens, outputTokens, totalTokens
	}
	for _, span := range aiGatewayFilterPrimaryRequests(reply.HTTPRequests) {
		inputTokens += span.InputTokens
		outputTokens += span.OutputTokens
		totalTokens += span.TotalTokens
	}
	return inputTokens, outputTokens, totalTokens
}

func aiGatewayReplyCostCredit(reply aiGatewayReplySnapshot) float64 {
	if reply.CostCredit > 0 {
		return reply.CostCredit
	}
	var total float64
	for _, span := range aiGatewayFilterPrimaryRequests(reply.HTTPRequests) {
		total += span.CostCredit
	}
	return total
}

func aiGatewayInputTokenValue(usage map[string]interface{}) int {
	base := aiGatewayTokenValue(usage, "input_tokens", "prompt_tokens", "inputTokens")
	cacheCreate := aiGatewayTokenValue(usage, "cache_creation_input_tokens", "cacheCreationInputTokens")
	cacheRead := aiGatewayTokenValue(usage, "cache_read_input_tokens", "cacheReadInputTokens")
	return base + cacheCreate + cacheRead
}

func aiGatewayOutputTokenValue(usage map[string]interface{}) int {
	return aiGatewayTokenValue(usage, "output_tokens", "completion_tokens", "outputTokens")
}

func aiGatewayUsageTotals(usage map[string]interface{}) (int, int, int) {
	inputTokens := aiGatewayInputTokenValue(usage)
	outputTokens := aiGatewayOutputTokenValue(usage)
	totalTokens := aiGatewayTokenValue(usage, "total_tokens", "totalTokens")
	if totalTokens == 0 {
		totalTokens = inputTokens + outputTokens
	}
	return inputTokens, outputTokens, totalTokens
}

func aiGatewayCacheTokens(usage map[string]interface{}) (int, int) {
	cacheCreate := aiGatewayTokenValue(usage, "cache_creation_input_tokens", "cacheCreationInputTokens")
	cacheRead := aiGatewayTokenValue(usage, "cache_read_input_tokens", "cacheReadInputTokens")
	return cacheCreate, cacheRead
}

func aiGatewayTokenValue(usage map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		if value := aiGatewayInt(usage[key]); value > 0 {
			return value
		}
	}
	return 0
}

func aiGatewayMap(value interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	if out, ok := value.(map[string]interface{}); ok {
		return out
	}
	return nil
}

func aiGatewaySlice(value interface{}) []interface{} {
	if value == nil {
		return nil
	}
	if out, ok := value.([]interface{}); ok {
		return out
	}
	return nil
}

func aiGatewayString(value interface{}) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func aiGatewayRawString(value interface{}) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func aiGatewayInt(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
}

func aiGatewayFloat(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

func aiGatewayOptionalInt(value interface{}) *int {
	switch v := value.(type) {
	case int:
		out := v
		return &out
	case int64:
		out := int(v)
		return &out
	case float64:
		out := int(v)
		return &out
	case float32:
		out := int(v)
		return &out
	case json.Number:
		if parsed, err := v.Int64(); err == nil {
			out := int(parsed)
			return &out
		}
	}
	return nil
}

func aiGatewayIntPtr(value int) *int {
	return &value
}

func aiGatewayFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func aiGatewayCloneAnyMap(value map[string]interface{}) map[string]interface{} {
	if len(value) == 0 {
		return map[string]interface{}{}
	}
	cloned := make(map[string]interface{}, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func aiGatewayCloneJSONValue(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned interface{}
	if err := json.Unmarshal(body, &cloned); err != nil {
		return value
	}
	return cloned
}

func aiGatewayWithID(raw interface{}, id int) interface{} {
	item := aiGatewayMap(raw)
	if len(item) == 0 {
		return raw
	}
	next := aiGatewayCloneAnyMap(item)
	next["id"] = id
	return next
}

func aiGatewayAssignSequentialIDs(items []interface{}, base int) []interface{} {
	if len(items) == 0 {
		return items
	}
	out := make([]interface{}, 0, len(items))
	for i, raw := range items {
		out = append(out, aiGatewayWithID(raw, base+i+1))
	}
	return out
}

// aiGatewayContentText flattens a message's content into a canonical text key,
// ignoring block structure and cache_control. This is essential because the
// dispatcher attaches a cache_control breakpoint to the LAST message of each
// request (copy-on-write), turning its content from a plain string into a
// [{type:text,text,cache_control}] block — so the same message looks different
// depending on whether it was the window's last message that turn. Normalizing
// makes the fingerprint stable across that transform.
func aiGatewayContentText(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		// Concatenate without separators so a single text block (the cache_control
		// form, [{type:text,text:X}]) canonicalizes to the SAME key as the plain
		// string X — that equivalence is the whole point of this normalization.
		var b strings.Builder
		for _, raw := range c {
			blk := aiGatewayMap(raw)
			if len(blk) == 0 {
				b.WriteString(aiGatewayJSONString(raw))
				continue
			}
			switch aiGatewayString(blk["type"]) {
			case "text", "":
				b.WriteString(aiGatewayString(blk["text"]))
			case "tool_use":
				b.WriteString("\x01tool_use:" + aiGatewayString(blk["id"]) + ":" + aiGatewayString(blk["name"]) + ":" + aiGatewayJSONString(blk["input"]))
			case "tool_result":
				b.WriteString("\x01tool_result:" + aiGatewayString(blk["tool_use_id"]) + ":" + aiGatewayContentText(blk["content"]))
			default:
				b.WriteString("\x01" + aiGatewayJSONString(blk))
			}
		}
		return b.String()
	default:
		return aiGatewayJSONString(content)
	}
}

// aiGatewayMessageIdentity is a stable content fingerprint (role + normalized
// content) for one history message. Used to align the current request's sliding
// window against the previous one so history_ids stay absolute and stable
// across turns even as the dispatcher trims old messages off the front and
// rewrites the last message's content for cache_control.
func aiGatewayMessageIdentity(item map[string]interface{}) string {
	return aiGatewayString(item["role"]) + "\x00" + aiGatewayContentText(item["content"])
}

// aiGatewayAssignAbsoluteIDs numbers newItems with absolute, monotonic, stable
// history ids by anchoring them to prevItems (the previous request's
// already-numbered window). The dispatcher sends a sliding window capped at N
// messages: each turn it appends to the tail and trims the front, so naive
// positional numbering (1..N) saturates at N and reuses ids for different
// content — which freezes the frontend's committed view. Instead we find the
// previous window's last message inside the new window: overlapping messages
// keep their prior id, tail messages continue from there. Falls back to
// positional (from prevMax, or 1) when there is no usable overlap.
func aiGatewayAssignAbsoluteIDs(prevItems []interface{}, newItems []interface{}, prevMax int) []interface{} {
	if len(newItems) == 0 {
		return newItems
	}
	if len(prevItems) == 0 {
		return aiGatewayAssignSequentialIDs(newItems, 0)
	}
	prevLast := aiGatewayMap(prevItems[len(prevItems)-1])
	prevLastID := aiGatewayInt(prevLast["id"])
	prevLastKey := aiGatewayMessageIdentity(prevLast)
	// Last occurrence wins: the dispatcher only appends, so the newest copy of
	// the previous tail message is the real anchor; everything after it is new.
	matchIdx := -1
	for i := len(newItems) - 1; i >= 0; i-- {
		if aiGatewayMessageIdentity(aiGatewayMap(newItems[i])) == prevLastKey {
			matchIdx = i
			break
		}
	}
	if matchIdx < 0 || prevLastID <= 0 {
		// No overlap (conversation reset, or prev window unnumbered) → keep ids
		// monotonic by continuing past the previous max.
		base := prevMax
		if base < 0 {
			base = 0
		}
		return aiGatewayAssignSequentialIDs(newItems, base)
	}
	out := make([]interface{}, 0, len(newItems))
	for i, raw := range newItems {
		id := prevLastID + (i - matchIdx)
		if id < 1 {
			id = 1
		}
		out = append(out, aiGatewayWithID(raw, id))
	}
	return out
}

func aiGatewayCurrentBodyMaxHistoryID(body interface{}) int {
	mapped := aiGatewayMap(body)
	if len(mapped) == 0 {
		return 0
	}
	items := []interface{}{}
	if messages := aiGatewaySlice(mapped["messages"]); len(messages) > 0 {
		items = messages
	} else if input := aiGatewaySlice(mapped["input"]); len(input) > 0 {
		items = input
	}
	maxID := 0
	for _, raw := range items {
		item := aiGatewayMap(raw)
		if len(item) == 0 {
			continue
		}
		itemID := aiGatewayInt(item["id"])
		if itemID > maxID {
			maxID = itemID
		}
	}
	return maxID
}

// aiGatewayBodyAlreadyNumbered reports whether every history message in the
// body already carries a positive id — i.e. this body was numbered on the
// request-capture path, so the write path must not renumber it.
func aiGatewayBodyAlreadyNumbered(mapped map[string]interface{}) bool {
	items := aiGatewaySlice(mapped["messages"])
	if len(items) == 0 {
		items = aiGatewaySlice(mapped["input"])
	}
	if len(items) == 0 {
		return false
	}
	for _, raw := range items {
		if aiGatewayInt(aiGatewayMap(raw)["id"]) <= 0 {
			return false
		}
	}
	return true
}

func aiGatewayAnnotateCurrentBodyHistoryIDs(agentID string, body interface{}) interface{} {
	mapped := aiGatewayMap(body)
	if len(mapped) == 0 {
		return body
	}
	// Idempotent: a body already numbered at capture is left untouched (the
	// write/status path re-annotates; sequential numbering is deterministic, so
	// this is just belt-and-suspenders).
	if aiGatewayBodyAlreadyNumbered(mapped) {
		return body
	}
	// current.json is the FULL, ordered conversation snapshot, so the messages
	// array IS the history — position is identity. Number it straight through
	// 1..N; the answer then takes slot N+1 (= max id + 1). NO content-fingerprint
	// anchoring: repeated identical messages (e.g. several "hi") never collide,
	// because ids depend only on position, not content. This is the SHARED path
	// for every agent — cicy via the local gateway, claude via mitm, same call
	// site. An agent's own context compaction (Claude Code auto-compact, or an
	// explicit cicy compact) just shortens the array → one re-number (an
	// acceptable one-shot re-page), not a per-turn drift.
	next := aiGatewayCloneAnyMap(mapped)
	delete(next, "history")
	if messages := aiGatewaySlice(next["messages"]); len(messages) > 0 {
		next["messages"] = aiGatewayAssignSequentialIDs(messages, 0)
	}
	if input := aiGatewaySlice(next["input"]); len(input) > 0 {
		next["input"] = aiGatewayAssignSequentialIDs(input, 0)
	}
	return next
}

func aiGatewayJSONOrString(value interface{}) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return aiGatewayJSONString(value)
}

func aiGatewayJSONString(value interface{}) string {
	if value == nil {
		return ""
	}
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(body)
}

func aiGatewayShortID() string {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("%08x", time.Now().UnixNano())
}
