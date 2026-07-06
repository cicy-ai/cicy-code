// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	_ "embed"
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

var mitmproxyProxy *httputil.ReverseProxy
var pmaProxy *httputil.ReverseProxy
var openClawProxy *httputil.ReverseProxy
var openClawStartMu sync.Mutex

func init() {
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
