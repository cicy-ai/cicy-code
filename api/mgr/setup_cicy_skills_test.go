package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCicySkillsBundleName(t *testing.T) {
	got, err := cicySkillsBundleName("linux", "amd64", "v0.1.0")
	if err != nil {
		t.Fatalf("cicySkillsBundleName error = %v", err)
	}
	if got != "cicy-skills_v0.1.0_linux_amd64.tar.gz" {
		t.Fatalf("cicySkillsBundleName = %q", got)
	}
}

func TestCicySkillsURLsUseProxy(t *testing.T) {
	t.Setenv("GITHUB_PROXY", "https://mirror.example.com")

	bundleURL, err := cicySkillsBundleURL("v0.1.0")
	if err != nil {
		t.Fatalf("cicySkillsBundleURL error = %v", err)
	}
	if !strings.HasPrefix(bundleURL, "https://mirror.example.com/https://github.com/cicy-ai/cicy-skills/releases/download/v0.1.0/") {
		t.Fatalf("unexpected bundle URL: %s", bundleURL)
	}

	sourceURL := cicySkillsSourceURL("v0.1.0")
	if sourceURL != "https://mirror.example.com/https://github.com/cicy-ai/cicy-skills/archive/refs/tags/v0.1.0.tar.gz" {
		t.Fatalf("unexpected source URL: %s", sourceURL)
	}
}

func TestNeedsCicySkillsInstall(t *testing.T) {
	home := t.TempDir()
	skillsRoot := filepath.Join(t.TempDir(), "skills")
	oldSkillsDir := cicySkillsDir
	cicySkillsDir = skillsRoot
	t.Cleanup(func() {
		cicySkillsDir = oldSkillsDir
	})
	t.Setenv("HOME", home)

	if !needsCicySkillsInstall() {
		t.Fatal("needsCicySkillsInstall should require install when files are missing")
	}

	required := []string{
		filepath.Join(skillsRoot, "cicy-skills", "go.mod"),
		filepath.Join(skillsRoot, "cicy-skills", "providers", "google-node", "package.json"),
		filepath.Join(skillsRoot, "cicy-skills", "dist", "cicy-skills"),
		filepath.Join(home, ".local", "bin", "cicy-skills"),
		filepath.Join(home, ".local", "bin", "agent-code-server"),
		filepath.Join(home, ".local", "bin", "agent-webpage"),
		filepath.Join(home, ".codex", "skills", "agent-code-server", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "agent-code-server", "SKILL.md"),
		filepath.Join(home, ".opencode", "skills", "agent-code-server", "SKILL.md"),
	}
	for _, path := range required {
		if err := ensureRuntimeDir(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("ensureRuntimeDir(%s) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte("ok\n"), 0644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	if needsCicySkillsInstall() {
		t.Fatal("needsCicySkillsInstall should be false when required files exist")
	}

	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "webpage"), []byte("legacy\n"), 0644); err != nil {
		t.Fatalf("WriteFile(legacy webpage) error = %v", err)
	}
	if !needsCicySkillsInstall() {
		t.Fatal("needsCicySkillsInstall should be true when legacy webpage alias still exists")
	}
}

func TestCicySkillsInstallScriptIncludesReleaseAndSkillSync(t *testing.T) {
	oldSkillsDir := cicySkillsDir
	cicySkillsDir = filepath.Join(t.TempDir(), "skills")
	t.Cleanup(func() {
		cicySkillsDir = oldSkillsDir
	})
	t.Setenv("GITHUB_PROXY", "https://mirror.example.com/")
	t.Setenv("CN_MIRROR", "1")

	script, err := cicySkillsInstallScript("v0.1.0")
	if err != nil {
		t.Fatalf("cicySkillsInstallScript error = %v", err)
	}
	for _, part := range []string{
		"https://mirror.example.com/https://github.com/cicy-ai/cicy-skills/archive/refs/tags/v0.1.0.tar.gz",
		"https://mirror.example.com/https://github.com/cicy-ai/cicy-skills/releases/download/v0.1.0/",
		"project_root=",
		"local_project_root=",
		"if [ -f \"$local_project_root/go.mod\" ]; then",
		"rm -rf \"$project_root\"",
		"/cicy-skills/dist",
		"export NPM_CONFIG_REGISTRY=\"https://registry.npmmirror.com\"",
		"install all",
		"agent sync opencode",
	} {
		if !strings.Contains(script, part) {
			t.Fatalf("install script missing %q:\n%s", part, script)
		}
	}
}
