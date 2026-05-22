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

// ── small util ─────────────────────────────────────────────────────────────

func emitJSON(v interface{}) {
	b, _ := json.MarshalIndent(v, "", "  ")
	os.Stdout.Write(b)
	os.Stdout.Write([]byte{'\n'})
}

func parseNameVersion(arg string) (name, version string) {
	if i := strings.Index(arg, "@"); i > 0 {
		return arg[:i], arg[i+1:]
	}
	return arg, ""
}

// ── list ───────────────────────────────────────────────────────────────────

func cmdList(args []string) error {
	jsonOut := contains(args, "--json")
	q := flagValue(args, "--query")
	cat := flagValue(args, "--category")
	agent := flagValue(args, "--agent")

	r := NewRegistry()
	resp, err := r.ListSkills(q, cat, agent, 0, 0)
	if err != nil {
		return err
	}

	if jsonOut {
		emitJSON(map[string]interface{}{"ok": true, "data": resp})
		return nil
	}

	if resp.Total == 0 {
		fmt.Println("(no skills found)")
		return nil
	}
	fmt.Printf("%-22s %-8s %-12s %s\n", "NAME", "VERSION", "CATEGORY", "DESCRIPTION")
	fmt.Println(strings.Repeat("-", 80))
	for _, s := range resp.Skills {
		desc := s.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		fmt.Printf("%-22s %-8s %-12s %s\n", s.Name, s.Version, s.Category, desc)
	}
	fmt.Printf("\n%d skill(s).\n", resp.Total)
	return nil
}

// ── info ───────────────────────────────────────────────────────────────────

func cmdInfo(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cicy-code skill info <name>[@<version>]")
	}
	jsonOut := contains(args, "--json")
	target, _ := positional(args)
	if len(target) == 0 {
		return fmt.Errorf("missing <name>")
	}
	name, version := parseNameVersion(target[0])

	r := NewRegistry()
	var d *SkillDetail
	var err error
	if version == "" {
		d, err = r.GetDetail(name)
	} else {
		d, err = r.GetVersion(name, version)
	}
	if err != nil {
		return err
	}

	if jsonOut {
		emitJSON(map[string]interface{}{"ok": true, "data": d})
		return nil
	}

	m := d.Manifest
	fmt.Printf("Name:        %s\n", m.Name)
	fmt.Printf("Version:     %s\n", m.Version)
	fmt.Printf("Title:       %s\n", m.Title)
	fmt.Printf("Description: %s\n", m.Description)
	fmt.Printf("Category:    %s\n", m.Category)
	if len(m.Tags) > 0 {
		fmt.Printf("Tags:        %s\n", strings.Join(m.Tags, ", "))
	}
	fmt.Printf("Author:      %s\n", m.Author)
	fmt.Printf("License:     %s\n", m.License)
	fmt.Printf("Runtime:     node %s\n", m.Runtime.Node)
	if m.Publish != nil {
		fmt.Printf("Published:   %s\n", m.Publish.PublishedAt)
		fmt.Printf("Size:        %d bytes\n", m.Publish.Size)
		fmt.Printf("Source:      %s @ %s (%s)\n",
			m.Publish.Source.Repository, m.Publish.Source.Tag, m.Publish.Source.Type)
	}
	if len(m.Permissions) > 0 {
		fmt.Printf("Permissions: %s\n", strings.Join(m.Permissions, ", "))
	}
	return nil
}

// ── installed ──────────────────────────────────────────────────────────────

func cmdInstalled(args []string) error {
	jsonOut := contains(args, "--json")
	cfg, err := loadInstalled()
	if err != nil {
		return err
	}
	if jsonOut {
		emitJSON(map[string]interface{}{"ok": true, "data": cfg})
		return nil
	}
	if len(cfg.Skills) == 0 {
		fmt.Println("(no skills installed)")
		return nil
	}
	fmt.Printf("%-22s %-10s %-10s %s\n", "NAME", "VERSION", "SOURCE", "INSTALLED")
	fmt.Println(strings.Repeat("-", 70))
	for _, s := range cfg.Skills {
		fmt.Printf("%-22s %-10s %-10s %s\n",
			s.Name, s.Version, s.Source.Type, s.InstalledAt.Format(time.RFC3339))
	}
	return nil
}

// ── install ────────────────────────────────────────────────────────────────

