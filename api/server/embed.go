// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package server

// embed.go exposes the webtty session + static-asset serving as standalone
// helpers so a host (the mgr) can terminate /ttyd/<pane>/ traffic directly,
// without spinning up a per-pane HTTP server bound to its own TCP port.
//
// These reuse the exact same webtty wiring as (*Server).processWSConn and
// handleIndex — the only thing dropped is the per-pane net.Listener and the
// reverse-proxy hop in front of it. The host owns the client WebSocket (and so
// the init handshake + keepalive); RunWebTTY just drives a tmux slave over an
// in-memory master the host pipes to. See RunWebTTY / WriteIndex / StaticHandler.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"io"
	"net/http"
	"sync"

	assetfs "github.com/elazarl/go-bindata-assetfs"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"ttyd-go/webtty"
)

// WSConfig carries the per-session knobs RunWebTTY needs. It mirrors the subset
// of server.Options that (*Server).processWSConn actually applied to webtty.
type WSConfig struct {
	// Title is the terminal window title (e.g. the pane id "w-10063"). The
	// per-pane server used a constant TitleFormat with no template vars, so a
	// plain string is sufficient here.
	Title         string
	PermitWrite   bool
	Reconnect     bool
	ReconnectTime int
	Preferences   *HtermPrefernces
}

// ttydUpgrader upgrades to the "webtty" subprotocol with a permissive origin
// check — equivalent to the per-pane server's upgrader (WSOrigin ".*").
var ttydUpgrader = &websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	Subprotocols:    webtty.Protocols,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// UpgradeWebTTY upgrades an HTTP request to a webtty WebSocket connection,
// negotiating the "webtty" subprotocol the frontend bundle expects.
func UpgradeWebTTY(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return ttydUpgrader.Upgrade(w, r, nil)
}

// RunWebTTY runs a single webtty session over the given in-memory master,
// backed by a fresh slave produced by factory. It blocks until the session
// ends. This is the webtty core of (*Server).processWSConn with the parts the
// host already owns removed: the host consumes the gotty init handshake and
// runs keepalive on the real client WebSocket, then pipes client↔webtty bytes
// through master. No TCP port, no reverse proxy.
func RunWebTTY(ctx context.Context, master io.ReadWriter, factory Factory, cfg *WSConfig) error {
	slave, err := factory.New(nil)
	if err != nil {
		return errors.Wrapf(err, "failed to create backend")
	}
	defer slave.Close()

	opts := []webtty.Option{webtty.WithWindowTitle([]byte(cfg.Title))}
	if cfg.PermitWrite {
		opts = append(opts, webtty.WithPermitWrite())
	}
	if cfg.Reconnect {
		opts = append(opts, webtty.WithReconnect(cfg.ReconnectTime))
	}
	if cfg.Preferences != nil {
		opts = append(opts, webtty.WithMasterPreferences(cfg.Preferences))
	}

	tty, err := webtty.New(master, slave, opts...)
	if err != nil {
		return errors.Wrapf(err, "failed to create webtty")
	}
	return tty.Run(ctx)
}

// --- static asset serving (index.html, js/, css/, favicon, *_token.js) ---

var (
	staticHandlerOnce sync.Once
	staticHandler     http.Handler

	indexTmplOnce sync.Once
	indexTmpl     *template.Template
	indexAssetVer string
)

// StaticHandler serves the embedded ttyd bundle (js/, css/, favicon.png) from
// go-bindata. The assets are pane-independent, so a single shared handler
// replaces every per-pane server's static file handler. The caller is expected
// to strip the "/ttyd/<pane>" prefix so paths resolve as "js/...", "css/...".
func StaticHandler() http.Handler {
	staticHandlerOnce.Do(func() {
		staticHandler = http.FileServer(
			&assetfs.AssetFS{Asset: Asset, AssetDir: AssetDir, Prefix: "static"},
		)
	})
	return staticHandler
}

func initIndex() {
	indexTmplOnce.Do(func() {
		data, err := Asset("static/index.html")
		if err != nil {
			panic("index not found") // must be in bindata
		}
		t, err := template.New("index").Parse(string(data))
		if err != nil {
			panic("index template parse failed") // must be valid
		}
		indexTmpl = t
		indexAssetVer = "dev"
		if bundle, e := Asset("static/js/gotty-bundle.js"); e == nil {
			sum := sha256.Sum256(bundle)
			indexAssetVer = hex.EncodeToString(sum[:])[:12]
		}
	})
}

// WriteIndex renders the ttyd index.html with the given per-pane title. It is
// the standalone equivalent of (*Server).handleIndex.
func WriteIndex(w http.ResponseWriter, title string) error {
	initIndex()
	buf := new(bytes.Buffer)
	if err := indexTmpl.Execute(buf, map[string]interface{}{
		"title":         title,
		"static_prefix": ttydStaticPrefix(),
		"asset_v":       indexAssetVer,
	}); err != nil {
		return err
	}
	// index.html embeds gotty-bundle.js?v=<hash>; if the html itself is cached
	// the browser won't see new hashes — never cache it.
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(buf.Bytes())
	return nil
}

// AuthTokenJS / TermConfigJS return the tiny generated JS shims the frontend
// loads (auth_token.js, config.js), standalone equivalents of the per-pane
// server's handleAuthToken / handleConfig.
func AuthTokenJS(credential string) []byte { return []byte("var gotty_auth_token = '" + credential + "';") }
func TermConfigJS(term string) []byte      { return []byte("var gotty_term = '" + term + "';") }
