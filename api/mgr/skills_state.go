package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// skills-state.json tracks user-driven decisions about the marketplace catalog
// that must survive container restarts. The bootstrap re-installs everything
// from the embedded tarball; without this state, every restart would resurrect
// skills the user already uninstalled.
//
// Schema (versioned so we can migrate later without breaking older files):
//   {
//     "version": 1,
//     "uninstalled": ["cicy-mihomo", "frp-server"]
//   }
//
// Only catalog entries (marketSkillsCatalog) ever appear here. User-created
// skills under ~/.{claude,codex,opencode}/skills/<name>/ are never touched.

type skillsState struct {
	Version     int      `json:"version"`
	Uninstalled []string `json:"uninstalled"`
}

const skillsStateVersion = 1

var skillsStateMu sync.Mutex

func skillsStatePath() string {
	return filepath.Join(cicyDBDir, "skills-state.json")
}

func loadSkillsState() (skillsState, error) {
	state := skillsState{Version: skillsStateVersion}
	data, err := os.ReadFile(skillsStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if len(data) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return skillsState{Version: skillsStateVersion}, err
	}
	if state.Version == 0 {
		state.Version = skillsStateVersion
	}
	return state, nil
}

func saveSkillsState(state skillsState) error {
	if state.Version == 0 {
		state.Version = skillsStateVersion
	}
	if state.Uninstalled == nil {
		state.Uninstalled = []string{}
	}
	sort.Strings(state.Uninstalled)
	if err := os.MkdirAll(cicyDBDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(cicyDBDir, ".skills-state.json.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, skillsStatePath())
}

func uninstalledSkillsSet() map[string]struct{} {
	out := map[string]struct{}{}
	skillsStateMu.Lock()
	defer skillsStateMu.Unlock()
	state, err := loadSkillsState()
	if err != nil {
		return out
	}
	for _, name := range state.Uninstalled {
		out[name] = struct{}{}
	}
	return out
}

func markSkillUninstalled(name string) error {
	skillsStateMu.Lock()
	defer skillsStateMu.Unlock()
	state, err := loadSkillsState()
	if err != nil {
		return err
	}
	for _, existing := range state.Uninstalled {
		if existing == name {
			return nil
		}
	}
	state.Uninstalled = append(state.Uninstalled, name)
	return saveSkillsState(state)
}

func markSkillInstalled(name string) error {
	skillsStateMu.Lock()
	defer skillsStateMu.Unlock()
	state, err := loadSkillsState()
	if err != nil {
		return err
	}
	filtered := state.Uninstalled[:0]
	for _, existing := range state.Uninstalled {
		if existing != name {
			filtered = append(filtered, existing)
		}
	}
	state.Uninstalled = filtered
	return saveSkillsState(state)
}
