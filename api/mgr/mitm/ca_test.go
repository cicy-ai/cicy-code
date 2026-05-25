package mitm

import (
	"crypto/x509"
	"path/filepath"
	"testing"
)

func newTestCA(t *testing.T) *CA {
	t.Helper()
	dir := t.TempDir()
	cfg := CAConfig{
		CertPath:       filepath.Join(dir, "ca.crt"),
		KeyPath:        filepath.Join(dir, "ca.key"),
		LeafCacheSize:  4,
		LeafValidYears: 1,
	}
	ca, err := LoadOrCreateCA(cfg)
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}
	return ca
}

func TestCA_RootGenerationIsValid(t *testing.T) {
	ca := newTestCA(t)
	if ca.rootCert == nil || ca.rootKey == nil || len(ca.rootDER) == 0 {
		t.Fatal("CA missing components")
	}
	if !ca.rootCert.IsCA {
		t.Fatal("root cert IsCA should be true")
	}
	if !ca.rootCert.BasicConstraintsValid {
		t.Fatal("BasicConstraintsValid should be true")
	}
}

func TestCA_PersistAndReload(t *testing.T) {
	dir := t.TempDir()
	cfg := CAConfig{
		CertPath:       filepath.Join(dir, "ca.crt"),
		KeyPath:        filepath.Join(dir, "ca.key"),
		LeafCacheSize:  4,
		LeafValidYears: 1,
	}
	first, err := LoadOrCreateCA(cfg)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	second, err := LoadOrCreateCA(cfg)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if first.rootCert.SerialNumber.Cmp(second.rootCert.SerialNumber) != 0 {
		t.Fatal("root serial changed across reload — persistence broken")
	}
}

func TestCA_LeafSigning(t *testing.T) {
	ca := newTestCA(t)
	cert, err := ca.SignLeaf("api.anthropic.com")
	if err != nil {
		t.Fatalf("SignLeaf: %v", err)
	}
	if len(cert.DERChain) != 2 {
		t.Fatalf("expected 2 certs in chain (leaf + root), got %d", len(cert.DERChain))
	}
	parsed, err := x509.ParseCertificate(cert.DERChain[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if parsed.Subject.CommonName != "api.anthropic.com" {
		t.Fatalf("CN = %q, want api.anthropic.com", parsed.Subject.CommonName)
	}
	foundExact := false
	foundWildcard := false
	for _, name := range parsed.DNSNames {
		if name == "api.anthropic.com" {
			foundExact = true
		}
		if name == "*.anthropic.com" {
			foundWildcard = true
		}
	}
	if !foundExact {
		t.Errorf("DNSNames missing exact host: %v", parsed.DNSNames)
	}
	if !foundWildcard {
		t.Errorf("DNSNames missing wildcard: %v", parsed.DNSNames)
	}
}

func TestCA_LeafCacheReuse(t *testing.T) {
	ca := newTestCA(t)
	a, _ := ca.SignLeaf("api.anthropic.com")
	b, _ := ca.SignLeaf("api.anthropic.com")
	if a != b {
		t.Fatal("leaf cache miss for repeat host")
	}
}

func TestCA_LeafCacheEvicts(t *testing.T) {
	ca := newTestCA(t)
	ca.cap = 2 // shrink for the test

	c1, _ := ca.SignLeaf("a.test")
	_, _ = ca.SignLeaf("b.test")
	_, _ = ca.SignLeaf("c.test") // should evict a.test (oldest)

	if len(ca.cache) != 2 {
		t.Fatalf("cache size = %d, want 2", len(ca.cache))
	}
	c1Again, _ := ca.SignLeaf("a.test")
	if c1 == c1Again {
		t.Fatal("a.test should have been re-signed after eviction")
	}
}

func TestCA_LeafForIPAddress(t *testing.T) {
	ca := newTestCA(t)
	cert, err := ca.SignLeaf("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := x509.ParseCertificate(cert.DERChain[0])
	if len(parsed.IPAddresses) == 0 {
		t.Fatal("IP cert should carry IPAddresses, not DNSNames")
	}
}

func TestCA_RootCertPEMRoundtrip(t *testing.T) {
	ca := newTestCA(t)
	pemBytes := ca.RootCertPEM()
	if len(pemBytes) == 0 {
		t.Fatal("RootCertPEM returned empty")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		t.Fatal("AppendCertsFromPEM failed")
	}
}
