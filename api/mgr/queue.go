package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleQueue routes GET/POST to /api/workers/queue
func handleQueue(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		handleQueueList(w, r)
	case "POST":
		handleQueuePush(w, r)
	default:
		httpErr(w, 405, "method not allowed")
	}
}

// handleQueueByID routes PATCH/DELETE to /api/workers/queue/{id}
func handleQueueByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path
	switch {
	case strings.HasPrefix(idStr, "/api/workers/queue/"):
		idStr = strings.TrimPrefix(idStr, "/api/workers/queue/")
	case strings.HasPrefix(idStr, "/api/queue/"):
		idStr = strings.TrimPrefix(idStr, "/api/queue/")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		httpErr(w, 400, "invalid id")
		return
	}
	switch r.Method {
	case "PATCH":
		handleQueueUpdate(w, r, id)
	case "DELETE":
		handleQueueDelete(w, r, id)
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleQueuePush(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaneID          string      `json:"pane_id"`
		TargetPaneID    string      `json:"target_pane_id"`
		Message         string      `json:"message"`
		Type            string      `json:"type"`
		Priority        int         `json:"priority"`
		StepKind        string      `json:"step_kind"`
		WorkflowID      interface{} `json:"workflow_id"`
		ParentID        interface{} `json:"parent_id"`
		StepIndex       int         `json:"step_index"`
		Title           string      `json:"title"`
		TargetMachineID interface{} `json:"target_machine_id"`
		CreatedBy       string      `json:"created_by"`
	}
	readBody(r, &req)
	paneID := firstNonEmpty(req.PaneID, req.TargetPaneID)
	if paneID == "" || req.Message == "" {
		httpErr(w, 400, "pane_id and message required")
		return
	}
	if req.Type == "" {
		req.Type = "message"
	}
	if req.StepKind == "" {
		req.StepKind = req.Type
	}
	paneID = normPaneID(paneID)
	res, err := store.Exec(`INSERT INTO agent_queue (
		pane_id, message, type, priority, step_kind, workflow_id, parent_id, step_index, title,
		status, target_machine_id, target_pane_id, created_by
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		paneID, req.Message, req.Type, req.Priority, req.StepKind, nullableInt(req.WorkflowID), nullableInt(req.ParentID), req.StepIndex, req.Title,
		"pending", nullableInt(req.TargetMachineID), shortPaneID(paneID), req.CreatedBy)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()

	J(w, M{"success": true, "id": id, "pane_id": shortPaneID(paneID)})
}

func handleQueueList(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	status := r.URL.Query().Get("status")
	workflowID := r.URL.Query().Get("workflow_id")
	items, err := listQueueItems(pane, status, workflowID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"queue": items})
}

func handleQueueUpdate(w http.ResponseWriter, r *http.Request, id int) {
	var req M
	readBody(r, &req)
	delete(req, "id")
	if len(req) == 0 {
		httpErr(w, 400, "no fields to update")
		return
	}
	var sets []string
	var vals []interface{}
	for k, v := range req {
		sets = append(sets, k+"=?")
		vals = append(vals, v)
	}
	vals = append(vals, id)
	_, err := store.Exec("UPDATE agent_queue SET "+strings.Join(sets, ", ")+" WHERE id=?", vals...)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	J(w, M{"success": true, "id": id})
}

func handleQueueDelete(w http.ResponseWriter, r *http.Request, id int) {
	store.Exec("DELETE FROM agent_queue WHERE id=?", id)
	J(w, M{"success": true, "id": id})
}

func forwardQueueToMachine(machineID int, paneID, message, msgType string) error {
	machine, err := findMachineByID(strconv.Itoa(machineID))
	if err != nil {
		return err
	}
	url, _ := machine["url"].(string)
	token, _ := machine["token"].(string)
	if url == "" {
		return fmt.Errorf("machine %d missing url", machineID)
	}
	return remoteQueuePush(url, token, shortPaneID(paneID), message, msgType)
}

// dispatchQueue checks for pending messages and sends to workers.
// Batches multiple pending messages together to reduce interruptions.
func dispatchQueue(paneID string) {
	rows, err := store.Query(
		"SELECT id, message, type, target_machine_id FROM agent_queue WHERE pane_id=? AND status IN ('pending','queued') ORDER BY priority DESC, id ASC",
		paneID,
	)
	if err != nil {
		return
	}
	defer rows.Close()

	var ids []int
	var messages []string
	var types []string
	var machineIDs []sql.NullInt64

	for rows.Next() {
		var id int
		var message, msgType string
		var machineID sql.NullInt64
		rows.Scan(&id, &message, &msgType, &machineID)
		ids = append(ids, id)
		messages = append(messages, message)
		types = append(types, msgType)
		machineIDs = append(machineIDs, machineID)
	}

	if len(messages) == 0 {
		return
	}

	for i, msg := range messages {
		if machineIDs[i].Valid {
			if err := forwardQueueToMachine(int(machineIDs[i].Int64), paneID, msg, types[i]); err == nil {
				store.Exec(fmt.Sprintf("UPDATE agent_queue SET status='sent', sent_at=%s WHERE id=?", store.Now()), ids[i])
				continue
			}
		}
		if types[i] == "command" {
			runTmux("send-keys", "-t", paneID, msg, "Enter")
		} else {
			runTmux("send-keys", "-t", paneID, "-l", msg)
			time.Sleep(enterDelay)
			runTmux("send-keys", "-t", paneID, "Enter")
		}
		store.Exec(fmt.Sprintf("UPDATE agent_queue SET status='sent', sent_at=%s WHERE id=?", store.Now()), ids[i])
		if i < len(messages)-1 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	log.Printf("[queue] dispatched %d msg(s) to %s", len(ids), shortPaneID(paneID))
}
