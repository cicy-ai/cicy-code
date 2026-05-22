package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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

	reply := agentInspectorLoadReply(target)
	if strings.TrimSpace(reply.TurnID) == "" {
		httpErr(w, http.StatusNotFound, "no reply on file for "+target)
		return
	}

	rendered := renderReplyItems(reply.Items, req.Full)
	resp := map[string]interface{}{
		"pane_id":       target,
		"status":        reply.Status,
		"turn_id":       reply.TurnID,
		"started_at":    reply.StartedAt,
		"updated_at":    reply.UpdatedAt,
		"input_tokens":  reply.InputTokens,
		"output_tokens": reply.OutputTokens,
		"total_tokens":  reply.LastTotalTokens,
		"full":          req.Full,
		"text":          rendered,
	}
	J(w, resp)
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
//   thinking: "💭 ..."
//   text:     "..."
//   tool_use: "🔧 <name>\n```json\n<input json>\n```"
// 长内容会截断（避免 IM 单条消息撑爆 WeChat ~4096 字符限制）：
//   - thinking: 1500 char
//   - text:     2500 char
//   - tool_use input 内长字符串字段: 800 char
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
		return imTruncateLongString(txt, 2500)
	case "tool_use":
		name, _ := item["name"].(string)
		var inputStr string
		switch v := item["input"].(type) {
		case string:
			inputStr = imTruncateLongString(v, 800)
		case nil:
			inputStr = ""
		default:
			// 截断 input 中的长字符串字段（如 Write 工具的 content / Bash 的大命令），
			// 避免整条 IM 消息撑爆 WeChat 4096 字符限制。
			truncated := imTruncateInputForIM(v, 800)
			// 用不 escape HTML 的 encoder + indent，让 ">" "&" "<" 保持原样、
			// JSON 多行更可读。
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			if err := enc.Encode(truncated); err == nil {
				inputStr = strings.TrimRight(buf.String(), "\n")
			}
		}
		if inputStr == "" {
			return "🔧 " + name
		}
		// 用 markdown code block 包起来，让 TG / WeChat / 其他 markdown-aware
		// 客户端正确渲染（缩进 / 高亮）。
		return "🔧 " + name + "\n```json\n" + inputStr + "\n```"
	}
	return ""
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

// imTruncateInputForIM 递归截断 tool_use input 中的长字符串字段，保留 JSON 结构。
// 用于避免 Write 工具的 content / 大命令文本撑爆 IM 单条消息。
func imTruncateInputForIM(v interface{}, limit int) interface{} {
	switch x := v.(type) {
	case string:
		return imTruncateLongString(x, limit)
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, item := range x {
			out[i] = imTruncateInputForIM(item, limit)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, val := range x {
			out[k] = imTruncateInputForIM(val, limit)
		}
		return out
	default:
		return v
	}
}
