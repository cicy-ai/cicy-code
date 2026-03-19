package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

// DB wraps *sql.DB with dialect-aware helpers.
type DB struct {
	*sql.DB
	Driver string // "mysql" or "sqlite"
}

var store *DB

func initDB() {
	var dsn, driver string

	if p := os.Getenv("SQLITE_PATH"); p != "" {
		dsn = p
		driver = "sqlite"
	} else if m := os.Getenv("MYSQL_DSN"); m != "" {
		dsn = m
		if !strings.Contains(dsn, "parseTime") {
			if strings.Contains(dsn, "?") {
				dsn += "&parseTime=true"
			} else {
				dsn += "?parseTime=true"
			}
		}
		driver = "mysql"
	} else {
		// Default: ~/.cicy/data.db
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".cicy")
		os.MkdirAll(dir, 0755)
		dsn = filepath.Join(dir, "data.db")
		driver = "sqlite"
	}

	raw, err := sql.Open(driver, dsn)
	if err != nil {
		log.Fatal(err)
	}

	if driver == "sqlite" {
		raw.SetMaxOpenConns(1)
		raw.SetMaxIdleConns(1)
		// Enable WAL mode for better concurrency
		raw.Exec("PRAGMA journal_mode=WAL")
		raw.Exec("PRAGMA foreign_keys=ON")
	} else {
		raw.SetMaxOpenConns(20)
		raw.SetMaxIdleConns(5)
		raw.SetConnMaxLifetime(5 * time.Minute)
	}

	store = &DB{DB: raw, Driver: driver}
	log.Printf("[db] driver=%s dsn=%s", driver, dsn)
}

func (d *DB) IsSQLite() bool { return d.Driver == "sqlite" }

// Now returns SQL expression for current timestamp.
func (d *DB) Now() string {
	if d.IsSQLite() {
		return "datetime('now')"
	}
	return "NOW()"
}

// UnixNow returns SQL expression for current unix timestamp.
func (d *DB) UnixNow() string {
	if d.IsSQLite() {
		return "strftime('%s','now')"
	}
	return "UNIX_TIMESTAMP(NOW())"
}

// Upsert generates INSERT ... ON CONFLICT/DUPLICATE KEY for a single unique key.
func (d *DB) Upsert(table, uniqueCol string, cols []string, updateCols []string) string {
	ph := make([]string, len(cols))
	for i := range cols {
		ph[i] = "?"
	}
	insert := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ","), strings.Join(ph, ","))

	if d.IsSQLite() {
		sets := make([]string, len(updateCols))
		for i, c := range updateCols {
			sets[i] = c + "=excluded." + c
		}
		return insert + fmt.Sprintf(" ON CONFLICT(%s) DO UPDATE SET %s", uniqueCol, strings.Join(sets, ","))
	}
	sets := make([]string, len(updateCols))
	for i, c := range updateCols {
		sets[i] = c + "=VALUES(" + c + ")"
	}
	return insert + " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ",")
}

// InsertIgnore generates INSERT IGNORE (MySQL) or INSERT OR IGNORE (SQLite).
func (d *DB) InsertIgnore(table string, cols []string) string {
	ph := make([]string, len(cols))
	for i := range cols {
		ph[i] = "?"
	}
	if d.IsSQLite() {
		return fmt.Sprintf("INSERT OR IGNORE INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ","), strings.Join(ph, ","))
	}
	return fmt.Sprintf("INSERT IGNORE INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ","), strings.Join(ph, ","))
}

// TokenPrefix returns SQL expression for first 8 chars of token + '...'.
func (d *DB) TokenPrefix() string {
	if d.IsSQLite() {
		return "substr(token,1,8)||'...' as token_prefix"
	}
	return "CONCAT(LEFT(token,8),'...') as token_prefix"
}

// DeleteOldLogs returns SQL to delete http_log older than 7 days.
func (d *DB) DeleteOldLogs() string {
	if d.IsSQLite() {
		return "DELETE FROM http_log WHERE ts < CAST(strftime('%s','now','-7 days') AS INTEGER)"
	}
	return "DELETE FROM http_log WHERE ts < UNIX_TIMESTAMP(NOW() - INTERVAL 7 DAY)"
}

// CastText returns SQL to cast a column to text for LIKE operations.
func (d *DB) CastText(col string) string {
	if d.IsSQLite() {
		return col // SQLite stores as text natively
	}
	return fmt.Sprintf("CAST(%s AS CHAR)", col)
}

// Migrate creates all tables if they don't exist.
func (d *DB) Migrate() {
	if d.IsSQLite() {
		d.migrateSQLite()
	} else {
		d.migrateMySQL()
	}
}

func (d *DB) migrateSQLite() {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agent_config (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pane_id TEXT NOT NULL UNIQUE,
			node_url TEXT DEFAULT 'http://localhost:13431',
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
		`CREATE TABLE IF NOT EXISTS auth_codes (
			code TEXT PRIMARY KEY, user_id TEXT NOT NULL,
			slug TEXT NOT NULL, vm_token TEXT NOT NULL,
			created_at TEXT DEFAULT (datetime('now')),
			used INTEGER DEFAULT 0
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
		`CREATE TABLE IF NOT EXISTS saas_users (
			id TEXT PRIMARY KEY, email TEXT UNIQUE NOT NULL,
			plan TEXT DEFAULT 'free', backend_url TEXT DEFAULT '',
			vm_url TEXT DEFAULT '', vm_token TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token TEXT NOT NULL UNIQUE,
			group_id INTEGER, pane_id TEXT,
			perms TEXT NOT NULL, note TEXT,
			expires_at TEXT, created_at TEXT DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS user_apps (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL,
			name TEXT NOT NULL, icon TEXT DEFAULT '',
			html TEXT, created_at TEXT DEFAULT (datetime('now'))
		)`,
		// Seed data
		`INSERT OR IGNORE INTO global_vars (key_name, value) VALUES ('worker_index', '20000')`,
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			log.Printf("[db] migrate error: %v\nSQL: %s", err, s[:minInt(len(s), 100)])
		}
	}
}

func (d *DB) migrateMySQL() {
	// MySQL uses schema.sql for initial setup; just ensure saas_users + user_apps exist
	d.Exec(`CREATE TABLE IF NOT EXISTS saas_users (
		id VARCHAR(36) PRIMARY KEY, email VARCHAR(255) UNIQUE NOT NULL,
		plan VARCHAR(20) DEFAULT 'free', backend_url VARCHAR(255) DEFAULT '',
		vm_url VARCHAR(255) DEFAULT '', vm_token VARCHAR(255) DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	d.Exec(`CREATE TABLE IF NOT EXISTS user_apps (
		id VARCHAR(64) PRIMARY KEY, user_id VARCHAR(64) NOT NULL,
		name VARCHAR(255) NOT NULL, icon VARCHAR(50) DEFAULT '',
		html LONGTEXT, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
