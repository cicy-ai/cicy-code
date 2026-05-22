package skillcmd

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"
)

func loadInstalled() (*InstalledConfig, error) {
	data, err := os.ReadFile(installedJSONPath())
	if errors.Is(err, fs.ErrNotExist) {
		return &InstalledConfig{SchemaVersion: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg InstalledConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = 1
	}
	return &cfg, nil
}

func writeInstalled(cfg *InstalledConfig) error {
	if err := ensureDir(skillsRoot()); err != nil {
		return err
	}
	sort.SliceStable(cfg.Skills, func(i, j int) bool {
		return cfg.Skills[i].Name < cfg.Skills[j].Name
	})
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(installedJSONPath(), buf, 0o644)
}

// upsertInstalled inserts or replaces an entry by name.
func upsertInstalled(cfg *InstalledConfig, entry InstalledSkill) {
	for i, s := range cfg.Skills {
		if s.Name == entry.Name {
			cfg.Skills[i] = entry
			return
		}
	}
	cfg.Skills = append(cfg.Skills, entry)
}

func removeInstalled(cfg *InstalledConfig, name string) bool {
	for i, s := range cfg.Skills {
		if s.Name == name {
			cfg.Skills = append(cfg.Skills[:i], cfg.Skills[i+1:]...)
			return true
		}
	}
	return false
}

func findInstalled(cfg *InstalledConfig, name string) *InstalledSkill {
	for i, s := range cfg.Skills {
		if s.Name == name {
			return &cfg.Skills[i]
		}
	}
	return nil
}
