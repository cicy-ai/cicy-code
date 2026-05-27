package mitm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// Server is one running MITM node. Construct with NewServer, then call Start.
type Server struct {
	cfg     *Config
	ca      *CA
	dialer  *Dialer
	hook    AuditHook
	breaker BreakerHook

	listener     net.Listener
	httpListener net.Listener
	wg           sync.WaitGroup

	// seenUpstreams dedups the "seen host" discovery log so each distinct
	// (agent, host:port, mode) tuple is logged only once per process. Lets the
	// operator discover which upstreams non-gateway agents hit (different model
	// providers under official login) and decide what to whitelist.
	seenUpstreams sync.Map

	// shutdown coordination
	closing chan struct{}
}

// NewServer prepares all resources (CA, dialer) but does not yet listen.
// Returns nil, nil if cfg.Enabled is false — caller should treat this as
// "MITM disabled, skip" rather than an error.
//
// hook    — receives audit events; pass nil to disable audit submission.
// breaker — receives PreventiveCheck calls; pass nil to disable inline
//
//	blocking (all turns pass through to upstream).
func NewServer(cfg *Config, hook AuditHook, breaker BreakerHook, egress ...EgressFunc) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("mitm: nil config")
	}
	if !cfg.Enabled {
		return nil, nil
	}
	ca, err := LoadOrCreateCA(cfg.CA)
	if err != nil {
		return nil, err
	}
	var eg EgressFunc
	if len(egress) > 0 {
		eg = egress[0]
	}
	dialer, err := NewDialer(cfg.Upstream, eg)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:     cfg,
		ca:      ca,
		dialer:  dialer,
		hook:    hook,
		breaker: breaker,
		closing: make(chan struct{}),
	}, nil
}

// RootCertPEM returns the MITM CA in PEM form — for cicy-code to expose
// over an /api/mitm/ca endpoint and for the install-ca CLI.
func (s *Server) RootCertPEM() []byte {
	if s == nil || s.ca == nil {
		return nil
	}
	return s.ca.RootCertPEM()
}

// Start binds the SOCKS5 listener and accepts connections in a goroutine.
// Start returns immediately; use Stop or ctx cancellation to terminate.
func (s *Server) Start(ctx context.Context) error {
	if s == nil {
		return nil
	}
	ln, err := net.Listen("tcp", s.cfg.SOCKS5Listen)
	if err != nil {
		return fmt.Errorf("mitm: listen %s: %w", s.cfg.SOCKS5Listen, err)
	}
	s.listener = ln
	log.Printf("[mitm] listening on %s (socks5), node=%s, final_hop=%v, whitelist=%v",
		s.cfg.SOCKS5Listen, s.cfg.Node.ID, s.cfg.Node.FinalHop, s.cfg.Hosts.Whitelist)
	s.wg.Add(1)
	go s.acceptLoop(ctx, ln, s.handleConn)

	// HTTP CONNECT listener for node-based CLIs that can't do SOCKS5.
	if s.cfg.HTTPConnectListen != "" {
		hln, err := net.Listen("tcp", s.cfg.HTTPConnectListen)
		if err != nil {
			return fmt.Errorf("mitm: listen %s: %w", s.cfg.HTTPConnectListen, err)
		}
		s.httpListener = hln
		log.Printf("[mitm] listening on %s (http connect)", s.cfg.HTTPConnectListen)
		s.wg.Add(1)
		go s.acceptLoop(ctx, hln, s.handleConnHTTP)
	}
	return nil
}

// Stop closes the listener and waits for in-flight connections to drain.
func (s *Server) Stop() {
	if s == nil {
		return
	}
	select {
	case <-s.closing:
		// already closed
	default:
		close(s.closing)
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.httpListener != nil {
		_ = s.httpListener.Close()
	}
	s.wg.Wait()
}

func (s *Server) acceptLoop(ctx context.Context, ln net.Listener, handle func(context.Context, net.Conn)) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.closing:
				return
			default:
			}
			if ctx.Err() != nil {
				return
			}
			// Transient accept errors: back off briefly and retry.
			log.Printf("[mitm] accept: %v", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			handle(ctx, conn)
		}()
	}
}

func (s *Server) handleConn(ctx context.Context, raw net.Conn) {
	req, err := readSOCKS5Handshake(raw, 10*time.Second)
	if err != nil {
		log.Printf("[mitm] socks5 handshake from %s: %v", raw.RemoteAddr(), err)
		return
	}
	s.serveRequest(ctx, req)
}

func (s *Server) handleConnHTTP(ctx context.Context, raw net.Conn) {
	req, err := readHTTPConnect(raw, 10*time.Second)
	if err != nil {
		log.Printf("[mitm] http connect from %s: %v", raw.RemoteAddr(), err)
		return
	}
	s.serveRequest(ctx, req)
}

// serveRequest runs the shared post-handshake path (identity → whitelist →
// TLS terminate → pump) for both the SOCKS5 and HTTP CONNECT front-ends.
func (s *Server) serveRequest(ctx context.Context, req *SOCKS5Request) {
	identity := InferIdentity(s.cfg.Identity.Rules, req.Conn.RemoteAddr(), req.Conn.LocalAddr(), req.Username, req.Host)
	hostPort := req.HostPort()

	intercepted := s.cfg.IsWhitelisted(req.Host)
	mode := "passthrough"
	if intercepted {
		mode = "intercept"
	}
	// Discovery log: record each distinct (agent, host, mode) once. Under
	// official login a worker may hit several different provider hosts; this
	// surfaces them (deduped) so the operator knows what to whitelist.
	agentLabel := identity.AgentID
	if agentLabel == "" {
		agentLabel = "?"
	}
	seenKey := agentLabel + "\x00" + hostPort + "\x00" + mode
	if _, dup := s.seenUpstreams.LoadOrStore(seenKey, struct{}{}); !dup {
		log.Printf("[mitm] seen upstream agent=%s host=%s mode=%s", agentLabel, hostPort, mode)
	}

	if !intercepted {
		// Non-MITM passthrough.
		if err := passthrough(ctx, req.Conn, hostPort, s.dialer.DialTCP); err != nil {
			log.Printf("[mitm] passthrough %s: %v", hostPort, err)
		}
		return
	}

	// MITM path: TLS terminate then pump.
	tlsConn, err := terminateClientTLS(req.Conn, s.ca, req.Host)
	if err != nil {
		// Client probably pins certs. Audit + give up; we can't fall back to
		// passthrough here because the handshake bytes are already consumed.
		log.Printf("[mitm] tls terminate %s: %v (pinning=%v)", req.Host, err, IsPinningError(err))
		return
	}
	defer tlsConn.Close()

	if err := pumpHTTP(ctx, tlsConn, req.Host, req.Port, s.cfg, s.dialer, s.hook, s.breaker, identity); err != nil {
		log.Printf("[mitm] pump %s: %v", hostPort, err)
	}
}
