package skillcmd

// registry_embed.go — exported helpers so the mgr daemon can host a private
// registry in-process (a goroutine on its own port) instead of shelling out to
// `cicy-code skill registry serve`. The mgr owns the http.Server lifecycle;
// these helpers just build the handler and do filesystem-level operations.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// NewRegistryHandler builds an http.Handler serving a private registry rooted
// at dir. readToken/adminToken may be empty (open / admin-disabled). publicURL
// overrides the download_url origin (usually "" → derive from request host).
func NewRegistryHandler(dir, readToken, adminToken, publicURL string) (http.Handler, error) {
	return NewRegistryHandlerWithPrefix(dir, readToken, adminToken, publicURL, "")
}

// NewRegistryHandlerWithPrefix is like NewRegistryHandler but for handlers
// mounted under a path prefix on a shared mux (e.g. "/registry" on the
// cicy-code daemon). The caller is expected to http.StripPrefix(prefix, ...)
// before delegating; pathPrefix here only ensures download_url is built with
// the prefix so clients reach /download through the same mount.
func NewRegistryHandlerWithPrefix(dir, readToken, adminToken, publicURL, pathPrefix string) (http.Handler, error) {
	store := newRegStore(dir)
	if err := ensureDir(store.skillsRoot()); err != nil {
		return nil, err
	}
	s := newRegServer(store, readToken, adminToken, publicURL)
	s.pathPrefix = strings.TrimRight(pathPrefix, "/")
	return s.handler(), nil
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

// PrivateSkillsDir is ~/cicy-ai/skills/private — where a node's own (authored)
// skills live. The "我的库" tab lists these and lets each be published.
func PrivateSkillsDir() string { return privateSkillsParent() }

// ListPrivateSkills lists name@version for every skill directory under
// PrivateSkillsDir (those with a readable manifest.json).
func ListPrivateSkills() []NameVersion {
	entries, err := os.ReadDir(privateSkillsParent())
	if err != nil {
		return nil
	}
	var out []NameVersion
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(privateSkillsParent(), e.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var m struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &m) != nil || m.Name == "" {
			continue
		}
		out = append(out, NameVersion{Name: m.Name, Version: m.Version})
	}
	return out
}

// UnpublishFromDir removes a skill entirely from the registry data dir (the
// "share" toggle going off). Unlike yank, it deletes the stored versions so a
// later re-publish starts clean.
func UnpublishFromDir(dataDir, name string) error {
	store := newRegStore(dataDir)
	dir := filepath.Join(store.skillsRoot(), name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("%s not published", name)
	}
	return os.RemoveAll(dir)
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
