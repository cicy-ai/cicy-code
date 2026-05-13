package hosttools

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func TestRunFRPServerHelp(t *testing.T) {
	var stdout bytes.Buffer
	env := &Env{Stdout: &stdout, Stderr: &bytes.Buffer{}}
	if err := env.runFRPServer([]string{"help"}); err != nil {
		t.Fatalf("runFRPServer(help) error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"frp-server", "start", "connections", "reload", "logs [N]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q in %q", want, out)
		}
	}
}

func TestRunFRPClientHelp(t *testing.T) {
	var stdout bytes.Buffer
	env := &Env{Stdout: &stdout, Stderr: &bytes.Buffer{}}
	if err := env.runFRPClient([]string{"help"}); err != nil {
		t.Fatalf("runFRPClient(help) error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"frp-client", "start", "connections", "reload", "raw -- <real frpc args...>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q in %q", want, out)
		}
	}
}

func TestParseFRPConfigHintsServerYAML(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "frps.yaml")
	data := `bindAddr: 0.0.0.0
bindPort: 7000
webServer:
  addr: 127.0.0.1
  port: 7500
  user: admin
  password: secret
`
	if err := os.WriteFile(cfg, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	hints, err := parseFRPConfigHints(frpServerKind, cfg)
	if err != nil {
		t.Fatalf("parseFRPConfigHints() error = %v", err)
	}
	if hints.BindPort != "7000" || hints.WebPort != "7500" || hints.WebUser != "admin" || hints.WebPassword != "secret" {
		t.Fatalf("unexpected hints = %#v", hints)
	}
}

func TestParseFRPConfigHintsClientTOML(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "frpc.toml")
	data := "serverAddr = \"35.220.220.223\"\nserverPort = 7000\n[webServer]\naddr = \"127.0.0.1\"\nport = 7400\nuser = \"admin\"\npassword = \"secret\"\n"
	if err := os.WriteFile(cfg, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	hints, err := parseFRPConfigHints(frpClientKind, cfg)
	if err != nil {
		t.Fatalf("parseFRPConfigHints() error = %v", err)
	}
	if hints.ServerAddr != "35.220.220.223" || hints.ServerPort != "7000" || hints.WebPort != "7400" {
		t.Fatalf("unexpected hints = %#v", hints)
	}
}

