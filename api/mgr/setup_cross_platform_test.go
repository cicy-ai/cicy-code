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
