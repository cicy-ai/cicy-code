package main

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func expectedPackageManager() string {
	if value := strings.TrimSpace(os.Getenv("CICY_TEST_PACKAGE_MANAGER")); value != "" {
		return value
	}
	if runtime.GOOS == "darwin" {
		return "brew"
	}
	return "apt"
}

func TestPackageInstallCmdPlatformBranches(t *testing.T) {
	cmd := packageInstallCmd("git")
	switch expectedPackageManager() {
	case "brew":
		if cmd != "brew install git" {
			t.Fatalf("packageInstallCmd git = %q", cmd)
		}
	case "apt":
		if !strings.Contains(cmd, "apt-get update") || !strings.Contains(cmd, "apt-get install -y git") {
			t.Fatalf("packageInstallCmd git = %q", cmd)
		}
	default:
		if strings.Contains(cmd, "apt-get") {
			t.Fatalf("packageInstallCmd still hardcodes apt on %s: %q", expectedPackageManager(), cmd)
		}
	}
}

func TestOpenSSHInstallCmdPlatformBranches(t *testing.T) {
	cmd := opensshInstallCmd()
	switch expectedPackageManager() {
	case "brew":
		if cmd != "brew install openssh" {
			t.Fatalf("opensshInstallCmd = %q", cmd)
		}
	case "apt":
		if !strings.Contains(cmd, "apt-get update") || !strings.Contains(cmd, "apt-get install -y openssh-client") {
			t.Fatalf("opensshInstallCmd = %q", cmd)
		}
	default:
		if strings.Contains(cmd, "apt-get") || strings.Contains(cmd, "openssh-client") {
			t.Fatalf("opensshInstallCmd still hardcodes Debian path on %s: %q", expectedPackageManager(), cmd)
		}
	}
}

func TestNodeInstallCmdPlatformBranches(t *testing.T) {
	cmd := nodeInstallCmd()
	switch expectedPackageManager() {
	case "brew":
		if cmd != "brew install node" {
			t.Fatalf("nodeInstallCmd = %q", cmd)
		}
	case "apt":
		if !strings.Contains(cmd, "https://deb.nodesource.com/setup_22.x") || !strings.Contains(cmd, "apt-get install -y nodejs") {
			t.Fatalf("nodeInstallCmd = %q", cmd)
		}
	default:
		if strings.Contains(cmd, "deb.nodesource.com/setup_22.x") || strings.Contains(cmd, "apt-get install -y nodejs") {
			t.Fatalf("nodeInstallCmd still hardcodes Debian path on %s: %q", expectedPackageManager(), cmd)
		}
	}
}

func TestCodeServerInstallCmdPlatformBranches(t *testing.T) {
	t.Setenv("CICY_RUNTIME_KIND", "")
	cmd := codeServerInstallCmd()
	if expectedPackageManager() == "brew" {
		if cmd != "brew install code-server" {
			t.Fatalf("codeServerInstallCmd = %q", cmd)
		}
		return
	}
	if !strings.Contains(cmd, "https://code-server.dev/install.sh") {
		t.Fatalf("codeServerInstallCmd = %q", cmd)
	}
}

func TestCodeServerInstallCmdContainerRuntimeDisabled(t *testing.T) {
	t.Setenv("CICY_RUNTIME_KIND", "container")
	if cmd := codeServerInstallCmd(); cmd != "" {
		t.Fatalf("codeServerInstallCmd in container = %q, want empty", cmd)
	}
}

func TestHermesInstallCmdUsesGitHubChinaProxy(t *testing.T) {
	cmd := hermesInstallCmd()
	if !strings.Contains(cmd, "https://gh-proxy.com/https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh") {
		t.Fatalf("hermesInstallCmd = %q", cmd)
	}
	if !strings.Contains(cmd, "https://gh-proxy.com/https://codeload.github.com/NousResearch/hermes-agent/tar.gz/refs/heads/${branch}") {
		t.Fatalf("hermesInstallCmd should use codeload tarball via gh proxy: %q", cmd)
	}
	if !strings.Contains(cmd, `if cicy_clone_hermes "$BRANCH" "$INSTALL_DIR"; then`) {
		t.Fatalf("hermesInstallCmd should rewrite https clone to tarball flow: %q", cmd)
	}
	if !strings.Contains(cmd, `export UV_INDEX_URL="https://pypi.tuna.tsinghua.edu.cn/simple"`) {
		t.Fatalf("hermesInstallCmd missing uv mirror: %q", cmd)
	}
	if !strings.Contains(cmd, `export PIP_INDEX_URL="https://pypi.tuna.tsinghua.edu.cn/simple"`) {
		t.Fatalf("hermesInstallCmd missing pip mirror: %q", cmd)
	}
}

func TestNormalizeHermesModel(t *testing.T) {
	cases := map[string]string{
		"":             "gpt-5.5",
		"gpt5.5":       "gpt-5.5",
		"gpt5.4":       "gpt-5.4",
		"claude4.7":    "claude-opus-4-7",
		"claude-4.7":   "claude-opus-4-7",
		"cluade4.7":    "claude-opus-4-7",
		"opus[1m]":     "claude-opus-4-7",
		"custom-model": "custom-model",
	}
	for input, want := range cases {
		if got := normalizeHermesModel(input); got != want {
			t.Fatalf("normalizeHermesModel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBaseToolsIncludeRequiredPackages(t *testing.T) {
	tools := baseTools()
	required := map[string]bool{
		"curl":       false,
		"unzip":      false,
		"jq":         false,
		"ssh-keygen": false,
		"tmux":       false,
		"git":        false,
		"node":       false,
	}
	for _, tool := range tools {
		if _, ok := required[tool.Name]; ok {
			required[tool.Name] = tool.Required
		}
	}
	for name, present := range required {
		if !present {
			t.Fatalf("baseTools missing required %s", name)
		}
	}
}
