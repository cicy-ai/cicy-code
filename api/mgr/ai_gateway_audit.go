package main

import (
	"bytes"
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
)

type aiGatewayToolCall struct {
	ToolID    string `json:"tool_id"`
	ToolName  string `json:"tool_name"`
	Arguments string `json:"arguments"`
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
}

type aiGatewayReplySnapshot struct {
	TurnID                   string                   `json:"turn_id"`
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
	RequestCount             int                      `json:"request_count"`
	StatusMap                aiGatewayStatusMap       `json:"status_map"`
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
	startedAt       time.Time
	replyEventIndex int
	current         aiGatewayCurrentSnapshot
	reply           aiGatewayReplySnapshot
	replyHooks      []aiGatewayReplyHook
	lastStatusPush  string
	// auxiliary 标记：当前请求是 SUGGESTION MODE 等辅助调用，
	// 不应该污染 current.json / reply.json，但仍然写 mirror（用于诊断）。
	auxiliary bool
	// pendingItem 是流式过程中正在累积的 reply.Items 候选（thinking / text / tool_use）。
	// 当 stream_kind 或 tool 切换时，旧 pending 会被 flush 到 reply.Items 并立刻刷盘。
	// 这让前端 / IM 推送能在每个 item 完成时实时看到新内容（而不是等整次 HTTP 完成）。
	pendingItem *aiGatewayPendingItem
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
	ssePending string
	isStream   bool
	once       sync.Once
}

type aiGatewayStreamAccumulator struct {
	thinkingParts []string
	answerParts   []string
	toolOrder     []string
	toolCalls     map[string]*aiGatewayToolCall
	usage         map[string]interface{}
	autoIndex     int
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
)

func newAIGatewayAuditSession(provider, agentID string, targetBase *url.URL, suffix string, method string, requestHeaders http.Header, requestBody []byte) *aiGatewayAuditSession {
	startedAt := time.Now().UTC()
	startedAtISO := startedAt.Format(time.RFC3339)
	prevReply := aiGatewayLoadReplySnapshot(agentID)
	prevInputTokens, prevOutputTokens, prevTotalTokens := aiGatewayReplyTotals(prevReply)
	prevCostCredit := aiGatewayReplyCostCredit(prevReply)
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
	annotatedBody := aiGatewayAnnotateCurrentBodyHistoryIDs(aiGatewayCloneJSONValue(payloadValue))
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
	reply := aiGatewayReplySnapshot{
		TurnID:           turnID,
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
		OutputTokens:     prevOutputTokens,
		TotalTokens:      prevTotalTokens,
		CostCredit:       prevCostCredit,
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
	// 识别辅助调用（SUGGESTION MODE 等）：这种请求不应污染主 reply.json / current.json。
	auxiliary := aiGatewayIsAuxiliaryRequest(question, payloadMap)
	return &aiGatewayAuditSession{
		agentID:        agentID,
		provider:       provider,
		model:          model,
		turnID:         turnID,
		requestID:      requestID,
		conversationID: conversationID,
		question:       question,
		startedAt:      startedAt,
		current:        current,
		reply:          reply,
		replyHooks:     newReplyHooksForPane(agentID, isContinuation),
		auxiliary:      auxiliary,
	}
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
	return rc
}

func (r *aiGatewayAuditReadCloser) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		_, _ = r.buf.Write(p[:n])
		if r.isStream {
			r.ssePending += string(p[:n])
			r.flushSSEEvents(err == io.EOF)
		}
	}
	if err == io.EOF {
		if r.isStream {
			r.flushSSEEvents(true)
		}
		r.finish(nil)
	}
	return n, err
}

