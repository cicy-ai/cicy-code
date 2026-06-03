package main

// local_registry.go — the node's always-on self-hosted skill registry.
//
// Every cicy-code daemon hosts a private registry, permanently, mounted on its
// own :8008 mux under "/registry". There is no start/stop. To share it you hand
// a teammate the node's public address + a read token; they subscribe with a
// URL + a team name (which becomes their install sub-path).
//
// "我的库" lists the node's own skills (~/cicy-ai/skills/private/*) and lets each
// be toggled public — publishing it into the registry so subscribers can pull.
//
// The read token lives in ~/cicy-ai/global.json (key "skill_registry_token"),
// generated on first boot. The registry is never open by default.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ttyd-go/skillcmd"
)

// localRegMountPrefix is where the registry is mounted on the daemon mux.
const localRegMountPrefix = "/registry"

// globalRegTokenKey is the global.json key holding the registry read token.
const globalRegTokenKey = "skill_registry_token"

// localRegistryConfig (~/cicy-ai/local-registry.json) holds non-secret-ish bits.
// The read token now lives in global.json; Token here is read once for legacy
// migration only.
type localRegistryConfig struct {
	Dir        string `json:"dir"`
	Token      string `json:"token"`       // legacy: migrated into global.json on boot
	AdminToken string `json:"admin_token"` // optional: gate remote publish/yank
}

var (
	localRegMu      sync.Mutex
	localRegHandler http.Handler // always non-nil after ensureLocalRegistry; StripPrefix-wrapped
)

func localRegistryConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "cicy-ai", "local-registry.json")
}

func loadLocalRegConfig() localRegistryConfig {
	cfg := localRegistryConfig{Dir: skillcmd.DefaultLocalRegistryDir()}
	if data, err := os.ReadFile(localRegistryConfigPath()); err == nil {
		_ = json.Unmarshal(data, &cfg)
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
	return os.WriteFile(localRegistryConfigPath(), b, 0o600)
}

func genToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// regReadToken returns the registry read token from global.json ("" if unset).
func regReadToken() string {
	cfg := readGlobalJSONConfig()
	if t, ok := cfg[globalRegTokenKey].(string); ok {
		return strings.TrimSpace(t)
	}
	return ""
}

// regSetToken persists the registry read token into global.json (read-modify-
// write under the shared lock so we don't stomp other keys like api_token).
func regSetToken(tok string) error {
	providersFileMu.Lock()
	defer providersFileMu.Unlock()
	cfg := readGlobalJSONConfig()
	cfg[globalRegTokenKey] = tok
	return writeGlobalJSONConfig(cfg)
}

// daemonPort mirrors main()'s PORT resolution (default 8008).
func daemonPort() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8008"
}

// selfPublicURL is the node's externally-reachable origin — the public URL the
// daemon was launched with (CICY_PUBLIC_URL, injected by dev.py).
func selfPublicURL() string {
	if u := strings.TrimRight(os.Getenv("CICY_PUBLIC_URL"), "/"); u != "" {
		return u
	}
	return "http://localhost:" + daemonPort()
}

// shareAddress is the full registry base a teammate subscribes to.
func shareAddress() string { return selfPublicURL() + localRegMountPrefix }

// shareURL is the single copy-paste share link: the address with the read token
// embedded as ?token=…. The subscriber pastes just this (the client extracts
// the token and sends it as a Bearer header, never as a query param).
func shareURL(token string) string {
	if token == "" {
		return shareAddress()
	}
	return shareAddress() + "?token=" + neturl.QueryEscape(token)
}

// armLocalRegistryLocked (re)builds the mounted handler. Caller holds localRegMu.
func armLocalRegistryLocked(cfg localRegistryConfig, token string) error {
	h, err := skillcmd.NewRegistryHandlerWithPrefix(cfg.Dir, token, cfg.AdminToken, selfPublicURL(), localRegMountPrefix)
	if err != nil {
		return err
	}
	localRegHandler = http.StripPrefix(localRegMountPrefix, h)
	return nil
}

// ensureLocalRegistry runs once at daemon boot. Resolves the read token (from
// global.json, migrating a legacy local-registry.json token, else generating
// one) and arms the handler. The registry is always on — no enable/disable.
func ensureLocalRegistry() {
	localRegMu.Lock()
	defer localRegMu.Unlock()
	// global.json holds secrets (api_token, registry read token) — keep it
	// owner-only even if a previous version wrote it world-readable.
	_ = os.Chmod(cicyGlobalJSONPath, 0o600)
	cfg := loadLocalRegConfig()
	tok := regReadToken()
	if tok == "" {
		if strings.TrimSpace(cfg.Token) != "" {
			tok = strings.TrimSpace(cfg.Token) // migrate legacy token → global.json
		} else {
			tok = genToken()
		}
		_ = regSetToken(tok)
	}
	// Don't let a migrated token linger as a duplicate secret in
	// local-registry.json — global.json is now the source of truth.
	if strings.TrimSpace(cfg.Token) != "" {
		cfg.Token = ""
		_ = saveLocalRegConfig(cfg)
	}
	_ = armLocalRegistryLocked(cfg, tok)
}

