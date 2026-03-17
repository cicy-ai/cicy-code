package main

import (
	"encoding/json"
	"log"
	"time"
)

func toJSON(s string) string {
	if json.Valid([]byte(s)) {
		return s
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func initHTTPLogConsumer() {
	go func() {
		for {
			consumeHTTPLogs()
			time.Sleep(200 * time.Millisecond)
		}
	}()
	// Auto-cleanup: delete http_log older than 7 days, every hour
	go func() {
		for {
			res, err := db.Exec("DELETE FROM http_log WHERE ts < UNIX_TIMESTAMP(NOW() - INTERVAL 7 DAY)")
			if err == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					log.Printf("[http-log] cleanup: deleted %d old rows", n)
				}
			}
			time.Sleep(1 * time.Hour)
		}
	}()
	log.Println("[http-log] consumer started")
}

func consumeHTTPLogs() {
	panes := map[string]bool{}
	for {
		raw := redisDo("RPOP", redisKey("kiro_http_log"))
		if raw == "" {
			break
		}
		var e struct {
			Pane       string            `json:"pane"`
			Method     string            `json:"method"`
			URL        string            `json:"url"`
			Status     int               `json:"status"`
			ReqKB      float64           `json:"req_kb"`
			ResKB      float64           `json:"res_kb"`
			ReqHeaders map[string]string `json:"req_headers"`
			ResHeaders map[string]string `json:"res_headers"`
			ReqBody    string            `json:"req_body"`
			ResBody    string            `json:"res_body"`
			TS         int64             `json:"ts"`
		}
		if json.Unmarshal([]byte(raw), &e) != nil {
			continue
		}
		data, _ := json.Marshal(map[string]interface{}{
			"request":  map[string]interface{}{"headers": e.ReqHeaders, "body": json.RawMessage(toJSON(e.ReqBody))},
			"response": map[string]interface{}{"headers": e.ResHeaders, "body": json.RawMessage(toJSON(e.ResBody))},
		})
		_, err := db.Exec(
			`INSERT INTO http_log (pane_id, method, url, status_code, req_kb, res_kb, data, ts)
			 VALUES (?,?,?,?,?,?,?,?)`,
			e.Pane, e.Method, e.URL, e.Status, e.ReqKB, e.ResKB,
			string(data), e.TS,
		)
		if err != nil {
			log.Printf("[http-log] insert error: %v", err)
		}
		panes[e.Pane] = true
	}
	for p := range panes {
		hub.broadcast(p, ChatEvent{Type: "http_log"})
	}
}
