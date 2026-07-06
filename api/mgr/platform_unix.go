//go:build !windows

// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
)

// initPlatform performs OS-specific process setup.
func initPlatform() {
	// The daemon is often spawned with a curated PATH — the Electron desktop
	// sidecar, for instance, omits /usr/sbin. That breaks exec() of system tools
	// that live there: sysctl + iostat (used by the system-resource sampler) are
	// in /usr/sbin on macOS, so the CPU/mem panel silently reads all-zeros. Make
	// sure the standard system bin dirs are always on PATH.
	ensureSystemPathDirs()
}

// ensureSystemPathDirs appends the canonical Unix system bin dirs to PATH if
// missing, so LookPath resolves sysctl/iostat/vm_stat/df/etc. regardless of how
// (or by what launcher) the process was started.
func ensureSystemPathDirs() {
	cur := os.Getenv("PATH")
	have := map[string]bool{}
	for _, p := range filepath.SplitList(cur) {
		have[p] = true
	}
	var add []string
	for _, d := range []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		if !have[d] {
			add = append(add, d)
		}
	}
	if len(add) == 0 {
		return
	}
	sep := string(os.PathListSeparator)
	if cur != "" {
		cur += sep
	}
	_ = os.Setenv("PATH", cur+strings.Join(add, sep))
}

// ensureTmuxServer makes sure a tmux server is reachable before session
// creation. No-op on unix — tmux auto-starts its server from any client.
func ensureTmuxServer() {}

// toPosixPath converts an OS path into the POSIX form understood by the bash
// that runs inside agent panes. Identity on unix; on Windows it rewrites
// C:\foo\bar → /c/foo/bar for the bundled MSYS2 bash (see platform_windows.go).
func toPosixPath(p string) string { return p }
