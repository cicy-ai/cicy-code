// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package mitm

import (
	"container/list"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CA owns the root certificate + private key and an LRU cache of leaf
// certificates signed for individual hosts.
//
// CA is goroutine-safe: SignLeaf may be called concurrently.
type CA struct {
	rootCert *x509.Certificate
	rootKey  *ecdsa.PrivateKey
	rootDER  []byte // raw DER, used as Certificate.Certificate entry

	leafKey *ecdsa.PrivateKey // single key shared across all leaves

	mu    sync.Mutex
	cache map[string]*list.Element // host → list element (lru order)
	lru   *list.List               // front = most recent
	cap   int

	leafValidity time.Duration
}

// leafEntry is stored in the LRU list.
type leafEntry struct {
	host string
	cert *Certificate
}

// Certificate is the cached pair (DER chain + key) used by tls.Config.
// We re-build tls.Certificate on demand to avoid sharing mutable state.
type Certificate struct {
	DERChain [][]byte
	Key      *ecdsa.PrivateKey
}

// LoadOrCreateCA reads the root cert + key from disk, or generates a new
// root on first run. Files are written with mode 0600.
func LoadOrCreateCA(cfg CAConfig) (*CA, error) {
	ca := &CA{
		cache:        map[string]*list.Element{},
		lru:          list.New(),
		cap:          cfg.LeafCacheSize,
		leafValidity: time.Duration(cfg.LeafValidYears) * 365 * 24 * time.Hour,
	}

	rootCert, rootKey, rootDER, err := loadRoot(cfg.CertPath, cfg.KeyPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		rootCert, rootKey, rootDER, err = generateRoot()
		if err != nil {
			return nil, fmt.Errorf("mitm: generate root CA: %w", err)
		}
		if err := writeRoot(cfg.CertPath, cfg.KeyPath, rootCert, rootKey); err != nil {
			return nil, err
		}
	}
	ca.rootCert = rootCert
	ca.rootKey = rootKey
	ca.rootDER = rootDER

	// Leaf key is shared (saves ~5ms per new host vs generating fresh).
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("mitm: gen leaf key: %w", err)
	}
	ca.leafKey = leafKey

	return ca, nil
}

// RootCertPEM returns the root certificate in PEM form (for install-ca).
func (c *CA) RootCertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: c.rootDER,
	})
}

// SignLeaf returns a cached or freshly-signed leaf certificate for host.
// host may be an exact name ("api.anthropic.com") or be empty (SNI absent),
// in which case the resulting cert is bound to "unknown".
func (c *CA) SignLeaf(host string) (*Certificate, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		host = "unknown"
	}

	c.mu.Lock()
	if el, ok := c.cache[host]; ok {
		c.lru.MoveToFront(el)
		cert := el.Value.(*leafEntry).cert
		c.mu.Unlock()
		return cert, nil
	}
	c.mu.Unlock()

	cert, err := c.signNew(host)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.cache[host]; ok { // lost a race; reuse winner
		c.lru.MoveToFront(el)
		return el.Value.(*leafEntry).cert, nil
	}
	el := c.lru.PushFront(&leafEntry{host: host, cert: cert})
	c.cache[host] = el
	for c.lru.Len() > c.cap {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		c.lru.Remove(oldest)
		delete(c.cache, oldest.Value.(*leafEntry).host)
	}
	return cert, nil
}

func (c *CA) signNew(host string) (*Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(c.leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
		// Add wildcard for one-level subdomain ("api.anthropic.com" → "*.anthropic.com").
		if parts := strings.SplitN(host, ".", 2); len(parts) == 2 && !strings.HasPrefix(host, "*.") {
			tmpl.DNSNames = append(tmpl.DNSNames, "*."+parts[1])
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.rootCert, &c.leafKey.PublicKey, c.rootKey)
	if err != nil {
		return nil, fmt.Errorf("mitm: sign leaf for %s: %w", host, err)
	}
	return &Certificate{
		DERChain: [][]byte{der, c.rootDER},
		Key:      c.leafKey,
	}, nil
}

// --- root cert load / generate / persist ---

func loadRoot(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, nil, err
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, nil, nil, fmt.Errorf("mitm: %s is not a CERTIFICATE PEM", certPath)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("mitm: parse cert: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, nil, fmt.Errorf("mitm: %s is not a PEM key", keyPath)
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("mitm: parse ec key: %w", err)
	}
	return cert, key, certBlock.Bytes, nil
}

func generateRoot() (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, err
	}
	hostname, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("cicy-mitm CA %s %s", hostname, time.Now().UTC().Format("2006-01-02")),
			Organization: []string{"cicy-ai"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		SubjectKeyId:          subjectKeyID(&key.PublicKey),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, err
	}
	return cert, key, der, nil
}

func writeRoot(certPath, keyPath string, cert *x509.Certificate, key *ecdsa.PrivateKey) error {
	if err := os.MkdirAll(filepath.Dir(certPath), 0700); err != nil {
		return fmt.Errorf("mitm: mkdir cert dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return fmt.Errorf("mitm: mkdir key dir: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return fmt.Errorf("mitm: write cert: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("mitm: write key: %w", err)
	}
	return nil
}

// subjectKeyID computes the SHA-1 hash of the public key per RFC 5280.
func subjectKeyID(pub *ecdsa.PublicKey) []byte {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil
	}
	h := sha256.Sum256(der)
	return h[:20]
}
