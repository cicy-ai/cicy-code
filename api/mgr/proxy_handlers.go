package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultMihomoMixedPort = "9001"
const defaultMihomoControllerPort = "19001"

// mihomoController returns the mihomo external-controller base URL. Tests run
// on the same host as the api (no docker hop), so the loopback default is
// correct in both prod (api+mihomo on host) and dev container (api+mihomo
// co-located inside the container). Override with CICY_MIHOMO_CONTROLLER if
// you ever need to point it elsewhere.
func mihomoController() string {
	if v := strings.TrimSpace(os.Getenv("CICY_MIHOMO_CONTROLLER")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:" + defaultMihomoControllerPort
}

// mihomoMixedAddr returns "host:port" for the mihomo mixed proxy port, used
// when we need to actually run traffic through mihomo (e.g. exit-IP test).
func mihomoMixedAddr() string {
	host := strings.TrimSpace(os.Getenv("CICY_MIHOMO_HOST"))
	if host == "" {
		host = "127.0.0.1"
	}
	port := strings.TrimSpace(os.Getenv("CICY_MIHOMO_PORT"))
	if port == "" {
		port = defaultMihomoMixedPort
	}
	return host + ":" + port
}

// readMihomoGlobalPasswordFromYAML pulls the top-level `globalPassword:` value
// from ~/cicy-ai/db/mihomo.yaml. Empty when missing — callers should fall back
// gracefully (e.g. skip the exit-IP test rather than fail the whole request).
//
// CICY_MIHOMO_GLOBAL_PASSWORD env wins when set — used in dev/docker mode where
// the api container can't read the host's yaml. dev.py reads the host file
// once at startup and passes the value through.
func readMihomoGlobalPasswordFromYAML() string {
	if v := strings.TrimSpace(os.Getenv("CICY_MIHOMO_GLOBAL_PASSWORD")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, "cicy-ai", "db", "mihomo.yaml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "globalPassword:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(t, "globalPassword:"))
		return strings.Trim(v, "\"' ")
	}
	return ""
}

// isMihomoGroupType reports whether a proxy `type` field returned by the
// controller represents a selectable group (rather than a leaf node). Mirrors
// mihomo's adapter classification; built-in pseudo-proxies (DIRECT/REJECT/PASS
// /COMPATIBLE/GLOBAL) are filtered separately by name.
func isMihomoGroupType(t string) bool {
	switch t {
	case "Selector", "URLTest", "Fallback", "LoadBalance", "Relay":
		return true
	}
	return false
}

// isMihomoBuiltinName reports whether a proxy name is a mihomo built-in we
// don't want to surface in the UI picker (the GLOBAL group, DIRECT/REJECT
// adapters, etc.).
func isMihomoBuiltinName(name string) bool {
	switch name {
	case "GLOBAL", "DIRECT", "REJECT", "REJECT-DROP", "PASS", "COMPATIBLE":
		return true
	}
	return false
}

