package hosttools

import (
	"bytes"
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

type frpKind string

const (
	frpServerKind frpKind = "server"
	frpClientKind frpKind = "client"
)

type frpTool struct {
	kind   frpKind
	stdout io.Writer
	stderr io.Writer
}

type frpState struct {
	PID       int      `json:"pid"`
	Binary    string   `json:"binary"`
	Config    string   `json:"config"`
	Log       string   `json:"log"`
	StartedAt string   `json:"started_at"`
	ExtraArgs []string `json:"extra_args,omitempty"`
}

type frpFlagOptions struct {
	Config string
	Binary string
	Lines  int
}

type frpConfigHints struct {
	BindAddr    string
	BindPort    string
	ServerAddr  string
	ServerPort  string
	WebAddr     string
	WebPort     string
	WebUser     string
	WebPassword string
}

type frpClientInfo struct {
	Key              string `json:"key"`
	User             string `json:"user"`
	ClientID         string `json:"clientID"`
	RunID            string `json:"runID"`
	Version          string `json:"version"`
	Hostname         string `json:"hostname"`
	ClientIP         string `json:"clientIP"`
	FirstConnectedAt int64  `json:"firstConnectedAt"`
	LastConnectedAt  int64  `json:"lastConnectedAt"`
	DisconnectedAt   int64  `json:"disconnectedAt"`
	Online           bool   `json:"online"`
}

func (e *Env) runFRPServer(args []string) error {
	return newFRPTool(frpServerKind, e.Stdout, e.Stderr).run(args)
}

func (e *Env) runFRPClient(args []string) error {
	return newFRPTool(frpClientKind, e.Stdout, e.Stderr).run(args)
}

func newFRPTool(kind frpKind, stdout, stderr io.Writer) *frpTool {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return &frpTool{kind: kind, stdout: stdout, stderr: stderr}
}

func (t *frpTool) run(args []string) error {
	cmd := "help"
	if len(args) > 0 {
		cmd = strings.TrimSpace(args[0])
		args = args[1:]
	}

	switch cmd {
	case "help", "-h", "--help":
		_, _ = fmt.Fprintln(t.stdout, t.helpText())
		return nil
	case "tools":
		_, _ = fmt.Fprintln(t.stdout, t.toolsText())
		return nil
	case "start":
		opts, extra, err := parseFRPFlags(args)
		if err != nil {
			return err
		}
		return t.start(opts, extra)
	case "run", "foreground":
		opts, extra, err := parseFRPFlags(args)
		if err != nil {
			return err
		}
		return t.runForeground(opts, extra)
	case "stop":
		_, extra, err := parseFRPFlags(args)
		if err != nil {
			return err
		}
		if len(extra) != 0 {
			return fmt.Errorf("usage: %s stop", t.commandName())
		}
		return t.stop()
	case "restart":
		opts, extra, err := parseFRPFlags(args)
		if err != nil {
			return err
		}
		return t.restart(opts, extra)
	case "status":
		opts, extra, err := parseFRPFlags(args)
		if err != nil {
			return err
		}
		if len(extra) != 0 {
			return fmt.Errorf("usage: %s status [--config PATH] [--bin PATH]", t.commandName())
		}
		return t.status(opts)
	case "connections", "conns":
		opts, extra, err := parseFRPFlags(args)
		if err != nil {
			return err
		}
		if len(extra) != 0 {
			return fmt.Errorf("usage: %s connections", t.commandName())
		}
		return t.connections(opts)
	case "clients", "client-list", "clientlist":
		opts, extra, err := parseFRPFlags(args)
		if err != nil {
			return err
		}
		if len(extra) != 0 {
			return fmt.Errorf("usage: %s clients", t.commandName())
		}
		return t.clients(opts)
	case "reload":
		opts, extra, err := parseFRPFlags(args)
		if err != nil {
			return err
		}
		if len(extra) != 0 {
			return fmt.Errorf("usage: %s reload [--config PATH] [--bin PATH]", t.commandName())
		}
		return t.reload(opts)
	case "logs", "log":
		opts, extra, err := parseFRPFlags(args)
		if err != nil {
			return err
		}
		if len(extra) > 1 {
			return fmt.Errorf("usage: %s logs [N]", t.commandName())
		}
		if len(extra) == 1 {
			n, convErr := strconv.Atoi(strings.TrimSpace(extra[0]))
			if convErr != nil || n <= 0 {
				return fmt.Errorf("invalid log line count: %s", extra[0])
			}
			opts.Lines = n
		}
		return t.logs(opts)
	case "raw":
		if len(args) == 0 {
			return fmt.Errorf("usage: %s raw -- <real %s args...>", t.commandName(), t.binaryName())
		}
		return t.raw(args)
	default:
		return t.run([]string{"help"})
	}
}

func (t *frpTool) commandName() string {
	if t.kind == frpServerKind {
		return "frp-server"
	}
	return "frp-client"
}

func (t *frpTool) binaryName() string {
	if t.kind == frpServerKind {
		return "frps"
	}
	return "frpc"
}

func (t *frpTool) roleLabel() string {
	if t.kind == frpServerKind {
		return "FRP server"
	}
	return "FRP client"
}

func (t *frpTool) binEnvName() string {
	if t.kind == frpServerKind {
		return "FRP_SERVER_BIN"
	}
	return "FRP_CLIENT_BIN"
}

func (t *frpTool) configEnvName() string {
	if t.kind == frpServerKind {
		return "FRP_SERVER_CONFIG"
	}
	return "FRP_CLIENT_CONFIG"
}

func (t *frpTool) logEnvName() string {
	if t.kind == frpServerKind {
		return "FRP_SERVER_LOG"
	}
	return "FRP_CLIENT_LOG"
}

func (t *frpTool) stateDir() string {
	return filepath.Join(userHomeDir(), ".local", "state", "cicy-skills", "frp", string(t.kind))
}

func (t *frpTool) pidFile() string {
	return filepath.Join(t.stateDir(), "pid")
}

func (t *frpTool) stateFile() string {
	return filepath.Join(t.stateDir(), "state.json")
}

func (t *frpTool) defaultConfigCandidates() []string {
	name := t.binaryName()
	return []string{
		filepath.Join(userHomeDir(), "data", "frp", name+".toml"),
		filepath.Join(userHomeDir(), "data", "frp", name+".yaml"),
		filepath.Join(userHomeDir(), "data", "frp", name+".yml"),
		filepath.Join(userHomeDir(), "data", "frp", name+".ini"),
		filepath.Join(userHomeDir(), ".config", "frp", name+".toml"),
		filepath.Join(userHomeDir(), ".config", "frp", name+".yaml"),
		filepath.Join(userHomeDir(), ".config", "frp", name+".yml"),
		filepath.Join(userHomeDir(), ".config", "frp", name+".ini"),
	}
}

func (t *frpTool) defaultBinaryCandidates() []string {
	name := t.binaryName()
	return []string{
		filepath.Join(userHomeDir(), ".frp-tunnel", "bin", name),
		filepath.Join(userHomeDir(), ".local", "bin", name),
		filepath.Join(userHomeDir(), "bin", name),
	}
}

func (t *frpTool) defaultLogPath(config string) string {
	if value := strings.TrimSpace(os.Getenv(t.logEnvName())); value != "" {
		return expandHostPath(value)
	}
	if strings.TrimSpace(config) != "" {
		return filepath.Join(filepath.Dir(config), t.binaryName()+".log")
	}
	return filepath.Join(t.stateDir(), t.binaryName()+".log")
}

func parseFRPFlags(args []string) (frpFlagOptions, []string, error) {
	opts := frpFlagOptions{Lines: 100}
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--":
			rest = append(rest, args[i+1:]...)
			return opts, rest, nil
		case arg == "-c" || arg == "--config":
			if i+1 >= len(args) {
				return frpFlagOptions{}, nil, errors.New("missing value for --config")
			}
			opts.Config = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(arg, "--config="):
			opts.Config = strings.TrimSpace(strings.TrimPrefix(arg, "--config="))
		case arg == "--bin":
			if i+1 >= len(args) {
				return frpFlagOptions{}, nil, errors.New("missing value for --bin")
			}
			opts.Binary = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(arg, "--bin="):
			opts.Binary = strings.TrimSpace(strings.TrimPrefix(arg, "--bin="))
		case arg == "-n" || arg == "--lines":
			if i+1 >= len(args) {
				return frpFlagOptions{}, nil, errors.New("missing value for --lines")
			}
			n, err := strconv.Atoi(strings.TrimSpace(args[i+1]))
			if err != nil || n <= 0 {
				return frpFlagOptions{}, nil, fmt.Errorf("invalid value for --lines: %s", args[i+1])
			}
			opts.Lines = n
			i++
		case strings.HasPrefix(arg, "--lines="):
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(arg, "--lines=")))
			if err != nil || n <= 0 {
				return frpFlagOptions{}, nil, fmt.Errorf("invalid value for --lines: %s", arg)
			}
			opts.Lines = n
		default:
			rest = append(rest, arg)
		}
	}
	return opts, rest, nil
}

