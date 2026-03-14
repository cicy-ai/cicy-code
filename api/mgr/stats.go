package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type httpLogEntry struct {
	Type   string  `json:"type"`
	Pane   string  `json:"pane"`
	Method string  `json:"method"`
	URL    string  `json:"url"`
	ReqKB  float64 `json:"req_kb"`
	ResKB  float64 `json:"res_kb"`
	Status int     `json:"status"`
	TS     int64   `json:"ts"`
}

type minuteStats struct {
	Minute string  `json:"minute"`
	ReqKB  float64 `json:"req_kb"`
	ResKB  float64 `json:"res_kb"`
	Count  int     `json:"count"`
}

func redisPublish(channel, message string) {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	conn, err := net.DialTimeout("tcp", host+":"+port, 2*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	req := fmt.Sprintf("*3\r\n$7\r\nPUBLISH\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n", len(channel), channel, len(message), message)
	conn.Write([]byte(req))
}

func redisLRange(key string) []string {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	conn, err := net.DialTimeout("tcp", host+":"+port, 2*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Limit to last 5000 entries to prevent OOM
	req := fmt.Sprintf("*4\r\n$6\r\nLRANGE\r\n$%d\r\n%s\r\n$5\r\n-5000\r\n$2\r\n-1\r\n", len(key), key)
	conn.Write([]byte(req))

	buf := make([]byte, 1024*1024)
	n, _ := conn.Read(buf)
	resp := string(buf[:n])
	
	if !strings.HasPrefix(resp, "*") {
		return nil
	}
	
	lines := strings.Split(resp, "\r\n")
	count, _ := strconv.Atoi(lines[0][1:])
	
	result := []string{}
	i := 1
	for len(result) < count && i < len(lines)-1 {
		if strings.HasPrefix(lines[i], "$") {
			size, _ := strconv.Atoi(lines[i][1:])
			if size >= 0 && i+1 < len(lines) {
				result = append(result, lines[i+1])
			}
			i += 2
		} else {
			i++
		}
	}
	return result
}

func handleStatsTraffic(w http.ResponseWriter, r *http.Request) {
	minutes := 60
	if m := r.URL.Query().Get("minutes"); m != "" {
		if v, err := strconv.Atoi(m); err == nil {
			minutes = v
		}
	}
	interval := 1
	if m := r.URL.Query().Get("interval"); m != "" {
		if v, err := strconv.Atoi(m); err == nil && v > 0 {
			interval = v
		}
	}
	paneFilter := r.URL.Query().Get("pane")

	items := redisLRange("kiro_http_log")
	if items == nil {
		J(w, M{"success": true, "data": []minuteStats{}})
		return
	}

	cutoff := time.Now().Unix() - int64(minutes*60)
	agg := map[string]*minuteStats{}

	for _, item := range items {
		var log httpLogEntry
		if err := json.Unmarshal([]byte(item), &log); err != nil {
			continue
		}
		if log.TS < cutoff {
			continue
		}
		if paneFilter != "" && log.Pane != paneFilter {
			continue
		}
		min := time.Unix(log.TS-log.TS%int64(interval*60), 0).Format("2006-01-02T15:04")
		if agg[min] == nil {
			agg[min] = &minuteStats{Minute: min}
		}
		agg[min].ReqKB += log.ReqKB
		agg[min].ResKB += log.ResKB
		agg[min].Count++
	}

	result := []minuteStats{}
	for _, v := range agg {
		result = append(result, *v)
	}

	J(w, M{"success": true, "data": result})
}

func handleStatsTrafficRaw(w http.ResponseWriter, r *http.Request) {
	paneFilter := r.URL.Query().Get("pane")
	items := redisLRange("kiro_http_log")
	if items == nil {
		J(w, M{"success": true, "data": []httpLogEntry{}})
		return
	}

	result := []httpLogEntry{}
	for _, item := range items {
		var log httpLogEntry
		if err := json.Unmarshal([]byte(item), &log); err != nil {
			continue
		}
		if paneFilter != "" && log.Pane != paneFilter {
			continue
		}
		result = append(result, log)
	}

	J(w, M{"success": true, "data": result})
}

func handleTrafficLive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	conn, err := net.DialTimeout("tcp", host+":"+port, 2*time.Second)
	if err != nil {
		http.Error(w, "redis error", 500)
		return
	}
	defer conn.Close()

	// SUBSCRIBE kiro_traffic_live
	conn.Write([]byte("SUBSCRIBE kiro_traffic_live\r\n"))

	ctx := r.Context()
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				fmt.Fprintf(w, ": keepalive\n\n")
				flusher.Flush()
				continue
			}
			return
		}
		raw := string(buf[:n])
		lines := strings.Split(raw, "\r\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "{") {
				fmt.Fprintf(w, "data: %s\n\n", line)
				flusher.Flush()
			}
		}
	}
}