func (r *aiGatewayAuditReadCloser) Close() error {
	err := r.inner.Close()
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
		if parsed.Answer != "" {
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
	s.reply.Usage = aiGatewayCloneAnyMap(parsed.Usage)
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

	if failed {
		errorText := aiGatewayExtractErrorText(responseBody, responseErr)
		if s.reply.Answer == "" {
			s.reply.Answer = errorText
			requestSpan.AnswerPreview = aiGatewayPreviewText(errorText, 220)
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
	}
	replySnapshot := s.reply
	replyHooks := s.replyHooks
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
	for _, h := range replyHooks {
		h.finalize(replySnapshot)
	}
	// 调试/数据收集：当 CICY_GATEWAY_REPLY_MIRROR=1 时把本次完整的请求+响应+解析结果
	// 镜像写到 reply_mirror/ 目录，供后续分析。完全不影响主路径。
	aiGatewayWriteReplyMirror(s, statusCode, headers, responseBody, parsed, replySnapshot)
}

func aiGatewayHistoryDir(agentID string) string {
	newDir := filepath.Join(builtinWorkerRuntimeDir(agentID), "history")
	legacyDirs := []string{
		filepath.Join(builtinWorkerWorkspace(agentID), ".history"),
		filepath.Join(builtinWorkerWorkspace(agentID), "history"),
		filepath.Join("/home/cicy", "workers", agentID, ".history"),
		filepath.Join("/home/cicy", "workers", agentID, "history"),
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
		for _, name := range []string{"current.json", "system_prompt.txt", "prompt_rules.json"} {
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

func aiGatewayWriteCurrentSnapshot(agentID string, current aiGatewayCurrentSnapshot) error {
	current.Body = aiGatewayAnnotateCurrentBodyHistoryIDs(aiGatewayCloneJSONValue(current.Body))
	current.MaxHistoryID = aiGatewayCurrentBodyMaxHistoryID(current.Body)
	return aiGatewayWriteJSONAtomic(aiGatewayCurrentSnapshotPath(agentID), current)
}

// aiGatewayReplySnapshotLite is a simplified version for reply.json
// Contains native content blocks format (like body.messages in current.json)
type aiGatewayReplySnapshotLite struct {
	TurnID                   string                   `json:"turn_id"`
	Status                   string                   `json:"status"`
	StartedAt                string                   `json:"started_at"`
	UpdatedAt                string                   `json:"updated_at"`
	Items                    []map[string]interface{} `json:"items"`
	InputTokens              int                      `json:"input_tokens"`
	OutputTokens             int                      `json:"output_tokens"`
	CacheCreationInputTokens int                      `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int                      `json:"cache_read_input_tokens"`
	TotalTokens              int                      `json:"total_tokens"`
	CostCredit               float64                  `json:"cost_credit"`
}

func aiGatewayWriteReplySnapshot(agentID string, reply aiGatewayReplySnapshot) error {
	aiGatewayStoreLiveReplySnapshot(agentID, reply)
	lite := aiGatewayReplySnapshotLite{
		TurnID:                   reply.TurnID,
		Status:                   reply.Status,
		StartedAt:                reply.StartedAt,
		UpdatedAt:                reply.UpdatedAt,
		Items:                    reply.Items,
		InputTokens:              reply.InputTokens,
		OutputTokens:             reply.OutputTokens,
		CacheCreationInputTokens: reply.CacheCreationInputTokens,
		CacheReadInputTokens:     reply.CacheReadInputTokens,
		TotalTokens:              reply.TotalTokens,
		CostCredit:               reply.CostCredit,
	}
	return aiGatewayWriteJSONAtomic(aiGatewayReplySnapshotPath(agentID), lite)
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
	statusEvent := s.applyStreamEventsLocked(events)
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
	if !s.auxiliary {
		if err := aiGatewayWriteReplySnapshot(s.agentID, s.reply); err != nil {
			log.Printf("[ai-gateway] write live reply snapshot failed for %s: %v", s.agentID, err)
		}
	}
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
	} else {
		primary = "completed"
	}
	return aiGatewayStatusMap{
		Primary: primary,
		Items: []aiGatewayStatusItem{
			{Kind: "thinking", Label: "Thinking 思考中", Active: hasThinking && (primary == "thinking" || primary == "working" || primary == "streaming"), Count: aiGatewayBoolInt(hasThinking)},
			{Kind: "tool_call", Label: "Working 工作中", Active: toolCount > 0 && (activeRequestCount > 0 || hasCurrentActive) && (primary == "working" || primary == "streaming"), Count: toolCount},
			{Kind: "http", Label: "HTTP", Active: activeRequestCount > 0, Count: activeRequestCount},
			{Kind: "streaming", Label: "Working 工作中", Active: hasAnswer && activeRequestCount > 0 && primary == "streaming", Count: aiGatewayBoolInt(hasAnswer && activeRequestCount > 0 && primary == "streaming")},
		},
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
	if strings.Contains(contentType, "text/event-stream") || bytes.HasPrefix(bytes.TrimSpace(body), []byte("data:")) {
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
	return aiGatewayParsedResponse{
		Thinking:  strings.Join(acc.thinkingParts, ""),
		Answer:    strings.Join(acc.answerParts, ""),
		ToolCalls: acc.toolCallsInOrder(),
		Usage:     aiGatewayCloneAnyMap(acc.usage),
	}
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
		case "function_call_output", "tool_result", "tool_use_result", "computer_call_output", "shell_call_output", "apply_patch_call_output":
			return true
		}
		return false
	}
	return false
}

// aiGatewayIsAuxiliaryRequest 识别 SUGGESTION MODE / 提示猜测等辅助调用。
// 这类调用不应该污染主 reply.json / current.json（仍然走 mirror 用于诊断）。
//
// 判别规则（任一命中即认为是 auxiliary）：
//   - question 文本以 "[SUGGESTION MODE:" 开头（Claude Code 的 next-step 预测）
//   - body.metadata.purpose / body.metadata.kind 标记为 suggestion
func aiGatewayIsAuxiliaryRequest(question string, body map[string]interface{}) bool {
	if q := strings.TrimSpace(question); q != "" {
		upper := strings.ToUpper(q)
		if strings.HasPrefix(upper, "[SUGGESTION MODE") {
			return true
		}
	}
	if body == nil {
		return false
	}
	if meta := aiGatewayMap(body["metadata"]); len(meta) > 0 {
		for _, key := range []string{"purpose", "kind", "category"} {
			val := strings.ToLower(strings.TrimSpace(aiGatewayString(meta[key])))
			if strings.Contains(val, "suggestion") {
				return true
			}
		}
	}
	return false
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
	pricing := map[string][2]float64{
		"gpt-5.4":           {5.0, 15.0},
		"gpt-5":             {5.0, 15.0},
		"gpt-4.1":           {2.0, 8.0},
		"claude-sonnet-4-6": {3.0, 15.0},
		"claude-haiku-4-5":  {0.8, 4.0},
		"claude-opus-4-6":   {15.0, 75.0},
	}
	normalized := strings.ToLower(strings.TrimSpace(model))
	var matched *[2]float64
	for key, value := range pricing {
		if strings.Contains(normalized, key) {
			v := value
			matched = &v
			break
		}
	}
	if matched == nil {
		return 0
	}
	inputTokens := float64(aiGatewayInputTokenValue(usage))
	outputTokens := float64(aiGatewayOutputTokenValue(usage))
	inputCost := (inputTokens / 1_000_000.0) * matched[0]
	outputCost := (outputTokens / 1_000_000.0) * matched[1]
	return float64(int((inputCost+outputCost)*1_000_000+0.5)) / 1_000_000
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

func aiGatewayAssignSequentialIDs(items []interface{}) []interface{} {
	if len(items) == 0 {
		return items
	}
	out := make([]interface{}, 0, len(items))
	for i, raw := range items {
		item := aiGatewayMap(raw)
		if len(item) == 0 {
			out = append(out, raw)
			continue
		}
		next := aiGatewayCloneAnyMap(item)
		next["id"] = i + 1
		out = append(out, next)
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

func aiGatewayAnnotateCurrentBodyHistoryIDs(body interface{}) interface{} {
	mapped := aiGatewayMap(body)
	if len(mapped) == 0 {
		return body
	}
	next := aiGatewayCloneAnyMap(mapped)
	delete(next, "history")
	if messages := aiGatewaySlice(next["messages"]); len(messages) > 0 {
		next["messages"] = aiGatewayAssignSequentialIDs(messages)
	}
	if input := aiGatewaySlice(next["input"]); len(input) > 0 {
		next["input"] = aiGatewayAssignSequentialIDs(input)
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
