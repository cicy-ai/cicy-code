package skillcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ── update ─────────────────────────────────────────────────────────────────

func cmdUpdate(args []string) error {
	jsonOut := contains(args, "--json")
	all := contains(args, "--all")
	pos, _ := positional(args)

	cfg, err := loadInstalled()
	if err != nil {
		return err
	}

	var targets []string
	if all {
		for _, s := range cfg.Skills {
			targets = append(targets, s.Name)
		}
	} else {
		if len(pos) == 0 {
			return fmt.Errorf("usage: cicy-code skill update <name> | --all")
		}
		targets = pos
	}

	type updateResult struct {
		Name    string `json:"name"`
		From    string `json:"from"`
		To      string `json:"to"`
		Updated bool   `json:"updated"`
		Error   string `json:"error,omitempty"`
	}
	results := []updateResult{}

	for _, name := range targets {
		entry := findInstalled(cfg, name)
		if entry == nil {
			results = append(results, updateResult{Name: name, Error: "not installed"})
			continue
		}
		if entry.Source.Type == "local" {
			results = append(results, updateResult{Name: name, From: entry.Version, Error: "local source — use 'skill dev' to refresh"})
			continue
		}
		// fetch latest from whichever source currently has it
		r, err := registryForSkill(name, "")
		if err != nil {
			results = append(results, updateResult{Name: name, From: entry.Version, Error: err.Error()})
			continue
		}
		d, err := r.GetDetail(name)
		if err != nil {
			results = append(results, updateResult{Name: name, From: entry.Version, Error: err.Error()})
			continue
		}
		latest := d.Manifest.Version
		if latest == entry.Version {
			results = append(results, updateResult{Name: name, From: entry.Version, To: latest, Updated: false})
			continue
		}
		// reuse install path: install latest replaces existing
		if !jsonOut {
			fmt.Printf("→ updating %s: %s → %s\n", name, entry.Version, latest)
		}
		if err := cmdInstall([]string{name}); err != nil {
			results = append(results, updateResult{Name: name, From: entry.Version, To: latest, Error: err.Error()})
			continue
		}
		results = append(results, updateResult{Name: name, From: entry.Version, To: latest, Updated: true})
	}

	if jsonOut {
		emitJSON(map[string]interface{}{"ok": true, "results": results})
		return nil
	}
	for _, r := range results {
		switch {
		case r.Error != "":
			fmt.Printf("  ✗ %s: %s\n", r.Name, r.Error)
		case r.Updated:
			fmt.Printf("  ✓ %s: %s → %s\n", r.Name, r.From, r.To)
		default:
			fmt.Printf("  · %s: already at %s\n", r.Name, r.From)
		}
	}
	return nil
}

// ── dev ────────────────────────────────────────────────────────────────────
//
// `cicy-code skill dev <path>` — install a local skill directory directly,
// bypassing the registry. Useful for skill authors testing changes.
//
// Behavior:
//   1. Read <path>/manifest.json (must exist, must validate basic fields).
//   2. Copy (NOT zip) the directory tree into ~/cicy-ai/skills/<name>/.
//      We use a fresh sync so deletions in the source are reflected.
//   3. Run npm ci if manifest.npm_dependencies (and node_modules absent).
//   4. chmod +x bin/<name>, create ~/.local/bin/<name> symlink.
//   5. Sync to agent skills_dirs.
//   6. Mark in installed.json with source.type="local" + source.path=<abs path>.

