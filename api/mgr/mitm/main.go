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

	listener net.Listener
	wg       sync.WaitGroup

	// shutdown coordination
	closing chan struct{}
}

// NewServer prepares all resources (CA, dialer) but does not yet listen.
// Returns nil, nil if cfg.Enabled is false — caller should treat this as
// "MITM disabled, skip" rather than an error.
//
// hook    — receives audit events; pass nil to disable audit submission.
// breaker — receives PreventiveCheck calls; pass nil to disable inline
//           blocking (all turns pass through to upstream).
func NewServer(cfg *Config, hook AuditHook, breaker BreakerHook) (*Server, error) {
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
	dialer, err := NewDialer(cfg.Upstream)
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
	log.Printf("[mitm] listening on %s, node=%s, final_hop=%v, whitelist=%v",
		s.cfg.SOCKS5Listen, s.cfg.Node.ID, s.cfg.Node.FinalHop, s.cfg.Hosts.Whitelist)

	s.wg.Add(1)
	go s.acceptLoop(ctx)
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
	s.wg.Wait()
}

func (s *Server) acceptLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
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
			s.handleConn(ctx, conn)
		}()
	}
}

func (s *Server) handleConn(ctx context.Context, raw net.Conn) {
	// SOCKS5 handshake
	req, err := readSOCKS5Handshake(raw, 10*time.Second)
	if err != nil {
		log.Printf("[mitm] socks5 handshake from %s: %v", raw.RemoteAddr(), err)
		return
	}

	identity := InferIdentity(s.cfg.Identity.Rules, raw.RemoteAddr(), raw.LocalAddr(), req.Username, req.Host)
	hostPort := req.HostPort()

	if !s.cfg.IsWhitelisted(req.Host) {
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
