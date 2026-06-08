package main

import (
	"strings"
	"testing"
)

// The official role roster's templates must be baked into the binary so a fresh
// install can seed them (ensureRoleMemoryTemplates) before creating the role
// agents. If any is missing/empty, the agent's AGENTS.md would lose its charter.
func TestRoleRosterTemplatesEmbedded(t *testing.T) {
	want := []string{"项目经理", "产品经理", "测试工程师", "法务", "人力资源", "Token优化师"}
	for _, slug := range want {
		raw, err := agentRoleTemplatesFS.ReadFile("embed/agent-roles/" + slug + ".md")
		if err != nil {
			t.Errorf("role template %q not embedded: %v", slug, err)
			continue
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			t.Errorf("role template %q embedded but empty", slug)
		}
	}
}

// Each role template must carry an extractable `## 开场白` so the empty-chat
// greeting (agentOpeningGreeting) has real text to show for that role.
func TestRoleGreetingsExtractable(t *testing.T) {
	for _, slug := range []string{"项目经理", "产品经理", "测试工程师", "法务", "人力资源", "Token优化师", "运维工程师", "团队助手"} {
		raw, err := agentRoleTemplatesFS.ReadFile("embed/agent-roles/" + slug + ".md")
		if err != nil {
			t.Errorf("role template %q missing: %v", slug, err)
			continue
		}
		if g := extractOpeningSection(string(raw)); strings.TrimSpace(g) == "" {
			t.Errorf("role %q has no extractable 开场白", slug)
		}
	}
}

// Every slug the roster references must resolve to an embedded template — guards
// against a roster entry whose RoleTemplate has no matching file.
func TestOfficialRosterRoleTemplatesExist(t *testing.T) {
	for _, w := range officialRoleRoster() {
		if w.RoleTemplate == "" {
			continue
		}
		if _, err := agentRoleTemplatesFS.ReadFile("embed/agent-roles/" + w.RoleTemplate + ".md"); err != nil {
			t.Errorf("roster %s (w-%d) role template %q not embedded: %v", w.Title, w.Port, w.RoleTemplate, err)
		}
	}
}
