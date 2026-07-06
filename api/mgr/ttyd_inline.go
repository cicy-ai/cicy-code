// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// ttyd_inline.go terminates /ttyd/<pane>/ and /ttyd-shell/<agent>/ traffic
// directly inside mgr, with no per-pane HTTP server and no TCP port. webtty runs
// in-process, driven through an in-memory message pipe (localTTY), while mgr
// keeps its client-side interception ('6' ws-api channel, filterDAQuery) on the
// real browser WebSocket. This replaces the old instance.go port pool + the
// proxyWS/proxyHTTP reverse-proxy hop.

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"ttyd-go/backend/localcommand"
	"ttyd-go/server"
)

// localTTY is an in-memory, message-oriented duplex. One value is shared by two
// callers via crossed channels:
//   - webtty uses it as its master io.ReadWriter (Read/Write)
//   - mgr's client loop uses it like the subset of *websocket.Conn it needs
//     (ReadMessage/WriteMessage/Close)
//
// client input → WriteMessage → toWebtty → master.Read  → webtty
// webtty output → master.Write → fromWebtty → ReadMessage → client
type localTTY struct {
	toWebtty   chan []byte // client → webtty
	fromWebtty chan []byte // webtty → client
	done       chan struct{}
	closeOnce  sync.Once
}

func newLocalTTY() *localTTY {
	return &localTTY{
		toWebtty:   make(chan []byte, 64),
		fromWebtty: make(chan []byte, 64),
		done:       make(chan struct{}),
	}
}

func (l *localTTY) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return nil
}

// Read delivers one client→webtty message per call, matching the old
// wsWrapper's one-frame-per-Read semantics. A message longer than len(p) has
// its tail discarded, exactly as wsWrapper.Read (fresh NextReader each call)
// did — webtty's buffer is sized well above any single gotty message.
func (l *localTTY) Read(p []byte) (int, error) {
	select {
	case msg := <-l.toWebtty:
		return copy(p, msg), nil
	case <-l.done:
		return 0, io.EOF
	}
}

// Write enqueues one webtty→client message (webtty writes one message per call).
func (l *localTTY) Write(p []byte) (int, error) {
	b := make([]byte, len(p))
	copy(b, p)
	select {
	case l.fromWebtty <- b:
		return len(p), nil
	case <-l.done:
		return 0, io.ErrClosedPipe
	}
}

// WriteMessage feeds a client message toward webtty.
func (l *localTTY) WriteMessage(_ int, msg []byte) error {
	b := make([]byte, len(msg))
	copy(b, msg)
	select {
	case l.toWebtty <- b:
		return nil
	case <-l.done:
		return io.ErrClosedPipe
	}
}

// ReadMessage returns the next webtty→client message as a TextMessage.
func (l *localTTY) ReadMessage() (int, []byte, error) {
	select {
	case msg := <-l.fromWebtty:
		return websocket.TextMessage, msg, nil
	case <-l.done:
		return 0, nil, io.EOF
	}
}

// ttydPreferences mirrors the HtermPrefernces the old per-pane server set.
func ttydPreferences() *server.HtermPrefernces {
	return &server.HtermPrefernces{
		ForegroundColor:         "#c0c0c0",
		FontSize:                10,
		CopyOnSelect:            true,
		CtrlCCopy:               true,
		CtrlVPaste:              true,
		UseDefaultWindowCopy:    true,
		ClearSelectionAfterCopy: false,
	}
}

// ttydActive tracks live viewers per tmux target so handleTtydStatus can report
// "running" without a persistent per-pane object. Value is *int32.
var ttydActive sync.Map

func ttydActiveAdd(target string) {
	v, _ := ttydActive.LoadOrStore(target, new(int32))
	atomic.AddInt32(v.(*int32), 1)
}

func ttydActiveRemove(target string) {
	if v, ok := ttydActive.Load(target); ok {
		atomic.AddInt32(v.(*int32), -1)
	}
}

func ttydActiveCount(target string) int32 {
	if v, ok := ttydActive.Load(target); ok {
		return atomic.LoadInt32(v.(*int32))
	}
	return 0
}

// serveTtydHTTP dispatches one /ttyd(-shell)/ request to the right inline
// handler: the index page, the webtty WebSocket, the auth/config JS shims, or a
// shared static asset. tmuxTarget is the `tmux attach -t` target, title the
// terminal window title, apiPane the pane id for the '6' ws-api channel.
func serveTtydHTTP(w http.ResponseWriter, r *http.Request, tmuxTarget, subPath, title, apiPane string) {
	switch {
	case subPath == "/":
		if err := server.WriteIndex(w, title); err != nil {
			httpErr(w, 500, err.Error())
		}
	case subPath == "/ws":
		serveTTY(w, r, tmuxTarget, title, apiPane)
	case subPath == "/auth_token.js":
		w.Header().Set("Content-Type", "application/javascript")
		w.Write(server.AuthTokenJS(""))
	case subPath == "/config.js":
		w.Header().Set("Content-Type", "application/javascript")
		w.Write(server.TermConfigJS("xterm-256color"))
		// Inject the model-mask token SYNCHRONOUSLY (config.js runs at page load,
		// before the WS opens). The client reads window.cicyModelMask at mount and
		// masks it from the PTY stream from the very first byte — avoiding the race
		// where codex prints its model before an async API lookup could set the
		// mask. Only codex-on-gateway panes get a token (else nothing is injected).
		if m := codexGatewayMaskModel(apiPane); m != "" {
			if b, err := json.Marshal(m); err == nil {
				w.Write([]byte("\nwindow.cicyModelMask=" + string(b) + ";\n"))
			}
		}
	default:
		// Shared static bundle (js/, css/, favicon.png) — identical for every
		// pane. Rewrite the path to the asset-relative form the bundle handler
		// expects (it strips no prefix of its own).
		r2 := r.Clone(r.Context())
		r2.URL.Path = subPath
		server.StaticHandler().ServeHTTP(w, r2)
	}
}

