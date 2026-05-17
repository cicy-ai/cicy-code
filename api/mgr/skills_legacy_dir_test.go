package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestComputeMarketStatusAcceptsLegacyAgentSkillDirs covers the case where an
// older cicy-skills binary installed the SKILL.md under a different directory
// name than the catalog Name (e.g. `~/.<profile>/skills/mihomo/SKILL.md` for
// the `cicy-mihomo` market entry). Without this fallback the marketplace
// status reports `installed=false` forever and the UI keeps showing the
// install button on a host that is, in fact, functional.
func TestComputeMarketStatusAcceptsLegacyAgentSkillDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create ~/.local/bin/cicy-mihomo so the symlinkInstalled half of the
	// status check passes — we are isolating the doc-directory fallback.
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "cicy-mihomo"), nil, 0o755); err != nil {
		t.Fatalf("WriteFile bin: %v", err)
	}

	// Seed SKILL.md only under the LEGACY dir name (`mihomo`) in each of the
	// three agent profiles — this mirrors a host where the embedded
	// cicy-skills binary still writes the pre-rename directory.
	for _, profile := range []string{"claude", "codex", "opencode"} {
		dir := filepath.Join(home, "."+profile, "skills", "mihomo")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: cicy-mihomo\n---\n"), 0o644); err != nil {
			t.Fatalf("WriteFile SKILL.md: %v", err)
		}
	}

	skill := marketSkill{
		Name:                 "cicy-mihomo",
		BinaryAliases:        []string{"cicy-mihomo"},
		ConfigFile:           "~/cicy-ai/db/mihomo.yaml",
		legacyAgentSkillDirs: []string{"mihomo"},
	}
	computeMarketStatus(&skill)
	if !skill.Status.Installed {
		t.Fatalf("expected installed=true when legacy dir holds SKILL.md, got %+v", skill.Status)
	}

	// Removing the legacy dir from one profile must flip status back to
	// uninstalled (the doc check still requires all three profiles).
	if err := os.RemoveAll(filepath.Join(home, ".claude", "skills", "mihomo")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	computeMarketStatus(&skill)
	if skill.Status.Installed {
		t.Fatalf("expected installed=false after removing one profile, got %+v", skill.Status)
	}
}
