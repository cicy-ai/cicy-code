package hosttools

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRunCFTunnelHelpDoesNotRequireConfig(t *testing.T) {
	var stdout bytes.Buffer
	env := &Env{
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
	}

	if err := env.runCFTunnel([]string{"help"}); err != nil {
		t.Fatalf("runCFTunnel(help) error = %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Usage: cf-tunnel <list|add|del> [ports...]") {
		t.Fatalf("unexpected stdout: %q", out)
	}
	if !strings.Contains(out, "CF_ENV=prod|dev") {
		t.Fatalf("missing environment help: %q", out)
	}
}

func TestRunTMHelpDoesNotRequireConfig(t *testing.T) {
	var stdout bytes.Buffer
	env := &Env{
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
	}

	if err := env.runTM([]string{"help"}); err != nil {
		t.Fatalf("runTM(help) error = %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Usage: cicy-agent [--node NAME] <command> [args]") {
		t.Fatalf("unexpected stdout: %q", out)
	}
	if !strings.Contains(out, "TM_API_BASE or API_BASE") {
		t.Fatalf("missing config priority: %q", out)
	}
	if !strings.Contains(out, "~/cicy-ai/db/cicy-agent.json") {
		t.Fatalf("missing cicy-agent.json path: %q", out)
	}
}

func TestRunGlobalAPITokenShow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalPath := filepath.Join(home, "cicy-ai", "global.json")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(globalPath, []byte("{\"api_token\":\"cicy_test_show\"}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	env, err := newEnv(&stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("newEnv() error = %v", err)
	}
	if err := env.runGlobalAPIToken([]string{"show"}); err != nil {
		t.Fatalf("runGlobalAPIToken(show) error = %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "cicy_test_show" {
		t.Fatalf("show output = %q", got)
	}
}

func TestRunGlobalAPITokenRefresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "cicy-ai", "global.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("{\"api_token\":\"cicy_old\",\"other\":1}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	env, err := newEnv(&stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("newEnv() error = %v", err)
	}
	if err := env.runGlobalAPIToken([]string{"refresh"}); err != nil {
		t.Fatalf("runGlobalAPIToken(refresh) error = %v", err)
	}

	newToken := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(newToken, "cicy_") {
		t.Fatalf("refresh output = %q", newToken)
	}
	if newToken == "cicy_old" {
		t.Fatalf("refresh did not change token")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), newToken) {
		t.Fatalf("global.json not updated: %s", string(data))
	}
	if !strings.Contains(string(data), "\"other\": 1") {
		t.Fatalf("global.json lost unrelated fields: %s", string(data))
	}
}

func TestResolveTMConfigUsesDefaultNodeFromGlobal(t *testing.T) {
	env := &Env{
		Global: map[string]any{
			"api_token": "global_token",
		},
		TM: map[string]any{
			"default": "prod",
			"nodes": map[string]any{
				"prod": map[string]any{
					"api":       "http://10.0.0.12:8008",
					"api_token": "tm_prod_token",
				},
			},
		},
	}

	cfg, err := env.resolveTMConfig("")
	if err != nil {
		t.Fatalf("resolveTMConfig() error = %v", err)
	}
	if cfg.Node != "prod" {
		t.Fatalf("cfg.Node = %q", cfg.Node)
	}
	if cfg.API != "http://10.0.0.12:8008" {
		t.Fatalf("cfg.API = %q", cfg.API)
	}
	if cfg.Token != "tm_prod_token" {
		t.Fatalf("cfg.Token = %q", cfg.Token)
	}
}

func TestResolveTMConfigUsesTMNodeEnv(t *testing.T) {
	t.Setenv("TM_NODE", "dev")
	env := &Env{
		Global: map[string]any{
			"api_token": "global_token",
		},
		TM: map[string]any{
			"nodes": map[string]any{
				"dev": map[string]any{
					"api":       "http://10.0.0.20:8008",
					"api_token": "dev_token",
				},
			},
		},
	}

	cfg, err := env.resolveTMConfig("")
	if err != nil {
		t.Fatalf("resolveTMConfig() error = %v", err)
	}
	if cfg.Node != "dev" {
		t.Fatalf("cfg.Node = %q", cfg.Node)
	}
	if cfg.API != "http://10.0.0.20:8008" {
		t.Fatalf("cfg.API = %q", cfg.API)
	}
	if cfg.Token != "dev_token" {
		t.Fatalf("cfg.Token = %q", cfg.Token)
	}
}

func TestResolveTMConfigUsesEnvOverride(t *testing.T) {
	t.Setenv("TM_API_BASE", "http://127.0.0.1:9001")
	t.Setenv("TM_TOKEN", "tm_env_token")
	env := &Env{
		Global: map[string]any{
			"api_token": "global_token",
		},
		TM: map[string]any{
			"default": "prod",
			"nodes": map[string]any{
				"prod": map[string]any{
					"api":       "http://10.0.0.12:8008",
					"api_token": "prod_token",
				},
			},
		},
	}

	cfg, err := env.resolveTMConfig("")
	if err != nil {
		t.Fatalf("resolveTMConfig() error = %v", err)
	}
	if cfg.API != "http://127.0.0.1:9001" {
		t.Fatalf("cfg.API = %q", cfg.API)
	}
	if cfg.Token != "tm_env_token" {
		t.Fatalf("cfg.Token = %q", cfg.Token)
	}
}

func TestResolveTMConfigUsesInMemoryDefaultWhenTMJSONMissing(t *testing.T) {
	env := &Env{
		Global: map[string]any{
			"api_token": "cicy_root_token",
		},
		TM: map[string]any{},
	}

	cfg, err := env.resolveTMConfig("")
	if err != nil {
		t.Fatalf("resolveTMConfig() error = %v", err)
	}
	if cfg.Node != "default" {
		t.Fatalf("cfg.Node = %q", cfg.Node)
	}
	if cfg.API != "http://127.0.0.1:8008" {
		t.Fatalf("cfg.API = %q", cfg.API)
	}
	if cfg.Token != "cicy_root_token" {
		t.Fatalf("cfg.Token = %q", cfg.Token)
	}
}

func TestResolveTMConfigRequiresNodeSpecificToken(t *testing.T) {
	env := &Env{
		Global: map[string]any{
			"api_token": "global_token",
		},
		TM: map[string]any{
			"default": "prod",
			"nodes": map[string]any{
				"prod": map[string]any{
					"api": "http://10.0.0.12:8008",
				},
			},
		},
	}

	_, err := env.resolveTMConfig("")
	if err == nil {
		t.Fatal("resolveTMConfig() expected error for missing node token")
	}
	if !strings.Contains(err.Error(), "missing api_token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizeCodeServerOpenPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "file uri with line", input: "file://home/cicy/project/main.go:10", want: "/home/cicy/project/main.go:10"},
		{name: "file uri with line and column", input: "file:///tmp/demo.txt:8:2", want: "/tmp/demo.txt:8:2"},
		{name: "relative path with range", input: "demo.txt:8:1-14:18", want: filepath.Join(mustGetwd(), "demo.txt") + ":8:1-14:18"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeCodeServerOpenPath(tt.input); got != tt.want {
				t.Fatalf("normalizeCodeServerOpenPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRunAgentCodeServerHelp(t *testing.T) {
	var stdout bytes.Buffer
	env := &Env{
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
	}

	if err := env.runAgentCodeServer([]string{"help"}); err != nil {
		t.Fatalf("runAgentCodeServer(help) error = %v", err)
	}

	out := stdout.String()
	for _, want := range []string{"ping [page_client_id]", "list", "open <path> [page_client_id]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q in %q", want, out)
		}
	}
}

func TestRunAgentWebpageHelpDoesNotAdvertiseLegacyAliases(t *testing.T) {
	var stdout bytes.Buffer
	env := &Env{
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
	}

	if err := env.runAgentWebpage([]string{"help"}); err != nil {
		t.Fatalf("runAgentWebpage(help) error = %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "agent-webpage - CiCy live webpage client tool") {
		t.Fatalf("unexpected help output: %q", out)
	}
	for _, banned := range []string{"aliases:", "webpage-ping", "webpage, ", "agent-page-ping"} {
		if strings.Contains(out, banned) {
			t.Fatalf("help output should not mention %q: %q", banned, out)
		}
	}
	for _, want := range []string{"ipc-ping [client_id]", "current-active-agent-id [client_id]", "current-master-agent-id [client_id]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q: %q", want, out)
		}
	}
}

func TestRunAgentCodeServerList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/clients", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"w-10001": map[string]any{
				"web-1":          map[string]any{},
				"web-1:code-ext": map[string]any{},
				"web-2":          map[string]any{},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var stdout bytes.Buffer
	env := &Env{
		API:    srv.URL,
		HTTP:   srv.Client(),
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
	}

	if err := env.runAgentCodeServer([]string{"list"}); err != nil {
		t.Fatalf("runAgentCodeServer(list) error = %v", err)
	}

	var out []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("json.Unmarshal(list output) error = %v; output=%q", err, stdout.String())
	}
	if len(out) != 2 {
		t.Fatalf("list len = %d, want 2", len(out))
	}
	if got := out[0]["page_client_id"]; got != "web-1" {
		t.Fatalf("first page_client_id = %v, want web-1", got)
	}
	if got := out[0]["code_server_connected"]; got != true {
		t.Fatalf("web-1 code_server_connected = %v, want true", got)
	}
	if got := out[1]["page_client_id"]; got != "web-2" {
		t.Fatalf("second page_client_id = %v, want web-2", got)
	}
	if got := out[1]["code_server_connected"]; got != false {
		t.Fatalf("web-2 code_server_connected = %v, want false", got)
	}
}

