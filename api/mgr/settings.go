package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

func handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		var val []byte
		err := store.QueryRow("SELECT `value` FROM global_vars WHERE `key_name`='global_settings'").Scan(&val)
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
		store.Exec(store.Upsert("global_vars", "key_name", []string{"key_name", "value"}, []string{"value"}), "global_settings", string(data))
		J(w, M{"success": true})
	}
}

func handleFileExists(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = home + path[1:]
	}
	_, err := os.Stat(path)
	J(w, M{"exists": err == nil, "path": path})
}

func handleCorrectEnglish(w http.ResponseWriter, r *http.Request) {
	// Stub - would need Cloudflare AI integration
	J(w, M{"success": false, "error": "not implemented in cicy-code-api"})
}
