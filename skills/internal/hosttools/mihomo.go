package hosttools

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type mihomoTool struct {
	stdout io.Writer
	stderr io.Writer
}

type mihomoState struct {
	PID       int    `json:"pid"`
	Binary    string `json:"binary"`
	Config    string `json:"config"`
	Log       string `json:"log"`
	StartedAt string `json:"started_at"`
}

type mihomoFlagOptions struct {
	Config string
	Binary string
	Lines  int
	Follow bool
}

func (e *Env) runCicyMihomo(args []string) error {
	return newMihomoTool(e.Stdout, e.Stderr).run(args)
}

func newMihomoTool(stdout, stderr io.Writer) *mihomoTool {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return &mihomoTool{stdout: stdout, stderr: stderr}
}

func (t *mihomoTool) run(args []string) error {
	cmd := "help"
	if len(args) > 0 {
		cmd = strings.TrimSpace(args[0])
		args = args[1:]
	}

	switch cmd {
	case "help", "-h", "--help":
		_, _ = fmt.Fprintln(t.stdout, t.helpText())
		return nil
	case "template":
		_, _ = fmt.Fprint(t.stdout, t.defaultTemplate())
		return nil
	case "show-config":
		return t.showConfig()
	case "gen-config":
		return t.genConfig()
	case "status":
		return t.status()
	case "start":
		return t.start()
	case "stop":
		return t.stop()
	case "restart":
		if err := t.stop(); err != nil && !errors.Is(err, errMihomoNotRunning) {
			return err
		}
		return t.start()
	case "reload":
		return t.reload()
	case "logs", "log":
		opts, extra, err := parseMihomoFlags(args)
		if err != nil {
			return err
		}
		if len(extra) > 1 {
			return fmt.Errorf("usage: cicy-mihomo logs [N]")
		}
		if len(extra) == 1 {
			n, convErr := strconv.Atoi(strings.TrimSpace(extra[0]))
			if convErr != nil || n <= 0 {
				return fmt.Errorf("invalid log line count: %s", extra[0])
			}
			opts.Lines = n
		}
		return t.logs(opts)
	case "install":
		_, _ = fmt.Fprintln(t.stdout, t.installGuide())
		return nil
	case "test":
		return t.testAll()
	default:
		_, _ = fmt.Fprintln(t.stdout, t.helpText())
		return fmt.Errorf("unknown subcommand: %s", cmd)
	}
}

var errMihomoNotRunning = errors.New("cicy-mihomo is not running")

func (t *mihomoTool) helpText() string {
	return `cicy-mihomo - manage local mihomo proxy

Usage:
  cicy-mihomo help
  cicy-mihomo template
  cicy-mihomo gen-config
  cicy-mihomo show-config
  cicy-mihomo status
  cicy-mihomo start
  cicy-mihomo stop
  cicy-mihomo restart
  cicy-mihomo reload
  cicy-mihomo logs [N|-f]
  cicy-mihomo install
  cicy-mihomo test                  test all proxy node speed (anthropic/google/github/cf)

Defaults:
  binary:   ~/.local/bin/mihomo (or set MIHOMO_BIN)
  config:   ~/cicy-ai/db/mihomo.yaml
  port:     9001
  api:      127.0.0.1:18009
`
}

func (t *mihomoTool) installGuide() string {
	return `Install mihomo with CN GitHub mirror:
  git clone https://gh-proxy.com/https://github.com/cicy-ai/mihomo.git
  cd mihomo
  go build
`
}

func (t *mihomoTool) stateDir() string {
	return filepath.Join(userHomeDir(), ".local", "state", "cicy-skills", "mihomo")
}

func (t *mihomoTool) pidFile() string {
	return filepath.Join(t.stateDir(), "pid")
}

func (t *mihomoTool) stateFile() string {
	return filepath.Join(t.stateDir(), "state.json")
}

func (t *mihomoTool) logFile() string {
	return filepath.Join(t.stateDir(), "mihomo.log")
}

func (t *mihomoTool) configPath() string {
	return filepath.Join(userHomeDir(), "cicy-ai", "db", "mihomo.yaml")
}

func (t *mihomoTool) binaryCandidates() []string {
	home := userHomeDir()
	return []string{
		strings.TrimSpace(os.Getenv("MIHOMO_BIN")),
		filepath.Join(home, "projects", "cicy-mihomo", "bin", "mihomo-darwin-amd64"),
		filepath.Join(home, ".local", "bin", "mihomo"),
		filepath.Join(home, ".local", "bin", "mihomo-test"),
		"mihomo",
		"mihomo-test",
	}
}

