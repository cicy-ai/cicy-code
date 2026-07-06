// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
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

// handleTtydProxy serves /ttyd/{pane_id}/* directly: index.html, the shared
// static bundle, the auth/config shims, and the webtty WebSocket — all in
// process, with no per-pane port or reverse proxy. See ttyd_inline.go.
func handleTtydProxy(w http.ResponseWriter, r *http.Request) {
	// Served under BOTH /ttyd/ (used internally by the SPA iframe) and the
	// friendlier public alias /agent/ (for direct external links to a team
	// member's terminal). Strip whichever prefix this request came in on — the
	// ttyd page loads its assets/WS relative to that same prefix, so both must
	// terminate here.
	path := r.URL.Path
	if strings.HasPrefix(path, "/agent/") {
		path = strings.TrimPrefix(path, "/agent/")
	} else {
		path = strings.TrimPrefix(path, "/ttyd/")
	}
	parts := strings.SplitN(path, "/", 2)
	paneID := normPaneID(parts[0])
	subPath := "/"
	if len(parts) > 1 {
		subPath = "/" + parts[1]
	}

	// Token required only for the root page; assets + WS follow after load.
	if subPath == "/" {
		token := r.URL.Query().Get("token")
		if token == "" || !verifyToken(token) {
			httpErr(w, 401, "token required")
			return
		}
	}

	// Pane must exist and be active — the row's presence is all we check.
	var one int
	if err := store.QueryRow("SELECT 1 FROM agent_config WHERE pane_id=? AND active=1", paneID).Scan(&one); err != nil {
		httpErr(w, 404, "pane not found or inactive")
		return
	}

	serveTtydHTTP(w, r, paneID, subPath, shortPaneID(paneID), paneID)
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
