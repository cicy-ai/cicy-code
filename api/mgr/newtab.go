// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"regexp"
)

// newtabProfileRE keeps only a safe profile token (digits) for the pill.
var newtabProfileRE = regexp.MustCompile(`[^0-9]`)

// newtabLogoSVG returns the CiCy brand mark, inlined so the start page is fully
// self-contained (no external asset request — the Electron cicyui:// page is a
// secure context, and an external http img would be blocked as mixed content).
// Single source: app/assets/logos/cicy-logo.svg, baked into the ui embed at build
// — change that ONE file (and rebuild the frontend) to change the logo everywhere.
func newtabLogoSVG() string {
	if b, err := uiFS.ReadFile("ui/assets/logos/cicy-logo.svg"); err == nil && len(b) > 0 {
		return string(b)
	}
	// Fallback if the embed is absent (dev): a minimal white CiCy sparkle.
	return `<svg viewBox="0 0 96 96" fill="#fff"><path d="M48 11L39.5 33.3L16 29.5L31 48L16 66.5L39.5 62.7L48 85L56.5 62.7L80 66.5L65 48L80 29.5L56.5 33.3Z"/></svg>`
}

// handleNewtab serves the ONE canonical browser start page ("新标签页") shared by
// every surface: the Electron tab browser (cicyui://newtab fetches this) and the
// Chrome profile start tab both point here. So the logo / layout / profile pill
// live in EXACTLY ONE place — edit this handler (and /assets/logos/cicy-logo.svg)
// and both surfaces update on the next tab open, with no cicy-desktop rebuild.
//
// It carries ONLY what the page itself can know: logo, title, and Profile #N from
// ?profile=N. The agent-driver prompt (which needs a live client_id +
// webContentsId) is deliberately NOT here — the panel injects that per-tab where
// those values exist; a tab opened without the panel simply shows this base page.
//
// Public (no auth): a fresh tab must be able to load it without a token.
func handleNewtab(w http.ResponseWriter, r *http.Request) {
	profile := newtabProfileRE.ReplaceAllString(r.URL.Query().Get("profile"), "")
	pill := ""
	if profile != "" {
		pill = `<div class="pid" data-id="start-profile-id">Profile #` + profile + `</div>`
	}
	html := `<!doctype html><html lang="zh"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>起始页</title>
<style>:root{color-scheme:dark}*{box-sizing:border-box}
html,body{height:100%;margin:0}
body{display:flex;align-items:center;justify-content:center;background:#202124;color:#e8eaed;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif}
.w{text-align:center}
.logo{width:56px;height:56px;margin:0 auto 16px}
.logo svg{width:100%;height:100%;display:block}
h1{font-size:16px;font-weight:600;margin:0 0 6px}
p{color:#9aa0a6;font-size:13px;margin:0}
.pid{display:inline-block;margin:14px 0 0;padding:4px 12px;border-radius:999px;background:rgba(139,92,246,.18);border:1px solid rgba(139,92,246,.35);color:#c4b5fd;font-size:13px;font-weight:600;font-family:ui-monospace,Menlo,Consolas,monospace}</style></head>
<body><div class="w">
<div class="logo">` + newtabLogoSVG() + `</div>
<h1>CiCy Browser</h1>
<p>新标签页</p>` + pill + `
</div></body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(html))
}