func (t *mihomoTool) resolveBinary() (string, error) {
	for _, candidate := range t.binaryCandidates() {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, string(os.PathSeparator)) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("mihomo binary not found; set MIHOMO_BIN or build mihomo first")
}

func parseMihomoFlags(args []string) (mihomoFlagOptions, []string, error) {
	opts := mihomoFlagOptions{Lines: 80}
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--config" && i+1 < len(args):
			opts.Config = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(arg, "--config="):
			opts.Config = strings.TrimSpace(strings.TrimPrefix(arg, "--config="))
		case arg == "--bin" && i+1 < len(args):
			opts.Binary = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(arg, "--bin="):
			opts.Binary = strings.TrimSpace(strings.TrimPrefix(arg, "--bin="))
		case arg == "-f" || arg == "--follow":
			opts.Follow = true
		default:
			rest = append(rest, arg)
		}
	}
	return opts, rest, nil
}

func (t *mihomoTool) defaultTemplate() string {
	// globalPassword is the single shared secret for all agents on this host.
	// We generate it fresh at gen-config time and never rotate it automatically.
	// cicy-mihomo's Verify lets any non-empty username through when the
	// password matches globalPassword, so per-user `authentication:` entries
	// aren't necessary.
	password := randomAlphaNum(16)
	return fmt.Sprintf("mixed-port: 9001\nallow-lan: true\nbind: 0.0.0.0\nmode: rule\nlog-level: debug\n\nexternal-controller: 127.0.0.1:18009\n\nglobalPassword: %q\n\nproxies:\n  - name: \"default_proxy_node\"\n    type: socks5\n    server: 127.0.0.1\n    port: 1084\n\nrules:\n  - IN-USER,w-10001,default_proxy_node\n  - MATCH,REJECT\n", password)
}

func randomAlphaNum(n int) string {
	if n <= 0 {
		return ""
	}
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	buf := make([]byte, n)
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		for i := range buf {
			buf[i] = alphabet[i%len(alphabet)]
		}
		return string(buf)
	}
	for i := range buf {
		buf[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(buf)
}

func (t *mihomoTool) genConfig() error {
	path := t.configPath()
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(t.defaultTemplate()), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(t.stdout, path)
	return nil
}

func (t *mihomoTool) showConfig() error {
	data, err := os.ReadFile(t.configPath())
	if err != nil {
		return err
	}
	_, _ = t.stdout.Write(data)
	if len(data) == 0 || data[len(data)-1] != '\n' {
		_, _ = fmt.Fprintln(t.stdout)
	}
	return nil
}

func (t *mihomoTool) start() error {
	binary, err := t.resolveBinary()
	if err != nil {
		return err
	}
	configPath := t.configPath()
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config not found: %s", configPath)
	}
	if err := os.MkdirAll(t.stateDir(), 0o755); err != nil {
		return err
	}
	logPath := t.logFile()
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(binary, "-d", filepath.Join(userHomeDir(), "cicy-ai"), "-f", configPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	state := mihomoState{PID: cmd.Process.Pid, Binary: binary, Config: configPath, Log: logPath, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := t.writeState(state); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(t.stdout, "started")
	return nil
}

func (t *mihomoTool) stop() error {
	state, err := t.readState()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errMihomoNotRunning
		}
		return err
	}
	process, err := os.FindProcess(state.PID)
	if err == nil {
		_ = process.Signal(syscall.SIGTERM)
	}
	_ = os.Remove(t.stateFile())
	_, _ = fmt.Fprintln(t.stdout, "stopped")
	return nil
}

func (t *mihomoTool) status() error {
	state, err := t.readState()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintln(t.stdout, "status: stopped")
			return nil
		}
		return err
	}
	_, controller := parseMihomoPortsFromConfig(state.Config)
	if strings.TrimSpace(controller) == "" {
		controller = "127.0.0.1:19001"
	}
	controllerURL := "http://" + controller + "/version"
	version := ""
	if resp, err := http.Get(controllerURL); err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		version = strings.TrimSpace(string(body))
	}
	_, _ = fmt.Fprintf(t.stdout, "status: running\npid: %d\nbinary: %s\nconfig: %s\nlog: %s\nstarted_at: %s\ncontroller: %s\nversion: %s\n", state.PID, state.Binary, state.Config, state.Log, state.StartedAt, controllerURL, version)
	return nil
}

