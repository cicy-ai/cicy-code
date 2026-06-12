//go:build windows

package main

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

var (
	ptmBackend *ptmManager
	ptmOnce    sync.Once
)

// ptmEnabled reports whether the native pty backend replaces tmux on Windows.
// Default OFF: a normal release behaves exactly as before (real MSYS2 tmux).
// Set CICY_PTY_BACKEND=go|1|on to switch to the ConPTY backend.
func ptmEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CICY_PTY_BACKEND"))) {
	case "go", "1", "on", "ptymux", "native":
		return true
	}
	return false
}

func ptmGet() *ptmManager {
	ptmOnce.Do(func() { ptmBackend = ptmNewManager() })
	return ptmBackend
}

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