// serveLocalRegistry is mounted at /registry/ on the daemon mux.
func serveLocalRegistry(w http.ResponseWriter, r *http.Request) {
	localRegMu.Lock()
	h := localRegHandler
	localRegMu.Unlock()
	if h == nil {
		localRegMu.Lock()
		_ = armLocalRegistryLocked(loadLocalRegConfig(), regReadToken())
		h = localRegHandler
		localRegMu.Unlock()
	}
	if h == nil {
		httpErr(w, 500, "local registry unavailable")
		return
	}
	h.ServeHTTP(w, r)
}

// handleLocalRegistry: manage the always-on registry (no start/stop).
//
//	GET  /api/local-registry            → status (address, token, my skills)
//	POST /api/local-registry/rotate     → rotate the read token (invalidates old)
//	POST /api/local-registry/publish    → {name} share a private skill (publish)
//	POST /api/local-registry/unpublish  → {name} stop sharing (remove)
func handleLocalRegistry(w http.ResponseWriter, r *http.Request) {
	action := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/local-registry"), "/")

	localRegMu.Lock()
	defer localRegMu.Unlock()

	switch {
	case r.Method == "GET" && action == "":
		writeLocalRegStatus(w)

	case r.Method == "POST" && action == "rotate":
		tok := genToken()
		if err := regSetToken(tok); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		if err := armLocalRegistryLocked(loadLocalRegConfig(), tok); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		writeLocalRegStatus(w)

	case r.Method == "POST" && action == "publish":
		var body struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		_ = readBody(r, &body)
		path := strings.TrimSpace(body.Path)
		if path == "" {
			name := strings.TrimSpace(body.Name)
			if name == "" {
				httpErr(w, 400, "name or path required")
				return
			}
			path = filepath.Join(skillcmd.PrivateSkillsDir(), name)
		}
		cfg := loadLocalRegConfig()
		name, version, err := skillcmd.PublishToDir(cfg.Dir, path)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		_ = name
		_ = version
		writeLocalRegStatus(w)

	case r.Method == "POST" && action == "unpublish":
		var body struct {
			Name string `json:"name"`
		}
		if err := readBody(r, &body); err != nil || strings.TrimSpace(body.Name) == "" {
			httpErr(w, 400, "name required")
			return
		}
		cfg := loadLocalRegConfig()
		if err := skillcmd.UnpublishFromDir(cfg.Dir, strings.TrimSpace(body.Name)); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		writeLocalRegStatus(w)

	// Back-compat no-ops (the registry is always on now).
	case r.Method == "POST" && (action == "start" || action == "stop"):
		writeLocalRegStatus(w)

	default:
		httpErr(w, 405, "method not allowed")
	}
}

// mySkillView is one row in the "我的库" list: a private skill + whether it is
// currently published (shared) into the registry.
type mySkillView struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Version     string `json:"version"`
	Published   bool   `json:"published"`
}

// writeLocalRegStatus emits the always-on registry's status. Caller holds localRegMu.
func writeLocalRegStatus(w http.ResponseWriter) {
	cfg := loadLocalRegConfig()
	published := map[string]bool{}
	for _, s := range skillcmd.LocalRegistrySkills(cfg.Dir) {
		published[s.Name] = true
	}
	mine := []mySkillView{}
	for _, s := range skillcmd.ListPrivateSkills() {
		v := mySkillView{Name: s.Name, Version: s.Version, Published: published[s.Name]}
		if ms := privateMarketSkill(s.Name); ms != nil {
			v.Title = ms.Title
			v.Description = ms.Description
			v.Category = ms.Category
			if v.Version == "" {
				v.Version = ms.Version
			}
		}
		mine = append(mine, v)
	}
	tok := regReadToken()
	J(w, M{
		"always_on":  true,
		"address":    shareAddress(),
		"share_url":  shareURL(tok), // single copy-paste link (token embedded)
		"mount_path": localRegMountPrefix,
		"port":       daemonPort(),
		"dir":        cfg.Dir,
		"token":      tok,
		"has_admin":  cfg.AdminToken != "",
		"my_skills":  mine,
	})
}