func cmdDev(args []string) error {
	jsonOut := contains(args, "--json")
	pos, _ := positional(args)
	if len(pos) == 0 {
		return fmt.Errorf("usage: cicy-code skill dev <path-to-skill-dir>")
	}
	src, err := filepath.Abs(pos[0])
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(src, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest.json: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if m.Name == "" || m.Entry == "" {
		return fmt.Errorf("manifest missing name or entry")
	}

	target := skillDir(m.Name)

	// Clean and copy
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := copyTree(src, target); err != nil {
		return fmt.Errorf("copy: %w", err)
	}

	if !jsonOut {
		fmt.Printf("→ dev install %s@%s from %s\n", m.Name, m.Version, src)
	}

	// chmod entry
	_ = chmodExec(filepath.Join(target, m.Entry))

	// npm ci if needed
	if m.NpmDependencies {
		if _, err := os.Stat(filepath.Join(target, "node_modules")); os.IsNotExist(err) {
			if !jsonOut {
				fmt.Println("  npm ci ...")
			}
			if _, err := exec.LookPath("npm"); err != nil {
				return fmt.Errorf("npm not found")
			}
			if err := runNpmCI(target); err != nil {
				return fmt.Errorf("npm ci: %w", err)
			}
		}
	}

	// symlinks
	if _, errs := ensureBinSymlinksForSkill(target, &m); len(errs) > 0 && !jsonOut {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  warn: symlink failed: %v\n", e)
		}
	}

	// sync agents
	agents, err := loadAgentsConfig()
	if err != nil {
		return err
	}
	synced := syncToAgents(m.Name, target, &m, agents)

	// installed.json
	cfg, err := loadInstalled()
	if err != nil {
		return err
	}
	upsertInstalled(cfg, InstalledSkill{
		Name:        m.Name,
		Version:     m.Version,
		InstalledAt: time.Now().UTC(),
		Source: InstalledSource{
			Type: "local",
			Path: src,
		},
		AgentsSynced: synced,
	})
	if err := writeInstalled(cfg); err != nil {
		return err
	}

	if jsonOut {
		emitJSON(map[string]interface{}{
			"ok":            true,
			"name":          m.Name,
			"version":       m.Version,
			"path":          target,
			"source":        src,
			"agents_synced": synced,
		})
	} else {
		fmt.Printf("✓ dev installed %s@%s → %s\n", m.Name, m.Version, target)
		fmt.Printf("  source: %s\n", src)
		if len(synced) > 0 {
			fmt.Printf("  synced to: %s\n", strings.Join(synced, ", "))
		}
	}
	return nil
}

// copyTree recursively copies src → dst. Skips node_modules and .git.
func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if name == "node_modules" || name == ".git" || name == ".DS_Store" {
			continue
		}
		s := filepath.Join(src, name)
		d := filepath.Join(dst, name)
		if e.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
			continue
		}
		// copy file, preserving mode
		info, err := e.Info()
		if err != nil {
			return err
		}
		buf, err := os.ReadFile(s)
		if err != nil {
			return err
		}
		if err := os.WriteFile(d, buf, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

// ── search ─────────────────────────────────────────────────────────────────

func cmdSearch(args []string) error {
	pos, _ := positional(args)
	if len(pos) == 0 {
		return fmt.Errorf("usage: cicy-code skill search <query>")
	}
	q := strings.Join(pos, " ")
	jsonOut := contains(args, "--json")

	items, err := mergedCatalog(q, "", "")
	if err != nil {
		return err
	}

	if jsonOut {
		emitJSON(map[string]interface{}{"ok": true, "data": items})
		return nil
	}

	if len(items) == 0 {
		fmt.Printf("(no skills match %q)\n", q)
		return nil
	}
	fmt.Printf("%-22s %-8s %-12s %-10s %s\n", "NAME", "VERSION", "CATEGORY", "SOURCE", "DESCRIPTION")
	fmt.Println(strings.Repeat("-", 90))
	for _, it := range items {
		s := it.Summary
		desc := s.Description
		if len(desc) > 48 {
			desc = desc[:45] + "..."
		}
		fmt.Printf("%-22s %-8s %-12s %-10s %s\n", s.Name, s.Version, s.Category, it.Source, desc)
	}
	fmt.Printf("\n%d skill(s) match %q.\n", len(items), q)
	return nil
}

// ── eject ──────────────────────────────────────────────────────────────────
//
// `cicy-code skill eject <name>` — convert an installed registry skill into a
// local-source one. Files stay in place under ~/cicy-ai/skills/<name>/; only
// the installed.json entry's source flips to {type:"local", path:<dir>},
// matching what `skill dev` records. Future `skill update` then skips it.

func cmdEject(args []string) error {
	jsonOut := contains(args, "--json")
	pos, _ := positional(args)
	if len(pos) == 0 {
		return fmt.Errorf("usage: cicy-code skill eject <name>")
	}
	name := pos[0]
	entry, err := PublicEject(name, os.Stdout)
	if err != nil {
		return err
	}
	if jsonOut {
		emitJSON(map[string]interface{}{
			"ok":      true,
			"name":    entry.Name,
			"version": entry.Version,
			"source":  entry.Source,
		})
	}
	return nil
}
