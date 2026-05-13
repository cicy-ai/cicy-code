package main

import (
	"embed"
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

func appDistDir() string {
	if d := strings.TrimSpace(os.Getenv("CICY_PREVIEW_DIST")); d != "" {
		return d
	}
	return filepath.Join("app", "dist")
}

func nonAppPath(p string) bool {
	for _, pre := range []string{"/api/", "/ttyd/", "/code/", "/mitm/", "/pma/", "/static/", "/v1/", "/oauth/"} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return strings.HasPrefix(p, "/stt")
}

// serveUI picks where the web UI comes from:
//   --hot      -> reverse-proxy to the vite dev server on :8022 (HMR)
//   --preview  -> the on-disk app/dist (refresh with `npm run build`)
//   (neither)  -> the binary-embedded assets (the production build baked in by build.sh)
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
	case previewMode:
		diskFS = http.Dir(appDistDir())
		diskSrv = http.FileServer(diskFS)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nonAppPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		if devProxy != nil {
			devProxy.ServeHTTP(w, r)
			return
		}
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		if diskFS != nil {
			if f, err := diskFS.Open(strings.TrimPrefix(path, "/")); err == nil {
				f.Close()
				diskSrv.ServeHTTP(w, r)
				return
			}
			r.URL.Path = "/"
			diskSrv.ServeHTTP(w, r)
			return
		}
		if f, err := sub.Open(strings.TrimPrefix(path, "/")); err == nil {
			f.Close()
			embedded.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		embedded.ServeHTTP(w, r)
	})
}
