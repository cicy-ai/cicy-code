package skillcmd

// api.go — public wrappers used by the mgr HTTP layer.
//
// Unlike cmd*.go which writes to stdout and exits via os.Exit, the API
// helpers here:
//   - take an io.Writer for human-readable progress (mgr streams it back to
//     the UI),
//   - return a typed result + error rather than printing,
//   - never call os.Exit.
//
// These are the only entry points the mgr should call. The CLI dispatch
// (Run() in main.go) goes through cmd*.go so the user-facing CLI stays
// stable; the API helpers below share the same internal helpers.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// InstallResult is what /api/skill-market/<name>/install returns to the UI.
type InstallResult struct {
	Name         string         `json:"name"`
	Version      string         `json:"version"`
	Path         string         `json:"path"`
	SHA256       string         `json:"sha256"`
	AgentsSynced []string       `json:"agents_synced"`
	Installed    InstalledSkill `json:"installed"`
	LogText      string         `json:"log_text"`
}

// PublicInstall downloads + extracts + symlinks + agent-syncs + records
// installed.json. Logs each step to `sink`. Returns the recorded
// InstalledSkill on success.
func PublicInstall(spec string, sink io.Writer) (*InstallResult, error) {
	if sink == nil {
		sink = io.Discard
	}
	buf := &bytes.Buffer{}
	mw := io.MultiWriter(sink, buf)

	name, wantVersion := parseNameVersion(spec)

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
		return nil, err
	}
	m := d.Manifest
	if m.Publish == nil || m.Publish.DownloadURL == "" {
		return nil, fmt.Errorf("manifest missing publish.download_url")
	}

	fmt.Fprintf(mw, "→ installing %s@%s\n", m.Name, m.Version)
	fmt.Fprintf(mw, "  url:    %s\n", m.Publish.DownloadURL)
	fmt.Fprintf(mw, "  sha256: %s\n", m.Publish.SHA256)

	// 2. download + verify
	fmt.Fprintln(mw, "  downloading...")
	zipPath, err := downloadAndVerify(m.Name, m.Version, m.Publish.DownloadURL, m.Publish.SHA256)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}

	// 3. extract
	if err := ensureDir(skillsRoot()); err != nil {
		return nil, err
	}
	fmt.Fprintln(mw, "  extracting...")
	skillPath, err := extractZip(zipPath, m.Name, skillsRoot())
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}

	// 4. chmod entry
	if m.Entry != "" {
		_ = chmodExec(filepath.Join(skillPath, m.Entry))
	}

	// 5. npm ci if needed
	if m.NpmDependencies {
		fmt.Fprintln(mw, "  npm ci --omit=dev --ignore-scripts ...")
		if _, err := exec.LookPath("npm"); err != nil {
			return nil, fmt.Errorf("npm not found on PATH (required by manifest.npm_dependencies)")
		}
		if err := runNpmCI(skillPath); err != nil {
			return nil, fmt.Errorf("npm ci: %w", err)
		}
	}

	// 6. symlink ~/.local/bin/<name> → entry (and bin_aliases)
	if _, errs := ensureBinSymlinksForSkill(skillPath, &m); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(mw, "  warn: symlink failed: %v\n", e)
		}
	}

	// 7. sync to agents
	agents, err := loadAgentsConfig()
	if err != nil {
		return nil, err
	}
	synced := syncToAgents(m.Name, skillPath, &m, agents)

	// 8. update installed.json
	cfg, err := loadInstalled()
	if err != nil {
		return nil, err
	}
	entry := InstalledSkill{
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
	}
	upsertInstalled(cfg, entry)
	if err := writeInstalled(cfg); err != nil {
		return nil, err
	}

	fmt.Fprintf(mw, "✓ installed %s@%s → %s\n", m.Name, m.Version, skillPath)
	if len(synced) > 0 {
		fmt.Fprintf(mw, "  synced to: %s\n", strings.Join(synced, ", "))
	}

	return &InstallResult{
		Name:         m.Name,
		Version:      m.Version,
		Path:         skillPath,
		SHA256:       m.Publish.SHA256,
		AgentsSynced: synced,
		Installed:    entry,
		LogText:      buf.String(),
	}, nil
}

// PublicRemove uninstalls a skill, returning the removed entry.
func PublicRemove(name string, sink io.Writer) (*InstalledSkill, error) {
	if sink == nil {
		sink = io.Discard
	}
	cfg, err := loadInstalled()
	if err != nil {
		return nil, err
	}
	entry := findInstalled(cfg, name)
	if entry == nil {
		return nil, fmt.Errorf("skill not installed: %s", name)
	}

	// 1. remove skill dir
	_ = os.RemoveAll(skillDir(name))
	// 2. remove ~/.local/bin/<name>
	_ = os.Remove(localBinPath(name))
	// 3. remove agent skills dirs
	if agents, _ := loadAgentsConfig(); agents != nil {
		removeFromAgents(name, agents)
	}
	// 4. update installed.json
	removed := *entry
	removeInstalled(cfg, name)
	if err := writeInstalled(cfg); err != nil {
		return nil, err
	}
	fmt.Fprintf(sink, "✓ removed %s@%s\n", name, removed.Version)
	return &removed, nil
}

// PublicUpdate updates a skill to the latest registry version. Returns
// the From/To version pair, whether an update happened, and the new
// install record (nil if no update was performed).
type UpdateResult struct {
	Name      string         `json:"name"`
	From      string         `json:"from"`
	To        string         `json:"to"`
	Updated   bool           `json:"updated"`
	Installed InstalledSkill `json:"installed,omitempty"`
}

func PublicUpdate(name string, sink io.Writer) (*UpdateResult, error) {
	if sink == nil {
		sink = io.Discard
	}
	cfg, err := loadInstalled()
	if err != nil {
		return nil, err
	}
	entry := findInstalled(cfg, name)
	if entry == nil {
		return nil, fmt.Errorf("skill not installed: %s", name)
	}
	if entry.Source.Type == "local" {
		return nil, fmt.Errorf("local source — use 'skill dev' to refresh")
	}

	r := NewRegistry()
	d, err := r.GetDetail(name)
	if err != nil {
		return nil, err
	}
	latest := d.Manifest.Version
	if latest == entry.Version {
		fmt.Fprintf(sink, "  · %s: already at %s\n", name, entry.Version)
		return &UpdateResult{Name: name, From: entry.Version, To: latest, Updated: false}, nil
	}

	fmt.Fprintf(sink, "→ updating %s: %s → %s\n", name, entry.Version, latest)
	res, err := PublicInstall(name, sink)
	if err != nil {
		return nil, err
	}
	return &UpdateResult{
		Name: name, From: entry.Version, To: res.Version, Updated: true,
		Installed: res.Installed,
	}, nil
}

// PublicInstalled returns the current installed.json contents.
func PublicInstalled() (*InstalledConfig, error) {
	return loadInstalled()
}
