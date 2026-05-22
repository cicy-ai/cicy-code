package skillcmd

import (
	"os"
	"path/filepath"
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

func skillDir(name string) string         { return filepath.Join(skillsRoot(), name) }
func cacheDir() string                    { return filepath.Join(skillsRoot(), ".cache") }
func installedJSONPath() string           { return filepath.Join(skillsRoot(), "installed.json") }
func agentsJSONPath() string              { return filepath.Join(skillsRoot(), "agents.json") }
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
