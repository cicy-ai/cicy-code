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
			time.Sleep(2 * time.Second)
		}
	}()
	log.Println("[http-log] consumer started")
}

func consumeHTTPLogs() {
	for {
		raw := redisDo("RPOP", "kiro_http_log")
		if raw == "" {
			return
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
	}
}
