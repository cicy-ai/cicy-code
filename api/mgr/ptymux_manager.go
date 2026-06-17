//go:build windows

package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ptmManager is the in-process session registry — the tmux SERVER replacement.
// Sessions live exactly as long as the cicy-code process ("cicy-code 在,它就在").
type ptmManager struct {
	mu        sync.Mutex
	sessions  map[string]*ptmSession
	shell     string
	shellArgs []string
	cols      int
	rows      int
}

func ptmNewManager() *ptmManager {
	m := &ptmManager{sessions: map[string]*ptmSession{}, cols: 120, rows: 36}
	// Pane shell = cmd.exe (NATIVE). MSYS2/cygwin bash cannot hand a working
	// console to the native node CLIs (claude/codex/opencode) it spawns under
	// our ConPTY — the exec deadlocks (proven: bash hangs on `node -v`; cmd
	// returns instantly). cmd.exe is native, so node/claude launch fine AND
	// programmatic send-keys reaches it. Agent boot is therefore done natively
	// (Go writes the config files + sends the native launch command), not via
	// `source boot.sh`. See initPaneEnv's native branch.
	m.shell = "cmd.exe"
	return m
}

func (m *ptmManager) Has(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[ptmSessionOf(name)]
	return ok
}

func (m *ptmManager) New(name, shell, cwd string, env []string, args ...string) error {
	m.mu.Lock()
	if _, ok := m.sessions[name]; ok {
		m.mu.Unlock()
		return nil // tmux new-session on an existing name is an error, but cicy
		// guards with has-session first; treat as idempotent success.
	}
	m.mu.Unlock()

	s, err := ptmNewSession(name, shell, cwd, env, m.cols, m.rows, args...)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.sessions[name] = s
	m.mu.Unlock()
	return nil
}

func (m *ptmManager) Kill(name string) error {
	name = ptmSessionOf(name)
	m.mu.Lock()
	s, ok := m.sessions[name]
	delete(m.sessions, name)
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("can't find session: %s", name)
	}
	return s.Close()
}

func (m *ptmManager) List() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.sessions))
	for k := range m.sessions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (m *ptmManager) get(paneID string) (*ptmSession, bool) {
	m.mu.Lock()
	s, ok := m.sessions[ptmSessionOf(paneID)]
	m.mu.Unlock()
	return s, ok
}

// Tmux is the drop-in for runTmux/exec.Command("tmux",…): it parses the tmux
// subcommands cicy emits and dispatches to the registry, returning tmux-shaped
// stdout.
func (m *ptmManager) Tmux(args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("tmux: no command")
	}
	sub, f := args[0], ptmParseFlags(args[1:])

	switch sub {
	case "has-session":
		if m.Has(f.target) {
			return "", nil
		}
		return "", fmt.Errorf("can't find session: %s", f.target)

	case "new-session":
		name := f.sName
		if name == "" {
			name = f.target
		}
		shell, sargs := m.shell, m.shellArgs
		if len(f.positional) > 0 {
			shell, sargs = f.positional[0], f.positional[1:]
		}
		return "", m.New(name, shell, ptmFromPosix(f.cwd), nil, sargs...)

	case "kill-session":
		return "", m.Kill(f.target)

	case "list-sessions":
		return strings.Join(m.List(), "\n"), nil

	case "send-keys":
		s, ok := m.get(f.target)
		if !ok {
			return "", fmt.Errorf("can't find pane: %s", f.target)
		}
		return "", s.SendKeys(ptmTranslateKeys(f.positional, f.literal))

	case "capture-pane":
		s, ok := m.get(f.target)
		if !ok {
			return "", fmt.Errorf("can't find pane: %s", f.target)
		}
		return s.Capture(), nil

	case "display-message":
		s, ok := m.get(f.target)
		spec := ""
		if len(f.positional) > 0 {
			spec = f.positional[len(f.positional)-1]
		}
		switch spec {
		case "#{pane_current_command}":
			if !ok {
				return "", nil
			}
			return s.Foreground(), nil
		case "#{pane_pid}":
			if !ok {
				return "", nil
			}
			return fmt.Sprintf("%d", s.Pid()), nil
		case "#{session_name}":
			return ptmSessionOf(f.target), nil
		default:
			return "", nil
		}

	case "new-window":
		// single-window model: no real new window; address the main pane.
		return ptmSessionOf(f.target) + ":0", nil

	case "list-windows":
		// one window "main" at index 0, active.
		if strings.Contains(f.format, "|") {
			return "0|main|1", nil
		}
		return "0 main", nil

	case "set-environment", "pipe-pane", "clear-history", "select-window",
		"rename-window", "kill-window", "kill-pane", "split-window",
		"set-option", "set", "show-options", "choose-tree":
		return "", nil // accepted no-ops for the cicy subset
	}
	return "", fmt.Errorf("tmux: unsupported subcommand %q", sub)
}

