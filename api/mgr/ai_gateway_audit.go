package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	TurnID           string   `json:"turn_id"`
	AgentID          string   `json:"agent_id"`
	Question         string   `json:"question"`
	QuestionPreview  string   `json:"question_preview"`
	Provider         string   `json:"provider"`
	Model            string   `json:"model"`
	Status           string   `json:"status"`
	StartedAt        string   `json:"started_at"`
	UpdatedAt        string   `json:"updated_at"`
	RequestIDs       []string `json:"request_ids"`
	ActiveRequestIDs []string `json:"active_request_ids"`
	ConversationIDs  []string `json:"conversation_ids"`
}

type aiGatewayReplySnapshot struct {
	TurnID       string                 `json:"turn_id"`
	Status       string                 `json:"status"`
	StartedAt    string                 `json:"started_at"`
	UpdatedAt    string                 `json:"updated_at"`
	Thinking     string                 `json:"thinking"`
	Answer       string                 `json:"answer"`
	ToolCalls    []aiGatewayToolCall    `json:"tool_calls"`
	HTTPRequests []aiGatewayRequestSpan `json:"http_requests"`
	Usage        map[string]interface{} `json:"usage"`
	CostCredit   float64                `json:"cost_credit"`
	Models       []string               `json:"models"`
	RequestCount int                    `json:"request_count"`
	StatusMap    aiGatewayStatusMap     `json:"status_map"`
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
	mu             sync.Mutex
	finalized      bool
	agentID        string
	provider       string
	model          string
	turnID         string
	requestID      string
	conversationID string
	question       string
	startedAt      time.Time
	current        aiGatewayCurrentSnapshot
	reply          aiGatewayReplySnapshot
}

type aiGatewayAuditReadCloser struct {
	inner      io.ReadCloser
	audit      *aiGatewayAuditSession
	statusCode int
	headers    http.Header
	buf        bytes.Buffer
	once       sync.Once
}

type aiGatewayStreamAccumulator struct {
	thinkingParts []string
	answerParts   []string
	toolOrder     []string
	toolCalls     map[string]*aiGatewayToolCall
	usage         map[string]interface{}
	autoIndex     int
}

func newAIGatewayAuditSession(provider, agentID string, targetBase *url.URL, suffix string, method string, requestBody []byte) *aiGatewayAuditSession {
	startedAt := time.Now().UTC()
	startedAtISO := startedAt.Format(time.RFC3339)
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
	question := aiGatewayExtractQuestion(payloadMap)
	model := aiGatewayString(payloadMap["model"])
	conversationID := aiGatewayExtractConversationID(payloadMap)
	requestID := aiGatewayFirstNonEmpty(aiGatewayString(payloadMap["request_id"]), aiGatewayString(payloadMap["id"]), aiGatewayShortID())
	turnID := aiGatewayShortID()
	targetURL := *targetBase
	targetURL.Path = resolveOpenClawProviderTargetPath(targetBase.Path, suffix)
	targetURL.RawPath = ""

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
	current := aiGatewayCurrentSnapshot{
		TurnID:           turnID,
		AgentID:          agentID,
		Question:         question,
		QuestionPreview:  aiGatewayPreviewText(question, 140),
		Provider:         provider,
		Model:            model,
		Status:           "thinking",
		StartedAt:        startedAtISO,
		UpdatedAt:        startedAtISO,
		RequestIDs:       []string{requestID},
		ActiveRequestIDs: []string{requestID},
		ConversationIDs:  []string{conversationID},
	}
	reply := aiGatewayReplySnapshot{
		TurnID:       turnID,
		Status:       "thinking",
		StartedAt:    startedAtISO,
		UpdatedAt:    startedAtISO,
		Thinking:     "",
		Answer:       "",
		ToolCalls:    []aiGatewayToolCall{},
		HTTPRequests: []aiGatewayRequestSpan{requestSpan},
		Usage:        map[string]interface{}{},
		CostCredit:   0,
		Models:       aiGatewayOptionalStringList(model),
		RequestCount: 1,
		StatusMap: aiGatewayStatusMap{
			Primary: "thinking",
			Items: []aiGatewayStatusItem{
				{Kind: "thinking", Label: "Thinking", Active: true, Count: 1},
				{Kind: "tool_call", Label: "Tool", Active: false, Count: 0},
				{Kind: "http", Label: "HTTP", Active: true, Count: 1},
				{Kind: "streaming", Label: "Streaming", Active: false, Count: 0},
			},
		},
	}
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
	}
}

