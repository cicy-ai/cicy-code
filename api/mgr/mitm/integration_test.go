package mitm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAuditHook records every StartTurn + WrapResponseBody invocation so
// tests can assert plaintext was captured. Mirrors the structure of the
// real ai_gateway adapter without depending on package main.
type fakeAuditHook struct {
	mu       sync.Mutex
	turns    []*fakeAuditTurn
}

func (f *fakeAuditHook) StartTurn(provider, agentID string, target *url.URL, method string, headers http.Header, body []byte) AuditTurn {
	turn := &fakeAuditTurn{
		Provider: provider,
		AgentID:  agentID,
		URL:      target.String(),
		Method:   method,
		Headers:  headers,
		Body:     append([]byte{}, body...),
	}
	f.mu.Lock()
	f.turns = append(f.turns, turn)
	f.mu.Unlock()
	return turn
}

type fakeAuditTurn struct {
	Provider     string
	AgentID      string
	URL          string
	Method       string
	Headers      http.Header
	Body         []byte
	ResponseBody []byte
	StatusCode   int
	Err          error
}

func (t *fakeAuditTurn) WrapResponseBody(inner io.ReadCloser, statusCode int, headers http.Header, contentLength int64) io.ReadCloser {
	t.StatusCode = statusCode
	return &teeReader{src: inner, sink: &t.ResponseBody}
}

func (t *fakeAuditTurn) Fail(err error) { t.Err = err }

type teeReader struct {
	src  io.ReadCloser
	sink *[]byte
}

func (t *teeReader) Read(p []byte) (int, error) {
	n, err := t.src.Read(p)
	if n > 0 {
		*t.sink = append(*t.sink, p[:n]...)
	}
	return n, err
}
func (t *teeReader) Close() error { return t.src.Close() }

// startFakeUpstream brings up a TLS HTTPS server backed by a freshly
// minted self-signed cert. Returns the host:port and a CertPool that
// trusts the upstream — used by the Dialer for chain mode emulation, or
// passed via custom dialer to override system CA.
func startFakeUpstream(t *testing.T, handler http.HandlerFunc) (hostPort string, ca *x509.CertPool, stop func()) {
	t.Helper()
	srv := &http.Server{Handler: handler}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// reuse MITM CA to mint upstream cert
	caDir := t.TempDir()
	cfg := CAConfig{
		CertPath:       filepath.Join(caDir, "upstream-ca.crt"),
		KeyPath:        filepath.Join(caDir, "upstream-ca.key"),
		LeafCacheSize:  4,
		LeafValidYears: 1,
	}
	upCA, err := LoadOrCreateCA(cfg)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := upCA.SignLeaf("127.0.0.1")
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: leaf.DERChain,
			PrivateKey:  leaf.Key,
		}},
		NextProtos: []string{"http/1.1"},
	}
	tlsLn := tls.NewListener(ln, tlsCfg)
	go srv.Serve(tlsLn)

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(upCA.RootCertPEM())
	return ln.Addr().String(), pool, func() {
		_ = srv.Close()
	}
}