func (t *frpTool) loadState() (frpState, error) {
	var state frpState
	data, err := os.ReadFile(t.stateFile())
	if err == nil {
		if unmarshalErr := json.Unmarshal(data, &state); unmarshalErr != nil {
			return frpState{}, unmarshalErr
		}
	}
	if state.PID == 0 {
		pidData, pidErr := os.ReadFile(t.pidFile())
		if pidErr == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(pidData)))
			if convErr == nil {
				state.PID = pid
			}
		}
	}
	if err != nil && !os.IsNotExist(err) {
		return frpState{}, err
	}
	return state, nil
}

func (t *frpTool) writeState(state frpState) error {
	if err := os.MkdirAll(t.stateDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(t.stateFile(), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(t.pidFile(), []byte(strconv.Itoa(state.PID)+"\n"), 0o644)
}

func (t *frpTool) clearState() {
	_ = os.Remove(t.pidFile())
	_ = os.Remove(t.stateFile())
}

func (t *frpTool) currentState() (frpState, bool) {
	state, err := t.loadState()
	if err != nil {
		return frpState{}, false
	}
	if state.PID <= 0 {
		return state, false
	}
	if processRunning(state.PID, t.binaryName()) {
		return state, true
	}
	t.clearState()
	return state, false
}

func processRunning(pid int, expected string) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	expected = strings.ToLower(strings.TrimSpace(expected))
	if expected == "" {
		return true
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return true
	}
	text := strings.ToLower(strings.ReplaceAll(string(bytes.ReplaceAll(cmdline, []byte{0}, []byte(" "))), "\x00", " "))
	return strings.Contains(text, expected)
}

func (t *frpTool) resolveBinary(override, saved string) (string, error) {
	if value := strings.TrimSpace(override); value != "" {
		path, err := findExecutable(value)
		if err != nil {
			return "", fmt.Errorf("resolve %s binary from --bin: %w", t.roleLabel(), err)
		}
		return path, nil
	}
	if value := strings.TrimSpace(os.Getenv(t.binEnvName())); value != "" {
		path, err := findExecutable(value)
		if err != nil {
			return "", fmt.Errorf("resolve %s binary from %s: %w", t.roleLabel(), t.binEnvName(), err)
		}
		return path, nil
	}
	if value := strings.TrimSpace(saved); value != "" {
		path, err := findExecutable(value)
		if err == nil {
			return path, nil
		}
	}
	if path, err := exec.LookPath(t.binaryName()); err == nil {
		return path, nil
	}
	for _, candidate := range t.defaultBinaryCandidates() {
		if isExecutableFile(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s binary not found; install %s on PATH or set %s", t.roleLabel(), t.binaryName(), t.binEnvName())
}

func (t *frpTool) maybeResolveBinary(override, saved string) string {
	path, err := t.resolveBinary(override, saved)
	if err == nil {
		return path
	}
	if strings.TrimSpace(override) != "" {
		return expandHostPath(override)
	}
	if value := strings.TrimSpace(os.Getenv(t.binEnvName())); value != "" {
		return expandHostPath(value)
	}
	return saved
}

func findExecutable(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("empty path")
	}
	if strings.Contains(value, "/") || strings.HasPrefix(value, "~") {
		path := expandHostPath(value)
		if !isExecutableFile(path) {
			return "", fmt.Errorf("%s does not exist or is not executable", path)
		}
		return path, nil
	}
	path, err := exec.LookPath(value)
	if err != nil {
		return "", err
	}
	return path, nil
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

func (t *frpTool) resolveConfig(override, saved string) (string, error) {
	if value := strings.TrimSpace(override); value != "" {
		path := expandHostPath(value)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("resolve %s config from --config: %w", t.roleLabel(), err)
		}
		return path, nil
	}
	if value := strings.TrimSpace(os.Getenv(t.configEnvName())); value != "" {
		path := expandHostPath(value)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("resolve %s config from %s: %w", t.roleLabel(), t.configEnvName(), err)
		}
		return path, nil
	}
	if value := strings.TrimSpace(saved); value != "" {
		path := expandHostPath(value)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	for _, candidate := range t.defaultConfigCandidates() {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	candidates := strings.Join(t.defaultConfigCandidates(), ", ")
	return "", fmt.Errorf("%s config not found; create one of %s or set %s", t.roleLabel(), candidates, t.configEnvName())
}

func (t *frpTool) maybeResolveConfig(override, saved string) string {
	path, err := t.resolveConfig(override, saved)
	if err == nil {
		return path
	}
	if strings.TrimSpace(override) != "" {
		return expandHostPath(override)
	}
	if value := strings.TrimSpace(os.Getenv(t.configEnvName())); value != "" {
		return expandHostPath(value)
	}
	return saved
}

func (t *frpTool) start(opts frpFlagOptions, extra []string) error {
	state, running := t.currentState()
	if running {
		_, _ = fmt.Fprintf(t.stdout, "%s already running (pid %d)\n", t.commandName(), state.PID)
		return nil
	}

	config, err := t.resolveConfig(opts.Config, state.Config)
	if err != nil {
		return err
	}
	binary, err := t.resolveBinary(opts.Binary, state.Binary)
	if err != nil {
		return err
	}
	if t.kind == frpServerKind {
		if changed, token, syncErr := ensureFRPServerToken(config); syncErr != nil {
			return syncErr
		} else if changed {
			_, _ = fmt.Fprintf(t.stdout, "generated auth token for %s\n", config)
			if syncedClientPath, synced := syncLocalFRPClientToken(config, token); synced {
				_, _ = fmt.Fprintf(t.stdout, "synced local frp-client token in %s\n", syncedClientPath)
			}
		}
	}
	logPath := firstNonEmpty(strings.TrimSpace(state.Log), t.defaultLogPath(config))
	if strings.TrimSpace(logPath) == "" {
		logPath = t.defaultLogPath(config)
	}
	logPath = expandHostPath(logPath)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	_, _ = fmt.Fprintf(logFile, "\n=== %s start %s ===\n", t.commandName(), time.Now().Format(time.RFC3339))

	cmdArgs := append([]string{"-c", config}, extra...)
	cmd := exec.Command(binary, cmdArgs...)
	cmd.Dir = filepath.Dir(config)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}

	newState := frpState{
		PID:       cmd.Process.Pid,
		Binary:    binary,
		Config:    config,
		Log:       logPath,
		StartedAt: time.Now().Format(time.RFC3339),
		ExtraArgs: append([]string(nil), extra...),
	}
	if err := t.writeState(newState); err != nil {
		_ = cmd.Process.Kill()
		return err
	}

	time.Sleep(400 * time.Millisecond)
	if !processRunning(cmd.Process.Pid, t.binaryName()) {
		t.clearState()
		tail, _ := tailTextFile(logPath, 20)
		if strings.TrimSpace(tail) != "" {
			return fmt.Errorf("%s exited during startup; recent logs:\n%s", t.commandName(), tail)
		}
		return fmt.Errorf("%s exited during startup", t.commandName())
	}

	_, _ = fmt.Fprintf(t.stdout, "%s started in background\n", t.commandName())
	_, _ = fmt.Fprintf(t.stdout, "pid: %d\n", cmd.Process.Pid)
	_, _ = fmt.Fprintf(t.stdout, "config: %s\n", config)
	_, _ = fmt.Fprintf(t.stdout, "log: %s\n", logPath)
	return nil
}

func (t *frpTool) runForeground(opts frpFlagOptions, extra []string) error {
	state, _ := t.currentState()
	config, err := t.resolveConfig(opts.Config, state.Config)
	if err != nil {
		return err
	}
	binary, err := t.resolveBinary(opts.Binary, state.Binary)
	if err != nil {
		return err
	}
	cmdArgs := append([]string{"-c", config}, extra...)
	cmd := exec.Command(binary, cmdArgs...)
	cmd.Dir = filepath.Dir(config)
	cmd.Stdout = t.stdout
	cmd.Stderr = t.stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func (t *frpTool) stop() error {
	state, running := t.currentState()
	if !running {
		_, _ = fmt.Fprintf(t.stdout, "%s is already stopped\n", t.commandName())
		return nil
	}
	proc, err := os.FindProcess(state.PID)
	if err != nil {
		t.clearState()
		return nil
	}
	_ = proc.Signal(syscall.SIGTERM)
	if waitForExit(state.PID, t.binaryName(), 5*time.Second) {
		t.clearState()
		_, _ = fmt.Fprintf(t.stdout, "%s stopped\n", t.commandName())
		return nil
	}
	_ = proc.Signal(syscall.SIGKILL)
	if waitForExit(state.PID, t.binaryName(), 2*time.Second) {
		t.clearState()
		_, _ = fmt.Fprintf(t.stdout, "%s stopped\n", t.commandName())
		return nil
	}
	return fmt.Errorf("failed to stop %s pid %d", t.commandName(), state.PID)
}

func waitForExit(pid int, expected string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processRunning(pid, expected) {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return !processRunning(pid, expected)
}

func (t *frpTool) restart(opts frpFlagOptions, extra []string) error {
	if err := t.stop(); err != nil {
		return err
	}
	return t.start(opts, extra)
}

func (t *frpTool) status(opts frpFlagOptions) error {
	state, running := t.currentState()
	config := t.maybeResolveConfig(opts.Config, state.Config)
	binary := t.maybeResolveBinary(opts.Binary, state.Binary)
	logPath := firstNonEmpty(strings.TrimSpace(state.Log), t.defaultLogPath(config))
	hints, _ := parseFRPConfigHints(t.kind, config)

	status := "stopped"
	if running {
		status = "running"
	}
	_, _ = fmt.Fprintf(t.stdout, "%s status\n", t.commandName())
	_, _ = fmt.Fprintf(t.stdout, "state: %s\n", status)
	if state.PID > 0 {
		_, _ = fmt.Fprintf(t.stdout, "pid: %d\n", state.PID)
	}
	if strings.TrimSpace(binary) != "" {
		_, _ = fmt.Fprintf(t.stdout, "binary: %s\n", binary)
	}
	if strings.TrimSpace(config) != "" {
		_, _ = fmt.Fprintf(t.stdout, "config: %s\n", config)
	}
	if strings.TrimSpace(logPath) != "" {
		_, _ = fmt.Fprintf(t.stdout, "log: %s\n", logPath)
	}
	if strings.TrimSpace(state.StartedAt) != "" {
		_, _ = fmt.Fprintf(t.stdout, "started_at: %s\n", state.StartedAt)
	}
	if t.kind == frpServerKind {
		if hints.BindPort != "" {
			_, _ = fmt.Fprintf(t.stdout, "bind: %s\n", formatEndpoint(firstNonEmpty(hints.BindAddr, "0.0.0.0"), hints.BindPort))
		}
		if hints.WebPort != "" {
			_, _ = fmt.Fprintf(t.stdout, "dashboard: %s\n", formatEndpoint(firstNonEmpty(hints.WebAddr, "127.0.0.1"), hints.WebPort))
		}
	} else {
		if hints.ServerAddr != "" || hints.ServerPort != "" {
			_, _ = fmt.Fprintf(t.stdout, "server: %s\n", formatEndpoint(firstNonEmpty(hints.ServerAddr, "127.0.0.1"), firstNonEmpty(hints.ServerPort, "7000")))
		}
		if hints.WebPort != "" {
			_, _ = fmt.Fprintf(t.stdout, "admin: %s\n", formatEndpoint(firstNonEmpty(hints.WebAddr, "127.0.0.1"), hints.WebPort))
		}
	}
	if running {
		if listeners, err := describeProcessSockets(state.PID, true); err == nil && strings.TrimSpace(listeners) != "" {
			_, _ = fmt.Fprintf(t.stdout, "\nlisteners:\n%s\n", listeners)
		}
		if t.kind == frpClientKind {
			if text, err := t.tryNativeCommand(binary, config, "status"); err == nil && strings.TrimSpace(text) != "" {
				_, _ = fmt.Fprintf(t.stdout, "\nproxy status:\n%s", ensureTrailingNewline(text))
			}
		}
	}
	return nil
}

func formatEndpoint(addr, port string) string {
	addr = strings.TrimSpace(addr)
	port = strings.TrimSpace(port)
	if addr == "" && port == "" {
		return ""
	}
	if addr == "" {
		addr = "127.0.0.1"
	}
	if port == "" {
		return addr
	}
	return addr + ":" + port
}

func (t *frpTool) connections(opts frpFlagOptions) error {
	state, running := t.currentState()
	if !running {
		return fmt.Errorf("%s is not running", t.commandName())
	}
	config := t.maybeResolveConfig(opts.Config, state.Config)
	binary := t.maybeResolveBinary(opts.Binary, state.Binary)
	if t.kind == frpClientKind {
		if text, err := t.tryNativeCommand(binary, config, "status"); err == nil && strings.TrimSpace(text) != "" {
			_, _ = fmt.Fprint(t.stdout, ensureTrailingNewline(text))
			return nil
		}
	}
	text, err := describeProcessSockets(state.PID, false)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		_, _ = fmt.Fprintf(t.stdout, "%s has no current TCP sockets\n", t.commandName())
		return nil
	}
	_, _ = fmt.Fprintf(t.stdout, "%s connections (pid %d)\n%s\n", t.commandName(), state.PID, text)
	return nil
}

func (t *frpTool) clients(opts frpFlagOptions) error {
	if t.kind != frpServerKind {
		return fmt.Errorf("%s does not support clients; use %s status or connections", t.commandName(), t.commandName())
	}
	state, running := t.currentState()
	if !running {
		return fmt.Errorf("%s is not running", t.commandName())
	}
	config := t.maybeResolveConfig(opts.Config, state.Config)
	hints, err := parseFRPConfigHints(t.kind, config)
	if err != nil {
		return err
	}
	if strings.TrimSpace(hints.WebPort) == "" {
		return fmt.Errorf("%s dashboard API is unavailable because webServer.port is not configured in %s", t.commandName(), config)
	}
	apiBase := "http://" + formatEndpoint(firstNonEmpty(hints.WebAddr, "127.0.0.1"), hints.WebPort)
	items, err := fetchFRPClients(apiBase, hints.WebUser, hints.WebPassword)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		_, _ = fmt.Fprintln(t.stdout, "no online frp clients")
		return nil
	}
	for _, item := range items {
		_, _ = fmt.Fprintf(t.stdout, "%s\t%s\t%s\t%s\t%s\t%t\n",
			firstNonEmpty(item.Key, "-"),
			firstNonEmpty(item.User, "-"),
			firstNonEmpty(item.ClientID, "-"),
			firstNonEmpty(item.Hostname, "-"),
			firstNonEmpty(item.ClientIP, "-"),
			item.Online,
		)
	}
	return nil
}

func describeProcessSockets(pid int, listenersOnly bool) (string, error) {
	if path, err := exec.LookPath("ss"); err == nil && path != "" {
		args := []string{"-tanp"}
		if listenersOnly {
			args = []string{"-ltnp"}
		}
		out, cmdErr := exec.Command("ss", args...).CombinedOutput()
		if cmdErr == nil {
			needle := fmt.Sprintf("pid=%d,", pid)
			var lines []string
			for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "State") || strings.HasPrefix(line, "Recv-Q") {
					continue
				}
				if strings.Contains(line, needle) {
					lines = append(lines, line)
				}
			}
			return strings.Join(lines, "\n"), nil
		}
	}
	if path, err := exec.LookPath("lsof"); err == nil && path != "" {
		args := []string{"-Pan", "-p", strconv.Itoa(pid), "-iTCP"}
		if listenersOnly {
			args = append(args, "-sTCP:LISTEN")
		}
		out, cmdErr := exec.Command("lsof", args...).CombinedOutput()
		if cmdErr == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}
	return "", errors.New("cannot inspect sockets: neither ss nor lsof is available")
}

