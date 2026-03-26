package main

import (
	"net/http"
	"strings"
)

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
		args = append(args, paneID)
	}
	rows, err := store.Query(query, args...)
	if err != nil {
		J(w, []M{})
		return
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
	J(w, agents)
}

func handleAgentBind(w http.ResponseWriter, r *http.Request) {
	var req M
	readBody(r, &req)
	paneID, _ := req["pane_id"].(string)
	agentName, _ := req["agent_name"].(string)
	if paneID == "" || agentName == "" {
		httpErr(w, 400, "pane_id and agent_name required")
		return
	}
	fullAgentName := normPaneID(agentName)
	shortName := shortPaneID(fullAgentName)
	var existing int
	store.QueryRow("SELECT id FROM pane_agents WHERE pane_id=? AND agent_name=?", paneID, shortName).Scan(&existing)
	if existing > 0 {
		httpErr(w, 400, "Agent already bound to this pane")
		return
	}
	res, err := store.Exec("INSERT INTO pane_agents (pane_id, agent_name, status) VALUES (?,?,'active')", paneID, shortName)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	J(w, M{"success": true, "id": id, "agent_name": shortName})
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
