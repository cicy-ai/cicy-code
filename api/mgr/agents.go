package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// liveSessionSet returns the names of tmux sessions currently alive on this
// host. Used to report REAL liveness — a pane_agents row only means "attached
// to a master", never "running" (Barry's console must not show zombies as
// online).
func liveSessionSet() map[string]bool {
	out, err := runTmux("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return map[string]bool{}
	}
	live := map[string]bool{}
	for _, s := range strings.Split(out, "\n") {
		if s = strings.TrimSpace(s); s != "" {
			live[s] = true
		}
	}
	return live
}

func listAgentsByPane(paneID string) ([]M, error) {
	query := `SELECT pa.id, pa.pane_id, pa.agent_name, pa.status,
		COALESCE(ac.title, pa.agent_name) as title,
		COALESCE(ac.agent_type, '') as agent_type,
		COALESCE(ac.machine_id, 0) as machine_id,
		COALESCE(m.label, '') as machine_label,
		COALESCE(ac.source_kind, 'local') as source_kind,
		COALESCE(ac.source_ref, '') as source_ref
		FROM pane_agents pa
		LEFT JOIN agent_config ac ON ac.pane_id = CASE WHEN instr(pa.agent_name, ':') > 0 THEN pa.agent_name ELSE pa.agent_name || ':main.0' END
		LEFT JOIN machines m ON ac.machine_id = m.id`
	var args []interface{}
	if paneID != "" && paneID != "all" {
		query += " WHERE pa.pane_id=?"
		args = append(args, shortPaneID(normPaneID(paneID)))
	}
	query += " ORDER BY COALESCE(pa.sort_order, 0) ASC, pa.id ASC"
	rows, err := store.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	live := liveSessionSet()
	var agents []M
	for rows.Next() {
		var id int
		var pid, name, status, title, agentType, machineLabel, sourceKind, sourceRef string
		var machineID int
		rows.Scan(&id, &pid, &name, &status, &title, &agentType, &machineID, &machineLabel, &sourceKind, &sourceRef)
		// online = real liveness, not row presence. Local agents: live tmux
		// session — EXCEPT cicy, which is headless (no pane/tmux session); its
		// liveness is server-side session registry membership. Remote agents
		// (machine_id>0): liveness is unknown from here — report null so the UI
		// falls back to its own signal instead of a false green.
		var online interface{}
		if machineID == 0 {
			if normalizeAgentType(agentType) == "cicy" {
				online = cicySessionRegistered(shortPaneID(name))
			} else {
				online = live[shortPaneID(name)]
			}
		}
		agents = append(agents, M{"id": id, "pane_id": pid, "name": name, "status": status, "title": title, "agent_type": agentType, "machine_id": machineID, "machine_label": machineLabel, "source_kind": sourceKind, "source_ref": sourceRef, "online": online})
	}
	if agents == nil {
		agents = []M{}
	}
	return agents, nil
}

type boundAgentWorkspace struct {
	shortID    string
	paneID     string
	workspace  string
	machineID  int
	sourceKind string
}

func normalizeLegacyWorkspacePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "/cicy" {
		return cicyRootDir
	}
	if strings.HasPrefix(value, "/cicy/") {
		return filepath.Join(cicyRootDir, strings.TrimPrefix(value, "/cicy/"))
	}
	return value
}

var managedWorkerLinkPattern = regexp.MustCompile(`^w-\d+$`)

