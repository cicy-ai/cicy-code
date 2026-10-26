package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// legacyKiroSteeringMemory is where kiro agents' guidance lived before they
// moved to the workspace-root AGENTS.md (which kiro-cli loads by itself).
var legacyKiroSteeringMemory = filepath.Join(".kiro", "steering", "memory.md")

// migrateKiroSteeringMemory moves <ws>/.kiro/steering/memory.md to
// <ws>/AGENTS.md (minus its `inclusion: always` front-matter) when AGENTS.md
// does not exist yet, then parks the old file in .cicy/memory-backups/ so kiro
// never loads the same rules twice. Idempotent; other steering files
// (reply-in-chinese.md) are left alone.
func migrateKiroSteeringMemory(workspace string) bool {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return false
	}
	legacy := filepath.Join(workspace, legacyKiroSteeringMemory)
	old, err := os.ReadFile(legacy)
	if err != nil {
		return false
	}
	agents := filepath.Join(workspace, "AGENTS.md")
	if _, err := os.Stat(agents); os.IsNotExist(err) {
		if err := os.WriteFile(agents, []byte(stripKiroFrontMatter(string(old))), 0644); err != nil {
			log.Printf("[kiro] migrate %s → AGENTS.md failed: %v", legacy, err)
			return false
		}
	}
	backupDir := filepath.Join(workspace, ".cicy", "memory-backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		log.Printf("[kiro] backup dir for %s: %v", legacy, err)
		return false
	}
	backup := filepath.Join(backupDir, "kiro-steering-memory.md."+time.Now().UTC().Format("20060102-150405"))
	if err := os.Rename(legacy, backup); err != nil {
		log.Printf("[kiro] retire %s failed: %v", legacy, err)
		return false
	}
	log.Printf("[kiro] guidance moved to %s (old file → %s)", agents, backup)
	return true
}

// stripKiroFrontMatter drops a leading `---\n…\n---\n` block (and the blank
// line after it); AGENTS.md takes no inclusion modes.
func stripKiroFrontMatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return s
	}
	return strings.TrimLeft(s[4+end+5:], "\n")
}
