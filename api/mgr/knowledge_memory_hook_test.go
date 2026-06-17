package main

import (
	"strings"
	"testing"
)

func resetKnowledgeMemoryGuard() {
	knowledgeMemoryMu.Lock()
	knowledgeMemoryRecent = map[string]knowledgeMemoryDispatch{}
	knowledgeMemoryMu.Unlock()
}

func setGlobalSettings(t *testing.T, blobJSON string) {
	t.Helper()
	if _, err := store.Exec(
		"INSERT INTO global_vars (key_name, value) VALUES ('global_settings', ?) ON CONFLICT(key_name) DO UPDATE SET value=excluded.value",
		blobJSON); err != nil {
		t.Fatalf("set global_settings: %v", err)
	}
}

func TestIsMemoryFilePath(t *testing.T) {
	cases := map[string]bool{
		"/home/cicy/.claude/projects/-home-cicy-w-1/memory/fact.md": true,
		"/home/cicy/.claude/projects/-x/memory/sub/deep.md":         true,
		"/home/cicy/.claude/projects/-x/memory/MEMORY.md":           false, // index excluded
		"/home/cicy/.claude/projects/-x/notes/fact.md":              false, // not memory
		"/home/cicy/projects/cicy-code/memory/x.md":                 false, // not under .claude/projects
		"":                  false,
		"relative/memory.md": false,
	}
	for in, want := range cases {
		if got := isMemoryFilePath(in); got != want {
			t.Errorf("isMemoryFilePath(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseMemoryToolCall(t *testing.T) {
	cases := []struct{ args, wantPath, wantContent string }{
		{`{"file_path":"/a/memory/f.md","content":"hello"}`, "/a/memory/f.md", "hello"},
		{`{"file_path":"/a/memory/f.md","old_string":"x","new_string":"added"}`, "/a/memory/f.md", "added"},
		{`{"notebook_path":"/a/memory/n.ipynb","new_source":"src"}`, "/a/memory/n.ipynb", "src"},
		{`not json`, "", ""},
		{``, "", ""},
	}
	for _, c := range cases {
		p, ct := parseMemoryToolCall(c.args)
		if p != c.wantPath || ct != c.wantContent {
			t.Errorf("parse(%q) = (%q,%q), want (%q,%q)", c.args, p, ct, c.wantPath, c.wantContent)
		}
	}
}

func TestKnowledgeTitleFromMemory(t *testing.T) {
	if got := knowledgeTitleFromMemory("/m/f.md", "---\nname: My Fact\ndescription: d\n---\nbody"); got != "My Fact" {
		t.Errorf("frontmatter name: got %q", got)
	}
	if got := knowledgeTitleFromMemory("/m/f.md", "# Heading One\nmore"); got != "Heading One" {
		t.Errorf("heading: got %q", got)
	}
	if got := knowledgeTitleFromMemory("/m/runbook.md", ""); got != "runbook.md" {
		t.Errorf("basename fallback: got %q", got)
	}
}

// knowledge_hook_enabled defaults ON and reads its OWN key — audit_enabled must
// not affect it (decoupling).
func TestKnowledgeHookEnabledIndependentOfAudit(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	if !knowledgeHookEnabled() {
		t.Fatalf("default should be enabled")
	}
	// audit OFF, knowledge key unset → knowledge still ON.
	setGlobalSettings(t, `{"audit_enabled":false}`)
	if !knowledgeHookEnabled() {
		t.Fatalf("audit_enabled=false must not disable the knowledge hook")
	}
	// audit ON but knowledge explicitly OFF → knowledge OFF (its own switch wins).
	setGlobalSettings(t, `{"audit_enabled":true,"knowledge_hook_enabled":false}`)
	if knowledgeHookEnabled() {
		t.Fatalf("knowledge_hook_enabled=false must disable the hook")
	}
}

// End-to-end: a Write into a memory path seeds one pending memory-hook entry
// (no 知识专员 provisioned → still creates the entry, just no brief).
func TestMemoryWriteHookSeedsPending(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	resetKnowledgeMemoryGuard()

	path := "/home/cicy/.claude/projects/-home-cicy-w-30001/memory/db-fact.md"
	h := &memoryWriteHook{sourcePane: "w-30001"}
	h.finalize(aiGatewayReplySnapshot{
		Status: "completed",
		ToolCalls: []aiGatewayToolCall{{
			ToolName:  "Write",
			Arguments: `{"file_path":"` + path + `","content":"---\nname: DB fact\n---\n8008 is production."}`,
		}},
	})

	rows, _ := listKnowledge(knowledgeFilter{Status: "pending"})
	if len(rows) != 1 {
		t.Fatalf("want 1 pending entry, got %d", len(rows))
	}
	k := rows[0]
	if k.SourceKind != "memory-hook" || k.OriginRef != path || k.Title != "DB fact" || k.SourcePane != normPaneID("w-30001") {
		t.Fatalf("seeded entry wrong: %+v", k)
	}
}

// A non-memory write (and a non-edit tool) must NOT seed anything.
func TestMemoryWriteHookIgnoresNonMemory(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	resetKnowledgeMemoryGuard()

	h := &memoryWriteHook{sourcePane: "w-30001"}
	h.finalize(aiGatewayReplySnapshot{
		Status: "completed",
		ToolCalls: []aiGatewayToolCall{
			{ToolName: "Write", Arguments: `{"file_path":"/home/cicy/projects/x/src/main.go","content":"code"}`},
			{ToolName: "Bash", Arguments: `{"command":"ls"}`},
		},
	})
	rows, _ := listKnowledge(knowledgeFilter{})
	if len(rows) != 0 {
		t.Fatalf("non-memory write should seed nothing, got %d", len(rows))
	}
}

// Dedup: two writes to the SAME memory file within the window → one entry, body
// refreshed to the latest content. A different file → a separate entry.
func TestMemoryWriteHookDedup(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	resetKnowledgeMemoryGuard()

	path := "/home/cicy/.claude/projects/-home-cicy-w-30001/memory/fact.md"
	h := &memoryWriteHook{sourcePane: "w-30001"}
	write := func(body string) {
		h.finalize(aiGatewayReplySnapshot{
			Status:    "completed",
			ToolCalls: []aiGatewayToolCall{{ToolName: "Write", Arguments: `{"file_path":"` + path + `","content":"` + body + `"}`}},
		})
	}
	write("first version")
	write("second version")

	rows, _ := listKnowledge(knowledgeFilter{})
	if len(rows) != 1 {
		t.Fatalf("dedup: want 1 entry, got %d", len(rows))
	}
	if strings.TrimSpace(rows[0].Body) != "second version" {
		t.Fatalf("latest content should win: body=%q", rows[0].Body)
	}

	// a different memory file → a separate entry.
	h2 := &memoryWriteHook{sourcePane: "w-30001"}
	h2.finalize(aiGatewayReplySnapshot{
		Status:    "completed",
		ToolCalls: []aiGatewayToolCall{{ToolName: "Write", Arguments: `{"file_path":"/home/cicy/.claude/projects/-home-cicy-w-30001/memory/other.md","content":"x"}`}},
	})
	rows, _ = listKnowledge(knowledgeFilter{})
	if len(rows) != 2 {
		t.Fatalf("distinct file should add an entry, got %d", len(rows))
	}
}

// knowledge_hook_enabled=false short-circuits finalize even with a memory write.
func TestMemoryWriteHookDisabled(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	resetKnowledgeMemoryGuard()
	setGlobalSettings(t, `{"knowledge_hook_enabled":false}`)

	h := &memoryWriteHook{sourcePane: "w-30001"}
	h.finalize(aiGatewayReplySnapshot{
		Status:    "completed",
		ToolCalls: []aiGatewayToolCall{{ToolName: "Write", Arguments: `{"file_path":"/home/cicy/.claude/projects/-x/memory/f.md","content":"c"}`}},
	})
	rows, _ := listKnowledge(knowledgeFilter{})
	if len(rows) != 0 {
		t.Fatalf("disabled hook should seed nothing, got %d", len(rows))
	}
}

// The 知识专员 resolver finds a live agent carrying role_template=知识专员.
func TestKnowledgeSpecialistPaneID(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	if knowledgeSpecialistPaneID() != "" {
		t.Fatalf("no specialist provisioned → want empty")
	}
	if _, err := store.Exec(
		"INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, role_template, active) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		"w-30099:main.0", "KnowSpec", 30099, "/tmp/w-30099", "", "{}", "worker", "", "cicy", knowledgeSpecialistRoleTemplate, 1,
	); err != nil {
		t.Fatalf("insert specialist: %v", err)
	}
	if got := knowledgeSpecialistPaneID(); got != "w-30099:main.0" {
		t.Fatalf("resolver = %q, want w-30099:main.0", got)
	}
}
