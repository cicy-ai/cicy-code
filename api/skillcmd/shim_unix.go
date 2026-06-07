//go:build !windows

package skillcmd

// ensureCmdShim generates a Windows .cmd shim next to the bin link so native
// (non-msys) spawns can execute shebang scripts. No-op on unix — the symlink
// plus shebang is already executable everywhere.
func ensureCmdShim(src, target string) error { return nil }
