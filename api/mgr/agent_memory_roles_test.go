package main

import (
	"strings"
	"testing"
)

// The preinstalled roles must be baked into the binary as <slug>/role.md (the
// English default persona) so a fresh install can seed them before creating the
// role agents.
func TestRoleRosterTemplatesEmbedded(t *testing.T) {
	want := []string{"knowledge-specialist", "audit-policy-specialist"}
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
	for _, slug := range []string{"knowledge-specialist", "audit-policy-specialist"} {
		if g := strings.TrimSpace(loadRoleMeta(slug).Greeting); g == "" {
			t.Errorf("role %q has no greeting in meta.yaml", slug)
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