// codexGatewayMaskModel returns the model string codex launches with (its `-m`
// value) for codex-on-gateway panes — the token the terminal client masks out of
// the PTY stream so the leaked model name never renders. "" for any other pane.
func codexGatewayMaskModel(apiPane string) string {
	if store == nil {
		return ""
	}
	shortID := shortPaneID(normPaneID(apiPane))
	var agentType, defaultModel string
	var gw int
	if err := store.QueryRow(
		"SELECT COALESCE(agent_type,''), COALESCE(default_model,''), COALESCE(use_custom_gateway,0) FROM agent_config WHERE pane_id=?",
		shortID+":main.0",
	).Scan(&agentType, &defaultModel, &gw); err != nil {
		return ""
	}
	if normalizeAgentType(agentType) != "codex" || gw == 0 {
		return ""
	}
	return resolveCodexStartupModel(defaultModel, loadRuntimeAIConfig(), shortID)
}

// serveTTY upgrades the client WebSocket and runs an in-process webtty session
// attached to the given tmux target (`tmux attach -t <tmuxTarget>`). title is
// the terminal window title; apiPane is the pane id used for the '6' ws-api
// channel (same as tmuxTarget for agents, the grouped name for shell panels).
func serveTTY(w http.ResponseWriter, r *http.Request, tmuxTarget, title, apiPane string) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[ttyd] PANIC in serveTTY %s: %v", tmuxTarget, rec)
		}
	}()
	clientConn, err := server.UpgradeWebTTY(w, r)
	if err != nil {
		log.Printf("[ttyd] upgrade error: %v", err)
		return
	}
	defer clientConn.Close()

	var clientWriteMu sync.Mutex
	writeClient := func(mt int, data []byte) error {
		clientWriteMu.Lock()
		defer clientWriteMu.Unlock()
		return clientConn.WriteMessage(mt, data)
	}

	// Keepalive on the real client conn: reap dead/half-open peers so a vanished
	// browser doesn't leave the tmux attach as a ghost client wedging the shared
	// window size. (The old per-pane server did this on its own conn; we now own
	// the only conn, so it lives here.) WriteControl is safe concurrent with the
	// writeClient mutex's WriteMessage.
	const (
		pongWait   = 60 * time.Second
		pingPeriod = 25 * time.Second
	)
	clientConn.SetReadDeadline(time.Now().Add(pongWait))
	clientConn.SetPongHandler(func(string) error {
		clientConn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	var factory server.Factory
	lf, err := localcommand.NewFactory(
		"tmux", []string{"attach", "-t", tmuxTarget},
		&localcommand.Options{CloseSignal: 1, CloseTimeout: -1},
	)
	if err != nil {
		log.Printf("[ttyd] factory error: %v", err)
		return
	}
	factory = lf

	l := newLocalTTY()
	defer l.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	ttydActiveAdd(tmuxTarget)
	defer ttydActiveRemove(tmuxTarget)

	// webtty drives the tmux slave over the in-memory master.
	go func() {
		err := server.RunWebTTY(ctx, l, factory, &server.WSConfig{
			Title:         title,
			PermitWrite:   true,
			Reconnect:     true,
			ReconnectTime: 30,
			Preferences:   ttydPreferences(),
		})
		if err != nil && err != context.Canceled {
			log.Printf("[ttyd] webtty %s ended: %v", tmuxTarget, err)
		}
		cancel()
	}()

	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				if err := clientConn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			}
		}
	}()

	// When the session ends backend-side (tmux EOF → webtty returns → cancel),
	// unblock the input loop's clientConn.ReadMessage so serveTTY tears down
	// promptly instead of hanging until the browser happens to disconnect.
	go func() {
		<-ctx.Done()
		clientConn.SetReadDeadline(time.Now())
	}()

	// Output: webtty → client.
	go func() {
		for {
			mt, msg, err := l.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			if err := writeClient(mt, msg); err != nil {
				cancel()
				return
			}
		}
	}()

	// Input: client → webtty, with mgr interception preserved verbatim from the
	// old proxyWS (the '6' ws-api channel + filterDAQuery).
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
				// The gotty init/auth handshake is a bare JSON object
				// ({"AuthToken":...,"Arguments":...}). webtty protocol messages
				// always start with a digit type byte (0-6), never '{', so any
				// '{'-leading message is a handshake and must NOT reach webtty.
				// The frontend sends a '6' ws-api call BEFORE the init and may
				// re-send the handshake on reconnect, so we can't just swallow
				// the first message — we drop every '{'-leading one.
				if len(msg) > 0 && msg[0] == '{' {
					continue
				}
				if len(msg) > 1 && msg[0] == '6' {
					if err := handleWSAPIRequest(writeClient, apiPane, msg[1:]); err != nil {
						log.Printf("[ttyd] ws api error: %v", err)
					}
					continue
				}
				msg = filterDAQuery(msg)
				if msg == nil {
					continue
				}
			}
			if err := l.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}
}
