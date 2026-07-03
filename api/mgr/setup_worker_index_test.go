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

// worker_index recovery parses the agent id ("w-<n>", shortPaneID semantics)
// out of every agent_config.pane_id and re-raises worker_index to the max <n>.
func TestSyncWorkerIndexFromBuiltinAgents(t *testing.T) {
	withTestStore(t)

	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?)",
		"w-1001:main.0", "CiCy", "/tmp/w-1001", "", "{}", "master", "", "cicy-claude", true, true,
	); err != nil {
		t.Fatalf("insert w-1001: %v", err)
	}
	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?)",
		"w-10002:main.0", "Codex", "/tmp/w-10002", "", "{}", "worker", "", "codex", true, true,
	); err != nil {
		t.Fatalf("insert w-10002: %v", err)
	}
	// A bare agent id (no ":main.0" suffix) must be tolerated too.
	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?)",
		"w-10007", "Bare", "/tmp/w-10007", "", "{}", "worker", "", "claude", true, true,
	); err != nil {
		t.Fatalf("insert w-10007: %v", err)
	}

	syncWorkerIndexToExistingAgents()

	var got int
	if err := store.QueryRow("SELECT value FROM global_vars WHERE key_name='worker_index'").Scan(&got); err != nil {
		t.Fatalf("read worker_index: %v", err)
	}
	if got != 10007 {
		t.Fatalf("worker_index = %d, want 10007 (max agent number parsed from pane ids)", got)
	}
}

// worker_index is only ever RAISED by recovery, never lowered — a dynamic
// value above the max surviving agent number must survive a restart.
func TestSyncWorkerIndexKeepsHigherDynamicValue(t *testing.T) {
	withTestStore(t)

	setWorkerIndex(20005)
	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?)",
		"w-1001:main.0", "CiCy", "/tmp/w-1001", "", "{}", "master", "", "cicy-claude", true, true,
	); err != nil {
		t.Fatalf("insert w-1001: %v", err)
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