func TestFRPClientStartStatusReloadStop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, "data", "frp")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	configPath := filepath.Join(cfgDir, "frpc.toml")
	configText := "serverAddr = \"127.0.0.1\"\nserverPort = 7000\n[webServer]\naddr = \"127.0.0.1\"\nport = 7400\n"
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	binDir := filepath.Join(home, "fake-bin")
	frpcPath := filepath.Join(binDir, "frpc")
	writeExecutable(t, frpcPath, `#!/usr/bin/env sh
set -eu
cmd="${1:-}"
if [ "$cmd" = "reload" ]; then
  echo "frpc reload ok"
  exit 0
fi
if [ "$cmd" = "status" ]; then
  echo "proxy ssh_6000 tcp online"
  exit 0
fi
trap 'exit 0' TERM INT
while :; do sleep 1; done
`)

	var stdout bytes.Buffer
	env := &Env{Stdout: &stdout, Stderr: &bytes.Buffer{}}

	if err := env.runFRPClient([]string{"start", "--config", configPath, "--bin", frpcPath}); err != nil {
		t.Fatalf("runFRPClient(start) error = %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "started in background") {
		t.Fatalf("start output = %q", out)
	}

	statePath := filepath.Join(home, ".local", "state", "cicy-skills", "frp", "client", "state.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file missing: %v", err)
	}

	stdout.Reset()
	if err := env.runFRPClient([]string{"status", "--config", configPath, "--bin", frpcPath}); err != nil {
		t.Fatalf("runFRPClient(status) error = %v", err)
	}
	statusOut := stdout.String()
	for _, want := range []string{"state: running", "config: " + configPath, "server: 127.0.0.1:7000", "admin: 127.0.0.1:7400", "proxy status:"} {
		if !strings.Contains(statusOut, want) {
			t.Fatalf("status output missing %q in %q", want, statusOut)
		}
	}

	stdout.Reset()
	if err := env.runFRPClient([]string{"connections", "--config", configPath, "--bin", frpcPath}); err != nil {
		t.Fatalf("runFRPClient(connections) error = %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "proxy ssh_6000 tcp online") {
		t.Fatalf("connections output = %q", out)
	}

	stdout.Reset()
	if err := env.runFRPClient([]string{"reload", "--config", configPath, "--bin", frpcPath}); err != nil {
		t.Fatalf("runFRPClient(reload) error = %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "frpc reload ok") {
		t.Fatalf("reload output = %q", out)
	}

	stdout.Reset()
	if err := env.runFRPClient([]string{"stop"}); err != nil {
		t.Fatalf("runFRPClient(stop) error = %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "stopped") {
		t.Fatalf("stop output = %q", out)
	}

	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state file should be removed after stop, err=%v", err)
	}
}

func TestFRPServerStartGeneratesTokenAndSyncsLocalClient(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, "data", "frp")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	serverConfigPath := filepath.Join(cfgDir, "frps.toml")
	serverConfigText := "bindAddr = \"0.0.0.0\"\nbindPort = 9500\n"
	if err := os.WriteFile(serverConfigPath, []byte(serverConfigText), 0o644); err != nil {
		t.Fatalf("WriteFile(server config) error = %v", err)
	}
	clientConfigPath := filepath.Join(cfgDir, "frpc.toml")
	clientConfigText := "serverAddr = \"127.0.0.1\"\nserverPort = 9500\n"
	if err := os.WriteFile(clientConfigPath, []byte(clientConfigText), 0o644); err != nil {
		t.Fatalf("WriteFile(client config) error = %v", err)
	}

	binDir := filepath.Join(home, "fake-bin")
	frpsPath := filepath.Join(binDir, "frps")
	writeExecutable(t, frpsPath, `#!/usr/bin/env sh
set -eu
trap 'exit 0' TERM INT
while :; do sleep 1; done
`)

	var stdout bytes.Buffer
	env := &Env{Stdout: &stdout, Stderr: &bytes.Buffer{}}
	if err := env.runFRPServer([]string{"start", "--config", serverConfigPath, "--bin", frpsPath}); err != nil {
		t.Fatalf("runFRPServer(start) error = %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "generated auth token") {
		t.Fatalf("start output should mention generated token: %q", out)
	}

	serverData, err := os.ReadFile(serverConfigPath)
	if err != nil {
		t.Fatalf("ReadFile(server config) error = %v", err)
	}
	serverToken := findFRPScalar(string(serverData), "auth.token")
	if !strings.HasPrefix(serverToken, "frp_") {
		t.Fatalf("server token = %q", serverToken)
	}
	clientData, err := os.ReadFile(clientConfigPath)
	if err != nil {
		t.Fatalf("ReadFile(client config) error = %v", err)
	}
	clientToken := findFRPScalar(string(clientData), "auth.token")
	if clientToken != serverToken {
		t.Fatalf("client token %q != server token %q", clientToken, serverToken)
	}

	stdout.Reset()
	if err := env.runFRPServer([]string{"stop"}); err != nil {
		t.Fatalf("runFRPServer(stop) error = %v", err)
	}
}
func TestFRPServerReloadFallsBackToRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, "data", "frp")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	configPath := filepath.Join(cfgDir, "frps.toml")
	configText := "bindAddr = \"0.0.0.0\"\nbindPort = 7000\n[webServer]\naddr = \"127.0.0.1\"\nport = 7500\n"
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	binDir := filepath.Join(home, "fake-bin")
	frpsPath := filepath.Join(binDir, "frps")
	writeExecutable(t, frpsPath, `#!/usr/bin/env sh
set -eu
cmd="${1:-}"
if [ "$cmd" = "reload" ]; then
  echo "reload not supported" >&2
  exit 1
fi
trap 'exit 0' TERM INT
while :; do sleep 1; done
`)

	var stdout bytes.Buffer
	env := &Env{Stdout: &stdout, Stderr: &bytes.Buffer{}}

	if err := env.runFRPServer([]string{"start", "--config", configPath, "--bin", frpsPath}); err != nil {
		t.Fatalf("runFRPServer(start) error = %v", err)
	}
	tool := newFRPTool(frpServerKind, &bytes.Buffer{}, &bytes.Buffer{})
	stateBefore, running := tool.currentState()
	if !running {
		t.Fatal("frp-server should be running before reload fallback")
	}

	stdout.Reset()
	if err := env.runFRPServer([]string{"reload", "--config", configPath, "--bin", frpsPath}); err != nil {
		t.Fatalf("runFRPServer(reload) error = %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "native reload is unavailable; restarting") {
		t.Fatalf("reload fallback output = %q", out)
	}

	stateAfter, running := tool.currentState()
	if !running {
		t.Fatal("frp-server should be running after reload fallback")
	}
	if stateAfter.PID == stateBefore.PID {
		t.Fatalf("reload fallback should restart the process; pid before=%d after=%d", stateBefore.PID, stateAfter.PID)
	}

	stdout.Reset()
	if err := env.runFRPServer([]string{"stop"}); err != nil {
		t.Fatalf("runFRPServer(stop) error = %v", err)
	}
}

