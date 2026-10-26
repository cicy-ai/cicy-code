package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLocalSkill(t *testing.T, dir, manifest string) {
	t.Helper()
	name := filepath.Base(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: English desc\n---\n"), 0o644)
	if manifest != "" {
		os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644)
	}
}

const localI18nManifest = `{"name":"dsh-fleet","version":"0.4.1","title":"DSH Fleet","description":"English desc","category":"dev",
 "i18n":{"zh-CN":{"title":"DSH 机队","description":"中文简介"}}}`

func TestUserSkillsUseManifestI18n(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CICY_SKILLS_ROOT", root)
	writeLocalSkill(t, filepath.Join(root, "dsh-fleet"), localI18nManifest)
	writeLocalSkill(t, filepath.Join(root, "bare-skill"), "")

	byName := func(lang string) map[string]marketSkill {
		out := map[string]marketSkill{}
		for _, s := range scanUserSkills(nil, lang) {
			out[s.Name] = s
		}
		return out
	}
	zh := byName("zh-CN")["dsh-fleet"]
	if zh.Title != "DSH 机队" || zh.Description != "中文简介" {
		t.Fatalf("zh-CN: got %q / %q", zh.Title, zh.Description)
	}
	if zh := byName("zh")["dsh-fleet"]; zh.Title != "DSH 机队" {
		t.Fatalf("base-language zh: got %q", zh.Title)
	}
	en := byName("en-US")["dsh-fleet"]
	if en.Title != "DSH Fleet" || en.Description != "English desc" {
		t.Fatalf("en: got %q / %q", en.Title, en.Description)
	}
	// No manifest.json → SKILL.md frontmatter description, titleized name.
	if bare := byName("zh-CN")["bare-skill"]; bare.Title != "Bare Skill" || bare.Description != "English desc" || bare.Source != "user" {
		t.Fatalf("bare: %+v", bare)
	}
}

func TestPrivateMarketSkillLocalized(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CICY_SKILLS_ROOT", root)
	writeLocalSkill(t, filepath.Join(root, "private", "dsh-fleet"), localI18nManifest)

	if s := privateMarketSkill("dsh-fleet", "zh-CN"); s == nil || s.Title != "DSH 机队" || s.Description != "中文简介" || s.Source != "private" {
		t.Fatalf("zh-CN: %+v", s)
	}
	if s := privateMarketSkill("dsh-fleet", ""); s == nil || s.Title != "DSH Fleet" {
		t.Fatalf("default: %+v", s)
	}
	if privateMarketSkill("missing", "zh-CN") != nil {
		t.Fatal("missing skill should be nil")
	}
}
