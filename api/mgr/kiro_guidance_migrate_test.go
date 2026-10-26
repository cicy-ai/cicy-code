package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKiroGuidanceIsAgentsMD(t *testing.T) {
	if got := guidanceFilenameForAgentType("kiro-cli"); got != "AGENTS.md" {
		t.Fatalf("kiro guidance file = %q, want AGENTS.md", got)
	}
}

func TestMigrateKiroSteeringMemory(t *testing.T) {
	ws := t.TempDir()
	steering := filepath.Join(ws, ".kiro", "steering")
	if err := os.MkdirAll(steering, 0755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(steering, "memory.md")
	os.WriteFile(legacy, []byte("---\ninclusion: always\n---\n\n## 协作\nrules\n<!-- cicy:custom -->\nmine\n"), 0644)
	os.WriteFile(filepath.Join(steering, "reply-in-chinese.md"), []byte("x"), 0644)

	if !migrateKiroSteeringMemory(ws) {
		t.Fatal("expected a migration")
	}
	got, _ := os.ReadFile(filepath.Join(ws, "AGENTS.md"))
	if string(got) != "## 协作\nrules\n<!-- cicy:custom -->\nmine\n" {
		t.Fatalf("AGENTS.md = %q", got)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy memory.md should be gone")
	}
	backups, _ := filepath.Glob(filepath.Join(ws, ".cicy", "memory-backups", "kiro-steering-memory.md.*"))
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	if _, err := os.Stat(filepath.Join(steering, "reply-in-chinese.md")); err != nil {
		t.Fatal("other steering files must stay")
	}
	if migrateKiroSteeringMemory(ws) {
		t.Fatal("second run must be a no-op")
	}
}

func TestMigrateKiroKeepsExistingAgentsMD(t *testing.T) {
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, ".kiro", "steering"), 0755)
	os.WriteFile(filepath.Join(ws, ".kiro", "steering", "memory.md"), []byte("old"), 0644)
	os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("current"), 0644)
	migrateKiroSteeringMemory(ws)
	got, _ := os.ReadFile(filepath.Join(ws, "AGENTS.md"))
	if string(got) != "current" {
		t.Fatalf("existing AGENTS.md overwritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(ws, ".kiro", "steering", "memory.md")); !os.IsNotExist(err) {
		t.Fatal("legacy file should still be retired")
	}
}