func TestFRPServerClientsUsesDashboardAPI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, "data", "frp")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	configPath := filepath.Join(cfgDir, "frps.toml")
	configText := "bindAddr = \"0.0.0.0\"\nbindPort = 9500\nwebServer.addr = \"127.0.0.1\"\nwebServer.port = 7500\nwebServer.user = \"admin\"\nwebServer.password = \"secret\"\n"
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	var stdout bytes.Buffer
	env := &Env{Stdout: &stdout, Stderr: &bytes.Buffer{}}
	tool := newFRPTool(frpServerKind, &stdout, &bytes.Buffer{})

	mux := http.NewServeMux()
	mux.HandleFunc("/api/clients", func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("unauthorized"))
			return
		}
		if got := r.URL.Query().Get("status"); got != "online" {
			t.Fatalf("status query = %q, want online", got)
		}
		_, _ = w.Write([]byte(`[
  {"key":"alice.mac-ssh","user":"alice","clientID":"mac-ssh","runID":"rid-1","version":"0.68.1","hostname":"macbook","clientIP":"1.2.3.4:5000","online":true}
]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	hostPort := strings.TrimPrefix(srv.URL, "http://")
	parts := strings.Split(hostPort, ":")
	if len(parts) != 2 {
		t.Fatalf("unexpected test server url: %s", srv.URL)
	}
	configText = "bindAddr = \"0.0.0.0\"\nbindPort = 9500\nwebServer.addr = \"" + parts[0] + "\"\nwebServer.port = " + parts[1] + "\nwebServer.user = \"admin\"\nwebServer.password = \"secret\"\n"
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(tool.stateFile()), 0o755); err != nil {
		t.Fatalf("MkdirAll(state dir) error = %v", err)
	}
	frpsScript := filepath.Join(cfgDir, "frps")
	writeExecutable(t, frpsScript, `#!/usr/bin/env sh
trap 'exit 0' TERM INT
while :; do sleep 1; done
`)
	proc := exec.Command(frpsScript)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start(fake process) error = %v", err)
	}
	defer func() {
		_ = proc.Process.Kill()
		_, _ = proc.Process.Wait()
	}()
	if err := os.WriteFile(tool.stateFile(), []byte(`{"pid":`+strconv.Itoa(proc.Process.Pid)+`,"config":"`+configPath+`","binary":"`+frpsScript+`"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(state) error = %v", err)
	}
	if err := os.WriteFile(tool.pidFile(), []byte(strconv.Itoa(proc.Process.Pid)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pid) error = %v", err)
	}

	_ = env
	if err := tool.clients(frpFlagOptions{Config: configPath}); err != nil {
		t.Fatalf("tool.clients() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"alice.mac-ssh", "alice", "mac-ssh", "macbook", "1.2.3.4:5000", "true"} {
		if !strings.Contains(out, want) {
			t.Fatalf("clients output missing %q in %q", want, out)
		}
	}
}
