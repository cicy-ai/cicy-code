//go:build windows

// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package mitm

import "golang.org/x/sys/windows"

// isElevated reports whether this process runs with an elevated (High
// integrity) token — i.e. can write LocalMachine\ROOT without a UAC prompt.
func isElevated() bool {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &tok); err != nil {
		return false
	}
	defer tok.Close()
	return tok.IsElevated()
}
