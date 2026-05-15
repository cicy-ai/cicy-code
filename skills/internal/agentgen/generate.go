package agentgen

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type SkillStatus struct {
	Name   string
	Status string
}

type SkillHelp struct {
	Name string
	Path string
	Text string
}

func profileSkillsDir(dirname string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", dirname, "skills")
	}
	return filepath.Join(home, dirname, "skills")
}

func CodexSkillsDir() string {
	return profileSkillsDir(".codex")
}

func ClaudeSkillsDir() string {
	return profileSkillsDir(".claude")
}

func OpenCodeSkillsDir() string {
	return profileSkillsDir(".opencode")
}

func ApprovedCodexSkills() []string {
	return []string{"agent-code-server", "agent-summary", "agent-webpage", "cf-tunnel", "cping", "docker-build-github-action", "frp-client", "frp-server", "globalApiToken", "google", "cicy-ssh", "cicy-agent", "cicy-mihomo", "us-spot-proxy", "proxy_ssh"}
}

func canonicalCodexSkillName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "agent-code-server", "agentcodeserver", "agent_code_server", "code-server", "codeserver":
		return "agent-code-server"
	case "agent-summary", "agentsummary", "agent_summary":
		return "agent-summary"
	case "agent-webpage", "agentwebpage", "agent_webpage":
		return "agent-webpage"
	case "cf-tunnel":
		return "cf-tunnel"
	case "cping":
		return "cping"
	case "docker-build-github-action", "dockerbuildgithubaction", "docker_build_github_action", "docker-github-action", "dockerhub-build", "docker-build":
		return "docker-build-github-action"
	case "frp-client", "frpclient", "frpc", "frp-client-skill":
		return "frp-client"
	case "frp-server", "frpserver", "frps", "frp-server-skill":
		return "frp-server"
	case "globalapitoken", "global-api-token":
		return "globalApiToken"
	case "google":
		return "google"
	case "ssh", "cicy-ssh", "cicyssh", "cicy_ssh":
		return "cicy-ssh"
	case "cicy-agent", "cicyagent", "cicy_agent":
		return "cicy-agent"
	case "cicy-mihomo", "cicymihomo", "cicy_mihomo", "mihomo":
		return "cicy-mihomo"
	case "us-spot-proxy", "usspotproxy", "us_spot_proxy", "usspp":
		return "us-spot-proxy"
	case "proxy_ssh", "proxy-ssh", "proxyssh", "ssh-socks", "ssh_socks":
		return "proxy_ssh"
	default:
		return ""
	}
}

func Generate(root, profileName, targetRoot string) error {
	_, err := Sync(root, profileName, targetRoot)
	return err
}

func List(profileName, targetRoot string) ([]SkillStatus, error) {
	profileName = normalizeProfile(profileName)
	targetRoot = defaultProfileTarget(profileName, targetRoot)
	switch profileName {
	case "codex", "claude", "opencode":
		return listCodex(targetRoot)
	default:
		return nil, fmt.Errorf("only codex, claude, and opencode skill generation are enabled right now")
	}
}

func Help(profileName, targetRoot, skillName string) (SkillHelp, error) {
	profileName = normalizeProfile(profileName)
	targetRoot = defaultProfileTarget(profileName, targetRoot)
	switch profileName {
	case "codex", "claude", "opencode":
		return helpCodex(targetRoot, skillName)
	default:
		return SkillHelp{}, fmt.Errorf("only codex, claude, and opencode skill generation are enabled right now")
	}
}

func Tools(profileName, targetRoot, skillName string) (SkillHelp, error) {
	profileName = normalizeProfile(profileName)
	targetRoot = defaultProfileTarget(profileName, targetRoot)
	switch profileName {
	case "codex", "claude", "opencode":
		return toolsCodex(targetRoot, skillName)
	default:
		return SkillHelp{}, fmt.Errorf("only codex, claude, and opencode skill generation are enabled right now")
	}
}

func Install(root, profileName, targetRoot string, skillNames []string) ([]string, error) {
	profileName = normalizeProfile(profileName)
	targetRoot = defaultProfileTarget(profileName, targetRoot)
	switch profileName {
	case "codex", "claude", "opencode":
		return installCodex(root, targetRoot, skillNames)
	default:
		return nil, fmt.Errorf("only codex, claude, and opencode skill generation are enabled right now")
	}
}

func Update(root, profileName, targetRoot string, skillNames []string) ([]string, error) {
	return Install(root, profileName, targetRoot, skillNames)
}

func Remove(profileName, targetRoot string, skillNames []string) ([]string, error) {
	profileName = normalizeProfile(profileName)
	targetRoot = defaultProfileTarget(profileName, targetRoot)
	switch profileName {
	case "codex", "claude", "opencode":
		return removeCodex(targetRoot, skillNames)
	default:
		return nil, fmt.Errorf("only codex, claude, and opencode skill generation are enabled right now")
	}
}

func Sync(root, profileName, targetRoot string) ([]string, error) {
	profileName = normalizeProfile(profileName)
	targetRoot = defaultProfileTarget(profileName, targetRoot)
	switch profileName {
	case "codex", "claude", "opencode":
		return installCodex(root, targetRoot, ApprovedCodexSkills())
	default:
		return nil, fmt.Errorf("only codex, claude, and opencode skill generation are enabled right now")
	}
}

func normalizeProfile(profileName string) string {
	return strings.ToLower(strings.TrimSpace(profileName))
}

func defaultProfileTarget(profileName, targetRoot string) string {
	if strings.TrimSpace(targetRoot) != "" {
		return targetRoot
	}
	switch normalizeProfile(profileName) {
	case "codex":
		return CodexSkillsDir()
	case "claude":
		return ClaudeSkillsDir()
	case "opencode":
		return OpenCodeSkillsDir()
	default:
		return targetRoot
	}
}

func listCodex(targetRoot string) ([]SkillStatus, error) {
	approved := ApprovedCodexSkills()
	approvedSet := make(map[string]struct{}, len(approved))
	statuses := make([]SkillStatus, 0, len(approved))
	for _, skill := range approved {
		approvedSet[skill] = struct{}{}
		status := "missing"
		if dirExists(filepath.Join(targetRoot, skill)) {
			status = "installed"
		}
		statuses = append(statuses, SkillStatus{Name: skill, Status: status})
	}

	entries, err := os.ReadDir(targetRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return statuses, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if _, ok := approvedSet[name]; ok {
			continue
		}
		statuses = append(statuses, SkillStatus{Name: name, Status: "external"})
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})
	return statuses, nil
}

func installCodex(root, targetRoot string, skillNames []string) ([]string, error) {
	skills, err := resolveCodexSkills(skillNames)
	if err != nil {
		return nil, err
	}
	installed := make([]string, 0, len(skills))
	for _, skill := range skills {
		if err := generateCodexSkill(root, targetRoot, skill); err != nil {
			return nil, err
		}
		installed = append(installed, skill)
	}
	if containsString(skills, "cicy-agent") {
		if err := os.RemoveAll(filepath.Join(targetRoot, "tm")); err != nil {
			return nil, err
		}
	}
	return installed, nil
}

func removeCodex(targetRoot string, skillNames []string) ([]string, error) {
	skills, err := resolveCodexSkills(skillNames)
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0, len(skills))
	for _, skill := range skills {
		if err := os.RemoveAll(filepath.Join(targetRoot, skill)); err != nil {
			return nil, err
		}
		removed = append(removed, skill)
	}
	return removed, nil
}

