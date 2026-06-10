package main

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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
type agentHistoryIDPage struct {
	Items          []int64
	ConversationID string
	NextBefore     int64
	HasMore        bool
}
type agentHistoryPersistedRecord struct {
	ID     int64
	Item   M
	Record aiGatewayMessageRecord
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
		`CREATE TABLE IF NOT EXISTS conversations (id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, provider TEXT DEFAULT '', model TEXT DEFAULT '', started_at TEXT DEFAULT '', updated_at TEXT DEFAULT '', flushed_turn_count INTEGER DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS history_turns (id INTEGER PRIMARY KEY AUTOINCREMENT, conversation_id TEXT NOT NULL, turn_key TEXT NOT NULL, agent_id TEXT NOT NULL, q TEXT DEFAULT '', a TEXT DEFAULT '', thinking TEXT DEFAULT '', model TEXT DEFAULT '', status TEXT DEFAULT '', q_time TEXT DEFAULT '', a_time TEXT DEFAULT '', created_at TEXT DEFAULT '', updated_at TEXT DEFAULT '', item_json TEXT NOT NULL, UNIQUE(conversation_id, turn_key))`,
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
	sum := sha1.Sum([]byte(strings.Join([]string{agentID, strings.TrimSpace(record.QTime), strings.TrimSpace(record.ATime), strings.TrimSpace(record.Q)}, "\x00")))
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

func agentHistoryFindMergeTarget(tx *sql.Tx, agentID string, record aiGatewayMessageRecord) (string, string, bool, error) {
	question := agentInspectorCanonicalQuestion(record.Q)
	if strings.TrimSpace(question) == "" {
		return "", "", false, nil
	}
	rows, err := tx.Query(`SELECT conversation_id, turn_key, q, q_time, a_time FROM history_turns WHERE agent_id = ? ORDER BY id DESC LIMIT 8`, agentID)
	if err != nil {
		return "", "", false, err
	}
	defer rows.Close()
	for rows.Next() {
		var existingConversationID, existingTurnKey, existingQ, existingQTime, existingATime string
		if err := rows.Scan(&existingConversationID, &existingTurnKey, &existingQ, &existingQTime, &existingATime); err != nil {
			return "", "", false, err
		}
		existing := aiGatewayMessageRecord{Q: existingQ, QTime: existingQTime, ATime: existingATime}
		if !agentInspectorShouldMergeHistoryRecord(existing, record) {
			continue
		}
		return existingConversationID, existingTurnKey, true, nil
	}
	return "", "", false, rows.Err()
}

func agentHistoryUpsertRecordWithItem(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot, record aiGatewayMessageRecord, item M) (int64, error) {
	if item == nil {
		currentTurns := agentInspectorBuildCurrentOnlyPersistedTurns(current)
		if len(currentTurns) > 0 {
			item = currentTurns[len(currentTurns)-1]
		} else {
			turns := agentInspectorBuildChatTurnsFromRecords([]aiGatewayMessageRecord{record}, aiGatewayCurrentSnapshot{Model: current.Model}, aiGatewayReplySnapshot{})
			if len(turns) == 0 {
				return 0, nil
			}
			item = turns[0]
		}
	}
	item["q"] = strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(item["q"]), record.Q))
	item["a"] = strings.TrimSpace(aiGatewayFirstNonEmpty(aiGatewayString(item["a"]), record.A))
	item = agentHistoryNormalizePersistedItem(item, record)
	itemBody, err := json.Marshal(item)
	if err != nil {
		return 0, err
	}
	db, err := agentHistoryOpen(agentID)
	if err != nil {
		return 0, err
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
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO conversations (id, agent_id, provider, model, started_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET provider=excluded.provider, model=excluded.model, updated_at=excluded.updated_at`, conversationID, agentID, strings.TrimSpace(current.Provider), model, createdAt, updatedAt); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO history_turns (conversation_id, turn_key, agent_id, q, a, thinking, model, status, q_time, a_time, created_at, updated_at, item_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(conversation_id, turn_key) DO UPDATE SET q=excluded.q, a=excluded.a, thinking=excluded.thinking, model=excluded.model, status=excluded.status, q_time=excluded.q_time, a_time=excluded.a_time, updated_at=excluded.updated_at, item_json=excluded.item_json`, conversationID, turnKey, agentID, strings.TrimSpace(record.Q), strings.TrimSpace(record.A), strings.TrimSpace(record.Thinking), model, status, strings.TrimSpace(record.QTime), strings.TrimSpace(record.ATime), createdAt, updatedAt, string(itemBody)); err != nil {
		return 0, err
	}
	var historyID int64
	if err := tx.QueryRow(`SELECT id FROM history_turns WHERE conversation_id = ? AND turn_key = ? LIMIT 1`, conversationID, turnKey).Scan(&historyID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return historyID, nil
}

func agentHistoryUpsertRecordWithItemAndCount(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot, record aiGatewayMessageRecord, item M, flushedTurnCount *int) (int64, error) {
	return agentHistoryUpsertRecordWithItem(agentID, current, reply, record, item)
}
func agentHistoryUpsertRecord(agentID string, current aiGatewayCurrentSnapshot, reply aiGatewayReplySnapshot, record aiGatewayMessageRecord) (int64, error) {
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
	rows, err := db.Query(`SELECT id, item_json FROM history_turns WHERE `+where+` ORDER BY id DESC LIMIT ?`, args...)
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
		item = agentHistoryNormalizePersistedItem(item, aiGatewayMessageRecord{A: aiGatewayString(item["a"])})
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
	return agentHistoryPage{Items: items, ConversationID: conversationID, NextBefore: nextBefore, HasMore: hasMore}, nil
}

func agentHistoryLoadCurrentItemsPage(agentID string, conversationID string, limit int, before int64) (agentHistoryPage, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > agentInspectorHistoryMaxLimit {
		limit = agentInspectorHistoryMaxLimit
	}
	resolvedConversationID, maxID, err := agentHistoryCurrentMaxID(agentID, conversationID)
	if err != nil {
		return agentHistoryPage{}, err
	}
	if resolvedConversationID == "" || maxID <= 0 {
		return agentHistoryPage{ConversationID: resolvedConversationID}, nil
	}
	current, err := aiGatewayReadCurrentSnapshot(agentID)
	if err != nil {
		return agentHistoryPage{}, err
	}
	items := agentHistoryCurrentBodyItems(current)
	if len(items) == 0 {
		return agentHistoryPage{ConversationID: resolvedConversationID}, nil
	}
	upper := maxID
	if before > 0 && before-1 < upper {
		upper = before - 1
	}
	if upper <= 0 {
		return agentHistoryPage{ConversationID: resolvedConversationID}, nil
	}
	lower := upper - int64(limit) + 1
	if lower < 1 {
		lower = 1
	}
	out := make([]M, 0, limit)
	for _, item := range items {
		itemID := int64(aiGatewayInt(item["id"]))
		if itemID < lower || itemID > upper {
			continue
		}
		next := aiGatewayCloneAnyMap(item)
		next["history_id"] = itemID
		next["conversation_id"] = resolvedConversationID
		out = append(out, next)
	}
	sort.Slice(out, func(i, j int) bool {
		return int64(aiGatewayInt(out[i]["history_id"])) < int64(aiGatewayInt(out[j]["history_id"]))
	})
	nextBefore := int64(0)
	hasMore := lower > 1
	if len(out) > 0 {
		nextBefore = int64(aiGatewayInt(out[0]["history_id"]))
	}
	if !hasMore {
		nextBefore = 0
	}
	return agentHistoryPage{Items: out, ConversationID: resolvedConversationID, NextBefore: nextBefore, HasMore: hasMore}, nil
}

func agentHistoryLoadIDPage(agentID string, conversationID string, limit int, before int64) (agentHistoryIDPage, error) {
	page, err := agentHistoryLoadPage(agentID, conversationID, limit, before)
	if err != nil {
		return agentHistoryIDPage{}, err
	}
	items := make([]int64, 0, len(page.Items))
	for _, item := range page.Items {
		if id := int64(aiGatewayInt(item["history_id"])); id > 0 {
			items = append(items, id)
		}
	}
	return agentHistoryIDPage{Items: items, ConversationID: page.ConversationID, NextBefore: page.NextBefore, HasMore: page.HasMore}, nil
}

func agentHistoryLoadRecordsAfter(agentID string, afterID int64, limit int) ([]agentHistoryPersistedRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	db, err := agentHistoryOpen(agentID)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, item_json, q, a, q_time, a_time, model, thinking FROM history_turns WHERE agent_id = ? AND id > ? ORDER BY id ASC LIMIT ?`, agentID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]agentHistoryPersistedRecord, 0, limit)
	for rows.Next() {
		var entry agentHistoryPersistedRecord
		var itemText string
		if err := rows.Scan(&entry.ID, &itemText, &entry.Record.Q, &entry.Record.A, &entry.Record.QTime, &entry.Record.ATime, &entry.Record.Model, &entry.Record.Thinking); err != nil {
			return nil, err
		}
		item := M{}
		if err := json.Unmarshal([]byte(itemText), &item); err == nil {
			entry.Item = agentHistoryNormalizePersistedItem(item, entry.Record)
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func agentHistoryLatestID(agentID string) (int64, error) {
	db, err := agentHistoryOpen(agentID)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var id int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM history_turns WHERE agent_id = ?`, agentID).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func agentHistoryCurrentMaxID(agentID string, conversationID string) (string, int64, error) {
	current, err := aiGatewayReadCurrentSnapshot(agentID)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, nil
		}
		return "", 0, err
	}
	resolvedConversationID := strings.TrimSpace(current.ConversationID)
	if candidate := strings.TrimSpace(conversationID); candidate != "" && candidate != resolvedConversationID {
		return candidate, 0, nil
	}
	if current.MaxHistoryID > 0 {
		return resolvedConversationID, int64(current.MaxHistoryID), nil
	}
	items := agentHistoryCurrentBodyItems(current)
	maxID := int64(0)
	for _, item := range items {
		itemID := int64(aiGatewayInt(item["id"]))
		if itemID > maxID {
			maxID = itemID
		}
	}
	return resolvedConversationID, maxID, nil
}

