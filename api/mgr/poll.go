package main

import (
	"net/http"
	"time"
)

func handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	paneID := r.URL.Query().Get("pane_id")
	agents, err := listAgentsByPane(paneID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{
		"success":     true,
		"pane_id":     shortPaneID(normPaneID(paneID)),
		"agents":      agents,
		"statuses":    M{},
		"server_time": time.Now().UTC().Format(time.RFC3339),
	})
}
