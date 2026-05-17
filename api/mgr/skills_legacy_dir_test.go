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

// TestReadSkillDocFallsBackToLegacyDir covers the detail-modal doc reads:
// SKILL.md / references/help.md / references/tools.md must surface even when
// the on-disk dir uses a legacy name (e.g. `mihomo/` instead of
// `cicy-mihomo/`). Without the fallback, the Skill Detail Modal renders with
// blank help/tools panels on hosts running a stale cicy-skills binary.
func TestReadSkillDocFallsBackToLegacyDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Seed SKILL.md + references under the LEGACY dir name only.
	for _, profile := range []string{".claude", ".codex", ".opencode"} {
		refsDir := filepath.Join(home, profile, "skills", "mihomo", "references")
		if err := os.MkdirAll(refsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(home, profile, "skills", "mihomo", "SKILL.md"), []byte("SKILL"), 0o644); err != nil {
			t.Fatalf("WriteFile SKILL.md: %v", err)
		}
		if err := os.WriteFile(filepath.Join(refsDir, "help.md"), []byte("HELP"), 0o644); err != nil {
			t.Fatalf("WriteFile help.md: %v", err)
		}
		if err := os.WriteFile(filepath.Join(refsDir, "tools.md"), []byte("TOOLS"), 0o644); err != nil {
			t.Fatalf("WriteFile tools.md: %v", err)
		}
	}

	skill := &marketSkill{
		Name:                 "cicy-mihomo",
		legacyAgentSkillDirs: []string{"mihomo"},
	}
	if got := readSkillDoc(skill, "SKILL.md"); got != "SKILL" {
		t.Fatalf("SKILL.md fallback = %q, want %q", got, "SKILL")
	}
	if got := readSkillDoc(skill, filepath.Join("references", "help.md")); got != "HELP" {
		t.Fatalf("help.md fallback = %q, want %q", got, "HELP")
	}
	if got := readSkillDoc(skill, filepath.Join("references", "tools.md")); got != "TOOLS" {
		t.Fatalf("tools.md fallback = %q, want %q", got, "TOOLS")
	}

	// A skill with no legacy aliases must still get the canonical dir hit.
	canonical := &marketSkill{Name: "cicy-ssh"}
	for _, profile := range []string{".claude", ".codex", ".opencode"} {
		dir := filepath.Join(home, profile, "skills", "cicy-ssh")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("CANON"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	if got := readSkillDoc(canonical, "SKILL.md"); got != "CANON" {
		t.Fatalf("canonical = %q, want %q", got, "CANON")
	}
}
