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

func initDB() {
	var dsn string

	if p := os.Getenv("SQLITE_PATH"); p != "" {
		dsn = p
	} else {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".cicy")
		os.MkdirAll(dir, 0755)
		dsn = filepath.Join(dir, "data.db")
	}

	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatal(err)
	}

	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)
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

func (d *DB) DeleteOldLogs() string {
	return "DELETE FROM http_log WHERE ts < CAST(strftime('%s','now','-7 days') AS INTEGER)"
}

func (d *DB) CastText(col string) string { return col }

func (d *DB) Migrate() {
	stmts := []string{
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
			role TEXT, default_model TEXT, trust_level TEXT
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
			sent_at TEXT
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
		`CREATE TABLE IF NOT EXISTS http_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pane_id TEXT NOT NULL, method TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL, status_code INTEGER DEFAULT 0,
			req_kb REAL DEFAULT 0, res_kb REAL DEFAULT 0,
			data TEXT, ts INTEGER NOT NULL,
			created_at TEXT DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_http_log_pane ON http_log(pane_id)`,
		`CREATE INDEX IF NOT EXISTS idx_http_log_ts ON http_log(ts)`,
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
		`INSERT OR IGNORE INTO global_vars (key_name, value) VALUES ('worker_index', '20000')`,
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			log.Printf("[db] migrate error: %v\nSQL: %s", err, s[:minInt(len(s), 100)])
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
