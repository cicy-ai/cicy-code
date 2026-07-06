// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package mitm

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
)

// terminateClientTLS wraps client in a tls.Server using a leaf cert signed
// by ca for the requested SNI host. ALPN is restricted to http/1.1 — h2
// support is out of scope for v1 (see design doc §1.4).
//
// host is the SOCKS5 target host; the server will prefer the actual SNI
// from ClientHello if present (to handle clients that re-resolve the host).
func terminateClientTLS(client net.Conn, ca *CA, host string) (*tls.Conn, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			sni := hello.ServerName
			if sni == "" {
				sni = host
			}
			leaf, err := ca.SignLeaf(sni)
			if err != nil {
				return nil, err
			}
			return &tls.Certificate{
				Certificate: leaf.DERChain,
				PrivateKey:  leaf.Key,
			}, nil
		},
	}
	tlsConn := tls.Server(client, tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		// Most common failure: cert pinning (client refuses our CA).
		// Surface a typed error so the caller can decide to passthrough.
		return nil, &TLSHandshakeError{Host: host, Err: err}
	}
	return tlsConn, nil
}

// TLSHandshakeError signals that the client-side TLS handshake failed,
// typically because the client pins certificates and does not trust our
// CA. Callers should record an audit event with status=mitm_skipped and
// fall back to passthrough where possible (within the current connection
// it is already too late; passthrough must be decided before terminating).
type TLSHandshakeError struct {
	Host string
	Err  error
}

func (e *TLSHandshakeError) Error() string {
	return fmt.Sprintf("mitm: client TLS handshake for %s failed: %v", e.Host, e.Err)
}

func (e *TLSHandshakeError) Unwrap() error { return e.Err }

// IsPinningError best-effort detects cert pinning vs other TLS failures.
// Useful for distinguishing audit reasons.
func IsPinningError(err error) bool {
	var hsErr *TLSHandshakeError
	if !errors.As(err, &hsErr) {
		return false
	}
	msg := hsErr.Err.Error()
	for _, marker := range []string{
		"unknown certificate",
		"bad certificate",
		"unknown ca",
		"certificate signed by unknown authority",
		"tls: unknown certificate authority",
	} {
		if containsLower(msg, marker) {
			return true
		}
	}
	return false
}

func containsLower(s, sub string) bool {
	// trivial case-insensitive contains
	if len(sub) == 0 {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a := s[i+j]
			b := sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
