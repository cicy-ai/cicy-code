// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// --gateway: dial OUT to a cicy zero-trust gateway and serve this instance's
// local API over that reverse tunnel — no inbound port is ever opened here.
//
// The node authenticates to the gateway with a typ=node JWT (from --gateway-token,
// CICY_GATEWAY_TOKEN, or ~/cicy-ai/db/gateway.json). The gateway verifies the
// short-lived typ=access JWT that mobile/web clients carry, authorizes org+node,
// then proxies over this tunnel. For each proxied request we inject the local
// api_token so cicy-code's authM is satisfied WITHOUT the client ever holding it:
// the phone carries only the revocable cloud access token; the node's static
// api_token never leaves the node.
//
// This is the node half of ~/projects/cicy-gateway (P1/①/A). Transport is WSS +
// yamux multiplexing (443-friendly, survives NAT/egress proxies).
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

var (
	gatewayMode     bool   // --gateway present (or CICY_GATEWAY_URL/gateway.json set)
	gatewayURL      string // wss://<gw>/_tunnel/connect
	gatewayToken    string // node JWT (typ=node)
	gatewayInsecure bool   // --gateway-insecure → skip TLS verify (dev self-signed)
)

type gatewayConfigFile struct {
	URL      string `json:"url"`
	Token    string `json:"token"`
	Insecure bool   `json:"insecure"`
}

// resolveGatewayConfig fills url/token/insecure from, in priority order:
// explicit flags → env (CICY_GATEWAY_URL / CICY_GATEWAY_TOKEN) → gateway.json.
func resolveGatewayConfig() (url, token string, insecure bool) {
	url, token, insecure = gatewayURL, gatewayToken, gatewayInsecure
	if url == "" {
		url = strings.TrimSpace(os.Getenv("CICY_GATEWAY_URL"))
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv("CICY_GATEWAY_TOKEN"))
	}
	if url == "" || token == "" {
		var cfg gatewayConfigFile
		if b, err := os.ReadFile(filepath.Join(cicyRootDir, "db", "gateway.json")); err == nil {
			if json.Unmarshal(b, &cfg) == nil {
				if url == "" {
					url = strings.TrimSpace(cfg.URL)
				}
				if token == "" {
					token = strings.TrimSpace(cfg.Token)
				}
				insecure = insecure || cfg.Insecure
			}
		}
	}
	return
}

// startGatewayTunnel supervises the reverse tunnel: dial, serve, reconnect with
// backoff. Runs in its own goroutine; never returns while --gateway is on.
func startGatewayTunnel(localPort string) {
	url, token, insecure := resolveGatewayConfig()
	if url == "" || token == "" {
		log.Printf("[gateway] --gateway set but url/token missing (need --gateway-token / CICY_GATEWAY_TOKEN / db/gateway.json) — tunnel not started")
		return
	}
	local := "127.0.0.1:" + localPort
	for {
		if err := gwServe(url, token, local, insecure); err != nil {
			log.Printf("[gateway] tunnel dropped: %v — reconnecting in 3s", err)
			time.Sleep(3 * time.Second)
		}
	}
}

func gwServe(url, token, local string, insecure bool) error {
	c, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
		HTTPClient: &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
		}},
	})
	if err != nil {
		return err
	}
	c.SetReadLimit(1 << 30)
	nc := websocket.NetConn(context.Background(), c, websocket.MessageBinary)
	sess, err := yamux.Server(nc, nil)
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "yamux")
		return err
	}
	log.Printf("[gateway] tunnel up → %s (serving %s, no inbound port)", url, local)
	for {
		stream, err := sess.Accept()
		if err != nil {
			return err
		}
		go gwForward(stream, local)
	}
}

// gwForward pipes one gateway stream <-> a fresh local cicy-code connection,
// injecting Authorization: Bearer <api_token> into the request head so authM
// passes. Works for plain HTTP and the WS upgrade handshake alike.
func gwForward(stream net.Conn, local string) {
	defer stream.Close()
	up, err := net.Dial("tcp", local)
	if err != nil {
		log.Printf("[gateway] dial local %s: %v", local, err)
		return
	}
	defer up.Close()

	src := io.Reader(stream)
	if tok := loadAPIToken(); tok != "" {
		if r, err := gwInjectAuth(stream, up, tok); err == nil {
			src = r
		} else {
			log.Printf("[gateway] inject skipped: %v", err)
		}
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, src); done <- struct{}{} }()
	go func() { _, _ = io.Copy(stream, up); done <- struct{}{} }()
	<-done
}

// gwInjectAuth reads the HTTP request head from stream, writes it to up with an
// injected Authorization header (unless the request already carries one), and
// returns a reader positioned at the remaining bytes (body / WS frames).
func gwInjectAuth(stream, up net.Conn, apiToken string) (io.Reader, error) {
	br := bufio.NewReader(stream)
	var head bytes.Buffer
	hasAuth := false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(strings.ToLower(line), "authorization:") {
			hasAuth = true
		}
		if line == "\r\n" || line == "\n" { // end of header block
			if !hasAuth {
				head.WriteString("Authorization: Bearer " + apiToken + "\r\n")
			}
			head.WriteString(line)
			break
		}
		head.WriteString(line)
	}
	if _, err := up.Write(head.Bytes()); err != nil {
		return nil, err
	}
	return br, nil
}