// TestMITM_EndToEnd verifies the full pipeline:
//
//   curl --proxy socks5://mitm  https://fake-upstream/v1/messages
//      → SOCKS5 handshake → MITM CA leaf → HTTP parse → audit StartTurn →
//        upstream dial → response → WrapResponseBody → back to client
//
// Both inbound and outbound TLS use the test CA; no real network needed.
func TestMITM_EndToEnd(t *testing.T) {
	// 1. Fake upstream that returns a fixed JSON body (mimics an Anthropic
	//    Messages non-streaming response).
	upstreamHost, upstreamPool, stopUpstream := startFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"msg_test","content":[{"type":"text","text":"echoed: %s"}]}`, string(body))
	})
	defer stopUpstream()
	upstreamHostName, upstreamPortStr, _ := net.SplitHostPort(upstreamHost)
	_ = upstreamHostName

	// 2. MITM config: listen on a free port, whitelist 127.0.0.1, direct upstream.
	caDir := t.TempDir()
	cfg := &Config{
		Enabled:      true,
		SOCKS5Listen: "127.0.0.1:0",
		CA: CAConfig{
			CertPath:       filepath.Join(caDir, "mitm-ca.crt"),
			KeyPath:        filepath.Join(caDir, "mitm-ca.key"),
			LeafCacheSize:  4,
			LeafValidYears: 1,
		},
		Hosts: HostsConfig{Whitelist: []string{"127.0.0.1"}},
		Node:  NodeConfig{ID: "test-node", FinalHop: true, MaxHops: 5},
		Upstream: UpstreamConfig{
			Mode:        "direct",
			DialTimeout: Duration(5 * time.Second),
			TLSTimeout:  Duration(5 * time.Second),
		},
		Identity: IdentityConfig{Rules: []IdentityRule{
			{Kind: "socks5_username"},
			{Kind: "fallback", Value: "mitm:{host}"},
		}},
		Audit: AuditConfig{HistoryRoot: caDir},
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	// 3. Override the upstream's TLS trust so the dialer accepts our test CA.
	hook := &fakeAuditHook{}
	srv, err := NewServer(cfg, hook, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Inject upstream CA into the dialer (test-only — production uses system pool).
	srv.dialer.cas = upstreamPool
	srv.dialer.chain = true // forces dialer to use srv.dialer.cas

	// 4. Bind on ephemeral port (cfg.SOCKS5Listen has :0)
	ln, err := net.Listen("tcp", cfg.SOCKS5Listen)
	if err != nil {
		t.Fatal(err)
	}
	srv.listener = ln
	srv.wg.Add(1)
	go srv.acceptLoop(context.Background(), ln, srv.handleConn)
	defer srv.Stop()

	mitmAddr := ln.Addr().String()

	// 5. SOCKS5 dial helper for the http.Transport.
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := net.Dial("tcp", mitmAddr)
		if err != nil {
			return nil, err
		}
		// Target is the upstream as we want MITM to see it.
		target := "127.0.0.1:" + upstreamPortStr
		if err := socks5ClientHandshakeWithUser(conn, "w-10042", target); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}

	// 6. Trust the MITM CA from the client side.
	clientPool := x509.NewCertPool()
	clientPool.AppendCertsFromPEM(srv.RootCertPEM())

	client := &http.Client{
		Transport: &http.Transport{
			DialContext:     dial,
			TLSClientConfig: &tls.Config{RootCAs: clientPool, ServerName: "127.0.0.1"},
		},
		Timeout: 10 * time.Second,
	}

	// 7. Make the request through MITM.
	resp, err := client.Post(
		"https://127.0.0.1/v1/messages",
		"application/json",
		strings.NewReader(`{"model":"claude-test","prompt":"hello"}`),
	)
	if err != nil {
		t.Fatalf("client.Post: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "echoed:") {
		t.Fatalf("upstream body not relayed: %s", body)
	}

	// 8. Assert MITM audit hook captured the turn.
	if len(hook.turns) != 1 {
		t.Fatalf("expected 1 audit turn, got %d", len(hook.turns))
	}
	turn := hook.turns[0]
	if turn.AgentID != "w-10042" {
		t.Errorf("AgentID=%q (expected from SOCKS5 username)", turn.AgentID)
	}
	if turn.Method != "POST" {
		t.Errorf("Method=%q", turn.Method)
	}
	if !strings.Contains(string(turn.Body), "claude-test") {
		t.Errorf("request body not captured: %s", turn.Body)
	}
	if turn.StatusCode != 200 {
		t.Errorf("status code not captured: %d", turn.StatusCode)
	}
	if !strings.Contains(string(turn.ResponseBody), "echoed:") {
		t.Errorf("response body not captured: %s", turn.ResponseBody)
	}
	// Verify chain headers were injected.
	if turn.Headers.Get(HeaderAgent) == "" {
		t.Errorf("X-Cicy-Mitm-Agent not injected")
	}
	if !strings.Contains(turn.Headers.Get(HeaderTrace), "test-node") {
		t.Errorf("X-Cicy-Mitm-Trace=%q missing test-node", turn.Headers.Get(HeaderTrace))
	}
}

// socks5ClientHandshakeWithUser performs greeting+auth+CONNECT in one
// shot. Differs from production socks5ClientHandshake (upstream_dial.go)
// by always sending RFC 1929 user/pass auth so the server captures the
// username for identity inference. Empty password.
func socks5ClientHandshakeWithUser(conn net.Conn, user, target string) error {
	// Greeting: claim user/pass support.
	if _, err := conn.Write([]byte{socks5Version, 1, authUserPass}); err != nil {
		return err
	}
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return err
	}
	if hdr[0] != socks5Version || hdr[1] != authUserPass {
		return fmt.Errorf("bad greeting reply: %v", hdr)
	}
	// Userpass: send user, empty password.
	authMsg := []byte{authNone1929, byte(len(user))}
	authMsg = append(authMsg, []byte(user)...)
	authMsg = append(authMsg, 0)
	if _, err := conn.Write(authMsg); err != nil {
		return err
	}
	authResp := make([]byte, 2)
	if _, err := io.ReadFull(conn, authResp); err != nil {
		return err
	}
	if authResp[1] != 0 {
		return fmt.Errorf("userpass auth rejected")
	}
	// CONNECT request (no greeting — already past it).
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return err
	}
	req := []byte{socks5Version, cmdConnect, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, atypIPv4)
			req = append(req, ip4...)
		} else {
			req = append(req, atypIPv6)
			req = append(req, ip.To16()...)
		}
	} else {
		req = append(req, atypDomain, byte(len(host)))
		req = append(req, []byte(host)...)
	}
	req = append(req, byte(port>>8), byte(port&0xFF))
	if _, err := conn.Write(req); err != nil {
		return err
	}
	respHdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, respHdr); err != nil {
		return err
	}
	if respHdr[1] != repSuccess {
		return fmt.Errorf("CONNECT rep=%d", respHdr[1])
	}
	// drain BND addr (we know ATYP=IPv4 from our server impl)
	switch respHdr[3] {
	case atypIPv4:
		if _, err := io.ReadFull(conn, make([]byte, 4+2)); err != nil {
			return err
		}
	case atypIPv6:
		if _, err := io.ReadFull(conn, make([]byte, 16+2)); err != nil {
			return err
		}
	case atypDomain:
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, make([]byte, int(l[0])+2)); err != nil {
			return err
		}
	}
	return nil
}
