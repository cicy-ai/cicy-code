package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// The "shell panel" lets the UI show a second terminal next to the agent's main
// view without disturbing it. tmux's current-window is a *session*-level
// property, so two clients on the same session always display the same window —
// switching the shell would drag the agent's view along. The fix is a tmux
// grouped session (`new-session -t <agent> -s <agent>-sh`): it shares the
// window list (so windows created either side are visible to both) but keeps an
// independent current-window. The agent's main card stays attached to the real
// session (frozen on main.0, agent untouched); the panel attaches to the
// grouped session and navigates windows on its own.

// shellGroupedName maps an agent session ("w-10053") to its shell grouped
// session name ("w-10053-sh").
func shellGroupedName(session string) string { return session + "-sh" }

// ensureShellSession makes sure the grouped shell session for an agent exists,
// landing it on a dedicated "shell" window (not the agent's main.0) on first
// creation. Idempotent. Returns the grouped session name.
func ensureShellSession(session string) string {
	grouped := shellGroupedName(session)
	if tmuxCommand("has-session", "-t", grouped).Run() == nil {
		return grouped
	}
	// Create the grouped session sharing the agent session's window list.
	if err := tmuxCommand("new-session", "-d", "-t", session, "-s", grouped).Run(); err != nil {
		return grouped // best effort; attach will surface a clearer error
	}
	// Add a dedicated shell window so the panel doesn't start mirroring the
	// agent. `-d` keeps the *agent* session's current window on main.0; we then
	// point only the grouped session at the new window.
	workdir := filepath.Join(cicyWorkersDir, session)
	_ = os.MkdirAll(workdir, 0755)
	out, err := tmuxCommand("new-window", "-d", "-t", session, "-n", "shell", "-c", workdir, "-P", "-F", "#{window_index}").Output()
	if err == nil {
		if idx := strings.TrimSpace(string(out)); idx != "" {
			_ = tmuxCommand("select-window", "-t", grouped+":"+idx).Run()
		}
	}
	return grouped
}

// stopShellSession tears down the grouped shell session and its ttyd instance.
// Called when the underlying agent is stopped.
func stopShellSession(session string) {
	if session == "" {
		return
	}
	grouped := shellGroupedName(session)
	// No ttyd instance to stop now; killing the tmux session EOFs any live
	// attach, which tears down its webtty session.
	tmuxCommand("kill-session", "-t", grouped).Run()
}

// handleTtydShellProxy serves a ttyd terminal attached to the agent's grouped
// shell session. Route: /ttyd-shell/{agentId}/...  Mirrors handleTtydProxy but
// targets the grouped session instead of a DB-backed pane.
func handleTtydShellProxy(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/ttyd-shell/")
	parts := strings.SplitN(path, "/", 2)
	session := shortPaneID(normPaneID(parts[0])) // "w-10053"
	subPath := "/"
	if len(parts) > 1 {
		subPath = "/" + parts[1]
	}

	// Token required only for the root page (assets + WS follow after load).
	if subPath == "/" {
		token := r.URL.Query().Get("token")
		if token == "" || !verifyToken(token) {
			httpErr(w, 401, "token required")
			return
		}
	}

	// The parent agent must exist and be active.
	var one int
	if err := store.QueryRow("SELECT 1 FROM agent_config WHERE pane_id=? AND active=1", normPaneID(session)).Scan(&one); err != nil {
		httpErr(w, 404, "agent not found or inactive")
		return
	}

	grouped := ensureShellSession(session)

	// Serve in-process, attached to the grouped shell session. Title + ws-api
	// pane both use the grouped name, matching the old proxyWS(grouped, ...).
	serveTtydHTTP(w, r, grouped, subPath, grouped, grouped)
}
