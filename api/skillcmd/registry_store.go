package skillcmd

// registry_store.go — filesystem-backed storage for a self-hosted private
// skill registry (`cicy-code skill registry serve`).
//
// Layout under <dir>:
//
//	<dir>/skills/<name>/<version>/
//	    manifest.json   # full Manifest (publish.download_url stored EMPTY;
//	                    # the server rewrites it per-request to its own host)
//	    skill.zip       # the packed asset, served directly by /download
//	    files.json      # optional {skill_md,help_md,tools_md,readme}
//
// There is no separate index/DB: catalog/versions/categories/latest are all
// derived by scanning the directory. Registries are small (per-team), so the
// scan cost is negligible.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// versionEntry mirrors the Worker's VersionEntry shape used by
// GET /v1/skills/:name/versions.
type versionEntry struct {
	Version     string `json:"version"`
	PublishedAt string `json:"published_at"`
	Size        int64  `json:"size"`
	Yanked      bool   `json:"yanked,omitempty"`
}

// regStore is a filesystem-backed registry store rooted at dir.
type regStore struct {
	dir string
}

func newRegStore(dir string) *regStore { return &regStore{dir: dir} }

func (s *regStore) skillsRoot() string             { return filepath.Join(s.dir, "skills") }
func (s *regStore) nameDir(name string) string     { return filepath.Join(s.skillsRoot(), name) }
func (s *regStore) verDir(name, ver string) string { return filepath.Join(s.nameDir(name), ver) }
func (s *regStore) manifestPath(name, ver string) string {
	return filepath.Join(s.verDir(name, ver), "manifest.json")
}
func (s *regStore) zipPath(name, ver string) string {
	return filepath.Join(s.verDir(name, ver), "skill.zip")
}
func (s *regStore) filesPath(name, ver string) string {
	return filepath.Join(s.verDir(name, ver), "files.json")
}

// catalog returns all skill names (sorted).
func (s *regStore) catalog() []string {
	entries, err := os.ReadDir(s.skillsRoot())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// versions returns all version entries for a skill, sorted highest-first.
// Returns nil if the skill does not exist.
func (s *regStore) versions(name string) []versionEntry {
	entries, err := os.ReadDir(s.nameDir(name))
	if err != nil {
		return nil
	}
	var out []versionEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ver := e.Name()
		m, _, err := s.readManifest(name, ver)
		if err != nil {
			continue
		}
		ve := versionEntry{Version: ver, Yanked: m.Yanked}
		if m.Publish != nil {
			ve.PublishedAt = m.Publish.PublishedAt
			ve.Size = m.Publish.Size
		}
		out = append(out, ve)
	}
	sort.Slice(out, func(i, j int) bool {
		return compareSemver(out[i].Version, out[j].Version) > 0
	})
	return out
}

// latest returns the highest non-yanked version, or "" if none.
func (s *regStore) latest(name string) string {
	for _, v := range s.versions(name) { // already sorted highest-first
		if !v.Yanked {
			return v.Version
		}
	}
	return ""
}

// readManifest loads the manifest + companion files.json for a version.
func (s *regStore) readManifest(name, ver string) (*Manifest, map[string]string, error) {
	data, err := os.ReadFile(s.manifestPath(name, ver))
	if err != nil {
		return nil, nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, nil, fmt.Errorf("parse manifest %s@%s: %w", name, ver, err)
	}
	files := map[string]string{}
	if fb, err := os.ReadFile(s.filesPath(name, ver)); err == nil {
		_ = json.Unmarshal(fb, &files)
	}
	return &m, files, nil
}

// writeSkill persists a manifest, its zip asset, and optional doc files.
func (s *regStore) writeSkill(m *Manifest, zipBytes []byte, files map[string]string) error {
	vd := s.verDir(m.Name, m.Version)
	if err := os.MkdirAll(vd, 0o755); err != nil {
		return err
	}
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.manifestPath(m.Name, m.Version), mb, 0o644); err != nil {
		return err
	}
	if zipBytes != nil {
		if err := os.WriteFile(s.zipPath(m.Name, m.Version), zipBytes, 0o644); err != nil {
			return err
		}
	}
	if len(files) > 0 {
		fb, _ := json.MarshalIndent(files, "", "  ")
		if err := os.WriteFile(s.filesPath(m.Name, m.Version), fb, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// yank marks a version yanked (sets manifest.yanked = true on disk).
func (s *regStore) yank(name, ver string) (bool, error) {
	m, files, err := s.readManifest(name, ver)
	if err != nil {
		return false, nil // not found
	}
	m.Yanked = true
	if err := s.writeSkill(m, nil, files); err != nil {
		return false, err
	}
	return true, nil
}

// categories returns category -> sorted skill names, over latest versions.
func (s *regStore) categories() map[string][]string {
	cats := map[string][]string{}
	for _, name := range s.catalog() {
		lv := s.latest(name)
		if lv == "" {
			continue
		}
		m, _, err := s.readManifest(name, lv)
		if err != nil {
			continue
		}
		c := m.Category
		if c == "" {
			c = "other"
		}
		cats[c] = append(cats[c], name)
	}
	for c := range cats {
		sort.Strings(cats[c])
	}
	return cats
}

// ── semver (mirrors workers/skills-registry/src/lib/semver.ts) ───────────────

// isValidSemver reports whether v is MAJOR.MINOR.PATCH[-pre][+build].
func isValidSemver(v string) bool {
	maj, min, pat, _, ok := parseSemver(v)
	return ok && maj >= 0 && min >= 0 && pat >= 0
}

// parseSemver splits v into numeric core + prerelease. Build metadata (+...)
// is ignored, matching the Worker.
func parseSemver(v string) (maj, min, pat int, pre string, ok bool) {
	core := v
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core = core[:i]
	}
	if i := strings.IndexByte(core, '-'); i >= 0 {
		pre = core[i+1:]
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return 0, 0, 0, "", false
	}
	var err error
	if maj, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, 0, "", false
	}
	if min, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, 0, "", false
	}
	if pat, err = strconv.Atoi(parts[2]); err != nil {
		return 0, 0, 0, "", false
	}
	return maj, min, pat, pre, true
}

// compareSemver returns >0 if a>b, <0 if a<b, 0 if equal. Invalid versions
// fall back to plain string comparison (matching the Worker's localeCompare).
func compareSemver(a, b string) int {
	am, an, ap, apre, aok := parseSemver(a)
	bm, bn, bp, bpre, bok := parseSemver(b)
	if !aok || !bok {
		return strings.Compare(a, b)
	}
	if am != bm {
		return am - bm
	}
	if an != bn {
		return an - bn
	}
	if ap != bp {
		return ap - bp
	}
	// release (no prerelease) > prerelease
	if apre == "" && bpre != "" {
		return 1
	}
	if apre != "" && bpre == "" {
		return -1
	}
	return strings.Compare(apre, bpre)
}