func resolveCodexSkills(skillNames []string) ([]string, error) {
	approved := ApprovedCodexSkills()
	approvedSet := make(map[string]struct{}, len(approved))
	for _, skill := range approved {
		approvedSet[strings.ToLower(skill)] = struct{}{}
	}

	normalizedNames := make([]string, 0, len(skillNames))
	for _, name := range skillNames {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if normalized == "" {
			continue
		}
		normalizedNames = append(normalizedNames, normalized)
	}

	seen := map[string]struct{}{}
	var resolved []string
	for _, normalized := range normalizedNames {
		if normalized == "all" {
			if len(normalizedNames) > 1 {
				return nil, fmt.Errorf("all cannot be mixed with explicit skill names")
			}
			return append([]string(nil), approved...), nil
		}
		canonical := canonicalCodexSkillName(normalized)
		if canonical == "" {
			return nil, fmt.Errorf("skill %q is not approved for codex; approved: %s", normalized, strings.Join(approved, ", "))
		}
		if _, ok := approvedSet[strings.ToLower(canonical)]; !ok {
			return nil, fmt.Errorf("skill %q is not approved for codex; approved: %s", normalized, strings.Join(approved, ", "))
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		resolved = append(resolved, canonical)
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("at least one approved skill is required")
	}
	sort.Strings(resolved)
	return resolved, nil
}

func generateCodexSkill(root, targetRoot, skill string) error {
	switch skill {
	case "agent-code-server":
		return generateCodexAgentCodeServer(targetRoot)
	case "agent-summary":
		return generateCodexAgentSummary(targetRoot)
	case "agent-webpage":
		return generateCodexAgentWebpage(targetRoot)
	case "cf-tunnel":
		return generateCodexCFTunnel(targetRoot)
	case "cping":
		return generateCodexCPing(targetRoot)
	case "docker-build-github-action":
		return generateStaticSkill(root, targetRoot, "infra", "docker-build-github-action")
	case "frp-client":
		return generateCodexFRPClient(targetRoot)
	case "frp-server":
		return generateCodexFRPServer(targetRoot)
	case "globalApiToken":
		return generateCodexGlobalAPIToken(targetRoot)
	case "google":
		return generateCodexGoogle(targetRoot)
	case "cicy-ssh":
		return generateCodexSSH(targetRoot)
	case "cicy-agent":
		return generateCodexTM(targetRoot)
	case "cicy-mihomo":
		return generateCodexCicyMihomo(targetRoot)
	case "us-spot-proxy":
		return generateCodexUSSpotProxy(targetRoot)
	case "proxy_ssh":
		return generateCodexProxySSH(targetRoot)
	default:
		return fmt.Errorf("skill %q is not implemented", skill)
	}
}

func generateStaticSkill(root, targetRoot, category, skill string) error {
	if strings.TrimSpace(root) == "" {
		var err error
		root, err = findRepoRoot()
		if err != nil {
			return err
		}
	}
	src := filepath.Join(root, "legacy", "skills", category, skill)
	dst := filepath.Join(targetRoot, skill)
	if !dirExists(src) {
		return fmt.Errorf("static skill source %q does not exist", src)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return copyDir(src, dst)
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if dirExists(filepath.Join(dir, "legacy", "skills")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root containing legacy/skills")
		}
		dir = parent
	}
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := copyFile(srcPath, dstPath, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func generateCodexCFTunnel(targetRoot string) error {
	cfDir := filepath.Join(targetRoot, "cf-tunnel")
	refsDir := filepath.Join(cfDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(cfDir, "SKILL.md"), renderCFTunnelSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderCFTunnelHelp()); err != nil {
		return err
	}
	tools := renderCFTunnelCommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func generateCodexCPing(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "cping")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderCPingSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderCPingHelp()); err != nil {
		return err
	}
	tools := renderCPingCommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func generateCodexGoogle(targetRoot string) error {
	googleDir := filepath.Join(targetRoot, "google")
	refsDir := filepath.Join(googleDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(googleDir, "SKILL.md"), renderGoogleSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderGoogleHelp()); err != nil {
		return err
	}
	tools := renderGoogleCommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func generateCodexProxySSH(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "proxy_ssh")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderProxySSHSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderProxySSHHelp()); err != nil {
		return err
	}
	tools := renderProxySSHCommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func generateCodexGlobalAPIToken(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "globalApiToken")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderGlobalAPITokenSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderGlobalAPITokenHelp()); err != nil {
		return err
	}
	tools := renderGlobalAPITokenCommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func generateCodexFRPServer(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "frp-server")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderFRPServerSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderFRPServerHelp()); err != nil {
		return err
	}
	tools := renderFRPServerCommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func generateCodexFRPClient(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "frp-client")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderFRPClientSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderFRPClientHelp()); err != nil {
		return err
	}
	tools := renderFRPClientCommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func generateCodexAgentCodeServer(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "agent-code-server")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderAgentCodeServerSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderAgentCodeServerHelp()); err != nil {
		return err
	}
	tools := renderAgentCodeServerTools()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func generateCodexAgentWebpage(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "agent-webpage")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderAgentWebpageSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderAgentWebpageHelp()); err != nil {
		return err
	}
	tools := renderAgentWebpageTools()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func generateCodexTM(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "cicy-agent")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderTMSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderTMHelp()); err != nil {
		return err
	}
	tools := renderTMCommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func generateCodexSSH(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "cicy-ssh")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderSSHSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderSSHHelp()); err != nil {
		return err
	}
	tools := renderSSHCommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func helpCodex(targetRoot, skillName string) (SkillHelp, error) {
	return readCodexReference(targetRoot, skillName, "help.md")
}

func toolsCodex(targetRoot, skillName string) (SkillHelp, error) {
	return readCodexReference(targetRoot, skillName, "tools.md", "commands.md")
}

