//go:build windows

// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package localcommand

import (
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sys/windows"
)

const (
	DefaultCloseSignal  = syscall.SIGINT // semantic only — Windows close is console termination
	DefaultCloseTimeout = 10 * time.Second
)

// LocalCommand on Windows drives the child under a ConPTY pseudo-console
// (vendored implementation, conpty_windows.go). The msys2 runtime (tmux
// attach) treats the ConPTY as a tty, so webtty sees the same byte stream as
// on unix.
//
// Teardown is strictly ordered — this is load-bearing, see conpty_windows.go:
//  1. ClosePty() once: terminates the console; pending reads drain (EOF),
//     the reaper's Wait observes process exit.
//  2. The reaper, after Wait returns AND all in-flight Read/Write calls have
//     left (ioWG), calls ReleaseHandles() exactly once.
type LocalCommand struct {
	command string
	argv    []string

	closeSignal  syscall.Signal
	closeTimeout time.Duration

	cpty      *ConPty
	ptyClosed chan struct{}
	closing   atomic.Bool
	closeOnce sync.Once
	ioWG      sync.WaitGroup
}

func New(command string, argv []string, options ...Option) (*LocalCommand, error) {
	// Same UTF-8/TERM environment as the unix path: tmux derives the attach
	// client's utf8 flag from LC_*/LANG, and needs a sane TERM.
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		// Never leak the host's own tmux identity into the child: cicy-code
		// itself may run inside a tmux pane, and `tmux attach` refuses to
		// nest when $TMUX is set.
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"TERM=xterm-256color",
		"LANG=en_US.UTF-8",
		"LC_ALL=en_US.UTF-8",
	)
	cmdLine := windows.ComposeCommandLine(append([]string{command}, argv...))
	cpty, err := startConPty(cmdLine,
		ConPtyDimensions(120, 32),
		ConPtyEnv(env),
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to start command `%s` under ConPTY", command)
	}

	lcmd := &LocalCommand{
		command:      command,
		argv:         argv,
		closeSignal:  DefaultCloseSignal,
		closeTimeout: DefaultCloseTimeout,
		cpty:         cpty,
		ptyClosed:    make(chan struct{}),
	}
	for _, option := range options {
		option(lcmd)
	}

	// Reaper: sole owner of ReleaseHandles. Runs strictly after the process
	// is gone and no Read/Write is in flight.
	go func() {
		_, _ = lcmd.cpty.Wait(contextBackground())
		lcmd.closing.Store(true)
		lcmd.ioWG.Wait()
		lcmd.cpty.ReleaseHandles()
		close(lcmd.ptyClosed)
	}()

	return lcmd, nil
}

func (lcmd *LocalCommand) Read(p []byte) (n int, err error) {
	if lcmd.closing.Load() {
		return 0, io.EOF
	}
	lcmd.ioWG.Add(1)
	defer lcmd.ioWG.Done()
	return lcmd.cpty.Read(p)
}

func (lcmd *LocalCommand) Write(p []byte) (n int, err error) {
	if lcmd.closing.Load() {
		return 0, io.ErrClosedPipe
	}
	lcmd.ioWG.Add(1)
	defer lcmd.ioWG.Done()
	return lcmd.cpty.Write(p)
}

func (lcmd *LocalCommand) Close() error {
	// Terminate the console only — NEVER the handles (the reaper owns those).
	// The attached tmux client detaches/dies; the tmux SERVER is unaffected.
	lcmd.closeOnce.Do(func() { lcmd.cpty.ClosePty() })
	select {
	case <-lcmd.ptyClosed:
	case <-lcmd.closeTimeoutC():
	}
	return nil
}

func (lcmd *LocalCommand) WindowTitleVariables() map[string]interface{} {
	return map[string]interface{}{
		"command": lcmd.command,
		"argv":    lcmd.argv,
		"pid":     lcmd.cpty.Pid(),
	}
}

func (lcmd *LocalCommand) ResizeTerminal(width int, height int) error {
	if lcmd.closing.Load() {
		return nil
	}
	lcmd.ioWG.Add(1)
	defer lcmd.ioWG.Done()
	return lcmd.cpty.Resize(width, height)
}

func (lcmd *LocalCommand) closeTimeoutC() <-chan time.Time {
	if lcmd.closeTimeout >= 0 {
		return time.After(lcmd.closeTimeout)
	}
	return make(chan time.Time)
}
