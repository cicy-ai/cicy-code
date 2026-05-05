package main

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func agentHistoryNormalizePersistedItem(item M, record aiGatewayMessageRecord) M {
	if item == nil {
		item = M{}
	}
	if question := strings.TrimSpace(aiGatewaySanitizeUserQuestion(aiGatewayString(item["q"]))); question != "" {
		item["q"] = question
	}
	steps, _ := item["steps"].([]M)
	if steps == nil {
		switch raw := item["steps"].(type) {
		case []interface{}:
			steps = make([]M, 0, len(raw))
			for _, part := range raw {
				if step := aiGatewayMap(part); len(step) > 0 {
					steps = append(steps, step)
				}
			}
		default:
			steps = []M{}
		}
	}
	answer := strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(item["a"]), record.A))
	hasText := false
	for _, step := range steps {
		if strings.TrimSpace(aiGatewayString(step["type"])) != "text" {
			continue
		}
		if strings.TrimSpace(aiGatewayString(step["text"])) != "" {
			hasText = true
			break
		}
	}
	if !hasText && answer != "" {
		steps = append(steps, M{"type": "text", "text": answer})
	}
	item["steps"] = steps
	item["a"] = answer
	return item
}

type agentHistoryPage struct {
	Items          []M
	ConversationID string
	NextBefore     int64
	HasMore        bool
}

func agentHistoryDBPath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "history.db")
}

