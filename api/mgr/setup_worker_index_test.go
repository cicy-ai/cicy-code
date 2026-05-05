package main

import (
	"path/filepath"
	"testing"
)

func withTestStore(t *testing.T) {
	t.Helper()
	prev := store
	dbPath := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("SQLITE_PATH", dbPath)
	initDB()
	store.Migrate()
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
		store = prev
	})
}

func TestSyncWorkerIndexFromBuiltinAgents(t *testing.T) {
	withTestStore(t)

	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		"w-10001:main.0", "CiCy", 10001, "/tmp/w-10001", "", "{}", "master", "", "cicy-claude", true, true,
	); err != nil {
		t.Fatalf("insert w-10001: %v", err)
	}
	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		"w-10002:main.0", "Codex", 10002, "/tmp/w-10002", "", "{}", "worker", "", "codex", true, true,
	); err != nil {
		t.Fatalf("insert w-10002: %v", err)
	}

	syncWorkerIndexToExistingAgents()

	var got int
	if err := store.QueryRow("SELECT value FROM global_vars WHERE key_name='worker_index'").Scan(&got); err != nil {
		t.Fatalf("read worker_index: %v", err)
	}
	if got != 10002 {
		t.Fatalf("worker_index = %d, want 10002", got)
	}
}

func TestSyncWorkerIndexKeepsHigherDynamicValue(t *testing.T) {
	withTestStore(t)

	setWorkerIndex(20005)
	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		"w-10001:main.0", "CiCy", 10001, "/tmp/w-10001", "", "{}", "master", "", "cicy-claude", true, true,
	); err != nil {
		t.Fatalf("insert w-10001: %v", err)
	}

	syncWorkerIndexToExistingAgents()

	var got int
	if err := store.QueryRow("SELECT value FROM global_vars WHERE key_name='worker_index'").Scan(&got); err != nil {
		t.Fatalf("read worker_index: %v", err)
	}
	if got != 20005 {
		t.Fatalf("worker_index = %d, want 20005", got)
	}
}
