package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeAgentRoleInstalled(t *testing.T) {
	dir := t.TempDir()
	if runtimeAgentRoleInstalled(dir) {
		t.Fatal("empty directory must not be installed")
	}
	for _, name := range []string{"meta.yaml", "role.md", "system.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if !runtimeAgentRoleInstalled(dir) {
		t.Fatal("complete runtime role must be installed without marketplace marker")
	}
	if err := os.WriteFile(filepath.Join(dir, "role.md"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if runtimeAgentRoleInstalled(dir) {
		t.Fatal("empty standard file must not count as installed")
	}
}
