package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNeedsCicySkillsInstall covers the install-detection logic that
// startup uses to decide whether to (re-)bootstrap cicy-skills. The
// detection now scans every agentgen-approved skill across all profiles,
// not just agent-code-server, so a partial install (e.g. a crash mid-way
// through the previous bootstrap, or a binary upgrade that added a new
// skill not yet present in the existing profile dirs) re-triggers
// install instead of silently leaving the marketplace UI broken.
func TestNeedsCicySkillsInstall(t *testing.T) {
	home := t.TempDir()
	skillsRoot := filepath.Join(t.TempDir(), "skills")
	oldSkillsDir := cicySkillsDir
	cicySkillsDir = skillsRoot
	t.Cleanup(func() {
		cicySkillsDir = oldSkillsDir
	})
	t.Setenv("HOME", home)

	if !needsCicySkillsInstall() {
		t.Fatal("needsCicySkillsInstall should require install when files are missing")
	}

	// Required files: cicy-skills CLI symlink + every approved skill's
	// SKILL.md across every profile.
	required := []string{
		filepath.Join(home, ".local", "bin", "cicy-skills"),
	}
	for skillName := range agentgenApprovedMarketSkills {
		for _, profile := range []string{"codex", "claude", "opencode"} {
			required = append(required, filepath.Join(home, "."+profile, "skills", skillName, "SKILL.md"))
		}
	}
	for _, path := range required {
		if err := ensureRuntimeDir(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("ensureRuntimeDir(%s) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte("ok\n"), 0755); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	if needsCicySkillsInstall() {
		t.Fatal("needsCicySkillsInstall should be false when all approved skills' SKILL.md and the CLI symlink exist")
	}

	// Removing one approved skill's SKILL.md should re-trigger install.
	var sampleSkill string
	for name := range agentgenApprovedMarketSkills {
		sampleSkill = name
		break
	}
	if sampleSkill == "" {
		t.Fatal("agentgenApprovedMarketSkills is empty — test setup invalid")
	}
	missing := filepath.Join(home, ".claude", "skills", sampleSkill, "SKILL.md")
	if err := os.Remove(missing); err != nil {
		t.Fatalf("Remove(%s) error = %v", missing, err)
	}
	if !needsCicySkillsInstall() {
		t.Fatalf("needsCicySkillsInstall should be true when %s SKILL.md is missing", sampleSkill)
	}

	// Re-create it, then verify a stale legacy alias still triggers install
	// (prevents drift from older bundles that left bin/webpage etc behind).
	if err := os.WriteFile(missing, []byte("ok\n"), 0755); err != nil {
		t.Fatalf("WriteFile(restore %s) error = %v", missing, err)
	}
	if needsCicySkillsInstall() {
		t.Fatal("needsCicySkillsInstall should be false after restoring the missing SKILL.md")
	}
	legacy := filepath.Join(home, ".local", "bin", "webpage")
	if err := os.WriteFile(legacy, []byte("legacy\n"), 0755); err != nil {
		t.Fatalf("WriteFile(legacy webpage) error = %v", err)
	}
	if !needsCicySkillsInstall() {
		t.Fatal("needsCicySkillsInstall should be true when legacy webpage alias still exists")
	}
}

// TestNeedsCicySkillsInstallSkipsUninstalled ensures that skills the user
// has explicitly uninstalled via the marketplace (tracked in
// uninstalledSkillsSet) don't re-trigger install on every startup. Without
// this the bootstrap would resurrect the skill, then applySkillsUninstallState
// would tear it down again — wasted churn on every restart.
// TestChooseDevBootstrapDecision exhaustively covers the four routes the
// dev-mode bootstrap dispatcher can take. Catches the exact bug class that
// motivated this whole fix: a fresh dev-mode startup would skip bootstrap
// entirely, leaving ~/.local/bin/cicy-skills missing and the marketplace
// detail modal showing empty Help/Tools.
func TestChooseDevBootstrapDecision(t *testing.T) {
	stage := func(t *testing.T) (home, skillsRoot string) {
		t.Helper()
		home = t.TempDir()
		skillsRoot = filepath.Join(t.TempDir(), "skills")
		dbDir := filepath.Join(t.TempDir(), "db")
		if err := os.MkdirAll(dbDir, 0755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dbDir, err)
		}
		oldSkills, oldDB := cicySkillsDir, cicyDBDir
		cicySkillsDir, cicyDBDir = skillsRoot, dbDir
		t.Cleanup(func() {
			cicySkillsDir, cicyDBDir = oldSkills, oldDB
		})
		t.Setenv("HOME", home)
		return home, skillsRoot
	}

	stageAllSkillDocs := func(t *testing.T, home string) {
		t.Helper()
		paths := []string{filepath.Join(home, ".local", "bin", "cicy-skills")}
		for skillName := range agentgenApprovedMarketSkills {
			for _, profile := range []string{"codex", "claude", "opencode"} {
				paths = append(paths, filepath.Join(home, "."+profile, "skills", skillName, "SKILL.md"))
			}
		}
		for _, p := range paths {
			if err := ensureRuntimeDir(filepath.Dir(p), 0755); err != nil {
				t.Fatalf("ensureRuntimeDir(%s): %v", p, err)
			}
			if err := os.WriteFile(p, []byte("ok\n"), 0755); err != nil {
				t.Fatalf("WriteFile(%s): %v", p, err)
			}
		}
	}

	t.Run("SkipNotNeeded_everythingPresent", func(t *testing.T) {
		home, _ := stage(t)
		stageAllSkillDocs(t, home)
		if got := chooseDevBootstrapDecision(); got != devBootstrapSkipNotNeeded {
			t.Fatalf("got %v, want devBootstrapSkipNotNeeded", got)
		}
	})

	t.Run("ExtractFresh_emptyMachine", func(t *testing.T) {
		stage(t) // nothing staged → CLI missing → needsInstall=true; projectRoot dir absent
		if _, err := os.Stat(cicySkillsProjectDir()); !os.IsNotExist(err) {
			t.Fatalf("expected projectRoot to not exist, got err=%v", err)
		}
		if got := chooseDevBootstrapDecision(); got != devBootstrapExtractFresh {
			t.Fatalf("got %v, want devBootstrapExtractFresh", got)
		}
	})

	t.Run("WaitForBuild_sourceWithoutDist", func(t *testing.T) {
		_, skillsRoot := stage(t)
		// Create the project root but no dist binary — simulates a dev
		// who cloned the source but hasn't built it yet.
		projectRoot := filepath.Join(skillsRoot, "cicy-skills")
		if err := os.MkdirAll(projectRoot, 0755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", projectRoot, err)
		}
		// Touch a file so the dir clearly looks "occupied" — extract would
		// destroy it.
		if err := os.WriteFile(filepath.Join(projectRoot, "go.mod"), []byte("module x\n"), 0644); err != nil {
			t.Fatalf("WriteFile go.mod: %v", err)
		}
		if got := chooseDevBootstrapDecision(); got != devBootstrapWaitForBuild {
			t.Fatalf("got %v, want devBootstrapWaitForBuild", got)
		}
	})

	t.Run("InstallExisting_distBinaryReady", func(t *testing.T) {
		_, skillsRoot := stage(t)
		projectRoot := filepath.Join(skillsRoot, "cicy-skills")
		distBin := filepath.Join(projectRoot, "dist", "cicy-skills")
		if err := os.MkdirAll(filepath.Dir(distBin), 0755); err != nil {
			t.Fatalf("MkdirAll(dist): %v", err)
		}
		if err := os.WriteFile(distBin, []byte("#!/bin/sh\necho stub\n"), 0755); err != nil {
			t.Fatalf("WriteFile distBin: %v", err)
		}
		if got := chooseDevBootstrapDecision(); got != devBootstrapInstallExisting {
			t.Fatalf("got %v, want devBootstrapInstallExisting", got)
		}
	})

	t.Run("WaitForBuild_distBinaryNotExecutable", func(t *testing.T) {
		_, skillsRoot := stage(t)
		projectRoot := filepath.Join(skillsRoot, "cicy-skills")
		distBin := filepath.Join(projectRoot, "dist", "cicy-skills")
		if err := os.MkdirAll(filepath.Dir(distBin), 0755); err != nil {
			t.Fatalf("MkdirAll(dist): %v", err)
		}
		// Non-executable mode → should be treated like missing.
		if err := os.WriteFile(distBin, []byte("garbage"), 0644); err != nil {
			t.Fatalf("WriteFile distBin: %v", err)
		}
		if got := chooseDevBootstrapDecision(); got != devBootstrapWaitForBuild {
			t.Fatalf("got %v, want devBootstrapWaitForBuild (non-executable dist binary)", got)
		}
	})
}

func TestNeedsCicySkillsInstallSkipsUninstalled(t *testing.T) {
	home := t.TempDir()
	skillsRoot := filepath.Join(t.TempDir(), "skills")
	dbDir := filepath.Join(t.TempDir(), "db")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", dbDir, err)
	}
	oldSkillsDir, oldDBDir := cicySkillsDir, cicyDBDir
	cicySkillsDir, cicyDBDir = skillsRoot, dbDir
	t.Cleanup(func() {
		cicySkillsDir, cicyDBDir = oldSkillsDir, oldDBDir
	})
	t.Setenv("HOME", home)

	// Pick a skill to "uninstall" before populating files.
	var target string
	for name := range agentgenApprovedMarketSkills {
		target = name
		break
	}
	if target == "" {
		t.Fatal("agentgenApprovedMarketSkills is empty — test setup invalid")
	}

	// Stage the uninstall state file so uninstalledSkillsSet() returns target.
	if err := os.WriteFile(filepath.Join(dbDir, "skills-state.json"), []byte(`{"version":1,"uninstalled":["`+target+`"]}`), 0644); err != nil {
		t.Fatalf("write skills-state.json: %v", err)
	}

	required := []string{
		filepath.Join(home, ".local", "bin", "cicy-skills"),
	}
	for skillName := range agentgenApprovedMarketSkills {
		if skillName == target {
			continue // intentionally not staged — verifies skip works
		}
		for _, profile := range []string{"codex", "claude", "opencode"} {
			required = append(required, filepath.Join(home, "."+profile, "skills", skillName, "SKILL.md"))
		}
	}
	for _, path := range required {
		if err := ensureRuntimeDir(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("ensureRuntimeDir(%s) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte("ok\n"), 0755); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	if needsCicySkillsInstall() {
		t.Fatalf("needsCicySkillsInstall should be false when the only missing skill (%s) is user-uninstalled", target)
	}
}