func agentHistoryLoadCurrentItemByID(agentID string, conversationID string, historyID int64) (string, M, bool, error) {
	resolvedConversationID, maxID, err := agentHistoryCurrentMaxID(agentID, conversationID)
	if err != nil {
		return "", nil, false, err
	}
	if resolvedConversationID == "" || historyID <= 0 || historyID > maxID {
		return resolvedConversationID, nil, false, nil
	}
	current, err := aiGatewayReadCurrentSnapshot(agentID)
	if err != nil {
		if os.IsNotExist(err) {
			return resolvedConversationID, nil, false, nil
		}
		return "", nil, false, err
	}
	for _, item := range agentHistoryCurrentBodyItems(current) {
		itemID := int64(aiGatewayInt(item["id"]))
		if itemID != historyID {
			continue
		}
		next := aiGatewayCloneAnyMap(item)
		next["history_id"] = historyID
		next["conversation_id"] = resolvedConversationID
		return resolvedConversationID, next, true, nil
	}
	return resolvedConversationID, nil, false, nil
}

// cicyTagCompactSummaryAsSystem 把 cicy 压缩注入的那条摘要消息(wire 上必须是 role:user ——
// deepseek 原生 /anthropic 端点不接受 messages 里的 system role,且无翻译层可剥)在**展示**时
// 改标成 role:system。前端对 system 项有现成折叠(SystemNoticeCard),于是摘要变成一个收起的
// 系统分隔条、不再霸屏成一条 q。学 claude:压缩走 system,不当用户提问。这是唯一能加标记的地方
// (web 读的就是 wire 快照,wire 不能动)。用 cicy 自己的常量判定,权威、就此一处。
func cicyTagCompactSummaryAsSystem(messages []map[string]interface{}) {
	marker := strings.TrimSpace(cicyCompactSummaryPrefix)
	for _, m := range messages {
		if r, _ := m["role"].(string); r != "user" {
			continue
		}
		if s, ok := m["content"].(string); ok && strings.HasPrefix(strings.TrimSpace(s), marker) {
			m["role"] = "system"
		}
	}
}

