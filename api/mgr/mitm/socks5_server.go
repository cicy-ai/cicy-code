package mitm

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// SOCKS5 protocol constants (RFC 1928).
const (
	socks5Version = 0x05

	authNone     = 0x00
	authUserPass = 0x02
	authNone1929 = 0x01 // RFC 1929 sub-version
	authNoneOK   = 0xFF // NO ACCEPTABLE METHODS

	cmdConnect = 0x01

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04

	repSuccess              = 0x00
	repGeneralFailure       = 0x01
	repCommandNotSupported  = 0x07
	repAddrTypeNotSupported = 0x08
)

// SOCKS5Request is what we hand off to the upstream-handling code.
// Username is empty unless the client used RFC 1929 username/password auth.
type SOCKS5Request struct {
	Conn     net.Conn // already past handshake; raw bytes after follow
	Host     string   // target host (domain or IP)
	Port     int      // target port
	Username string   // SOCKS5 username (identity hint)
}

// HostPort returns the canonical host:port string.
func (r *SOCKS5Request) HostPort() string {
	return net.JoinHostPort(r.Host, strconv.Itoa(r.Port))
}

// readSOCKS5Handshake performs the full RFC 1928 (+ optional RFC 1929)
// handshake. On success the returned request's Conn is positioned right
// before the first application byte.
//
// The handshakeDeadline applies to the whole handshake, not each step.
func readSOCKS5Handshake(conn net.Conn, handshakeDeadline time.Duration) (*SOCKS5Request, error) {
	if handshakeDeadline > 0 {
		_ = conn.SetDeadline(time.Now().Add(handshakeDeadline))
		defer conn.SetDeadline(time.Time{})
	}
	br := bufio.NewReader(conn)

	// Greeting
	header := make([]byte, 2)
	if _, err := io.ReadFull(br, header); err != nil {
		return nil, fmt.Errorf("socks5: read greeting: %w", err)
	}
	if header[0] != socks5Version {
		return nil, fmt.Errorf("socks5: unsupported version %d", header[0])
	}
	nmethods := int(header[1])
	methods := make([]byte, nmethods)
	if _, err := io.ReadFull(br, methods); err != nil {
		return nil, fmt.Errorf("socks5: read methods: %w", err)
	}

	supports := map[byte]bool{}
	for _, m := range methods {
		supports[m] = true
	}

	var username string
	switch {
	case supports[authUserPass]:
		if _, err := conn.Write([]byte{socks5Version, authUserPass}); err != nil {
			return nil, err
		}
		u, err := readUserPassAuth(br, conn)
		if err != nil {
			return nil, err
		}
		username = u
	case supports[authNone]:
		if _, err := conn.Write([]byte{socks5Version, authNone}); err != nil {
			return nil, err
		}
	default:
		_, _ = conn.Write([]byte{socks5Version, authNoneOK})
		return nil, errors.New("socks5: no acceptable auth method")
	}

	// Request
	reqHeader := make([]byte, 4)
	if _, err := io.ReadFull(br, reqHeader); err != nil {
		return nil, fmt.Errorf("socks5: read request: %w", err)
	}
	if reqHeader[0] != socks5Version {
		return nil, fmt.Errorf("socks5: bad version in request: %d", reqHeader[0])
	}
	if reqHeader[1] != cmdConnect {
		_ = writeSOCKS5Reply(conn, repCommandNotSupported)
		return nil, fmt.Errorf("socks5: unsupported command %d (only CONNECT)", reqHeader[1])
	}
	// reqHeader[2] reserved
	var host string
	switch reqHeader[3] {
	case atypIPv4:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, fmt.Errorf("socks5: read ipv4: %w", err)
		}
		host = net.IP(buf).String()
	case atypDomain:
		lenByte := make([]byte, 1)
		if _, err := io.ReadFull(br, lenByte); err != nil {
			return nil, fmt.Errorf("socks5: read domain len: %w", err)
		}
		buf := make([]byte, int(lenByte[0]))
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, fmt.Errorf("socks5: read domain: %w", err)
		}
		host = string(buf)
	case atypIPv6:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, fmt.Errorf("socks5: read ipv6: %w", err)
		}
		host = net.IP(buf).String()
	default:
		_ = writeSOCKS5Reply(conn, repAddrTypeNotSupported)
		return nil, fmt.Errorf("socks5: unsupported atyp %d", reqHeader[3])
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(br, portBuf); err != nil {
		return nil, fmt.Errorf("socks5: read port: %w", err)
	}
	port := int(binary.BigEndian.Uint16(portBuf))

	if err := writeSOCKS5Reply(conn, repSuccess); err != nil {
		return nil, err
	}

	// Re-wrap the conn so any bytes the bufio reader may have buffered
	// past the request remain readable by the caller.
	wrapped := &bufferedConn{Conn: conn, r: br}
	return &SOCKS5Request{
		Conn:     wrapped,
		Host:     host,
		Port:     port,
		Username: username,
	}, nil
}

func readUserPassAuth(br *bufio.Reader, conn net.Conn) (string, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(br, header); err != nil {
		return "", fmt.Errorf("socks5: read userpass header: %w", err)
	}
	if header[0] != authNone1929 {
		return "", fmt.Errorf("socks5: bad userpass version: %d", header[0])
	}
	ulen := int(header[1])
	uname := make([]byte, ulen)
	if _, err := io.ReadFull(br, uname); err != nil {
		return "", fmt.Errorf("socks5: read username: %w", err)
	}
	plenByte := make([]byte, 1)
	if _, err := io.ReadFull(br, plenByte); err != nil {
		return "", fmt.Errorf("socks5: read plen: %w", err)
	}
	pword := make([]byte, int(plenByte[0]))
	if _, err := io.ReadFull(br, pword); err != nil {
		return "", fmt.Errorf("socks5: read password: %w", err)
	}
	// We accept any credentials — auth is informational only (identity hint).
	if _, err := conn.Write([]byte{authNone1929, 0x00}); err != nil {
		return "", err
	}
	return string(uname), nil
}

// writeSOCKS5Reply writes a SOCKS5 CONNECT reply with BND.ADDR = 0.0.0.0:0.
func writeSOCKS5Reply(conn net.Conn, rep byte) error {
	resp := []byte{
		socks5Version, rep, 0x00,
		atypIPv4,
		0, 0, 0, 0,
		0, 0,
	}
	_, err := conn.Write(resp)
	return err
}

// bufferedConn returns any data buffered by bufio.Reader before delegating
// to the underlying conn. Required because the SOCKS5 handshake reader may
// have read past the request bytes.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}
