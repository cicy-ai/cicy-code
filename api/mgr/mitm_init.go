package main

// MITM lifecycle wiring. Reads ~/cicy-ai/mitm/config.json (or default
// disabled config if file is missing), starts the SOCKS5 listener if
// enabled, and exposes /api/mitm/ca for the install-ca CLI.
//
// See docs/v1/mitm-system-design.md for the full design.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ttyd-go/mgr/mitm"
)

// Package-level handle so /api/mitm/ca can read the CA PEM from the
// running server. Set by startMITM; never set elsewhere.
var mitmServer *mitm.Server

// Captured from the MITM config at startup so the agent boot path can route
// traffic through the MITM. Empty unless MITM is enabled+running.
var (
	mitmSOCKS5Addr string
	mitmHTTPAddr   string
	mitmCACertPath string
	// mitmCABundlePath is a concatenation of the OS root CAs + our MITM CA, so
	// Rust agents (codex/kiro-cli) can trust the MITM via SSL_CERT_FILE without
	// root/sudo and without dropping the real internet roots. Empty until built.
	mitmCABundlePath string
)

// mitmAgentProxyBootLines returns boot export lines that route a non-gateway
// agent's outbound HTTPS through the local MITM's HTTP CONNECT listener, using
// the agent's pane id as the proxy-auth username so the MITM's socks5_username
// identity rule attributes every captured turn (and its reply callback) to the
// right agent. node-based CLIs (claude/opencode/codex via undici) honor
// HTTP(S)_PROXY but reject SOCKS5, so we use the http:// proxy here. Also adds
// the MITM CA. Returns nil unless MITM is running (disabled by default), so the
// common boot path is unchanged.
//
// useMitm is the per-agent agent_config.use_mitm switch (default ON; surfaced
// in the inspector only for non-gateway agents). With it off, a non-gateway
// agent boots with NO proxy override — its traffic follows the global mihomo
// env from .cicy_tmux.conf and is not MITM-audited.
func mitmAgentProxyBootLines(useMitm bool) []string {
	if !useMitm || mitmServer == nil || mitmHTTPAddr == "" {
		return nil
	}
	// Export the FULL set (upper+lower, incl. ALL_PROXY) so a non-gateway
	// agent's MITM proxy completely overrides the global mihomo proxy that
	// .cicy_tmux.conf sets on every shell (HTTP/HTTPS/ALL_PROXY=127.0.0.1:9001).
	// If we only set HTTP(S)_PROXY, ALL_PROXY/lowercase would leak the agent's
	// traffic straight to mihomo, bypassing MITM capture.
	mitmURL := fmt.Sprintf("http://${X_AGENT_SHORT_ID}:x@%s", mitmHTTPAddr)
	lines := []string{
		fmt.Sprintf(`export HTTPS_PROXY="%s"`, mitmURL),
		fmt.Sprintf(`export HTTP_PROXY="%s"`, mitmURL),
		fmt.Sprintf(`export ALL_PROXY="%s"`, mitmURL),
		fmt.Sprintf(`export https_proxy="%s"`, mitmURL),
		fmt.Sprintf(`export http_proxy="%s"`, mitmURL),
		fmt.Sprintf(`export all_proxy="%s"`, mitmURL),
	}
	if mitmCACertPath != "" {
		lines = append(lines, fmt.Sprintf(`export NODE_EXTRA_CA_CERTS="%s"`, mitmCACertPath))
	}
	// Rust agents (codex/kiro-cli) read OS/openssl trust, NOT NODE_EXTRA_CA_CERTS,
	// so without this they hit `invalid peer certificate: UnknownIssuer` on the
	// MITM-intercepted TLS. SSL_CERT_FILE points at a bundle that INCLUDES the OS
	// roots (built by ensureMITMCABundle), so the real internet keeps working and
	// the MITM CA is additionally trusted — no system-store install / sudo needed.
	// rustls-native-certs + openssl + python(requests via SSL_CERT_FILE) all honor it.
	if mitmCABundlePath != "" {
		lines = append(lines, fmt.Sprintf(`export SSL_CERT_FILE="%s"`, mitmCABundlePath))
	}
	return lines
}