func readCodexReference(targetRoot, skillName string, filenames ...string) (SkillHelp, error) {
	skills, err := resolveCodexSkills([]string{skillName})
	if err != nil {
		return SkillHelp{}, err
	}
	skill := skills[0]
	var paths []string
	for _, filename := range filenames {
		path := filepath.Join(targetRoot, skill, "references", filename)
		paths = append(paths, path)
		data, err := os.ReadFile(path)
		if err == nil {
			return SkillHelp{
				Name: skill,
				Path: path,
				Text: string(data),
			}, nil
		}
		if !os.IsNotExist(err) {
			return SkillHelp{}, err
		}
	}
	return SkillHelp{}, fmt.Errorf("skill %q reference is missing at %s; install or update the skill first", skill, strings.Join(paths, ", "))
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func writeText(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func renderGoogleSkill() string {
	return fmt.Sprintf(`---
name: google
description: Use the local google CLI wrapper for Gmail, Sheets, Drive, and Calendar on this host. Also exposes `+"`google login`"+` and `+"`google status`"+` for OAuth setup (device-code flow) when authorization is missing or expired.
---

# Google

This skill covers the local `+"`google`"+` wrapper from `+"`PATH`"+`.

Use these commands directly from `+"`PATH`"+`. They read real credentials from `+"`~/cicy-ai/db/google.json`"+` (created by `+"`google login`"+`).

## Scope

Use this skill when the task involves:

- **OAuth setup / re-authorization** (`+"`google login`"+`, `+"`google status`"+`) — when the user asks to "connect Google", "authorize", "log in", "bind Google account", or any Google API call fails with an auth error
- Gmail inbox listing, reading, sending, or verification-code watching
- Google Sheets read/write/append/create
- Google Drive list/upload/download/quota work
- Google Calendar list/events/create

## OAuth Setup (`+"`google login`"+`)

If any Google subcommand reports missing authorization, run `+"`google login`"+` and follow its stdout verbatim. It self-detects three states and prints the right next-step for each:

1. **No OAuth client yet** — it prints step-by-step instructions for the user to create a "TVs and Limited Input devices" OAuth client in Google Cloud Console, copy the Client ID, and save it to `+"`~/cicy-ai/db/google_oauth_client.json`"+`. After they do this, run `+"`google login`"+` again.
2. **Client configured but not authorized** — it runs the device-code flow inline: prints a `+"`user_code`"+` and a verification URL. Show both to the user clearly, wait while the command polls Google, and report the resulting authorized email when it finishes.
3. **Already authorized** — it prints the connected email and exits.

Walk the user through one step at a time. Do not skip ahead or assume state — re-run `+"`google login`"+` after each user action and let its output drive the next step.

## Rules

1. Prefer the local wrapper commands first.
2. For unfamiliar subcommands, run `+"`google help`"+` or `+"`google <service> help`"+`.
3. Use the real token configured on the host. Do not mock Google responses.
4. Report the concrete command result back to the user.
5. **Never** invent client IDs, secrets, or refresh tokens — only what `+"`google login`"+` and the user produce together.

## Help

Read [help.md](./references/help.md) first for quick usage, rules, and examples.

## Tools

Read [tools.md](./references/tools.md) for the full tool and command shapes.
`)
}

func renderGlobalAPITokenSkill() string {
	return fmt.Sprintf(`---
name: globalApiToken
description: Use the local globalApiToken wrapper to show or refresh ~/cicy-ai/global.json api_token on this host.
---

# Global API Token

This skill covers the local `+"`globalApiToken`"+` wrapper from `+"`PATH`"+`.

Use this command directly from `+"`PATH`"+`. It reads and updates the real `+"`~/cicy-ai/global.json`"+` file on this host.

## Scope

Use this skill when the task involves:

- showing the current `+"`api_token`"+` from `+"`~/cicy-ai/global.json`"+`
- rotating or refreshing `+"`~/cicy-ai/global.json api_token`"+`

## Rules

1. Prefer the local `+"`globalApiToken`"+` command first.
2. Operate on the real `+"`~/cicy-ai/global.json`"+`; do not fabricate token values.
3. Only refresh the token when the user explicitly asks to rotate or refresh it.
4. Report the resulting token value back to the user when requested.

## Help

Read [help.md](./references/help.md) first for quick usage.

## Tools

Read [tools.md](./references/tools.md) for the full tool and command shapes.
`)
}

func renderFRPServerSkill() string {
	return fmt.Sprintf(`---
name: frp-server
description: Use the local frp-server wrapper to manage a local frps process with background start, status, connections, hot reload, and stop/start controls.
---

# FRP Server

This skill covers the local `+"`frp-server`"+` wrapper from `+"`PATH`"+`.

Use this command directly from `+"`PATH`"+`. It manages the real `+"`frps`"+` process on this host.

## Scope

Use this skill when the task involves:

- starting `+"`frps`"+` as a background service
- checking whether the FRP server is running
- checking listeners or current connections
- reloading or restarting the FRP server after config changes
- stopping the FRP server cleanly

## Rules

1. Prefer the local `+"`frp-server`"+` wrapper first.
2. Use the real config file on disk; do not invent FRP state.
3. Use `+"`status`"+` before destructive actions when the user asks to inspect the current state.
4. Prefer `+"`reload`"+` for hot reload; the wrapper may fall back to restart when the installed FRP build does not support native reload.
5. Report the real config path, log path, pid, and connection/listener data back to the user.

## Help

Read [help.md](./references/help.md) first for quick usage.

## Tools

Read [tools.md](./references/tools.md) for the supported commands.
`)
}

func renderFRPClientSkill() string {
	return fmt.Sprintf(`---
name: frp-client
description: Use the local frp-client wrapper to manage a local frpc process with background start, status, proxy connections, hot reload, and stop/start controls, including remote client management over ssh.
---

# FRP Client

This skill covers the local `+"`frp-client`"+` wrapper from `+"`PATH`"+`.

Use this command directly from `+"`PATH`"+`. It manages the real `+"`frpc`"+` process on this host.

## Scope

Use this skill when the task involves:

- starting `+"`frpc`"+` as a background service
- checking whether the FRP client is running
- checking current proxy status or connections
- reloading or restarting the FRP client after config changes
- stopping the FRP client cleanly
- managing a remote FRP client machine over `+"`ssh`"+`

## Rules

1. Prefer the local `+"`frp-client`"+` wrapper first.
2. Use the real config file on disk; do not invent FRP state.
3. Prefer `+"`connections`"+` or `+"`status`"+` before changing a working client.
4. Prefer `+"`reload`"+` for hot reload; the wrapper may fall back to restart when the installed FRP build does not support native reload.
5. Report the real config path, log path, pid, and proxy status back to the user.
6. When the target FRP client is on another machine, manage it through `+"`ssh <host> '<command>'`"+` using the remote machine's own `+"`frpc`"+`, config files, and service manager.

## Help

Read [help.md](./references/help.md) first for quick usage.

## Tools

Read [tools.md](./references/tools.md) for the supported commands.
`)
}

func renderGoogleHelp() string {
	return fmt.Sprintf(`# Google Help

## Command

- primary command: `+"`google`"+`

## Quick Start

- inspect usage: `+"`google help`"+`
- check auth status: `+"`google status`"+`
- start / re-run OAuth setup: `+"`google login`"+` (TV/Limited-Input device-code flow; self-guides through all states)
- inspect gmail shortcuts: `+"`google gmail help`"+`
- list recent mail: `+"`google gmail list 5`"+`
- list spreadsheets: `+"`google sheets list`"+`
- list drive files: `+"`google drive list`"+`
- list calendars: `+"`google calendar list`"+`

## Rules

- use the real credentials in `+"`~/cicy-ai/global.json`"+`
- do not mock Google responses
- report exact command output or concrete results back to the user

## More

- tool map: [tools.md](./tools.md)
`)
}

func renderGlobalAPITokenHelp() string {
	return fmt.Sprintf(`# Global API Token Help

## Command

- primary command: `+"`globalApiToken`"+`

## Quick Start

- show current token: `+"`globalApiToken show`"+`
- refresh token: `+"`globalApiToken refresh`"+`

## Rules

- read the real token from `+"`~/cicy-ai/global.json`"+`
- refresh updates `+"`~/cicy-ai/global.json api_token`"+` in place
- do not rotate the token unless the user explicitly asks

## More

- tool map: [tools.md](./tools.md)
`)
}

func renderCFTunnelSkill() string {
	return fmt.Sprintf(`---
name: cf-tunnel
description: Use the local cf-tunnel wrapper to manage Cloudflare Tunnel routes and DNS on this host.
---

# Cf Tunnel

This skill covers the local `+"`cf-tunnel`"+` wrapper from `+"`PATH`"+`.

Use this command directly from `+"`PATH`"+`. It reads real Cloudflare credentials from `+"`~/cicy-ai/global.json`"+`.

## Scope

Use this skill when the task involves:

- listing current Cloudflare tunnel routes for this host
- adding one or more `+"`g-<port>.<domain>`"+` tunnel hostnames that map to local ports
- deleting one or more existing tunnel routes and DNS records
- working against `+"`prod`"+` or `+"`dev`"+` Cloudflare config via `+"`CF_ENV`"+`

## Rules

1. Prefer the local `+"`cf-tunnel`"+` command first.
2. Use the real Cloudflare config from `+"`~/cicy-ai/global.json`"+`. Do not mock responses.
3. `+"`cf-tunnel`"+` manages routes and DNS only. Do not kill or manage the `+"`cloudflared`"+` process unless the user explicitly asks.
4. Report the exact hostname and port mapping results back to the user.

## Help

Read [help.md](./references/help.md) first for quick usage, rules, and examples.

## Tools

Read [tools.md](./references/tools.md) for the full tool and command shapes.
`)
}

func renderCPingSkill() string {
	return fmt.Sprintf(`---
name: cping
description: Use the local cping wrapper to check network latency to a domain or IP from this host, with emphasis on China-side reachability.
---

# cping

This skill covers the local `+"`cping`"+` wrapper from `+"`PATH`"+`.

Use it when the user asks for latency checks, China-side ping quality, or quick network verification for a hostname or IP.

## Scope

Use this skill for:

- checking latency for a domain or IP
- comparing rough China-side network quality from this host
- reporting target resolution from hostname to IP
- verifying whether a public endpoint looks reachable and fast

## Rules

1. Prefer the local `+"`cping`"+` command first.
2. Report the actual target used and the resolved IP when shown.
3. Treat the output as observational network data; do not over-claim the cause of latency.
4. If the user needs protocol-specific debugging beyond `+"`cping`"+`, say so and switch to other tools only after this quick check.

## Help

Read [help.md](./references/help.md) first for quick usage.

## Tools

Read [tools.md](./references/tools.md) for the supported command shapes.
`)
}

func renderAgentWebpageSkill() string {
	return fmt.Sprintf(`---
name: agent-webpage
description: Use the local agent-webpage wrapper to talk to the live webpage client for an agent on this host.
---

# Agent Webpage

This skill covers the local `+"`agent-webpage`"+` wrapper from `+"`PATH`"+`.

Use this command directly from `+"`PATH`"+`. It talks to the real webpage client through the live chat websocket and returns the real webpage response.

## Scope

Use this skill when the task involves:

- checking whether an agent's webpage client is connected
- running JS in the live webpage client
- sending webpage events and waiting for the response
- checking connected webpage clients for an agent

## Rules

1. Prefer the local `+"`agent-webpage`"+` command first.
2. Target a specific connected webpage by `+"`client_id`"+`.
3. If no `+"`client_id`"+` is provided, only auto-target when the current agent has exactly one connected client.
4. For response-oriented calls, wait for and report the actual webpage response instead of only reporting that the event was sent.
5. Use `+"`agent-webpage help`"+` and `+"`agent-webpage tools`"+` before guessing subcommand shapes.

## Help

Read [help.md](./references/help.md) first for quick usage, rules, and examples.

## Tools

Read [tools.md](./references/tools.md) for the supported tools, response types, and command shapes.
`)
}

func renderTMSkill() string {
	return fmt.Sprintf(`---
name: cicy-agent
description: Operate tmux panes and windows on this host with the local cicy-agent wrapper.
---

# cicy-agent

This skill is for tmux-style pane and window operations in the CiCy environment.

Primary tool:

- `+"`cicy-agent`"+` for local pane and window operations on this host

Do not use `+"`fast-api`"+` for tmux work when `+"`cicy-agent`"+` covers it.

## Scope

Use this skill for:

- listing panes
- checking pane or window status
- capturing pane output
- sending text or keys to a pane
- creating or restarting panes
- clearing panes
- listing, selecting, renaming, creating, or deleting tmux windows

## Rules

1. Prefer `+"`cicy-agent`"+` for local convenience operations on this host.
2. Do not route tmux work through `+"`fast-api`"+` unless there is a specific reason `+"`cicy-agent`"+` cannot do it.
3. The primary pane is usually `+"`w-10001`"+`.
4. Config currently lives at `+"`~/cicy-ai/db/cicy-agent.json`"+`.

## Help

Read [help.md](./references/help.md) first for quick usage.

## Tools

Read [tools.md](./references/tools.md) for the command map.
`)
}

func renderSSHSkill() string {
	return `---
name: cicy-ssh
description: Use OpenSSH on this host. Trigger when the task mentions ssh, ~/.ssh/config, ssh config hosts, ssh aliases, remote login, jump hosts, or adding/listing/using SSH nodes from local config.
---

# cicy-ssh

This skill is for SSH access and local SSH config management on this host.

Use the real OpenSSH client and treat ` + "`~/.ssh/config`" + ` as the primary source of named nodes.

## Scope

Use this skill for:

- explaining how SSH on this host is configured
- reading ` + "`~/.ssh/config`" + ` first when the user asks about SSH nodes
- listing configured ` + "`Host`" + ` entries
- adding or updating SSH node entries in ` + "`~/.ssh/config`" + `
- using configured nodes via ` + "`ssh <host>`" + `
- running one-off remote commands via ` + "`ssh <host> '<cmd>'`" + `
- checking jump-host settings, ports, users, and identity files

## Rules

1. Read ` + "`~/.ssh/config`" + ` before guessing host aliases.
2. Prefer existing ` + "`Host`" + ` aliases from config over raw hostnames when both exist.
3. Never overwrite ` + "`~/.ssh/config`" + `; preserve unrelated entries and edit surgically.
4. If the config uses ` + "`Include`" + `, inspect ` + "`~/.ssh/config`" + ` first, then follow includes only when needed.
5. When adding a node, keep the block minimal unless the user asks for extra options.
6. For actual connections, use the real ` + "`ssh`" + ` command directly. Do not invent wrapper commands.
7. If a command may prompt for a password, host-key trust, or MFA, note that interactive input may be required.

## Help

Read [help.md](./references/help.md) first for quick usage.

## Tools

Read [tools.md](./references/tools.md) for the common command shapes.
`
}

func renderCFTunnelHelp() string {
	return fmt.Sprintf(`# Cf Tunnel Help

## Command

- primary command: `+"`cf-tunnel`"+`

## Quick Start

- inspect routes: `+"`cf-tunnel list`"+`
- add one route: `+"`cf-tunnel add 8101`"+`
- add multiple routes: `+"`cf-tunnel add 5174 8010 13000`"+`
- delete a route: `+"`cf-tunnel del 8101`"+`
- use dev config: `+"`CF_ENV=dev cf-tunnel list`"+`

## Rules

- use the real Cloudflare config in `+"`~/cicy-ai/global.json`"+`
- do not mock Cloudflare responses
- `+"`cf-tunnel`"+` manages route and DNS state only; it does not manage the `+"`cloudflared`"+` process
- report exact hostname and port mappings back to the user

## More

- tool map: [tools.md](./tools.md)
`)
}

func renderCPingHelp() string {
	return fmt.Sprintf(`# cping Help

## Command

- primary command: `+"`cping`"+`

## Quick Start

- ping a domain: `+"`cping tn.cicy-ai.com`"+`
- ping an IP: `+"`cping 35.241.97.128`"+`
- compare a public hostname: `+"`cping baidu.com`"+`

## Rules

- use the real `+"`cping`"+` wrapper output, not a mocked summary
- report the target and resolved IP when shown
- treat this as a quick latency signal, not a full root-cause analysis
- if the user needs deeper diagnosis, use `+"`cping`"+` first and then move to other network tools

## More

- tool map: [tools.md](./tools.md)
`)
}

func renderFRPServerHelp() string {
	return fmt.Sprintf(`# FRP Server Help

## Command

- primary command: `+"`frp-server`"+`

## Quick Start

- inspect usage: `+"`frp-server help`"+`
- start in background: `+"`frp-server start`"+`
- check status: `+"`frp-server status`"+`
- inspect listeners or sockets: `+"`frp-server connections`"+`
- list currently connected clients: `+"`frp-server clients`"+`
- hot reload config: `+"`frp-server reload`"+`
- restart after a larger change: `+"`frp-server restart`"+`
- stop the service: `+"`frp-server stop`"+`

## Defaults

- wrapper config lookup: `+"`~/data/frp/frps.toml`"+`, `+"`~/data/frp/frps.yaml`"+`, `+"`~/data/frp/frps.yml`"+`, `+"`~/data/frp/frps.ini`"+`
- wrapper binary lookup: `+"`frps`"+` on `+"`PATH`"+`, then common local install locations
- wrapper state dir: `+"`~/.local/state/cicy-skills/frp/server`"+`

## Port Plan

- default public control port: `+"`bindPort = 9500`"+`
- keep `+"`9500/tcp`"+` open in the firewall for remote `+"`frpc`"+` clients
- allocate proxy `+"`remotePort`"+` values from `+"`9501`"+` upward
- suggested convention:
  - `+"`9501`"+` first test or bootstrap proxy
  - `+"`9510-9599`"+` long-lived service ports
  - `+"`9600-9999`"+` temporary or per-device ports
- the local dashboard can stay on `+"`127.0.0.1:7500`"+` and does not need public firewall exposure

## Token Rule

- on `+"`frp-server start`"+`, if `+"`auth.token`"+` is missing, the wrapper generates a random token automatically
- for local loopback testing, the wrapper also syncs the token into `+"`~/data/frp/frpc.toml`"+` when that client points back to this server
- for Mac, Linux, or Windows clients, copy the generated token into their installer prompt or `+"`frpc.toml`"+`

## Client Install And Start

Use the server skill itself to tell the user how to install the client.

### macOS / Linux one-line install

Direct install URL:

- `+"`curl -fsSL https://install.cicy-ai.com/frp | bash`"+`

What it does:

- downloads and installs `+"`frpc`"+`
- writes `+"`~/.config/frp/frpc.toml`"+`
- prompts the user to enter the FRP token interactively
- installs a service automatically
  - macOS -> LaunchAgent
  - Linux -> systemd service
- defaults to exposing local `+"`127.0.0.1:22`"+` as remote `+"`9502`"+`

### Windows one-line install

Use the same `+"`/frp`"+` endpoint, but save it to a file and run it with PowerShell:

- `+"`$u='https://install.cicy-ai.com/frp'; $p=Join-Path $env:TEMP 'install-frpc-client.ps1'; irm $u -OutFile $p; powershell -ExecutionPolicy Bypass -File $p`"+`

Why it is file-based instead of `+"`irm ... | iex`"+`:

- the installer needs to relaunch from a script file so it can self-elevate and install the Windows service

What it does:

- downloads and installs `+"`frpc.exe`"+`
- writes the Windows client config
- prompts the user to enter the FRP token interactively
- self-elevates and installs a Windows service through `+"`WinSW`"+`
- defaults to exposing local `+"`127.0.0.1:22`"+` as remote `+"`9502`"+`

### After install

Default SSH access path:

- `+"`ssh -p 9502 <client-user>@47.114.96.114`"+`

If the client machine is not serving SSH yet:

- macOS: enable `+"`Remote Login`"+`
- Linux: ensure `+"`sshd`"+` is installed and listening on port `+"`22`"+`
- Windows: enable `+"`OpenSSH Server`"+` if the user wants SSH-based access

### Alternate ports and multi-client checks

Examples:

- expose local `+"`3000`"+` on remote `+"`9503`"+` with the shell installer:
  - `+"`curl -fsSL https://install.cicy-ai.com/frp | bash -s -- --local-port 3000 --remote-port 9503 --name web-3000`"+`
- expose local `+"`5173`"+` on remote `+"`9504`"+` with the shell installer:
  - `+"`curl -fsSL https://install.cicy-ai.com/frp | bash -s -- --local-port 5173 --remote-port 9504 --name web-5173`"+`
- validate extra clients from the server side with a fresh port such as `+"`9511`"+` or `+"`9512`"+`, then check:
  - `+"`frp-server clients`"+`
  - `+"`frp-server connections`"+`
  - `+"`ssh -p <remote-port> <client-user>@47.114.96.114`"+`

Verified flows:

- Linux Docker client tested successfully on `+"`9511`"+`
- macOS extra client tested successfully on `+"`9512`"+`
- both were visible in `+"`frp-server clients`"+` and `+"`frp-server connections`"+`

## Rules

- use the wrapper first instead of running ad-hoc background shell jobs
- use `+"`status`"+` to report pid, config, log path, and parsed bind/dashboard info
- use `+"`connections`"+` to inspect current sockets for the live process
- prefer `+"`reload`"+` for hot reload; if native reload is unavailable, the wrapper restarts with the same config
- when the user asks how to install a client, answer from this server skill help directly instead of assuming they already have `+"`frpc`"+`

## More

- tool map: [tools.md](./tools.md)
`)
}

func renderFRPClientHelp() string {
	return fmt.Sprintf(`# FRP Client Help

## Command

- primary command: `+"`frp-client`"+`

## Quick Start

- inspect usage: `+"`frp-client help`"+`
- start in background: `+"`frp-client start`"+`
- check status: `+"`frp-client status`"+`
- inspect proxy status or sockets: `+"`frp-client connections`"+`
- hot reload config: `+"`frp-client reload`"+`
- restart after a larger change: `+"`frp-client restart`"+`
- stop the service: `+"`frp-client stop`"+`

## Defaults

- wrapper config lookup: `+"`~/data/frp/frpc.toml`"+`, `+"`~/data/frp/frpc.yaml`"+`, `+"`~/data/frp/frpc.yml`"+`, `+"`~/data/frp/frpc.ini`"+`
- wrapper binary lookup: `+"`frpc`"+` on `+"`PATH`"+`, then common local install locations
- wrapper state dir: `+"`~/.local/state/cicy-skills/frp/client`"+`

## Remote Management Over SSH

When the FRP client runs on another machine, manage it over `+"`ssh`"+` instead of pretending the local host owns that process.

Typical remote commands:

- remote status:
  - `+"`ssh ton-mac '~/.local/bin/frpc status -c ~/.config/frp/frpc.toml'`"+`
- remote logs:
  - `+"`ssh ton-mac 'tail -100 ~/.local/frp/frpc.log'`"+`
- remote config:
  - `+"`ssh ton-mac 'sed -n \"1,160p\" ~/.config/frp/frpc.toml'`"+`
- remote restart on macOS:
  - `+"`ssh ton-mac 'launchctl kickstart -k \"gui/$(id -u)/com.cicy.frpc\"'`"+`
- remote service check on macOS:
  - `+"`ssh ton-mac 'launchctl list | grep com.cicy.frpc'`"+`
- remote restart on Linux:
  - `+"`ssh my-linux 'sudo systemctl restart frpc-cicy-$USER.service'`"+`
- remote service status on Linux:
  - `+"`ssh my-linux 'systemctl status frpc-cicy-$USER.service --no-pager'`"+`

## Rules

- use the wrapper first instead of running ad-hoc background shell jobs
- use `+"`status`"+` to report pid, config, log path, and parsed upstream/admin info
- use `+"`connections`"+` to inspect native proxy status when available
- prefer `+"`reload`"+` for hot reload; if native reload is unavailable, the wrapper restarts with the same config
- when managing a remote client machine, use `+"`ssh`"+` to run the remote machine's own `+"`frpc`"+` and service commands

## More

- tool map: [tools.md](./tools.md)
`)
}

func renderAgentCodeServerSkill() string {
	return fmt.Sprintf(`---
name: agent-code-server
description: Use the local agent-code-server wrapper to open a file in the current page-bound code-server on this host.
---

# Agent Code Server

This skill covers the local `+"`agent-code-server`"+` wrapper from `+"`PATH`"+`.

Use this command directly from `+"`PATH`"+`. It sends the standard `+"`code.open_file`"+` event to the real page client.

## Scope

Use this skill when the task involves:

- opening a file in the current page's code-server
- targeting a specific connected page by `+"`page_client_id`"+`
- checking available page clients before opening a file

## Rules

1. Prefer the local `+"`agent-code-server`"+` command first.
2. Target a specific page by `+"`page_client_id`"+`.
3. If no `+"`page_client_id`"+` is provided, only auto-target when the current agent has exactly one connected page client.
4. `+"`ping`"+` checks whether the matching `+"`:code-ext`"+` client is online.
5. The standard open action accepts plain paths, `+"`file://`"+` paths, and optional line/column suffixes.
6. Use `+"`agent-code-server help`"+` and `+"`agent-code-server tools`"+` before guessing command shapes.

## Help

Read [help.md](./references/help.md) first for quick usage and examples.

## Tools

Read [tools.md](./references/tools.md) for the supported commands.
`)
}

func renderAgentCodeServerHelp() string {
	return fmt.Sprintf(`# Agent Code Server Help

## Command

- primary command: `+"`agent-code-server`"+`

## Quick Start

- inspect usage: `+"`agent-code-server help`"+`
- inspect tool map: `+"`agent-code-server tools`"+`
- inspect current page clients: `+"`agent-code-server list`"+`
- check whether code-server is connected for a page: `+"`agent-code-server ping web-abc123`"+`
- open a file in the current page-bound code-server: `+"`agent-code-server open ~/.bashrc:12 web-abc123`"+`

## Rules

- use the real live page client, not mocks
- identify the target by `+"`page_client_id`"+`
- `+"`ping`"+` checks whether `+"`page_client_id:code-ext`"+` is connected
- the standard event is `+"`code.open_file`"+`
- the open path may include `+"`:line`"+`, `+"`:line:column`"+`, or range suffixes
- if you need the exact command shape, read [tools.md](./tools.md)

## More

- tool map: [tools.md](./tools.md)
`)
}

func renderAgentWebpageHelp() string {
	return fmt.Sprintf(`# Agent Webpage Help

## Command

- primary command: `+"`agent-webpage`"+`

## Quick Start

- inspect usage: `+"`agent-webpage help`"+`
- inspect tool map: `+"`agent-webpage tools`"+`
- ping the current agent's only connected webpage client: `+"`agent-webpage ping`"+`
- ping a specific client: `+"`agent-webpage ping web-abc123`"+`
- run JS in a specific live webpage client: `+"`agent-webpage exec-js 'window.location.href' web-abc123`"+`
- print the current active agent id from the live webpage: `+"`agent-webpage current-active-agent-id web-abc123`"+`
- print the current master agent id from the live webpage: `+"`agent-webpage current-master-agent-id web-abc123`"+`
- inspect connected clients: `+"`agent-webpage clients`"+`

## Rules

- use the real live webpage client, not mocks
- identify the target by `+"`client_id`"+`; the tool resolves the owning `+"`agent_id`"+`
- for response-oriented calls, report the actual returned payload
- if you need the exact subcommand shape, read [tools.md](./tools.md)

## More

- tool map: [tools.md](./tools.md)
`)
}

func renderTMHelp() string {
	return fmt.Sprintf(`# cicy-agent Help

## Command

- primary command: `+"`cicy-agent`"+`

## Quick Start

- list panes: `+"`cicy-agent ls`"+`
- capture pane output: `+"`cicy-agent capture w-10001`"+`
- send a message: `+"`cicy-agent msg w-10001 \"hello\"`"+`
- send a key: `+"`cicy-agent send-keys w-10001 Enter`"+`
- inspect tmux windows: `+"`cicy-agent windows`"+`

## Multi-Node

- use the configured default target: `+"`cicy-agent ls`"+`
- select a configured node: `+"`cicy-agent --node dev ls`"+`
- select a configured node by env: `+"`TM_NODE=dev cicy-agent ls`"+`
- bypass config and hit a specific API directly: `+"`TM_API_BASE=http://127.0.0.1:8021 cicy-agent ls`"+`

How to configure and use multi-node:

- create `+"`~/cicy-ai/db/cicy-agent.json`"+`
- set top-level `+"`default`"+` to the node you want `+"`cicy-agent ls`"+` to use
- define each node under `+"`nodes.<name>`"+`
- use `+"`cicy-agent --node <name> ...`"+` when you want a non-default node

Recommended `+"`~/cicy-ai/db/cicy-agent.json`"+` shape:

    {
      "default": "default",
      "nodes": {
        "default": {"api": "http://127.0.0.1:8008", "api_token": "<copy from ~/cicy-ai/global.json api_token>"},
        "dev": {"api": "http://127.0.0.1:8021", "api_token": "<copy from ~/cicy-ai/global.json api_token>"}
      }
    }

Resolution order:

- `+"`TM_API_BASE`"+` or `+"`API_BASE`"+`
- `+"`TM_NODE`"+` or `+"`--node`"+`, then `+"`~/cicy-ai/db/cicy-agent.json nodes[<name>]`"+`
- `+"`~/cicy-ai/db/cicy-agent.json default`"+` -> `+"`nodes[<default>]`"+`
- `+"`~/cicy-ai/db/cicy-agent.json api|api_base|url`"+`
- local fallback `+"`http://127.0.0.1:${TM_API_PORT|API_PORT|8008}`"+`

Token rules:

- `+"`TM_TOKEN`"+` overrides everything
- otherwise `+"`nodes.<name>.api_token`"+` is used
- if `+"`~/cicy-ai/db/cicy-agent.json`"+` is missing, `+"`cicy-agent`"+` uses an in-memory default:
  - `+"`default = default`"+`
  - `+"`nodes.default.api = http://127.0.0.1:8008`"+`
  - `+"`nodes.default.api_token = ~/cicy-ai/global.json api_token`"+`

## Rules

- prefer `+"`cicy-agent`"+` for quick local pane work
- avoid `+"`fast-api`"+` for tmux work when `+"`cicy-agent`"+` covers it
- the common primary pane is `+"`w-10001`"+`

## More

- tool map: [tools.md](./tools.md)
`)
}

func renderSSHHelp() string {
	return `# cicy-ssh Help

## Primary Files

- main config: ` + "`~/.ssh/config`" + `
- optional includes: inspect ` + "`Include`" + ` lines only when needed

## Quick Start

- list configured aliases from ` + "`~/.ssh/config`" + `
- inspect one alias block before using it
- connect with ` + "`ssh <alias>`" + `
- run a remote command with ` + "`ssh <alias> '<command>'`" + `

## Add Node Workflow

Preferred minimal block:

` + "```sshconfig" + `
Host my-node
  HostName 1.2.3.4
  User root
  Port 22
` + "```" + `

Only add ` + "`IdentityFile`" + `, ` + "`ProxyJump`" + `, or other advanced fields when the user asks or the existing config style clearly expects them.

## Rules

- always read ` + "`~/.ssh/config`" + ` before guessing aliases
- preserve unrelated config when editing
- prefer existing aliases from config over raw hostnames
- after editing, re-read the affected block and report the alias used
`
}

func renderGoogleCommands() string {
	return `# Google Commands

## Auth

- ` + "`google login`" + `  — set up or re-run Google OAuth on this host (device-code flow). Self-detects current state and prints the right next-step. Use whenever the user says "connect Google" / "authorize" / "log in", or when any other ` + "`google ...`" + ` command fails with auth error.
- ` + "`google status`" + ` — show whether Google is currently authorized (and as which account) without starting the device flow.

## Gmail

- ` + "`google gmail list [count]`" + `
- ` + "`google gmail read <n>`" + `
- ` + "`google gmail read-all`" + `
- ` + "`google gmail send <to> <subject> [body]`" + `
- ` + "`google gmail watch [keyword]`" + `

## Sheets

- ` + "`google sheets list`" + `
- ` + "`google sheets read <id> <range>`" + `
- ` + "`google sheets write <id> <range> <json>`" + `
- ` + "`google sheets append <id> <range> <json>`" + `
- ` + "`google sheets create <title>`" + `

## Drive

- ` + "`google drive list [query] [pageSize]`" + `
- ` + "`google drive upload <name> <content>`" + `
- ` + "`google drive upload-dir <path> [--exclude patterns]`" + `
- ` + "`google drive download <id>`" + `
- ` + "`google drive download-dir <id> <path>`" + `
- ` + "`google drive quota`" + `

## Calendar

- ` + "`google calendar list`" + `
- ` + "`google calendar events [calId] [max]`" + `
- ` + "`google calendar create <summary> <start> <end>`" + `
`
}

func renderProxySSHSkill() string {
	return `---
name: proxy_ssh
description: Use the local proxy_ssh wrapper to manage autossh-based SOCKS5 proxy profiles on this host (list/show/create/delete, start/stop/restart, connectivity test). Each profile pins a local SOCKS port to an SSH target.
---

# Proxy SSH

This skill covers the local ` + "`proxy_ssh`" + ` wrapper from ` + "`PATH`" + `.

Use this command directly from ` + "`PATH`" + `. It manages real autossh processes and persists profiles in ` + "`~/cicy-ai/db/proxy_ssh.json`" + ` on this host.

## Scope

Use this skill when the task involves:

- Listing or inspecting SOCKS proxy profiles on this host
- Creating a new SOCKS proxy that tunnels through an SSH host (` + "`ssh -D <port>`" + ` via autossh)
- Starting / stopping / restarting an existing profile
- Testing whether a profile's SOCKS port actually reaches the public internet
- Installing autossh (` + "`proxy_ssh install-autossh`" + `) when the binary is missing

## Rules

1. Prefer the local ` + "`proxy_ssh`" + ` command first; do not invoke ` + "`autossh`" + `/` + "`ssh -D`" + ` by hand unless the user asks for the raw command.
2. Always reference profiles by their ` + "`name`" + `. Run ` + "`proxy_ssh list`" + ` first if unsure.
3. When creating a profile, gather: ` + "`name`" + `, ` + "`--local-port`" + `, and either ` + "`--target user@host`" + ` (one-shot) or the trio ` + "`--ssh-host-name`" + ` + ` + "`--ssh-user`" + ` + ` + "`--ssh-port`" + `. Confirm with the user before running.
4. After ` + "`start`" + `, optionally run ` + "`test <name>`" + ` to confirm egress; report the resulting IP/country back to the user.
5. Use ` + "`--json`" + ` for scriptable output when chaining with other tools.

## Help

Read [help.md](./references/help.md) first for quick usage, rules, and examples.

## Tools

Read [tools.md](./references/tools.md) for the full tool and command shapes.
`
}

func renderProxySSHHelp() string {
	return `# Proxy SSH Help

## Command

- primary command: ` + "`proxy_ssh`" + `
- config file: ` + "`~/cicy-ai/db/proxy_ssh.json`" + ` (managed by this command — do not edit by hand)

## Quick Start

- inspect usage: ` + "`proxy_ssh --help`" + ` or ` + "`proxy_ssh <command> --help`" + `
- list all profiles: ` + "`proxy_ssh list`" + `
- show one profile: ` + "`proxy_ssh show <name>`" + `
- create a profile: ` + "`proxy_ssh create <name> --local-port 1080 --target user@example.com`" + `
- start: ` + "`proxy_ssh start <name>`" + `
- stop: ` + "`proxy_ssh stop <name>`" + `
- restart: ` + "`proxy_ssh restart <name>`" + `
- connectivity test: ` + "`proxy_ssh test <name>`" + `  (reports egress IP / country)
- delete: ` + "`proxy_ssh delete <name>`" + `  (use ` + "`--force`" + ` to stop first)
- install autossh: ` + "`proxy_ssh install-autossh`" + `

## Rules

- always reference profiles by ` + "`name`" + ` — run ` + "`proxy_ssh list`" + ` to discover them
- after ` + "`create`" + ` the profile is **not** auto-started — run ` + "`start`" + ` explicitly
- ` + "`test`" + ` accepts an optional ` + "`--url`" + ` to override the default probe URL
- pass ` + "`--json`" + ` to any subcommand for machine-readable output

## More

Read [tools.md](./references/tools.md) for the full subcommand reference with all flags.
`
}

// renderProxySSHCommands returns the proxy_ssh tools.md content.
//
// tools.md convention (apply to every skill):
//   - tools.md is rendered in the UI as click-to-send tokens. EVERY inline
//     `code` span becomes a clickable button that loads the token into the
//     "Send to agent" textarea. Therefore tools.md MUST contain only real,
//     send-as-is command strings — no prose, no placeholder annotations like
//     `<required>` or `--name <value>`, no example flows, no convention text.
//   - The only allowed content is one H1 title and a single 2-column table:
//     Command | What it does. The Command column holds a single backticked
//     copy-pasteable command per row, ready to send to an agent verbatim.
//   - Detailed usage, flag matrices, and multi-step examples belong in
//     help.md (which is rendered non-clickable).
func renderProxySSHCommands() string {
	return `# Proxy SSH Commands

| Command | What it does |
|---------|--------------|
| ` + "`proxy_ssh list`" + ` | List every configured profile |
| ` + "`proxy_ssh list --json`" + ` | Same, as JSON |
| ` + "`proxy_ssh show <name>`" + ` | Show one profile's config and runtime state |
| ` + "`proxy_ssh create <name> --local-port <port> --target user@host`" + ` | Create a profile that tunnels SOCKS5 through an SSH target |
| ` + "`proxy_ssh delete <name>`" + ` | Remove a profile |
| ` + "`proxy_ssh delete <name> --force`" + ` | Stop then remove |
| ` + "`proxy_ssh start <name>`" + ` | Launch the autossh tunnel for a profile |
| ` + "`proxy_ssh stop <name>`" + ` | Kill the running autossh for a profile |
| ` + "`proxy_ssh restart <name>`" + ` | Stop then start |
| ` + "`proxy_ssh test <name>`" + ` | Probe egress IP through the SOCKS port |
| ` + "`proxy_ssh install-autossh`" + ` | Install autossh via apt or brew when missing |
`
}

func renderCFTunnelCommands() string {
	return `# Cf Tunnel Commands

## Main

- ` + "`cf-tunnel list`" + `
- ` + "`cf-tunnel add <port> [port2 ...]`" + `
- ` + "`cf-tunnel del <port> [port2 ...]`" + `

## Environment

- default environment: ` + "`prod`" + `
- override environment: ` + "`CF_ENV=dev cf-tunnel list`" + `
- override environment: ` + "`CF_ENV=dev cf-tunnel add 5174 8010 13000`" + `

## Notes

- hostnames follow the pattern ` + "`g-<port>.<domain>`" + `
- the command reads Cloudflare config from ` + "`~/cicy-ai/global.json`" + `
- it manages tunnel routes and DNS records, not the ` + "`cloudflared`" + ` process
`
}

func renderCPingCommands() string {
	return `# cping Commands

## Main

- ` + "`cping <domain_or_ip>`" + `

## Examples

- ` + "`cping tn.cicy-ai.com`" + `
- ` + "`cping 35.241.97.128`" + `
- ` + "`cping baidu.com`" + `

## Notes

- the command may resolve a hostname to an IP before reporting results
- the output is a quick latency snapshot from this host's perspective
- use it as a first-pass network check before deeper debugging
`
}

func renderGlobalAPITokenCommands() string {
	return `# Global API Token Commands

## Main

- ` + "`globalApiToken show`" + `
- ` + "`globalApiToken refresh`" + `

## Notes

- both commands operate on ` + "`~/cicy-ai/global.json`" + `
- ` + "`show`" + ` prints the current ` + "`api_token`" + `
- ` + "`refresh`" + ` generates a new token and writes it back to ` + "`~/cicy-ai/global.json`" + `
`
}

func renderFRPServerCommands() string {
	return `# FRP Server Commands

## Main

- ` + "`frp-server start [--config PATH] [--bin PATH]`" + `
- ` + "`frp-server run [--config PATH] [--bin PATH]`" + `
- ` + "`frp-server stop`" + `
- ` + "`frp-server restart [--config PATH] [--bin PATH]`" + `
- ` + "`frp-server status [--config PATH] [--bin PATH]`" + `
- ` + "`frp-server connections`" + `
- ` + "`frp-server clients`" + `
- ` + "`frp-server reload [--config PATH] [--bin PATH]`" + `
- ` + "`frp-server logs [N]`" + `
- ` + "`frp-server raw -- <real frps args...>`" + `

## Defaults

- config search: ` + "`~/data/frp/frps.toml`" + `, ` + "`~/data/frp/frps.yaml`" + `, ` + "`~/data/frp/frps.yml`" + `, ` + "`~/data/frp/frps.ini`" + `
- binary search: ` + "`frps`" + ` on PATH, then common local install locations
- state dir: ` + "`~/.local/state/cicy-skills/frp/server`" + `

## Port Plan

- use ` + "`bindPort = 9500`" + ` for the public FRP control port
- keep ` + "`9500/tcp`" + ` open in the firewall for remote clients
- assign ` + "`remotePort`" + ` from ` + "`9501`" + ` upward
- reserve ` + "`9501`" + ` as the first smoke-test or bootstrap proxy port

## Notes

- ` + "`start`" + ` runs the server in the background and records pid/config/log state
- when ` + "`auth.token`" + ` is missing, ` + "`start`" + ` generates a random token automatically
- ` + "`status`" + ` reports pid, config path, log path, bind address, dashboard address, and live listeners when available
- ` + "`connections`" + ` reports current process sockets for the live FRP server
- ` + "`reload`" + ` attempts native reload first and falls back to restart when needed
`
}

func renderFRPClientCommands() string {
	return `# FRP Client Commands

## Main

- ` + "`frp-client start [--config PATH] [--bin PATH]`" + `
- ` + "`frp-client run [--config PATH] [--bin PATH]`" + `
- ` + "`frp-client stop`" + `
- ` + "`frp-client restart [--config PATH] [--bin PATH]`" + `
- ` + "`frp-client status [--config PATH] [--bin PATH]`" + `
- ` + "`frp-client connections`" + `
- ` + "`frp-client reload [--config PATH] [--bin PATH]`" + `
- ` + "`frp-client logs [N]`" + `
- ` + "`frp-client raw -- <real frpc args...>`" + `

## Remote Management Over SSH

- ` + "`ssh ton-mac '~/.local/bin/frpc status -c ~/.config/frp/frpc.toml'`" + `
- ` + "`ssh ton-mac 'tail -100 ~/.local/frp/frpc.log'`" + `
- ` + "`ssh ton-mac 'sed -n \"1,160p\" ~/.config/frp/frpc.toml'`" + `
- ` + "`ssh ton-mac 'launchctl kickstart -k \"gui/$(id -u)/com.cicy.frpc\"'`" + `
- ` + "`ssh my-linux 'sudo systemctl restart frpc-cicy-$USER.service'`" + `

## Notes

- local wrapper commands manage the current host's own frpc process
- for a client machine reached through FRP SSH, manage its frpc through ` + "`ssh <host> '<cmd>'`" + `
- prefer the remote machine's native service manager over ad-hoc background shell jobs
`
}

func renderTMCommands() string {
	return `# cicy-agent Command Reference

This skill uses the local ` + "`cicy-agent`" + ` command from ` + "`PATH`" + `.

## Main

Use `+"`cicy-agent`"+` for local pane work:

- `+"`cicy-agent ls`"+`
- `+"`cicy-agent capture w-10001`"+`
- `+"`cicy-agent msg w-10001 \"hello\"`"+`
- `+"`cicy-agent send-keys w-10001 Enter`"+`
- `+"`cicy-agent create my-pane`"+`
- `+"`cicy-agent restart`"+`
- `+"`cicy-agent clear w-10001`"+`

Multi-node examples:

- `+"`cicy-agent ls`"+` -> use configured default target
- `+"`cicy-agent --node dev ls`"+` -> use the `+"`dev`"+` node config
- `+"`TM_NODE=dev cicy-agent capture w-10001`"+` -> target `+"`dev`"+`
- `+"`TM_API_BASE=http://127.0.0.1:8021 cicy-agent capture w-10001`"+` -> bypass node selection

Supported `+"`~/cicy-ai/db/cicy-agent.json`"+` keys:

- `+"`default`"+`
- `+"`api | api_base | url`"+`
- `+"`port`"+`
- `+"`nodes.<name>.api | api_base | url`"+`
- `+"`nodes.<name>.api_token`"+`
- `+"`nodes.<name>.token`"+` for legacy compatibility
- `+"`nodes.<name>.port`"+`

Observed `+"`cicy-agent`"+` commands:

- `+"`ls`"+`
- `+"`tree`"+`
- `+"`windows`"+`
- `+"`capture <pane>`"+`
- `+"`msg <pane> <text>`"+`
- `+"`msg_wait <pane> <text> [timeout]`"+`
- `+"`send-keys <pane> <keys>`"+`
- `+"`create <name>`"+`
- `+"`restart`"+`
- `+"`clear <pane>`"+`

## Notes

- avoid `+"`fast-api`"+` for tmux work; use `+"`cicy-agent`"+`
- the common primary pane is `+"`w-10001`"+`
- config currently uses `+"`TM_*`"+` env vars and `+"`~/cicy-ai/db/cicy-agent.json`"+`
`
}

func renderSSHCommands() string {
	return `# cicy-ssh Command Reference

## Config Discovery

- read ` + "`~/.ssh/config`" + `
- parse ` + "`Host`" + ` entries to list known nodes
- inspect ` + "`HostName`" + `, ` + "`User`" + `, ` + "`Port`" + `, ` + "`IdentityFile`" + `, and ` + "`ProxyJump`" + `

## Common Commands

- ` + "`ssh <alias>`" + `
- ` + "`ssh <alias> '<command>'`" + `
- ` + "`ssh -J <jump-host> <alias>`" + ` when a one-off jump is needed
- ` + "`ssh -F ~/.ssh/config <alias>`" + ` when you need to force a specific config file

## Editing Rules

- append or edit host blocks surgically
- do not replace the whole config file
- keep new host blocks minimal unless the user asks for more options
`
}

func renderAgentCodeServerTools() string {
	return `# Agent Code Server Tools

- ping [page_client_id] -> checks whether the matching :code-ext client is connected
- list -> lists current page_client_id values and code-server connectivity
- clients -> legacy alias of list
- open <path> [page_client_id] -> direct push code.open_file to the page client; supports file:// and line/column suffixes
`
}

func generateCodexAgentSummary(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "agent-summary")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderAgentSummarySkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderAgentSummaryHelp()); err != nil {
		return err
	}
	tools := renderAgentSummaryTools()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func renderAgentSummarySkill() string {
	return fmt.Sprintf(`---
name: agent-summary
description: Use the local agent-summary wrapper to generate conversation summaries and handoff documents for agents on this host.
---

# Agent Summary

This skill covers the local `+"`agent-summary`"+` wrapper from `+"`PATH`"+`.

Use this command directly from `+"`PATH`"+`. It reads agent request snapshots from `+"`~/cicy-ai/workers/<agent-id>/.cicy/history/current.json`"+` and generates summaries.

## Scope

Use this skill when the task involves:

- generating a summary of an agent's conversation
- creating a handoff document for another agent to continue work
- analyzing token usage and conversation stats
- extracting slim conversation JSON for further processing

## Rules

1. Prefer the local `+"`agent-summary`"+` command first.
2. Target agents by their worker ID (e.g., `+"`w-10019`"+`) or by path to current.json.
3. The `+"`--ai`"+` mode generates a detailed handoff document using configured AI providers.
4. Report the generated summary or stats back to the user.

## Help

Read [help.md](./references/help.md) first for quick usage and examples.

## Tools

Read [tools.md](./references/tools.md) for the supported commands.
`)
}

func renderAgentSummaryHelp() string {
	return fmt.Sprintf(`# Agent Summary Help

## Command

- primary command: `+"`agent-summary`"+`

## Quick Start

- generate text summary: `+"`agent-summary w-10019`"+`
- show token stats: `+"`agent-summary w-10019 --stats`"+`
- output slim conversation JSON: `+"`agent-summary w-10019 --slim`"+`
- output structured text for AI: `+"`agent-summary w-10019 --text`"+`
- generate AI summary (default provider): `+"`agent-summary w-10019 --ai`"+`
- use specific provider: `+"`agent-summary w-10019 --ai --provider=deepseek`"+`
- use specific model: `+"`agent-summary w-10019 --ai --model=deepseek-chat`"+`
- custom prompt: `+"`agent-summary w-10019 --ai --prompt=\"自定义提示\"`"+`

## Snapshot Location

- snapshots are at `+"`~/cicy-ai/workers/<agent-id>/.cicy/history/current.json`"+`
- supports both Anthropic and OpenAI (Responses API) formats

## AI Summary Output

When using `+"`--ai`"+`, the tool saves three files to `+"`~/cicy-ai/workers/<agent-id>/.cicy/history/summary/`"+`:

- `+"`<conversation_id>.stats.md`"+` - token stats and metadata
- `+"`<conversation_id>.raw.md`"+` - raw structured conversation
- `+"`<conversation_id>.summary.md`"+` - AI-generated handoff document

## Rules

- use the real snapshot data, not mocks
- AI providers are configured in `+"`~/cicy-ai/global.json`"+`
- the default AI summary generates a Chinese handoff document

## More

- tool map: [tools.md](./tools.md)
`)
}

func renderAgentSummaryTools() string {
	return `# Agent Summary Commands

## Main

- ` + "`agent-summary <agent-id>`" + ` -> generate text summary (default)
- ` + "`agent-summary <path-to-current.json>`" + ` -> summary from specific file
- ` + "`agent-summary <agent-id> --stats`" + ` -> show token stats only
- ` + "`agent-summary <agent-id> --slim`" + ` -> output slim conversation JSON
- ` + "`agent-summary <agent-id> --text`" + ` -> output structured text for AI
- ` + "`agent-summary <agent-id> --ai`" + ` -> generate AI summary (default provider)
- ` + "`agent-summary <agent-id> --ai --provider=<name>`" + ` -> use specific provider
- ` + "`agent-summary <agent-id> --ai --model=<model>`" + ` -> use specific model
- ` + "`agent-summary <agent-id> --ai --prompt='<prompt>'`" + ` -> custom prompt

## Notes

- agent-id is the worker ID like ` + "`w-10019`" + `
- snapshots are at ` + "`~/cicy-ai/workers/<agent-id>/.cicy/history/current.json`" + `
- supports both Anthropic and OpenAI API formats
- AI providers configured in ` + "`~/cicy-ai/global.json`" + `
`
}

func renderAgentWebpageTools() string {
	return `# Agent Webpage Tools

## Main

- ` + "`agent-webpage help`" + ` -> print usage and guidance
- ` + "`agent-webpage tools`" + ` -> print this tool map
- ` + "`agent-webpage ping [client_id]`" + ` -> sends ` + "`webpage_ping`" + ` directly to ` + "`client_id`" + ` and waits for ` + "`webpage_pong`" + `
- ` + "`agent-webpage ipc-ping [client_id]`" + ` -> sends ` + "`ipc_ping`" + ` directly to ` + "`client_id`" + ` and waits for ` + "`ipc_pong`" + `
- ` + "`agent-webpage exec-js '<js>' [client_id]`" + ` -> sends ` + "`exec_js`" + ` directly to ` + "`client_id`" + ` and waits for ` + "`exec_js_result`" + `
- ` + "`agent-webpage current-active-agent-id [client_id]`" + ` -> prints ` + "`devStore.Workspace.activeCliPaneId`" + ` from the live webpage
- ` + "`agent-webpage current-master-agent-id [client_id]`" + ` -> prints ` + "`devStore.Workspace.masterAgentId`" + ` from the live webpage
- ` + "`agent-webpage send <type> <data_json> [client_id] [expect_type]`" + ` -> sends a custom event directly to ` + "`client_id`" + ` and waits for a matching websocket response when possible
- ` + "`agent-webpage clients`" + ` -> lists connected chat/webpage clients

## Response Rules

- ` + "`ping`" + ` waits for ` + "`webpage_pong`" + `
- ` + "`ipc-ping`" + ` waits for ` + "`ipc_pong`" + `
- ` + "`exec-js`" + ` waits for ` + "`exec_js_result`" + `
- ` + "`send`" + ` injects a ` + "`requestId`" + ` when the payload is a JSON object and waits for a response when it can match by ` + "`requestId`" + ` and/or ` + "`expect_type`" + `
- when a response is captured, print the real response JSON or result body back to the caller

## Notes

- the preferred target is ` + "`client_id`" + ` such as ` + "`web-abc123`" + `
- if ` + "`client_id`" + ` is omitted, the command only auto-targets when the current worker agent has exactly one connected client
- the command resolves the owning ` + "`agent_id`" + ` and then talks to the live chat websocket using ` + "`agent_id + client_id`" + `
`
}

func generateCodexUSSpotProxy(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "us-spot-proxy")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderUSSpotProxySkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderUSSpotProxyHelp()); err != nil {
		return err
	}
	tools := renderUSSpotProxyCommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func generateCodexCicyMihomo(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "cicy-mihomo")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderCicyMihomoSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderCicyMihomoHelp()); err != nil {
		return err
	}
	tools := renderCicyMihomoCommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func renderCicyMihomoSkill() string {
	return `---
name: cicy-mihomo
description: Cicy Mihomo Proxy — manage the local Cicy Mihomo (mihomo / clash-meta fork) proxy on this host with start/stop/reload/status/logs and node speed-testing.
---

# Cicy Mihomo Proxy

This skill (` + "`cicy-mihomo`" + `) covers the local ` + "`cicy-mihomo`" + ` wrapper from ` + "`PATH`" + `.

Use this command directly. It controls a local Cicy Mihomo (a fork of
` + "`mihomo`" + ` / clash-meta) proxy process and exposes a SOCKS/mixed port on
` + "`9001`" + `, with the controller API on ` + "`127.0.0.1:19001`" + `.

## Scope

Use this skill when the task involves:

- starting / stopping / restarting / reloading mihomo
- viewing the current config or generating a fresh template
- tailing mihomo logs
- speed-testing the configured proxy nodes against fixed targets (anthropic / google / github / cf)
- installing the mihomo binary itself (` + "`cicy-mihomo install`" + ` downloads from the cicy-ai/cicy-mihomo release)

## Rules

1. Prefer ` + "`cicy-mihomo`" + ` over hand-rolled ` + "`mihomo`" + ` invocations — the wrapper handles pid/log/state in a consistent location.
2. Config lives at ` + "`~/cicy-ai/db/mihomo.yaml`" + `. Don't move it; the wrapper hard-codes that path.
3. Hot reload via ` + "`reload`" + ` rather than restart whenever possible — keeps connections alive.
4. ` + "`test`" + ` reports observational network data; don't over-attribute slowness to a single node from one run.

## Help

Read [help.md](./references/help.md) first for quick usage.

## Tools

Read [tools.md](./references/tools.md) for the full subcommand reference.
`
}

