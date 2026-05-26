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
	var agents []M
	for rows.Next() {
		var id int
		var pid, name, status, title, agentType, machineLabel, sourceKind, sourceRef string
		var machineID int
		rows.Scan(&id, &pid, &name, &status, &title, &agentType, &machineID, &machineLabel, &sourceKind, &sourceRef)
		agents = append(agents, M{"id": id, "pane_id": pid, "name": name, "status": status, "title": title, "agent_type": agentType, "machine_id": machineID, "machine_label": machineLabel, "source_kind": sourceKind, "source_ref": sourceRef})
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

func syncBoundAgentWorkspaceLinks(parentPaneID string) error {
	parentPaneID = normPaneID(strings.TrimSpace(parentPaneID))
	if parentPaneID == "" {
		return nil
	}
	if nodeURL(parentPaneID) != "" {
		log.Printf("[poll_links] skip remote parent=%s", shortPaneID(parentPaneID))
		return nil
	}
	parentWorkspace := runtimePathToHostPath(paneWorkspace(parentPaneID))
	if parentWorkspace == "" {
		return nil
	}
	workersDir := filepath.Join(parentWorkspace, "workers")
	if err := os.MkdirAll(workersDir, 0755); err != nil {
		return err
	}
	items, err := listBoundAgentWorkspaces(parentPaneID)
	if err != nil {
		return err
	}
	desired := map[string]string{}
	for _, item := range items {
		if item.shortID == "" || item.workspace == "" {
			continue
		}
		if item.machineID > 0 || (item.sourceKind != "" && item.sourceKind != "local") {
			log.Printf("[poll_links] skip remote child parent=%s child=%s machine_id=%d source_kind=%s", shortPaneID(parentPaneID), item.shortID, item.machineID, item.sourceKind)
			continue
		}
		linkPath := filepath.Join(workersDir, item.shortID)
		targetPath := runtimePathToHostPath(item.workspace)
		desired[item.shortID] = targetPath
		currentTarget, readErr := os.Readlink(linkPath)
		if readErr == nil {
			if currentTarget == targetPath {
				continue
			}
			if err := os.Remove(linkPath); err != nil {
				return err
			}
		} else if !os.IsNotExist(readErr) {
			if info, statErr := os.Lstat(linkPath); statErr == nil && info.Mode()&os.ModeSymlink == 0 {
				log.Printf("[poll_links] skip conflicting path parent=%s child=%s path=%s", shortPaneID(parentPaneID), item.shortID, linkPath)
				continue
			}
		}
		if err := os.Symlink(targetPath, linkPath); err != nil {
			return err
		}
		log.Printf("[poll_links] synced child link parent=%s child=%s target=%s", shortPaneID(parentPaneID), item.shortID, targetPath)
	}
	entries, err := os.ReadDir(workersDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !managedWorkerLinkPattern.MatchString(name) {
			continue
		}
		if _, ok := desired[name]; ok {
			continue
		}
		linkPath := filepath.Join(workersDir, name)
		info, err := os.Lstat(linkPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			log.Printf("[poll_links] skip non-symlink stale candidate parent=%s child=%s path=%s", shortPaneID(parentPaneID), name, linkPath)
			continue
		}
		if err := os.Remove(linkPath); err != nil {
			return err
		}
		log.Printf("[poll_links] removed stale child link parent=%s child=%s", shortPaneID(parentPaneID), name)
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
		if inheritGuidance {
			appendMasterReferenceToGuidance(subShort, masterShort, masterAgentTypeHint)
		}
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
	if inheritGuidance {
		appendMasterReferenceToGuidance(subShort, masterShort, masterAgentTypeHint)
	}
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
	// Stop the sub-worker if it's not bound to any other master anymore.
	if unbindSubName != "" {
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
	if unbindPaneID != "" {
		go broadcastPollData(unbindPaneID)
	}
	J(w, M{"success": true})
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
