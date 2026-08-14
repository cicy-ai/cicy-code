// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// handleAgentReplyText returns the receiving agent's last reply as one
// structured-text blob. Designed for cross-agent use cases: when sender A
// gets a "[B] work done" callback, A calls this endpoint to read what B
// actually answered without having to scrape B's terminal scrollback.
//
// Request body:
//
//	{ "pane_id": "w-10003", "full": false }
//
// full=false (default): only thinking + final text (the conversational reply).
// full=true: also include tool_use items (name + input JSON) inline in
//
//	turn order, so the caller can see HOW the receiver got to its answer.
//
// Returned shape:
//
//	{ "status": "...", "turn_id": "...", "updated_at": "...",
//	  "input_tokens": N, "output_tokens": N, "total_tokens": N,
//	  "text": "<rendered text>" }
func handleAgentReplyText(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		PaneID string `json:"pane_id"`
		WinID  string `json:"win_id"`
		Full   bool   `json:"full"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	target := strings.TrimSpace(req.PaneID)
	if target == "" {
		target = strings.TrimSpace(req.WinID)
	}
	target = shortPaneID(normPaneID(target))
	if target == "" {
		httpErr(w, http.StatusBadRequest, "pane_id required")
		return
	}

	resp, err := agentReplyTextData(target, req.Full)
	if err != nil {
		httpErr(w, http.StatusNotFound, "no reply on file for "+target)
		return
	}
	J(w, resp)
}

func agentReplyTextData(target string, full bool) (M, error) {
	target = shortPaneID(normPaneID(strings.TrimSpace(target)))
	reply := agentInspectorLoadReply(target)
	if strings.TrimSpace(reply.TurnID) == "" {
		return nil, fmt.Errorf("no reply on file for %s", target)
	}
	rendered := renderReplyItems(reply.Items, full)
	resp := M{
		"pane_id":       target,
		"status":        reply.Status,
		"turn_id":       reply.TurnID,
		"started_at":    reply.StartedAt,
		"updated_at":    reply.UpdatedAt,
		"input_tokens":  reply.InputTokens,
		"output_tokens": reply.OutputTokens,
		"total_tokens":  reply.LastTotalTokens,
		"full":          full,
		"text":          rendered,
	}
	return resp, nil
}

// renderReplyItems turns the structured reply.json items into a single
// human-readable text block. Sections are labeled with [thinking], [text],
// and (when full=true) [tool_use ...] so an AI consumer can parse them.
func renderReplyItems(items []map[string]interface{}, full bool) string {
	var b strings.Builder
	for _, it := range items {
		typ, _ := it["type"].(string)
		switch typ {
		case "thinking":
			th, _ := it["thinking"].(string)
			th = strings.TrimSpace(th)
			if th == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString("[thinking]\n")
			b.WriteString(th)
		case "text":
			txt, _ := it["text"].(string)
			txt = strings.TrimSpace(txt)
			if txt == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString("[text]\n")
			b.WriteString(txt)
		case "tool_use":
			if !full {
				continue
			}
			name, _ := it["name"].(string)
			// reply.Items 中 tool_use 的 tool_id 字段是 LLM 返回的原生 tool call id（如 "call_xxx"）。
			// "id" 字段是 cicy 自加的序号。
			toolID, _ := it["tool_id"].(string)
			input := it["input"]
			var inputStr string
			switch v := input.(type) {
			case string:
				inputStr = v
			default:
				if buf, err := json.Marshal(v); err == nil {
					inputStr = string(buf)
				}
			}
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			if toolID != "" {
				fmt.Fprintf(&b, "[tool_use name=%s id=%s]\n%s", name, toolID, inputStr)
			} else {
				fmt.Fprintf(&b, "[tool_use name=%s]\n%s", name, inputStr)
			}
		}
	}
	return b.String()
}

// renderReplyItemForIM 把单个 reply.json item 渲染成给 IM（TG / WeChat）推送的纯文本。
// 每个 type 一种格式，用空格 + emoji 让用户在 IM 客户端里更容易扫读。
//
//	thinking: "💭 ..."
//	text:     "..."
//	tool_use: skipped（内部执行细节不推送到 IM）
//
// 长内容会截断（避免 IM 单条消息撑爆 WeChat ~4096 字符限制）：
//   - thinking: 1500 char
//   - text:     2500 char
//   - tool_use input 内长字符串字段: 800 char
//
// 返回空字符串表示 skip（item 内容为空）。
func renderReplyItemForIM(item map[string]interface{}) string {
	if item == nil {
		return ""
	}
	typ, _ := item["type"].(string)
	switch typ {
	case "thinking":
		// 不再推送 thinking 到 IM —— 内部思考过程对用户没价值且会刷屏。
		return ""
	case "text":
		txt, _ := item["text"].(string)
		txt = strings.TrimSpace(txt)
		if txt == "" {
			return ""
		}
		// Gateway/network diagnostics are useful in reply.json and local logs, but
		// leaking localhost ports and Go socket errors into Feishu/WeChat makes a
		// transient retry look like several separate user-facing failures.
		if imIsTechnicalTransportFailure(txt) {
			return ""
		}
		return imTruncateLongString(txt, 2500)
	case "tool_use":
		// wait / exec / write_stdin / read 等工具调用属于 agent 内部执行过程。
		// reply.json 和网页端继续保留完整记录，但所有 IM 出站统一跳过，避免刷屏。
		return ""
	case "tool_error":
		// 合成 item（aiGatewayInjectToolResultsIntoItems 发现 is_error 的
		// tool_result 时造出来,只走 IM,不进 reply.json):工具跑失败,推一条
		// ❌ 让用户不打开客户端也知道 agent 撞了错。
		name, _ := item["name"].(string)
		if strings.TrimSpace(name) == "" {
			name = "tool"
		}
		errText := strings.TrimSpace(scalarToString(item["error"]))
		if errText == "" {
			return "❌ " + name + " 失败"
		}
		if imIsTechnicalTransportFailure("生成失败 " + errText) {
			return ""
		}
		return "❌ " + name + " 失败\n" + imTruncateLongString(errText, 500)
	}
	return ""
}

func imIsTechnicalTransportFailure(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if !strings.Contains(lower, "生成失败") && !strings.Contains(lower, "http 5") {
		return false
	}
	for _, marker := range []string{
		"broken pipe", "closed network connection", "connection reset by peer",
		"write tcp", "read tcp", "dial tcp", "127.0.0.1:", "localhost:",
		"tls: bad record mac", "upstream tls handshake", "\neof", "\r\neof",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// imTruncateLongString 把字符串截到 limit 个 rune，超出则加 "...(N chars total)" 标记。
func imTruncateLongString(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + fmt.Sprintf("\n...(truncated, %d chars total)", len(runes))
}

// summarizeToolForIM 把一次工具调用的参数压成一行人类可读的摘要(而不是整坨 JSON)。
// 同时兼容两种 API 的工具格式:
//   - Claude(tool_use):input 本身就是对象(map)。
//   - OpenAI(tool_calls):arguments 是 JSON 编码的字符串 → 先解析成对象。
//
// 解析后优先取最能说明"这工具在干什么"的那个字段(command / file_path / url /
// pattern / query / prompt …),没有已知字段时退化成紧凑的 key: value 摘要。
func summarizeToolForIM(input interface{}) string {
	m := normalizeToolInput(input)
	if m == nil {
		// input 不是对象也不是合法 JSON 字符串 —— 有原始标量就直接发(截断)。
		if s, ok := input.(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				return imTruncateLongString(s, 500)
			}
		}
		return ""
	}
	// 最能标识工具意图的字段,按优先级取第一个非空的。
	for _, k := range []string{
		"command", "file_path", "filePath", "path", "notebook_path",
		"url", "pattern", "query", "prompt", "description", "expression", "name",
	} {
		if v, ok := m[k]; ok {
			if s := strings.TrimSpace(scalarToString(v)); s != "" {
				return imTruncateLongString(s, 500)
			}
		}
	}
	// 没有已知字段:按 key 排序取前几个标量字段拼成紧凑摘要(跳过嵌套对象/数组)。
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		s := strings.TrimSpace(scalarToString(m[k]))
		if s == "" {
			continue
		}
		parts = append(parts, k+": "+imTruncateLongString(s, 200))
		if len(parts) >= 4 {
			break
		}
	}
	return strings.Join(parts, "\n")
}

// normalizeToolInput 把工具参数统一成 map。OpenAI 的 arguments 是 JSON 字符串,
// Claude 的 input 直接是对象。
func normalizeToolInput(input interface{}) map[string]interface{} {
	switch v := input.(type) {
	case map[string]interface{}:
		return v
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil
		}
		var m map[string]interface{}
		if json.Unmarshal([]byte(s), &m) == nil {
			return m
		}
		return nil
	default:
		return nil
	}
}

// scalarToString 渲染标量参数值;数组/对象折叠成简短占位,保持 IM 单行简洁。
func scalarToString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return fmt.Sprintf("%t", x)
	case float64:
		// JSON 数字解码为 float64;整数干净输出。
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case nil:
		return ""
	case []interface{}:
		return fmt.Sprintf("[%d items]", len(x))
	case map[string]interface{}:
		return fmt.Sprintf("{%d fields}", len(x))
	default:
		return fmt.Sprintf("%v", x)
	}
}
