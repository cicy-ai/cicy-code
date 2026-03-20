package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func mitmCACertPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mitmproxy", "mitmproxy-ca-cert.pem")
}

// GET /ca.pem — download mitmproxy CA certificate
func handleCACert(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(mitmCACertPath())
	if err != nil {
		httpErr(w, 404, "CA certificate not found. Start mitmproxy first to generate it.")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", "attachment; filename=cicy-audit-ca.pem")
	w.Write(data)
}

// GET /install-ca — bash script to download and install CA cert
func handleInstallCA(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, installCAScript)
}

const installCAScript = `#!/bin/bash
set -euo pipefail

AUDIT_HOST="${AUDIT_HOST:-audit.cicy-ai.com}"
CERT_URL="https://${AUDIT_HOST}/ca.pem"
CERT_NAME="cicy-audit-ca"

echo "🔐 CiCy Audit — CA Certificate Installer"
echo "   Downloading from ${CERT_URL}"
echo ""

TMP=$(mktemp /tmp/cicy-ca-XXXXXX.pem)
curl -fsSL "${CERT_URL}" -o "${TMP}"

if [ ! -s "${TMP}" ]; then
  echo "❌ Failed to download CA certificate"
  rm -f "${TMP}"
  exit 1
fi

OS=$(uname -s)
case "${OS}" in
  Linux)
    echo "📦 Installing on Linux..."
    if command -v update-ca-certificates &>/dev/null; then
      sudo cp "${TMP}" "/usr/local/share/ca-certificates/${CERT_NAME}.crt"
      sudo update-ca-certificates
    elif command -v update-ca-trust &>/dev/null; then
      sudo cp "${TMP}" "/etc/pki/ca-trust/source/anchors/${CERT_NAME}.pem"
      sudo update-ca-trust
    else
      echo "⚠️  Cannot auto-install. Copy manually:"
      echo "   ${TMP} → /usr/local/share/ca-certificates/"
      exit 0
    fi
    ;;
  Darwin)
    echo "📦 Installing on macOS..."
    sudo security add-trusted-cert -d -r trustRoot \
      -k /Library/Keychains/System.keychain "${TMP}"
    ;;
  *)
    echo "⚠️  Unsupported OS: ${OS}"
    echo "   Certificate saved to: ${TMP}"
    echo "   Please install it manually as a trusted CA."
    exit 0
    ;;
esac

rm -f "${TMP}"
echo ""
echo "✅ CA certificate installed successfully!"
echo ""
echo "📋 Next step — configure your proxy:"
echo "   export https_proxy=https://YOUR_TOKEN:x@${AUDIT_HOST}:8003"
echo ""
echo "   Or get a token at https://${AUDIT_HOST}"
`

// GET /setup — setup guide JSON
func handleSetupGuide(w http.ResponseWriter, r *http.Request) {
	hasCert := false
	if _, err := os.Stat(mitmCACertPath()); err == nil {
		hasCert = true
	}
	J(w, M{
		"success": true,
		"data": M{
			"proxy_host":  "audit.cicy-ai.com",
			"proxy_port":  mitmproxyPort(),
			"ca_cert_url": "https://audit.cicy-ai.com/ca.pem",
			"install_cmd": "curl -fsSL https://audit.cicy-ai.com/install-ca | bash",
			"ca_ready":    hasCert,
			"platforms": []M{
				{
					"name": "macOS / Linux (CLI tools)",
					"steps": []string{
						"curl -fsSL https://audit.cicy-ai.com/install-ca | bash",
						"export https_proxy=https://YOUR_TOKEN:x@audit.cicy-ai.com:8003",
					},
				},
				{
					"name": "Cursor / VS Code",
					"steps": []string{
						"Install CA certificate first (see above)",
						"Add to settings.json: \"http.proxy\": \"https://YOUR_TOKEN:x@audit.cicy-ai.com:8003\"",
					},
				},
				{
					"name": "Claude Code / Kiro CLI",
					"steps": []string{
						"Install CA certificate first (see above)",
						"export https_proxy=https://YOUR_TOKEN:x@audit.cicy-ai.com:8003",
						"Run your AI tool normally",
					},
				},
			},
		},
	})
}

// Serve audit SPA — serves index.html for all non-API, non-asset routes on audit.cicy-ai.com
func handleAuditSPA(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if !strings.Contains(host, "audit") {
		http.NotFound(w, r)
		return
	}
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/") || path == "/ca.pem" || path == "/install-ca" || path == "/setup" {
		return
	}
	// Serve the audit SPA
	auditDir := filepath.Join(monitorDir, "web", "dist")
	if _, err := os.Stat(filepath.Join(auditDir, "index.html")); err != nil {
		// Fallback: serve inline SPA from Go
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, auditFallbackHTML)
		return
	}
	// Serve static files, fallback to index.html for SPA routes
	fs := http.Dir(auditDir)
	if f, err := fs.Open(path); err == nil {
		f.Close()
		http.FileServer(fs).ServeHTTP(w, r)
	} else {
		http.ServeFile(w, r, filepath.Join(auditDir, "index.html"))
	}
}

const auditFallbackHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>CiCy Audit</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{font-family:system-ui;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#0a0a0a;color:#e5e5e5}
.c{text-align:center;max-width:500px;padding:2rem}h1{font-size:2rem;margin-bottom:.5rem}
p{color:#a3a3a3;line-height:1.6}code{background:#1e1e1e;padding:2px 8px;border-radius:4px;font-size:.9em}
a{color:#60a5fa;text-decoration:none}a:hover{text-decoration:underline}</style>
</head><body><div class="c">
<h1>🔍 CiCy Audit</h1>
<p>AI Traffic Audit Service</p>
<p style="margin-top:2rem">The audit dashboard is being built.<br>
API is ready at <code>/api/audit/*</code></p>
<p style="margin-top:1rem"><a href="/ca.pem">Download CA Certificate</a> · 
<a href="/setup">Setup Guide</a></p>
</div></body></html>`
