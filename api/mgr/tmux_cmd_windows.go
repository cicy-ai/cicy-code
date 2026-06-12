//go:build windows

package main

import (
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Announce the backend state once at startup — a definitive signal in the log,
// independent of whether any session-creation path (ensureTmuxServer) runs.
func init() {
	if ptmEnabled() {
		log.Printf("[ptymux] native ConPTY pty backend ENABLED — tmux calls routed in-process (force=%q env=%q)", ptmForceOn, os.Getenv("CICY_PTY_BACKEND"))
	} else {
		log.Printf("[ptymux] native backend DISABLED — using external tmux binary")
	}
}

var (
	ptmBackend *ptmManager
	ptmOnce    sync.Once
)

// ptmForceOn is empty in normal builds (committed default = OFF, safe). A test
// build can bake the backend ON without touching env via:
//   go build -ldflags "-X main.ptmForceOn=1" ./mgr/
var ptmForceOn string

// ptmEnabled reports whether the native pty backend replaces tmux on Windows.
// Default ON (this file is windows-only): the whole point on Windows is to drop
// the buggy MSYS2 tmux. Escape hatch: CICY_PTY_BACKEND=tmux|off reverts to the
// external tmux binary. (Unix/macOS never compile this file — they keep tmux.)
func ptmEnabled() bool {
	if ptmForceOn == "1" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CICY_PTY_BACKEND"))) {
	case "go", "1", "on", "ptymux", "native":
		return true
	case "tmux", "0", "off", "no", "msys", "legacy":
		return false
	}
	return true // Windows default: native ConPTY backend
}

func ptmGet() *ptmManager {
	ptmOnce.Do(func() { ptmBackend = ptmNewManager() })
	return ptmBackend
}

// nativePtyActive reports whether the native ConPTY backend is in effect — used
// by shared (non-build-tagged) code to branch the pane boot for Windows.
func nativePtyActive() bool { return ptmEnabled() }

func newTmuxCommand(args []string) tmuxRunner {
	if ptmEnabled() {
		return &ptmCmd{args: args}
	}
	return exec.Command("tmux", args...)
}

// ptmCmd adapts a tmux arg vector to the native backend behind the tmuxRunner
// interface, so call sites that do .Run()/.Output()/.CombinedOutput() work
// unchanged.
type ptmCmd struct{ args []string }

func (c *ptmCmd) run() (string, error) { return ptmGet().Tmux(c.args...) }

func (c *ptmCmd) Run() error { _, err := c.run(); return err }

func (c *ptmCmd) Output() ([]byte, error) {
	s, err := c.run()
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

func (c *ptmCmd) CombinedOutput() ([]byte, error) { return c.Output() }
