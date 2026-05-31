package skillcmd

import (
	"os"
	"path/filepath"
	"strings"
)

// Default registry, can be overridden by env CICY_SKILLS_REGISTRY.
const DefaultRegistry = "https://skills.cicy-ai.com"

// Layout helpers — all under ~/cicy-ai/skills/<name>/
func registryURL() string {
	if v := os.Getenv("CICY_SKILLS_REGISTRY"); v != "" {
		return v
	}
	return DefaultRegistry
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.Getenv("HOME")
}

// ~/cicy-ai/skills
func skillsRoot() string {
	if v := os.Getenv("CICY_SKILLS_ROOT"); v != "" {
		return v
	}
	return filepath.Join(homeDir(), "cicy-ai", "skills")
}

func skillDir(name string) string { return filepath.Join(skillsRoot(), name) }

// Source-based install layout (see InstalledSkill.InstallDir):
//
//	~/cicy-ai/skills/<name>            public registry (flat, default)
//	~/cicy-ai/skills/private/<name>    your own local registry (localhost)
//	~/cicy-ai/skills/team/<src>/<name> another team's private registry
//
// "private" and "team" are reserved top-level dirs (mgr scanUserSkills skips
// them).
func privateSkillsParent() string        { return filepath.Join(skillsRoot(), "private") }
func teamSkillsParent(src string) string { return filepath.Join(skillsRoot(), "team", src) }

// installedDir resolves where an installed skill lives, honoring the recorded
// source-based layout, falling back to the flat path for legacy installs.
func installedDir(name string) string {
	if cfg, err := loadInstalled(); err == nil {
		if e := findInstalled(cfg, name); e != nil && e.InstallDir != "" {
			return e.InstallDir
		}
	}
	return skillDir(name)
}
func cacheDir() string          { return filepath.Join(skillsRoot(), ".cache") }
func installedJSONPath() string { return filepath.Join(skillsRoot(), "installed.json") }
func agentsJSONPath() string    { return filepath.Join(skillsRoot(), "agents.json") }
func cacheZipPath(name, version string) string {
	return filepath.Join(cacheDir(), name+"-"+version+".zip")
}

// ~/.local/bin
func localBinDir() string {
	return filepath.Join(homeDir(), ".local", "bin")
}

func localBinPath(name string) string {
	return filepath.Join(localBinDir(), name)
}

// expandHome turns "~/foo" into "/home/<user>/foo".
func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		return filepath.Join(homeDir(), p[2:])
	}
	return p
}

// ensureDir mkdir -p.
func ensureDir(p string) error {
	return os.MkdirAll(p, 0o755)
}

// pruneEmptyInstallParents removes now-empty layout dirs (team/<src>, team,
// private) after a skill is removed, walking up but never past skillsRoot.
func pruneEmptyInstallParents(skillPath string) {
	root := skillsRoot()
	p := filepath.Dir(skillPath)
	for p != root && strings.HasPrefix(p, root+string(os.PathSeparator)) {
		entries, err := os.ReadDir(p)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(p); err != nil {
			return
		}
		p = filepath.Dir(p)
	}
}
