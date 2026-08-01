package main

import (
	"strings"
	"testing"
)

// The preinstalled roles must be baked into the binary as <slug>/role.md (the
// English default persona) so a fresh install can seed them before creating the
// role agents.
func TestRoleRosterTemplatesEmbedded(t *testing.T) {
	want := []string{"knowledge-specialist", "audit-policy-specialist", "koubo"}
	for _, slug := range want {
		raw, err := agentRoleTemplatesFS.ReadFile("embed/memory-seed/agents/" + slug + "/role.md")
		if err != nil {
			t.Errorf("role persona %q not embedded: %v", slug, err)
			continue
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			t.Errorf("role persona %q embedded but empty", slug)
		}
	}
}

// Each role's meta.yaml must carry a greeting (the empty-chat opening line lives
// in meta.yaml now, never in the persona/memory).
func TestRoleGreetingsInMeta(t *testing.T) {
	for _, slug := range []string{"knowledge-specialist", "audit-policy-specialist", "koubo"} {
		if g := strings.TrimSpace(loadRoleMeta(slug).Greeting); g == "" {
			t.Errorf("role %q has no greeting in meta.yaml", slug)
		}
	}
}

func TestAgentLanguageDefaultsToChinese(t *testing.T) {
	for _, config := range []string{"", `{}`, `{"lang":""}`} {
		if got := agentLangFromConfig(config); got != "zh-CN" {
			t.Errorf("agentLangFromConfig(%q) = %q, want zh-CN", config, got)
		}
	}
	if got := agentLangFromConfig(`{"lang":"en"}`); got != "en" {
		t.Errorf("explicit English language = %q, want en", got)
	}
}

func TestKouboSystemPromptRequiresPublicSkill(t *testing.T) {
	raw, err := agentRoleTemplatesFS.ReadFile("embed/memory-seed/agents/koubo/system.md")
	if err != nil {
		t.Fatalf("koubo system prompt not embedded: %v", err)
	}
	prompt := string(raw)
	for _, required := range []string{
		"cicy-koubo skill (required)",
		"cicy-koubo status --json",
		"references/ui-workflows.md",
		"agent-electron",
		"profile 1",
		"restore, show, and focus its owning Electron window",
		"session_download_url",
		"~/cicy-ai/global.json",
	} {
		if !strings.Contains(prompt, required) {
			t.Errorf("koubo system prompt missing %q", required)
		}
	}
}

// Every slug the roster references must resolve to an embedded role.md.
func TestOfficialRosterRoleTemplatesExist(t *testing.T) {
	for _, w := range officialRoleRoster() {
		if w.RoleTemplate == "" {
			continue
		}
		if _, err := agentRoleTemplatesFS.ReadFile("embed/memory-seed/agents/" + w.RoleTemplate + "/role.md"); err != nil {
			t.Errorf("roster %s (w-%d) role %q not embedded: %v", w.Title, w.Port, w.RoleTemplate, err)
		}
	}
}
