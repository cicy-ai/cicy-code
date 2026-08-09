// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"os"
	"strings"
	"time"
)

// agent_messages is the cross-agent message store: every agent→agent send is
// recorded as a queryable row whose status walks sent → done / failed, instead
// of leaning on a "✅ work done" chat line to express completion. The row keeps
// only a POINTER to the receiver's reply (conversation_id + turn_id), never a
// copy of the reply text — history_turns is the single source of truth for
// content (keyed by turn_id), so we JOIN it at read time.
//
// Storage note: agent_messages lives in the shared main DB (store, WAL,
// serialized), while history_turns lives in each agent's per-agent history.db
// (aiGatewayHistoryDir/history.db). Because the two tables sit in different
// SQLite files, the content JOIN is done in Go (lookupHistoryTurn), not a single
// cross-DB SQL statement.

type agentMessageRow struct {
	ID                  string `json:"id"`
	FromPane            string `json:"from_pane"`
	ToPane              string `json:"to_pane"`
	Text                string `json:"text"`
	Status              string `json:"status"`
	Callback            int    `json:"callback"`
	Replied             int    `json:"replied"`
	FromConversationID  string `json:"from_conversation_id"`
	FromTurnID          string `json:"from_turn_id"`
	ReplyConversationID string `json:"reply_conversation_id"`
	ReplyTurnID         string `json:"reply_turn_id"`
	ReplyHistoryID      int64  `json:"reply_history_id"`
	CreatedAt           string `json:"created_at"`
	CompletedAt         string `json:"completed_at"`
	Error               string `json:"error"`
}

// insertAgentMessage records a freshly-sent agent→agent message as status='sent'.
// fromPane may be empty (a --no-callback send whose originator we couldn't
// recover); we still record the row for the to-side link. fromConv/fromTurn are
// the INITIATOR's in-flight conversation/turn (best-effort, may be empty) — the
// from-side half of the causal link that pairs with reply_* (the receiver's
// turn, written at finalize). A blank id is a no-op.
func insertAgentMessage(id, fromPane, toPane, text string, callback bool, fromConv, fromTurn string) error {
	if store == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	cb := 0
	if callback {
		cb = 1
	}
	_, err := store.Exec(
		`INSERT INTO agent_messages (id, from_pane, to_pane, text, status, callback, replied, from_conversation_id, from_turn_id, created_at)
		 VALUES (?, ?, ?, ?, 'sent', ?, 0, ?, ?, ?)`,
		strings.TrimSpace(id), normPaneID(strings.TrimSpace(fromPane)), normPaneID(strings.TrimSpace(toPane)),
		text, cb, strings.TrimSpace(fromConv), strings.TrimSpace(fromTurn), time.Now().Format(time.RFC3339))
	return err
}

// markAgentMessageDone flips a message to status='done' and writes the reply
// pointer (conversation_id / turn_id / history_id). It returns the row's prior
// `replied` flag so finalize can decide whether to also push a chat line.
func markAgentMessageDone(msgID string, reply aiGatewayReplySnapshot) (replied bool, err error) {
	return updateAgentMessageTerminal(msgID, "done", reply, "")
}

// markAgentMessageFailed flips a message to status='failed', writing the reply
// pointer plus an error string. Returns the prior `replied` flag.
func markAgentMessageFailed(msgID string, reply aiGatewayReplySnapshot, errMsg string) (replied bool, err error) {
	return updateAgentMessageTerminal(msgID, "failed", reply, errMsg)
}

func updateAgentMessageTerminal(msgID, status string, reply aiGatewayReplySnapshot, errMsg string) (bool, error) {
	if store == nil || strings.TrimSpace(msgID) == "" {
		return false, nil
	}
	var rep int
	_ = store.QueryRow(`SELECT replied FROM agent_messages WHERE id=?`, msgID).Scan(&rep)
	_, err := store.Exec(
		`UPDATE agent_messages
		 SET status=?, completed_at=?, reply_conversation_id=?, reply_turn_id=?, reply_history_id=?, error=?
		 WHERE id=?`,
		status, time.Now().Format(time.RFC3339),
		strings.TrimSpace(reply.ConversationID), strings.TrimSpace(reply.TurnID), reply.HistoryID,
		strings.TrimSpace(errMsg), msgID)
	return rep == 1, err
}

// markAgentMessagesReplied marks every still-open (status='sent') message that
// went originalFrom → originalTo as replied=1. Called when the original
// recipient sends a message back to the original sender during the same turn —
// i.e. the agent already answered in-band, so finalize must NOT also push a
// redundant "work done" line.
func markAgentMessagesReplied(originalFrom, originalTo string) {
	if store == nil {
		return
	}
	originalFrom = normPaneID(strings.TrimSpace(originalFrom))
	originalTo = normPaneID(strings.TrimSpace(originalTo))
	if originalFrom == "" || originalTo == "" {
		return
	}
	_, _ = store.Exec(
		`UPDATE agent_messages SET replied=1 WHERE from_pane=? AND to_pane=? AND status='sent'`,
		originalFrom, originalTo)
}

type agentMessageFilter struct {
	From   string
	To     string
	Status string
	Open   bool
	Limit  int
}

