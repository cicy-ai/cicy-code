//go:build windows

package skillcmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ensureCmdShim writes an npm-style <target>.cmd shim so NATIVE Windows
// spawns (cmd/powershell/node child_process) can run a shebang script bin.
// msys bash handles the shebang symlink natively; the shim is the bridge for
// everything outside the msys world (e.g. skill→skill node spawns, decided
// with w-10029 2026-06-07). Interpreter is picked from the script's shebang:
// node for `env node`, bash for sh/bash, bash as the POSIX-script default.
func ensureCmdShim(src, target string) error {
	interp := "bash"
	if f, err := os.Open(src); err == nil {
		line, _ := bufio.NewReader(f).ReadString('\n')
		f.Close()
		if strings.HasPrefix(line, "#!") {
			if strings.Contains(line, "node") {
				interp = "node"
			}
		} else {
			// No shebang → likely a native binary; no shim needed.
			return nil
		}
	}
	shim := fmt.Sprintf("@ECHO off\r\n%s \"%s\" %%*\r\n", interp, src)
	return os.WriteFile(target+".cmd", []byte(shim), 0755)
}
