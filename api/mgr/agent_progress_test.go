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
	store.Exec("UPDATE agent_config SET agent_type='cicy' WHERE pane_id='w-2002:main.0'")
	ins("w-2003:main.0", `{"progress_of":"w-2001"}`) // legacy kiro helper
	invalidateProgressHelperCache()

	if got := progressHelperFor("w-2001"); got != "w-2002" {
		t.Fatalf("helper of w-2001 = %q (legacy kiro helper must not count)", got)
	}
	if len(progressHelpersOf("w-2001")) != 2 {
		t.Fatal("progressHelpersOf must list legacy helpers too")
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
	writeProgressHelperFiles(ws, "w-1075", []string{"w-1087"})
	link, err := os.ReadFile(filepath.Join(ws, ".cicy", "progress.json"))
	if err != nil || !strings.Contains(string(link), `"main":"w-1075"`) || !strings.Contains(string(link), `"previous_helpers":["w-1087"]`) {
		t.Fatalf("progress.json: %s %v", link, err)
	}
}

func TestProgressAgentDocAndPrompt(t *testing.T) {
	ca := parseCustomAgentDoc(progressAgentSlug, progressAgentDoc)
	if ca.Model != progressHelperModel || strings.Join(ca.Tools, ",") != "skill,shell" || !strings.Contains(ca.Body, "agent-progress") {
		t.Fatalf("custom agent doc: %+v", ca)
	}
	p := progressRefreshPrompt("w-3001")
	for _, want := range []string{"agent-progress digest --helper w-3001", "agent-progress commit --helper w-3001"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
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