func TestRunAgentCodeServerOpenPreservesFileURIAndLine(t *testing.T) {
	var (
		pushed   map[string]any
		upgrader = websocket.Upgrader{}
		wsMu     sync.Mutex
		wsConn   *websocket.Conn
		wsReady  = make(chan struct{})
		wsOnce   sync.Once
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/clients", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"w-10001": map[string]any{
				"web-1":          map[string]any{},
				"web-1:code-ext": map[string]any{},
			},
		})
	})
	mux.HandleFunc("/api/chat/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade() error = %v", err)
		}
		wsMu.Lock()
		wsConn = conn
		wsMu.Unlock()
		wsOnce.Do(func() { close(wsReady) })
	})
	mux.HandleFunc("/api/chat/push", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&pushed); err != nil {
			t.Fatalf("Decode(push) error = %v", err)
		}
		data, _ := pushed["data"].(map[string]any)
		select {
		case <-wsReady:
		case <-time.After(2 * time.Second):
			t.Fatal("ws not ready during push")
		}
		wsMu.Lock()
		conn := wsConn
		wsMu.Unlock()
		if conn == nil {
			t.Fatal("wsConn is nil during push")
		}
		if err := conn.WriteJSON(map[string]any{
			"type": "code.opened",
			"data": map[string]any{
				"requestId":      data["requestId"],
				"path":           data["path"],
				"page_client_id": "web-1",
				"code_client_id": "web-1:code-ext",
			},
		}); err != nil {
			t.Fatalf("WriteJSON(code.opened) error = %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var stdout bytes.Buffer
	env := &Env{
		API:    srv.URL,
		WS:     "ws" + strings.TrimPrefix(srv.URL, "http"),
		HTTP:   srv.Client(),
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
	}

	if err := env.runAgentCodeServer([]string{"open", "file://home/cicy/projects/cicy-code/Makefile:8:1-14:18", "web-1"}); err != nil {
		t.Fatalf("runAgentCodeServer(open) error = %v", err)
	}

	if got := pushed["type"]; got != "code.open_file" {
		t.Fatalf("push type = %v, want code.open_file", got)
	}
	if got := pushed["client_id"]; got != "web-1" {
		t.Fatalf("push client_id = %v, want web-1", got)
	}
	data, _ := pushed["data"].(map[string]any)
	if got := data["path"]; got != "/home/cicy/projects/cicy-code/Makefile:8:1-14:18" {
		t.Fatalf("push data.path = %v, want /home/cicy/projects/cicy-code/Makefile:8:1-14:18", got)
	}
	wsMu.Lock()
	_ = wsConn.Close()
	wsMu.Unlock()
}

func TestRunAgentCodeServerPingWaitsForPong(t *testing.T) {
	var (
		upgrader = websocket.Upgrader{}
		wsMu     sync.Mutex
		wsConn   *websocket.Conn
		wsReady  = make(chan struct{})
		wsOnce   sync.Once
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/clients", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"w-10001": map[string]any{
				"web-1":          map[string]any{},
				"web-1:code-ext": map[string]any{},
			},
		})
	})
	mux.HandleFunc("/api/chat/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade() error = %v", err)
		}
		wsMu.Lock()
		wsConn = conn
		wsMu.Unlock()
		wsOnce.Do(func() { close(wsReady) })
	})
	mux.HandleFunc("/api/chat/push", func(w http.ResponseWriter, r *http.Request) {
		var pushed map[string]any
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&pushed); err != nil {
			t.Fatalf("Decode(push) error = %v", err)
		}
		data, _ := pushed["data"].(map[string]any)
		select {
		case <-wsReady:
		case <-time.After(2 * time.Second):
			t.Fatal("ws not ready during push")
		}
		wsMu.Lock()
		conn := wsConn
		wsMu.Unlock()
		if conn == nil {
			t.Fatal("wsConn is nil during push")
		}
		if err := conn.WriteJSON(map[string]any{
			"type": "code.pong",
			"data": map[string]any{
				"requestId":      data["requestId"],
				"page_client_id": "web-1",
				"code_client_id": "web-1:code-ext",
				"version":        "test",
			},
		}); err != nil {
			t.Fatalf("WriteJSON(code.pong) error = %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var stdout bytes.Buffer
	env := &Env{
		API:    srv.URL,
		WS:     "ws" + strings.TrimPrefix(srv.URL, "http"),
		HTTP:   srv.Client(),
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
	}

	if err := env.runAgentCodeServer([]string{"ping", "web-1"}); err != nil {
		t.Fatalf("runAgentCodeServer(ping) error = %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "收到 code.pong") {
		t.Fatalf("ping output missing code.pong confirmation: %q", out)
	}

	wsMu.Lock()
	_ = wsConn.Close()
	wsMu.Unlock()
}
