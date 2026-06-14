package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// DB wraps *sql.DB with SQLite-specific helpers.
type DB struct {
	*sql.DB
	Driver string // always "sqlite" in cicy-code
}

var store *DB

func defaultSQLitePath() string {
	return filepath.Join(cicyRootDir, "db", "data.db")
}

func initDB() {
	var dsn string

	if p := os.Getenv("SQLITE_PATH"); p != "" {
		dsn = p
	} else {
		dsn = defaultSQLitePath()
	}
	if err := os.MkdirAll(filepath.Dir(dsn), 0755); err != nil {
		log.Fatal(err)
	}

	// Open with a busy timeout so concurrent writers (e.g. the IM workers' QR-login
	// state updates) wait for the lock instead of failing immediately with SQLITE_BUSY.
	// The _pragma= form is applied to every connection the pool opens.
	openDSN := dsn
	if !strings.Contains(openDSN, "?") {
		openDSN = "file:" + openDSN + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	}
	raw, err := sql.Open("sqlite", openDSN)
	if err != nil {
		log.Fatal(err)
	}

	raw.SetMaxOpenConns(4)
	raw.SetMaxIdleConns(4)
	raw.Exec("PRAGMA journal_mode=WAL")
	raw.Exec("PRAGMA foreign_keys=ON")

	store = &DB{DB: raw, Driver: "sqlite"}
	log.Printf("[db] driver=sqlite dsn=%s", dsn)
}

func (d *DB) IsSQLite() bool { return true }

func (d *DB) Now() string     { return "datetime('now')" }
func (d *DB) UnixNow() string { return "strftime('%s','now')" }

func (d *DB) Upsert(table, uniqueCol string, cols []string, updateCols []string) string {
	ph := make([]string, len(cols))
	for i := range cols {
		ph[i] = "?"
	}
	insert := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ","), strings.Join(ph, ","))
	sets := make([]string, len(updateCols))
	for i, c := range updateCols {
		sets[i] = c + "=excluded." + c
	}
	return insert + fmt.Sprintf(" ON CONFLICT(%s) DO UPDATE SET %s", uniqueCol, strings.Join(sets, ","))
}