// ensureMITMCABundle writes <db>/mitm-ca-bundle.crt = OS root CAs + the MITM CA,
// so the agent boot path can hand Rust agents an SSL_CERT_FILE that trusts BOTH
// the public internet and the MITM, without root and without the "bare CA
// replaces the system roots" footgun. Best-effort: on any failure the bundle path
// stays empty and boot simply omits SSL_CERT_FILE (Rust non-gateway agents then
// fall back to needing the system-store install).
func ensureMITMCABundle() {
	if mitmCACertPath == "" {
		return
	}
	mitmCA, err := os.ReadFile(mitmCACertPath)
	if err != nil {
		log.Printf("[mitm] read CA for bundle failed: %v", err)
		return
	}
	out := filepath.Join(filepath.Dir(mitmCACertPath), "mitm-ca-bundle.crt")
	sys := osRootCAs(out)
	var buf bytes.Buffer
	if len(sys) > 0 {
		buf.Write(sys)
		if !bytes.HasSuffix(sys, []byte("\n")) {
			buf.WriteByte('\n')
		}
	} else {
		log.Printf("[mitm] no OS root bundle found — SSL_CERT_FILE bundle would drop public roots; skipping")
		return
	}
	buf.Write(mitmCA)
	if !bytes.HasSuffix(mitmCA, []byte("\n")) {
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(out, buf.Bytes(), 0644); err != nil {
		log.Printf("[mitm] write CA bundle failed: %v", err)
		return
	}
	mitmCABundlePath = out
	log.Printf("[mitm] CA bundle ready: %s (OS roots + MITM CA)", out)
}

// osRootCAs returns the platform's trusted root CAs as PEM, WITHOUT requiring any
// privileged install (the whole point is a zero-friction, no-sudo/no-GUI trust
// path). macOS keeps roots in the Keychain rather than a file, so we export them
// read-only via `security find-certificate` (reading needs no authorization; only
// WRITING to the keychain would prompt). Other Unixes ship a PEM bundle on disk.
// selfPath is our own output bundle, skipped so we never fold it back in.
func osRootCAs(selfPath string) []byte {
	if runtime.GOOS == "darwin" {
		var out bytes.Buffer
		for _, kc := range []string{
			"/System/Library/Keychains/SystemRootCertificates.keychain", // built-in roots
			"/Library/Keychains/System.keychain",                        // admin-added roots
		} {
			if b, err := exec.Command("security", "find-certificate", "-a", "-p", kc).Output(); err == nil && len(b) > 0 {
				out.Write(b)
				if !bytes.HasSuffix(b, []byte("\n")) {
					out.WriteByte('\n')
				}
			}
		}
		return out.Bytes()
	}
	for _, c := range []string{
		strings.TrimSpace(os.Getenv("SSL_CERT_FILE")),
		"/etc/ssl/certs/ca-certificates.crt", // Debian/Ubuntu/Alpine
		"/etc/pki/tls/certs/ca-bundle.crt",   // RHEL/Fedora/CentOS
		"/etc/ssl/ca-bundle.pem",             // openSUSE
		"/etc/ssl/cert.pem",                  // BSD
	} {
		if c == "" || c == selfPath {
			continue
		}
		if b, e := os.ReadFile(c); e == nil && len(b) > 0 {
			return b
		}
	}
	return nil
}

// mitmEgressResolver implements mitm.EgressFunc. MITM ALWAYS routes its upstream
// dials (intercept + passthrough) through the local mihomo mixed port, so the
// exit IP follows whatever node mihomo currently has selected. The local mihomo
// needs no proxy auth, so auth is empty.
//
// Always ON — there is no opt-out flag (the former mihomo_global_egress:false
// escape hatch was removed; egress must go through mihomo). Safe by construction:
//   - DialTCP fails open to a direct dial when mihomo is unreachable, so a
//     first-boot box (mihomo not up yet) or a mihomo restart never breaks agent
//     traffic; and
//   - the default mihomo node is 'direct' (cicy-mihomo template), so even once
//     mihomo is up, traffic exits the box's own IP until a real egress node is
//     configured.
//
// So on first install everything works, and dropping in a real node changes the
// exit IP — all without any flag.
func mitmEgressResolver() (enabled bool, socks5Addr string, auth string) {
	return true, mihomoMixedAddr(), ""
}

// startMITM is called once at server startup, after audit.Init.
// Safe to call when MITM is disabled — it logs and returns.
// mitmHTTPListenAddr returns the loopback address for the MITM HTTP CONNECT
// proxy. Default is the fixed port 8007; override with CICY_MITM_HTTP_PORT (or
// config http_connect_listen). Always 127.0.0.1 — the proxy must never be
// reachable off-box (the API port may bind 0.0.0.0 in container mode, but this
// listener stays loopback-only). Running a 2nd instance on one host? Set
// CICY_MITM_HTTP_PORT on it so the two don't both grab 8007.
func mitmHTTPListenAddr() string {
	if p := strings.TrimSpace(os.Getenv("CICY_MITM_HTTP_PORT")); p != "" {
		return "127.0.0.1:" + p
	}
	return "127.0.0.1:8007"
}

func startMITM() {
	cfg, err := mitm.LoadConfig("")
	if err != nil {
		log.Printf("[mitm] config load failed (disabled): %v", err)
		return
	}
	if !cfg.Enabled {
		log.Printf("[mitm] disabled in config")
		return
	}

	// Default the HTTP CONNECT listen address to 8007 unless the config pinned it
	// (http_connect_listen) or CICY_MITM_HTTP_PORT overrides. SOCKS5 stays as-is —
	// empty means OFF (Start skips it); only chain nodes set socks5_listen.
	if cfg.HTTPConnectListen == "" {
		cfg.HTTPConnectListen = mitmHTTPListenAddr()
	}

	srv, err := mitm.NewServer(cfg, mitmAuditAdapter{}, mitmBreakerAdapter{}, mitmEgressResolver)
	if err != nil {
		log.Printf("[mitm] init failed: %v", err)
		return
	}
	if srv == nil {
		return
	}
	if err := srv.Start(context.Background()); err != nil {
		log.Printf("[mitm] start failed: %v", err)
		return
	}
	mitmServer = srv
	mitmSOCKS5Addr = cfg.SOCKS5Listen
	mitmHTTPAddr = cfg.HTTPConnectListen
	mitmCACertPath = cfg.CA.CertPath
	ensureMITMCABundle() // build OS-roots+MITM bundle for Rust agents' SSL_CERT_FILE

	// CA cert download — operator runs `cicy-code mitm install-ca` which
	// fetches this endpoint and installs into the OS trust store. `/ca.pem` is
	// the same local MITM CA, exposed at the short path the audit dashboard's
	// "install CA" card links to (so the manual-download link works against a
	// self-hosted node instead of the central audit service).
	http.HandleFunc("/api/mitm/ca", w(handleMITMCA))
	http.HandleFunc("/ca.pem", w(handleMITMCA))
	http.HandleFunc("/api/mitm/ca-status", w(handleMITMCAStatus))
	// Consent gate (compliance §1.4): the cicy-desktop card POSTs here to opt in
	// (or revoke) OS-trust install. Authenticated (team Bearer) — it mutates
	// system trust anchors.
	http.HandleFunc("/api/mitm/consent", wa(handleMITMConsent))
}

// handleMITMCAStatus reports whether this node's MITM CA is trusted in the OS
// trust store. The agent inspector calls it when a codex / kiro-cli pane is
// switched to non-gateway: those run their real HTTP from a Rust binary that
// reads the OS store (unlike node agents, which trust the CA via
// NODE_EXTRA_CA_CERTS automatically), so on a host without the CA installed
// their MITM-intercepted TLS would fail. Linux installs it automatically; macOS
// needs the one-time `cicy-code mitm install-ca`.
func handleMITMCAStatus(rw http.ResponseWriter, r *http.Request) {
	// Agents (codex/claude/…) trust the CA via NODE_EXTRA_CA_CERTS once the user
	// consents, so "trusted"/"installed" track CONSENT, not the OS trust store —
	// enabling audit is silent and has nothing to do with the system keychain.
	consent := mitmServer != nil && mitm.CATrustConsented()
	resp := map[string]any{
		"enabled":   mitmServer != nil,
		"platform":  runtime.GOOS,
		"generated": mitmServer != nil, // CA exists once the server started it
		"trusted":   consent,           // agents trust via NODE_EXTRA_CA_CERTS on consent
		"consent":   consent,
		"installed": consent, // legacy alias (kept for older callers)
		"command":   "cicy-code mitm install-ca",
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(resp)
}

// handleMITMConsent is the desktop consent card's backend.
//
//	POST {enable:true}  → record opt-in + install the CA into the OS trust store
//	POST {enable:false} → uninstall + revoke
//
// On {enable:true} the privileged store write happens IN THIS PROCESS — silent
// when cicy-code is elevated (production schtasks = High integrity), else it
// returns {ok:false, error:"need_elevation"} so the card can fall back to an
// elevated `cicy-code mitm install-ca` (UAC/polkit/osascript prompt). The
// compliance red line — never install without consent — lives here: consent is
// recorded only on a successful (or already-trusted) install.
func handleMITMConsent(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(rw, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(rw, http.StatusBadRequest, "invalid body")
		return
	}
	writeJSON := func(v any) {
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(v)
	}

	if !body.Enable {
		// Revoke: best-effort uninstall, then clear the flag regardless.
		uninstallMITMCAOSTrust()
		if err := mitm.ClearCATrustConsent(); err != nil {
			writeJSON(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(map[string]any{"ok": true, "trusted": mitmCATrustedInOS(), "consent": false})
		return
	}

	// Enable = record the user's consent, full stop. That consent is the whole
	// opt-in: the agents cicy-code launches (codex/claude/opencode/…) trust the
	// MITM CA via the NODE_EXTRA_CA_CERTS we inject at launch, so auditing turns on
	// silently — NO OS keychain trust, NO admin, NO GUI dialog. The card is purely a
	// compliance "you are turning on HTTPS audit" acknowledgement; it has nothing to
	// do with the system trust store. (Anyone who additionally wants non-agent apps
	// like Safari to trust the CA can run `cicy-code mitm install-ca` themselves.)
	if err := mitm.SetCATrustConsent(time.Now().Format(time.RFC3339), "desktop"); err != nil {
		writeJSON(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(map[string]any{"ok": true, "trusted": true, "consent": true})
}

// mitmCATrustedInOS checks the platform trust store for this node's MITM CA,
// using the same signals as ensureMITMCAInSystemTrust (Linux: the installed copy
// matches; macOS: a trust setting exists).
func mitmCATrustedInOS() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	srcBytes, err := os.ReadFile(filepath.Join(home, "cicy-ai", "db", "mitm-ca.crt"))
	if err != nil {
		return false
	}
	switch runtime.GOOS {
	case "linux":
		cur, err := os.ReadFile("/usr/local/share/ca-certificates/cicy-mitm.crt")
		return err == nil && bytes.Equal(cur, srcBytes)
	case "darwin":
		// Trusted in the USER domain (our install target — `dump-trust-settings` with
		// no flag) OR the admin/system domain (`-d`, older/admin-scope installs).
		// Either counts as installed so the card doesn't nag to reinstall a CA the
		// user already trusts for themselves.
		if out, err := exec.Command("security", "dump-trust-settings").CombinedOutput(); err == nil && bytes.Contains(bytes.ToLower(out), []byte("cicy-mitm")) {
			return true
		}
		out, err := exec.Command("security", "dump-trust-settings", "-d").CombinedOutput()
		return err == nil && bytes.Contains(bytes.ToLower(out), []byte("cicy-mitm"))
	case "windows":
		return mitm.RootCATrusted(srcBytes)
	}
	return false
}

func handleMITMCA(rw http.ResponseWriter, r *http.Request) {
	if mitmServer == nil {
		http.Error(rw, "mitm not running", http.StatusServiceUnavailable)
		return
	}
	pem := mitmServer.RootCertPEM()
	if pem == nil {
		http.Error(rw, "mitm CA unavailable", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "application/x-pem-file")
	rw.Header().Set("Content-Disposition", `attachment; filename="cicy-mitm-ca.crt"`)
	_, _ = rw.Write(pem)
}
