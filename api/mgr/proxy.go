package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
)

var codeServerProxy *httputil.ReverseProxy
var mitmproxyProxy *httputil.ReverseProxy
var pmaProxy *httputil.ReverseProxy
var codeServerInjectContent []byte
var codeServerInjectMtime int64

func init() {
	csPort := os.Getenv("CS_PORT")
	if csPort == "" {
		csPort = "8002"
	}
	target, _ := url.Parse("http://127.0.0.1:" + csPort)
	codeServerProxy = httputil.NewSingleHostReverseProxy(target)
	codeServerProxy.ModifyResponse = injectCodeServerJS
	
	mitmTarget, _ := url.Parse("http://127.0.0.1:18889")
	mitmproxyProxy = httputil.NewSingleHostReverseProxy(mitmTarget)

	pmaTarget, _ := url.Parse("http://127.0.0.1:8899")
	pmaProxy = httputil.NewSingleHostReverseProxy(pmaTarget)
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
	if !strings.Contains(ct, "text/html") && !strings.Contains(ct, "text/plain") {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	inject := string(loadCodeServerInject())
	html := strings.Replace(string(body), "<body", inject+"<body", 1)
	if html == string(body) {
		html = strings.Replace(string(body), "</head>", inject+"</head>", 1)
	}
	resp.Body = io.NopCloser(strings.NewReader(html))
	resp.ContentLength = int64(len(html))
	resp.Header.Set("Content-Length", strconv.Itoa(len(html)))
	return nil
}

func handleCodeServer(w http.ResponseWriter, r *http.Request) {
	r.URL.Path = r.URL.Path[len("/code"):]
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	r.Header.Del("Authorization")

	// WebSocket: hijack 双向代理
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		csPort := os.Getenv("CS_PORT")
		if csPort == "" {
			csPort = "8002"
		}
		wsProxy(w, r, "127.0.0.1:"+csPort)
		return
	}

	codeServerProxy.ServeHTTP(w, r)
}

func wsProxy(w http.ResponseWriter, r *http.Request, target string) {
	backend, err := net.Dial("tcp", target)
	if err != nil {
		http.Error(w, "backend unreachable", 502)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		backend.Close()
		http.Error(w, "hijack not supported", 500)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		backend.Close()
		return
	}
	// 把原始请求转发给后端
	_ = r.Write(backend)
	// 双向拷贝
	go func() { io.Copy(backend, client); backend.Close() }()
	io.Copy(client, backend)
	client.Close()
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

func handlePmaAuth(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	token := ""
	if strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimPrefix(auth, "Bearer ")
	} else {
		token = r.URL.Query().Get("token")
	}
	// fallback to cookie
	if token == "" {
		if c, err := r.Cookie("pma_token"); err == nil {
			token = c.Value
		}
	}
	if token == "" || !verifyToken(token) {
		httpErr(w, 401, "Not authenticated")
		return
	}
	// set cookie so sub-resources work
	http.SetCookie(w, &http.Cookie{Name: "pma_token", Value: token, Path: "/pma/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/pma")
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	r.Header.Del("Authorization")
	pmaProxy.ServeHTTP(w, r)
}
