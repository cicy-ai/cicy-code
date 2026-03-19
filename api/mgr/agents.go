package main

import (
	"net/http"
	"strings"
)

func handleAgentsByPane(w http.ResponseWriter, r *http.Request) {
	paneID := strings.TrimPrefix(r.URL.Path, "/api/agents/pane/")
	if paneID == "" || paneID == "all" {
		// Return all bindings
		rows, err := store.Query("SELECT id, pane_id, agent_name, status FROM pane_agents")
		if err != nil { J(w, []M{}); return }
		defer rows.Close()
		var all []M
		for rows.Next() {
			var id int; var pid, name, status string
			rows.Scan(&id, &pid, &name, &status)
			all = append(all, M{"id": id, "pane_id": pid, "name": name, "status": status})
		}
		if all == nil { all = []M{} }
		J(w, all)
		return
	}
	rows, err := store.Query("SELECT id, pane_id, agent_name as name, status FROM pane_agents WHERE pane_id=?", paneID)
	if err != nil {
		J(w, []M{})
		return
	}
	defer rows.Close()
	var agents []M
	for rows.Next() {
		var id int
		var pid, name, status string
		rows.Scan(&id, &pid, &name, &status)
		agents = append(agents, M{"id": id, "pane_id": pid, "name": name, "status": status})
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
	var existing int
	store.QueryRow("SELECT id FROM pane_agents WHERE pane_id=? AND agent_name=?", paneID, agentName).Scan(&existing)
	if existing > 0 {
		httpErr(w, 400, "Agent already bound to this pane")
		return
	}
	res, err := store.Exec("INSERT INTO pane_agents (pane_id, agent_name, status) VALUES (?,?,'active')", paneID, agentName)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	J(w, M{"success": true, "id": id})
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
