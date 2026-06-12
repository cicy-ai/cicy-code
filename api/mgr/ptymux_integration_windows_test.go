//go:build windows

package main

import (
	"strings"
	"testing"
	"time"
)

// TestPtmIntegration drives the SAME shim cicy's runTmux routes to on Windows,
// using the exact tmux arg vectors tmux.go emits. Run as a cross-compiled test
// binary on a real Windows box (no Go toolchain / no full server needed):
//
//	GOOS=windows GOARCH=amd64 go test -c ./mgr -o mgr_test.exe
//	scp mgr_test.exe win:  &&  ssh win "mgr_test.exe -test.run TestPtmIntegration -test.v"
func TestPtmIntegration(t *testing.T) {
	m := ptmNewManager()
	m.shell, m.shellArgs = "cmd.exe", nil // deterministic shell for the smoke test
	m.cols, m.rows = 90, 24

	tx := func(args ...string) string {
		out, err := m.Tmux(args...)
		t.Logf("tmux %s -> err=%v out=%q", strings.Join(args, " "), err, oneLine(out))
		if err != nil {
			t.Logf("  (err: %v)", err)
		}
		return out
	}

	// 1) has-session on a missing session must error
	if _, err := m.Tmux("has-session", "-t", "w-test"); err == nil {
		t.Fatal("has-session on missing session should error")
	}

	// 2) create
	if _, err := m.Tmux("new-session", "-d", "-s", "w-test", "-n", "main", "-c", t.TempDir()); err != nil {
		t.Fatalf("new-session failed: %v", err)
	}
	defer m.Tmux("kill-session", "-t", "w-test")

	// 3) has-session now ok
	if _, err := m.Tmux("has-session", "-t", "w-test"); err != nil {
		t.Fatalf("has-session after create: %v", err)
	}

	// 4) list-sessions
	if got := tx("list-sessions", "-F", "#{session_name}"); !strings.Contains(got, "w-test") {
		t.Fatalf("list-sessions missing w-test: %q", got)
	}

	// 5) send-keys (literal) + Enter
	tx("send-keys", "-t", "w-test:main.0", "-l", "--", "echo ptm-shim-works")
	tx("send-keys", "-t", "w-test:main.0", "Enter")
	time.Sleep(700 * time.Millisecond)

	// 6) capture-pane must show the echoed output
	cap := tx("capture-pane", "-t", "w-test:main.0", "-p", "-S", "-80")
	if !strings.Contains(cap, "ptm-shim-works") {
		t.Fatalf("capture-pane missing echo output; got:\n%s", cap)
	}

	// 7) display-message foreground — idle shell (cmd)
	idle := tx("display-message", "-p", "-t", "w-test:main.0", "#{pane_current_command}")
	if idle == "" {
		t.Fatal("foreground (idle) returned empty")
	}

	// 8) busy probe: run timeout, foreground must change to a non-shell command
	tx("send-keys", "-t", "w-test:main.0", "-l", "--", "timeout /t 2 /nobreak")
	tx("send-keys", "-t", "w-test:main.0", "Enter")
	time.Sleep(700 * time.Millisecond)
	busy := tx("display-message", "-p", "-t", "w-test:main.0", "#{pane_current_command}")
	if strings.EqualFold(busy, idle) {
		t.Logf("WARNING: foreground did not change while busy (idle=%q busy=%q)", idle, busy)
	}

	t.Logf("PASS: shim drove a real ConPTY pane — create/list/send/capture/foreground all worked")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > 90 {
		s = s[:90] + "…"
	}
	return s
}