func fetchFRPClients(apiBase, user, password string) ([]frpClientInfo, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(apiBase, "/")+"/api/clients?status=online", nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(user) != "" || strings.TrimSpace(password) != "" {
		req.SetBasicAuth(user, password)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("frps dashboard api %s returned http %d: %s", req.URL.String(), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var items []frpClientInfo
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (t *frpTool) reload(opts frpFlagOptions) error {
	state, running := t.currentState()
	if !running {
		return fmt.Errorf("%s is not running", t.commandName())
	}
	config, err := t.resolveConfig(opts.Config, state.Config)
	if err != nil {
		return err
	}
	binary, err := t.resolveBinary(opts.Binary, state.Binary)
	if err != nil {
		return err
	}
	if text, cmdErr := t.tryNativeCommand(binary, config, "reload"); cmdErr == nil {
		if strings.TrimSpace(text) != "" {
			_, _ = fmt.Fprint(t.stdout, ensureTrailingNewline(text))
		} else {
			_, _ = fmt.Fprintf(t.stdout, "%s reloaded\n", t.commandName())
		}
		return nil
	}
	_, _ = fmt.Fprintf(t.stdout, "%s native reload is unavailable; restarting with the same config\n", t.commandName())
	return t.restart(opts, state.ExtraArgs)
}

func (t *frpTool) tryNativeCommand(binary, config, subcommand string) (string, error) {
	if strings.TrimSpace(binary) == "" {
		return "", errors.New("binary is required")
	}
	if strings.TrimSpace(config) == "" {
		return "", errors.New("config is required")
	}
	cmd := exec.Command(binary, subcommand, "-c", config)
	cmd.Dir = filepath.Dir(config)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		message := strings.TrimSpace(text)
		if message == "" {
			message = err.Error()
		}
		return text, errors.New(message)
	}
	return text, nil
}

func (t *frpTool) logs(opts frpFlagOptions) error {
	state, _ := t.currentState()
	config := t.maybeResolveConfig(opts.Config, state.Config)
	logPath := firstNonEmpty(strings.TrimSpace(state.Log), t.defaultLogPath(config))
	if strings.TrimSpace(logPath) == "" {
		return fmt.Errorf("cannot determine log path for %s", t.commandName())
	}
	text, err := tailTextFile(logPath, opts.Lines)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(t.stdout, ensureTrailingNewline(text))
	return nil
}

func tailTextFile(path string, lines int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if lines <= 0 {
		lines = 100
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	parts := strings.Split(text, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n"), nil
}

func newFRPToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("frp_%x", buf), nil
}

func ensureFRPServerToken(configPath string) (changed bool, token string, err error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false, "", err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if token = findFRPScalar(text, "auth.token", "token"); strings.TrimSpace(token) != "" {
		return false, strings.TrimSpace(token), nil
	}
	generated, err := newFRPToken()
	if err != nil {
		return false, "", err
	}
	updated, changed := setFRPScalar(text, "auth.token", generated)
	if !changed {
		updated = strings.TrimRight(text, "\n") + "\n\nauth.token = \"" + generated + "\"\n"
		changed = true
	}
	if _, ok := findFRPScalarWithSource(updated, "auth.method"); !ok {
		if rewritten, methodChanged := setFRPScalar(updated, "auth.method", "token"); methodChanged {
			updated = rewritten
		} else {
			updated = strings.TrimRight(updated, "\n") + "\n" + `auth.method = "token"` + "\n"
		}
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return false, "", err
	}
	return true, generated, nil
}

func syncLocalFRPClientToken(serverConfigPath, token string) (string, bool) {
	serverHints, err := parseFRPConfigHints(frpServerKind, serverConfigPath)
	if err != nil {
		return "", false
	}
	serverPort := strings.TrimSpace(serverHints.BindPort)
	if serverPort == "" {
		serverPort = strings.TrimSpace(findFRPScalarFromFile(serverConfigPath, "bindPort", "bind_port"))
	}
	clientPath := filepath.Join(filepath.Dir(serverConfigPath), "frpc.toml")
	if _, err := os.Stat(clientPath); err != nil {
		return "", false
	}
	clientHints, err := parseFRPConfigHints(frpClientKind, clientPath)
	if err != nil {
		return "", false
	}
	serverAddr := strings.TrimSpace(clientHints.ServerAddr)
	clientServerPort := strings.TrimSpace(clientHints.ServerPort)
	if serverAddr != "127.0.0.1" && serverAddr != "localhost" && serverAddr != "" {
		return "", false
	}
	if serverPort != "" && clientServerPort != "" && serverPort != clientServerPort {
		return "", false
	}
	data, err := os.ReadFile(clientPath)
	if err != nil {
		return "", false
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	updated, changed := setFRPScalar(text, "auth.token", token)
	if !changed {
		updated = strings.TrimRight(text, "\n") + "\nauth.token = \"" + token + "\"\n"
	}
	if err := os.WriteFile(clientPath, []byte(updated), 0o644); err != nil {
		return "", false
	}
	return clientPath, true
}

func findFRPScalarFromFile(path string, keys ...string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return findFRPScalar(string(data), keys...)
}

func findFRPScalarWithSource(text string, keys ...string) (string, bool) {
	for _, key := range keys {
		re := regexp.MustCompile(`(?mi)^([ \t-]*` + regexp.QuoteMeta(key) + `[ \t]*[:=][ \t]*)(.+?)\s*$`)
		if match := re.FindStringSubmatch(text); len(match) == 3 {
			value := cleanFRPConfigValue(match[2])
			if value != "" {
				return value, true
			}
			return "", true
		}
	}
	return "", false
}

func setFRPScalar(text, key, value string) (string, bool) {
	re := regexp.MustCompile(`(?mi)^([ \t-]*` + regexp.QuoteMeta(key) + `[ \t]*[:=][ \t]*)(.+?)\s*$`)
	if !re.MatchString(text) {
		return text, false
	}
	replaced := re.ReplaceAllString(text, `${1}"`+value+`"`)
	return replaced, true
}

func ensureTrailingNewline(text string) string {
	if text == "" || strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

func (t *frpTool) raw(args []string) error {
	wrapperArgs, rest, err := parseFRPFlags(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("usage: %s raw -- <real %s args...>", t.commandName(), t.binaryName())
	}
	state, _ := t.currentState()
	binary, err := t.resolveBinary(wrapperArgs.Binary, state.Binary)
	if err != nil {
		return err
	}
	cmd := exec.Command(binary, rest...)
	cmd.Stdout = t.stdout
	cmd.Stderr = t.stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func parseFRPConfigHints(kind frpKind, path string) (frpConfigHints, error) {
	if strings.TrimSpace(path) == "" {
		return frpConfigHints{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return frpConfigHints{}, err
	}
	text := string(data)
	webSections := []string{"webServer", "web_server"}
	if kind == frpServerKind {
		return frpConfigHints{
			BindAddr:    firstNonEmpty(strings.TrimSpace(os.Getenv("FRP_SERVER_BIND_ADDR")), findFRPScalar(text, "bindAddr", "bind_addr")),
			BindPort:    firstNonEmpty(strings.TrimSpace(os.Getenv("FRP_SERVER_BIND_PORT")), findFRPScalar(text, "bindPort", "bind_port")),
			WebAddr:     firstNonEmpty(strings.TrimSpace(os.Getenv("FRP_SERVER_WEB_ADDR")), findFRPScalar(text, "webServer.addr", "web_server.addr"), findFRPSectionScalar(text, webSections, []string{"addr"}), findFRPScalar(text, "dashboard_addr")),
			WebPort:     firstNonEmpty(strings.TrimSpace(os.Getenv("FRP_SERVER_WEB_PORT")), findFRPScalar(text, "webServer.port", "web_server.port"), findFRPSectionScalar(text, webSections, []string{"port"}), findFRPScalar(text, "dashboard_port")),
			WebUser:     firstNonEmpty(strings.TrimSpace(os.Getenv("FRP_SERVER_WEB_USER")), findFRPScalar(text, "webServer.user", "web_server.user"), findFRPSectionScalar(text, webSections, []string{"user"}), findFRPScalar(text, "dashboard_user")),
			WebPassword: firstNonEmpty(strings.TrimSpace(os.Getenv("FRP_SERVER_WEB_PASSWORD")), findFRPScalar(text, "webServer.password", "web_server.password"), findFRPSectionScalar(text, webSections, []string{"password"}), findFRPScalar(text, "dashboard_pwd", "dashboard_password")),
		}, nil
	}
	return frpConfigHints{
		ServerAddr:  firstNonEmpty(strings.TrimSpace(os.Getenv("FRP_CLIENT_SERVER_ADDR")), findFRPScalar(text, "serverAddr", "server_addr")),
		ServerPort:  firstNonEmpty(strings.TrimSpace(os.Getenv("FRP_CLIENT_SERVER_PORT")), findFRPScalar(text, "serverPort", "server_port")),
		WebAddr:     firstNonEmpty(strings.TrimSpace(os.Getenv("FRP_CLIENT_ADMIN_ADDR")), findFRPScalar(text, "webServer.addr", "web_server.addr"), findFRPSectionScalar(text, webSections, []string{"addr"}), findFRPScalar(text, "admin_addr")),
		WebPort:     firstNonEmpty(strings.TrimSpace(os.Getenv("FRP_CLIENT_ADMIN_PORT")), findFRPScalar(text, "webServer.port", "web_server.port"), findFRPSectionScalar(text, webSections, []string{"port"}), findFRPScalar(text, "admin_port")),
		WebUser:     firstNonEmpty(strings.TrimSpace(os.Getenv("FRP_CLIENT_ADMIN_USER")), findFRPScalar(text, "webServer.user", "web_server.user"), findFRPSectionScalar(text, webSections, []string{"user"}), findFRPScalar(text, "admin_user")),
		WebPassword: firstNonEmpty(strings.TrimSpace(os.Getenv("FRP_CLIENT_ADMIN_PASSWORD")), findFRPScalar(text, "webServer.password", "web_server.password"), findFRPSectionScalar(text, webSections, []string{"password"}), findFRPScalar(text, "admin_pwd", "admin_password")),
	}, nil
}

func findFRPScalar(text string, keys ...string) string {
	for _, key := range keys {
		re := regexp.MustCompile(`(?mi)^[ \t-]*` + regexp.QuoteMeta(key) + `[ \t]*[:=][ \t]*(.+?)\s*$`)
		if match := re.FindStringSubmatch(text); len(match) == 2 {
			if value := cleanFRPConfigValue(match[1]); value != "" {
				return value
			}
		}
	}
	return ""
}

func findFRPSectionScalar(text string, sections, keys []string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for _, section := range sections {
		iniHeader := "[" + section + "]"
		for i := 0; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == iniHeader {
				var block []string
				for j := i + 1; j < len(lines); j++ {
					next := strings.TrimSpace(lines[j])
					if strings.HasPrefix(next, "[") && strings.HasSuffix(next, "]") {
						break
					}
					block = append(block, lines[j])
				}
				if value := findFRPScalar(strings.Join(block, "\n"), keys...); value != "" {
					return value
				}
			}
		}

		for i := 0; i < len(lines); i++ {
			line := lines[i]
			trimmed := strings.TrimSpace(line)
			if trimmed != section+":" {
				continue
			}
			baseIndent := leadingIndentWidth(line)
			var block []string
			for j := i + 1; j < len(lines); j++ {
				nextLine := lines[j]
				nextTrimmed := strings.TrimSpace(nextLine)
				if nextTrimmed == "" {
					block = append(block, nextLine)
					continue
				}
				if leadingIndentWidth(nextLine) <= baseIndent {
					break
				}
				block = append(block, nextLine)
			}
			if value := findFRPScalar(strings.Join(block, "\n"), keys...); value != "" {
				return value
			}
		}
	}
	return ""
}

func leadingIndentWidth(line string) int {
	count := 0
	for _, r := range line {
		if r == ' ' || r == '\t' {
			count++
			continue
		}
		break
	}
	return count
}

func cleanFRPConfigValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, `"`) {
		if idx := strings.LastIndex(value, `"`); idx > 0 {
			value = value[1:idx]
			return strings.TrimSpace(value)
		}
	}
	if strings.HasPrefix(value, `'`) {
		if idx := strings.LastIndex(value, `'`); idx > 0 {
			value = value[1:idx]
			return strings.TrimSpace(value)
		}
	}
	if idx := strings.Index(value, "#"); idx >= 0 {
		value = value[:idx]
	}
	if idx := strings.Index(value, ";"); idx >= 0 {
		value = value[:idx]
	}
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, ",")
	return strings.TrimSpace(value)
}

func (t *frpTool) helpText() string {
	name := t.commandName()
	binary := t.binaryName()
	configEnv := t.configEnvName()
	binEnv := t.binEnvName()
	logEnv := t.logEnvName()
	configExamples := strings.Join(t.defaultConfigCandidates(), ", ")
	binaryExamples := append([]string{"`" + binary + "` on PATH"}, wrapBackticks(t.defaultBinaryCandidates())...)
	reloadNote := "reload tries the native FRP reload command first and falls back to restart"
	connectionsNote := "connections shows current TCP sockets owned by the background process"
	if t.kind == frpClientKind {
		connectionsNote = "connections prefers native `frpc status -c <config>` output and falls back to current TCP sockets"
	}
	return fmt.Sprintf(`%s - %s wrapper

Usage:
  %s <command> [args]

Commands:
  help                                      Show this help
  tools                                     Show machine-friendly command list
  start [--config PATH] [--bin PATH] [-- ...]
                                            Start %s in background
  run [--config PATH] [--bin PATH] [-- ...]
                                            Run %s in foreground
  stop                                      Stop the background process
  restart [--config PATH] [--bin PATH] [-- ...]
                                            Restart the background process
  status [--config PATH] [--bin PATH]       Show process, config, and listener info
  connections                               Show current process connections
  clients                                   List currently connected frp clients (server only)
  reload [--config PATH] [--bin PATH]       Reload config or restart in place
  logs [N]                                  Show recent log lines
  raw -- <real %s args...>                  Pass through to the real FRP binary

Defaults:
  config: %s or %s
  binary: %s or %s
  log: %s or %s.log next to the config file
  state dir: %s

Notes:
  - start writes pid and state files under %s
  - %s
  - %s
  - if the real FRP binary is not installed, set %s/%s explicitly`, name, t.roleLabel(), name, binary, binary, binary, configEnv, configExamples, binEnv, strings.Join(binaryExamples, ", "), logEnv, binary, t.stateDir(), t.stateDir(), connectionsNote, reloadNote, configEnv, binEnv)
}

func wrapBackticks(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, "`"+value+"`")
	}
	return out
}

func (t *frpTool) toolsText() string {
	name := t.commandName()
	binary := t.binaryName()
	reloadNote := "native hot reload when supported, otherwise restart with the same config"
	if t.kind == frpClientKind {
		reloadNote = "calls `frpc reload -c <config>` when available, otherwise restarts the client"
	}
	return "# " + name + " tools\n\n" +
		"- " + name + " start [--config PATH] [--bin PATH] -> start " + binary + " in the background and record pid/log/state\n" +
		"- " + name + " run [--config PATH] [--bin PATH] -> run " + binary + " in the foreground\n" +
		"- " + name + " stop -> stop the background process\n" +
		"- " + name + " restart [--config PATH] [--bin PATH] -> restart the process with the same wrapper behavior\n" +
		"- " + name + " status -> show current process state, config path, binary path, and parsed listener info\n" +
		"- " + name + " connections -> show current process connections or native proxy status when available\n" +
		"- " + name + " clients -> query the frps dashboard API and list currently connected clients (server only)\n" +
		"- " + name + " reload -> " + reloadNote + "\n" +
		"- " + name + " logs [N] -> print recent log lines\n" +
		"- " + name + " raw -- <real " + binary + " args...> -> call the real FRP binary directly"
}
