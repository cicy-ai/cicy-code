package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type toolInfo struct {
	Name   string `json:"name"`
	Arg    string `json:"arg,omitempty"`
	Result string `json:"result,omitempty"`
}

type step struct {
	Type   string    `json:"type"`            // "text", "tool"
	Text   string    `json:"text,omitempty"`   // for type=text
	Tools  []toolInfo `json:"tools,omitempty"` // for type=tool
}

type chatTurn struct {
	Q       string  `json:"q"`
	Steps   []step  `json:"steps,omitempty"`
	Credit  float64 `json:"credit"`
	FirstMs int     `json:"first_ms"`
	Status  string  `json:"status"`
	Model   string  `json:"model,omitempty"`
	TS      int64   `json:"ts"`
	StartTS int64   `json:"start_ts"`
}

var usageRe = regexp.MustCompile(`"usage":([\d.]+)`)
var contentRe = regexp.MustCompile(`assistantResponseEvent.*?"content":"(.*?)"`)
var toolNameIdRe = regexp.MustCompile(`"name":"([^"]+)","toolUseId"`)
var modelRe = regexp.MustCompile(`"modelId":"([^"]+)"`)

func extractArg(inp map[string]interface{}, name string) string {
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

// Extract toolUses from the last assistantResponseMessage in history,
// and match toolResults from currentMessage
func extractHistoryTools(reqParsed map[string]interface{}) []toolInfo {
	cs, _ := reqParsed["conversationState"].(map[string]interface{})
	hist, _ := cs["history"].([]interface{})

	// Build toolResult map from currentMessage
	resultMap := map[string]string{}
	cm, _ := cs["currentMessage"].(map[string]interface{})
	um, _ := cm["userInputMessage"].(map[string]interface{})
	ctx, _ := um["userInputMessageContext"].(map[string]interface{})
	trs, _ := ctx["toolResults"].([]interface{})
	for _, tr := range trs {
		trm, _ := tr.(map[string]interface{})
		tid, _ := trm["toolUseId"].(string)
		status, _ := trm["status"].(string)
		content, _ := trm["content"].([]interface{})
		summary := ""
		if status == "error" {
			summary = "❌ error"
		}
		for _, c := range content {
			cm2, _ := c.(map[string]interface{})
			if j, ok := cm2["json"].(map[string]interface{}); ok {
				stderr, _ := j["stderr"].(string)
				stdout, _ := j["stdout"].(string)
				exit, _ := j["exit_status"].(string)
				if exit != "" && exit != "0" {
					summary = "exit " + exit
					if stderr != "" {
						s := strings.TrimSpace(stderr)
						if len([]rune(s)) > 120 {
							s = string([]rune(s)[:120]) + "..."
						}
						summary += "\n" + s
					}
				} else if stdout != "" {
					s := strings.TrimSpace(stdout)
					if len([]rune(s)) > 300 {
						s = string([]rune(s)[:300]) + "..."
					}
					summary = s
				}
				// grep/glob json results
				if results, ok := j["results"].([]interface{}); ok {
					summary = fmt.Sprintf("%d matches", len(results))
				}
				if fps, ok := j["filePaths"].([]interface{}); ok {
					summary = fmt.Sprintf("%d files", len(fps))
				}
			}
			if text, _ := cm2["text"].(string); text != "" {
				s := strings.TrimSpace(text)
				if len([]rune(s)) > 300 {
					s = string([]rune(s)[:300]) + "..."
				}
				summary = s
			}
		}
		if tid != "" {
			resultMap[tid] = summary
		}
	}

	for i := len(hist) - 1; i >= 0; i-- {
		entry, _ := hist[i].(map[string]interface{})
		arm, _ := entry["assistantResponseMessage"].(map[string]interface{})
		tus, _ := arm["toolUses"].([]interface{})
		if len(tus) > 0 {
			var tools []toolInfo
			for _, tu := range tus {
				tm, _ := tu.(map[string]interface{})
				name, _ := tm["name"].(string)
				tid, _ := tm["toolUseId"].(string)
				inp, _ := tm["input"].(map[string]interface{})
				tools = append(tools, toolInfo{Name: name, Arg: extractArg(inp, name), Result: resultMap[tid]})
			}
			return tools
		}
	}
	return nil
}

func handleChatHistory(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	if pane == "" {
		http.Error(w, "pane required", 400)
		return
	}
	agentType := ""
	// Try to get from DB first
	var at sql.NullString
	db.QueryRow("SELECT agent_type FROM agent_config WHERE pane_id=?", pane).Scan(&at)
	if at.Valid && at.String != "" {
		agentType = at.String
	} else {
		// Fallback: extract from latest request
		var data []byte
		err := db.QueryRow(`SELECT data FROM http_log WHERE pane_id=? AND url LIKE '%q.us-east-1%' AND data LIKE '%GenerateAssistantResponse%' ORDER BY id DESC LIMIT 1`, pane).Scan(&data)
		if err == nil {
			var full map[string]interface{}
			if json.Unmarshal(data, &full) == nil {
				if req, ok := full["request"].(map[string]interface{}); ok {
					var body map[string]interface{}
					switch v := req["body"].(type) {
					case string:
						json.Unmarshal([]byte(v), &body)
					case map[string]interface{}:
						body = v
					}
					if cs, ok := body["conversationState"].(map[string]interface{}); ok {
						if taskType, ok := cs["agentTaskType"].(string); ok {
							agentType = taskType
						}
					}
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"data": buildChatTurns(pane), "agentType": agentType})
}

func buildChatTurns(pane string) []chatTurn {
	rows, err := db.Query(`SELECT id, req_kb, res_kb, ts, data FROM http_log
		WHERE pane_id=? AND url LIKE '%q.us-east-1%'
		AND CAST(data AS CHAR) LIKE '%GenerateAssistantResponse%'
		ORDER BY id DESC LIMIT 50`, pane)
	if err != nil {
		return nil
	}
	defer rows.Close()

	type rawRow struct {
		id        int64
		reqKB     float64
		resKB     float64
		ts        int64
		data      []byte
		reqParsed map[string]interface{}
		resStr    string
	}
	var rawRows []rawRow
	for rows.Next() {
		var rr rawRow
		rows.Scan(&rr.id, &rr.reqKB, &rr.resKB, &rr.ts, &rr.data)
		var full map[string]interface{}
		if json.Unmarshal(rr.data, &full) != nil {
			continue
		}
		req, _ := full["request"].(map[string]interface{})
		switch v := req["body"].(type) {
		case string:
			json.Unmarshal([]byte(v), &rr.reqParsed)
		case map[string]interface{}:
			rr.reqParsed = v
		}
		res, _ := full["response"].(map[string]interface{})
		rr.resStr, _ = res["body"].(string)
		rawRows = append(rawRows, rr)
	}
	// Reverse to chronological
	for i, j := 0, len(rawRows)-1; i < j; i, j = i+1, j-1 {
		rawRows[i], rawRows[j] = rawRows[j], rawRows[i]
	}

	// Pass 1: build turns with basic info
	type rawTurn struct {
		q       string
		text    string
		credit  float64
		status  string
		model   string
		ts      int64
		hasTool bool
	}
	var turns []rawTurn
	for _, rr := range rawRows {
		q := ""
		if rr.reqParsed != nil {
			cs, _ := rr.reqParsed["conversationState"].(map[string]interface{})
			cm, _ := cs["currentMessage"].(map[string]interface{})
			um, _ := cm["userInputMessage"].(map[string]interface{})
			content, _ := um["content"].(string)
			if i := strings.Index(content, "USER MESSAGE BEGIN ---"); i >= 0 {
				m := content[i+21:]
				if e := strings.Index(m, "--- USER MESSAGE END"); e >= 0 {
					m = m[:e]
				}
				q = strings.TrimSpace(m)
				q = strings.TrimPrefix(q, "-\n")
			}
		}

		credit := 0.0
		if m := usageRe.FindStringSubmatch(rr.resStr); len(m) > 1 {
			json.Unmarshal([]byte(m[1]), &credit)
		}

		model := ""
		if m := modelRe.FindStringSubmatch(rr.resStr); len(m) > 1 {
			model = m[1]
		}

		hasTool := strings.Contains(rr.resStr, "toolUseEvent")
		status := "text"
		if hasTool {
			status = "tool_use"
		}

		chunks := contentRe.FindAllStringSubmatch(rr.resStr, -1)
		var buf strings.Builder
		for _, c := range chunks {
			buf.WriteString(c[1])
		}
		raw := buf.String()
		raw = strings.ReplaceAll(raw, `\"`, `"`)
		raw = strings.ReplaceAll(raw, `\n`, "\n")
		raw = strings.ReplaceAll(raw, `\t`, "\t")
		raw = strings.ReplaceAll(raw, `\\`, `\`)
		raw = strings.TrimSpace(raw)

		turns = append(turns, rawTurn{
			q: q, text: raw, credit: credit, status: status, model: model, ts: rr.ts, hasTool: hasTool,
		})
	}

	// Pass 2: fill tool args from N+1's history for turn N
	toolsPerTurn := make([][]toolInfo, len(turns))
	for i := 0; i < len(turns); i++ {
		if !turns[i].hasTool {
			continue
		}
		if i+1 < len(rawRows) && rawRows[i+1].reqParsed != nil {
			toolsPerTurn[i] = extractHistoryTools(rawRows[i+1].reqParsed)
		} else {
			for _, m := range toolNameIdRe.FindAllStringSubmatch(rawRows[i].resStr, -1) {
				toolsPerTurn[i] = append(toolsPerTurn[i], toolInfo{Name: m[1]})
			}
		}
	}

	// Pass 3: merge into chatTurns with ordered steps
	var result []chatTurn
	for i, t := range turns {
		if t.q != "" {
			ct := chatTurn{Q: t.q, Credit: t.credit, Status: t.status, Model: t.model, TS: t.ts, StartTS: t.ts}
			if t.text != "" && t.hasTool {
				ct.Steps = append(ct.Steps, step{Type: "text", Text: t.text})
			}
			if t.hasTool && len(toolsPerTurn[i]) > 0 {
				ct.Steps = append(ct.Steps, step{Type: "tool", Tools: toolsPerTurn[i]})
			}
			if t.text != "" && !t.hasTool {
				ct.Steps = append(ct.Steps, step{Type: "text", Text: t.text})
			}
			result = append(result, ct)
		} else if len(result) > 0 {
			last := &result[len(result)-1]
			last.Credit += t.credit
			last.TS = t.ts
			if t.text != "" && t.hasTool {
				last.Steps = append(last.Steps, step{Type: "text", Text: t.text})
			}
			if t.hasTool && len(toolsPerTurn[i]) > 0 {
				last.Steps = append(last.Steps, step{Type: "tool", Tools: toolsPerTurn[i]})
			}
			if t.text != "" && !t.hasTool {
				last.Steps = append(last.Steps, step{Type: "text", Text: t.text})
				last.Status = "text"
			} else if t.hasTool {
				last.Status = "tool_use"
			}
		}
	}
	return result
}

func handleChatStream(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	if pane == "" {
		http.Error(w, "pane required", 400)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	var lastCount int
	for {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
			var count int
			db.QueryRow(`SELECT COUNT(*) FROM http_log WHERE pane_id=? AND url LIKE '%q.us-east-1%'`, pane).Scan(&count)
			if count != lastCount {
				lastCount = count
				resp := buildChatTurns(pane)
				b, _ := json.Marshal(resp)
				w.Write([]byte("data: " + string(b) + "\n\n"))
				flusher.Flush()
			}
		}
	}
}
