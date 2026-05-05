package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const gottyInput = '0' // gotty protocol: client→server input message type

type wsAPIRequest struct {
	ID          string            `json:"id"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Body        json.RawMessage   `json:"body"`
	Headers     map[string]string `json:"headers"`
	BodyBase64  string            `json:"bodyBase64"`
	ContentType string            `json:"contentType"`
}

type wsAPIResponse struct {
	ID     string      `json:"id"`
	Ok     bool        `json:"ok"`
	Status int         `json:"status"`
	Body   interface{} `json:"body,omitempty"`
	Error  string      `json:"error,omitempty"`
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
		if subPath == "/" {
			token := r.URL.Query().Get("token")
			readyInst, err := ensureInstanceReady(paneID, token, 5*time.Second)
			if err != nil {
				httpErr(w, 500, "failed to start ttyd: "+err.Error())
				return
			}
			inst = readyInst
		} else {
			inst = waitForInstanceReady(paneID, 5*time.Second)
			if inst == nil {
				httpErr(w, 503, "instance starting")
				return
			}
		}
	} else {
		readyInst := waitForInstanceReady(paneID, 5*time.Second)
		if readyInst == nil {
			httpErr(w, 503, "instance unavailable")
			return
		}
		inst = readyInst
	}
	if inst == nil {
		httpErr(w, 503, "instance unavailable")
		return
	}

	// WebSocket upgrade
	if subPath == "/ws" {
		proxyWS(w, r, paneID, inst.Port)
		return
	}

	// HTTP reverse proxy
	targetURL := fmt.Sprintf("http://127.0.0.1:%d%s", inst.Port, subPath)
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		targetURL += "?" + rawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, nil)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		httpErr(w, 502, err.Error())
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func proxyWS(w http.ResponseWriter, r *http.Request, paneID string, port int) {
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
	var clientWriteMu sync.Mutex
	writeClient := func(mt int, data []byte) error {
		clientWriteMu.Lock()
		defer clientWriteMu.Unlock()
		return clientConn.WriteMessage(mt, data)
	}

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
			if err := writeClient(mt, msg); err != nil {
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
				if len(msg) > 1 && msg[0] == '6' {
					if err := handleWSAPIRequest(writeClient, paneID, msg[1:]); err != nil {
						log.Printf("[ws-proxy] ws api error: %v", err)
					}
					continue
				}
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

func handleWSAPIRequest(writeClient func(int, []byte) error, paneID string, payload []byte) error {
	var req wsAPIRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return writeWSAPIResponse(writeClient, wsAPIResponse{
			Ok:     false,
			Status: 400,
			Error:  "invalid ws api request: " + err.Error(),
		})
	}

	if req.ID == "" {
		req.ID = fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	if req.Method == "" || req.Path == "" {
		return writeWSAPIResponse(writeClient, wsAPIResponse{
			ID:     req.ID,
			Ok:     false,
			Status: 400,
			Error:  "ws api request requires method and path",
		})
	}

	bodyBytes := []byte{}
	if req.BodyBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.BodyBase64)
		if err != nil {
			return writeWSAPIResponse(writeClient, wsAPIResponse{
				ID:     req.ID,
				Ok:     false,
				Status: 400,
				Error:  "invalid base64 body: " + err.Error(),
			})
		}
		bodyBytes = decoded
	} else if len(req.Body) > 0 {
		bodyBytes = req.Body
	}

	requestURL := req.Path
	if !strings.HasPrefix(requestURL, "/") {
		requestURL = "/" + requestURL
	}
	httpReq, err := http.NewRequest(req.Method, "http://ws.local"+requestURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return writeWSAPIResponse(writeClient, wsAPIResponse{
			ID:     req.ID,
			Ok:     false,
			Status: 400,
			Error:  "failed to create request: " + err.Error(),
		})
	}
	httpReq.RemoteAddr = "ttyd-ws:" + paneID
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}
	if req.ContentType != "" && httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", req.ContentType)
	}
	if len(req.Body) > 0 && req.ContentType == "" && httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	recorder := httptest.NewRecorder()
	http.DefaultServeMux.ServeHTTP(recorder, httpReq)

	resp := wsAPIResponse{
		ID:     req.ID,
		Ok:     recorder.Code < 400,
		Status: recorder.Code,
	}
	respBytes := recorder.Body.Bytes()
	if len(respBytes) > 0 {
		contentType := recorder.Header().Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			var decoded interface{}
			if err := json.Unmarshal(respBytes, &decoded); err == nil {
				resp.Body = decoded
			} else {
				resp.Body = string(respBytes)
			}
		} else {
			resp.Body = string(respBytes)
		}
	}
	if !resp.Ok && resp.Error == "" {
		if body, ok := resp.Body.(string); ok && body != "" {
			resp.Error = body
		} else {
			resp.Error = fmt.Sprintf("request failed with status %d", resp.Status)
		}
	}

	return writeWSAPIResponse(writeClient, resp)
}

func writeWSAPIResponse(writeClient func(int, []byte) error, resp wsAPIResponse) error {
	payload, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return writeClient(websocket.TextMessage, append([]byte{'6'}, payload...))
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
