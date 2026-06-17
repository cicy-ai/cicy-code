//go:build windows

// Package-local native pty multiplexer (Windows only) — replaces tmux/MSYS2.
//
// On Windows the MSYS2 tmux is buggy; this provides the exact tmux surface
// cicy uses (send-keys / capture-pane / #{pane_current_command} / sessions)
// on top of native ConPTY via github.com/aymanbagabas/go-pty, with a pure-Go
// vt100 grid (vt10x) and a terminal capability-query responder. Unix builds
// never compile this file — they keep calling real tmux unchanged.
package main

import (
	"bufio"
	"os/exec"
	"strings"
	"sync"

	pty "github.com/aymanbagabas/go-pty"
	"github.com/hinshun/vt10x"
)

// ptmSession = one pane: a child process under its own ConPTY, with a live
// vt100 screen model fed by a background read loop.
type ptmSession struct {
	name       string
	pt         pty.Pty
	cmd        *pty.Cmd
	term       vt10x.Terminal
	cols, rows int
	mu         sync.Mutex
	queryCarry []byte

	// viewers attached over the web terminal (ttyd) — the native equivalent of
	// multiple `tmux attach` clients sharing one pane.
	subs    map[int]chan []byte
	nextSub int
}

func ptmNewSession(name, shell, cwd string, env []string, cols, rows int, args ...string) (*ptmSession, error) {
	pt, err := pty.New()
	if err != nil {
		return nil, err
	}
	if err := pt.Resize(cols, rows); err != nil {
		_ = pt.Close()
		return nil, err
	}
	term := vt10x.New(vt10x.WithSize(cols, rows))

	// Absolute-resolve the shell: with Dir set, a bare name would be looked up
	// relative to Dir and fail.
	if !strings.ContainsAny(shell, `/\`) {
		if resolved, err := exec.LookPath(shell); err == nil {
			shell = resolved
		}
	}
	cmd := pt.Command(shell, args...)
	cmd.Dir = cwd
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		_ = pt.Close()
		return nil, err
	}

	s := &ptmSession{name: name, pt: pt, cmd: cmd, term: term, cols: cols, rows: rows, subs: map[int]chan []byte{}}

	go func() {
		br := bufio.NewReader(pt)
		buf := make([]byte, 4096)
		for {
			n, rerr := br.Read(buf)
			if n > 0 {
				data := make([]byte, n) // copy: buf is reused, viewers keep refs
				copy(data, buf[:n])
				s.mu.Lock()
				_, _ = s.term.Write(data)
				row, col := s.term.Cursor().Y+1, s.term.Cursor().X+1
				s.queryCarry = append(s.queryCarry, data...)
				reply, leftover := ptmDrainQueries(s.queryCarry, s.cols, s.rows, row, col)
				s.queryCarry = leftover
				for _, ch := range s.subs { // fan out to web viewers (non-blocking)
					select {
					case ch <- data:
					default: // slow viewer: drop rather than stall the read loop
					}
				}
				s.mu.Unlock()
				if len(reply) > 0 {
					_, _ = s.pt.Write(reply)
				}
			}
			if rerr != nil {
				s.mu.Lock()
				for id, ch := range s.subs {
					delete(s.subs, id)
					close(ch)
				}
				s.mu.Unlock()
				return
			}
		}
	}()
	return s, nil
}

// Subscribe attaches a web viewer: returns a subscription id, a channel of live
// pty output, and a snapshot to repaint the current screen immediately (so the
// browser shows the pane's current state, not a blank until the next redraw).
func (s *ptmSession) Subscribe() (int, chan []byte, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	ch := make(chan []byte, 512)
	s.subs[id] = ch
	return id, ch, s.snapshotLocked()
}

func (s *ptmSession) Unsubscribe(id int) {
	s.mu.Lock()
	if ch, ok := s.subs[id]; ok {
		delete(s.subs, id)
		close(ch)
	}
	s.mu.Unlock()
}

// snapshotLocked builds bytes that repaint the current screen: clear + the grid
// as text. Colors are lost on the initial paint but live output restores full
// fidelity on the next redraw. Caller holds s.mu.
func (s *ptmSession) snapshotLocked() []byte {
	out := []byte("\x1b[2J\x1b[3J\x1b[H")
	lines := strings.Split(s.term.String(), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return append(out, []byte(strings.Join(lines, "\r\n"))...)
}

// SendKeys writes literal bytes to the child's stdin == `tmux send-keys -l`.
func (s *ptmSession) SendKeys(text string) error {
	_, err := s.pt.Write([]byte(text))
	return err
}

// Capture returns the visible screen as text == `tmux capture-pane -p`.
func (s *ptmSession) Capture() string {
	s.mu.Lock()
	raw := s.term.String()
	s.mu.Unlock()
	lines := strings.Split(raw, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// Foreground == `tmux display-message -p '#{pane_current_command}'`.
func (s *ptmSession) Foreground() string { return ptmForeground(s.pt, s.cmd) }

// Pid returns the child's pid == `#{pane_pid}`.
func (s *ptmSession) Pid() int {
	if s.cmd != nil && s.cmd.Process != nil {
		return s.cmd.Process.Pid
	}
	return 0
}

func (s *ptmSession) Resize(cols, rows int) error {
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.term.Resize(cols, rows)
	s.mu.Unlock()
	return s.pt.Resize(cols, rows)
}

func (s *ptmSession) Close() error {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return s.pt.Close()
}
