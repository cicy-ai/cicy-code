package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Writing employees.yaml under cicyRootDir and resetting the cache makes the
// loader pick it up; the resolvers must return the configured fields, and "" /
// nil for roles absent from the file (so callers fall back to the role .md).
func TestEmployeeTemplateResolvers(t *testing.T) {
	prev := cicyRootDir
	cicyRootDir = t.TempDir()
	resetEmployeeTemplatesCache()
	defer func() { cicyRootDir = prev; resetEmployeeTemplatesCache() }()

	yaml := `templates:
  项目经理:
    tools: ["coordinate"]
    greeting: |
      你好,我是项目经理。
  团队助手:
    tools: ["coordinate", "shell"]
    greeting: "您好,我是团队小助手。"
    prompt: "你是装机向导。"
`
	dbDir := filepath.Join(cicyRootDir, "db")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dbDir, "employees.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	if g := strings.TrimSpace(employeeTemplateGreeting("项目经理")); g != "你好,我是项目经理。" {
		t.Errorf("项目经理 greeting = %q", g)
	}
	if tools := employeeTemplateTools("团队助手"); len(tools) != 2 || tools[1] != "shell" {
		t.Errorf("团队助手 tools = %v", tools)
	}
	if p := employeeTemplatePrompt("团队助手"); p != "你是装机向导。" {
		t.Errorf("团队助手 prompt = %q", p)
	}
	// A role not in the file → empty/nil so callers fall back.
	if employeeTemplateGreeting("法务") != "" || employeeTemplateTools("法务") != nil {
		t.Error("absent role should resolve empty/nil (fall back to role .md)")
	}
}

// ensureEmployeeTemplates must generate employees.yaml from the embedded role
// templates (tools frontmatter + 开场白) and never clobber an existing file.
func TestEnsureEmployeeTemplatesSeeds(t *testing.T) {
	prev := cicyRootDir
	cicyRootDir = t.TempDir()
	resetEmployeeTemplatesCache()
	defer func() { cicyRootDir = prev; resetEmployeeTemplatesCache() }()

	ensureEmployeeTemplates()

	path := filepath.Join(cicyRootDir, "db", "employees.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("employees.yaml not seeded: %v", err)
	}
	// audit-policy-specialist selects [coordinate, audit, shell] in meta.yaml → must surface.
	if tools := employeeTemplateTools("audit-policy-specialist"); len(tools) == 0 {
		t.Error("seeded audit-policy-specialist should carry its meta.yaml tools")
	}
	// knowledge-specialist has a greeting in meta.yaml → must be non-empty after seeding.
	if strings.TrimSpace(employeeTemplateGreeting("knowledge-specialist")) == "" {
		t.Error("seeded knowledge-specialist should carry its meta.yaml greeting")
	}

	// Idempotent: a second call must NOT overwrite operator edits.
	if err := os.WriteFile(path, []byte("templates:\n  knowledge-specialist:\n    greeting: \"EDITED\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	resetEmployeeTemplatesCache()
	ensureEmployeeTemplates() // should be a no-op now
	if g := strings.TrimSpace(employeeTemplateGreeting("knowledge-specialist")); g != "EDITED" {
		t.Errorf("ensureEmployeeTemplates clobbered an existing file; greeting=%q", g)
	}
}