func (t *mihomoTool) reload() error {
	state, err := t.readState()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errMihomoNotRunning
		}
		return err
	}
	configPath := state.Config
	if strings.TrimSpace(configPath) == "" {
		configPath = t.configPath()
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	_, controller := parseMihomoPortsFromConfig(configPath)
	if strings.TrimSpace(controller) == "" {
		controller = "127.0.0.1:19001"
	}
	endpoint := "http://" + controller + "/configs?force=true"
	payload := map[string]any{
		"path":    configPath,
		"payload": string(data),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("reload failed: %s", strings.TrimSpace(string(respBody)))
	}
	_, _ = fmt.Fprintln(t.stdout, "reloaded")
	return nil
}

func (t *mihomoTool) logs(opts mihomoFlagOptions) error {
	path := t.logFile()
	if opts.Follow {
		cmd := exec.Command("tail", "-f", path)
		cmd.Stdout = t.stdout
		cmd.Stderr = t.stderr
		return cmd.Run()
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	lines := []string{}
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > opts.Lines {
			lines = lines[1:]
		}
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(t.stdout, line)
	}
	return scanner.Err()
}

func (t *mihomoTool) readState() (mihomoState, error) {
	var state mihomoState
	data, err := os.ReadFile(t.stateFile())
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	return state, nil
}

func (t *mihomoTool) writeState(state mihomoState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(t.stateFile(), data, 0o644)
}

type proxyEntry struct {
	Name string
	Type string
	Host string
	Port int
	User string
	Pass string
}

var testURLs = []string{
	"https://api.anthropic.com",
	"https://www.google.com",
	"https://github.com",
	"https://cloudflare.com",
}

func (t *mihomoTool) testAll() error {
	state, err := t.readState()
	if err != nil {
		return fmt.Errorf("cannot read state: %w", err)
	}
	cfgPath := state.Config
	if cfgPath == "" {
		cfgPath = t.configPath()
	}
	proxies := parseProxiesFromConfig(cfgPath)
	_, _ = fmt.Fprintf(t.stdout, "testing %d proxy nodes:\n", len(proxies))
	// header
	_, _ = fmt.Fprintf(t.stdout, "%-20s", "")
	for _, url := range testURLs {
		short := strings.TrimPrefix(url, "https://")
		if len(short) > 16 {
			short = short[:16]
		}
		_, _ = fmt.Fprintf(t.stdout, " %10s", short)
	}
	_, _ = fmt.Fprintln(t.stdout)
	// rows
	for _, p := range proxies {
		_, _ = fmt.Fprintf(t.stdout, "%-20s", p.Name)
		for _, url := range testURLs {
			t.testViaLocal(p, url)
		}
		_, _ = fmt.Fprintln(t.stdout)
	}
	return nil
}

func (t *mihomoTool) testViaLocal(p proxyEntry, url string) {
	ctrl := "http://127.0.0.1:18009"
	body := fmt.Sprintf(`{"name":"%s"}`, p.Name)
	req, _ := http.NewRequest("PUT", ctrl+"/proxies/default_proxy_group", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(t.stdout, " %10s", "sel_err")
		return
	}
	resp.Body.Close()

	time.Sleep(200 * time.Millisecond)

	proxyURL := fmt.Sprintf("http://w-10001:MsZTKFsSCWrQC25d@127.0.0.1:9001")
	cmd := exec.Command("curl", "-sS", "-o", "/dev/null", "-w", "%{time_total}", "--connect-timeout", "8", "--max-time", "15", "-x", proxyURL, url)
	out, err := cmd.Output()
	timeStr := strings.TrimSpace(string(out))
	if err != nil {
		_, _ = fmt.Fprintf(t.stdout, " %10s", "timeout")
	} else if sec, parseErr := strconv.ParseFloat(timeStr, 64); parseErr == nil {
		_, _ = fmt.Fprintf(t.stdout, " %7.2fs ", sec)
	} else {
		_, _ = fmt.Fprintf(t.stdout, " %10s", timeStr)
	}
}

