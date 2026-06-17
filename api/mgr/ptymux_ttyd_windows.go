//go:build windows

package main

import (
	"fmt"
	"io"

	"ttyd-go/server"
)

// ptmTTYSlave bridges a ttyd web-terminal session to a live native pty pane —
// the native equivalent of `tmux attach`. It streams the pane's output to the
// browser (via a fan-out subscription) and forwards browser keystrokes back to
// the pty. Multiple browsers can attach to the same pane concurrently.
type ptmTTYSlave struct {
	s     *ptmSession
	subID int
	ch    chan []byte
	rbuf  []byte // pending bytes (snapshot first, then streamed chunks)
}

func (t *ptmTTYSlave) Read(p []byte) (int, error) {
	if len(t.rbuf) == 0 {
		data, ok := <-t.ch
		if !ok {
			return 0, io.EOF // session ended / unsubscribed
		}
		t.rbuf = data
	}
	n := copy(p, t.rbuf)
	t.rbuf = t.rbuf[n:]
	return n, nil
}

func (t *ptmTTYSlave) Write(p []byte) (int, error) {
	if err := t.s.SendKeys(string(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *ptmTTYSlave) ResizeTerminal(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return t.s.Resize(cols, rows)
}

func (t *ptmTTYSlave) WindowTitleVariables() map[string]interface{} {
	return map[string]interface{}{"command": "ptymux", "pane": t.s.name}
}

func (t *ptmTTYSlave) Close() error {
	t.s.Unsubscribe(t.subID)
	return nil
}

// ptmTTYFactory produces ptmTTYSlave instances for a given pane target.
type ptmTTYFactory struct{ target string }

func (f *ptmTTYFactory) Name() string { return "ptymux" }

func (f *ptmTTYFactory) New(params map[string][]string) (server.Slave, error) {
	sess, ok := ptmGet().get(f.target)
	if !ok {
		return nil, fmt.Errorf("ptymux: no session for %q", f.target)
	}
	id, ch, snapshot := sess.Subscribe()
	return &ptmTTYSlave{s: sess, subID: id, ch: ch, rbuf: snapshot}, nil
}

// ptmTTYFactoryFor returns a web-terminal factory backed by the native pty
// when the backend is on and a session exists for target; else (nil,false) so
// serveTTY falls back to `tmux attach`.
func ptmTTYFactoryFor(target string) (server.Factory, bool) {
	if !ptmEnabled() {
		return nil, false
	}
	if _, ok := ptmGet().get(target); !ok {
		return nil, false
	}
	return &ptmTTYFactory{target: target}, true
}
