package main

import (
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
)

var codeServerProxy *httputil.ReverseProxy
var mitmproxyProxy *httputil.ReverseProxy
var codeServerInjectContent []byte
var codeServerInjectMtime int64

func init() {
	target, _ := url.Parse("http://127.0.0.1:18080")
	codeServerProxy = httputil.NewSingleHostReverseProxy(target)
	codeServerProxy.ModifyResponse = injectCodeServerJS
	
	mitmTarget, _ := url.Parse("http://127.0.0.1:18889")
	mitmproxyProxy = httputil.NewSingleHostReverseProxy(mitmTarget)
}

func loadCodeServerInject() []byte {
	path := "resources/code-server-inject.html"
	info, err := os.Stat(path)
	if err != nil {
		return codeServerInjectContent
	}
	if info.ModTime().Unix() != codeServerInjectMtime {
		data, err := os.ReadFile(path)
		if err == nil {
			codeServerInjectContent = data
			codeServerInjectMtime = info.ModTime().Unix()
		}
	}
	return codeServerInjectContent
}

func injectCodeServerJS(resp *http.Response) error {
	ct := resp.Header.Get("Content-Type")
	log.Printf("[INJECT] URL=%s Status=%d ContentType=%s", resp.Request.URL.Path, resp.StatusCode, ct)
	if !strings.Contains(ct, "text/html") && !strings.Contains(ct, "text/plain") {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	log.Printf("[INJECT] Body length=%d, first 100 chars: %s", len(body), string(body[:min(100, len(body))]))
	inject := string(loadCodeServerInject())
	html := strings.Replace(string(body), "<body", inject+"<body", 1)
	if html == string(body) {
		log.Printf("[INJECT] No <body found, trying </head>")
		html = strings.Replace(string(body), "</head>", inject+"</head>", 1)
	}
	resp.Body = io.NopCloser(strings.NewReader(html))
	resp.ContentLength = int64(len(html))
	resp.Header.Set("Content-Length", strconv.Itoa(len(html)))
	log.Printf("[INJECT] Injected, new length=%d", len(html))
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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
