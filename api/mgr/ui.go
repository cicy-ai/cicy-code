// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

//go:embed ui
var uiFS embed.FS

// BuiltAppCDNPrefix is baked in at build time via ldflags (-X
// main.BuiltAppCDNPrefix=…) and consumed below for --cdn asset rewriting. It
// formerly lived in cos_active_versions.go, which was removed with the
// Tencent-COS active-version heartbeat (COS→R2 migration).
var BuiltAppCDNPrefix string

// previewDistDir resolves the on-disk SPA build to serve, or "" to fall back to
// the binary-embedded assets.
//
// CICY_PREVIEW_DIST is enough on its own — pointing it at a dist without also
// passing --preview used to be a silent no-op (the env var was only read from
// inside the previewMode branch), which reads as "the env var doesn't work".
// Naming a directory to serve IS the intent to serve it.
//
// The bare --preview default stays a RELATIVE "app/dist", i.e. relative to the
// process CWD — it only resolves when launched from the repo root. Set
// CICY_PREVIEW_DIST to an absolute path to serve a dist from anywhere.
func previewDistDir() string {
	if d := strings.TrimSpace(os.Getenv("CICY_PREVIEW_DIST")); d != "" {
		return d
	}
	if previewMode {
		return filepath.Join("app", "dist")
	}
	return ""
}

// cdnRewriteIndex rewrites the App SPA index.html so its root-absolute asset
// references point at the baked-in R2 prefix instead of the local origin. Only
// active under --cdn with a non-empty BuiltAppCDNPrefix; otherwise a no-op. The
// built index uses relative root paths (vite base '/') — `="/assets/…"` and
// `="/favicon…"` — so a targeted prefix-prepend covers <script>, <link>,
// modulepreload, and the favicon without touching anything else.
func cdnRewriteIndex(b []byte) []byte {
	if !cdnMode {
		return b
	}
	prefix := strings.TrimRight(strings.TrimSpace(BuiltAppCDNPrefix), "/")
	if prefix == "" {
		return b
	}
	s := string(b)
	s = strings.ReplaceAll(s, `="/assets/`, `="`+prefix+`/assets/`)
	s = strings.ReplaceAll(s, `="/favicon`, `="`+prefix+`/favicon`)
	return []byte(s)
}

func nonAppPath(p string) bool {
	for _, pre := range []string{"/api/", "/ttyd/", "/agent/", "/mitm/", "/pma/", "/static/", "/v1/", "/oauth/"} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return strings.HasPrefix(p, "/stt")
}

// serveUI picks where the web UI comes from:
//
//	--hot                -> reverse-proxy to the vite dev server on :8022 (HMR)
//	--preview            -> the on-disk app/dist (refresh with `npm run build`)
//	CICY_PREVIEW_DIST=…  -> that directory, --preview implied
//	(none of the above)  -> the binary-embedded assets (the production build baked in by build.sh)
//
// --hot still wins over both: an explicit "proxy to vite" beats "serve a dist".
func serveUI() http.Handler {
	sub, _ := fs.Sub(uiFS, "ui")
	embedded := http.FileServer(http.FS(sub))

	var devProxy *httputil.ReverseProxy
	var diskFS http.FileSystem
	var diskSrv http.Handler
	switch {
	case hotMode:
		if target, err := url.Parse("http://127.0.0.1:8022"); err == nil {
			devProxy = httputil.NewSingleHostReverseProxy(target)
		}
	default:
		if dir := previewDistDir(); dir != "" {
			diskFS = http.Dir(dir)
			diskSrv = http.FileServer(diskFS)
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nonAppPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		// index.html references hashed asset filenames, so we never want
		// browsers to cache it; the hashed /assets/* are safe forever.
		p := r.URL.Path
		switch {
		case p == "/" || p == "/index.html" || strings.HasSuffix(p, ".html"):
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(p, "/assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if devProxy != nil {
			devProxy.ServeHTTP(w, r)
			return
		}
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// Under --cdn we rewrite the index's asset URLs to R2 at serve time.
		// Only the index needs this; hashed /assets/* are still served locally
		// as a fallback (the browser fetches them from R2 in cdn mode).
		rewriteActive := cdnMode && strings.TrimSpace(BuiltAppCDNPrefix) != ""
		serveIndex := func() {
			if rewriteActive {
				var b []byte
				var err error
				if diskFS != nil {
					if f, ferr := diskFS.Open("index.html"); ferr == nil {
						b, err = io.ReadAll(f)
						f.Close()
					} else {
						err = ferr
					}
				} else {
					b, err = fs.ReadFile(sub, "index.html")
				}
				if err == nil {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Write(cdnRewriteIndex(b))
					return
				}
			}
			r.URL.Path = "/"
			if diskSrv != nil {
				diskSrv.ServeHTTP(w, r)
			} else {
				embedded.ServeHTTP(w, r)
			}
		}

		if diskFS != nil {
			if f, err := diskFS.Open(strings.TrimPrefix(path, "/")); err == nil {
				f.Close()
				if path == "/index.html" && rewriteActive {
					serveIndex()
					return
				}
				diskSrv.ServeHTTP(w, r)
				return
			}
			// A missing hashed asset is not an SPA route. Returning index.html here
			// gives module requests text/html and, worse, the /assets cache policy
			// stores that bad response as immutable. Let the browser see a real 404
			// so a stale index can recover on refresh after a deployment.
			if strings.HasPrefix(path, "/assets/") {
				w.Header().Set("Cache-Control", "no-store")
				http.NotFound(w, r)
				return
			}
			serveIndex()
			return
		}
		if f, err := sub.Open(strings.TrimPrefix(path, "/")); err == nil {
			f.Close()
			if path == "/index.html" && rewriteActive {
				serveIndex()
				return
			}
			embedded.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(path, "/assets/") {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		serveIndex()
	})
}
