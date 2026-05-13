package main

import (
	"bufio"
	"compress/gzip"
	"net"
	"net/http"
	"strings"
)

func gzipCompressibleType(ct string) bool {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return false
	}
	// Streaming responses must not be buffered through gzip.
	if ct == "text/event-stream" || strings.HasPrefix(ct, "multipart/") {
		return false
	}
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/javascript", "text/javascript", "application/x-javascript",
		"application/json", "application/manifest+json", "application/ld+json",
		"application/xml", "text/xml", "image/svg+xml", "application/wasm",
		"application/vnd.ms-fontobject", "font/ttf", "font/otf", "font/woff", "font/woff2",
		"application/font-sfnt", "application/x-font-ttf":
		return true
	}
	return false
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gw          *gzip.Writer
	wroteHeader bool
}

func (g *gzipResponseWriter) ensure(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	h := g.Header()
	if code == http.StatusOK && h.Get("Content-Encoding") == "" && gzipCompressibleType(h.Get("Content-Type")) {
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		g.gw = gzip.NewWriter(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) WriteHeader(code int) { g.ensure(code) }

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.ensure(http.StatusOK)
	}
	if g.gw != nil {
		return g.gw.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipResponseWriter) Close() {
	if g.gw != nil {
		_ = g.gw.Close()
	}
}

func (g *gzipResponseWriter) Flush() {
	if g.gw != nil {
		_ = g.gw.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := g.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// withGzip gzip-encodes compressible responses (js/css/html/json/svg/wasm/fonts)
// when the client supports it. Skips websocket upgrades and already-encoded bodies.
func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") ||
			r.Header.Get("Sec-WebSocket-Key") != "" ||
			strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
			strings.EqualFold(r.Header.Get("Connection"), "upgrade") {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.Close()
		next.ServeHTTP(gw, r)
	})
}
