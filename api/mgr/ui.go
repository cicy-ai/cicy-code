package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed ui
var uiFS embed.FS

func serveUI() http.Handler {
	sub, _ := fs.Sub(uiFS, "ui")
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API 请求不走这里
		if strings.HasPrefix(r.URL.Path, "/api/") ||
			strings.HasPrefix(r.URL.Path, "/ttyd/") ||
			strings.HasPrefix(r.URL.Path, "/code/") ||
			strings.HasPrefix(r.URL.Path, "/mitm/") ||
			strings.HasPrefix(r.URL.Path, "/pma/") ||
			strings.HasPrefix(r.URL.Path, "/static/") ||
			strings.HasPrefix(r.URL.Path, "/v1/") ||
			strings.HasPrefix(r.URL.Path, "/oauth/") ||
			strings.HasPrefix(r.URL.Path, "/stt") {
			http.NotFound(w, r)
			return
		}

		// 尝试静态文件
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// 检查文件是否存在
		f, err := sub.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback: 返回 index.html
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
