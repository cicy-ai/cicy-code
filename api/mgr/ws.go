package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const gottyInput = '0' // gotty protocol: client→server input message type

//go:embed resources/ttyd-inject-00-base.html
var embedInject00 string

//go:embed resources/ttyd-inject-01-panel.html
var embedInject01 string

//go:embed resources/ttyd-inject-02-voice.html
var embedInject02 string

// Embedded inject HTML (concatenated at compile time)
var embedInjectAll string

func init() {
	embedInjectAll = embedInject00 + "\n" + embedInject01 + "\n" + embedInject02
}

// ttyd COS version: only used in dev mode
var ttydCosVer = "v1"

func init() {
	data, err := os.ReadFile("../versions.json")
	if err == nil {
		var m map[string]string
		if json.Unmarshal(data, &m) == nil && m["ttyd"] != "" {
			ttydCosVer = "v" + m["ttyd"]
		}
	}
}

// ttyd HTML inject: dev mode reads from filesystem (hot-reload), release mode uses embedded
var (
	ttydInject    string
	ttydInjectMu  sync.RWMutex
	ttydInjectDir = "api/resources"
	ttydInjectMod time.Time
)

func loadTtydInject() string {
	// Release mode: return embedded content directly
	if !devMode {
		return embedInjectAll
	}

	// Dev mode: read from filesystem with hot-reload
	entries, err := os.ReadDir(ttydInjectDir)
	if err != nil {
		return embedInjectAll // fallback to embedded
	}
	// collect matching files and latest mod time
	var files []string
	var latest time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ttyd-inject") || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		fp := ttydInjectDir + "/" + e.Name()
		info, err := os.Stat(fp)
		if err != nil {
			continue
		}
		files = append(files, fp)
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	ttydInjectMu.RLock()
	cached := ttydInject
	mod := ttydInjectMod
	ttydInjectMu.RUnlock()
	if cached != "" && !latest.After(mod) {
		return cached
	}
	sort.Strings(files)
	var buf strings.Builder
	for _, fp := range files {
		data, err := os.ReadFile(fp)
		if err != nil {
			continue
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	s := buf.String()
	ttydInjectMu.Lock()
	ttydInject = s
	ttydInjectMod = latest
	ttydInjectMu.Unlock()
	log.Printf("[ttyd] reloaded inject: %d files, %d bytes", len(files), len(s))
	return s
}

func handleWSProxy(w http.ResponseWriter, r *http.Request) {
	paneID := strings.TrimPrefix(r.URL.Path, "/api/ws/")
	inst := getInstance(paneID)
	if inst == nil {
		httpErr(w, 404, "pane not found")
		return
	}

	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer clientConn.Close()

	ttydConn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://localhost:%d/ws", inst.Port), nil)
	if err != nil {
		return
	}
	defer ttydConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		for {
			mt, msg, err := ttydConn.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			if err := clientConn.WriteMessage(mt, msg); err != nil {
				cancel()
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			mt, msg, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if mt == websocket.TextMessage {
				msg = filterDAQuery(msg)
				if msg == nil {
					continue
				}
			}
			if err := ttydConn.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}
}

// handleTtydProxy proxies /ttyd/{pane_id}/* to the embedded ttyd-go instance
func handleTtydProxy(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/ttyd/")
	parts := strings.SplitN(path, "/", 2)
	paneID := normPaneID(parts[0])
	subPath := "/"
	if len(parts) > 1 {
		subPath = "/" + parts[1]
	}

	// Token required only for root page; assets and WS skip auth (WS only reachable after page load)
	if subPath == "/" {
		token := r.URL.Query().Get("token")
		if token == "" {
			httpErr(w, 401, "token required")
			return
		}
		if !verifyToken(token) {
			httpErr(w, 401, "invalid token")
			return
		}
	}

	// Check pane exists in DB and is active
	var dbPort int
	if err := store.QueryRow("SELECT ttyd_port FROM agent_config WHERE pane_id=? AND active=1", paneID).Scan(&dbPort); err != nil {
		httpErr(w, 404, "pane not found or inactive")
		return
	}

	inst := getInstance(paneID)
	if inst == nil {
		if subPath != "/" && subPath != "/ws" {
			httpErr(w, 404, "instance not started")
			return
		}
		port := portPool.Allocate()
		if port == 0 {
			httpErr(w, 500, "no available ports")
			return
		}
		token := r.URL.Query().Get("token")
		if err := startInstance(paneID, port, token); err != nil {
			portPool.Release(port)
			httpErr(w, 500, "failed to start ttyd: "+err.Error())
			return
		}
		if !waitPort(port, 5*time.Second) {
			httpErr(w, 500, "ttyd start timeout")
			return
		}
		inst = getInstance(paneID)
	}

	// WebSocket upgrade
	if subPath == "/ws" {
		proxyWS(w, r, inst.Port)
		return
	}

	// HTTP reverse proxy
	targetURL := fmt.Sprintf("http://127.0.0.1:%d%s", inst.Port, subPath)
	resp, err := http.Get(targetURL)
	if err != nil {
		httpErr(w, 502, err.Error())
		return
	}
	defer resp.Body.Close()

	// Inject CSS + JS for root HTML page
	if subPath == "/" && strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		body, _ := io.ReadAll(resp.Body)
		html := string(body)
		// WS patch must run in <head> before gotty-bundle.js creates WebSocket
		wsPatch := `<script>var __cicyWs=null,__origWsSend=WebSocket.prototype.send;WebSocket.prototype.send=function(d){if(typeof d==='string'&&d.length>0&&'01234'.indexOf(d[0])>=0)__cicyWs=this;return __origWsSend.call(this,d)};</script>`
		html = strings.Replace(html, "</head>", wsPatch+"</head>", 1)
		// UI + styles inject in </body> so DOM is ready
		if inj := loadTtydInject(); inj != "" {
			html = strings.Replace(html, "</body>", inj+"</body>", 1)
		}
		html = strings.Replace(html, "<html>", `<html style="overflow:hidden">`, 1)
		// Dev mode: replace local asset paths with COS CDN
		// Release mode: keep local paths (assets served from embedded binary)
		if devMode {
			cosBase := "https://cicy-1372193042.cos.ap-shanghai.myqcloud.com/ttyd/" + ttydCosVer
			html = strings.Replace(html, `"./js/gotty-bundle.js"`, fmt.Sprintf(`"%s/gotty-bundle.js"`, cosBase), 1)
			html = strings.Replace(html, `"./css/index.css"`, fmt.Sprintf(`"%s/css/index.css"`, cosBase), 1)
			html = strings.Replace(html, `"./css/xterm.css"`, fmt.Sprintf(`"%s/css/xterm.css"`, cosBase), 1)
			html = strings.Replace(html, `"./css/xterm_customize.css"`, fmt.Sprintf(`"%s/css/xterm_customize.css"`, cosBase), 1)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(html)))
		w.WriteHeader(resp.StatusCode)
		w.Write([]byte(html))
		return
	}

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func proxyWS(w http.ResponseWriter, r *http.Request, port int) {
	// Must pass through subprotocol ("webtty") from client to ttyd-go
	respHeader := http.Header{}
	if protos := websocket.Subprotocols(r); len(protos) > 0 {
		respHeader.Set("Sec-WebSocket-Protocol", protos[0])
	}
	clientConn, err := upgrader.Upgrade(w, r, respHeader)
	if err != nil {
		log.Printf("[ws-proxy] upgrade error: %v", err)
		return
	}
	defer clientConn.Close()

	ttydURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	dialer := websocket.Dialer{Subprotocols: websocket.Subprotocols(r)}
	ttydConn, _, err := dialer.Dial(ttydURL, nil)
	if err != nil {
		log.Printf("[ws-proxy] dial ttyd error: %v", err)
		return
	}
	defer ttydConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		for {
			mt, msg, err := ttydConn.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			if mt == websocket.TextMessage && bytes.Contains(msg, []byte("0;276;0c")) {
				log.Printf("[ws-proxy] DA response ttyd→client: %q", msg[:minInt(len(msg), 120)])
			}
			if err := clientConn.WriteMessage(mt, msg); err != nil {
				cancel()
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			mt, msg, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if mt == websocket.TextMessage {
				msg = filterDAQuery(msg)
				if msg == nil {
					continue
				}
				// Log mouse sequences
				if len(msg) > 1 && msg[0] == '0' {
					payload := string(msg[1:])
					if strings.Contains(payload, "\x1b[<") || strings.Contains(payload, "\x1b[M") {
						log.Printf("[ws-proxy] mouse: %q", payload)
					}
				}
			}
			if err := ttydConn.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}
}

// filterDAQuery removes DA queries and click/drag mouse sequences from gotty Input messages.
// Preserves scroll wheel events (SGR button 64-67) for terminal scrolling.
var mouseClickRe = regexp.MustCompile(`\x1b\[<(?:0|1|2|3|32|33|34|35);\d+;\d+[Mm]|\x1b\[M[\s\S]{3}`)

func filterDAQuery(data []byte) []byte {
	if len(data) < 2 || data[0] != gottyInput {
		return data
	}
	raw := data[1:]
	// Log DA queries before filtering
	if bytes.Contains(raw, []byte("\x1b[c")) || bytes.Contains(raw, []byte("\x1b[0c")) || bytes.Contains(raw, []byte("0;276;0c")) {
		log.Printf("[ws-filter] DA detected in input: %q", raw)
	}
	// Remove DA queries
	cleaned := bytes.ReplaceAll(raw, []byte("\x1b[c"), nil)
	cleaned = bytes.ReplaceAll(cleaned, []byte("\x1b[0c"), nil)
	// Remove click/drag mouse sequences, keep scroll (button 64-67)
	cleaned = mouseClickRe.ReplaceAll(cleaned, nil)
	if len(cleaned) == 0 {
		return nil
	}
	return append([]byte{gottyInput}, cleaned...)
}
