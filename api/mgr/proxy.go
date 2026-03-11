package main

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

var codeServerProxy *httputil.ReverseProxy
var mitmproxyProxy *httputil.ReverseProxy

func init() {
	target, _ := url.Parse("http://127.0.0.1:18080")
	codeServerProxy = httputil.NewSingleHostReverseProxy(target)
	
	mitmTarget, _ := url.Parse("http://127.0.0.1:18889")
	mitmproxyProxy = httputil.NewSingleHostReverseProxy(mitmTarget)
}

func handleCodeServer(w http.ResponseWriter, r *http.Request) {
	r.URL.Path = r.URL.Path[len("/code"):]
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	r.Header.Del("Authorization")
	codeServerProxy.ServeHTTP(w, r)
}

func handleCodeServerAuth(w http.ResponseWriter, r *http.Request) {
	// Only verify token for root path, bypass for assets
	if r.URL.Path == "/code/" || r.URL.Path == "/code" {
		auth := r.Header.Get("Authorization")
		token := ""
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		} else {
			token = r.URL.Query().Get("token")
		}
		if token == "" || !verifyToken(token) {
			httpErr(w, 401, "Not authenticated")
			return
		}
	}
	handleCodeServer(w, r)
}

func handleMitmproxy(w http.ResponseWriter, r *http.Request) {
	r.URL.Path = r.URL.Path[len("/mitm"):]
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	r.Header.Del("Authorization")
	mitmproxyProxy.ServeHTTP(w, r)
}

func handleMitmproxyAuth(w http.ResponseWriter, r *http.Request) {
	// Only verify token for root path
	if r.URL.Path == "/mitm/" || r.URL.Path == "/mitm" {
		auth := r.Header.Get("Authorization")
		token := ""
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		} else {
			token = r.URL.Query().Get("token")
		}
		if token == "" || !verifyToken(token) {
			httpErr(w, 401, "Not authenticated")
			return
		}
	}
	handleMitmproxy(w, r)
}

// handleXuiProxy 代理请求到 pane 所属节点的 xui
// /api/xui/{pane_id}/... → xui node /api/...
func handleXuiProxy(w http.ResponseWriter, r *http.Request) {
	// /api/xui/{pane_id}/rest/of/path
	path := strings.TrimPrefix(r.URL.Path, "/api/xui/")
	slash := strings.Index(path, "/")
	if slash < 0 {
		httpErr(w, 400, "missing path: /api/xui/{pane_id}/...")
		return
	}
	paneID := normPaneID(path[:slash])
	subPath := path[slash:] // e.g. /api/run_shell

	target, _ := url.Parse(nodeURL(paneID))
	proxy := httputil.NewSingleHostReverseProxy(target)
	r.URL.Path = subPath
	r.Host = target.Host
	r.Header.Del("Authorization")
	proxy.ServeHTTP(w, r)
}
