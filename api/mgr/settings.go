package main

import (
	"encoding/json"
	"net/http"
	"os"
)

func handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		var val []byte
		err := db.QueryRow("SELECT `value` FROM global_var WHERE `key`='global_settings'").Scan(&val)
		if err != nil || val == nil {
			J(w, M{"favor": M{"dir": []string{}, "cmd": []string{}}})
			return
		}
		var result interface{}
		json.Unmarshal(val, &result)
		J(w, result)
	case "POST":
		var req interface{}
		readBody(r, &req)
		data, _ := json.Marshal(req)
		db.Exec("INSERT INTO global_var (`key`, `value`) VALUES ('global_settings', ?) ON DUPLICATE KEY UPDATE `value`=VALUES(`value`)", string(data))
		J(w, M{"success": true})
	}
}

func handleFileExists(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	_, err := os.Stat(path)
	J(w, M{"exists": err == nil, "path": path})
}

func handleCorrectEnglish(w http.ResponseWriter, r *http.Request) {
	// Stub - would need Cloudflare AI integration
	J(w, M{"success": false, "error": "not implemented in ttyd-manager"})
}