func queryAgentMessages(f agentMessageFilter) ([]agentMessageRow, error) {
	if store == nil {
		return nil, nil
	}
	var where []string
	var args []interface{}
	if v := normPaneID(strings.TrimSpace(f.From)); v != "" {
		where = append(where, "from_pane=?")
		args = append(args, v)
	}
	if v := normPaneID(strings.TrimSpace(f.To)); v != "" {
		where = append(where, "to_pane=?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(f.Status); v != "" {
		where = append(where, "status=?")
		args = append(args, v)
	}
	if f.Open {
		where = append(where, "status='sent'")
	}
	q := `SELECT id, from_pane, to_pane, text, status, callback, replied,
	             from_conversation_id, from_turn_id,
	             reply_conversation_id, reply_turn_id, reply_history_id,
	             created_at, completed_at, error
	      FROM agent_messages`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at DESC, rowid DESC"
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q += " LIMIT ?"
	args = append(args, limit)

	rows, err := store.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentMessageRow{}
	for rows.Next() {
		var m agentMessageRow
		if err := rows.Scan(&m.ID, &m.FromPane, &m.ToPane, &m.Text, &m.Status, &m.Callback, &m.Replied,
			&m.FromConversationID, &m.FromTurnID,
			&m.ReplyConversationID, &m.ReplyTurnID, &m.ReplyHistoryID,
			&m.CreatedAt, &m.CompletedAt, &m.Error); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// agentMessageTurn is the slice of the receiver's history_turns row that a
// message points at: the question it answered, the answer, and the raw item
// blocks (tool_calls live inside item_json).
type agentMessageTurn struct {
	Q        string `json:"q,omitempty"`
	A        string `json:"a,omitempty"`
	ItemJSON string `json:"item_json,omitempty"`
}

// lookupHistoryTurn resolves a message's reply pointer to the receiver's actual
// turn content by opening that agent's per-agent history.db and selecting the
// (conversation_id, turn_key) row. It never creates a history.db: a missing file
// just means "no content yet" and returns ok=false.
func lookupHistoryTurn(agentShortID, conversationID, turnID string) (agentMessageTurn, bool) {
	agentShortID = strings.TrimSpace(agentShortID)
	conversationID = strings.TrimSpace(conversationID)
	turnID = strings.TrimSpace(turnID)
	if agentShortID == "" || conversationID == "" || turnID == "" {
		return agentMessageTurn{}, false
	}
	if _, err := os.Stat(agentHistoryDBPath(agentShortID)); err != nil {
		return agentMessageTurn{}, false
	}
	db, err := agentHistoryOpen(agentShortID)
	if err != nil {
		return agentMessageTurn{}, false
	}
	defer db.Close()
	var t agentMessageTurn
	if err := db.QueryRow(
		`SELECT q, a, item_json FROM history_turns WHERE conversation_id=? AND turn_key=?`,
		conversationID, turnID).Scan(&t.Q, &t.A, &t.ItemJSON); err != nil {
		return agentMessageTurn{}, false
	}
	return t, true
}

// stampedSenderID recovers the sender pane id from a 📮 [<sender>] … stamped
// message body — the best-effort fallback when a --no-callback send carries no
// callback_to. Returns "" when the text isn't stamped.
func stampedSenderID(text string) string {
	const pfx = "📮 ["
	if !strings.HasPrefix(text, pfx) {
		return ""
	}
	rest := text[len(pfx):]
	if i := strings.IndexByte(rest, ']'); i > 0 {
		return strings.TrimSpace(rest[:i])
	}
	return ""
}

// handleAgentMessages serves GET /api/agent/messages — the read-only link view.
// It lists agent_messages (filtered by from/to/status/open) and, for each row,
// JOINs in the receiver's turn (q / answer / tool_calls via item_json) so a
// caller can trace one message all the way to what the receiver actually did.
func handleAgentMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	q := r.URL.Query()
	open := q.Get("open")
	resp, err := agentMessagesData(agentMessageFilter{
		From:   q.Get("from"),
		To:     q.Get("to"),
		Status: q.Get("status"),
		Open:   open == "1" || open == "true",
	})
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, resp)
}

func agentMessagesData(filter agentMessageFilter) (M, error) {
	rows, err := queryAgentMessages(filter)
	if err != nil {
		return nil, err
	}
	type outRow struct {
		agentMessageRow
		FromTurn *agentMessageTurn `json:"from_turn,omitempty"`
		Turn     *agentMessageTurn `json:"turn,omitempty"`
	}
	out := make([]outRow, 0, len(rows))
	for _, m := range rows {
		o := outRow{agentMessageRow: m}
		// from-side: what the initiator was doing when it sent (its own history.db).
		if t, ok := lookupHistoryTurn(shortPaneID(m.FromPane), m.FromConversationID, m.FromTurnID); ok {
			o.FromTurn = &t
		}
		// to-side: what the receiver actually did this turn (its history.db).
		if t, ok := lookupHistoryTurn(shortPaneID(m.ToPane), m.ReplyConversationID, m.ReplyTurnID); ok {
			o.Turn = &t
		}
		out = append(out, o)
	}
	return M{"messages": out}, nil
}
