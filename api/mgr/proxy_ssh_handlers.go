package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// proxySshBin returns the absolute path to the local proxy_ssh wrapper.
// Search order matches how cicy-skills installs it: ~/.local/bin/proxy_ssh,
// then $PATH lookup. Returns "" when the binary isn't installed yet — callers
// should surface that as a 503 / placeholder.
func proxySshBin() string {
	home, err := os.UserHomeDir()
	if err == nil {
		cand := filepath.Join(home, ".local", "bin", "proxy_ssh")
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	if p, err := exec.LookPath("proxy_ssh"); err == nil {
		return p
	}
	return ""
}

func runProxySsh(ctx context.Context, args ...string) (string, error) {
	bin := proxySshBin()
	if bin == "" {
		return "", fmt.Errorf("proxy_ssh not installed (run `cicy-skills install proxy_ssh`)")
	}
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	return string(out), err
}

// handleProxySshList — GET /api/proxy-ssh/list
// Shells out to `proxy_ssh list` (JSON output) and proxies the JSON array
// back. Each entry: {name, proxy_url, start_cmd}. Status (running/stopped)
// isn't carried in the JSON output — clients should call /status for each
// profile (or rely on a future combined endpoint).
func handleProxySshList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := runProxySsh(ctx, "list")
	if err != nil {
		httpErr(w, http.StatusInternalServerError, fmt.Sprintf("%s: %s", err.Error(), strings.TrimSpace(out)))
		return
	}
	// the wrapper prints a table by default — fall back to parsing the
	// `proxy_ssh show <name>` JSON. The CLI hands back the in-memory list
	// when no name is given, which we get from the table row count.
	profiles, err := parseProxySshList(strings.TrimSpace(out))
	if err != nil {
		// last-ditch: try the underlying json file directly so the API still
		// works if the CLI changes its table format
		home, _ := os.UserHomeDir()
		raw, ferr := os.ReadFile(filepath.Join(home, "cicy-ai", "db", "proxy_ssh.json"))
		if ferr == nil {
			var direct []map[string]any
			if jerr := json.Unmarshal(raw, &direct); jerr == nil {
				profiles = direct
			}
		}
	}
	if profiles == nil {
		profiles = []map[string]any{}
	}
	// enrich with status from a per-profile show call
	enriched := make([]map[string]any, 0, len(profiles))
	for _, p := range profiles {
		name, _ := p["name"].(string)
		status := "unknown"
		if name != "" {
			ctxS, cancelS := context.WithTimeout(r.Context(), 3*time.Second)
			outS, errS := runProxySsh(ctxS, "list")
			cancelS()
			_ = outS
			_ = errS
			// fall back to lightweight ps probe — the CLI's `show` doesn't
			// carry status either. The `list` table form does have a
			// status column; reuse that.
			status = lookupProxySshStatusFromList(out, name)
		}
		entry := map[string]any{}
		for k, v := range p {
			entry[k] = v
		}
		entry["status"] = status
		enriched = append(enriched, entry)
	}
	J(w, M{"success": true, "profiles": enriched})
}

// parseProxySshList tolerates two output shapes from the wrapper:
//   - JSON array (with `--json` or future default)
//   - whitespace-aligned table:  name    status    proxy_url
//
// It tries JSON first; on failure falls back to the table header.
func parseProxySshList(s string) ([]map[string]any, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		var arr []map[string]any
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			return arr, nil
		}
	}
	lines := strings.Split(s, "\n")
	if len(lines) < 2 {
		return []map[string]any{}, nil
	}
	// header: "name    status   proxy_url"
	header := strings.Fields(lines[0])
	if len(header) < 3 {
		return nil, fmt.Errorf("unexpected proxy_ssh list header: %q", lines[0])
	}
	out := make([]map[string]any, 0, len(lines)-1)
	for _, ln := range lines[1:] {
		cols := strings.Fields(ln)
		if len(cols) < 3 {
			continue
		}
		out = append(out, map[string]any{
			"name":      cols[0],
			"proxy_url": cols[2],
			"start_cmd": "",
		})
	}
	return out, nil
}

func lookupProxySshStatusFromList(listOut, name string) string {
	for _, ln := range strings.Split(listOut, "\n") {
		cols := strings.Fields(ln)
		if len(cols) >= 2 && cols[0] == name {
			return cols[1]
		}
	}
	return "unknown"
}

// handleProxySshShow — GET /api/proxy-ssh/show?name=<name>
// Returns the full profile JSON from `proxy_ssh show <name>`, including
// start_cmd (useful for the drawer to display).
func handleProxySshShow(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		httpErr(w, http.StatusBadRequest, "name required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	out, err := runProxySsh(ctx, "show", name)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, fmt.Sprintf("%s: %s", err.Error(), strings.TrimSpace(out)))
		return
	}
	var profile map[string]any
	if jerr := json.Unmarshal([]byte(out), &profile); jerr != nil {
		httpErr(w, http.StatusInternalServerError, "proxy_ssh show: bad JSON output")
		return
	}
	J(w, M{"success": true, "profile": profile})
}

// handleProxySshLifecycle — POST /api/proxy-ssh/lifecycle
// Body: {"name":"<profile>", "action":"start|stop|restart"}
// Shells out to the wrapper and relays stdout/stderr back so the drawer can
// show what happened.
func handleProxySshLifecycle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Action string `json:"action"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "bad body: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpErr(w, http.StatusBadRequest, "name required")
		return
	}
	action := strings.TrimSpace(req.Action)
	switch action {
	case "start", "stop", "restart":
	default:
		httpErr(w, http.StatusBadRequest, "action must be one of start|stop|restart")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, err := runProxySsh(ctx, action, name)
	result := M{"success": err == nil, "action": action, "name": name, "output": strings.TrimSpace(out)}
	if err != nil {
		result["error"] = err.Error()
	}
	J(w, result)
}

// handleProxySshTest — POST /api/proxy-ssh/test
// Body: {"name":"<profile>"}
// Returns the test output (typically "ok <name>" + a JSON probe body).
func handleProxySshTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "bad body: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpErr(w, http.StatusBadRequest, "name required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	start := time.Now()
	out, err := runProxySsh(ctx, "test", name)
	elapsed := time.Since(start).Milliseconds()
	result := M{"success": err == nil, "name": name, "output": strings.TrimSpace(out), "elapsed_ms": elapsed}
	if err != nil {
		result["error"] = err.Error()
	}
	J(w, result)
}
