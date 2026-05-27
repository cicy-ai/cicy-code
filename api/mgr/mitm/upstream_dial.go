package mitm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// errMihomoUnreachable marks a global-egress dial that failed because the local
// mihomo SOCKS5 proxy could not be reached (TCP connect failed) — as opposed to
// mihomo being up and rejecting the CONNECT. DialTCP fails open to direct only
// for this case.
var errMihomoUnreachable = errors.New("mitm: mihomo unreachable")

// Dialer wraps the upstream-dial logic; one instance per running MITM
// node. Construct with NewDialer.
type Dialer struct {
	cfg    UpstreamConfig
	cas    *x509.CertPool // for chain mode trust_ca; nil otherwise (system pool)
	chain  bool
	egress EgressFunc // optional dynamic global-egress override; see EgressFunc
}

// EgressFunc, when set on a Dialer, is consulted on every DialTCP. If it
// returns enabled=true, the dial is routed through the given SOCKS5 proxy (the
// local mihomo mixed port) instead of the static upstream mode — this is how
// the "global egress via mihomo" switch in global.json takes effect live,
// without restarting MITM. auth is "" for the local mihomo (no auth needed).
type EgressFunc func() (enabled bool, socks5Addr string, auth string)

// NewDialer reads the upstream config and pre-loads any trust_ca file. egress
// may be nil (no dynamic global-egress override).
func NewDialer(cfg UpstreamConfig, egress EgressFunc) (*Dialer, error) {
	d := &Dialer{cfg: cfg, egress: egress}
	if cfg.Mode == "chain" {
		if cfg.Chain == nil {
			return nil, errors.New("mitm: chain mode requires upstream.chain")
		}
		pem, err := os.ReadFile(cfg.Chain.TrustCA)
		if err != nil {
			return nil, fmt.Errorf("mitm: read chain trust_ca %s: %w", cfg.Chain.TrustCA, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("mitm: chain trust_ca has no valid certs")
		}
		d.cas = pool
		d.chain = true
	}
	return d, nil
}

// DialTCP returns an opaque byte-stream to the upstream identified by
// hostPort. The caller is responsible for any TLS layering on top.
// Honors the configured upstream mode.
func (d *Dialer) DialTCP(ctx context.Context, hostPort string) (net.Conn, error) {
	// Dynamic global egress (global.json mihomo_global_egress): when enabled,
	// route every dial through the local mihomo mixed port so the exit IP is
	// whatever node mihomo currently has selected. Overrides the static mode;
	// when off, fall through to the configured mode (direct by default).
	if d.egress != nil {
		if enabled, addr, auth := d.egress(); enabled {
			if addr == "" {
				return nil, fmt.Errorf("mitm: global egress enabled but no mihomo socks5 addr")
			}
			conn, err := d.dialViaSOCKS5(ctx, addr, auth, hostPort)
			if err == nil {
				return conn, nil
			}
			// Fail-open: only when the local mihomo itself is UNREACHABLE (not
			// started yet, crashed, mid-restart) do we fall back to a direct
			// dial — otherwise global-egress-on (which now defaults on) would
			// break every request whenever mihomo is briefly down. The exit IP
			// degrades to the box's own IP instead of mihomo's selected node.
			// A reachable mihomo that REJECTS the route (e.g. an explicit
			// fail-closed reject node) is honored, not bypassed.
			if errors.Is(err, errMihomoUnreachable) {
				log.Printf("[mitm] global egress on but mihomo unreachable (%v); failing open to direct for %s", err, hostPort)
				return d.dialDirect(ctx, hostPort)
			}
			return nil, err
		}
	}
	switch d.cfg.Mode {
	case "direct", "":
		return d.dialDirect(ctx, hostPort)
	case "mihomo":
		return d.dialViaSOCKS5(ctx, d.cfg.MihomoSOCKS5, d.cfg.MihomoAuth, hostPort)
	case "chain":
		return d.dialViaSOCKS5(ctx, d.cfg.Chain.NextHop, d.cfg.Chain.Auth, hostPort)
	default:
		return nil, fmt.Errorf("mitm: unknown upstream mode %q", d.cfg.Mode)
	}
}

// DialTLS dials and additionally completes a TLS client handshake against
// hostPort. ServerName is taken from the host portion of hostPort.
//
// In chain mode the TLS handshake is validated against the chain trust_ca
// pool (not the system pool), because the next hop presents a cert signed
// by its own MITM CA, not by a real public CA.
func (d *Dialer) DialTLS(ctx context.Context, hostPort string) (*tls.Conn, error) {
	raw, err := d.DialTCP(ctx, hostPort)
	if err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("mitm: split host:port %q: %w", hostPort, err)
	}
	tlsCfg := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
	}
	if d.chain {
		tlsCfg.RootCAs = d.cas
	}
	tlsConn := tls.Client(raw, tlsCfg)
	if deadline := tlsDeadline(d.cfg.TLSTimeout); !deadline.IsZero() {
		_ = raw.SetDeadline(deadline)
		defer raw.SetDeadline(time.Time{})
	}
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("mitm: upstream TLS handshake to %s: %w", hostPort, err)
	}
	return tlsConn, nil
}