func parseProxiesFromConfig(path string) []proxyEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var proxies []proxyEntry
	inProxies := false
	var cur *proxyEntry
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "proxies:" {
			inProxies = true
			continue
		}
		if !inProxies {
			continue
		}
		if strings.HasPrefix(trimmed, "- name:") {
			if cur != nil {
				proxies = append(proxies, *cur)
			}
			cur = &proxyEntry{}
			name := strings.TrimPrefix(trimmed, "- name:")
			name = strings.TrimSpace(name)
			name = strings.Trim(name, "\"")
			cur.Name = name
			continue
		}
		if strings.HasPrefix(trimmed, "type:") {
			if cur == nil {
				continue
			}
			cur.Type = strings.TrimSpace(strings.TrimPrefix(trimmed, "type:"))
			continue
		}
		if strings.HasPrefix(trimmed, "server:") {
			if cur == nil {
				continue
			}
			cur.Host = strings.TrimSpace(strings.TrimPrefix(trimmed, "server:"))
			continue
		}
		if strings.HasPrefix(trimmed, "port:") {
			if cur == nil {
				continue
			}
			portStr := strings.TrimSpace(strings.TrimPrefix(trimmed, "port:"))
			cur.Port, _ = strconv.Atoi(portStr)
			continue
		}
		if strings.HasPrefix(trimmed, "username:") {
			if cur == nil {
				continue
			}
			cur.User = strings.TrimSpace(strings.TrimPrefix(trimmed, "username:"))
			cur.User = strings.Trim(cur.User, "\"")
			continue
		}
		if strings.HasPrefix(trimmed, "password:") {
			if cur == nil {
				continue
			}
			cur.Pass = strings.TrimSpace(strings.TrimPrefix(trimmed, "password:"))
			cur.Pass = strings.Trim(cur.Pass, "\"")
			continue
		}
		if strings.HasPrefix(trimmed, "authentication:") || strings.HasPrefix(trimmed, "rules:") || strings.HasPrefix(trimmed, "proxy-groups:") {
			if cur != nil {
				proxies = append(proxies, *cur)
				cur = nil
			}
			inProxies = false
		}
	}
	if cur != nil {
		proxies = append(proxies, *cur)
	}
	// fallback to global authentication for proxies without per-proxy auth
	authRe := regexp.MustCompile(`(?m)^\s*-\s+"([^:]+):([^"]+)"\s*$`)
	authMatches := authRe.FindAllStringSubmatch(string(data), -1)
	for i := range proxies {
		if proxies[i].User == "" && len(authMatches) > 0 {
			proxies[i].User = authMatches[0][1]
			proxies[i].Pass = authMatches[0][2]
		}
	}
	return proxies
}

func (t *mihomoTool) testProxy(p proxyEntry) {
	url := "https://api.anthropic.com"
	var cmd *exec.Cmd
	switch strings.ToLower(p.Type) {
	case "socks5":
		addr := fmt.Sprintf("%s:%d", p.Host, p.Port)
		cmd = exec.Command("curl", "-sS", "-o", "/dev/null", "-w", "%{time_total}", "--connect-timeout", "10", "--max-time", "20", "--socks5-hostname", addr, url)
	case "http":
		addr := fmt.Sprintf("%s:%d", p.Host, p.Port)
		proxyURL := fmt.Sprintf("http://%s", addr)
		if p.User != "" && p.Pass != "" {
			proxyURL = fmt.Sprintf("http://%s:%s@%s", p.User, p.Pass, addr)
		}
		cmd = exec.Command("curl", "-sS", "-o", "/dev/null", "-w", "%{time_total}", "--connect-timeout", "10", "--max-time", "20", "-x", proxyURL, url)
	}
	if cmd == nil {
		return
	}
	out, err := cmd.Output()
	timeStr := strings.TrimSpace(string(out))
	if err != nil {
		_, _ = fmt.Fprintf(t.stdout, "%-20s ❌ %v\n", p.Name, err)
	} else if sec, parseErr := strconv.ParseFloat(timeStr, 64); parseErr == nil {
		_, _ = fmt.Fprintf(t.stdout, "%-20s %.2fs\n", p.Name, sec)
	} else {
		_, _ = fmt.Fprintf(t.stdout, "%-20s %s\n", p.Name, timeStr)
	}
}

func parseMihomoPortsFromConfig(path string) (mixedPort int, controller string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, ""
	}
	text := string(data)
	mixedRe := regexp.MustCompile(`(?m)^mixed-port:\s*(\d+)\s*$`)
	controllerRe := regexp.MustCompile(`(?m)^external-controller:\s*([^\s]+)\s*$`)
	if m := mixedRe.FindStringSubmatch(text); len(m) == 2 {
		mixedPort, _ = strconv.Atoi(m[1])
	}
	if m := controllerRe.FindStringSubmatch(text); len(m) == 2 {
		controller = strings.TrimSpace(m[1])
	}
	return mixedPort, controller
}
