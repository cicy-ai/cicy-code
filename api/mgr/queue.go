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
		PaneID   string `json:"pane_id"`
		Message  string `json:"message"`
		Type     string `json:"type"`
		Priority int    `json:"priority"`
	}
	readBody(r, &req)
	if req.PaneID == "" || req.Message == "" {
		httpErr(w, 400, "pane_id and message required")
		return
	}
	if req.Type == "" {
		req.Type = "message"
	}
	paneID := normPaneID(req.PaneID)
	res, err := store.Exec("INSERT INTO agent_queue (pane_id, message, type, priority) VALUES (?,?,?,?)", paneID, req.Message, req.Type, req.Priority)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()

	// Don't dispatch immediately - let watcher handle batching on thinking->idle transition

	J(w, M{"success": true, "id": id, "pane_id": shortPaneID(paneID)})
}

func handleQueueList(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	status := r.URL.Query().Get("status")
	query := "SELECT id, pane_id, message, type, status, priority, created_at, sent_at FROM agent_queue WHERE 1=1"
	var args []interface{}
	if pane != "" {
		query += " AND pane_id=?"
		args = append(args, normPaneID(pane))
	}
	if status != "" {
		query += " AND status=?"
		args = append(args, status)
	}
	query += " ORDER BY priority DESC, id ASC"
	rows, err := store.Query(query, args...)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	var items []M
	for rows.Next() {
		var id int
		var paneID, message, msgType, st string
		var priority int
		var createdAt, sentAt sql.NullString
		rows.Scan(&id, &paneID, &message, &msgType, &st, &priority, &createdAt, &sentAt)
		item := M{"id": id, "pane_id": shortPaneID(paneID), "message": message, "type": msgType, "status": st, "priority": priority}
		if createdAt.Valid {
			item["created_at"] = createdAt.String
		}
		if sentAt.Valid {
			item["sent_at"] = sentAt.String
		}
		items = append(items, item)
	}
	if items == nil {
		items = []M{}
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

// dispatchQueue checks for pending messages and sends to workers.
// Batches multiple pending messages together to reduce interruptions.
func dispatchQueue(paneID string) {
	rows, err := store.Query(
		"SELECT id, message, type FROM agent_queue WHERE pane_id=? AND status='pending' ORDER BY priority DESC, id ASC",
		paneID,
	)
	if err != nil {
		return
	}
	defer rows.Close()

	var ids []int
	var messages []string
	var types []string

	for rows.Next() {
		var id int
		var message, msgType string
		rows.Scan(&id, &message, &msgType)
		ids = append(ids, id)
		messages = append(messages, message)
		types = append(types, msgType)
	}

	if len(messages) == 0 {
		return
	}

	// Send messages one by one with enterDelay before Enter for TUI compatibility
	for i, msg := range messages {
		if types[i] == "command" {
			runTmux("send-keys", "-t", paneID, msg, "Enter")
		} else {
			runTmux("send-keys", "-t", paneID, "-l", msg)
			time.Sleep(enterDelay)
			runTmux("send-keys", "-t", paneID, "Enter")
		}
		if i < len(messages)-1 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	log.Printf("[queue] sent %d msg(s) to %s", len(messages), shortPaneID(paneID))

	// Mark all as sent
	for _, id := range ids {
		store.Exec(fmt.Sprintf("UPDATE agent_queue SET status='sent', sent_at=%s WHERE id=?", store.Now()), id)
	}

	log.Printf("[queue] dispatched %d msg(s) to %s", len(ids), shortPaneID(paneID))
}