func (d *Dialer) dialDirect(ctx context.Context, hostPort string) (net.Conn, error) {
	dl := net.Dialer{Timeout: time.Duration(d.cfg.DialTimeout)}
	return dl.DialContext(ctx, "tcp", hostPort)
}

// dialViaSOCKS5 connects to a SOCKS5 server at proxyAddr and issues a
// CONNECT to targetHostPort. Optional auth is "user:pass" using RFC 1929.
func (d *Dialer) dialViaSOCKS5(ctx context.Context, proxyAddr, auth, targetHostPort string) (net.Conn, error) {
	dl := net.Dialer{Timeout: time.Duration(d.cfg.DialTimeout)}
	conn, err := dl.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		// Wrap as errMihomoUnreachable so DialTCP can distinguish "proxy down"
		// (fail open to direct) from "proxy up but rejected the CONNECT" (honor).
		return nil, fmt.Errorf("%w: dial socks5 proxy %s: %v", errMihomoUnreachable, proxyAddr, err)
	}
	if err := socks5ClientHandshake(conn, auth, targetHostPort); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// socks5ClientHandshake is the minimal SOCKS5 client we need. It performs
// the greeting (optionally with RFC 1929 user/pass) and a CONNECT.
func socks5ClientHandshake(conn net.Conn, auth, targetHostPort string) error {
	// Greeting
	if auth != "" {
		if _, err := conn.Write([]byte{socks5Version, 2, authNone, authUserPass}); err != nil {
			return err
		}
	} else {
		if _, err := conn.Write([]byte{socks5Version, 1, authNone}); err != nil {
			return err
		}
	}
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return fmt.Errorf("socks5 client greeting: %w", err)
	}
	if hdr[0] != socks5Version {
		return fmt.Errorf("socks5 client: bad version %d", hdr[0])
	}
	switch hdr[1] {
	case authNone:
		// nothing to do
	case authUserPass:
		if err := socks5ClientUserPass(conn, auth); err != nil {
			return err
		}
	default:
		return fmt.Errorf("socks5 client: server requires unsupported auth %d", hdr[1])
	}

	// CONNECT request
	host, portStr, err := net.SplitHostPort(targetHostPort)
	if err != nil {
		return fmt.Errorf("socks5 client: split target: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("socks5 client: bad port: %w", err)
	}
	var req []byte
	req = append(req, socks5Version, cmdConnect, 0x00)
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, atypIPv4)
			req = append(req, ip4...)
		} else {
			req = append(req, atypIPv6)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("socks5 client: hostname too long")
		}
		req = append(req, atypDomain, byte(len(host)))
		req = append(req, []byte(host)...)
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	req = append(req, portBytes...)
	if _, err := conn.Write(req); err != nil {
		return err
	}

	// CONNECT reply: VER REP RSV ATYP BND.ADDR BND.PORT
	respHdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, respHdr); err != nil {
		return fmt.Errorf("socks5 client: read reply: %w", err)
	}
	if respHdr[1] != repSuccess {
		return fmt.Errorf("socks5 client: connect failed, rep=%d", respHdr[1])
	}
	switch respHdr[3] {
	case atypIPv4:
		if _, err := io.ReadFull(conn, make([]byte, 4+2)); err != nil {
			return err
		}
	case atypDomain:
		lenByte := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenByte); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, make([]byte, int(lenByte[0])+2)); err != nil {
			return err
		}
	case atypIPv6:
		if _, err := io.ReadFull(conn, make([]byte, 16+2)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("socks5 client: bad bnd atyp %d", respHdr[3])
	}
	return nil
}

func socks5ClientUserPass(conn net.Conn, auth string) error {
	i := strings.IndexByte(auth, ':')
	if i < 0 {
		return fmt.Errorf("socks5 client: auth must be user:pass")
	}
	user, pass := auth[:i], auth[i+1:]
	if len(user) > 255 || len(pass) > 255 {
		return fmt.Errorf("socks5 client: user/pass too long")
	}
	msg := []byte{authNone1929, byte(len(user))}
	msg = append(msg, []byte(user)...)
	msg = append(msg, byte(len(pass)))
	msg = append(msg, []byte(pass)...)
	if _, err := conn.Write(msg); err != nil {
		return err
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return err
	}
	if resp[1] != 0x00 {
		return fmt.Errorf("socks5 client: auth rejected")
	}
	return nil
}

func tlsDeadline(d Duration) time.Time {
	if d == 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(d))
}
