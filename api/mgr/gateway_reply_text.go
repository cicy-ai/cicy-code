package main

import (
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