func renderCicyMihomoHelp() string {
	return `# Cicy Mihomo Proxy — Help

## Command
- primary command: ` + "`cicy-mihomo`" + `

## Quick Start
- inspect usage: ` + "`cicy-mihomo help`" + `
- generate a default config: ` + "`cicy-mihomo gen-config`" + `
- show the current config: ` + "`cicy-mihomo show-config`" + `
- start in background: ` + "`cicy-mihomo start`" + `
- check status: ` + "`cicy-mihomo status`" + `
- hot reload after editing config: ` + "`cicy-mihomo reload`" + `
- tail recent logs: ` + "`cicy-mihomo logs 200`" + `
- follow logs live: ` + "`cicy-mihomo logs -f`" + `
- stop the service: ` + "`cicy-mihomo stop`" + `
- restart cleanly: ` + "`cicy-mihomo restart`" + `
- install the mihomo binary (first run): ` + "`cicy-mihomo install`" + `
- test all proxy nodes: ` + "`cicy-mihomo test`" + `

## Defaults
- mihomo binary lookup: ` + "`~/.local/bin/mihomo`" + ` (or ` + "`MIHOMO_BIN`" + ` env)
- config path: ` + "`~/cicy-ai/db/mihomo.yaml`" + `
- mixed proxy port: ` + "`9001`" + `
- controller API: ` + "`127.0.0.1:19001`" + `
- pid / state / log dir: ` + "`~/.local/state/cicy-skills/mihomo/`" + `

## Install env overrides (for ` + "`cicy-mihomo install`" + `)
- ` + "`CICY_MIHOMO_VERSION`" + ` — pin a release tag (default v1.10.2)
- ` + "`GITHUB_PROXY`" + ` — URL prefix for github.com (default https://gh-proxy.com/)
- ` + "`CICY_MIHOMO_RELEASE_URL`" + ` — fully qualified direct download URL

## Rules
- read the real config in ` + "`~/cicy-ai/db/mihomo.yaml`" + `; do not invent state
- prefer ` + "`reload`" + ` over ` + "`restart`" + ` for config changes
- ` + "`test`" + ` is observational — report exact targets and timings, not opinions

## More
- tool map: [tools.md](./tools.md)
`
}

