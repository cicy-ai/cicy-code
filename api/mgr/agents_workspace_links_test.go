package main

import (
	"path/filepath"
	"testing"
)

func TestPaneWorkspaceConvertsRuntimePathToHostPath(t *testing.T) {
	withTestStore(t)

	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		"w-1001:main.0", "CiCy", 10001, "/cicy/workers/w-1001", "", "{}", "master", "", "claude", true, true,
	); err != nil {
		t.Fatalf("insert w-1001: %v", err)
	}

	got := paneWorkspace("w-1001")
	want := filepath.Join(cicyRootDir, "workers", "w-1001")
	if got != want {
		t.Fatalf("paneWorkspace = %q, want %q", got, want)
	}
}

func TestListBoundAgentWorkspacesConvertsRuntimePathToHostPath(t *testing.T) {
	withTestStore(t)

	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		"w-1001:main.0", "CiCy", 10001, "/cicy/workers/w-1001", "", "{}", "master", "", "claude", true, true,
	); err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		"w-20005:main.0", "Worker", 20005, "/cicy/workers/w-20005", "", "{}", "worker", "", "codex", true, true,
	); err != nil {
		t.Fatalf("insert child: %v", err)
	}
	if _, err := store.Exec("INSERT INTO pane_agents (pane_id, agent_name, status) VALUES (?,?,?)",
		"w-1001", "w-20005", "active",
	); err != nil {
		t.Fatalf("insert binding: %v", err)
	}

	items, err := listBoundAgentWorkspaces("w-1001")
	if err != nil {
		t.Fatalf("listBoundAgentWorkspaces: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("listBoundAgentWorkspaces len = %d, want 1", len(items))
	}
	want := filepath.Join(cicyRootDir, "workers", "w-20005")
	if items[0].workspace != want {
		t.Fatalf("workspace = %q, want %q", items[0].workspace, want)
	}
}
