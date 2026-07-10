// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// --gateway: dial OUT to a cicy zero-trust gateway and serve this instance's
// local API over that reverse tunnel — no inbound port is ever opened here.
//
// The node authenticates to the gateway with a typ=node JWT (from --gateway-token,
// CICY_GATEWAY_TOKEN, or ~/cicy-ai/db/gateway.json). That dial is the ONLY thing
// the gateway verifies — it is how the gateway learns which slug this node serves.
// After that the gateway is a TRANSPARENT pipe: it forwards client requests to us
// byte for byte, and cicy-code's authM authenticates each one with the api_token
// the client carried (Authorization / ?token=), exactly as for localhost:8008.
// Reaching <slug>.gw.cicy-ai.com is reachability, not a credential.
//
// This is the node half of ~/projects/cicy-gateway (P1/①/A). Transport is WSS +
// yamux multiplexing (443-friendly, survives NAT/egress proxies).
package main

import (
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
// explicit flags → env → config file. The current names are CICY_TUNNEL_URL /
// CICY_TUNNEL_TOKEN and ~/cicy-ai/db/tunnel.json; the older CICY_GATEWAY_* /
// gateway.json are still accepted as deprecated aliases (the flag/product was
// renamed gateway → tunnel; old deployments keep working).
func resolveGatewayConfig() (url, token string, insecure bool) {
	url, token, insecure = gatewayURL, gatewayToken, gatewayInsecure
	firstEnv := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				return v
			}
		}
		return ""
	}
	if url == "" {
		url = firstEnv("CICY_TUNNEL_URL", "CICY_GATEWAY_URL")
	}
	if token == "" {
		token = firstEnv("CICY_TUNNEL_TOKEN", "CICY_GATEWAY_TOKEN")
	}
	// tunnel.json (current) then gateway.json (deprecated alias).
	for _, name := range []string{"tunnel.json", "gateway.json"} {
		if url != "" && token != "" {
			break
		}
		var cfg gatewayConfigFile
		if b, err := os.ReadFile(filepath.Join(cicyRootDir, "db", name)); err == nil {
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
	// With --id set and no explicit url, DERIVE it from the hub domain:
	// wss://<id>.<hub>/_tunnel/connect. Hub domain: CICY_HUB env, else hub.cicy-ai.com.
	// So a hub-mode node just needs `--id <team>` + its token (tunnel.json / env).
	if url == "" && teamID != "" {
		hub := strings.TrimSpace(os.Getenv("CICY_HUB"))
		if hub == "" {
			hub = "hub.cicy-ai.com"
		}
		url = "wss://" + teamID + "." + hub + "/_tunnel/connect"
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
// byte for byte. TRANSPARENT: the gateway makes this node reachable at
// <slug>.gw.cicy-ai.com; the request that arrives is exactly what the client
// sent, and cicy-code's authM authenticates it with the api_token the client
// carried (Authorization / ?token=) — identical to a request to localhost:8008.
// The node does NOT inject its api_token: reaching the tunnel is not the same as
// holding the credential, so the caller must present it, just like local.
func gwForward(stream net.Conn, local string) {
	defer stream.Close()
	up, err := net.Dial("tcp", local)
	if err != nil {
		log.Printf("[gateway] dial local %s: %v", local, err)
		return
	}
	defer up.Close()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, stream); done <- struct{}{} }()
	go func() { _, _ = io.Copy(stream, up); done <- struct{}{} }()
	<-done
}