func renderCicyMihomoCommands() string {
	return `# Cicy Mihomo Proxy — Command Reference

This skill uses the local ` + "`cicy-mihomo`" + ` command from ` + "`PATH`" + `.

## Lifecycle

- ` + "`cicy-mihomo start`" + ` — start mihomo in the background
- ` + "`cicy-mihomo stop`" + ` — stop the running mihomo
- ` + "`cicy-mihomo restart`" + ` — stop then start
- ` + "`cicy-mihomo reload`" + ` — hot reload the config via the controller API (preferred for config-only changes)
- ` + "`cicy-mihomo status`" + ` — print pid, binary path, config path, log path, port, controller addr

## Config

- ` + "`cicy-mihomo template`" + ` — print a starter mihomo.yaml to stdout
- ` + "`cicy-mihomo gen-config`" + ` — write the starter config to ` + "`~/cicy-ai/db/mihomo.yaml`" + ` if absent
- ` + "`cicy-mihomo show-config`" + ` — print the current config (secrets masked)

## Logs

- ` + "`cicy-mihomo logs`" + ` — print the last 100 lines
- ` + "`cicy-mihomo logs N`" + ` — print the last N lines
- ` + "`cicy-mihomo logs -f`" + ` — follow the log live

## Binary

- ` + "`cicy-mihomo install`" + ` — download the platform-matching mihomo binary into ` + "`~/.local/bin/mihomo`" + `

## Speed testing

- ` + "`cicy-mihomo test`" + ` — for each configured proxy node, time HTTP requests to fixed targets (anthropic, google, github, cf) and report per-node summary

## Files / ports

- config:     ` + "`~/cicy-ai/db/mihomo.yaml`" + `
- pid:        ` + "`~/.local/state/cicy-skills/mihomo/pid`" + `
- state:      ` + "`~/.local/state/cicy-skills/mihomo/state.json`" + `
- log:        ` + "`~/.local/state/cicy-skills/mihomo/mihomo.log`" + `
- proxy port: 9001
- API:        127.0.0.1:19001
`
}

