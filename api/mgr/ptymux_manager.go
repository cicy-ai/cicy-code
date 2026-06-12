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

func ptmSessionOf(paneID string) string {
	if i := strings.IndexByte(paneID, ':'); i >= 0 {
		return paneID[:i]
	}
	return paneID
}

// ptmFromPosix converts an MSYS path (/c/Users/x) back to a Windows path
// (C:\Users\x) for ConPTY's working directory. cicy passes -c in MSYS form.
func ptmFromPosix(p string) string {
	if len(p) >= 2 && p[0] == '/' &&
		((p[1] >= 'a' && p[1] <= 'z') || (p[1] >= 'A' && p[1] <= 'Z')) &&
		(len(p) == 2 || p[2] == '/') {
		drive := strings.ToUpper(string(p[1]))
		rest := strings.ReplaceAll(p[2:], "/", `\`)
		return drive + ":" + rest
	}
	return p
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

type ptmFlags struct {
	target, sName, window, cwd, format string
	literal                            bool
	positional                         []string
}

func ptmParseFlags(args []string) ptmFlags {
	var f ptmFlags
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--":
			f.positional = append(f.positional, args[i+1:]...)
			return f
		case a == "-t" && i+1 < len(args):
			f.target = args[i+1]
			i += 2
		case a == "-s" && i+1 < len(args):
			f.sName = args[i+1]
			i += 2
		case a == "-n" && i+1 < len(args):
			f.window = args[i+1]
			i += 2
		case a == "-c" && i+1 < len(args):
			f.cwd = args[i+1]
			i += 2
		case a == "-F" && i+1 < len(args):
			f.format = args[i+1]
			i += 2
		case a == "-S" && i+1 < len(args):
			i += 2
		case a == "-l":
			f.literal = true
			i++
		case a == "-d" || a == "-p" || a == "-J" || a == "-e" || a == "-r" ||
			a == "-Zs" || a == "-g" || a == "-o" || a == "-P":
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1 && a[1] >= 'a' && a[1] <= 'z':
			i++
		default:
			f.positional = append(f.positional, a)
			i++
		}
	}
	return f
}

func ptmTranslateKeys(args []string, literal bool) string {
	if literal {
		return strings.Join(args, " ")
	}
	var b strings.Builder
	for _, a := range args {
		switch a {
		case "Enter":
			b.WriteByte('\r')
		case "Tab":
			b.WriteByte('\t')
		case "Space":
			b.WriteByte(' ')
		case "Escape":
			b.WriteByte(0x1b)
		case "BSpace":
			b.WriteByte(0x7f)
		case "Up":
			b.WriteString("\x1b[A")
		case "Down":
			b.WriteString("\x1b[B")
		case "Right":
			b.WriteString("\x1b[C")
		case "Left":
			b.WriteString("\x1b[D")
		case "C-c":
			b.WriteByte(0x03)
		case "C-u":
			b.WriteByte(0x15)
		case "C-l":
			b.WriteByte(0x0c)
		case "C-d":
			b.WriteByte(0x04)
		default:
			b.WriteString(a)
		}
	}
	return b.String()
}
