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

// readSkillManifestAt loads <dir>/manifest.json from disk. Used by backfill —
// install-time code paths already have the manifest in memory.
func readSkillManifestAt(dir string) (*Manifest, error) {
	p := filepath.Join(dir, "manifest.json")
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
	// Windows: the <name>.cmd shim for native (non-msys) spawns must exist
	// UNCONDITIONALLY (npm semantics) — including when the symlink below is
	// already correct, and on every install/repair/dev path. (w-10029's win
	// regression: shim was skipped whenever the symlink early-returned.)
	if err := ensureCmdShim(src, target); err != nil {
		return false, fmt.Errorf("cmd shim: %w", err)
	}
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
		dir := s.InstallDir
		if dir == "" {
			dir = skillDir(s.Name)
		}
		m, err := readSkillManifestAt(dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: read manifest: %w", s.Name, err))
			continue
		}
		fixed, perr := ensureBinSymlinksForSkill(dir, m)
		repaired = append(repaired, fixed...)
		errs = append(errs, perr...)
	}
	return repaired, errs
}

// EnsureAgentSurfacing re-surfaces every installed skill into each *detected*
// agent's skills dir (~/.<agent>/skills/<name>/). Idempotent — syncToAgents
// rewrites the symlinks each call.
//
// Why this exists: skills are surfaced only at install time. If an agent dir is
// later wiped (an agent CLI resets ~/.opencode), or an agent is installed AFTER
// the skills were (so they only synced to the agents present then), the already
// installed skills are never re-surfaced — `skill update` is a no-op at the same
// version and `ensurePreinstalledSkills` skips already-installed skills. This is
// the surfacing analogue of EnsureBinSymlinks, run on server startup. It also
// rewrites installed.json's agents_synced to match what's actually on disk.
func EnsureAgentSurfacing() (surfaced []string, errs []error) {
	cfg, err := loadInstalled()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{err}
	}
	agents, aerr := loadAgentsConfig()
	if aerr != nil || agents == nil || len(agents.Agents) == 0 {
		return nil, nil
	}
	changed := false
	for i := range cfg.Skills {
		s := &cfg.Skills[i]
		// Note: local (skill dev / ejected) skills are surfaced too — they are
		// real skills the agent should see, and cmdDev already syncs them to
		// every agent. Surfacing only refreshes symlinks, so it's safe to
		// re-run for them; skipping them would leave e.g. a dev'd skill
		// missing from agents that appeared after it was installed.
		dir := s.InstallDir
		if dir == "" {
			dir = skillDir(s.Name)
		}
		m, err := readSkillManifestAt(dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: read manifest: %w", s.Name, err))
			continue
		}
		before := append([]string(nil), s.AgentsSynced...)
		synced := syncToAgents(s.Name, dir, m, agents)
		for _, a := range synced {
			if !contains(before, a) {
				surfaced = append(surfaced, s.Name+"→"+a)
			}
		}
		if !sameStringSet(before, synced) {
			s.AgentsSynced = synced
			changed = true
		}
	}
	if changed {
		_ = writeInstalled(cfg)
	}
	return surfaced, errs
}

// sameStringSet reports whether a and b contain the same elements (order-insensitive).
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		seen[x] = true
	}
	for _, y := range b {
		if !seen[y] {
			return false
		}
	}
	return true
}
