package main

// MITM lifecycle wiring. Reads ~/cicy-ai/mitm/config.json (or default
// disabled config if file is missing), starts the SOCKS5 listener if
// enabled, and exposes /api/mitm/ca for the install-ca CLI.
//
// See docs/v1/mitm-system-design.md for the full design.

import (
	"context"
	"fmt"
	"log"
	"net/http"

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
)

// mitmAgentProxyBootLines returns boot export lines that route a non-gateway
// agent's outbound HTTPS through the local MITM's HTTP CONNECT listener, using
// the agent's pane id as the proxy-auth username so the MITM's socks5_username
// identity rule attributes every captured turn (and its reply callback) to the
// right agent. node-based CLIs (claude/opencode/codex via undici) honor
// HTTP(S)_PROXY but reject SOCKS5, so we use the http:// proxy here. Also adds
// the MITM CA. Returns nil unless MITM is running (disabled by default), so the
// common boot path is unchanged.
func mitmAgentProxyBootLines() []string {
	if mitmServer == nil || mitmHTTPAddr == "" {
		return nil
	}
	lines := []string{
		fmt.Sprintf(`export HTTPS_PROXY="http://${X_AGENT_SHORT_ID}:x@%s"`, mitmHTTPAddr),
		fmt.Sprintf(`export HTTP_PROXY="http://${X_AGENT_SHORT_ID}:x@%s"`, mitmHTTPAddr),
	}
	if mitmCACertPath != "" {
		lines = append(lines, fmt.Sprintf(`export NODE_EXTRA_CA_CERTS="%s"`, mitmCACertPath))
	}
	return lines
}

// mitmEgressResolver implements mitm.EgressFunc. MITM routes its upstream dials
// (intercept + passthrough) through the local mihomo mixed port, so the exit IP
// follows whatever node mihomo currently has selected. Read live on every dial —
// toggle the flag in global.json and it takes effect on the next request, no
// restart. The local mihomo needs no proxy auth, so auth is empty.
//
// Default ON: route through mihomo unless explicitly turned off
// (mihomo_global_egress:false). Safe by construction —
//   - DialTCP fails open to a direct dial when mihomo is unreachable, so a
//     first-boot box (mihomo not up yet) or a mihomo restart never breaks agent
//     traffic; and
//   - the default mihomo node is 'direct' (cicy-mihomo template), so even once
//     mihomo is up, traffic exits the box's own IP until a real egress node is
//     configured.
//
// So on first install everything works, and dropping in a real node changes the
// exit IP with no flag flip. Set mihomo_global_egress:false to force MITM to
// bypass mihomo entirely (static upstream.mode, direct by default).
func mitmEgressResolver() (enabled bool, socks5Addr string, auth string) {
	cfg := readGlobalJSONConfig()
	if v, ok := cfg["mihomo_global_egress"].(bool); ok && !v {
		return false, "", ""
	}
	return true, mihomoMixedAddr(), ""
}

// startMITM is called once at server startup, after audit.Init.
// Safe to call when MITM is disabled — it logs and returns.
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

	// CA cert download — operator runs `cicy-code mitm install-ca` which
	// fetches this endpoint and installs into the OS trust store.
	http.HandleFunc("/api/mitm/ca", w(handleMITMCA))
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
