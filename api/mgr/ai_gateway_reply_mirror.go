// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Reply mirror — 调试/数据收集开关。
//
// 启用方式：环境变量 CICY_GATEWAY_REPLY_MIRROR=1（默认关闭）。
//
// 启用后，每次 AI gateway audit session 完成时，会把本次请求 + 响应的完整快照
// 写到一份独立 JSON 文件：
//   <agent_history_dir>/reply_mirror/<turn_id>_<request_id>_<timestamp>.json
//
// 主路径（reply.json / current.json）行为完全不变，只是多写一份镜像。
//
// 设计目的：在不动主解析逻辑的前提下，先把"模型返回的真实数据"原原本本保留下来，
// 用于诊断 reply.Items 是否解析正确、SUGGESTION MODE 之类的辅助调用是否被混入等问题。
// 收集足够的样本后再决定如何优化主路径的 reply.Items 构造。

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// aiGatewayReplyMirrorEnabled 读取环境变量决定是否启用镜像。
// 写在函数里而不是包级 var，方便运行时切换（重启后立即生效）。
func aiGatewayReplyMirrorEnabled() bool {
	v := strings.TrimSpace(os.Getenv("CICY_GATEWAY_REPLY_MIRROR"))
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// aiGatewayReplyMirrorDir 返回某个 agent 的 reply_mirror 目录路径。
func aiGatewayReplyMirrorDir(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "reply_mirror")
}

// aiGatewayReplyMirrorRecord 是写入磁盘的一份镜像记录。
type aiGatewayReplyMirrorRecord struct {
	// 元数据
	MirroredAt    string `json:"mirrored_at"`              // 镜像写入时间
	AgentID       string `json:"agent_id"`
	TurnID        string `json:"turn_id"`
	RequestID     string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Method        string `json:"method"`
	URL           string `json:"url"`
	StatusCode    int    `json:"status_code"`
	Status        string `json:"status"`     // completed / failed
	LatencyMS     int64  `json:"latency_ms"`
	Question      string `json:"question,omitempty"`

	// 请求侧（来自 current snapshot）
	RequestHeaders map[string][]string `json:"request_headers"`
	RequestBody    interface{}         `json:"request_body"`

	// 响应侧
	ResponseHeaders map[string][]string `json:"response_headers"`
	ResponseBody    string              `json:"response_body"` // 原文（SSE 流是拼好的整段，非流是 JSON）

	// 解析结果（让我们对比 parser 的输出和原始响应）
	ParsedThinking  string                   `json:"parsed_thinking"`
	ParsedAnswer    string                   `json:"parsed_answer"`
	ParsedToolCalls []aiGatewayToolCall      `json:"parsed_tool_calls"`
	ParsedUsage     map[string]interface{}   `json:"parsed_usage"`

	// 主路径最终写到 reply.json 的快照（让我们对比 reply.Items 和原始响应）
	ReplySnapshot aiGatewayReplySnapshot `json:"reply_snapshot"`
}

// aiGatewayWriteReplyMirror 在 audit session 完成时调用。
//
// session.mu 必须由调用方在调用前 Unlock（这个函数里不再加锁）；
// 我们在 completeFromResponse 末尾、s.mu.Unlock() 之后调用即可。
//
// 任何错误都只 log，不抛出（绝不影响主路径）。
func aiGatewayWriteReplyMirror(
	s *aiGatewayAuditSession,
	statusCode int,
	responseHeaders http.Header,
	responseBody []byte,
	parsed aiGatewayParsedResponse,
	replySnapshot aiGatewayReplySnapshot,
) {
	if s == nil || !aiGatewayReplyMirrorEnabled() {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[reply-mirror] panic agent=%s turn=%s: %v", s.agentID, s.turnID, r)
		}
	}()

	dir := aiGatewayReplyMirrorDir(s.agentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[reply-mirror] mkdir failed agent=%s dir=%s err=%v", s.agentID, dir, err)
		return
	}

	now := time.Now().UTC()
	latencyMS := int64(0)
	if !s.startedAt.IsZero() {
		latencyMS = now.Sub(s.startedAt).Milliseconds()
	}

	// 用方法快照里的 status 字符串（completed / failed），跟 [ai-gateway] complete 日志一致。
	status := "completed"
	if statusCode >= 400 {
		status = "failed"
	}

	rec := aiGatewayReplyMirrorRecord{
		MirroredAt:     now.Format(time.RFC3339Nano),
		AgentID:        s.agentID,
		TurnID:         s.turnID,
		RequestID:      s.requestID,
		ConversationID: s.conversationID,
		Provider:       s.provider,
		Model:          s.model,
		Method:         s.current.Method,
		URL:            s.current.URL,
		StatusCode:     statusCode,
		Status:         status,
		LatencyMS:      latencyMS,
		Question:       s.question,

		RequestHeaders: aiGatewayMirrorCloneHeaderMap(s.current.Headers),
		RequestBody:    aiGatewayCloneJSONValue(s.current.Body),

		ResponseHeaders: aiGatewayMirrorCloneHeaderMap(map[string][]string(responseHeaders)),
		ResponseBody:    string(responseBody),

		ParsedThinking:  parsed.Thinking,
		ParsedAnswer:    parsed.Answer,
		ParsedToolCalls: parsed.ToolCalls,
		ParsedUsage:     aiGatewayCloneAnyMap(parsed.Usage),

		ReplySnapshot: replySnapshot,
	}

	// 文件名：<turn>_<req>_<ts>.json
	// turn / req 可能比较短或者重复（同一 turn 多次 request），加 ts 避免覆盖。
	turnPart := aiGatewayMirrorSafeFileSegment(s.turnID, "noturn")
	reqPart := aiGatewayMirrorSafeFileSegment(s.requestID, "noreq")
	tsPart := now.Format("20060102T150405.000000000Z")
	name := fmt.Sprintf("%s_%s_%s.json", turnPart, reqPart, tsPart)
	path := filepath.Join(dir, name)

	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		log.Printf("[reply-mirror] marshal failed agent=%s turn=%s err=%v", s.agentID, s.turnID, err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		log.Printf("[reply-mirror] write failed agent=%s path=%s err=%v", s.agentID, path, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("[reply-mirror] rename failed agent=%s path=%s err=%v", s.agentID, path, err)
		_ = os.Remove(tmp)
		return
	}
	log.Printf("[reply-mirror] wrote agent=%s turn=%s req=%s status_code=%d resp_bytes=%d items=%d tools=%d path=%s",
		s.agentID, s.turnID, s.requestID, statusCode, len(responseBody),
		len(replySnapshot.Items), len(replySnapshot.ToolCalls), path)
}

// aiGatewayMirrorSafeFileSegment 把字符串里非 [A-Za-z0-9._-] 的字符替换为 _。
func aiGatewayMirrorSafeFileSegment(s string, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.' || r == '-' || r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return string(out)
}

// aiGatewayMirrorCloneHeaderMap 简单深拷贝 header map（避免依赖 http.Header 类型）。
func aiGatewayMirrorCloneHeaderMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}