// handleProxyList — GET /api/proxy/list
// Returns {success, groups: [...], nodes: [...]}. Each entry has name, type,
// and last_delay_ms (0 if never tested). Groups also expose `now` and `members`.
func handleProxyList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, mihomoController()+"/proxies", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		httpErr(w, 502, "mihomo controller unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		httpErr(w, 502, fmt.Sprintf("mihomo controller status %d", resp.StatusCode))
		return
	}
	var body struct {
		Proxies map[string]struct {
			Type    string   `json:"type"`
			Now     string   `json:"now"`
			All     []string `json:"all"`
			History []struct {
				Time  string `json:"time"`
				Delay int    `json:"delay"`
			} `json:"history"`
		} `json:"proxies"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		httpErr(w, 502, "parse: "+err.Error())
		return
	}
	groups := []M{}
	nodes := []M{}
	for name, p := range body.Proxies {
		if isMihomoBuiltinName(name) {
			continue
		}
		var lastDelay int
		if len(p.History) > 0 {
			lastDelay = p.History[len(p.History)-1].Delay
		}
		item := M{
			"name":          name,
			"type":          p.Type,
			"last_delay_ms": lastDelay,
		}
		if isMihomoGroupType(p.Type) {
			item["now"] = p.Now
			// Filter out builtin members so the UI doesn't get DIRECT/REJECT
			// noise in group expand views.
			cleanMembers := make([]string, 0, len(p.All))
			for _, m := range p.All {
				if !isMihomoBuiltinName(m) {
					cleanMembers = append(cleanMembers, m)
				}
			}
			item["members"] = cleanMembers
			groups = append(groups, item)
		} else {
			nodes = append(nodes, item)
		}
	}
	J(w, M{"success": true, "groups": groups, "nodes": nodes})
}

// handleProxyTest — POST /api/proxy/test
//
// Body: {"name": "<proxy_or_group>", "urls": ["https://...", ...]}
//
// For each URL, asks mihomo's controller for a latency probe via
// /proxies/<name>/delay (which is non-mutating — it dials a one-off TCP+HTTP
// HEAD and returns the rtt without changing the active selection).
//
// Also runs an exit-IP probe: it temporarily PUTs the named proxy into
// `default_proxy_group`, fetches https://api.myip.com through the mihomo
// mixed port, then restores the previous selection. Concurrent tests can
// race on the selection — accept that for a debugging tool; callers are
// expected to not fan out N tests in parallel.
func handleProxyTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string   `json:"name"`
		URLs []string `json:"urls"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "bad body: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpErr(w, 400, "name required")
		return
	}
	if len(req.URLs) == 0 {
		req.URLs = []string{
			"https://api.anthropic.com",
			"https://chatgpt.com",
			"https://api.myip.com",
		}
	}

	results := []M{}
	for _, target := range req.URLs {
		results = append(results, mihomoDelayProbe(r.Context(), name, target))
	}
	ip := mihomoExitIPProbe(name)
	J(w, M{
		"success": true,
		"name":    name,
		"results": results,
		"ip":      ip,
	})
}

