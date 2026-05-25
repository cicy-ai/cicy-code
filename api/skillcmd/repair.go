package skillcmd

// repair.go — idempotent `~/.local/bin/<name>` symlink management.
//
// Why this exists: until 2026-05, the three install code paths (api.go,
// cmd.go, cmd_extra.go) each inlined a `makeSymlink(...)` with the error
// swallowed as a non-fatal warning written only to the install sink. When
// the symlink silently failed, installed.json still recorded the skill as
// "installed", so the bug persisted invisibly — only 1/16 skills on this
// host ended up on $PATH despite all 16 being recorded successful.
//
// The fix has three parts:
//   1. ensureBinSymlink — single source of truth, called from all three
//      install paths. Idempotent: a correct existing symlink is a no-op.
//   2. EnsureBinSymlinks — walks installed.json + reads each skill's
//      on-disk manifest.json and (re)links any missing/stale entries.
//      Used at server startup for backfill repair.
//   3. callers in setup.go log results via log.Printf so any future
//      failure is visible in systemd journal, not just the UI sink.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// readSkillManifest loads <skillsRoot>/<name>/manifest.json from disk.
// Used by backfill — install-time code paths already have the manifest in
// memory.
func readSkillManifest(name string) (*Manifest, error) {
	p := filepath.Join(skillDir(name), "manifest.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &m, nil
}

// ensureBinSymlink makes sure ~/.local/bin/<linkName> is a symlink
// pointing at <skillPath>/<entry>. Returns true when the link was
// created or repaired, false when it was already correct.
//
// Errors when: entry is empty, entry file is missing, mkdir/symlink
// itself fails. The entry file gets chmod +x as a side effect.
func ensureBinSymlink(skillPath, entry, linkName string) (bool, error) {
	if entry == "" {
		return false, errors.New("manifest entry is empty")
	}
	if linkName == "" {
		return false, errors.New("link name is empty")
	}
	src := filepath.Join(skillPath, entry)
	if _, err := os.Stat(src); err != nil {
		return false, fmt.Errorf("entry not found: %w", err)
	}
	_ = chmodExec(src)

	target := localBinPath(linkName)
	if got, err := os.Readlink(target); err == nil && got == src {
		return false, nil
	}
	if err := makeSymlink(src, target); err != nil {
		return false, err
	}
	return true, nil
}

// ensureBinSymlinksForSkill ensures the primary name + all bin_aliases
// of a single skill have correct symlinks. Aggregates per-link errors —
// one broken alias does not abort the others.
func ensureBinSymlinksForSkill(skillPath string, m *Manifest) (repaired []string, errs []error) {
	if m == nil || m.Entry == "" {
		return nil, nil
	}
	if fixed, err := ensureBinSymlink(skillPath, m.Entry, m.Name); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", m.Name, err))
	} else if fixed {
		repaired = append(repaired, m.Name)
	}
	for _, alias := range m.BinAliases {
		if fixed, err := ensureBinSymlink(skillPath, m.Entry, alias); err != nil {
			errs = append(errs, fmt.Errorf("%s (alias of %s): %w", alias, m.Name, err))
		} else if fixed {
			repaired = append(repaired, alias)
		}
	}
	return repaired, errs
}

// EnsureBinSymlinks walks installed.json and (re)creates any missing or
// stale ~/.local/bin/<name> symlinks. Idempotent: returns empty slices
// on a clean host.
//
// Per-skill failures (missing manifest.json, missing entry file, broken
// symlink) are returned in errs but do NOT abort the walk — one broken
// skill should never block the rest.
//
// This is the public backfill entry point called from server startup.
func EnsureBinSymlinks() (repaired []string, errs []error) {
	cfg, err := loadInstalled()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{err}
	}
	for _, s := range cfg.Skills {
		m, err := readSkillManifest(s.Name)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: read manifest: %w", s.Name, err))
			continue
		}
		fixed, perr := ensureBinSymlinksForSkill(skillDir(s.Name), m)
		repaired = append(repaired, fixed...)
		errs = append(errs, perr...)
	}
	return repaired, errs
}
