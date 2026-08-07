package main

import (
	"testing"
)

// Public roles are installed and updated through Role Market. Keeping copies in
// the binary would create a second source of truth and could silently re-seed a
// role that the user intentionally removed.
func TestMarketplaceRolesNotEmbedded(t *testing.T) {
	want := []string{"assistant", "knowledge-specialist", "audit-policy-specialist", "desktop-assist", "koubo"}
	for _, slug := range want {
		if _, err := agentRoleTemplatesFS.ReadFile("embed/memory-seed/agents/" + slug + "/role.md"); err == nil {
			t.Errorf("marketplace role %q must not be embedded", slug)
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

func TestOfficialRosterDefaultsToChineseTitles(t *testing.T) {
	want := map[int]string{1001: "知识专员", 101: "架构师", 102: "全栈工程师", 103: "软件工程师", 104: "审计策略专员", 105: "口播智能体"}
	for _, worker := range officialRoleRoster() {
		if got := worker.Title; got != want[worker.Port] {
			t.Errorf("w-%d title = %q, want %q", worker.Port, got, want[worker.Port])
		}
		if worker.TitleEn == "" {
			t.Errorf("w-%d is missing its explicit English title", worker.Port)
		}
	}
}

func TestSpokenContentAgentIsCreatedStandalone(t *testing.T) {
	for _, worker := range officialRoleRoster() {
		if worker.Port == 105 {
			if worker.BindToPrimary {
				t.Fatal("w-105 must be pre-created without binding to the primary")
			}
			return
		}
	}
	t.Fatal("w-105 missing from official roster")
}