// mihomoDelayProbe asks the controller to time a TCP+HEAD against `target`
// via `name`, with a 5-second budget. Returns a result row suitable for the
// UI: {url, ok, delay_ms?, error?}.
func mihomoDelayProbe(parent context.Context, name, target string) M {
	delayURL := fmt.Sprintf("%s/proxies/%s/delay?timeout=5000&url=%s",
		mihomoController(),
		url.PathEscape(name),
		url.QueryEscape(target),
	)
	ctx, cancel := context.WithTimeout(parent, 7*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, delayURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return M{"url": target, "ok": false, "error": err.Error()}
	}
	defer resp.Body.Close()
	var d struct {
		Delay   int    `json:"delay"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&d)
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(d.Message)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return M{"url": target, "ok": false, "error": msg}
	}
	return M{"url": target, "ok": true, "delay_ms": d.Delay}
}

// mihomoExitIPProbe runs an actual HTTP request to api.myip.com through the
// mihomo mixed port, routed via `name`. It does this by briefly mutating the
// `default_proxy_group` selection (PUT /proxies/default_proxy_group), then
// running curl with the mihomo proxy URL, then restoring the previous
// selection. Returns {ok, ip, country, cc} on success.
//
// curl is used (vs http.Client) so we get robust auth + CONNECT semantics
// without re-implementing CONNECT-via-proxy here.
func mihomoExitIPProbe(name string) M {
	password := readMihomoGlobalPasswordFromYAML()
	if password == "" {
		return M{"ok": false, "error": "no globalPassword in mihomo.yaml"}
	}

	// `default_proxy_group` is the group that worker traffic flows through
	// (see IN-USER-PREFIX,w-,default_proxy_group). Switching it to itself is a
	// no-op the controller rejects with HTTP 400, so skip the switch when the
	// caller asked about the group itself — the probe naturally exercises
	// whatever node is currently selected. For any other target we temporarily
	// repoint the group to it, probe, then restore.
	skipSwitch := name == "default_proxy_group"
	var prevSelection string
	if !skipSwitch {
		prevSelection = readMihomoGroupSelection("default_proxy_group")
		if err := setMihomoGroupSelection("default_proxy_group", name); err != nil {
			return M{"ok": false, "error": "switch: " + err.Error()}
		}
	}
	defer func() {
		if !skipSwitch && prevSelection != "" && prevSelection != name {
			_ = setMihomoGroupSelection("default_proxy_group", prevSelection)
		}
	}()

	proxyURL := fmt.Sprintf("http://w-proxytest:%s@%s", password, mihomoMixedAddr())
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "curl",
		"-sS", "-m", "8",
		"-x", proxyURL,
		"https://api.myip.com",
	)
	out, err := cmd.Output()
	if err != nil {
		return M{"ok": false, "error": "fetch: " + strings.TrimSpace(err.Error())}
	}
	body := strings.TrimSpace(string(out))
	var parsed struct {
		IP      string `json:"ip"`
		Country string `json:"country"`
		CC      string `json:"cc"`
	}
	if json.Unmarshal([]byte(body), &parsed) == nil && parsed.IP != "" {
		return M{"ok": true, "ip": parsed.IP, "country": parsed.Country, "cc": parsed.CC}
	}
	return M{"ok": true, "raw": body}
}

// readMihomoGroupSelection returns the currently-selected member of a group,
// or "" on any error. Best-effort.
func readMihomoGroupSelection(group string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		mihomoController()+"/proxies/"+url.PathEscape(group), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var body struct {
		Now string `json:"now"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return body.Now
}

func setMihomoGroupSelection(group, member string) error {
	payload := fmt.Sprintf(`{"name":%q}`, member)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut,
		mihomoController()+"/proxies/"+url.PathEscape(group),
		strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// cicyMihomoBin resolves the wrapper binary path. CICY_MIHOMO_BIN overrides;
// otherwise the PATH lookup runs (the wrapper is typically symlinked into
// ~/.local/bin/cicy-mihomo by the cicy-skills installer).
func cicyMihomoBin() (string, error) {
	if v := strings.TrimSpace(os.Getenv("CICY_MIHOMO_BIN")); v != "" {
		return v, nil
	}
	return exec.LookPath("cicy-mihomo")
}

// runCicyMihomo invokes the wrapper with the given subcommand and a short
// budget. Returns (stdout+stderr, error). The wrapper always exits 0 for
// `status` (running or stopped); other subcommands surface failures through
// the exit code, which we propagate as err.
func runCicyMihomo(ctx context.Context, sub string) (string, error) {
	bin, err := cicyMihomoBin()
	if err != nil {
		return "", fmt.Errorf("cicy-mihomo not found on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin, sub)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// parseMihomoStatusOutput extracts pid / binary / config / log / started_at /
// controller / version from the canonical `cicy-mihomo status` block. Missing
// keys map to empty strings. `running` is true when the block starts with
// "status: running".
func parseMihomoStatusOutput(out string) M {
	info := M{
		"running":      false,
		"pid":          "",
		"binary":       "",
		"config":       "",
		"log":          "",
		"started_at":   "",
		"controller":   "",
		"version":      "",
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		switch key {
		case "status":
			info["running"] = val == "running"
		case "pid", "binary", "config", "log", "started_at", "controller":
			info[key] = val
		case "version":
			// The wrapper prints the controller's /version JSON verbatim:
			// `{"meta":true,"version":"v1.10.2"}`. Don't surface that JSON to
			// the UI — parse it and keep only the human-readable version
			// string. Falls back to the raw line if it isn't JSON-shaped.
			parsed := struct {
				Version string `json:"version"`
			}{}
			if json.Unmarshal([]byte(val), &parsed) == nil && parsed.Version != "" {
				info["version"] = parsed.Version
			} else {
				info["version"] = val
			}
		}
	}
	return info
}

// handleProxyStatus — GET /api/proxy/status
// Returns parsed `cicy-mihomo status` output: {running, pid, binary, config,
// log, started_at, controller, version}.
func handleProxyStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	out, err := runCicyMihomo(ctx, "status")
	if err != nil {
		// status always exits 0 normally; non-zero means the wrapper itself
		// failed (e.g. binary missing). Surface the message + raw output so
		// the UI can render something useful instead of a generic 500.
		httpErr(w, 500, fmt.Sprintf("%s: %s", err.Error(), out))
		return
	}
	info := parseMihomoStatusOutput(out)
	info["raw"] = out
	info["success"] = true
	J(w, info)
}

// mihomoYAMLPath returns the canonical mihomo.yaml location.
func mihomoYAMLPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cicy-ai", "db", "mihomo.yaml"), nil
}

// readMihomoAllowLAN reads the current `allow-lan:` value (defaults to true if
// the key is missing or the file doesn't exist — matches gen-config's
// template). A read error returns (true, nil) so callers don't have to
// distinguish "file not yet generated" from "real I/O failure."
func readMihomoAllowLAN() (bool, error) {
	path, err := mihomoYAMLPath()
	if err != nil {
		return true, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return true, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "allow-lan:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(t, "allow-lan:"))
		v = strings.Trim(v, "\"' ")
		return strings.EqualFold(v, "true") || v == "1" || strings.EqualFold(v, "yes"), nil
	}
	return true, nil
}

// writeMihomoAllowLAN rewrites the `allow-lan:` line in-place. Appends the key
// when missing. Preserves all other formatting (comments / whitespace). The
// caller is responsible for triggering `cicy-mihomo reload`.
//
// If mihomo.yaml doesn't exist yet (mihomo hasn't been started for this user
// before), we run `cicy-mihomo gen-config` to seed the default template first
// so the toggle still works without forcing the user to manually generate.
func writeMihomoAllowLAN(value bool) error {
	path, err := mihomoYAMLPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if _, runErr := runCicyMihomo(ctx, "gen-config"); runErr != nil {
			return fmt.Errorf("mihomo.yaml missing and gen-config failed: %w", runErr)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	repl := "false"
	if value {
		repl = "true"
	}
	lines := strings.Split(string(data), "\n")
	replaced := false
	for i, line := range lines {
		// only match top-level keys (no leading whitespace)
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "allow-lan:") {
			lines[i] = "allow-lan: " + repl
			replaced = true
			break
		}
	}
	if !replaced {
		// Insert just after the first non-empty / non-comment line so it sits
		// near other top-level scalars (mixed-port etc.). Falling back to the
		// top is fine if we can't find a good spot.
		insertAt := 0
		for i, line := range lines {
			t := strings.TrimSpace(line)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			insertAt = i + 1
			break
		}
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:insertAt]...)
		out = append(out, "allow-lan: "+repl)
		out = append(out, lines[insertAt:]...)
		lines = out
	}
	return writeMihomoYAMLValidated(path, []byte(strings.Join(lines, "\n")))
}

// resolveMihomoBinary returns the mihomo binary path used by the validator,
// honoring MIHOMO_BIN, the canonical install location, and finally $PATH.
// Returns an empty string when none are present.
func resolveMihomoBinary() string {
	for _, cand := range []string{
		strings.TrimSpace(os.Getenv("MIHOMO_BIN")),
		filepath.Join(userHomeForMihomo(), ".local", "bin", "mihomo"),
	} {
		if cand == "" {
			continue
		}
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	if p, err := exec.LookPath("mihomo"); err == nil {
		return p
	}
	return ""
}

func userHomeForMihomo() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

// writeMihomoYAMLValidated stages data to /tmp, validates it with
// `mihomo -t -f <tmp>`, and only writes to `path` on success. `mihomo -t`
// always exits 0, so we parse stdout for the "test is successful" sentinel —
// exit code is useless. The target file is untouched on validation failure.
//
// /tmp may be on a separate filesystem (tmpfs) from the config dir, so we
// can't `rename` from /tmp directly. After validation passes we re-stage
// into a sibling temp file under the target dir and rename that — the
// rename stays atomic within the same filesystem.
func writeMihomoYAMLValidated(path string, data []byte) error {
	bin := resolveMihomoBinary()
	if bin == "" {
		return os.WriteFile(path, data, 0o644)
	}
	// 1) stage in /tmp for validation
	probe, err := os.CreateTemp("/tmp", "mihomo.yaml.validate-*")
	if err != nil {
		return err
	}
	probePath := probe.Name()
	defer os.Remove(probePath)
	if _, err := probe.Write(data); err != nil {
		probe.Close()
		return err
	}
	if err := probe.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, "-t", "-f", probePath).CombinedOutput()
	if !strings.Contains(string(out), "test is successful") {
		return fmt.Errorf("mihomo -t rejected the new config (yaml not written):\n%s", summarizeMihomoTestOutput(out))
	}
	// 2) atomic replace via sibling temp in the target dir
	dir := filepath.Dir(path)
	sibling, err := os.CreateTemp(dir, ".mihomo.yaml.commit-*")
	if err != nil {
		return err
	}
	siblingPath := sibling.Name()
	if _, err := sibling.Write(data); err != nil {
		sibling.Close()
		_ = os.Remove(siblingPath)
		return err
	}
	if err := sibling.Close(); err != nil {
		_ = os.Remove(siblingPath)
		return err
	}
	if err := os.Chmod(siblingPath, 0o644); err != nil {
		_ = os.Remove(siblingPath)
		return err
	}
	return os.Rename(siblingPath, path)
}

func summarizeMihomoTestOutput(out []byte) string {
	var keep []string
	for _, line := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.Contains(t, "level=error") ||
			strings.Contains(t, "test failed") ||
			strings.Contains(t, "test is successful") {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		all := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		if len(all) > 5 {
			all = all[len(all)-5:]
		}
		return strings.Join(all, "\n")
	}
	return strings.Join(keep, "\n")
}

// listLANIPv4 returns non-loopback IPv4 addresses bound on this host.
func listLANIPv4() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			ip := ipnet.IP.String()
			if !seen[ip] {
				seen[ip] = true
				out = append(out, ip)
			}
		}
	}
	return out
}

