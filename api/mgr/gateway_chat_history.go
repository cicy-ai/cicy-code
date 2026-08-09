// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

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

	resp, err := agentChatHistoryData(target, req.Index)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "query history: "+err.Error())
		return
	}
	J(w, resp)
}

func agentChatHistoryData(target string, index int) (M, error) {
	target = shortPaneID(normPaneID(strings.TrimSpace(target)))
	if normalizeAgentType(paneAgentType(target+":main.0")) == "cicy" {
		return cicyChatHistoryData(target, index)
	}
	db, err := agentHistoryOpen(target)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	const query = `SELECT conversation_id, turn_key, q, a, thinking, model, status, q_time, a_time, created_at
	               FROM history_turns
	               WHERE agent_id = ?
	               ORDER BY id DESC
	               LIMIT 1 OFFSET ?`
	row := db.QueryRow(query, target, index)

	var convID, turnKey, q, a, thinking, model, status, qTime, aTime, createdAt string
	switch err := row.Scan(&convID, &turnKey, &q, &a, &thinking, &model, &status, &qTime, &aTime, &createdAt); err {
	case nil:
		return M{
			"pane_id":         target,
			"index":           index,
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
		}, nil
	case sql.ErrNoRows:
		return M{
			"pane_id": target,
			"index":   index,
			"found":   false,
		}, nil
	default:
		return nil, err
	}
}

func cicyChatHistoryData(target string, index int) (M, error) {
	workspace := paneWorkspace(target)
	if workspace == "" {
		return M{"pane_id": target, "index": index, "found": false}, nil
	}
	session := getCicySession(target, workspace)
	session.mu.Lock()
	messages := append([]M{}, session.messages...)
	session.mu.Unlock()
	type turn struct{ q, a string }
	turns := make([]turn, 0)
	for i := 0; i < len(messages); i++ {
		if role, _ := messages[i]["role"].(string); role != "user" {
			continue
		}
		q := cicyHistoryPlainText(messages[i]["content"])
		if q == "" { // tool_result user blocks are part of the active turn, not a new question
			continue
		}
		var answers []string
		for j := i + 1; j < len(messages); j++ {
			role, _ := messages[j]["role"].(string)
			if role == "user" && cicyHistoryPlainText(messages[j]["content"]) != "" {
				break
			}
			if role == "assistant" {
				if text := cicyHistoryPlainText(messages[j]["content"]); text != "" && cicyOutcomeKindFromText(text) == "" {
					answers = append(answers, text)
				}
			}
		}
		turns = append(turns, turn{q: q, a: strings.Join(answers, "\n")})
	}
	pos := len(turns) - 1 - index
	if pos < 0 || pos >= len(turns) {
		return M{"pane_id": target, "index": index, "found": false}, nil
	}
	selected := turns[pos]
	return M{"pane_id": target, "index": index, "found": true, "q": selected.q, "a": selected.a,
		"status": map[bool]string{true: "completed", false: "pending"}[selected.a != ""]}, nil
}

func cicyHistoryPlainText(content interface{}) string {
	if text, ok := content.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(cicyTextFromBlocks(content))
}
