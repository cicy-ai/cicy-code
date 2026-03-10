package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const gottyInput = '0' // gotty protocol: client→server input message type

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

	// Check pane exists in DB
	var dbPort int
	if err := db.QueryRow("SELECT ttyd_port FROM ttyd_config WHERE pane_id=?", paneID).Scan(&dbPort); err != nil {
		httpErr(w, 404, "pane not found")
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
		inject := `<style>html,body,#terminal{background:#000;height:100%;width:100%;padding:4px;margin:0;box-sizing:border-box;font-size:12px;}.xterm-viewport{overflow:hidden!important;}</style></head>`
		html = strings.Replace(html, "</head>", inject, 1)
		// Use external gotty-bundle.js served by ttyd-manager
		html = strings.Replace(html, `"./js/gotty-bundle.js"`, fmt.Sprintf(`"/static/gotty-bundle.js?v=%d"`, time.Now().Unix()), 1)
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

// filterDAQuery removes DA queries and mouse sequences from gotty Input messages.
var mouseRe = regexp.MustCompile(`\x1b\[<[\d;]*[Mm]|\x1b\[M[\s\S]{3}`)

func filterDAQuery(data []byte) []byte {
	if len(data) < 2 || data[0] != gottyInput {
		return data
	}
	raw := data[1:]
	// Remove DA queries
	cleaned := bytes.ReplaceAll(raw, []byte("\x1b[c"), nil)
	cleaned = bytes.ReplaceAll(cleaned, []byte("\x1b[0c"), nil)
	// Remove mouse sequences (SGR + X10)
	cleaned = mouseRe.ReplaceAll(cleaned, nil)
	if len(cleaned) == 0 {
		return nil
	}
	return append([]byte{gottyInput}, cleaned...)
}
