package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type toolInfo struct {
	Name string `json:"name"`
	Arg  string `json:"arg,omitempty"`
}

type chatTurn struct {
	Q       string     `json:"q"`
	A       string     `json:"a,omitempty"`
	Tools   []toolInfo `json:"tools,omitempty"`
	Credit  float64    `json:"credit"`
	FirstMs int        `json:"first_ms"`
	Status  string     `json:"status"`
	TS      int64      `json:"ts"`
}

var usageRe = regexp.MustCompile(`"usage":([\d.]+)`)
var contentRe = regexp.MustCompile(`assistantResponseEvent.*?"content":"(.*?)"`)
var toolNameIdRe = regexp.MustCompile(`"name":"([^"]+)","toolUseId"`)

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
		if len([]rune(c)) > 80 {
			c = string([]rune(c)[:80]) + "..."
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

// Extract toolUses from the last assistantResponseMessage in history
func extractHistoryTools(reqParsed map[string]interface{}) []toolInfo {
	cs, _ := reqParsed["conversationState"].(map[string]interface{})
	hist, _ := cs["history"].([]interface{})
	for i := len(hist) - 1; i >= 0; i-- {
		entry, _ := hist[i].(map[string]interface{})
		arm, _ := entry["assistantResponseMessage"].(map[string]interface{})
		tus, _ := arm["toolUses"].([]interface{})
		if len(tus) > 0 {
			var tools []toolInfo
			for _, tu := range tus {
				tm, _ := tu.(map[string]interface{})
				name, _ := tm["name"].(string)
				inp, _ := tm["input"].(map[string]interface{})
				tools = append(tools, toolInfo{Name: name, Arg: extractArg(inp, name)})
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"data": buildChatTurns(pane)})
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
	var turns []chatTurn
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
			}
		}

		credit := 0.0
		if m := usageRe.FindStringSubmatch(rr.resStr); len(m) > 1 {
			json.Unmarshal([]byte(m[1]), &credit)
		}

		hasTool := strings.Contains(rr.resStr, "toolUseEvent")
		status := "text"
		if hasTool {
			status = "tool_use"
		}

		a := ""
		if !hasTool {
			chunks := contentRe.FindAllStringSubmatch(rr.resStr, -1)
			for _, c := range chunks {
				a += c[1]
			}
			a = strings.ReplaceAll(a, `\n`, "\n")
			a = strings.ReplaceAll(a, `\t`, "\t")
			a = strings.ReplaceAll(a, `\"`, `"`)
		}

		turns = append(turns, chatTurn{
			Q: q, A: a, Credit: credit, Status: status, TS: rr.ts,
		})
	}

	// Pass 2: fill tool args from N+1's history for turn N
	for i := 0; i < len(turns); i++ {
		if turns[i].Status != "tool_use" {
			continue
		}
		// Look at next request's history to get this turn's tool details
		if i+1 < len(rawRows) && rawRows[i+1].reqParsed != nil {
			turns[i].Tools = extractHistoryTools(rawRows[i+1].reqParsed)
		} else {
			// Last turn - only have tool names from response
			for _, m := range toolNameIdRe.FindAllStringSubmatch(rawRows[i].resStr, -1) {
				turns[i].Tools = append(turns[i].Tools, toolInfo{Name: m[1]})
			}
		}
	}

	// Pass 3: group - mark tool_result continuations (no user q)
	var result []chatTurn
	for _, t := range turns {
		if t.Q != "" {
			result = append(result, t)
		} else {
			t.Q = ""
			result = append(result, t)
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