// cicyOutcomeText pulls the first text block out of a message's content, whether
// the content is a bare string or the usual array of typed blocks (JSON-loaded as
// []interface{}). Used to spot the cicyOutcomePrefix marker.
func cicyOutcomeText(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		for _, b := range c {
			if bm, ok := b.(map[string]interface{}); ok {
				if t, _ := bm["type"].(string); t == "text" {
					if s, ok := bm["text"].(string); ok {
						return s
					}
				}
			}
		}
	}
	return ""
}

// cicyTagOutcome cleans the synthetic "turn produced no reply" marker
// (cicyOutcomePrefix, written by agent_cicy on a cancelled or post-retry-failed
// turn) for the web. It stays a role:assistant message — the web renders it as a
// normal assistant output (avatar, left-aligned) — but the raw marker text is
// replaced with a readable label and cicy_outcome is exposed so the UI can style
// it (failed/cancelled) and offer 重试 on the latest turn. The wire copy
// (session.messages) is untouched; this only rewrites the served display copy.
func cicyTagOutcome(messages []map[string]interface{}) {
	for _, m := range messages {
		if r, _ := m["role"].(string); r != "assistant" {
			continue
		}
		kind := cicyOutcomeKindFromText(cicyOutcomeText(m["content"]))
		if kind == "" {
			continue
		}
		label := "生成失败"
		if kind == "cancelled" {
			label = "已停止生成"
		}
		m["content"] = label
		m["cicy_outcome"] = kind // "cancelled" | "error" — UI styles it + 重试 on latest
	}
}

func agentHistoryCurrentBodyItems(current aiGatewayCurrentSnapshot) []map[string]interface{} {
	body := aiGatewayMap(current.Body)
	if len(body) == 0 {
		return nil
	}
	if messages := aiGatewayExtractMessages(body); len(messages) > 0 {
		cicyTagCompactSummaryAsSystem(messages)
		cicyTagOutcome(messages)
		return messages
	}
	inputs := aiGatewayExtractInputItems(body)
	out := make([]map[string]interface{}, 0, len(inputs))
	for _, raw := range inputs {
		if item := aiGatewayMap(raw); len(item) > 0 {
			out = append(out, item)
		}
	}
	return out
}
