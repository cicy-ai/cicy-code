//go:build !windows

package main

import "os/exec"

// mihomoWrapperCmd runs the cicy-mihomo skill wrapper directly — on Linux and
// macOS the kernel honors its `#!/usr/bin/env node` shebang, so the
// extension-less script is executable as-is. See the windows build for why
// this indirection exists.
func mihomoWrapperCmd(wrapper string, args ...string) *exec.Cmd {
	return exec.Command(wrapper, args...)
}
