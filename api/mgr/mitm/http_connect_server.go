// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package mitm

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// readHTTPConnect parses an HTTP CONNECT request — the proxy handshake that
// node-based agent CLIs (claude / opencode / codex via undici `fetch`) use.
// They honor HTTPS_PROXY but reject SOCKS5 ("UnsupportedProxyProtocol"), so
// this listener gives them an HTTP CONNECT entry into the same TLS-terminate +
// audit pipeline as the SOCKS5 path.
//
// Identity comes from the Proxy-Authorization username (Basic <base64(user:pw)>),
// which the boot sets to $X_AGENT_SHORT_ID — the socks5_username rule reads it.
//
// The client waits for our "200 Connection Established" before sending the TLS
// ClientHello, so reading the request headers does not over-read into TLS; the
// returned Conn is positioned right before the first TLS byte (same contract as
// readSOCKS5Handshake).
func readHTTPConnect(conn net.Conn, handshakeDeadline time.Duration) (*SOCKS5Request, error) {
	if handshakeDeadline > 0 {
		_ = conn.SetDeadline(time.Now().Add(handshakeDeadline))
		defer conn.SetDeadline(time.Time{})
	}
	br := bufio.NewReader(conn)

	requestLine, err := br.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("http connect: read request line: %w", err)
	}
	parts := strings.Fields(strings.TrimSpace(requestLine))
	if len(parts) < 2 || !strings.EqualFold(parts[0], "CONNECT") {
		return nil, fmt.Errorf("http connect: not a CONNECT request: %q", strings.TrimSpace(requestLine))
	}

	host, portStr, splitErr := net.SplitHostPort(parts[1])
	if splitErr != nil {
		host, portStr = parts[1], "443"
	}
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 443
	}

	var username string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("http connect: read headers: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		if i := strings.IndexByte(line, ':'); i > 0 {
			if strings.EqualFold(strings.TrimSpace(line[:i]), "Proxy-Authorization") {
				username = parseProxyAuthUsername(strings.TrimSpace(line[i+1:]))
			}
		}
	}

	// Acknowledge the tunnel. The client now starts TLS directly on conn.
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return nil, fmt.Errorf("http connect: write 200: %w", err)
	}
	return &SOCKS5Request{Conn: conn, Host: host, Port: port, Username: username}, nil
}

// parseProxyAuthUsername returns the username from a `Basic <base64(user:pw)>`
// Proxy-Authorization value. Empty on any parse failure.
func parseProxyAuthUsername(headerVal string) string {
	fields := strings.Fields(headerVal)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Basic") {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return ""
	}
	creds := string(decoded)
	if i := strings.IndexByte(creds, ':'); i >= 0 {
		return creds[:i]
	}
	return creds
}
