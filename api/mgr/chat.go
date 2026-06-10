package main

import (
	"fmt"
	"strings"
)

// extractArg renders a short, human-readable label for a tool call's arguments
// (the one-liner shown next to a tool in the agent inspector's history view). It
// is the lone survivor of the old kiro-traffic→chat-history reconstruction that
// used to live here — that feature (handleChatHistory / buildChatTurns /
// handleChatStream, all driven off the now-removed http_log table) was retired
// once agents stream their own history. agent_inspector.go is the remaining user.
func extractArg(inp map[string]interface{}, name string) string {
	switch strings.TrimSpace(name) {
	case "TaskCreate":
		for _, key := range []string{"subject", "activeForm", "description"} {
			if value, _ := inp[key].(string); strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	case "TaskUpdate":
		taskID, _ := inp["taskId"].(string)
		status, _ := inp["status"].(string)
		taskID = strings.TrimSpace(taskID)
		status = strings.TrimSpace(status)
		switch {
		case taskID != "" && status != "":
			return fmt.Sprintf("task #%s -> %s", taskID, status)
		case taskID != "":
			return fmt.Sprintf("task #%s", taskID)
		case status != "":
			return status
		}
	case "update_plan":
		if rawPlan, ok := inp["plan"].([]interface{}); ok && len(rawPlan) > 0 {
			inProgress := ""
			completed := 0
			pending := 0
			for _, rawItem := range rawPlan {
				item, _ := rawItem.(map[string]interface{})
				step, _ := item["step"].(string)
				status, _ := item["status"].(string)
				switch strings.TrimSpace(status) {
				case "in_progress":
					if inProgress == "" {
						inProgress = strings.TrimSpace(step)
					}
				case "completed":
					completed++
				case "pending":
					pending++
				}
			}
			if inProgress != "" {
				return inProgress
			}
			if completed > 0 || pending > 0 {
				return fmt.Sprintf("%d completed, %d pending", completed, pending)
			}
		}
	}
	if p, _ := inp["path"].(string); p != "" {
		return p
	}
	if p, _ := inp["file_path"].(string); p != "" {
		return p
	}
	// fs_read/fs_write with operations array
	if ops, ok := inp["operations"].([]interface{}); ok && len(ops) > 0 {
		op, _ := ops[0].(map[string]interface{})
		if p, _ := op["path"].(string); p != "" {
			sl, _ := op["start_line"].(float64)
			el, _ := op["end_line"].(float64)
			if sl > 0 && el > 0 {
				return fmt.Sprintf("%s %d-%d", p, int(sl), int(el))
			}
			return p
		}
	}
	if c, _ := inp["command"].(string); c != "" {
		c = strings.ReplaceAll(c, "\n", " ")
		if len([]rune(c)) > 200 {
			c = string([]rune(c)[:200]) + "..."
		}
		return c
	}
	if c, _ := inp["cmd"].(string); c != "" {
		c = strings.ReplaceAll(c, "\n", " ")
		if len([]rune(c)) > 200 {
			c = string([]rune(c)[:200]) + "..."
		}
		return c
	}
	if p, _ := inp["pattern"].(string); p != "" {
		return p
	}
	if u, _ := inp["query"].(string); u != "" {
		return u
	}
	if u, _ := inp["url"].(string); u != "" {
		return u
	}
	if s, _ := inp["symbol_name"].(string); s != "" {
		return s
	}
	return ""
}