func cmdInstall(args []string) error {
	jsonOut := contains(args, "--json")
	target, _ := positional(args)
	if len(target) == 0 {
		return fmt.Errorf("usage: cicy-code skill install <name>[@<version>]")
	}
	name, wantVersion := parseNameVersion(target[0])

	// 1. resolve manifest
	r := NewRegistry()
	var d *SkillDetail
	var err error
	if wantVersion == "" {
		d, err = r.GetDetail(name)
	} else {
		d, err = r.GetVersion(name, wantVersion)
	}
	if err != nil {
		return err
	}
	m := d.Manifest
	if m.Publish == nil || m.Publish.DownloadURL == "" {
		return fmt.Errorf("manifest missing publish.download_url")
	}

	if !jsonOut {
		fmt.Printf("→ installing %s@%s\n", m.Name, m.Version)
		fmt.Printf("  url:    %s\n", m.Publish.DownloadURL)
		fmt.Printf("  sha256: %s\n", m.Publish.SHA256)
	}

	// 2. download + verify
	zipPath, err := downloadAndVerify(m.Name, m.Version, m.Publish.DownloadURL, m.Publish.SHA256)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}

	// 3. extract
	if err := ensureDir(skillsRoot()); err != nil {
		return err
	}
	skillPath, err := extractZip(zipPath, m.Name, skillsRoot())
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	// 4. chmod entry
	if m.Entry != "" {
		_ = chmodExec(filepath.Join(skillPath, m.Entry))
	}

	// 5. npm ci if needed
	if m.NpmDependencies {
		if !jsonOut {
			fmt.Println("  npm ci --omit=dev --ignore-scripts ...")
		}
		if _, err := exec.LookPath("npm"); err != nil {
			return fmt.Errorf("npm not found on PATH (required by manifest.npm_dependencies)")
		}
		if err := runNpmCI(skillPath); err != nil {
			return fmt.Errorf("npm ci: %w", err)
		}
	}

	// 6. symlink ~/.local/bin/<name> → entry
	if m.Entry != "" {
		entryPath := filepath.Join(skillPath, m.Entry)
		if err := makeSymlink(entryPath, localBinPath(m.Name)); err != nil {
			// non-fatal; warn
			if !jsonOut {
				fmt.Fprintf(os.Stderr, "  warn: symlink ~/.local/bin/%s failed: %v\n", m.Name, err)
			}
		}
		// also bin_aliases
		for _, alias := range m.BinAliases {
			_ = makeSymlink(entryPath, localBinPath(alias))
		}
	}

	// 7. sync to agents
	agents, err := loadAgentsConfig()
	if err != nil {
		return err
	}
	synced := syncToAgents(m.Name, skillPath, &m, agents)

	// 8. update installed.json
	cfg, err := loadInstalled()
	if err != nil {
		return err
	}
	upsertInstalled(cfg, InstalledSkill{
		Name:        m.Name,
		Version:     m.Version,
		InstalledAt: time.Now().UTC(),
		Source: InstalledSource{
			Type:        "registry",
			DownloadURL: m.Publish.DownloadURL,
			Repository:  m.Publish.Source.Repository,
			Ref:         m.Publish.Source.Tag,
		},
		SHA256:       m.Publish.SHA256,
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
			"path":          skillPath,
			"sha256":        m.Publish.SHA256,
			"agents_synced": synced,
		})
	} else {
		fmt.Printf("✓ installed %s@%s → %s\n", m.Name, m.Version, skillPath)
		if len(synced) > 0 {
			fmt.Printf("  synced to: %s\n", strings.Join(synced, ", "))
		}
	}
	return nil
}

// ── remove ─────────────────────────────────────────────────────────────────

func cmdRemove(args []string) error {
	jsonOut := contains(args, "--json")
	target, _ := positional(args)
	if len(target) == 0 {
		return fmt.Errorf("usage: cicy-code skill remove <name>")
	}
	name := target[0]

	cfg, err := loadInstalled()
	if err != nil {
		return err
	}
	entry := findInstalled(cfg, name)
	if entry == nil {
		return fmt.Errorf("skill not installed: %s", name)
	}

	// 1. remove skill dir
	_ = os.RemoveAll(skillDir(name))

	// 2. remove ~/.local/bin/<name>
	_ = os.Remove(localBinPath(name))

	// 3. remove agent skills dirs
	agents, _ := loadAgentsConfig()
	if agents != nil {
		removeFromAgents(name, agents)
	}

	// 4. update installed.json
	removeInstalled(cfg, name)
	if err := writeInstalled(cfg); err != nil {
		return err
	}

	if jsonOut {
		emitJSON(map[string]interface{}{
			"ok":      true,
			"name":    name,
			"version": entry.Version,
		})
	} else {
		fmt.Printf("✓ removed %s@%s\n", name, entry.Version)
	}
	return nil
}

// ── flag helpers ──────────────────────────────────────────────────────────

func contains(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// flagValue: looks for "--key" "value" or "--key=value".
func flagValue(args []string, key string) string {
	for i, a := range args {
		if a == key && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, key+"=") {
			return strings.TrimPrefix(a, key+"=")
		}
	}
	return ""
}

// positional returns args that don't start with "--" and aren't values for
// known flags. Best-effort: we treat "--key value" as 2 consumed tokens.
var knownFlagsWithValue = map[string]bool{
	"--query":    true,
	"--category": true,
	"--agent":    true,
	"--version":  true,
}

func positional(args []string) ([]string, []string) {
	var pos, flags []string
	skipNext := false
	for i, a := range args {
		if skipNext {
			skipNext = false
			flags = append(flags, a)
			continue
		}
		if strings.HasPrefix(a, "--") {
			flags = append(flags, a)
			if knownFlagsWithValue[a] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				skipNext = true
			}
			continue
		}
		pos = append(pos, a)
	}
	return pos, flags
}
