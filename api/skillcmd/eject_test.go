package skillcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPublicEject verifies the registry → local source transition: files stay
// in place, installed.json source.type flips to "local" with path set, and a
// second eject on an already-local entry refuses.
func TestPublicEject(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CICY_SKILLS_ROOT", root)

	// Seed: a registry-source skill ("aliyun-cli" with source.type="url") and
	// its on-disk dir + SKILL.md.
	name := "aliyun-cli"
	skillDirPath := filepath.Join(root, name)
	if err := os.MkdirAll(skillDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDirPath, "SKILL.md"), []byte("---\nname: aliyun-cli\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &InstalledConfig{
		SchemaVersion: 1,
		Skills: []InstalledSkill{{
			Name:        name,
			Version:     "1.0.3",
			InstalledAt: time.Now().UTC(),
			Source:      InstalledSource{Type: "url", DownloadURL: "https://example.test/aliyun-cli-1.0.3.zip"},
		}},
	}
	if err := writeInstalled(cfg); err != nil {
		t.Fatal(err)
	}

	// 1. Eject — succeeds.
	var buf bytes.Buffer
	got, err := PublicEject(name, &buf)
	if err != nil {
		t.Fatalf("PublicEject: %v", err)
	}
	if got.Source.Type != "local" {
		t.Errorf("returned source.type = %q, want \"local\"", got.Source.Type)
	}
	if got.Source.Path == "" || got.Source.Path != skillDirPath {
		t.Errorf("returned source.path = %q, want %q", got.Source.Path, skillDirPath)
	}
	if got.Version != "1.0.3" {
		t.Errorf("version drifted: got %q, want \"1.0.3\"", got.Version)
	}

	// 2. installed.json on disk reflects the change.
	data, err := os.ReadFile(filepath.Join(root, "installed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk InstalledConfig
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatal(err)
	}
	if len(onDisk.Skills) != 1 || onDisk.Skills[0].Source.Type != "local" {
		t.Errorf("installed.json not updated: %s", string(data))
	}
	// Files preserved.
	if _, err := os.Stat(filepath.Join(skillDirPath, "SKILL.md")); err != nil {
		t.Errorf("SKILL.md was disturbed: %v", err)
	}

	// 3. Second eject on now-local entry refuses.
	if _, err := PublicEject(name, &buf); err == nil {
		t.Errorf("double-eject succeeded; expected refusal")
	}

	// 4. Eject of a non-installed name refuses.
	if _, err := PublicEject("ghost-skill", &buf); err == nil {
		t.Errorf("eject of missing skill succeeded; expected refusal")
	}
}
