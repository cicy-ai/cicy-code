package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeAgentMemoryIncludesRoleSystemBeforeProjectAndRole(t *testing.T) {
	previousRoot := cicyRootDir
	cicyRootDir = t.TempDir()
	t.Cleanup(func() { cicyRootDir = previousRoot })

	files := map[string]string{
		"memory/global.md":                 "GLOBAL_RULES",
		"memory/projects/demo.md":          "PROJECT_RULES",
		"memory/agents/reviewer/system.md": "SYSTEM_RULES",
		"memory/agents/reviewer/role.md":   "ROLE_RULES",
	}
	for name, content := range files {
		path := filepath.Join(cicyRootDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	got := composeAgentMemory("w-test", "/tmp/workspace", "codex", "demo", "reviewer", "en")
	last := -1
	for _, marker := range []string{"GLOBAL_RULES", "SYSTEM_RULES", "PROJECT_RULES", "ROLE_RULES"} {
		index := strings.Index(got, marker)
		if index <= last {
			t.Fatalf("guidance order is wrong for %s:\n%s", marker, got)
		}
		last = index
	}
}
