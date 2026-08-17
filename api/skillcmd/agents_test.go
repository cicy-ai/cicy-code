// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package skillcmd

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestAgentSkillDirsCodexIncludesModernAndLegacyRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := agentSkillDirs(Agent{ID: "codex", SkillsDir: "~/.codex/skills"})
	want := []string{
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".agents", "skills"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agentSkillDirs() = %#v, want %#v", got, want)
	}
}

func TestAgentSkillDirsNonCodexUsesConfiguredRootOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := agentSkillDirs(Agent{ID: "claude", SkillsDir: "~/.claude/skills"})
	want := []string{filepath.Join(home, ".claude", "skills")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agentSkillDirs() = %#v, want %#v", got, want)
	}
}