// fetchPublicIPv4 dials ifconfig.me directly (no proxy env) so the result
// reflects this host's egress, not whatever upstream mihomo is currently
// pointing at.
func fetchPublicIPv4(ctx context.Context) (string, error) {
	tr := &http.Transport{Proxy: nil}
	client := &http.Client{Transport: tr, Timeout: 6 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ifconfig.me/ip", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return strings.TrimSpace(string(body)), nil
}

// handleProxyBindMode — GET / PATCH /api/proxy/bind-mode
// GET returns `{allow_lan, listen_addr}`. PATCH {"allow_lan":bool} rewrites
// mihomo.yaml and triggers a reload so the change is live.
func handleProxyBindMode(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		v, err := readMihomoAllowLAN()
		if err != nil {
			httpErr(w, 500, "read mihomo.yaml: "+err.Error())
			return
		}
		listen := "127.0.0.1"
		if v {
			listen = "0.0.0.0"
		}
		J(w, M{"success": true, "allow_lan": v, "listen_addr": listen})
	case http.MethodPatch, http.MethodPost:
		var req struct {
			AllowLAN *bool `json:"allow_lan"`
		}
		if err := readBody(r, &req); err != nil {
			httpErr(w, 400, "bad body: "+err.Error())
			return
		}
		if req.AllowLAN == nil {
			httpErr(w, 400, "allow_lan required")
			return
		}
		if err := writeMihomoAllowLAN(*req.AllowLAN); err != nil {
			httpErr(w, 500, "write mihomo.yaml: "+err.Error())
			return
		}
		// reload so the new allow-lan takes effect without dropping connections
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		reloadOut, reloadErr := runCicyMihomo(ctx, "reload")
		resp := M{"success": true, "allow_lan": *req.AllowLAN, "reload_output": reloadOut}
		if reloadErr != nil {
			// the yaml is already written — bubble the reload failure up
			// but don't 500: the user can manually restart from the UI.
			resp["reload_error"] = reloadErr.Error()
		}
		J(w, resp)
	default:
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleProxyExport — GET /api/proxy/export?ip=local|public|<custom>&user=<name>
// Returns the same shell-export lines as `cicy_proxy_show 1`, parameterized by
// IP source and worker username. The default user is "cicy" (any username is
// fine because mihomo's globalPassword accepts wildcards).
func handleProxyExport(w http.ResponseWriter, r *http.Request) {
	mode := strings.TrimSpace(r.URL.Query().Get("ip"))
	if mode == "" {
		mode = "local"
	}
	user := strings.TrimSpace(r.URL.Query().Get("user"))
	if user == "" {
		// IN-USER-PREFIX,w-,default_proxy_group is the catch-all for the
		// fleet. Default to a `w-` prefixed name so an out-of-the-box export
		// actually routes; non-`w-` users would auth fine but get REJECTed
		// at the rules stage.
		user = "w-test"
	}
	password := readMihomoGlobalPasswordFromYAML()
	if password == "" {
		password = "<YOUR_PASSWORD_HERE>"
	}
	port := defaultMihomoMixedPort
	if v := strings.TrimSpace(os.Getenv("CICY_MIHOMO_PORT")); v != "" {
		port = v
	}
	lanIPs := listLANIPv4()
	host := "127.0.0.1"
	switch mode {
	case "local", "loopback", "127.0.0.1":
		host = "127.0.0.1"
		mode = "local"
	case "lan":
		if len(lanIPs) > 0 {
			host = lanIPs[0]
		}
	case "public":
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		ip, err := fetchPublicIPv4(ctx)
		if err == nil && ip != "" {
			host = ip
		}
	default:
		// custom IP / hostname literal
		host = mode
		mode = "custom"
	}
	proxyURL := fmt.Sprintf("http://%s:%s@%s:%s", url.QueryEscape(user), url.QueryEscape(password), host, port)
	lines := []string{
		fmt.Sprintf("export HTTP_PROXY=\"%s\"", proxyURL),
		fmt.Sprintf("export HTTPS_PROXY=\"%s\"", proxyURL),
		fmt.Sprintf("export ALL_PROXY=\"%s\"", proxyURL),
		fmt.Sprintf("export http_proxy=\"%s\"", proxyURL),
		fmt.Sprintf("export https_proxy=\"%s\"", proxyURL),
		fmt.Sprintf("export all_proxy=\"%s\"", proxyURL),
		"export NO_PROXY=\"localhost,127.0.0.1,::1\"",
		"export no_proxy=\"localhost,127.0.0.1,::1\"",
	}
	allowLAN, _ := readMihomoAllowLAN()
	J(w, M{
		"success":    true,
		"ip_mode":    mode,
		"host":       host,
		"port":       port,
		"user":       user,
		"password":   password,
		"proxy_url":  proxyURL,
		"lines":      lines,
		"script":     strings.Join(lines, "\n"),
		"lan_ips":    lanIPs,
		"allow_lan":  allowLAN,
	})
}

// handleProxyLifecycle — POST /api/proxy/lifecycle
// Body: {"action": "start"|"stop"|"restart"|"reload"}
// Shells out to the cicy-mihomo wrapper; relays stdout/stderr back so the UI
// can show the user what mihomo said. Unknown actions get 400.
func handleProxyLifecycle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "bad body: "+err.Error())
		return
	}
	action := strings.TrimSpace(req.Action)
	switch action {
	case "start", "stop", "restart", "reload":
	default:
		httpErr(w, 400, "action must be one of start|stop|restart|reload")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, err := runCicyMihomo(ctx, action)
	result := M{
		"success": err == nil,
		"action":  action,
		"output":  out,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	J(w, result)
}
