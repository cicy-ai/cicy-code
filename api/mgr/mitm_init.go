package main

// MITM lifecycle wiring. Reads ~/cicy-ai/mitm/config.json (or default
// disabled config if file is missing), starts the SOCKS5 listener if
// enabled, and exposes /api/mitm/ca for the install-ca CLI.
//
// See docs/v1/mitm-system-design.md for the full design.

import (
	"context"
	"log"
	"net/http"

	"ttyd-go/mgr/mitm"
)

// Package-level handle so /api/mitm/ca can read the CA PEM from the
// running server. Set by startMITM; never set elsewhere.
var mitmServer *mitm.Server

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

	srv, err := mitm.NewServer(cfg, mitmAuditAdapter{}, mitmBreakerAdapter{})
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
