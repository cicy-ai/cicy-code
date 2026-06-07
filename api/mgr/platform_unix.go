//go:build !windows

package main

// initPlatform performs OS-specific process setup. No-op on unix.
func initPlatform() {}

// ensureTmuxServer makes sure a tmux server is reachable before session
// creation. No-op on unix — tmux auto-starts its server from any client.
func ensureTmuxServer() {}

// toPosixPath converts an OS path into the POSIX form understood by the bash
// that runs inside agent panes. Identity on unix; on Windows it rewrites
// C:\foo\bar → /c/foo/bar for the bundled MSYS2 bash (see platform_windows.go).
func toPosixPath(p string) string { return p }
