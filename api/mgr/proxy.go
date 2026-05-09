package main

import (
	_ "embed"
	"bytes"
	"encoding/json"
	"fmt"
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
var openClawStartMu sync.Mutex
var codeServerPageContextMu sync.RWMutex
var codeServerPageContexts = map[string]M{}

//go:embed resources/code-server-inject.html
var codeServerInjectHTML []byte

//go:embed resources/code-server-inject.js
var codeServerInjectJS []byte

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
		if strings.Contains(r.URL.Host, "api.deepseek.com") && r.Body != nil {
			var bodyMap map[string]interface{}
			body, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if err := json.Unmarshal(body, &bodyMap); err == nil {
				if model, ok := bodyMap["model"].(string); ok && model != "" {
					bodyMap["model"] = "deepseek-v4-pro"
				}
				modified, _ := json.Marshal(bodyMap)
				r.Body = io.NopCloser(bytes.NewReader(modified))
				r.ContentLength = int64(len(modified))
			}
			if strings.HasSuffix(r.URL.Path, "/responses") {
				r.URL.Path = strings.TrimSuffix(r.URL.Path, "/responses") + "/chat/completions"
			}
		}
	}
}

func loadCodeServerInject() []byte {
	return codeServerInjectHTML
}

func loadCodeServerInjectJS() ([]byte, error) {
	return codeServerInjectJS, nil
}

func codeServerPort() string {
	csPort := strings.TrimSpace(os.Getenv("CS_PORT"))
	if csPort == "" {
		return "8002"
	}
	return csPort
}

func waitForCodeServerReady() bool {
	port := mustAtoi(codeServerPort())
	if isPortListening(port) {
		return true
	}
	return waitPort(port, 20*time.Second)
}

func serveCodeServerInjectJS(w http.ResponseWriter, r *http.Request) {
	data, err := loadCodeServerInjectJS()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func handleCodeServerPageContext(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Folder       string `json:"folder"`
			PageClientID string `json:"page_client_id"`
			PagePane     string `json:"page_pane"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		folder := strings.TrimSpace(body.Folder)
		pageClientID := strings.TrimSpace(body.PageClientID)
		pagePane := strings.TrimSpace(body.PagePane)
		if folder == "" || pageClientID == "" || pagePane == "" {
			http.Error(w, "folder, page_client_id and page_pane required", 400)
			return
		}
		codeServerPageContextMu.Lock()
		codeServerPageContexts[folder] = M{
			"folder":         folder,
			"page_client_id": pageClientID,
			"page_pane":      pagePane,
			"token":          strings.TrimSpace(r.Header.Get("Authorization")),
		}
		codeServerPageContextMu.Unlock()
		J(w, M{"success": true})
	case http.MethodGet:
		folder := strings.TrimSpace(r.URL.Query().Get("folder"))
		if folder == "" {
			http.Error(w, "folder required", 400)
			return
		}
		codeServerPageContextMu.RLock()
		ctx, ok := codeServerPageContexts[folder]
		codeServerPageContextMu.RUnlock()
		if !ok {
			http.Error(w, "not found", 404)
			return
		}
		J(w, M{"success": true, "data": ctx})
	default:
		http.Error(w, "method not allowed", 405)
	}
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
	if folder := strings.TrimSpace(r.URL.Query().Get("folder")); folder != "" {
		pageClientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
		pagePane := strings.TrimSpace(r.URL.Query().Get("page_pane"))
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		if pageClientID != "" && pagePane != "" {
			codeServerPageContextMu.Lock()
			codeServerPageContexts[folder] = M{
				"folder":         folder,
				"page_client_id": pageClientID,
				"page_pane":      pagePane,
				"token":          token,
			}
			codeServerPageContextMu.Unlock()
		}
	}
	r.URL.Path = r.URL.Path[len("/code"):]
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	r.Header.Del("Authorization")

	if !waitForCodeServerReady() {
		http.Error(w, "code-server not ready", http.StatusBadGateway)
		return
	}

	// WebSocket: hijack 双向代理
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		csPort := codeServerPort()
		wsProxyWithHeaders(w, r, "127.0.0.1:"+csPort, map[string]string{
			"Origin": "http://127.0.0.1:" + csPort,
		})
		return
	}

	codeServerProxy.ServeHTTP(w, r)
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func handleCodeServerSendPath(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Folder        string `json:"folder"`
		Path          string `json:"path"`
		FileName      string `json:"fileName"`
		SelectionText string `json:"selectionText"`
		Range         any    `json:"range"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	folder := strings.TrimSpace(body.Folder)
	path := strings.TrimSpace(body.Path)
	if folder == "" || path == "" {
		http.Error(w, "folder and path required", 400)
		return
	}
	codeServerPageContextMu.RLock()
	ctx, ok := codeServerPageContexts[folder]
	codeServerPageContextMu.RUnlock()
	if !ok {
		http.Error(w, "page context not found", 404)
		return
	}
	pageClientID, _ := ctx["page_client_id"].(string)
	pagePane, _ := ctx["page_pane"].(string)
	if pageClientID == "" || pagePane == "" {
		http.Error(w, "page context incomplete", 404)
		return
	}
	pathForAgent := path
	if rangeMap, ok := body.Range.(map[string]any); ok {
		startLine := intFromAny(rangeMap["startLine"])
		startCharacter := intFromAny(rangeMap["startCharacter"])
		endLine := intFromAny(rangeMap["endLine"])
		endCharacter := intFromAny(rangeMap["endCharacter"])
		if startLine > 0 && startCharacter > 0 && endLine > 0 && endCharacter > 0 {
			pathForAgent = fmt.Sprintf("%s:%d:%d-%d:%d", path, startLine, startCharacter, endLine, endCharacter)
		}
	}
	ok = hub.sendToClient(pageClientID, ChatEvent{Type: "code.send_path", Data: M{
		"path":           pathForAgent,
		"fileName":       strings.TrimSpace(body.FileName),
		"selectionText":  "",
		"range":          nil,
		"page_client_id": pageClientID,
		"page_pane":      pagePane,
	}})
	if !ok {
		http.Error(w, "workspace client not found", 404)
		return
	}
	J(w, M{"success": true, "page_client_id": pageClientID, "page_pane": pagePane, "path": pathForAgent})
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

// Backward-compatible no-op shim. OpenClaw token resolution no longer reads global.json.
func readOpenClawTokenFromGlobalJSON() string {
	return ""
}

func resolveOpenClawGatewayToken() string {
	if token := strings.TrimSpace(os.Getenv("OPENCLAW_GATEWAY_TOKEN")); token != "" {
		return token
	}
	if token := readOpenClawTokenFromConfig(); token != "" {
		return token
	}
	if token := readOpenClawTokenFromDashboard(); token != "" {
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
