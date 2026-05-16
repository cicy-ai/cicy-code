package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func frpServerBin() string {
	home, err := os.UserHomeDir()
	if err == nil {
		cand := filepath.Join(home, ".local", "bin", "frp-server")
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	if p, err := exec.LookPath("frp-server"); err == nil {
		return p
	}
	return ""
}

func runFrpServer(ctx context.Context, args ...string) (string, error) {
	bin := frpServerBin()
	if bin == "" {
		return "", errorf("frp-server not installed — run `cicy-skills install frp-server` first")
	}
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	return string(out), err
}

// parseFrpServerStatus turns the key: value text from `frp-server status`
// into a flat map. Multiline sections (listeners:, proxy status:) are folded
// into a single "extra" string to avoid leaking raw config details.
func parseFrpServerStatus(raw string) map[string]any {
	m := map[string]any{}
	var extra []string
	inSection := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			continue
		}
		// section headers (listeners:, proxy status:)
		if strings.HasSuffix(strings.TrimSpace(line), ":") && !strings.Contains(line, " ") {
			inSection = true
			continue
		}
		if inSection {
			extra = append(extra, strings.TrimSpace(line))
			continue
		}
		kv := strings.SplitN(line, ": ", 2)
		if len(kv) == 2 {
			m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	if len(extra) > 0 {
		m["listeners"] = strings.Join(extra, "\n")
	}
	return m
}

// handleFrpServerStatus — GET /api/frp-server/status
func handleFrpServerStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := runFrpServer(ctx, "status")
	if err != nil && strings.TrimSpace(out) == "" {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	parsed := parseFrpServerStatus(out)
	// normalise the state field
	state, _ := parsed["state"].(string)
	J(w, M{"success": true, "running": state == "running", "info": parsed})
}

// handleFrpServerLifecycle — POST /api/frp-server/lifecycle
// Body: {"action":"start"|"stop"|"restart"|"reload"}
func handleFrpServerLifecycle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "bad body: "+err.Error())
		return
	}
	action := strings.TrimSpace(req.Action)
	switch action {
	case "start", "stop", "restart", "reload":
	default:
		httpErr(w, http.StatusBadRequest, "action must be start|stop|restart|reload")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, err := runFrpServer(ctx, action)
	result := M{"success": err == nil, "action": action, "output": strings.TrimSpace(out)}
	if err != nil {
		result["error"] = err.Error()
	}
	J(w, result)
}

// handleFrpServerConnections — GET /api/frp-server/connections
func handleFrpServerConnections(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	out, err := runFrpServer(ctx, "connections")
	if err != nil {
		httpErr(w, http.StatusInternalServerError, strings.TrimSpace(out)+": "+err.Error())
		return
	}
	J(w, M{"success": true, "output": strings.TrimSpace(out)})
}

// handleFrpServerClients — GET /api/frp-server/clients
// Parses tab-separated output: key user clientID hostname clientIP online
func handleFrpServerClients(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	out, err := runFrpServer(ctx, "clients")
	text := strings.TrimSpace(out)
	if err != nil {
		// "not running" or "dashboard not configured" are soft errors
		J(w, M{"success": false, "error": text + ": " + err.Error(), "clients": []any{}})
		return
	}
	var clients []map[string]any
	for _, line := range strings.Split(text, "\n") {
		cols := strings.Split(line, "\t")
		if len(cols) < 6 {
			continue
		}
		clients = append(clients, map[string]any{
			"key":      cols[0],
			"user":     cols[1],
			"clientID": cols[2],
			"hostname": cols[3],
			"clientIP": cols[4],
			"online":   cols[5] == "true",
		})
	}
	if clients == nil {
		clients = []map[string]any{}
	}
	J(w, M{"success": true, "clients": clients})
}

// handleFrpServerLogs — GET /api/frp-server/logs?lines=50
func handleFrpServerLogs(w http.ResponseWriter, r *http.Request) {
	lines := r.URL.Query().Get("lines")
	if lines == "" {
		lines = "50"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := runFrpServer(ctx, "logs", "--lines", lines)
	if err != nil && strings.TrimSpace(out) == "" {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, M{"success": true, "logs": strings.TrimSpace(out)})
}

func errorf(s string) error {
	return &stringError{s}
}

type stringError struct{ msg string }

func (e *stringError) Error() string { return e.msg }