func renderUSSpotProxySkill() string {
	return `---
name: us-spot-proxy
description: Provision a US Aliyun spot ECS instance with mihomo + vpn_us passthrough and a persistent data disk that outlives the spot instance.
---

# us-spot-proxy

Provision a US Aliyun spot ECS instance with mihomo + vpn_us passthrough + **persistent data disk**.

The script lives at ` + "`~/projects/cicy-code/skills/us-spot-proxy`" + `.

## Design

- A persistent cloud disk (default 40GB, ~15元/月) is created once
- A cheap spot instance is created on demand (~26元/月, billed by hour)
- mihomo binary + config live on the persistent disk
- Instance reclaimed or destroyed: the disk survives
- Re-run the script to create a new instance and reattach the same disk

## Usage

  us-spot-proxy                  # create spot + attach persistent disk
  us-spot-proxy --destroy        # delete instance, keep disk
  us-spot-proxy --destroy-all    # delete instance AND disk

## Rules

1. Run from anywhere — it auto-detects existing disk and instance state.
2. After provisioning, ` + "`cicy-mihomo reload`" + ` to register the new node.
3. To fully clean up: ` + "`us-spot-proxy --destroy-all`" + `

## Help

Read [help.md](./references/help.md) for the quick reference.
`
}

func renderUSSpotProxyHelp() string {
	return `# us-spot-proxy Help

## Workflow

  1. First run: creates 40GB cloud_efficiency disk + spot instance + configures everything
  2. Subsequent runs: detects existing disk, creates new instance, attaches disk, done
  3. --destroy: kills the instance, disk stays available
  4. --destroy-all: kills instance AND deletes the persistent disk

## Data persistence

Everything is stored on the persistent disk mounted at /data/mihomo/:
- mihomo binary and config
- autossh SSH tunnel config
- All logs

After a spot reclaim, just run ` + "`us-spot-proxy`" + ` and the disk is reattached.
`
}

func renderUSSpotProxyCommands() string {
	return `# us-spot-proxy Commands

## Main

- ` + "`us-spot-proxy`" + ` -> provision + attach + configure + test
- ` + "`us-spot-proxy --destroy`" + ` -> delete instance (keeps disk)
- ` + "`us-spot-proxy --destroy-all`" + ` -> delete instance AND disk

## After provisioning

- ` + "`cicy-mihomo reload`" + ` -> pick up the new node in local mihomo
- ` + "`cicy-mihomo test`" + ` -> test all proxy nodes
- ` + "`ssh us-spot-proxy 'cat /data/mihomo/mihomo.log | tail -20'`" + ` -> check remote logs

## Config sources

- script: ` + "`~/projects/cicy-code/skills/us-spot-proxy`" + `
- local mihomo: ` + "`~/cicy-ai/db/mihomo.yaml`" + `
- remote data: ` + "`/data/mihomo/`" + ` on the spot instance
`
}
