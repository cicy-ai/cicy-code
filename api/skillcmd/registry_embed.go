package skillcmd

// registry_embed.go — exported helpers so the mgr daemon can host a private
// registry in-process (a goroutine on its own port) instead of shelling out to
// `cicy-code skill registry serve`. The mgr owns the http.Server lifecycle;
// these helpers just build the handler and do filesystem-level operations.

import (
	"fmt"
	"net/http"
	"path/filepath"
)

// NewRegistryHandler builds an http.Handler serving a private registry rooted
// at dir. readToken/adminToken may be empty (open / admin-disabled). publicURL
// overrides the download_url origin (usually "" → derive from request host).
func NewRegistryHandler(dir, readToken, adminToken, publicURL string) (http.Handler, error) {
	store := newRegStore(dir)
	if err := ensureDir(store.skillsRoot()); err != nil {
		return nil, err
	}
	return newRegServer(store, readToken, adminToken, publicURL).handler(), nil
}

// DefaultLocalRegistryDir is the default data dir for a self-hosted registry.
func DefaultLocalRegistryDir() string { return defaultRegistryDir() }

// NameVersion is a minimal skill identity for status listings.
type NameVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// LocalRegistrySkills lists the latest non-yanked version of each skill stored
// in the registry rooted at dir.
func LocalRegistrySkills(dir string) []NameVersion {
	store := newRegStore(dir)
	var out []NameVersion
	for _, n := range store.catalog() {
		if v := store.latest(n); v != "" {
			out = append(out, NameVersion{Name: n, Version: v})
		}
	}
	return out
}

// PublishToDir packs the skill directory at skillSrc and stores it into the
// registry data dir. Returns the published name@version.
func PublishToDir(dataDir, skillSrc string) (string, string, error) {
	abs, err := filepath.Abs(skillSrc)
	if err != nil {
		return "", "", err
	}
	m, err := publishToStore(newRegStore(dataDir), abs)
	if err != nil {
		return "", "", err
	}
	return m.Name, m.Version, nil
}

// YankFromDir marks a version yanked in the registry data dir.
func YankFromDir(dataDir, name, version string) error {
	ok, err := newRegStore(dataDir).yank(name, version)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s@%s not found", name, version)
	}
	return nil
}
