package main

import (
	"database/sql"
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
	rows, err := store.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []M
	for rows.Next() {
		var id int
		var pid, name, status, title, machineLabel, sourceKind, sourceRef string
		var machineID int
		rows.Scan(&id, &pid, &name, &status, &title, &machineID, &machineLabel, &sourceKind, &sourceRef)
		agents = append(agents, M{"id": id, "pane_id": pid, "name": name, "status": status, "title": title, "machine_id": machineID, "machine_label": machineLabel, "source_kind": sourceKind, "source_ref": sourceRef})
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

func ensureWorkspaceHomeLink(workspace string) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return nil
	}
	linkPath := filepath.Join(workspace, "home")
	currentTarget, readErr := os.Readlink(linkPath)
	if readErr == nil {
		if currentTarget == home {
			return nil
		}
		if err := os.Remove(linkPath); err != nil {
			return err
		}
	} else if readErr != nil && !os.IsNotExist(readErr) {
		info, statErr := os.Lstat(linkPath)
		if statErr == nil && info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
	}
	if err := os.Symlink(home, linkPath); err != nil {
		return err
	}
	return nil
}

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
	if err := ensureWorkspaceHomeLink(parentWorkspace); err != nil {
		return err
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
	paneID = shortPaneID(normPaneID(strings.TrimSpace(paneID)))
	agentName = strings.TrimSpace(agentName)
	if paneID == "" || agentName == "" {
		httpErr(w, http.StatusBadRequest, "pane_id and agent_name required")
		return
	}

	fullAgentName := normPaneID(agentName)
	shortName := shortPaneID(fullAgentName)

	var existingID int
	err := store.QueryRow("SELECT id FROM pane_agents WHERE pane_id=? AND agent_name=?", paneID, shortName).Scan(&existingID)
	switch {
	case err == nil && existingID > 0:
		J(w, M{"success": true, "id": existingID, "pane_id": paneID, "agent_name": shortName, "already_bound": true})
		return
	case err != nil && err != sql.ErrNoRows:
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	res, err := store.Exec("INSERT INTO pane_agents (pane_id, agent_name, status) VALUES (?,?,'active')", paneID, shortName)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := syncBoundAgentWorkspaceLinks(paneID); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	go broadcastPollData(paneID)
	J(w, M{"success": true, "id": id, "pane_id": paneID, "agent_name": shortName})
}

func handleAgentUnbind(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		httpErr(w, 405, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/agents/unbind/")
	// 先查出 pane_id 用于广播
	var unbindPaneID string
	_ = store.QueryRow("SELECT pane_id FROM pane_agents WHERE id=?", id).Scan(&unbindPaneID)
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
	if unbindPaneID != "" {
		go broadcastPollData(unbindPaneID)
	}
	J(w, M{"success": true})
}
