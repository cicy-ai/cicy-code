package main

import (
	"database/sql"
	"net/http"
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
	id, _ := res.LastInsertId()
	J(w, M{"success": true, "id": id, "pane_id": paneID, "agent_name": shortName})
}

func handleAgentUnbind(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		httpErr(w, 405, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/agents/unbind/")
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
	J(w, M{"success": true})
}
