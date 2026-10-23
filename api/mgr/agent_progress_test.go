package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProgressHelperLookupHideAndDelete(t *testing.T) {
	withTestStore(t)
	ins := func(id, cfg string) {
		t.Helper()
		if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese, active) VALUES (?,?,?,?,?,?,?,?,?,?,1)",
			id, id, "/tmp/"+id, "", cfg, "", "", "kiro-cli", true, true); err != nil {
			t.Fatal(err)
		}
	}
	ins("w-2001:main.0", "{}")
	ins("w-2002:main.0", `{"progress_of":"w-2001"}`)
	invalidateProgressHelperCache()

	if got := progressHelperFor("w-2001"); got != "w-2002" {
		t.Fatalf("helper of w-2001 = %q", got)
	}
	if !isBuiltinAgent("w-2002:main.0") || isBuiltinAgent("w-2001") {
		t.Fatal("only the helper must be hidden")
	}
	deleteProgressHelpers("w-2001")
	if got := progressHelperFor("w-2001"); got != "" {
		t.Fatalf("helper survived delete: %q", got)
	}
	if isBuiltinAgent("w-2002") {
		t.Fatal("hidden cache not invalidated")
	}
}

func TestWriteProgressHelperFiles(t *testing.T) {
	ws := t.TempDir()
	writeProgressHelperFiles(ws, "w-1075")
	link, err := os.ReadFile(filepath.Join(ws, ".cicy", "progress.json"))
	if err != nil || !strings.Contains(string(link), `"w-1075"`) {
		t.Fatalf("progress.json: %s %v", link, err)
	}
	steer, err := os.ReadFile(filepath.Join(ws, ".kiro", "steering", "progress-helper.md"))
	if err != nil || !strings.Contains(string(steer), "w-1075") || !strings.Contains(string(steer), "agent-progress") {
		t.Fatalf("steering: %v", err)
	}
}

func TestKiroBootPinsDefaultModel(t *testing.T) {
	lines := strings.Join(agentBootLines("kiro-cli", true, true, false, false, "w-2002", "claude-haiku-4.5"), "\n")
	if !strings.Contains(lines, "kiro-cli chat $CICY_KIRO_RESUME --model 'claude-haiku-4.5' --trust-all-tools") {
		t.Fatalf("model not pinned:\n%s", lines)
	}
	lines = strings.Join(agentBootLines("kiro-cli", true, true, false, false, "w-2003", ""), "\n")
	if strings.Contains(lines, "--model") {
		t.Fatal("no model → no --model flag")
	}
}

func TestKiroBootSkipsResidentCLIForProgressHelper(t *testing.T) {
	lines := strings.Join(agentBootLines("kiro-cli", true, true, false, false, "w-2002", ""), "\n")
	if !strings.Contains(lines, `if [ -f "$WORKSPACE/.cicy/progress.json" ]; then`) {
		t.Fatalf("helper guard missing:\n%s", lines)
	}
}
