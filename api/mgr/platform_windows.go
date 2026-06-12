//go:build windows

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Windows runs agent orchestration on a bundled MSYS2 runtime (bash + tmux +
// coreutils). cicy-desktop unpacks it and points CICY_MSYS_ROOT at the root;
// standalone installs fall back to the conventional locations. We prepend
// <root>\usr\bin to this process's PATH once at boot so every existing
// exec.Command("tmux"/"sh"/"bash"/"curl"/...) call site resolves to the MSYS2
// binaries with zero changes.

// msysRoot locates the MSYS2 root. Resolution order:
//  1. CICY_MSYS_ROOT (set by the cicy-desktop sidecar — the bundled runtime)
//  2. msys64/ next to the executable (portable zip layout)
//  3. C:\tools\msys64 (chocolatey), C:\msys64 (official installer)
func msysRoot() string {
	probe := func(root string) string {
		if root == "" {
			return ""
		}
		if _, err := os.Stat(filepath.Join(root, "usr", "bin", "bash.exe")); err == nil {
			return root
		}
		return ""
	}
	if r := probe(strings.TrimSpace(os.Getenv("CICY_MSYS_ROOT"))); r != "" {
		return r
	}
	if exe, err := os.Executable(); err == nil {
		if r := probe(filepath.Join(filepath.Dir(exe), "msys64")); r != "" {
			return r
		}
	}
	for _, root := range []string{`C:\tools\msys64`, `C:\msys64`} {
		if r := probe(root); r != "" {
			return r
		}
	}
	return ""
}

func initPlatform() {
	root := msysRoot()
	if root == "" {
		log.Printf("[platform] WARNING: no MSYS2 runtime found (set CICY_MSYS_ROOT or install to C:\\tools\\msys64) — tmux orchestration unavailable")
		return
	}
	// The slim MSYS2 bundle can ship WITHOUT /tmp. msys bash then warns "could
	// not find /tmp, please create!" and — the real killer — tmux can't create
	// its socket (/tmp/tmux-<uid>/default), so `tmux new-session` fails with "no
	// suitable socket path", the server never starts, and NO pane ever exists
	// (capture-pane / #{pane_current_command} come back empty because there is
	// literally no pane). Create it (idempotent) before anything spawns
	// bash/tmux. Verified on a real box: mkdir <root>\tmp → new-session OK.
	tmpDir := filepath.Join(root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o777); err != nil {
		log.Printf("[platform] WARNING: could not create %s (tmux socket will fail): %v", tmpDir, err)
	}
	// usr\bin = msys core (bash/tmux/coreutils); mingw64\bin = native-built
	// extras (e.g. jq). Prepend both so LookPath resolves either flavor.
	usrBin := filepath.Join(root, "usr", "bin")
	mingwBin := filepath.Join(root, "mingw64", "bin")
	prefix := usrBin + string(os.PathListSeparator)
	if _, err := os.Stat(mingwBin); err == nil {
		prefix += mingwBin + string(os.PathListSeparator)
	}
	// ~/.local/bin holds skill bins/shims (ensureBinSymlink). msys login
	// shells don't add it on Windows, so put it on the inherited PATH here.
	if home, err := os.UserHomeDir(); err == nil {
		prefix += filepath.Join(home, ".local", "bin") + string(os.PathListSeparator)
	}
	path := os.Getenv("PATH")
	if !strings.HasPrefix(path, prefix) {
		os.Setenv("PATH", prefix+path)
	}
	// CHERE_INVOKING: msys bash login shells stay in the invoking cwd instead
	// of cd-ing to the msys home — pane working dirs must win.
	os.Setenv("CHERE_INVOKING", "1")
	// inherit: /etc/profile keeps the parent PATH instead of rebuilding it —
	// without this, pane login shells lose our prepends (and node/jq/etc.,
	// which only exist on the Windows side of PATH).
	os.Setenv("MSYS2_PATH_TYPE", "inherit")
	// The msys runtime re-parses argv of msys programs spawned by NATIVE
	// Windows parents — including glob/brace expansion. That turns tmux format
	// strings like `#{pane_current_command}` into `#pane_current_command` and
	// would mangle any send-keys payload containing {}, *, ?. noglob disables
	// this re-parse mangling.
	if msys := os.Getenv("MSYS"); msys == "" {
		os.Setenv("MSYS", "noglob")
	} else if !strings.Contains(msys, "noglob") {
		os.Setenv("MSYS", msys+" noglob")
	}
	if os.Getenv("MSYSTEM") == "" {
		os.Setenv("MSYSTEM", "MSYS")
	}
	// Converge $HOME for every msys child (tmux server → panes → ssh-keygen,
	// agent CLIs): msys defaults to <msys64>\home\<user>, but the Windows side
	// (cicy-ssh, System32 OpenSSH, Claude Code) lives in %USERPROFILE%. POSIX
	// form so ~-expansion composes cleanly inside bash.
	if up := os.Getenv("USERPROFILE"); up != "" {
		os.Setenv("HOME", toPosixPath(up))
	}
	log.Printf("[platform] MSYS2 runtime: %s (usr\\bin prepended to PATH)", root)
}

// ensureTmuxServer makes sure a tmux server is reachable. On Windows the msys
// tmux SERVER needs a tty to start: clients spawned by a console-less native
// parent (this server, a sidecar, sshd-without-pty) hang instead of
// auto-starting it. Trick (verified on a real box): spawn the bootstrap
// client with CREATE_NO_WINDOW — Windows allocates a hidden console, the msys
// runtime maps it to a tty, the forked server then persists on its own.
// Client commands (ls/send-keys/capture/new-session against a live server)
// need no tty, so everything after this works from the plain service process.
func ensureTmuxServer() {
	if ptmEnabled() {
		// Native ConPTY backend replaces tmux entirely — no MSYS2 tmux server
		// to bootstrap. (initPlatform still sets up MSYS2 bash on PATH, which
		// the agent panes use as their shell.)
		log.Printf("[platform] CICY_PTY_BACKEND on — native pty backend, skipping tmux server bootstrap")
		return
	}
	if exec.Command("tmux", "has-session").Run() == nil {
		setTmuxDefaultShellWindows() // idempotent — also fix an already-up server
		return                       // server already up (with at least one session)
	}
	// Anchor session: a tmux server with zero sessions exits, so keep one
	// throwaway session alive as the server anchor.
	boot := exec.Command("tmux", "new-session", "-d", "-s", "cicy-boot")
	boot.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if err := boot.Run(); err != nil {
		log.Printf("[platform] tmux server bootstrap failed: %v", err)
		return
	}
	for i := 0; i < 20; i++ {
		if exec.Command("tmux", "has-session").Run() == nil {
			setTmuxDefaultShellWindows()
			log.Printf("[platform] tmux server bootstrapped (anchor session cicy-boot)")
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	log.Printf("[platform] tmux server bootstrap: server not confirmed within 5s")
}

// setTmuxDefaultShellWindows forces new panes onto the msys bash LOGIN shell.
// Nothing in the tmux config ships `set -g default-shell`, so on Windows tmux
// falls back to $SHELL/cmd.exe — every pane becomes a Windows command prompt
// instead of the msys bash the agents and boot.sh require (capture never shows a
// $/%/# prompt, boot.sh never sources, CLI never installs). Set it on the global
// option so every subsequent new-session (the real agent panes) inherits it.
// POSIX path: tmux is an msys program and execs the shell inside the msys
// namespace, where /usr/bin/bash resolves to <root>\usr\bin\bash.exe. Leaving
// default-command empty makes tmux run it as a login shell (sources
// /etc/profile → ~/.bash_profile → ~/.cicy_tmux.conf).
func setTmuxDefaultShellWindows() {
	if err := exec.Command("tmux", "set-option", "-g", "default-shell", "/usr/bin/bash").Run(); err != nil {
		log.Printf("[platform] set default-shell=/usr/bin/bash failed: %v", err)
	}
}

// (Windows MITM-CA OS-trust install moved to package mitm: trust_windows.go,
// shared by the install-ca CLI and the /api/mitm/consent server endpoint.)

// toPosixPath rewrites a Windows path into the POSIX form the MSYS2 bash
// inside panes understands: C:\Users\x → /c/Users/x. Forward slashes are
// normalized; non-drive paths just get slash-normalized (msys accepts
// C:/Users/x too, but /c/... survives quoting and concatenation better).
func toPosixPath(p string) string {
	if p == "" {
		return p
	}
	s := filepath.ToSlash(p)
	if len(s) >= 2 && s[1] == ':' &&
		((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) {
		drive := strings.ToLower(string(s[0]))
		rest := strings.TrimPrefix(s[2:], "/")
		if rest == "" {
			return "/" + drive
		}
		return "/" + drive + "/" + rest
	}
	return s
}
