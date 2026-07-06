//go:build windows

// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package mitm

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OS-trust install for the MITM root CA on Windows, via the CryptoAPI directly
// (the same call .NET X509Store.Add makes) — verified on a real box to be
// silent, persistent, and chain-validating when the process is elevated. We do
// NOT shell certutil: its -addstore did not persist reliably in session 0.
//
// Target is LocalMachine\ROOT (machine-wide Trusted Root CAs), which schannel
// consults for Rust TLS clients (codex / kiro-cli). Adding a trusted root there
// requires an elevated process; a running non-elevated process cannot elevate in
// place, so callers should fall back to an elevated relaunch (see runElevatedSelf).

// InstallRootCA adds the PEM (or DER) cert to LocalMachine\ROOT. Idempotent:
// returns nil if already present. Returns an error starting with "need_elevation"
// when the process lacks elevation.
func InstallRootCA(certBytes []byte) error {
	der := toDER(certBytes)
	if len(der) == 0 {
		return fmt.Errorf("empty CA cert")
	}
	if rootStoreHas(der) {
		return nil
	}
	if !isElevated() {
		return fmt.Errorf("need_elevation: LocalMachine\\ROOT requires an elevated process")
	}
	store, err := openRootStore()
	if err != nil {
		return fmt.Errorf("CertOpenStore(LocalMachine\\ROOT): %w", err)
	}
	defer windows.CertCloseStore(store, 0)
	ctx, err := windows.CertCreateCertificateContext(windows.X509_ASN_ENCODING, &der[0], uint32(len(der)))
	if err != nil {
		return fmt.Errorf("CertCreateCertificateContext: %w", err)
	}
	defer windows.CertFreeCertificateContext(ctx)
	if err := windows.CertAddCertificateContextToStore(store, ctx, windows.CERT_STORE_ADD_REPLACE_EXISTING, nil); err != nil {
		return fmt.Errorf("CertAddCertificateContextToStore: %w", err)
	}
	return nil
}

// RemoveRootCA deletes the cert (matched by DER equality) from LocalMachine\ROOT.
// Idempotent. Needs elevation to actually delete.
func RemoveRootCA(certBytes []byte) error {
	der := toDER(certBytes)
	if len(der) == 0 {
		return nil
	}
	store, err := openRootStore()
	if err != nil {
		return fmt.Errorf("CertOpenStore(LocalMachine\\ROOT): %w", err)
	}
	defer windows.CertCloseStore(store, 0)
	var prev *windows.CertContext
	for {
		ctx, err := windows.CertEnumCertificatesInStore(store, prev)
		if err != nil || ctx == nil {
			return nil
		}
		cur := unsafe.Slice(ctx.EncodedCert, int(ctx.Length))
		if bytes.Equal(cur, der) {
			// CertDeleteCertificateFromStore frees the context it's given, so hand
			// it a duplicate (the enum still owns ctx). We return right after.
			if err := windows.CertDeleteCertificateFromStore(windows.CertDuplicateCertificateContext(ctx)); err != nil {
				return fmt.Errorf("CertDeleteCertificateFromStore: %w", err)
			}
			return nil
		}
		prev = ctx
	}
}

// RootCATrusted reports whether the cert is present in LocalMachine\ROOT.
func RootCATrusted(certBytes []byte) bool {
	der := toDER(certBytes)
	if len(der) == 0 {
		return false
	}
	return rootStoreHas(der)
}

// CertThumbprint returns the uppercase SHA1 hex of the cert (for logging).
func CertThumbprint(certBytes []byte) string {
	der := toDER(certBytes)
	if len(der) == 0 {
		return ""
	}
	sum := sha1.Sum(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func toDER(b []byte) []byte {
	if block, _ := pem.Decode(b); block != nil {
		return block.Bytes
	}
	return b
}

func openRootStore() (windows.Handle, error) {
	rootW, err := windows.UTF16PtrFromString("ROOT")
	if err != nil {
		return 0, err
	}
	return windows.CertOpenStore(
		uintptr(windows.CERT_STORE_PROV_SYSTEM),
		0, 0,
		windows.CERT_SYSTEM_STORE_LOCAL_MACHINE,
		uintptr(unsafe.Pointer(rootW)),
	)
}

func rootStoreHas(der []byte) bool {
	store, err := openRootStore()
	if err != nil {
		return false
	}
	defer windows.CertCloseStore(store, 0)
	var prev *windows.CertContext
	for {
		ctx, err := windows.CertEnumCertificatesInStore(store, prev)
		if err != nil || ctx == nil {
			return false
		}
		cur := unsafe.Slice(ctx.EncodedCert, int(ctx.Length))
		if bytes.Equal(cur, der) {
			return true
		}
		prev = ctx
	}
}
