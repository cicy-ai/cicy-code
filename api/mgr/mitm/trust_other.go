//go:build !windows

// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package mitm

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
)

// These are the Windows OS-trust entry points (see trust_windows.go). On unix
// the system-trust path goes through update-ca-certificates / the keychain in
// cli.go's installSystem, so these are unused stubs kept for cross-compilation.

func InstallRootCA(certBytes []byte) error {
	return fmt.Errorf("InstallRootCA is Windows-only")
}

func RemoveRootCA(certBytes []byte) error { return nil }

func RootCATrusted(certBytes []byte) bool { return false }

func CertThumbprint(certBytes []byte) string {
	der := certBytes
	if block, _ := pem.Decode(certBytes); block != nil {
		der = block.Bytes
	}
	if len(der) == 0 {
		return ""
	}
	sum := sha1.Sum(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}
