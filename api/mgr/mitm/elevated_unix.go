//go:build !windows

package mitm

import "os"

// isElevated reports whether this process runs as root (euid 0) — i.e. can
// write the system trust store / keychain without a privilege prompt.
func isElevated() bool { return os.Geteuid() == 0 }
