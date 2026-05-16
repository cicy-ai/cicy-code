package agentgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexInstallListRemove(t *testing.T) {
	targetRoot := t.TempDir()

	installed, err := Install("", "codex", targetRoot, []string{"google"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(installed) != 1 || installed[0] != "google" {
		t.Fatalf("Install() installed = %#v", installed)
	}

	skillPath := filepath.Join(targetRoot, "google", "SKILL.md")
	if _, err := os.ReadFile(skillPath); err != nil {
		t.Fatalf("ReadFile(%s) error = %v", skillPath, err)
	}

	listed, err := List("codex", targetRoot)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	foundInstalledGoogle := false
	for _, item := range listed {
		if item.Name == "google" && item.Status == "installed" {
			foundInstalledGoogle = true
			break
		}
	}
	if !foundInstalledGoogle {
		t.Fatalf("List() = %#v", listed)
	}

	removed, err := Remove("codex", targetRoot, []string{"google"})
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if len(removed) != 1 || removed[0] != "google" {
		t.Fatalf("Remove() removed = %#v", removed)
	}
	if _, err := os.Stat(filepath.Join(targetRoot, "google")); !os.IsNotExist(err) {
		t.Fatalf("google skill directory still exists: err=%v", err)
	}
}

func TestProfileDefaultTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	installed, err := Install("", "claude", "", []string{"agent-code-server"})
	if err != nil {
		t.Fatalf("Install(claude) error = %v", err)
	}
	if len(installed) != 1 || installed[0] != "agent-code-server" {
		t.Fatalf("Install(claude) installed = %#v", installed)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "agent-code-server", "SKILL.md")); err != nil {
		t.Fatalf("claude skill missing in ~/.claude/skills: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "agent-code-server", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("claude install should not write ~/.codex/skills, err=%v", err)
	}

	installed, err = Install("", "opencode", "", []string{"agent-webpage"})
	if err != nil {
		t.Fatalf("Install(opencode) error = %v", err)
	}
	if len(installed) != 1 || installed[0] != "agent-webpage" {
		t.Fatalf("Install(opencode) installed = %#v", installed)
	}
	if _, err := os.Stat(filepath.Join(home, ".opencode", "skills", "agent-webpage", "SKILL.md")); err != nil {
		t.Fatalf("opencode skill missing in ~/.opencode/skills: %v", err)
	}
}

func TestCodexInstallCFTunnel(t *testing.T) {
	targetRoot := t.TempDir()

	installed, err := Install("", "codex", targetRoot, []string{"cf-tunnel"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(installed) != 1 || installed[0] != "cf-tunnel" {
		t.Fatalf("Install() installed = %#v", installed)
	}

	skillPath := filepath.Join(targetRoot, "cf-tunnel", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", skillPath, err)
	}
	text := string(data)
	if !strings.Contains(text, "`cf-tunnel`") {
		t.Fatalf("SKILL.md missing cf-tunnel command reference: %s", text)
	}
	helpPath := filepath.Join(targetRoot, "cf-tunnel", "references", "help.md")
	helpData, err := os.ReadFile(helpPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", helpPath, err)
	}
	if !strings.Contains(string(helpData), "cf-tunnel add 8101") {
		t.Fatalf("help.md missing quick start example: %s", string(helpData))
	}
	toolsPath := filepath.Join(targetRoot, "cf-tunnel", "references", "tools.md")
	toolsData, err := os.ReadFile(toolsPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", toolsPath, err)
	}
	if !strings.Contains(string(toolsData), "cf-tunnel list") {
		t.Fatalf("tools.md missing list example: %s", string(toolsData))
	}
}

func TestCodexInstallCPing(t *testing.T) {
	targetRoot := t.TempDir()

	installed, err := Install("", "codex", targetRoot, []string{"cping"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(installed) != 1 || installed[0] != "cping" {
		t.Fatalf("Install() installed = %#v", installed)
	}

	skillPath := filepath.Join(targetRoot, "cping", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", skillPath, err)
	}
	text := string(data)
	if !strings.Contains(text, "`cping`") {
		t.Fatalf("SKILL.md missing cping command reference: %s", text)
	}

	helpPath := filepath.Join(targetRoot, "cping", "references", "help.md")
	helpData, err := os.ReadFile(helpPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", helpPath, err)
	}
	if !strings.Contains(string(helpData), "cping tn.cicy-ai.com") {
		t.Fatalf("help.md missing cping example: %s", string(helpData))
	}

	toolsPath := filepath.Join(targetRoot, "cping", "references", "tools.md")
	toolsData, err := os.ReadFile(toolsPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", toolsPath, err)
	}
	if !strings.Contains(string(toolsData), "`cping baidu.com`") {
		t.Fatalf("tools.md missing cping usage: %s", string(toolsData))
	}
}

func TestCodexInstallGlobalAPIToken(t *testing.T) {
	targetRoot := t.TempDir()

	installed, err := Install("", "codex", targetRoot, []string{"globalApiToken"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(installed) != 1 || installed[0] != "globalApiToken" {
		t.Fatalf("Install() installed = %#v", installed)
	}

	skillPath := filepath.Join(targetRoot, "globalApiToken", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", skillPath, err)
	}
	text := string(data)
	if !strings.Contains(text, "`globalApiToken`") {
		t.Fatalf("SKILL.md missing command reference: %s", text)
	}
	helpPath := filepath.Join(targetRoot, "globalApiToken", "references", "help.md")
	helpData, err := os.ReadFile(helpPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", helpPath, err)
	}
	if !strings.Contains(string(helpData), "globalApiToken refresh") {
		t.Fatalf("help.md missing refresh example: %s", string(helpData))
	}
}

func TestCodexInstallAgentWebpage(t *testing.T) {
	targetRoot := t.TempDir()

	installed, err := Install("", "codex", targetRoot, []string{"agent-webpage"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(installed) != 1 || installed[0] != "agent-webpage" {
		t.Fatalf("Install() installed = %#v", installed)
	}

	skillPath := filepath.Join(targetRoot, "agent-webpage", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", skillPath, err)
	}
	text := string(data)
	if !strings.Contains(text, "`agent-webpage`") {
		t.Fatalf("SKILL.md missing command reference: %s", text)
	}
	for _, banned := range []string{"Legacy aliases still exist", "`webpage`", "`webpage-ping`", "`agent-page-ping`"} {
		if strings.Contains(text, banned) {
			t.Fatalf("SKILL.md should not contain %q: %s", banned, text)
		}
	}
	helpPath := filepath.Join(targetRoot, "agent-webpage", "references", "help.md")
	helpData, err := os.ReadFile(helpPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", helpPath, err)
	}
	if !strings.Contains(string(helpData), "agent-webpage ping") {
		t.Fatalf("help.md missing ping example: %s", string(helpData))
	}
	toolsPath := filepath.Join(targetRoot, "agent-webpage", "references", "tools.md")
	toolsData, err := os.ReadFile(toolsPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", toolsPath, err)
	}
	if !strings.Contains(string(toolsData), "agent-webpage exec-js") {
		t.Fatalf("tools.md missing exec-js command: %s", string(toolsData))
	}
	for _, banned := range []string{"## Aliases", "`webpage ...`", "`webpage-ping [client_id]`"} {
		if strings.Contains(string(toolsData), banned) {
			t.Fatalf("tools.md should not contain %q: %s", banned, string(toolsData))
		}
	}
}

func TestCodexInstallCicyAgent(t *testing.T) {
	targetRoot := t.TempDir()

	installed, err := Install("", "codex", targetRoot, []string{"cicy-agent"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(installed) != 1 || installed[0] != "cicy-agent" {
		t.Fatalf("Install() installed = %#v", installed)
	}

	skillPath := filepath.Join(targetRoot, "cicy-agent", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", skillPath, err)
	}
	text := string(data)
	if !strings.Contains(text, "`cicy-agent`") {
		t.Fatalf("SKILL.md missing cicy-agent command reference: %s", text)
	}

	helpPath := filepath.Join(targetRoot, "cicy-agent", "references", "help.md")
	helpData, err := os.ReadFile(helpPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", helpPath, err)
	}
	if !strings.Contains(string(helpData), "cicy-agent ls") {
		t.Fatalf("help.md missing cicy-agent ls example: %s", string(helpData))
	}

	toolsPath := filepath.Join(targetRoot, "cicy-agent", "references", "tools.md")
	toolsData, err := os.ReadFile(toolsPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", toolsPath, err)
	}
	if !strings.Contains(string(toolsData), "cicy-agent --node dev") {
		t.Fatalf("tools.md missing node-aware example: %s", string(toolsData))
	}
}

func TestCodexInstallSSH(t *testing.T) {
	targetRoot := t.TempDir()

	installed, err := Install("", "codex", targetRoot, []string{"cicy-ssh"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(installed) != 1 || installed[0] != "cicy-ssh" {
		t.Fatalf("Install() installed = %#v", installed)
	}

	skillPath := filepath.Join(targetRoot, "cicy-ssh", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", skillPath, err)
	}
	text := string(data)
	if !strings.Contains(text, "`~/.ssh/config`") {
		t.Fatalf("SKILL.md missing ssh config reference: %s", text)
	}
	if !strings.Contains(text, "`ssh <host>`") {
		t.Fatalf("SKILL.md missing ssh command reference: %s", text)
	}

	helpPath := filepath.Join(targetRoot, "cicy-ssh", "references", "help.md")
	helpData, err := os.ReadFile(helpPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", helpPath, err)
	}
	if !strings.Contains(string(helpData), "Host my-node") {
		t.Fatalf("help.md missing host block example: %s", string(helpData))
	}

	toolsPath := filepath.Join(targetRoot, "cicy-ssh", "references", "tools.md")
	toolsData, err := os.ReadFile(toolsPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", toolsPath, err)
	}
	if !strings.Contains(string(toolsData), "ssh <alias>") {
		t.Fatalf("tools.md missing ssh alias example: %s", string(toolsData))
	}
}

func TestCodexListShowsExternalDirs(t *testing.T) {
	targetRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(targetRoot, "manual-skill"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(targetRoot, ".system"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	listed, err := List("codex", targetRoot)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	foundGoogle := false
	foundCFTunnel := false
	foundCPing := false
	foundGlobalAPIToken := false
	foundAgentWebpage := false
	foundCicySSH := false
	foundCicyAgent := false
	foundExternal := false
	for _, item := range listed {
		if item.Name == "agent-webpage" && item.Status == "missing" {
			foundAgentWebpage = true
		}
		if item.Name == "cf-tunnel" && item.Status == "missing" {
			foundCFTunnel = true
		}
		if item.Name == "cping" && item.Status == "missing" {
			foundCPing = true
		}
		if item.Name == "globalApiToken" && item.Status == "missing" {
			foundGlobalAPIToken = true
		}
		if item.Name == "google" && item.Status == "missing" {
			foundGoogle = true
		}
		if item.Name == "cicy-agent" && item.Status == "missing" {
			foundCicyAgent = true
		}
		if item.Name == "cicy-ssh" && item.Status == "missing" {
			foundCicySSH = true
		}
		if item.Name == "manual-skill" && item.Status == "external" {
			foundExternal = true
		}
	}
	if !foundGoogle {
		t.Fatalf("List() missing google status: %#v", listed)
	}
	if !foundAgentWebpage {
		t.Fatalf("List() missing agent-webpage status: %#v", listed)
	}
	if !foundCFTunnel {
		t.Fatalf("List() missing cf-tunnel status: %#v", listed)
	}
	if !foundCPing {
		t.Fatalf("List() missing cping status: %#v", listed)
	}
	if !foundGlobalAPIToken {
		t.Fatalf("List() missing globalApiToken status: %#v", listed)
	}
	if !foundCicyAgent {
		t.Fatalf("List() missing cicy-agent status: %#v", listed)
	}
	if !foundCicySSH {
		t.Fatalf("List() missing cicy-ssh status: %#v", listed)
	}
	if !foundExternal {
		t.Fatalf("List() missing external status: %#v", listed)
	}
}

func TestCodexRejectsUnapprovedSkills(t *testing.T) {
	_, err := Install("", "codex", t.TempDir(), []string{"not-approved"})
	if err == nil {
		t.Fatal("Install() expected error for unapproved skill")
	}
	if !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCodexSyncPreservesExternalDirs(t *testing.T) {
	targetRoot := t.TempDir()
	externalDir := filepath.Join(targetRoot, "manual-external")
	externalFile := filepath.Join(externalDir, "SKILL.md")
	if err := os.MkdirAll(externalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(externalFile, []byte("external skill"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	synced, err := Sync("", "codex", targetRoot)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(synced) != len(ApprovedCodexSkills()) {
		t.Fatalf("Sync() synced = %#v", synced)
	}
	if _, err := os.Stat(externalFile); err != nil {
		t.Fatalf("external skill was touched: %v", err)
	}
}

func TestCodexSyncRemovesLegacyTMSkill(t *testing.T) {
	targetRoot := t.TempDir()
	legacyDir := filepath.Join(targetRoot, "tm")
	legacyFile := filepath.Join(legacyDir, "SKILL.md")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(legacyFile, []byte("legacy tm skill"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Sync("", "codex", targetRoot); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("legacy tm skill directory still exists: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(targetRoot, "cicy-agent", "SKILL.md")); err != nil {
		t.Fatalf("cicy-agent skill missing after sync: %v", err)
	}
}

func TestCodexHelpReadsGeneratedHelp(t *testing.T) {
	targetRoot := t.TempDir()
	if _, err := Install("", "codex", targetRoot, []string{"google"}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	help, err := Help("codex", targetRoot, "google")
	if err != nil {
		t.Fatalf("Help() error = %v", err)
	}
	if help.Name != "google" {
		t.Fatalf("Help() name = %q", help.Name)
	}
	if !strings.Contains(help.Path, "google/references/help.md") {
		t.Fatalf("Help() path = %q", help.Path)
	}
	if !strings.Contains(help.Text, "# Google Help") {
		t.Fatalf("Help() text = %q", help.Text)
	}
}

func TestCodexHelpReadsGeneratedCicyAgentHelp(t *testing.T) {
	targetRoot := t.TempDir()
	if _, err := Install("", "codex", targetRoot, []string{"cicy-agent"}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	help, err := Help("codex", targetRoot, "cicy-agent")
	if err != nil {
		t.Fatalf("Help() error = %v", err)
	}
	if help.Name != "cicy-agent" {
		t.Fatalf("Help() name = %q", help.Name)
	}
	if !strings.Contains(help.Path, "cicy-agent/references/help.md") {
		t.Fatalf("Help() path = %q", help.Path)
	}
	if !strings.Contains(help.Text, "# cicy-agent Help") {
		t.Fatalf("Help() text = %q", help.Text)
	}
}

func TestCodexHelpReadsGeneratedCPingHelp(t *testing.T) {
	targetRoot := t.TempDir()
	if _, err := Install("", "codex", targetRoot, []string{"cping"}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	help, err := Help("codex", targetRoot, "cping")
	if err != nil {
		t.Fatalf("Help() error = %v", err)
	}
	if help.Name != "cping" {
		t.Fatalf("Help() name = %q", help.Name)
	}
	if !strings.Contains(help.Path, "cping/references/help.md") {
		t.Fatalf("Help() path = %q", help.Path)
	}
	if !strings.Contains(help.Text, "# cping Help") {
		t.Fatalf("Help() text = %q", help.Text)
	}
}

func TestCodexHelpReadsGeneratedSSHHelp(t *testing.T) {
	targetRoot := t.TempDir()
	if _, err := Install("", "codex", targetRoot, []string{"cicy-ssh"}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	help, err := Help("codex", targetRoot, "cicy-ssh")
	if err != nil {
		t.Fatalf("Help() error = %v", err)
	}
	if help.Name != "cicy-ssh" {
		t.Fatalf("Help() name = %q", help.Name)
	}
	if !strings.Contains(help.Path, "cicy-ssh/references/help.md") {
		t.Fatalf("Help() path = %q", help.Path)
	}
	if !strings.Contains(help.Text, "# cicy-ssh Help") {
		t.Fatalf("Help() text = %q", help.Text)
	}
}

func TestCodexToolsReadsGeneratedCicyAgentTools(t *testing.T) {
	targetRoot := t.TempDir()
	if _, err := Install("", "codex", targetRoot, []string{"cicy-agent"}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	tools, err := Tools("codex", targetRoot, "cicy-agent")
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if tools.Name != "cicy-agent" {
		t.Fatalf("Tools() name = %q", tools.Name)
	}
	if !strings.Contains(tools.Path, "cicy-agent/references/tools.md") {
		t.Fatalf("Tools() path = %q", tools.Path)
	}
	if !strings.Contains(tools.Text, "cicy-agent Commands") {
		t.Fatalf("Tools() text = %q", tools.Text)
	}
}

func TestCodexToolsReadsGeneratedCPingTools(t *testing.T) {
	targetRoot := t.TempDir()
	if _, err := Install("", "codex", targetRoot, []string{"cping"}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	tools, err := Tools("codex", targetRoot, "cping")
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if tools.Name != "cping" {
		t.Fatalf("Tools() name = %q", tools.Name)
	}
	if !strings.Contains(tools.Path, "cping/references/tools.md") {
		t.Fatalf("Tools() path = %q", tools.Path)
	}
	if !strings.Contains(tools.Text, "cping Commands") {
		t.Fatalf("Tools() text = %q", tools.Text)
	}
}

func TestCodexToolsReadsGeneratedSSHTools(t *testing.T) {
	targetRoot := t.TempDir()
	if _, err := Install("", "codex", targetRoot, []string{"cicy-ssh"}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	tools, err := Tools("codex", targetRoot, "cicy-ssh")
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if tools.Name != "cicy-ssh" {
		t.Fatalf("Tools() name = %q", tools.Name)
	}
	if !strings.Contains(tools.Path, "cicy-ssh/references/tools.md") {
		t.Fatalf("Tools() path = %q", tools.Path)
	}
	if !strings.Contains(tools.Text, "cicy-ssh Commands") {
		t.Fatalf("Tools() text = %q", tools.Text)
	}
}

func TestCodexToolsReadsGeneratedTools(t *testing.T) {
	targetRoot := t.TempDir()
	if _, err := Install("", "codex", targetRoot, []string{"agent-webpage"}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	tools, err := Tools("codex", targetRoot, "agent-webpage")
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if tools.Name != "agent-webpage" {
		t.Fatalf("Tools() name = %q", tools.Name)
	}
	if !strings.Contains(tools.Path, "agent-webpage/references/tools.md") {
		t.Fatalf("Tools() path = %q", tools.Path)
	}
	for _, want := range []string{"agent-webpage ping", "current-active-agent-id", "current-master-agent-id"} {
		if !strings.Contains(tools.Text, want) {
			t.Fatalf("Tools() text missing %q: %q", want, tools.Text)
		}
	}
}

func TestCodexInstallFRPServer(t *testing.T) {
	targetRoot := t.TempDir()

	installed, err := Install("", "codex", targetRoot, []string{"frp-server"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(installed) != 1 || installed[0] != "frp-server" {
		t.Fatalf("Install() installed = %#v", installed)
	}

	skillPath := filepath.Join(targetRoot, "frp-server", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", skillPath, err)
	}
	text := string(data)
	if !strings.Contains(text, "`frp-server`") {
		t.Fatalf("SKILL.md missing frp-server command reference: %s", text)
	}

	helpPath := filepath.Join(targetRoot, "frp-server", "references", "help.md")
	helpData, err := os.ReadFile(helpPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", helpPath, err)
	}
	if !strings.Contains(string(helpData), "frp-server reload") {
		t.Fatalf("help.md missing reload example: %s", string(helpData))
	}
	if !strings.Contains(string(helpData), "bindPort = 9500") {
		t.Fatalf("help.md missing bindPort planning note: %s", string(helpData))
	}
	if !strings.Contains(string(helpData), "https://install.cicy-ai.com/frp") {
		t.Fatalf("help.md missing shell installer reference: %s", string(helpData))
	}
	if !strings.Contains(string(helpData), "$u='https://install.cicy-ai.com/frp'") {
		t.Fatalf("help.md missing PowerShell installer reference: %s", string(helpData))
	}
	if !strings.Contains(string(helpData), "self-elevate") {
		t.Fatalf("help.md missing Windows file-based explanation: %s", string(helpData))
	}
	if !strings.Contains(string(helpData), "Linux Docker client tested successfully on `9511`") {
		t.Fatalf("help.md missing verified multi-client note: %s", string(helpData))
	}

	toolsPath := filepath.Join(targetRoot, "frp-server", "references", "tools.md")
	toolsData, err := os.ReadFile(toolsPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", toolsPath, err)
	}
	if !strings.Contains(string(toolsData), "frp-server connections") {
		t.Fatalf("tools.md missing connections example: %s", string(toolsData))
	}
	if !strings.Contains(string(toolsData), "frp-server clients") {
		t.Fatalf("tools.md missing clients example: %s", string(toolsData))
	}
}

func TestCodexInstallFRPClient(t *testing.T) {
	targetRoot := t.TempDir()

	installed, err := Install("", "codex", targetRoot, []string{"frp-client"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(installed) != 1 || installed[0] != "frp-client" {
		t.Fatalf("Install() installed = %#v", installed)
	}

	skillPath := filepath.Join(targetRoot, "frp-client", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", skillPath, err)
	}
	text := string(data)
	if !strings.Contains(text, "`frp-client`") {
		t.Fatalf("SKILL.md missing frp-client command reference: %s", text)
	}

	helpPath := filepath.Join(targetRoot, "frp-client", "references", "help.md")
	helpData, err := os.ReadFile(helpPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", helpPath, err)
	}
	if !strings.Contains(string(helpData), "ssh ton-mac '~/.local/bin/frpc status -c ~/.config/frp/frpc.toml'") {
		t.Fatalf("help.md missing remote ssh management example: %s", string(helpData))
	}

	toolsPath := filepath.Join(targetRoot, "frp-client", "references", "tools.md")
	toolsData, err := os.ReadFile(toolsPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", toolsPath, err)
	}
	if !strings.Contains(string(toolsData), "frp-client reload") {
		t.Fatalf("tools.md missing reload example: %s", string(toolsData))
	}
}
