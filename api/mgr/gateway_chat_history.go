package main

import (
	"database/sql"
	"net/http"
	"strings"
)

// handleAgentChatHistory returns one historical turn of an agent by reverse
// index. index=0 is the most recent committed turn, index=1 the one before,
// etc. Backed by the per-pane sqlite at <history-dir>/history.db.
//
// Request body:
//
//	{ "pane_id": "w-10003", "index": 0 }
//
// Response (turn found):
//
//	{
//	  "pane_id":         "w-10003",
//	  "index":           0,
//	  "found":           true,
//	  "conversation_id": "...",
//	  "turn_id":         "...",   # turn_key in storage
//	  "q":               "...",
//	  "a":               "...",
//	  "thinking":        "...",
//	  "model":           "...",
//	  "status":          "completed",
//	  "q_time":          "RFC3339",
//	  "a_time":          "RFC3339",
//	  "created_at":      "RFC3339"
//	}
//
// Response (index out of range): same shape with "found": false and all
// other string fields empty. Not an error.
func handleAgentChatHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		PaneID string `json:"pane_id"`
		WinID  string `json:"win_id"`
		Index  int    `json:"index"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	target := strings.TrimSpace(req.PaneID)
	if target == "" {
		target = strings.TrimSpace(req.WinID)
	}
	target = shortPaneID(normPaneID(target))
	if target == "" {
		httpErr(w, http.StatusBadRequest, "pane_id required")
		return
	}
	if req.Index < 0 {
		httpErr(w, http.StatusBadRequest, "index must be >= 0")
		return
	}

	db, err := agentHistoryOpen(target)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "open history: "+err.Error())
		return
	}
	defer db.Close()

	const query = `SELECT conversation_id, turn_key, q, a, thinking, model, status, q_time, a_time, created_at
	               FROM history_turns
	               WHERE agent_id = ?
	               ORDER BY id DESC
	               LIMIT 1 OFFSET ?`
	row := db.QueryRow(query, target, req.Index)

	var convID, turnKey, q, a, thinking, model, status, qTime, aTime, createdAt string
	switch err := row.Scan(&convID, &turnKey, &q, &a, &thinking, &model, &status, &qTime, &aTime, &createdAt); err {
	case nil:
		J(w, map[string]interface{}{
			"pane_id":         target,
			"index":           req.Index,
			"found":           true,
			"conversation_id": convID,
			"turn_id":         turnKey,
			"q":               q,
			"a":               a,
			"thinking":        thinking,
			"model":           model,
			"status":          status,
			"q_time":          qTime,
			"a_time":          aTime,
			"created_at":      createdAt,
		})
	case sql.ErrNoRows:
		J(w, map[string]interface{}{
			"pane_id": target,
			"index":   req.Index,
			"found":   false,
		})
	default:
		httpErr(w, http.StatusInternalServerError, "query history: "+err.Error())
	}
}