func listBoundAgentWorkspaces(paneID string) ([]boundAgentWorkspace, error) {
	paneID = shortPaneID(normPaneID(strings.TrimSpace(paneID)))
	if paneID == "" {
		return []boundAgentWorkspace{}, nil
	}
	rows, err := store.Query(`SELECT
		COALESCE(pa.agent_name, ''),
		CASE WHEN instr(pa.agent_name, ':') > 0 THEN pa.agent_name ELSE pa.agent_name || ':main.0' END,
		COALESCE(ac.workspace, ''),
		COALESCE(ac.machine_id, 0),
		COALESCE(ac.source_kind, 'local')
		FROM pane_agents pa
		LEFT JOIN agent_config ac ON ac.pane_id = CASE WHEN instr(pa.agent_name, ':') > 0 THEN pa.agent_name ELSE pa.agent_name || ':main.0' END
		WHERE pa.pane_id=?`, paneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []boundAgentWorkspace{}
	for rows.Next() {
		var item boundAgentWorkspace
		if err := rows.Scan(&item.shortID, &item.paneID, &item.workspace, &item.machineID, &item.sourceKind); err != nil {
			return nil, err
		}
		item.shortID = shortPaneID(item.shortID)
		item.paneID = normPaneID(item.paneID)
		item.sourceKind = strings.ToLower(strings.TrimSpace(item.sourceKind))
		if item.workspace != "" {
			home, _ := os.UserHomeDir()
			item.workspace = normalizeLegacyWorkspacePath(os.ExpandEnv(strings.Replace(item.workspace, "~", home, 1)))
		}
		items = append(items, item)
	}
	if items == nil {
		items = []boundAgentWorkspace{}
	}
	return items, nil
}

// syncBoundAgentWorkspaceLinks is DISABLED: we no longer auto-create a
// `workers/` directory under a parent agent's workspace, nor symlink bound child
// agents into it. The function is kept (call sites unchanged) only to TEAR DOWN
// any artifacts left over from when the feature was enabled — it removes the
// managed `w-NNNN` symlinks (never real files/dirs) and drops the `workers/`
// directory if it ends up empty. It never creates anything.
func syncBoundAgentWorkspaceLinks(parentPaneID string) error {
	parentPaneID = normPaneID(strings.TrimSpace(parentPaneID))
	if parentPaneID == "" {
		return nil
	}
	if nodeURL(parentPaneID) != "" {
		return nil
	}
	parentWorkspace := runtimePathToHostPath(paneWorkspace(parentPaneID))
	if parentWorkspace == "" {
		return nil
	}
	workersDir := filepath.Join(parentWorkspace, "workers")
	entries, err := os.ReadDir(workersDir)
	if err != nil {
		// No workers/ dir (the common case once disabled) → nothing to do.
		return nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if !managedWorkerLinkPattern.MatchString(name) {
			continue
		}
		linkPath := filepath.Join(workersDir, name)
		info, lerr := os.Lstat(linkPath)
		if lerr != nil || info.Mode()&os.ModeSymlink == 0 {
			// Only ever remove our own symlinks, never real dirs/files.
			continue
		}
		if err := os.Remove(linkPath); err == nil {
			log.Printf("[poll_links] removed legacy child link parent=%s child=%s", shortPaneID(parentPaneID), name)
		}
	}
	// Drop the now-empty workers/ dir (it only ever held our symlinks).
	if remaining, rerr := os.ReadDir(workersDir); rerr == nil && len(remaining) == 0 {
		_ = os.Remove(workersDir)
	}
	return nil
}

func syncAllBoundAgentWorkspaceLinks() error {
	rows, err := store.Query("SELECT DISTINCT pane_id FROM pane_agents WHERE status='active'")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var paneID string
		if err := rows.Scan(&paneID); err != nil {
			return err
		}
		if err := syncBoundAgentWorkspaceLinks(paneID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func handleAgentsByPane(w http.ResponseWriter, r *http.Request) {
	paneID := r.URL.Query().Get("pane_id")
	if paneID == "" {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/agents/pane/"):
			paneID = strings.TrimPrefix(r.URL.Path, "/api/agents/pane/")
		case strings.HasPrefix(r.URL.Path, "/api/agents/by-pane/"):
			paneID = strings.TrimPrefix(r.URL.Path, "/api/agents/by-pane/")
		case strings.HasPrefix(r.URL.Path, "/api/agents/by-pane"):
			// no path id, keep empty to return all
		}
	}
	agents, err := listAgentsByPane(paneID)
	if err != nil {
		J(w, []M{})
		return
	}
	J(w, agents)
}

// bindAgentResult mirrors what handleAgentBind returns so the merged
// create-with-master path can stay consistent with the standalone bind API.
type bindAgentResult struct {
	ID           int64
	PaneID       string
	AgentName    string
	AlreadyBound bool
}

// bindAgentCore inserts a pane_agents row (or detects an existing one), syncs
// workspace symlinks, and optionally writes the master-reference into the sub's
// guidance file. Used by both /api/agents/bind and the merged create-with-master
// path.
//
// inheritGuidance controls whether the sub's CLAUDE.md/AGENTS.md picks up an
// `@<master-path>` reference line. masterAgentTypeHint, when non-empty,
// overrides the DB-derived master agent_type (useful for remote masters).
func bindAgentCore(masterPaneID, subShortName string, inheritGuidance bool, masterAgentTypeHint string) (bindAgentResult, error) {
	masterShort := shortPaneID(normPaneID(strings.TrimSpace(masterPaneID)))
	subShort := shortPaneID(normPaneID(strings.TrimSpace(subShortName)))
	if masterShort == "" || subShort == "" {
		return bindAgentResult{}, fmt.Errorf("pane_id and agent_name required")
	}
	var existingID int64
	err := store.QueryRow(
		"SELECT id FROM pane_agents WHERE pane_id=? AND agent_name=?",
		masterShort, subShort,
	).Scan(&existingID)
	switch {
	case err == nil && existingID > 0:
		if syncErr := syncBoundAgentWorkspaceLinks(masterShort); syncErr != nil {
			log.Printf("[agent-bind] re-sync after idempotent bind failed: %v", syncErr)
		}
		// Inheritance retired: agents no longer inject a master-rules reference.
		// Each agent owns a self-contained guidance file seeded from the global
		// template. (inheritGuidance/masterAgentTypeHint kept for API compat.)
		_ = inheritGuidance
		// Even when already bound, ensure the sub-worker is actually running
		// so re-binding doubles as a "wake up" gesture.
		if err := ensureAgentRunningByPaneID(subShort + ":main.0"); err != nil {
			log.Printf("[agent-bind] ensure sub-worker %s running failed: %v", subShort, err)
		}
		return bindAgentResult{ID: existingID, PaneID: masterShort, AgentName: subShort, AlreadyBound: true}, nil
	case err != nil && err != sql.ErrNoRows:
		return bindAgentResult{}, err
	}
	res, err := store.Exec(
		"INSERT INTO pane_agents (pane_id, agent_name, status) VALUES (?,?,'active')",
		masterShort, subShort,
	)
	if err != nil {
		return bindAgentResult{}, err
	}
	if err := syncBoundAgentWorkspaceLinks(masterShort); err != nil {
		return bindAgentResult{}, err
	}
	// Inheritance retired (see above): no master-rules reference is appended.
	_ = masterAgentTypeHint
	// If the sub-worker was previously stopped (tmux session killed), bring it
	// back up so the binding is immediately usable.
	if err := ensureAgentRunningByPaneID(subShort + ":main.0"); err != nil {
		log.Printf("[agent-bind] ensure sub-worker %s running failed: %v", subShort, err)
	}
	id, _ := res.LastInsertId()
	go broadcastPollData(masterShort)
	return bindAgentResult{ID: id, PaneID: masterShort, AgentName: subShort}, nil
}

func handleAgentBind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req M
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	paneID, _ := req["pane_id"].(string)
	agentName, _ := req["agent_name"].(string)
	if strings.TrimSpace(paneID) == "" || strings.TrimSpace(agentName) == "" {
		httpErr(w, http.StatusBadRequest, "pane_id and agent_name required")
		return
	}
	// Optional: master_agent_type (override) and inherit_guidance (default true).
	masterAgentType, _ := req["master_agent_type"].(string)
	inheritGuidance := true
	if v, ok := req["inherit_guidance"].(bool); ok {
		inheritGuidance = v
	}

	result, err := bindAgentCore(paneID, agentName, inheritGuidance, masterAgentType)
	if err != nil {
		if err.Error() == "pane_id and agent_name required" {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := M{"success": true, "id": result.ID, "pane_id": result.PaneID, "agent_name": result.AgentName}
	if result.AlreadyBound {
		resp["already_bound"] = true
	}
	J(w, resp)
}

func handleAgentUnbind(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		httpErr(w, 405, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/agents/unbind/")
	// 先查出 pane_id（master）和 agent_name（sub）用于广播和后续 stop
	var unbindPaneID, unbindSubName string
	_ = store.QueryRow("SELECT pane_id, agent_name FROM pane_agents WHERE id=?", id).Scan(&unbindPaneID, &unbindSubName)
	res, err := store.Exec("DELETE FROM pane_agents WHERE id=?", id)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpErr(w, 404, "Agent binding not found")
		return
	}
	if unbindPaneID != "" {
		if err := syncBoundAgentWorkspaceLinks(unbindPaneID); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	// Stop the sub-worker if it's not bound to any other master anymore — EXCEPT
	// cicy lite agents. A cicy agent (task secretary, role agents) is a
	// persistent fixture, not a disposable worker: unbinding it must only remove
	// the master→sub relation, never kill its REPL/tmux. So codex/claude/etc.
	// follow the reference-count rule below; cicy is always kept alive.
	if unbindSubName != "" {
		if paneAgentType(unbindSubName+":main.0") == "cicy" {
			log.Printf("[agent-unbind] keep cicy agent %s alive: cicy is never killed by unbind", unbindSubName)
		} else {
			var remaining int
			_ = store.QueryRow(
				"SELECT COUNT(*) FROM pane_agents WHERE agent_name=? AND status='active'",
				unbindSubName,
			).Scan(&remaining)
			if remaining == 0 {
				stopAgentByPaneID(unbindSubName + ":main.0")
			} else {
				log.Printf("[agent-unbind] keep %s alive: still bound to %d master(s)", unbindSubName, remaining)
			}
		}
	}
	if unbindPaneID != "" {
		go broadcastPollData(unbindPaneID)
	}
	J(w, M{"success": true})
}

// handleAgentGreeting returns the agent's opening line, shown by the UI when the
// chat history is empty (role agents draw it from their role template's 开场白).
func handleAgentGreeting(w http.ResponseWriter, r *http.Request) {
	id := shortPaneID(normPaneID(strings.TrimPrefix(r.URL.Path, "/api/agents/greeting/")))
	if id == "" {
		httpErr(w, http.StatusBadRequest, "pane id required")
		return
	}
	J(w, M{"pane_id": id, "greeting": agentOpeningGreeting(id)})
}

func handleAgentReorder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		PaneID     string   `json:"pane_id"`
		AgentNames []string `json:"agent_names"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	paneID := shortPaneID(normPaneID(strings.TrimSpace(req.PaneID)))
	if paneID == "" {
		httpErr(w, http.StatusBadRequest, "pane_id required")
		return
	}
	for i, name := range req.AgentNames {
		shortName := shortPaneID(normPaneID(strings.TrimSpace(name)))
		if shortName == "" {
			continue
		}
		_, _ = store.Exec("UPDATE pane_agents SET sort_order=? WHERE pane_id=? AND agent_name=?", i, paneID, shortName)
	}
	go broadcastPollData(paneID)
	J(w, M{"success": true})
}

// agentParentRef returns the short source_ref (tree parent) of an agent.
func agentParentRef(short string) string {
	var ref string
	store.QueryRow("SELECT COALESCE(source_ref,'') FROM agent_config WHERE pane_id=?", normPaneID(short)).Scan(&ref)
	return shortPaneID(normPaneID(strings.TrimSpace(ref)))
}

// agentIsDescendant reports whether `node` lives in the subtree rooted at
// `ancestor` — i.e. walking node's source_ref chain upward reaches ancestor.
// Used as a cycle guard for reparenting.
func agentIsDescendant(node, ancestor string) bool {
	seen := map[string]bool{}
	for cur := node; cur != "" && !seen[cur]; cur = agentParentRef(cur) {
		if cur == ancestor {
			return true
		}
		seen[cur] = true
	}
	return false
}

// handleAgentReparent moves an agent under a new tree parent by rewriting its
// source_ref. Empty new_parent promotes the agent to top-level. The tree is
// derived from source_ref (the fork-origin pointer), so this is all it takes.
func handleAgentReparent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		PaneID        string `json:"pane_id"`         // agent being moved
		NewParent     string `json:"new_parent"`      // new source_ref ("" = top-level)
		ContextPaneID string `json:"context_pane_id"` // whose poll to refresh
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	childFull := normPaneID(strings.TrimSpace(req.PaneID))
	child := shortPaneID(childFull)
	parent := shortPaneID(normPaneID(strings.TrimSpace(req.NewParent)))
	if child == "" {
		httpErr(w, http.StatusBadRequest, "pane_id required")
		return
	}
	if parent == child {
		httpErr(w, http.StatusBadRequest, "cannot reparent onto self")
		return
	}
	// Cycle guard: the new parent must not be a descendant of the moved node.
	if parent != "" && agentIsDescendant(parent, child) {
		httpErr(w, http.StatusBadRequest, "would create a cycle")
		return
	}
	var err error
	if parent == "" {
		_, err = store.Exec("UPDATE agent_config SET source_kind='', source_ref='' WHERE pane_id=?", childFull)
	} else {
		_, err = store.Exec("UPDATE agent_config SET source_kind='fork', source_ref=? WHERE pane_id=?", parent, childFull)
	}
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "reparent failed")
		return
	}
	ctx := shortPaneID(normPaneID(strings.TrimSpace(req.ContextPaneID)))
	if ctx == "" {
		ctx = child
	}
	go broadcastPollData(ctx)
	J(w, M{"success": true})
}