func handleNotify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pane    string `json:"pane"`
		Action  string `json:"action"`
		Tab     string `json:"tab"`
		Message string `json:"message"`
		File    string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	// open_file: use code-server IPC to open file directly
	if body.Action == "open_file" && body.File != "" {
		go openInCodeServer(body.File)
	}
	data, _ := json.Marshal(body)
	redisPublish("kiro_notify", string(data))
	J(w, M{"success": true})
}

func openInCodeServer(file string) {
	// Find IPC socket inside container
	out, err := exec.Command("docker", "exec", "cicy-code-server", "bash", "-c",
		`find /tmp -name "vscode-ipc-*.sock" -type s -printf "%T@ %p\n" 2>/dev/null | sort -rn | head -1 | cut -d' ' -f2`).Output()
	if err != nil || len(out) == 0 {
		log.Printf("[code-server] no IPC socket found")
		return
	}
	sock := strings.TrimSpace(string(out))
	cmd := exec.Command("docker", "exec",
		"-e", "VSCODE_IPC_HOOK_CLI="+sock,
		"cicy-code-server",
		"/usr/lib/code-server/lib/vscode/bin/remote-cli/code-linux.sh",
		"--reuse-window", "--goto", file+":1:1")
	if err := cmd.Run(); err != nil {
		log.Printf("[code-server] open file error: %v", err)
	}
}

func handleNotifyStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	filterPane := r.URL.Query().Get("pane")
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	host := os.Getenv("REDIS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	conn, err := net.DialTimeout("tcp", host+":"+port, 2*time.Second)
	if err != nil {
		http.Error(w, "redis error", 500)
		return
	}
	defer conn.Close()
	conn.Write([]byte("SUBSCRIBE kiro_notify\r\n"))

	ctx := r.Context()
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				fmt.Fprintf(w, ": keepalive\n\n")
				flusher.Flush()
				continue
			}
			return
		}
		lines := strings.Split(string(buf[:n]), "\r\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "{") {
				// Filter by pane if specified
				if filterPane != "" {
					var msg struct{ Pane string `json:"pane"` }
					json.Unmarshal([]byte(line), &msg)
					if msg.Pane != "" && msg.Pane != filterPane {
						continue
					}
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
				flusher.Flush()
			}
		}
	}
}

func paneWorkspace(pane string) string {
	var ws string
	db.QueryRow("SELECT workspace FROM agent_config WHERE pane_id=?", pane).Scan(&ws)
	if ws == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	return os.ExpandEnv(strings.Replace(ws, "~", home, 1))
}

func handleCicyFiles(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	ws := paneWorkspace(pane)
	if ws == "" {
		J(w, M{"files": []string{}})
		return
	}
	dir := filepath.Join(ws, ".cicy")
	entries, err := os.ReadDir(dir)
	if err != nil {
		J(w, M{"files": []string{}})
		return
	}
	files := []M{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, _ := e.Info()
		files = append(files, M{"name": e.Name(), "size": info.Size(), "modified": info.ModTime()})
	}
	J(w, M{"files": files, "path": dir})
}

func handleCicyFile(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	name := r.URL.Query().Get("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "bad name", 400)
		return
	}
	ws := paneWorkspace(pane)
	if ws == "" {
		http.Error(w, "pane not found", 404)
		return
	}
	f, err := os.Open(filepath.Join(ws, ".cicy", name))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.Copy(w, f)
}

func handlePair(w http.ResponseWriter, r *http.Request) {
	pane := normPaneID(r.URL.Query().Get("pane"))
	if pane == "" {
		httpErr(w, 400, "pane required")
		return
	}
	var ws sql.NullString
	var myRole sql.NullString
	db.QueryRow("SELECT workspace, role FROM agent_config WHERE pane_id=?", pane).Scan(&ws, &myRole)
	if ws.String == "" {
		httpErr(w, 404, "pane not found")
		return
	}
	rows, err := db.Query(`SELECT pane_id, title, role, default_model FROM agent_config WHERE workspace=? AND active=1 AND role IS NOT NULL AND role!=''`, ws.String)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	var master, worker M
	for rows.Next() {
		var pid, title, role, model sql.NullString
		rows.Scan(&pid, &title, &role, &model)
		info := M{"pane_id": shortPaneID(pid.String), "title": title.String, "role": role.String, "default_model": model.String}
		if role.String == "master" {
			master = info
		} else if role.String == "worker" {
			worker = info
		}
	}
	J(w, M{"master": master, "worker": worker})
}