func agentHistoryOpen(agentID string) (*sql.DB, error) {
	path := agentHistoryDBPath(agentID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=3000"); err != nil {
		db.Close()
		return nil, err
	}
	if err := agentHistoryMigrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func agentHistoryMigrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS conversations (
			id TEXT PRIMARY KEY,
			agent_id TEXT NOT NULL,
			provider TEXT DEFAULT '',
			model TEXT DEFAULT '',
			started_at TEXT DEFAULT '',
			updated_at TEXT DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS history_turns (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id TEXT NOT NULL,
			turn_key TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			q TEXT DEFAULT '',
			a TEXT DEFAULT '',
			thinking TEXT DEFAULT '',
			model TEXT DEFAULT '',
			status TEXT DEFAULT '',
			q_time TEXT DEFAULT '',
			a_time TEXT DEFAULT '',
			created_at TEXT DEFAULT '',
			updated_at TEXT DEFAULT '',
			item_json TEXT NOT NULL,
			UNIQUE(conversation_id, turn_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_history_turns_agent_id ON history_turns(agent_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_history_turns_conversation_id ON history_turns(conversation_id, id)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func agentHistoryFallbackConversationID(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot) string {
	if id := strings.TrimSpace(current.ConversationID); id != "" {
		return id
	}
	for _, req := range reply.HTTPRequests {
		if id := strings.TrimSpace(req.ConversationID); id != "" {
			return id
		}
	}
	return agentID
}

func agentHistoryTurnKey(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot, record aiGatewayMessageRecord) string {
	if id := strings.TrimSpace(reply.TurnID); id != "" {
		return id
	}
	if id := strings.TrimSpace(current.TurnID); id != "" {
		return id
	}
	sum := sha1.Sum([]byte(strings.Join([]string{
		agentID,
		strings.TrimSpace(record.QTime),
		strings.TrimSpace(record.ATime),
		strings.TrimSpace(record.Q),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func agentHistoryFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func agentHistoryFindMergeTarget(tx *sql.Tx, agentID string, record aiGatewayMessageRecord) (conversationID string, turnKey string, ok bool, err error) {
	question := agentInspectorCanonicalQuestion(record.Q)
	if strings.TrimSpace(question) == "" {
		return "", "", false, nil
	}
	rows, err := tx.Query(
		`SELECT conversation_id, turn_key, q, q_time, a_time
		 FROM history_turns
		 WHERE agent_id = ?
		 ORDER BY id DESC
		 LIMIT 8`,
		agentID,
	)
	if err != nil {
		return "", "", false, err
	}
	defer rows.Close()

	for rows.Next() {
		var existingConversationID, existingTurnKey, existingQ, existingQTime, existingATime string
		if err := rows.Scan(&existingConversationID, &existingTurnKey, &existingQ, &existingQTime, &existingATime); err != nil {
			return "", "", false, err
		}
		existing := aiGatewayMessageRecord{
			Q:     existingQ,
			QTime: existingQTime,
			ATime: existingATime,
		}
		if !agentInspectorShouldMergeHistoryRecord(existing, record) {
			continue
		}
		return existingConversationID, existingTurnKey, true, nil
	}
	if err := rows.Err(); err != nil {
		return "", "", false, err
	}
	return "", "", false, nil
}

func agentHistoryUpsertRecordWithItem(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot, record aiGatewayMessageRecord, item M) error {
	currentTurns := agentInspectorBuildCurrentOnlyPersistedTurns(current)
	if item == nil {
		if len(currentTurns) > 0 {
			item = currentTurns[len(currentTurns)-1]
		} else {
			turns := agentInspectorBuildChatTurnsFromRecords([]aiGatewayMessageRecord{record}, aiGatewayCurrentSnapshot{Model: current.Model}, aiGatewayReplySnapshot{})
			if len(turns) == 0 {
				return nil
			}
			item = turns[0]
		}
	} else if len(item) == 0 {
		turns := agentInspectorBuildChatTurnsFromRecords([]aiGatewayMessageRecord{record}, aiGatewayCurrentSnapshot{Model: current.Model}, aiGatewayReplySnapshot{})
		if len(turns) == 0 {
			return nil
		}
		item = turns[0]
	}
	item["q"] = strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(item["q"]), record.Q))
	item["a"] = strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(item["a"]), record.A))
	if model := strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(item["model"]), record.Model, aiGatewayReplyPrimaryModel(reply), current.Model)); model != "" {
		item["model"] = model
	}
	if status := strings.TrimSpace(aiGatewayFirstNonEmpty(reply.Status, current.Status, aiGatewayString(item["status"]), "done")); status != "" {
		switch strings.ToLower(status) {
		case "completed", "idle":
			item["status"] = "text"
		default:
			item["status"] = status
		}
	}
	startTS := int64(0)
	if ts, err := time.Parse(time.RFC3339, agentHistoryFirstNonEmpty(record.QTime, reply.StartedAt, current.StartedAt, current.Timestamp)); err == nil {
		startTS = ts.Unix()
	}
	endTS := startTS
	if ts, err := time.Parse(time.RFC3339, agentHistoryFirstNonEmpty(record.ATime, reply.UpdatedAt, current.UpdatedAt, record.QTime)); err == nil {
		endTS = ts.Unix()
	}
	if startTS > 0 {
		item["start_ts"] = startTS
	}
	if endTS > 0 {
		item["ts"] = endTS
	}
	item = agentHistoryNormalizePersistedItem(item, record)
	itemBody, err := json.Marshal(item)
	if err != nil {
		return err
	}
	db, err := agentHistoryOpen(agentID)
	if err != nil {
		return err
	}
	defer db.Close()

	conversationID := agentHistoryFallbackConversationID(agentID, current, reply)
	turnKey := agentHistoryTurnKey(agentID, current, reply, record)
	now := time.Now().UTC().Format(time.RFC3339)
	createdAt := agentHistoryFirstNonEmpty(record.QTime, reply.StartedAt, current.StartedAt, current.Timestamp, now)
	updatedAt := agentHistoryFirstNonEmpty(record.ATime, reply.UpdatedAt, current.UpdatedAt, createdAt, now)
	model := agentHistoryFirstNonEmpty(record.Model, aiGatewayReplyPrimaryModel(reply), current.Model)
	status := strings.TrimSpace(aiGatewayFirstNonEmpty(reply.Status, current.Status, "done"))

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if existingConversationID, existingTurnKey, found, err := agentHistoryFindMergeTarget(tx, agentID, record); err != nil {
		return err
	} else if found {
		conversationID = existingConversationID
		turnKey = existingTurnKey
	}

	if _, err := tx.Exec(
		`INSERT INTO conversations (id, agent_id, provider, model, started_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   provider=excluded.provider,
		   model=excluded.model,
		   updated_at=excluded.updated_at`,
		conversationID,
		agentID,
		strings.TrimSpace(current.Provider),
		model,
		createdAt,
		updatedAt,
	); err != nil {
		return err
	}

	if _, err := tx.Exec(
		`INSERT INTO history_turns (
		   conversation_id, turn_key, agent_id, q, a, thinking, model, status,
		   q_time, a_time, created_at, updated_at, item_json
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(conversation_id, turn_key) DO UPDATE SET
		   q=excluded.q,
		   a=excluded.a,
		   thinking=excluded.thinking,
		   model=excluded.model,
		   status=excluded.status,
		   q_time=excluded.q_time,
		   a_time=excluded.a_time,
		   updated_at=excluded.updated_at,
		   item_json=excluded.item_json`,
		conversationID,
		turnKey,
		agentID,
		strings.TrimSpace(record.Q),
		strings.TrimSpace(record.A),
		strings.TrimSpace(record.Thinking),
		model,
		status,
		strings.TrimSpace(record.QTime),
		strings.TrimSpace(record.ATime),
		createdAt,
		updatedAt,
		string(itemBody),
	); err != nil {
		return err
	}

	return tx.Commit()
}

func agentHistoryUpsertRecord(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot, record aiGatewayMessageRecord) error {
	return agentHistoryUpsertRecordWithItem(agentID, current, reply, record, nil)
}

func agentHistoryLoadPage(agentID string, conversationID string, limit int, before int64) (agentHistoryPage, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > agentInspectorHistoryMaxLimit {
		limit = agentInspectorHistoryMaxLimit
	}
	db, err := agentHistoryOpen(agentID)
	if err != nil {
		return agentHistoryPage{}, err
	}
	defer db.Close()

	conversationID = strings.TrimSpace(conversationID)
	args := []interface{}{agentID}
	where := `agent_id = ?`
	if conversationID != "" {
		where += ` AND conversation_id = ?`
		args = append(args, conversationID)
	}
	if before > 0 {
		where += ` AND id < ?`
		args = append(args, before)
	}
	args = append(args, limit+1)
	rows, err := db.Query(
		`SELECT id, item_json FROM history_turns WHERE `+where+` ORDER BY id DESC LIMIT ?`,
		args...,
	)
	if err != nil {
		return agentHistoryPage{}, err
	}
	defer rows.Close()

	type rowItem struct {
		id   int64
		item M
	}
	raw := make([]rowItem, 0, limit+1)
	for rows.Next() {
		var id int64
		var itemText string
		if err := rows.Scan(&id, &itemText); err != nil {
			return agentHistoryPage{}, err
		}
		item := M{}
		if err := json.Unmarshal([]byte(itemText), &item); err != nil {
			continue
		}
		item = agentHistoryNormalizePersistedItem(item, aiGatewayMessageRecord{
			A: aiGatewayString(item["a"]),
		})
		item["history_id"] = id
		raw = append(raw, rowItem{id: id, item: item})
	}
	if err := rows.Err(); err != nil {
		return agentHistoryPage{}, err
	}

	hasMore := len(raw) > limit
	if hasMore {
		raw = raw[:limit]
	}
	items := make([]M, 0, len(raw))
	nextBefore := int64(0)
	if len(raw) > 0 {
		nextBefore = raw[len(raw)-1].id
	}
	for i := len(raw) - 1; i >= 0; i-- {
		items = append(items, raw[i].item)
	}
	if !hasMore {
		nextBefore = 0
	}
	return agentHistoryPage{
		Items:          items,
		ConversationID: conversationID,
		NextBefore:     nextBefore,
		HasMore:        hasMore,
	}, nil
}