func (d *DB) InsertIgnore(table string, cols []string) string {
	ph := make([]string, len(cols))
	for i := range cols {
		ph[i] = "?"
	}
	return fmt.Sprintf("INSERT OR IGNORE INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ","), strings.Join(ph, ","))
}

func (d *DB) TokenPrefix() string {
	return "substr(token,1,8)||'...' as token_prefix"
}

func (d *DB) CastText(col string) string { return col }

func (d *DB) Migrate() {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS machines (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			machine_key TEXT NOT NULL UNIQUE,
			label TEXT DEFAULT '',
			host TEXT DEFAULT '',
			port INTEGER DEFAULT 8008,
			url TEXT NOT NULL DEFAULT '',
			token TEXT DEFAULT '',
			status TEXT DEFAULT 'unknown',
			last_seen_at TEXT,
			capabilities_json TEXT DEFAULT '{}',
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS workflows (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			description TEXT DEFAULT '',
			status TEXT DEFAULT 'pending',
			created_by TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			completed_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS agent_config (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pane_id TEXT NOT NULL UNIQUE,
			node_url TEXT DEFAULT '',
			title TEXT, ttyd_port INTEGER NOT NULL,
			workspace TEXT, init_script TEXT, proxy TEXT,
			tg_token TEXT, tg_chat_id TEXT, tg_enable INTEGER DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			ttyd_pid INTEGER, active INTEGER NOT NULL DEFAULT 1,
			private_mode INTEGER DEFAULT 0, allowed_users TEXT,
			proxy_enable INTEGER DEFAULT 0, agent_duty TEXT,
			preview TEXT, config TEXT, ttyd_preview TEXT,
			agent_type TEXT DEFAULT '', common_prompt TEXT,
			role TEXT, default_model TEXT, trust_level TEXT,
			allow_all_actions INTEGER DEFAULT 0,
			reply_in_chinese INTEGER DEFAULT 0,
			use_official_auth INTEGER DEFAULT 0,
			use_custom_gateway INTEGER DEFAULT 1,
			use_mitm INTEGER DEFAULT 1,
			machine_id INTEGER,
			source_kind TEXT DEFAULT 'local',
			source_ref TEXT DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS agent_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL, description TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS agent_queue (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pane_id TEXT NOT NULL, message TEXT NOT NULL,
			type TEXT DEFAULT 'message',
			status TEXT DEFAULT 'pending',
			priority INTEGER DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now')),
			sent_at TEXT,
			step_kind TEXT DEFAULT 'message',
			workflow_id INTEGER,
			parent_id INTEGER,
			step_index INTEGER DEFAULT 0,
			title TEXT DEFAULT '',
			result_summary TEXT DEFAULT '',
			result_payload TEXT DEFAULT '',
			target_machine_id INTEGER,
			target_pane_id TEXT DEFAULT '',
			created_by TEXT DEFAULT '',
			completed_at TEXT,
			workspace_id TEXT DEFAULT '',
			work_item_id TEXT DEFAULT '',
			artifact_id TEXT DEFAULT '',
			handoff_id TEXT DEFAULT '',
			employee_id TEXT DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS global_vars (
			key_name TEXT PRIMARY KEY, value TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS group_windows (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id INTEGER NOT NULL, win_id TEXT NOT NULL,
			win_type TEXT NOT NULL DEFAULT 'agent_ttyd',
			ref_id TEXT,
			pos_x REAL NOT NULL DEFAULT 20, pos_y REAL NOT NULL DEFAULT 20,
			width REAL NOT NULL DEFAULT 480, height REAL NOT NULL DEFAULT 320,
			z_index INTEGER NOT NULL DEFAULT 1,
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(group_id, win_id),
			FOREIGN KEY(group_id) REFERENCES agent_groups(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS pane_agents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pane_id TEXT NOT NULL, agent_name TEXT NOT NULL,
			status TEXT DEFAULT 'active',
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			UNIQUE(pane_id, agent_name)
		)`,
		`CREATE TABLE IF NOT EXISTS tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token TEXT NOT NULL UNIQUE,
			group_id INTEGER, pane_id TEXT,
			perms TEXT NOT NULL, note TEXT,
			expires_at TEXT, created_at TEXT DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS im_accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			platform TEXT NOT NULL,
			name TEXT DEFAULT '',
			secret TEXT DEFAULT '',
			config TEXT DEFAULT '{}',
			enabled INTEGER DEFAULT 1,
			state TEXT DEFAULT 'pending',
			state_detail TEXT DEFAULT '',
			bound_pane_id TEXT DEFAULT '',
			inbound_to_agent INTEGER DEFAULT 1,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_im_accounts_bound ON im_accounts(bound_pane_id)`,
		// agent_messages: cross-agent message store (status sent→done/failed) +
		// a pointer (conversation_id/turn_id) into the receiver's history_turns.
		// Reply content is NOT copied here — it is JOINed from history_turns. See
		// agent_messages_store.go.
		`CREATE TABLE IF NOT EXISTS agent_messages (
			id TEXT PRIMARY KEY,
			from_pane TEXT NOT NULL,
			to_pane TEXT NOT NULL,
			text TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'sent',
			callback INTEGER NOT NULL DEFAULT 1,
			replied INTEGER NOT NULL DEFAULT 0,
			from_conversation_id TEXT DEFAULT '',
			from_turn_id TEXT DEFAULT '',
			reply_conversation_id TEXT DEFAULT '',
			reply_turn_id TEXT DEFAULT '',
			reply_history_id INTEGER DEFAULT 0,
			created_at TEXT NOT NULL,
			completed_at TEXT DEFAULT '',
			error TEXT DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_msg_to ON agent_messages(to_pane, status)`,
		`CREATE INDEX IF NOT EXISTS idx_msg_from ON agent_messages(from_pane, created_at)`,
		fmt.Sprintf("INSERT OR IGNORE INTO global_vars (key_name, value) VALUES ('worker_index', '%d')", defaultWorkerIndex),
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			log.Printf("[db] migrate error: %v\nSQL: %s", err, s[:minInt(len(s), 100)])
		}
	}

	d.ensureColumn("agent_config", "machine_id", "INTEGER")
	d.ensureColumn("agent_config", "source_kind", "TEXT DEFAULT 'local'")
	d.ensureColumn("agent_config", "source_ref", "TEXT DEFAULT ''")
	d.ensureColumn("agent_config", "allow_all_actions", "INTEGER DEFAULT 0")
	d.ensureColumn("agent_config", "reply_in_chinese", "INTEGER DEFAULT 0")
	d.ensureColumn("agent_config", "use_official_auth", "INTEGER DEFAULT 0")
	d.ensureColumn("agent_config", "use_custom_gateway", "INTEGER DEFAULT 1")
	// Per-agent MITM audit toggle for the NON-gateway (official login) path.
	// Default ON: a non-gateway agent's HTTPS routes through the local MITM
	// (when running) so its turns are audited; the inspector exposes the switch
	// only when use_custom_gateway is off (gateway traffic is audited natively).
	d.ensureColumn("agent_config", "use_mitm", "INTEGER DEFAULT 1")
	// One-time migration: inverted semantic — convert use_official_auth → use_custom_gateway.
	// We force-set every row to the inverse of use_official_auth, but only once
	// (gated by config_migrations row), so manual edits to use_custom_gateway later
	// won't be clobbered.
	d.runOnceMigration("use_custom_gateway_v1", `UPDATE agent_config SET use_custom_gateway = CASE WHEN COALESCE(use_official_auth, 0) = 1 THEN 0 ELSE 1 END`)
	d.ensureColumn("agent_config", "inspector_notes", "TEXT DEFAULT ''")
	d.ensureColumn("agent_config", "inspector_notes_updated_at", "TEXT")
	// Memory-template selections made at creation time, persisted so forks can
	// inherit the same project/role layers (see composeAgentMemory).
	d.ensureColumn("agent_config", "project_template", "TEXT DEFAULT ''")
	d.ensureColumn("agent_config", "role_template", "TEXT DEFAULT ''")
	d.ensureColumn("pane_agents", "sort_order", "INTEGER DEFAULT 0")
	d.ensureColumn("agent_queue", "step_kind", "TEXT DEFAULT 'message'")
	d.ensureColumn("agent_queue", "workflow_id", "INTEGER")
	d.ensureColumn("agent_queue", "parent_id", "INTEGER")
	d.ensureColumn("agent_queue", "step_index", "INTEGER DEFAULT 0")
	d.ensureColumn("agent_queue", "title", "TEXT DEFAULT ''")
	d.ensureColumn("agent_queue", "result_summary", "TEXT DEFAULT ''")
	d.ensureColumn("agent_queue", "result_payload", "TEXT DEFAULT ''")
	d.ensureColumn("agent_queue", "target_machine_id", "INTEGER")
	d.ensureColumn("agent_queue", "target_pane_id", "TEXT DEFAULT ''")
	d.ensureColumn("agent_queue", "created_by", "TEXT DEFAULT ''")
	d.ensureColumn("agent_queue", "completed_at", "TEXT")
	d.ensureColumn("agent_queue", "workspace_id", "TEXT DEFAULT ''")
	d.ensureColumn("agent_queue", "work_item_id", "TEXT DEFAULT ''")
	d.ensureColumn("agent_queue", "artifact_id", "TEXT DEFAULT ''")
	d.ensureColumn("agent_queue", "handoff_id", "TEXT DEFAULT ''")
	d.ensureColumn("agent_queue", "employee_id", "TEXT DEFAULT ''")
	// #177: initiator-side conv/turn pointer, added to existing agent_messages
	// tables (the CREATE TABLE above already carries them for fresh installs).
	d.ensureColumn("agent_messages", "from_conversation_id", "TEXT DEFAULT ''")
	d.ensureColumn("agent_messages", "from_turn_id", "TEXT DEFAULT ''")

	// todo #104: the lite agent's agent_type "dispatcher" was renamed to the
	// product name "cicy". Migrate existing rows once, idempotently. Runs in the
	// NEW binary (which understands "cicy"), so it never breaks a live old daemon.
	if _, err := d.Exec("UPDATE agent_config SET agent_type='cicy' WHERE agent_type='dispatcher'"); err != nil {
		log.Printf("[migrate] rename agent_type dispatcher→cicy: %v", err)
	}
}

func (d *DB) ensureColumn(table, col, def string) {
	rows, err := d.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &defaultValue, &pk); err == nil && name == col {
			return
		}
	}
	stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, def)
	if _, err := d.Exec(stmt); err != nil {
		log.Printf("[db] ensure column error: %v SQL: %s", err, stmt)
	}
}

// runOnceMigration runs `sql` exactly once, tracked by `name` in the
// config_migrations table. Safe to call on every startup.
func (d *DB) runOnceMigration(name, sql string) {
	_, _ = d.Exec(`CREATE TABLE IF NOT EXISTS config_migrations (name TEXT PRIMARY KEY, applied_at TEXT)`)
	var applied string
	if err := d.QueryRow(`SELECT applied_at FROM config_migrations WHERE name=?`, name).Scan(&applied); err == nil && applied != "" {
		return
	}
	if _, err := d.Exec(sql); err != nil {
		log.Printf("[db] migration %s failed: %v", name, err)
		return
	}
	_, _ = d.Exec(`INSERT INTO config_migrations(name, applied_at) VALUES(?, datetime('now'))`, name)
	log.Printf("[db] applied migration %s", name)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