func newAIGatewayAuditReadCloser(inner io.ReadCloser, audit *aiGatewayAuditSession, statusCode int, headers http.Header, contentLength int64) *aiGatewayAuditReadCloser {
	rc := &aiGatewayAuditReadCloser{
		inner:      inner,
		audit:      audit,
		statusCode: statusCode,
		headers:    headers,
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
	}
	if err == io.EOF {
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

func (s *aiGatewayAuditSession) recordOutboundRequest(req *http.Request) {
	if s == nil || req == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reply.HTTPRequests) == 0 {
		return
	}
	s.reply.HTTPRequests[0].URL = req.URL.String()
	s.reply.HTTPRequests[0].Method = req.Method
	s.reply.HTTPRequests[0].RequestHeaders = aiGatewayCloneHeader(req.Header)
}

func (s *aiGatewayAuditSession) writeStartSnapshots() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeSnapshotsLocked()
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
	defer s.mu.Unlock()
	if s.finalized {
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
	s.reply.Usage = aiGatewayCloneAnyMap(parsed.Usage)
	s.reply.CostCredit = requestSpan.CostCredit
	s.reply.RequestCount = len(aiGatewayFilterPrimaryRequests(s.reply.HTTPRequests))

	if failed {
		errorText := aiGatewayExtractErrorText(responseBody, responseErr)
		if s.reply.Answer == "" {
			s.reply.Answer = errorText
			requestSpan.AnswerPreview = aiGatewayPreviewText(errorText, 220)
		}
		s.current.Status = "failed"
		s.reply.Status = "failed"
	} else {
		s.current.Status = "completed"
		s.reply.Status = "completed"
	}
	s.reply.StatusMap = aiGatewayBuildStatusMap(s.current, s.reply)

	if err := s.writeSnapshotsLocked(); err != nil {
		log.Printf("[ai-gateway] write reply snapshot failed for %s: %v", s.agentID, err)
	}
}

func (s *aiGatewayAuditSession) writeSnapshotsLocked() error {
	currentPath := filepath.Join(aiGatewayHistoryDir(s.agentID), "current.json")
	replyPath := filepath.Join(aiGatewayHistoryDir(s.agentID), "reply.json")
	if err := aiGatewayWriteJSONAtomic(currentPath, s.current); err != nil {
		return err
	}
	return aiGatewayWriteJSONAtomic(replyPath, s.reply)
}

func aiGatewayHistoryDir(agentID string) string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = "/root"
	}
	return filepath.Join(home, "workers", agentID, ".history")
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
	toolCount := len(reply.ToolCalls)
	hasThinking := strings.TrimSpace(reply.Thinking) != "" || strings.TrimSpace(reply.Answer) == ""
	hasAnswer := strings.TrimSpace(reply.Answer) != ""
	failed := current.Status == "failed" || reply.Status == "failed"
	primary := "thinking"
	if failed {
		primary = "failed"
	} else if hasAnswer {
		if activeRequestCount > 0 {
			primary = "streaming"
		} else {
			primary = "completed"
		}
	} else if activeRequestCount > 0 || toolCount > 0 {
		primary = "working"
	} else if !hasThinking {
		primary = "completed"
	}
	return aiGatewayStatusMap{
		Primary: primary,
		Items: []aiGatewayStatusItem{
			{Kind: "thinking", Label: "Thinking", Active: hasThinking && (primary == "thinking" || primary == "working" || primary == "streaming"), Count: aiGatewayBoolInt(hasThinking)},
			{Kind: "tool_call", Label: "Tool", Active: toolCount > 0 && (primary == "working" || primary == "streaming"), Count: toolCount},
			{Kind: "http", Label: "HTTP", Active: activeRequestCount > 0, Count: activeRequestCount},
			{Kind: "streaming", Label: "Streaming", Active: hasAnswer && primary == "streaming", Count: aiGatewayBoolInt(hasAnswer && primary == "streaming")},
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
		if acc.handleResponsesEvent(payload) {
			continue
		}
		for _, delta := range aiGatewayExtractStreamDeltas(payload) {
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
				a.usage = aiGatewayCloneAnyMap(usage)
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
	key := toolID
	if key == "" && toolIndex != nil {
		key = fmt.Sprintf("index:%d", *toolIndex)
	}
	if key == "" {
		key = fmt.Sprintf("auto:%d", a.autoIndex)
		a.autoIndex++
	}
	toolCall, ok := a.toolCalls[key]
	if !ok {
		toolCall = &aiGatewayToolCall{ToolID: toolID, ToolName: toolName, Arguments: ""}
		a.toolCalls[key] = toolCall
		a.toolOrder = append(a.toolOrder, key)
	}
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
		contentBlock := aiGatewayMap(payload["content_block"])
		if aiGatewayString(contentBlock["type"]) == "tool_use" {
			return []aiGatewayStreamDelta{{
				Kind:     "tool_call",
				ToolID:   aiGatewayString(contentBlock["id"]),
				ToolName: aiGatewayString(contentBlock["name"]),
			}}
		}
		return nil
	case "content_block_delta":
		delta := aiGatewayMap(payload["delta"])
		switch aiGatewayString(delta["type"]) {
		case "thinking_delta":
			return []aiGatewayStreamDelta{{Kind: "thinking", Content: aiGatewayRawString(delta["thinking"])}}
		case "text_delta":
			return []aiGatewayStreamDelta{{Kind: "answer", Content: aiGatewayRawString(delta["text"])}}
		case "input_json_delta":
			return []aiGatewayStreamDelta{{Kind: "tool_call", Content: aiGatewayRawString(delta["input_json"])}}
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
	if reasoning := aiGatewayString(delta["reasoning_content"]); reasoning != "" {
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
	if len(messages) > 0 {
		lastMessage := messages[len(messages)-1]
		if aiGatewayString(lastMessage["role"]) == "user" {
			if content := aiGatewayString(lastMessage["content"]); content != "" {
				return strings.TrimSpace(content)
			}
			if contentParts := aiGatewaySlice(lastMessage["content"]); len(contentParts) > 0 {
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
		}
	}

	inputItems := aiGatewayExtractInputItems(body)
	for i := len(inputItems) - 1; i >= 0; i-- {
		item := aiGatewayMap(inputItems[i])
		if len(item) == 0 || aiGatewayString(item["role"]) != "user" {
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

	textParts := []string{}
	for _, rawItem := range inputItems {
		switch item := rawItem.(type) {
		case string:
			if text := strings.TrimSpace(item); text != "" {
				textParts = append(textParts, text)
			}
		case map[string]interface{}:
			if role := aiGatewayString(item["role"]); role != "" && role != "user" {
				continue
			}
			if content := aiGatewayString(item["content"]); content != "" {
				textParts = append(textParts, strings.TrimSpace(content))
				continue
			}
			if contentParts := aiGatewaySlice(item["content"]); len(contentParts) > 0 {
				for _, part := range contentParts {
					if text := strings.TrimSpace(aiGatewayContentPartToText(part)); text != "" {
						textParts = append(textParts, text)
					}
				}
				continue
			}
			if text := strings.TrimSpace(aiGatewayContentPartToText(item)); text != "" {
				textParts = append(textParts, text)
			}
		}
	}
	return strings.Join(textParts, "\n")
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

func aiGatewayExtractConversationID(body map[string]interface{}) string {
	for _, key := range []string{"conversation_id", "session_id", "thread_id", "chat_id"} {
		if value := aiGatewayString(body[key]); value != "" {
			return value
		}
	}
	return aiGatewayShortID()
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
	inputTokens := float64(aiGatewayTokenValue(usage, "input_tokens", "prompt_tokens", "inputTokens"))
	outputTokens := float64(aiGatewayTokenValue(usage, "output_tokens", "completion_tokens", "outputTokens"))
	inputCost := (inputTokens / 1_000_000.0) * matched[0]
	outputCost := (outputTokens / 1_000_000.0) * matched[1]
	return float64(int((inputCost+outputCost)*1_000_000+0.5)) / 1_000_000
}

func aiGatewayUsageTotals(usage map[string]interface{}) (int, int, int) {
	inputTokens := aiGatewayTokenValue(usage, "input_tokens", "prompt_tokens", "inputTokens")
	outputTokens := aiGatewayTokenValue(usage, "output_tokens", "completion_tokens", "outputTokens")
	totalTokens := aiGatewayTokenValue(usage, "total_tokens", "totalTokens")
	if totalTokens == 0 {
		totalTokens = inputTokens + outputTokens
	}
	return inputTokens, outputTokens, totalTokens
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
