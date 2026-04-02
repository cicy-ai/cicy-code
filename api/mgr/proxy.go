package main

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var codeServerProxy *httputil.ReverseProxy
var mitmproxyProxy *httputil.ReverseProxy
var pmaProxy *httputil.ReverseProxy
var openClawProxy *httputil.ReverseProxy
var codeServerInjectContent []byte
var codeServerInjectMtime int64
var openClawStartMu sync.Mutex

const openClawGlobalTokenKey = "openclaw_token"

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

	openClawTarget, _ := url.Parse(openClawTargetURL())
	openClawProxy = httputil.NewSingleHostReverseProxy(openClawTarget)
	baseDirector := openClawProxy.Director
	openClawProxy.Director = func(r *http.Request) {
		baseDirector(r)
		r.Host = openClawTarget.Host
		if r.Header.Get("Origin") != "" {
			r.Header.Set("Origin", openClawTarget.Scheme+"://"+openClawTarget.Host)
		}
	}
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
		wsProxyWithHeaders(w, r, "127.0.0.1:"+csPort, map[string]string{
			"Origin": "http://127.0.0.1:" + csPort,
		})
		return
	}

	codeServerProxy.ServeHTTP(w, r)
}

func wsProxy(w http.ResponseWriter, r *http.Request, target string) {
	wsProxyWithHeaders(w, r, target, nil)
}

func wsProxyWithHeaders(w http.ResponseWriter, r *http.Request, target string, headers map[string]string) {
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
	req := r.Clone(r.Context())
	req.RequestURI = ""
	req.Host = target
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}
	// 把原始请求转发给后端
	_ = req.Write(backend)
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

func openClawPort() string {
	port := os.Getenv("OPENCLAW_PORT")
	if port == "" {
		port = "18789"
	}
	return port
}

func openClawTargetURL() string {
	return "http://127.0.0.1:" + openClawPort()
}

func openClawOrigin() string {
	return "http://127.0.0.1:" + openClawPort()
}

func openClawGatewayReady() bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+openClawPort(), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func waitOpenClawGatewayReady(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if openClawGatewayReady() {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func ensureOpenClawGatewayReady() bool {
	if openClawGatewayReady() {
		return true
	}
	openClawStartMu.Lock()
	defer openClawStartMu.Unlock()
	if openClawGatewayReady() {
		return true
	}
	cmd := exec.Command("bash", "-lc", "if ! pidof openclaw-gateway >/dev/null 2>&1 && ! pgrep -A -f 'openclaw gateway run' >/dev/null 2>&1; then nohup openclaw gateway run >/tmp/openclaw-gateway-proxy.log 2>&1 </dev/null & fi")
	if err := cmd.Run(); err != nil {
		log.Printf("[openclaw] failed to start gateway: %v", err)
		return false
	}
	return waitOpenClawGatewayReady(12 * time.Second)
}

func extractBearerOrQueryToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

func globalJSONPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "global.json")
}

func loadGlobalJSONMap() map[string]interface{} {
	gpath := globalJSONPath()
	if gpath == "" {
		return map[string]interface{}{}
	}
	data, err := os.ReadFile(gpath)
	if err != nil {
		return map[string]interface{}{}
	}
	cfg := map[string]interface{}{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return map[string]interface{}{}
	}
	return cfg
}

func saveGlobalJSONMap(cfg map[string]interface{}) {
	gpath := globalJSONPath()
	if gpath == "" {
		return
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(gpath, data, 0600)
}

func readOpenClawTokenFromGlobalJSON() string {
	cfg := loadGlobalJSONMap()
	if token, _ := cfg[openClawGlobalTokenKey].(string); token != "" {
		return strings.TrimSpace(token)
	}
	return ""
}

func writeOpenClawTokenToGlobalJSON(token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	cfg := loadGlobalJSONMap()
	if current, _ := cfg[openClawGlobalTokenKey].(string); strings.TrimSpace(current) == token {
		return
	}
	cfg[openClawGlobalTokenKey] = token
	saveGlobalJSONMap(cfg)
}

func readOpenClawTokenFromConfig() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	configPath := strings.TrimSpace(os.Getenv("OPENCLAW_CONFIG_PATH"))
	if configPath == "" {
		configPath = filepath.Join(home, ".openclaw", "openclaw.json")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	gateway, _ := cfg["gateway"].(map[string]interface{})
	auth := gateway["auth"]
	switch v := auth.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]interface{}:
		if token, _ := v["token"].(string); token != "" {
			return strings.TrimSpace(token)
		}
	}
	return ""
}

func readOpenClawTokenFromDashboard() string {
	cmd := exec.Command("bash", "-lc", "timeout 8s openclaw dashboard --no-open 2>&1 || true")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	m := regexp.MustCompile(`#token=([^[:space:]]+)`).FindStringSubmatch(string(out))
	if len(m) < 2 {
		return ""
	}
	token, err := url.QueryUnescape(strings.TrimSpace(m[1]))
	if err != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(token)
}

func resolveOpenClawGatewayToken() string {
	if token := readOpenClawTokenFromConfig(); token != "" {
		writeOpenClawTokenToGlobalJSON(token)
		return token
	}
	if token := readOpenClawTokenFromGlobalJSON(); token != "" {
		return token
	}
	if token := readOpenClawTokenFromDashboard(); token != "" {
		writeOpenClawTokenToGlobalJSON(token)
		return token
	}
	if token := strings.TrimSpace(os.Getenv("OPENCLAW_GATEWAY_TOKEN")); token != "" {
		writeOpenClawTokenToGlobalJSON(token)
		return token
	}
	if token := readOpenClawTokenFromConfig(); token != "" {
		writeOpenClawTokenToGlobalJSON(token)
		return token
	}
	return ""
}

func redirectToOpenClawDashboard(w http.ResponseWriter, r *http.Request) {
	target := "/openclaw/"
	if token := resolveOpenClawGatewayToken(); token != "" {
		target += "#token=" + url.QueryEscape(token)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func handleOpenClaw(w http.ResponseWriter, r *http.Request) {
	if !ensureOpenClawGatewayReady() {
		httpErr(w, 503, "openclaw_gateway_not_ready")
		return
	}
	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/openclaw")
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	r.Header.Del("Authorization")

	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		wsProxyWithHeaders(w, r, "127.0.0.1:"+openClawPort(), map[string]string{
			"Origin": openClawOrigin(),
		})
		return
	}

	openClawProxy.ServeHTTP(w, r)
}

func handleOpenClawAuth(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		handleOpenClaw(w, r)
		return
	}
	if r.URL.Path == "/openclaw" {
		token := extractBearerOrQueryToken(r)
		if token == "" || !verifyToken(token) {
			httpErr(w, 401, "Not authenticated")
			return
		}
		redirectToOpenClawDashboard(w, r)
		return
	}
	if r.URL.Path == "/openclaw/" {
		if token := extractBearerOrQueryToken(r); token != "" {
			if !verifyToken(token) {
				httpErr(w, 401, "Not authenticated")
				return
			}
			redirectToOpenClawDashboard(w, r)
			return
		}
	}
	handleOpenClaw(w, r)
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
