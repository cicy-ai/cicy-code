// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package skillcmd

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// defaultAgents is written to ~/cicy-ai/skills/agents.json on first run.
var defaultAgents = AgentsConfig{
	SchemaVersion: 1,
	Agents: []Agent{
		{
			ID: "claude", Name: "Claude Code",
			SkillsDir: "~/.claude/skills", ManifestFile: "SKILL.md",
			Detect: &AgentDetect{Command: "claude", VersionFlag: "--version"},
		},
		{
			ID: "codex", Name: "Codex CLI",
			SkillsDir: "~/.agents/skills", ManifestFile: "SKILL.md",
			Detect: &AgentDetect{Command: "codex", VersionFlag: "--version"},
		},
		{
			ID: "opencode", Name: "OpenCode",
			SkillsDir: "~/.opencode/skills", ManifestFile: "SKILL.md",
			Detect: &AgentDetect{Command: "opencode", VersionFlag: "--version"},
		},
		{
			ID: "kiro", Name: "Kiro CLI",
			SkillsDir: "~/.kiro/skills", ManifestFile: "SKILL.md",
			Detect: &AgentDetect{Command: "kiro-cli", VersionFlag: "--version"},
		},
	},
}

// loadAgentsConfig reads agents.json, creating defaults on first run.
func loadAgentsConfig() (*AgentsConfig, error) {
	p := agentsJSONPath()
	if err := ensureDir(filepath.Dir(p)); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		// write defaults
		if err := writeAgentsConfig(&defaultAgents); err != nil {
			return nil, err
		}
		cp := defaultAgents
		return &cp, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg AgentsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func writeAgentsConfig(cfg *AgentsConfig) error {
	if err := ensureDir(skillsRoot()); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(agentsJSONPath(), buf, 0o644)
}

// syncToAgents copies (symlinks) skill doc files into each agent's
// skills_dir/<name>/. Returns the list of agent IDs successfully synced.
//
// Surfacing is UNCONDITIONAL — we no longer gate on the agent CLI being present
// on PATH. cicy ships a fixed, self-managed agent roster (claude/codex/opencode/
// kiro), and the coding CLIs are installed on demand AFTER the box boots. Gating
// on `exec.LookPath(<cli>)` meant a fresh box never linked skills into
// ~/.claude/skills etc. (the CLI isn't there yet), so a freshly-installed CLI came
// up with zero skills until the next restart. The skills dir is just a symlink
// tree into ~/cicy-ai/skills; pre-creating it for a not-yet-installed CLI is
// harmless and gives true out-of-the-box behavior.
func syncToAgents(name, sourceDir string, manifest *Manifest, cfg *AgentsConfig) []string {
	if !skillCompatibleWithAny(manifest, cfg) {
		return nil
	}

	docFiles := manifestDocFiles(manifest)

	synced := []string{}
	for _, a := range cfg.Agents {
		if !skillCompatibleWith(manifest, a.ID) {
			continue
		}
		ok := false
		for _, skillsDir := range agentSkillDirs(a) {
			dst := filepath.Join(skillsDir, name)
			_ = os.RemoveAll(dst)
			if err := os.MkdirAll(dst, 0o755); err != nil {
				continue
			}
			dirOK := true
			for _, rel := range docFiles {
				srcPath := filepath.Join(sourceDir, rel)
				if _, err := os.Stat(srcPath); err != nil {
					continue
				}
				dstPath := filepath.Join(dst, rel)
				_ = os.MkdirAll(filepath.Dir(dstPath), 0o755)
				_ = os.Remove(dstPath)
				if err := os.Symlink(srcPath, dstPath); err != nil {
					dirOK = false
					break
				}
			}
			// also link references/ if it exists
			refSrc := filepath.Join(sourceDir, "references")
			if st, err := os.Stat(refSrc); err == nil && st.IsDir() {
				refDst := filepath.Join(dst, "references")
				_ = os.RemoveAll(refDst)
				if err := os.Symlink(refSrc, refDst); err != nil {
					dirOK = false
				}
			}
			ok = ok || dirOK
		}
		if ok {
			synced = append(synced, a.ID)
		}
	}
	return synced
}

// removeFromAgents deletes <skills_dir>/<name>/ from every agent.
func removeFromAgents(name string, cfg *AgentsConfig) {
	for _, a := range cfg.Agents {
		for _, skillsDir := range agentSkillDirs(a) {
			_ = os.RemoveAll(filepath.Join(skillsDir, name))
		}
	}
}

// agentSkillDirs returns every discovery root that should receive a skill.
// Codex 0.144+ follows the Agent Skills standard at ~/.agents/skills; older
// releases used ~/.codex/skills. Surface to both so upgrades do not make an
// already-installed CiCy skill disappear from the Codex @ picker.
func agentSkillDirs(a Agent) []string {
	dirs := []string{expandHome(a.SkillsDir)}
	if a.ID == "codex" {
		dirs = append(dirs, expandHome("~/.agents/skills"), expandHome("~/.codex/skills"))
	}
	out := make([]string, 0, len(dirs))
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}

func manifestDocFiles(m *Manifest) []string {
	out := []string{}
	if m.Files != nil {
		for _, f := range []string{m.Files.SkillMD, m.Files.HelpMD, m.Files.ToolsMD, m.Files.Readme} {
			if f != "" {
				out = append(out, f)
			}
		}
	}
	if len(out) == 0 {
		// fallback to conventional names
		out = []string{"SKILL.md", "help.md", "tools.md", "README.md"}
	}
	return out
}

func skillCompatibleWith(m *Manifest, agentID string) bool {
	if len(m.CompatibleAgents) == 0 {
		return true // default *
	}
	for _, a := range m.CompatibleAgents {
		if a == "*" || a == agentID {
			return true
		}
	}
	return false
}

func skillCompatibleWithAny(m *Manifest, cfg *AgentsConfig) bool {
	if len(m.CompatibleAgents) == 0 {
		return true
	}
	for _, want := range m.CompatibleAgents {
		if want == "*" {
			return true
		}
		for _, a := range cfg.Agents {
			if a.ID == want {
				return true
			}
		}
	}
	return false
}
