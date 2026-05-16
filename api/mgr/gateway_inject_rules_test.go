package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// insertAgentForRulesTest inserts a minimal agent_config row with the given
// pane_id (normalized form `w-xxxxx:main.0`), workspace, and config JSON.
func insertAgentForRulesTest(t *testing.T, paneID, workspace, configJSON string) {
	t.Helper()
	if _, err := store.Exec(
		`INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese, inject_rules_files)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		paneID, "Test", 0, workspace, "", configJSON, "worker", "", "claude", true, true, 1,
	); err != nil {
		t.Fatalf("insert agent_config: %v", err)
	}
}

func resetRulesFileCache() {
	rulesFileCache = sync.Map{}
}

func TestRulesFilesCollectMissingFiles(t *testing.T) {
	withTestStore(t)
	resetRulesFileCache()
	tmp := t.TempDir()
	// No CLAUDE.md / AGENTS.md created in tmp dir.
	insertAgentForRulesTest(t, "w-30001:main.0", tmp, "{}")
	files := agentInspectorCollectRulesFiles("w-30001")
	if len(files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(files))
	}
}

func TestRulesFilesProjectEmptyWorkspaceOnly(t *testing.T) {
	withTestStore(t)
	resetRulesFileCache()
	// agentInspectorProjectKey returns "" only when the workspace path matches
	// the /workers/w-\d+$ pattern (i.e. it IS a worker workspace, so it should
	// not double as a project root). Construct a tmp path that matches.
	base := t.TempDir()
	workspace := filepath.Join(base, "workers", "w-30002")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "CLAUDE.md"), []byte("workspace claude"), 0644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	insertAgentForRulesTest(t, "w-30002:main.0", workspace, "{}")
	files := agentInspectorCollectRulesFiles("w-30002")
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Scope != "workspace" {
		t.Fatalf("expected workspace scope, got %q", files[0].Scope)
	}
	if !strings.Contains(files[0].Content, "workspace claude") {
		t.Fatalf("unexpected content: %q", files[0].Content)
	}
}

func TestRulesFilesWorkspaceEmptyProjectOnly(t *testing.T) {
	withTestStore(t)
	resetRulesFileCache()
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "CLAUDE.md"), []byte("project claude"), 0644); err != nil {
		t.Fatalf("write project CLAUDE.md: %v", err)
	}
	cfg := fmt.Sprintf(`{"projects":[%q]}`, projectDir)
	insertAgentForRulesTest(t, "w-30003:main.0", "", cfg)
	files := agentInspectorCollectRulesFiles("w-30003")
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Scope != "project" {
		t.Fatalf("expected project scope, got %q", files[0].Scope)
	}
}

func TestRulesFilesDedupeProjectEqualsWorkspace(t *testing.T) {
	withTestStore(t)
	resetRulesFileCache()
	shared := t.TempDir()
	if err := os.WriteFile(filepath.Join(shared, "CLAUDE.md"), []byte("shared claude"), 0644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shared, "AGENTS.md"), []byte("shared agents"), 0644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	cfg := fmt.Sprintf(`{"projects":[%q]}`, shared)
	insertAgentForRulesTest(t, "w-30004:main.0", shared, cfg)
	files := agentInspectorCollectRulesFiles("w-30004")
	if len(files) != 2 {
		t.Fatalf("expected 2 files (deduped), got %d", len(files))
	}
	// Project layer is added first, so dedupe keeps project scope.
	for _, f := range files {
		if f.Scope != "project" {
			t.Fatalf("expected project scope after dedupe, got %q for %s", f.Scope, f.Path)
		}
	}
}

func TestRulesFilesSingleFileTruncated(t *testing.T) {
	withTestStore(t)
	resetRulesFileCache()
	tmp := t.TempDir()
	big := strings.Repeat("x", rulesFileMaxBytes+10*1024) // 60KB
	if err := os.WriteFile(filepath.Join(tmp, "CLAUDE.md"), []byte(big), 0644); err != nil {
		t.Fatalf("write big CLAUDE.md: %v", err)
	}
	insertAgentForRulesTest(t, "w-30005:main.0", tmp, "{}")
	files := agentInspectorCollectRulesFiles("w-30005")
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if !files[0].Truncated {
		t.Fatalf("expected truncated=true, got false")
	}
	if !strings.Contains(files[0].Content, "[truncated") {
		t.Fatalf("expected truncated marker in content")
	}
	if files[0].Bytes != len(big) {
		t.Fatalf("expected Bytes=%d (raw size), got %d", len(big), files[0].Bytes)
	}
}

func TestRulesFilesOverlayCapDropsProject(t *testing.T) {
	withTestStore(t)
	resetRulesFileCache()
	// Pack each file near the per-file cap so project + workspace exceeds the
	// overlay cap and the project layer gets dropped.
	near := strings.Repeat("a", rulesFileMaxBytes-1024) // ~49KB each
	projectDir := t.TempDir()
	workspaceDir := t.TempDir()
	for _, dir := range []string{projectDir, workspaceDir} {
		if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(near), 0644); err != nil {
			t.Fatalf("write CLAUDE.md in %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(near), 0644); err != nil {
			t.Fatalf("write AGENTS.md in %s: %v", dir, err)
		}
	}
	cfg := fmt.Sprintf(`{"projects":[%q]}`, projectDir)
	insertAgentForRulesTest(t, "w-30006:main.0", workspaceDir, cfg)
	files := agentInspectorCollectRulesFiles("w-30006")

	total := 0
	hasWorkspace := false
	hasProject := false
	for _, f := range files {
		total += len(f.Content)
		if f.Scope == "workspace" {
			hasWorkspace = true
		}
		if f.Scope == "project" {
			hasProject = true
		}
	}
	if total > rulesOverlayMaxBytes {
		t.Fatalf("total %d exceeds cap %d", total, rulesOverlayMaxBytes)
	}
	if !hasWorkspace {
		t.Fatalf("expected workspace scope preserved when over cap")
	}
	if hasProject && total+len(near) > rulesOverlayMaxBytes {
		// project should have been dropped; if it survived together with full
		// workspace the total math would already trip the cap above.
		t.Fatalf("expected project layer dropped when over cap")
	}
}

func TestRulesFilesCacheInvalidatesOnMtimeChange(t *testing.T) {
	withTestStore(t)
	resetRulesFileCache()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("v1"), 0644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	insertAgentForRulesTest(t, "w-30007:main.0", tmp, "{}")

	files := agentInspectorCollectRulesFiles("w-30007")
	if len(files) != 1 || !strings.Contains(files[0].Content, "v1") {
		t.Fatalf("first read: expected v1 content, got %+v", files)
	}

	// Sleep just past mtime resolution then rewrite with new content.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("v2 content longer than v1"), 0644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	// Force mtime to differ from cached value in case the filesystem resolution
	// rounded both writes to the same nanosecond.
	future := time.Now().Add(1 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	files = agentInspectorCollectRulesFiles("w-30007")
	if len(files) != 1 || !strings.Contains(files[0].Content, "v2") {
		t.Fatalf("second read: expected v2 content, got %+v", files)
	}
}

func TestRulesFilesGlobalSwitchOff(t *testing.T) {
	withTestStore(t)
	resetRulesFileCache()
	t.Setenv("CICY_GATEWAY_INJECT_RULES", "0")
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "CLAUDE.md"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	insertAgentForRulesTest(t, "w-30008:main.0", tmp, "{}")
	files := agentInspectorCollectRulesFiles("w-30008")
	if len(files) != 0 {
		t.Fatalf("expected 0 files with global switch off, got %d", len(files))
	}
}

func TestRulesFilesPaneSwitchOff(t *testing.T) {
	withTestStore(t)
	resetRulesFileCache()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "CLAUDE.md"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	if _, err := store.Exec(
		`INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese, inject_rules_files)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		"w-30009:main.0", "Test", 0, tmp, "", "{}", "worker", "", "claude", true, true, 0,
	); err != nil {
		t.Fatalf("insert agent_config: %v", err)
	}
	files := agentInspectorCollectRulesFiles("w-30009")
	if len(files) != 0 {
		t.Fatalf("expected 0 files with pane switch off, got %d", len(files))
	}
}
