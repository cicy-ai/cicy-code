package mitm

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWriteUpgradeResponsePreservesSwitchingProtocols(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		defer server.Close()
		done <- writeUpgradeResponse(server, &http.Response{
			Status:     "101 Switching Protocols",
			StatusCode: http.StatusSwitchingProtocols,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header: http.Header{
				"Connection":           []string{"Upgrade"},
				"Upgrade":              []string{"websocket"},
				"Sec-Websocket-Accept": []string{"test-accept"},
			},
		})
	}()

	resp, err := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	if !headerHasToken(resp.Header, "Connection", "upgrade") || !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		t.Fatalf("upgrade headers lost: %#v", resp.Header)
	}
	if err := <-done; err != nil {
		t.Fatalf("write upgrade response: %v", err)
	}
}

func TestProxyUpgradedConnectionIsBidirectional(t *testing.T) {
	clientPeer, mitmClient := net.Pipe()
	upstreamPeer, mitmUpstream := net.Pipe()
	defer clientPeer.Close()
	defer upstreamPeer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- proxyUpgradedConnection(ctx, mitmClient, bufio.NewReader(mitmClient), mitmUpstream)
	}()

	clientPayload := []byte("client-websocket-frame")
	go func() { _, _ = clientPeer.Write(clientPayload) }()
	gotClient := make([]byte, len(clientPayload))
	if _, err := io.ReadFull(upstreamPeer, gotClient); err != nil {
		t.Fatalf("client -> upstream: %v", err)
	}
	if string(gotClient) != string(clientPayload) {
		t.Fatalf("client payload = %q, want %q", gotClient, clientPayload)
	}

	upstreamPayload := []byte("upstream-websocket-frame")
	go func() { _, _ = upstreamPeer.Write(upstreamPayload) }()
	gotUpstream := make([]byte, len(upstreamPayload))
	if _, err := io.ReadFull(clientPeer, gotUpstream); err != nil {
		t.Fatalf("upstream -> client: %v", err)
	}
	if string(gotUpstream) != string(upstreamPayload) {
		t.Fatalf("upstream payload = %q, want %q", gotUpstream, upstreamPayload)
	}

	_ = clientPeer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("proxy returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy did not stop after client closed")
	}
}

func TestRawWebSocketRoundTripExposesWritableBody(t *testing.T) {
	upstreamHost, upstreamPool, stopUpstream := startFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("upstream response writer cannot hijack")
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("upstream hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = rw.Flush()
		buf := make([]byte, 128)
		for {
			n, readErr := rw.Read(buf)
			if n > 0 {
				if _, writeErr := rw.Write(buf[:n]); writeErr != nil {
					return
				}
				_ = rw.Flush()
			}
			if readErr != nil {
				return
			}
		}
	})
	defer stopUpstream()

	dialer, err := NewDialer(UpstreamConfig{
		Mode:        "direct",
		DialTimeout: Duration(5 * time.Second),
		TLSTimeout:  Duration(5 * time.Second),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dialer.cas = upstreamPool
	dialer.chain = true

	req, err := http.NewRequest(http.MethodGet, "https://"+upstreamHost+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	resp, err := roundTripWebSocket(context.Background(), dialer, req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	upgraded, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		t.Fatalf("101 body type %T is not io.ReadWriteCloser", resp.Body)
	}
	defer upgraded.Close()

	payload := []byte("roundtrip-websocket-frame")
	if _, err := upgraded.Write(payload); err != nil {
		t.Fatalf("write upgraded body: %v", err)
	}
	echo := make([]byte, len(payload))
	if _, err := io.ReadFull(upgraded, echo); err != nil {
		t.Fatalf("read upgraded body: %v", err)
	}
	if string(echo) != string(payload) {
		t.Fatalf("echo = %q, want %q", echo, payload)
	}
}
