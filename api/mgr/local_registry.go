package main

// local_registry.go — host a private skill registry in-process.
//
// The registry server (api/skillcmd) is just an http.Handler, so instead of
// spawning `cicy-code skill registry serve` as a child process we run it on its
// own port inside this daemon and own the lifecycle here. Config is persisted
// to ~/cicy-ai/local-registry.json and auto-started on daemon boot when enabled,
// so "turn on my team's registry" survives restarts.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ttyd-go/skillcmd"
)

type localRegistryConfig struct {
	Enabled    bool   `json:"enabled"`
	Port       int    `json:"port"`
	Dir        string `json:"dir"`
	Token      string `json:"token"`
	AdminToken string `json:"admin_token"`
}

var (
	localRegMu     sync.Mutex
	localRegSrv    *http.Server
	localRegCfgMem localRegistryConfig
)

func localRegistryConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "cicy-ai", "local-registry.json")
}

func loadLocalRegConfig() localRegistryConfig {
	cfg := localRegistryConfig{Port: 8787, Dir: skillcmd.DefaultLocalRegistryDir()}
	data, err := os.ReadFile(localRegistryConfigPath())
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	if cfg.Port == 0 {
		cfg.Port = 8787
	}
	if cfg.Dir == "" {
		cfg.Dir = skillcmd.DefaultLocalRegistryDir()
	}
	return cfg
}

func saveLocalRegConfig(cfg localRegistryConfig) error {
	if err := os.MkdirAll(filepath.Dir(localRegistryConfigPath()), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(localRegistryConfigPath(), b, 0o600) // holds tokens
}

func genToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// startLocalRegistryLocked starts the in-process server. Caller holds localRegMu.
func startLocalRegistryLocked(cfg localRegistryConfig) error {
	if localRegSrv != nil {
		// already running — stop first for a clean restart
		_ = localRegSrv.Close()
		localRegSrv = nil
	}
	handler, err := skillcmd.NewRegistryHandler(cfg.Dir, cfg.Token, cfg.AdminToken, "")
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port), Handler: handler}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listen :%d: %w", cfg.Port, err)
	}
	localRegSrv = srv
	localRegCfgMem = cfg
	go func() { _ = srv.Serve(ln) }()
	return nil
}

func stopLocalRegistryLocked() {
	if localRegSrv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = localRegSrv.Shutdown(ctx)
	localRegSrv = nil
}

// maybeAutostartLocalRegistry is called at daemon boot.
func maybeAutostartLocalRegistry() {
	cfg := loadLocalRegConfig()
	if !cfg.Enabled {
		return
	}
	localRegMu.Lock()
	defer localRegMu.Unlock()
	if err := startLocalRegistryLocked(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[local-registry] autostart failed: %v\n", err)
	}
}

// lanShareURLs returns http://<lan-ip>:<port> candidates to hand to teammates.
func lanShareURLs(port int) []string {
	var urls []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return urls
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		urls = append(urls, fmt.Sprintf("http://%s:%d", ip4.String(), port))
	}
	return urls
}

// handleLocalRegistry: GET status / POST start|stop|publish.
//
//	GET  /api/local-registry            → status
//	POST /api/local-registry/start      → {port?,dir?,token?,admin_token?}
//	POST /api/local-registry/stop
//	POST /api/local-registry/publish    → {path}
func handleLocalRegistry(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, "/api/local-registry")
	action = strings.Trim(action, "/")

	localRegMu.Lock()
	defer localRegMu.Unlock()

	switch {
	case r.Method == "GET" && action == "":
		writeLocalRegStatus(w)

	case r.Method == "POST" && action == "start":
		var body localRegistryConfig
		_ = readBody(r, &body)
		cfg := loadLocalRegConfig()
		if body.Port != 0 {
			cfg.Port = body.Port
		}
		if strings.TrimSpace(body.Dir) != "" {
			cfg.Dir = strings.TrimSpace(body.Dir)
		}
		if strings.TrimSpace(body.Token) != "" {
			cfg.Token = strings.TrimSpace(body.Token)
		}
		if cfg.Token == "" {
			cfg.Token = genToken() // default: protect with a generated read token
		}
		if strings.TrimSpace(body.AdminToken) != "" {
			cfg.AdminToken = strings.TrimSpace(body.AdminToken)
		}
		cfg.Enabled = true
		if err := startLocalRegistryLocked(cfg); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		if err := saveLocalRegConfig(cfg); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		writeLocalRegStatus(w)

	case r.Method == "POST" && action == "stop":
		stopLocalRegistryLocked()
		cfg := loadLocalRegConfig()
		cfg.Enabled = false
		_ = saveLocalRegConfig(cfg)
		writeLocalRegStatus(w)

	case r.Method == "POST" && action == "publish":
		var body struct {
			Path string `json:"path"`
		}
		if err := readBody(r, &body); err != nil || strings.TrimSpace(body.Path) == "" {
			httpErr(w, 400, "path required")
			return
		}
		cfg := loadLocalRegConfig()
		name, version, err := skillcmd.PublishToDir(cfg.Dir, strings.TrimSpace(body.Path))
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		J(w, M{"ok": true, "name": name, "version": version})

	default:
		httpErr(w, 405, "method not allowed")
	}
}

// writeLocalRegStatus emits the current host status. Caller holds localRegMu.
func writeLocalRegStatus(w http.ResponseWriter) {
	cfg := loadLocalRegConfig()
	running := localRegSrv != nil
	// when running, reflect the live config (port/dir/token may differ from disk
	// only transiently, but they're persisted on start so this matches)
	if running {
		cfg = localRegCfgMem
	}
	J(w, M{
		"running":    running,
		"port":       cfg.Port,
		"dir":        cfg.Dir,
		"token":      cfg.Token, // host's own session; needed to share
		"has_admin":  cfg.AdminToken != "",
		"skills":     skillcmd.LocalRegistrySkills(cfg.Dir),
		"share_urls": lanShareURLs(cfg.Port),
	})
}
