//go:build windows

// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

// mihomoWrapperCmd builds the command that invokes the cicy-mihomo skill
// wrapper (~/.local/bin/cicy-mihomo). That wrapper is an extension-less
// `#!/usr/bin/env node` script: on Linux/macOS the kernel honors the shebang,
// but native Windows CreateProcess cannot execute a bare script with no
// .exe/.cmd, so exec.Command(wrapper, ...) fails and — because the mihomo
// startup path is fail-open — the proxy silently never comes up.
//
// Fix: run the wrapper THROUGH node explicitly (`node <wrapper> <args...>`),
// which works regardless of the shebang. node is resolved from the bundled
// MSYS2 runtime first (the intended layout ships it under usr\local\bin), then
// from PATH. If no node is found we fall back to the bare wrapper so the
// caller's existing error logging still fires instead of us masking it.
func mihomoWrapperCmd(wrapper string, args ...string) *exec.Cmd {
	node := findBundledNode()
	if node == "" {
		return exec.Command(wrapper, args...)
	}
	return exec.Command(node, append([]string{wrapper}, args...)...)
}

// findBundledNode locates node.exe, preferring the MSYS2 runtime over PATH so
// the resolution is stable even when the process PATH is incomplete.
func findBundledNode() string {
	if root := msysRoot(); root != "" {
		for _, rel := range [][]string{
			{"usr", "local", "bin", "node.exe"},
			{"usr", "bin", "node.exe"},
			{"mingw64", "bin", "node.exe"},
		} {
			p := filepath.Join(append([]string{root}, rel...)...)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	if p, err := exec.LookPath("node"); err == nil {
		return p
	}
	return ""
}
