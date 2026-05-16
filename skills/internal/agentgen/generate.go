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
	return []string{"agent-code-server", "agent-summary", "agent-webpage", "aliyun-cli", "cf", "cf-tunnel", "cping", "email", "frp-client", "frp-server", "globalApiToken", "google", "cicy-ssh", "cicy-agent", "cicy-mihomo", "us-spot-proxy", "proxy_ssh", "us-spot-dev", "hk-spot-dev"}
}

func canonicalCodexSkillName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "agent-code-server", "agentcodeserver", "agent_code_server", "code-server", "codeserver":
		return "agent-code-server"
	case "agent-summary", "agentsummary", "agent_summary":
		return "agent-summary"
	case "agent-webpage", "agentwebpage", "agent_webpage":
		return "agent-webpage"
	case "cf":
		return "cf"
	case "cf-tunnel":
		return "cf-tunnel"
	case "cping":
		return "cping"
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
	case "aliyun-cli", "aliyun_cli", "aliyuncli", "aliyun":
		return "aliyun-cli"
	case "email", "email-sender", "emailsender", "mail":
		return "email"
	case "us-spot-dev", "usspotdev", "us_spot_dev":
		return "us-spot-dev"
	case "hk-spot-dev", "hkspotdev", "hk_spot_dev":
		return "hk-spot-dev"
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
	case "cf":
		return generateCodexCF(targetRoot)
	case "cf-tunnel":
		return generateCodexCFTunnel(targetRoot)
	case "cping":
		return generateCodexCPing(targetRoot)
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
	case "aliyun-cli":
		return generateCodexAliyunCLI(targetRoot)
	case "email":
		return generateCodexEmail(targetRoot)
	case "us-spot-dev":
		return generateCodexUSSpotDev(targetRoot)
	case "hk-spot-dev":
		return generateCodexHKSpotDev(targetRoot)
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

func generateCodexCF(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "cf")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderCFSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderCFHelp()); err != nil {
		return err
	}
	tools := renderCFCommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
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

func generateCodexAliyunCLI(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "aliyun-cli")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderAliyunCLISkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderAliyunCLIHelp()); err != nil {
		return err
	}
	tools := renderAliyunCLICommands()
	if err := writeText(filepath.Join(refsDir, "tools.md"), tools); err != nil {
		return err
	}
	return writeText(filepath.Join(refsDir, "commands.md"), tools)
}

func generateCodexEmail(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "email")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderEmailSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderEmailHelp()); err != nil {
		return err
	}
	tools := renderEmailCommands()
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
description: Use the local google CLI wrapper for Gmail, Sheets, Drive, and Calendar on this host. ` + "`google login`" + ` runs an OAuth flow via oauth-flow.cicy-ai.com (a code-relay Worker that never sees the user's client_secret or tokens).
---

# Google Workspace

Local `+"`google`"+` wrapper for Gmail / Sheets / Drive / Calendar. All credentials live in two files on this host (chmod 600):

- `+"`~/cicy-ai/db/google_oauth_client.json`"+`  — `+"`{client_id, client_secret}`"+` (you create this once)
- `+"`~/cicy-ai/db/google.json`"+`               — `+"`{refresh_token, ...}`"+` (created by `+"`google login`"+`)

## Hard rules — sensitive data

1. **NEVER cat / Read / grep / print** either file above. The wrapper is the only thing that should touch them.
2. **NEVER ask the user to paste client_secret, refresh_token, or any auth code into chat.** They go straight from Google → OAuth client config file → wrapper.
3. The OAuth flow uses `+"`https://oauth-flow.cicy-ai.com`"+` as a code relay. The Worker only briefly holds the single-use authorization code (10 min TTL); it does NOT see client_secret or tokens. Token exchange happens locally on this host.
4. Do not invent client IDs, secrets, or refresh tokens — only what the user produces in their own Google Cloud Console.

## Scope

- **OAuth setup / re-authorization** (`+"`google login`"+`, `+"`google status`"+`) — when the user asks to "connect Google", "authorize", "log in", or any Google API call fails with an auth error
- Gmail inbox listing, reading, sending, verification-code watching
- Google Sheets read / write / append / create
- Google Drive list / upload / download / quota
- Google Calendar list / events / create

## OAuth Setup — the full flow

Run `+"`google login`"+` and let its stdout drive the next step. It self-detects three states:

### State 1 — No OAuth client yet (first run)

`+"`google login`"+` prints exact steps. Walk the user through them one at a time:

1. Open `+"`https://console.cloud.google.com/apis/credentials`"+` in their browser
   (signed into the Google account they want to authorize)
2. If prompted, configure the OAuth consent screen first (User Type: External, app name = anything personal)
3. Click **Create credentials → OAuth client ID**
4. **Application type: "Web application"** (Desktop / TV won't work — the redirect URI requires Web)
5. Under **Authorized redirect URIs**, click ADD URI and paste exactly:
   `+"`https://oauth-flow.cicy-ai.com/callback`"+`
6. Click Create. The dialog shows **Client ID** and **Client Secret**.
7. Have the user paste BOTH back to you, then write them to the file:
   `+"`cat > ~/cicy-ai/db/google_oauth_client.json <<EOF`"+`
   `+"`{\"client_id\":\"<paste-id>\",\"client_secret\":\"<paste-secret>\"}`"+`
   `+"`EOF`"+`
   `+"`chmod 600 ~/cicy-ai/db/google_oauth_client.json`"+`
8. Re-run `+"`google login`"+` — it advances to State 2.

### State 2 — Client configured, not yet authorized

`+"`google login`"+` generates a session id and prints a one-shot URL:

`+"`https://oauth-flow.cicy-ai.com/start?session=...&client_id=...&scopes=...`"+`

Tell the user: **open that URL in your browser**. They'll see Google's consent screen, click Allow, and the page will say "Success — you can close this tab."

Meanwhile the wrapper polls `+"`oauth-flow.cicy-ai.com/poll`"+` every 2 seconds. When it sees the code, it exchanges it locally (with the client_secret) for a refresh_token and writes it to `+"`~/cicy-ai/db/google.json`"+`. Final line of stdout is `+"`✓ authorized as <email>`"+`.

### State 3 — Already authorized

`+"`google login`"+` prints the connected email and exits. To switch accounts, delete `+"`~/cicy-ai/db/google.json`"+` and re-run `+"`google login`"+`.

## Rules

1. Prefer the local wrapper commands first.
2. For unfamiliar subcommands, run `+"`google help`"+` or `+"`google <service> help`"+`.
3. Use the real token configured on the host — do not mock Google responses.
4. Report the concrete command result back to the user.
5. Re-run `+"`google login`"+` after each user action and let its stdout drive the next step. Don't skip ahead.

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
	return fmt.Sprintf(`# Google Workspace Help

## Command

- primary: `+"`google`"+`
- credentials: `+"`~/cicy-ai/db/google_oauth_client.json`"+` (client_id + client_secret) and `+"`~/cicy-ai/db/google.json`"+` (refresh_token). Both chmod 600. **Never read or print either file.**

## OAuth flow at a glance

1. `+"`google status`"+` — check current auth state.
2. `+"`google login`"+` — runs the OAuth flow via `+"`oauth-flow.cicy-ai.com`"+`:
   - if no client config: prints exact steps to create a Web-application OAuth client and add `+"`https://oauth-flow.cicy-ai.com/callback`"+` as a redirect URI
   - if client configured but not authorized: prints a one-shot `+"`oauth-flow.cicy-ai.com/start?...`"+` URL — the user opens it, authorizes, and the wrapper polls + completes the exchange locally
   - if already authorized: prints the connected email
3. After `+"`✓ authorized as <email>`"+`, all `+"`google <service>`"+` subcommands work.

**The Worker only sees the auth code (10 min, single-use). It never sees client_secret or refresh_token. Token exchange happens locally.**

## Quick Start

- check usage:        `+"`google help`"+`
- check auth status:  `+"`google status`"+`
- start OAuth setup:  `+"`google login`"+`
- gmail shortcuts:    `+"`google gmail help`"+`
- list recent mail:   `+"`google gmail list 5`"+`
- list spreadsheets:  `+"`google sheets list`"+`
- list drive files:   `+"`google drive list`"+`
- list calendars:     `+"`google calendar list`"+`

## Rules — sensitive data

- Never `+"`cat`"+` / `+"`Read`"+` / `+"`grep`"+` / print `+"`~/cicy-ai/db/google_oauth_client.json`"+` or `+"`~/cicy-ai/db/google.json`"+`.
- Never ask the user to paste client_secret, refresh_token, or any auth code into chat.
- The wrapper is the only thing that should touch credentials. Use the real token; do not mock responses.

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

func renderCFSkill() string {
	return `---
name: cf
description: Secure Cloudflare API wrapper. Use ` + "`cf curl`" + ` to call any Cloudflare API endpoint — the tool injects the api_token so the agent never sees it. Config lives in ~/cicy-ai/db/cf.json (chmod 600).
---

# Cloudflare API (cf)

> **Wrapper command:** ` + "`cf`" + `. Subcommands: ` + "`config`" + ` / ` + "`status`" + ` / ` + "`curl`" + `.
> ` + "`cf curl`" + ` injects ` + "`Authorization: Bearer <api_token>`" + ` into every request — the agent never sees the raw token.

## Security: hard rules

- **NEVER cat / Read / grep / print** ` + "`~/cicy-ai/db/cf.json`" + `. The api_token is a user secret.
- When credentials are missing, run ` + "`cf config`" + `. It auto-creates a placeholder (chmod 600) and opens it in code-server. **Do not ask the user to paste the api_token into chat.**
- Never construct a raw ` + "`curl`" + ` command with ` + "`-H \"Authorization: Bearer ...\"`" + ` using a token you read from the file. Use ` + "`cf curl`" + ` instead.
- ` + "`cf status`" + ` masks the api_token — trust its output.

## Config shape (illustrative — do not Read the live file)

` + "```json" + `
{
  "api_token": "<paste-your-cloudflare-api-token-here>",
  "account_id": "<paste-your-cloudflare-account-id-here>"
}
` + "```" + `

Create the token at https://dash.cloudflare.com/profile/api-tokens.
Use the **"Edit zone DNS"** template for DNS-only work, or create a custom token with the scopes your task requires.

## Bootstrap flow

1. ` + "`cf status`" + ` — check whether config is ready.
2. ` + "`cf config`" + ` — opens the placeholder JSON in code-server. Walk the user through the Cloudflare dashboard; **never ask them to paste the token into chat**.
3. ` + "`cf curl GET /zones`" + ` — verify access by listing zones.

## Using cf curl

` + "`cf curl <METHOD> <PATH> [json-body]`" + `

- ` + "`PATH`" + ` is relative to ` + "`https://api.cloudflare.com/client/v4`" + ` — leading ` + "`/`" + ` optional.
- ` + "`json-body`" + ` is passed as ` + "`-d`" + ` to curl. Quote it to avoid shell word-splitting.
- Output is the raw Cloudflare JSON response — parse with ` + "`jq`" + ` as needed.

### Common patterns

` + "```sh" + `
# List zones
cf curl GET /zones | jq '.result[] | {id, name}'

# List DNS records for a zone
cf curl GET /zones/ZONE_ID/dns_records | jq '.result[] | {id, type, name, content}'

# Add an A record
cf curl POST /zones/ZONE_ID/dns_records \
  '{"type":"A","name":"sub.example.com","content":"1.2.3.4","ttl":1,"proxied":false}'

# Update a record
cf curl PATCH /zones/ZONE_ID/dns_records/RECORD_ID \
  '{"content":"5.6.7.8"}'

# Delete a record
cf curl DELETE /zones/ZONE_ID/dns_records/RECORD_ID

# List Cloudflare Tunnel configs
cf curl GET /accounts/ACCOUNT_ID/cfd_tunnel
` + "```" + `

## Rules

1. ` + "`cf curl`" + ` is the only way to call the Cloudflare API. Do not use raw ` + "`curl`" + ` with the token.
2. If ` + "`status`" + ` says missing or placeholder, run ` + "`cf config`" + ` — never ask for the token in chat.
3. Always check ` + "`\"success\": true`" + ` in the response before reporting success.
4. Zone IDs and record IDs come from API responses — never guess them.

## Help

Read [help.md](./references/help.md) for the bare command list and common examples.

## Tools

Read [tools.md](./references/tools.md) for the subcommand reference.
`
}

func renderCFHelp() string {
	return `# Cloudflare API Help

## Command

- wrapper: ` + "`cf`" + ` (subcommands: ` + "`config`" + ` / ` + "`status`" + ` / ` + "`curl`" + `)
- base URL: ` + "`https://api.cloudflare.com/client/v4`" + `

## Bootstrap

1. ` + "`cf status`" + ` — if missing or placeholder, continue.
2. ` + "`cf config`" + ` — auto-creates ` + "`~/cicy-ai/db/cf.json`" + ` (chmod 600) and opens it in code-server.
   - ` + "`api_token`" + ` — create at https://dash.cloudflare.com/profile/api-tokens
   - ` + "`account_id`" + ` — visible in the Cloudflare dashboard URL after login
   - **Do not ask the user to paste the token into chat.**
3. ` + "`cf curl GET /zones`" + ` — verify access.

## Security reminder

- Never ` + "`cat`" + ` / ` + "`Read`" + ` / ` + "`grep`" + ` ` + "`~/cicy-ai/db/cf.json`" + `.
- Never pass the raw token to ` + "`curl -H \"Authorization: Bearer ...\"`" + `. Use ` + "`cf curl`" + `.

## curl usage

` + "`cf curl <METHOD> <PATH> [json-body]`" + `

PATH is relative to ` + "`https://api.cloudflare.com/client/v4`" + `. Output is raw JSON.

## Common examples

` + "```sh" + `
# Zones
cf curl GET /zones | jq '.result[] | {id, name}'

# DNS records
cf curl GET /zones/ZONE_ID/dns_records | jq '.result[] | {id, type, name, content}'

# Add record
cf curl POST /zones/ZONE_ID/dns_records \
  '{"type":"A","name":"sub.example.com","content":"1.2.3.4","ttl":1,"proxied":false}'

# Update record
cf curl PATCH /zones/ZONE_ID/dns_records/RECORD_ID '{"content":"new-ip"}'

# Delete record
cf curl DELETE /zones/ZONE_ID/dns_records/RECORD_ID

# Purge cache
cf curl POST /zones/ZONE_ID/purge_cache '{"purge_everything":true}'

# Workers / account resources
cf curl GET /accounts/ACCOUNT_ID/workers/scripts
` + "```" + `

## Response shape

Every Cloudflare API response:
` + "```json" + `
{"success": true/false, "result": ..., "errors": [], "messages": []}
` + "```" + `
Always check ` + "`success`" + ` before proceeding.

## More

Read [tools.md](./references/tools.md) for the full subcommand table.
`
}

func renderCFCommands() string {
	return `# Cloudflare API Commands

| Command | What it does |
|---------|--------------|
| ` + "`cf config`" + ` | Open ~/cicy-ai/db/cf.json in code-server (auto-creates placeholder) |
| ` + "`cf status`" + ` | Show config state (api_token masked) |
| ` + "`cf curl GET /zones`" + ` | List all zones in the account |
| ` + "`cf curl GET /zones/ZONE_ID/dns_records`" + ` | List DNS records for a zone |
| ` + "`cf curl POST /zones/ZONE_ID/dns_records '<json>'`" + ` | Create a DNS record |
| ` + "`cf curl PATCH /zones/ZONE_ID/dns_records/RECORD_ID '<json>'`" + ` | Update a DNS record |
| ` + "`cf curl DELETE /zones/ZONE_ID/dns_records/RECORD_ID`" + ` | Delete a DNS record |
| ` + "`cf curl POST /zones/ZONE_ID/purge_cache '{\"purge_everything\":true}'`" + ` | Purge all zone cache |
| ` + "`cf curl GET /accounts/ACCOUNT_ID/cfd_tunnel`" + ` | List Cloudflare Tunnels |
| ` + "`cf curl GET /accounts/ACCOUNT_ID/workers/scripts`" + ` | List Workers scripts |
| ` + "`cf curl GET /accounts/ACCOUNT_ID/pages/projects`" + ` | List Pages projects |

**Security**: never use raw ` + "`curl -H \"Authorization: Bearer ...\"`" + ` with the token — always use ` + "`cf curl`" + `.
`
}

func renderCFTunnelSkill() string {
	return `---
name: cf-tunnel
description: Manage Cloudflare Tunnel routes and DNS records on this host. Credentials live in ~/cicy-ai/db/cf.json and must never be read by the agent. Use cf-tunnel config to bootstrap and cf-tunnel status to verify.
---

# Cloudflare Tunnel

> **Wrapper command:** ` + "`cf-tunnel`" + `. Subcommands: ` + "`config`" + ` / ` + "`status`" + ` / ` + "`daemon`" + ` / ` + "`list`" + ` / ` + "`add`" + ` / ` + "`del`" + `.
> Credentials live in ` + "`~/cicy-ai/db/cf.json`" + ` (chmod 600). The wrapper reads them — the agent never sees them.

## Credentials: hard rules

- **NEVER cat / Read / grep / print** ` + "`~/cicy-ai/db/cf.json`" + `. The api_token is a user secret.
- When config is missing or a placeholder, run ` + "`cf-tunnel config`" + `. It auto-creates a placeholder JSON and opens it in code-server. **Do not ask the user to paste the api_token into chat.**
- ` + "`status`" + ` masks the api_token and never prints the full value; trust its output.

## Config shape (illustrative — do not Read the live file)

` + "```json" + `
{
  "prod": {
    "api_token":  "<paste-your-cloudflare-api-token-here>",
    "account_id": "<paste-your-cloudflare-account-id-here>",
    "tunnel_id":  "<paste-your-cloudflare-tunnel-id-here>",
    "domain":     "<paste-your-domain-here>",
    "zone_id":    "<paste-your-cloudflare-zone-id-here>"
  }
}
` + "```" + `

A ` + "`dev`" + ` block is optional; use ` + "`CF_ENV=dev cf-tunnel ...`" + ` to target it.

## Bootstrap flow

1. ` + "`cf-tunnel status`" + ` — check whether config and daemon are ready.
2. ` + "`cf-tunnel config`" + ` — opens ` + "`~/cicy-ai/db/cf.json`" + ` in code-server (auto-creates a placeholder if missing). Walk the user through filling in the five fields. **Never ask them to paste api_token into chat.**
3. ` + "`cf-tunnel daemon install`" + ` — fetches the tunnel connector token from the CF API, installs the ` + "`cloudflared`" + ` binary if missing, installs and starts it as a systemd service. Run this once per host.
4. ` + "`cf-tunnel list`" + ` — verify connectivity; lists current tunnel routes.
5. ` + "`cf-tunnel add 8080`" + ` — add a route for port 8080; hostname will be ` + "`g-8080.<domain>`" + `.

### Fields the user must provide (the agent cannot obtain these)

- **api_token** — Cloudflare API token with *Edit Cloudflare Tunnel* + *Zone DNS Edit* permissions.
  Create at: Cloudflare dashboard → Profile → API Tokens → Create Token → template "Edit Cloudflare Tunnel".
- **account_id** — visible in the URL when you're on the Cloudflare dashboard home (` + "`/accounts/<id>`" + `).
- **tunnel_id** — ID of the existing tunnel (not the name). Find it in Zero Trust → Networks → Tunnels → select tunnel → Overview tab.
- **domain** — base domain for route hostnames (e.g. ` + "`example.com`" + `); routes become ` + "`g-<port>.example.com`" + `.
- **zone_id** — Cloudflare Zone ID for that domain (right sidebar of the domain overview page).

## Scope

Use this skill when the task involves:

- checking whether cloudflared is installed and running as a service
- installing or managing the cloudflared daemon on this host
- listing current Cloudflare tunnel routes for this host
- adding one or more ` + "`g-<port>.<domain>`" + ` tunnel hostnames that map to local ports
- deleting existing tunnel routes and DNS records
- switching environments via ` + "`CF_ENV=dev`" + `

## Rules

1. The wrapper is the only thing that reads ` + "`~/cicy-ai/db/cf.json`" + `. You do not.
2. If ` + "`status`" + ` says missing or placeholder, run ` + "`cf-tunnel config`" + ` — never ask the user for the api_token in chat.
3. ` + "`cf-tunnel`" + ` manages routes and DNS only; do not manage the ` + "`cloudflared`" + ` process unless explicitly asked.
4. Report the exact hostname and port mapping results back to the user.

## Help

Read [help.md](./references/help.md) for the bare command list.

## Tools

Read [tools.md](./references/tools.md) for the full command reference.
`
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

# CiCy Agent

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

# CiCy SSH

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
	return `# Cloudflare Tunnel Help

## Command

- wrapper: ` + "`cf-tunnel`" + ` (subcommands: ` + "`config`" + ` / ` + "`status`" + ` / ` + "`list`" + ` / ` + "`add`" + ` / ` + "`del`" + `)
- config file: ` + "`~/cicy-ai/db/cf.json`" + ` (chmod 600, never read by agent)

## Quick Start

` + "```sh" + `
cf-tunnel status                          # check config
cf-tunnel config                          # bootstrap / open config in code-server
cf-tunnel list                            # list tunnel routes
cf-tunnel add 8080                        # add route for port 8080
cf-tunnel add 5174 8010 13000             # add multiple routes at once
cf-tunnel del 8080                        # remove route for port 8080
CF_ENV=dev cf-tunnel list                 # use the dev environment block
` + "```" + `

## Rules

- NEVER cat / Read / grep ` + "`~/cicy-ai/db/cf.json`" + ` — api_token is a secret
- If status says missing or placeholder, run ` + "`cf-tunnel config`" + ` and walk the user through it
- ` + "`cf-tunnel`" + ` manages route and DNS state only; it does not manage the ` + "`cloudflared`" + ` process
- Report exact hostname and port mappings back to the user

## More

- tool map: [tools.md](./tools.md)
`
}

func renderCPingHelp() string {
	return fmt.Sprintf(`# cping Help

## Command

- primary command: `+"`cping`"+`

## Quick Start

- ping a domain: `+"`cping your-domain.com`"+`
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
	return fmt.Sprintf(`# CiCy Agent Help

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
	return `# CiCy SSH Help

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

func renderAliyunCLISkill() string {
	return `---
name: aliyun-cli
description: Install and configure the official Aliyun CLI on this host. The ` + "`aliyun-cli`" + ` wrapper is bootstrap-only (install / config / status); for every real API call (ECS / VPC / RAM / OSS / …) use the native ` + "`aliyun`" + ` CLI directly. The CLI's native config at ~/.aliyun/config.json is the single source of truth — no intermediate JSON.
---

# Aliyun CLI

> **Two different commands. Pick the right one:**
>
> - ` + "`aliyun-cli`" + ` — **bootstrap wrapper only**. Three subcommands: ` + "`install`" + ` / ` + "`config`" + ` / ` + "`status`" + `. **Nothing else.**
> - ` + "`aliyun`" + ` — **the official Aliyun CLI**. Use this for every real API call: ECS, VPC, RAM, OSS, RDS, security groups, …
>
> If a task is "install / set up credentials / check setup state" → use ` + "`aliyun-cli`" + `.
> If a task is "do anything against the Aliyun API" → use ` + "`aliyun`" + ` directly.
> **The wrapper does NOT proxy ` + "`aliyun ecs ...`" + ` calls. Do not try ` + "`aliyun-cli ecs ...`" + `.**

The wrapper has exactly three jobs:

1. ` + "`install`" + ` — download the official ` + "`aliyun`" + ` binary into ` + "`~/.local/bin`" + `.
2. ` + "`config`" + ` — open the CLI's native config (` + "`~/.aliyun/config.json`" + `) in code-server so the user can fill in id/secret. Auto-creates a native-format placeholder if the file is missing.
3. ` + "`status`" + ` — report install state + active profile (AccessKey id masked).

There is intentionally **no intermediate JSON** at ` + "`~/cicy-ai/db/aliyun.json`" + ` and **no ` + "`apply`" + ` step**. ` + "`~/.aliyun/config.json`" + ` is the CLI's own native config and is the single source of truth — once the user fills it in, ` + "`aliyun`" + ` reads from it directly on every invocation.

## Credentials: hard rules

- **NEVER cat / Read / grep / print** ` + "`~/.aliyun/config.json`" + `. The contents are user secrets — the wrapper is the only thing that touches them.
- When credentials are missing, run ` + "`aliyun-cli config`" + `. It auto-creates a native-format placeholder (chmod 600, literal ` + "`<paste-your-...-here>`" + ` placeholders) and opens it in code-server. **Do not ask the user to paste the AccessKey id or secret into chat.**
- After the user saves the file, ` + "`aliyun`" + ` immediately picks it up — no ` + "`apply`" + ` step needed. Run ` + "`aliyun-cli status`" + ` to confirm.

## Native config shape (` + "`~/.aliyun/config.json`" + `, aliyun CLI's own format — never Read the live file)

` + "```json" + `
{
  "current": "default",
  "profiles": [
    {
      "name": "default",
      "mode": "AK",
      "access_key_id": "<paste-your-access-key-id-here>",
      "access_key_secret": "<paste-your-access-key-secret-here>",
      "region_id": "us-west-1",
      "output_format": "json",
      "language": "en"
    }
  ]
}
` + "```" + `

## Scope

Use this skill when the task involves:

- Installing the Aliyun CLI on a fresh host
- Bootstrapping credentials (` + "`config`" + ` → user fills in → done)
- Confirming the current binary version, profile, and (masked) AK in use
- Running any Aliyun API call (via plain ` + "`aliyun ...`" + ` directly)

## Rules

1. **Right tool for the job.** ` + "`aliyun-cli`" + ` = install/bootstrap only. ` + "`aliyun`" + ` = every real API call. Never try to invoke an Aliyun API through ` + "`aliyun-cli`" + ` — it has no such subcommand and will reject it.
2. If ` + "`aliyun`" + ` is not on PATH, run ` + "`aliyun-cli install`" + ` first.
3. If ` + "`status`" + ` shows the config is missing or has placeholder values, run ` + "`aliyun-cli config`" + ` and let the user fill it in via code-server. Do not ask for credentials in chat.
4. For real API work, call ` + "`aliyun`" + ` directly — e.g. ` + "`aliyun ecs DescribeInstances --region us-west-1`" + `. Do not wrap those calls inside ` + "`aliyun-cli`" + `.
5. Never echo the access-key id or secret. ` + "`status`" + ` masks the id and never prints the secret — trust the wrapper's output, do not re-read the file yourself.

## Help

Read [help.md](./references/help.md) first for quick usage.

## Tools

Read [tools.md](./references/tools.md) for the wrapper's subcommands.
`
}

func renderAliyunCLIHelp() string {
	return `# Aliyun CLI Help

## Two commands — don't confuse them

| Command | Purpose | Subcommands |
|---|---|---|
| ` + "`aliyun-cli`" + ` | **Bootstrap only** — install binary, edit config, report status | ` + "`install`" + ` / ` + "`config`" + ` / ` + "`status`" + ` |
| ` + "`aliyun`" + ` | **Native Aliyun CLI** — every real API call goes here | ` + "`ecs`" + ` / ` + "`vpc`" + ` / ` + "`ram`" + ` / ` + "`oss`" + ` / ` + "`rds`" + ` / … |

The aliyun CLI's own native config at ` + "`~/.aliyun/config.json`" + ` is the single source of truth — no middleware JSON, no ` + "`apply`" + ` step.

## Bootstrap flow

1. ` + "`aliyun-cli status`" + ` — confirms whether the binary is installed and whether the active profile has real credentials.
2. ` + "`aliyun-cli config`" + ` — opens ` + "`~/.aliyun/config.json`" + ` in code-server (auto-creates a native-format placeholder if missing). The user types the AccessKey id and secret directly into the ` + "`default`" + ` profile and saves. **Do not ask for the AK in chat.**
3. Done — every ` + "`aliyun ecs / vpc / ram ...`" + ` call reads from the file immediately, no apply needed.

## Examples

- list ECS instances: ` + "`aliyun ecs DescribeInstances --region us-west-1`" + `
- authorize a security group port: ` + "`aliyun ecs AuthorizeSecurityGroup --region us-west-1 --SecurityGroupId sg-... --IpProtocol tcp --PortRange 80/80 --SourceCidrIp 0.0.0.0/0`" + `

## Native config shape (` + "`~/.aliyun/config.json`" + ` — illustrative, never Read the live file)

` + "```json" + `
{
  "current": "default",
  "profiles": [
    {
      "name": "default",
      "mode": "AK",
      "access_key_id": "<paste-your-access-key-id-here>",
      "access_key_secret": "<paste-your-access-key-secret-here>",
      "region_id": "us-west-1",
      "output_format": "json",
      "language": "en"
    }
  ]
}
` + "```" + `

## Rules

- **Never** ` + "`cat`" + `, ` + "`Read`" + `, ` + "`grep`" + ` or otherwise print ` + "`~/.aliyun/config.json`" + `. The wrapper is the only component that touches it.
- The wrapper does **not** proxy ` + "`aliyun ecs ...`" + ` style calls — call the ` + "`aliyun`" + ` binary directly.
- Edits to the config take effect immediately (the CLI re-reads on every invocation). No ` + "`apply`" + ` step.
- ` + "`status`" + ` already masks the AK and never prints the secret; trust its output.

## More

Read [tools.md](./references/tools.md) for the bare command list.
`
}

func renderEmailSkill() string {
	return `---
name: email
description: Send transactional email from this host via Resend. The ` + "`email`" + ` wrapper is bootstrap + send; subcommands are ` + "`config`" + ` / ` + "`status`" + ` / ` + "`send`" + `. Credentials live in ~/cicy-ai/db/email.json and must never be read by the agent.
---

# Email Sender (Resend)

> **Wrapper command:** ` + "`email`" + `. Subcommands: ` + "`config`" + ` / ` + "`status`" + ` / ` + "`send`" + `.
> Backend is the [Resend](https://resend.com) transactional email API. The wrapper signs the request itself — the agent never sees the api_key.

## Credentials: hard rules

- **NEVER cat / Read / grep / print** ` + "`~/cicy-ai/db/email.json`" + `. The api_key is a user secret.
- When credentials are missing, run ` + "`email config`" + `. It auto-creates a placeholder JSON (id/secret literal ` + "`<paste-your-...-here>`" + ` strings) at ` + "`~/cicy-ai/db/email.json`" + ` (chmod 600) and opens it in code-server. **Do not ask the user to paste the api_key into chat.**
- After the user fills it in, the wrapper reads the file itself when ` + "`send`" + ` is called — you never need to.
- ` + "`status`" + ` masks the api_key and never prints the body; trust its output.

## Config shape (illustrative — do not Read the live file)

` + "```json" + `
{
  "api_key": "<paste-your-resend-api-key-here>",
  "from_address": "<paste-your-verified-from-address-here>",
  "default_to": ""
}
` + "```" + `

` + "`default_to`" + ` is optional; if set, ` + "`email send`" + ` without ` + "`--to`" + ` uses it.
The ` + "`from_address`" + ` must be from a domain you verified in Resend (or ` + "`onboarding@resend.dev`" + ` for quick testing).

## Bootstrap flow

1. ` + "`email status`" + ` — confirms whether the config file is ready.
2. ` + "`email config`" + ` — opens the bootstrap JSON in code-server (auto-creates a placeholder if missing). Walk the user through the signup steps below; do NOT ask them to paste the api_key into chat.
3. ` + "`email send --to <addr> --subject \"…\" --body \"…\"`" + ` — fire off a message.
4. Resend returns an ` + "`id`" + ` on success; the wrapper prints it.

### Requirements the user must satisfy (the agent cannot do these)

The user picks ONE of the two paths below. Walk them through the steps in chat; **never ask them to paste the api_key back to you** — they edit the file directly.

**Path 1 — Sandbox (no domain needed, mail only to themselves)**

1. Sign up at [resend.com/signup](https://resend.com/signup) (free, no credit card).
2. Create API key at [resend.com/api-keys](https://resend.com/api-keys) → copy ` + "`re_xxxxxxxx…`" + `.
3. In the opened ` + "`email.json`" + `:
   - ` + "`api_key`" + ` = the ` + "`re_xxx`" + ` value
   - ` + "`from_address`" + ` = ` + "`onboarding@resend.dev`" + `
4. **Limitation**: Resend's sandbox sender only delivers to the email used at signup. Any other recipient → 403.

**Path 2 — Production (own domain, mail anyone)**

1. Have a domain (Namecheap / Cloudflare / GoDaddy / any registrar).
2. Sign up at [resend.com/signup](https://resend.com/signup).
3. Add the domain at [resend.com/domains](https://resend.com/domains) → *Add Domain*. Resend displays 3 TXT records to add at the domain's DNS:
   - **SPF** (root, type=TXT) — ` + "`v=spf1 include:_spf.resend.com ~all`" + `. If the domain already has an SPF record, **merge** — keep existing includes and add ` + "`include:_spf.resend.com`" + ` (only one SPF record allowed per domain).
   - **DKIM** (host=` + "`resend._domainkey`" + `, type=TXT) — the long public-key string Resend gives.
   - **DMARC** (host=` + "`_dmarc`" + `, type=TXT) — ` + "`v=DMARC1; p=none; rua=mailto:<their-email>`" + `.
4. Wait 5–15 min for DNS to propagate; Resend's domain page must show "verified" (all three green).
5. Create API key at [resend.com/api-keys](https://resend.com/api-keys) → copy ` + "`re_xxxxxxxx…`" + `.
6. In ` + "`email.json`" + `:
   - ` + "`api_key`" + ` = the ` + "`re_xxx`" + ` value
   - ` + "`from_address`" + ` = any address on the verified domain (e.g. ` + "`noreply@your-domain.com`" + `)

Confirm: ` + "`email status`" + ` → ` + "`config: ready`" + `. If Gmail still bounces with SPF/DKIM errors after Path 2, the DNS records haven't fully propagated yet — wait longer or check with a DMARC analyzer.

## Body sources

In order of precedence:

- ` + "`--body-file <path>`" + ` — read plain-text body from a file
- ` + "`--body <text>`" + ` — inline plain-text body
- ` + "`--html <html>`" + ` — HTML body (mutually exclusive with ` + "`--body`" + `)
- piped stdin — if no ` + "`--body`" + `/` + "`--html`" + `/` + "`--body-file`" + ` and stdin is not a TTY, the wrapper reads stdin as plain-text body

## Rules

1. The wrapper is the only thing that touches ` + "`~/cicy-ai/db/email.json`" + `. You do not.
2. If ` + "`status`" + ` says missing or placeholder, run ` + "`email config`" + ` — never ask the user for the api_key in chat.
3. ` + "`from_address`" + ` must be a domain verified inside Resend. If sends 403 with a domain-verification error, tell the user; don't try to "fix" the from address.
4. Resend rate-limits free tier (~100 emails/day). Don't batch-blast.

## Help

Read [help.md](./references/help.md) for the bare command list and a typical session.

## Tools

Read [tools.md](./references/tools.md) for the wrapper's subcommands.
`
}

func renderEmailHelp() string {
	return `# Email Help

## Command

- wrapper: ` + "`email`" + ` (subcommands: ` + "`config`" + ` / ` + "`status`" + ` / ` + "`send`" + `)
- backend: Resend (https://api.resend.com/emails)

## Bootstrap flow

1. ` + "`email status`" + ` — if missing / placeholder, continue.
2. ` + "`email config`" + ` — auto-creates ` + "`~/cicy-ai/db/email.json`" + ` (chmod 600) and opens it in code-server. The user pastes the Resend api_key and a verified ` + "`from_address`" + ` into the file directly. Do not ask for the api_key in chat.
3. ` + "`email send --to user@example.com --subject \"hello\" --body \"world\"`" + ` — send a one-shot plain-text email.

### Getting Resend credentials (first-time users do this themselves)

**Sandbox path** (no domain — only mails the signup address):

1. [resend.com/signup](https://resend.com/signup) → free, no credit card.
2. [resend.com/api-keys](https://resend.com/api-keys) → *Create API Key* → copy ` + "`re_xxx…`" + ` into ` + "`api_key`" + `.
3. ` + "`from_address`" + ` = ` + "`onboarding@resend.dev`" + ` (sandbox; 403 if you try to send anywhere except your own signup email).

**Production path** (own domain — mail anyone):

1. Have a domain (any registrar).
2. [resend.com/signup](https://resend.com/signup), then [resend.com/domains](https://resend.com/domains) → *Add Domain*.
3. Resend gives 3 TXT records — add at the domain's DNS:
   - **SPF**: ` + "`v=spf1 include:_spf.resend.com ~all`" + ` (merge with any existing SPF — one record per domain).
   - **DKIM**: at host ` + "`resend._domainkey`" + `, value = public key string from Resend.
   - **DMARC**: at host ` + "`_dmarc`" + `, value = ` + "`v=DMARC1; p=none; rua=mailto:<your-email>`" + `.
4. Wait until Resend shows the domain "verified" (~5–15 min after DNS propagation).
5. [resend.com/api-keys](https://resend.com/api-keys) → copy ` + "`re_xxx…`" + ` into ` + "`api_key`" + `.
6. ` + "`from_address`" + ` = any address on the verified domain.

` + "`email config`" + ` prints both paths to the terminal whenever it creates the placeholder, so this is also visible from inside a session.

## Send flag reference

- ` + "`--to <addr>`" + ` — required (or ` + "`default_to`" + ` set in config)
- ` + "`--subject <text>`" + ` — required
- ` + "`--body <text>`" + ` — plain-text body
- ` + "`--body-file <path>`" + ` — read body from file
- ` + "`--html <html>`" + ` — HTML body (mutually exclusive with ` + "`--body`" + `)
- ` + "`--from <addr>`" + ` — override the configured ` + "`from_address`" + `

If neither ` + "`--body`" + `/` + "`--html`" + `/` + "`--body-file`" + ` is given and stdin is piped, the wrapper reads stdin as the plain-text body.

## Examples

- ` + "`email send --to a@x.com --subject \"ping\" --body \"hi from $(hostname)\"`" + `
- ` + "`echo \"build done at $(date)\" | email send --to me@x.com --subject \"build\"`" + `
- ` + "`email send --to me@x.com --subject \"report\" --body-file /tmp/report.txt`" + `
- ` + "`email send --to me@x.com --subject \"weekly\" --html \"<h1>Hi</h1><p>...</p>\"`" + `

## Rules

- Never ` + "`cat`" + ` / ` + "`Read`" + ` / ` + "`grep`" + ` ` + "`~/cicy-ai/db/email.json`" + `. The wrapper is the only thing that should touch it.
- The ` + "`from_address`" + ` must be a domain verified in Resend. ` + "`onboarding@resend.dev`" + ` works for dev/testing.
- Resend's free tier rate-limits to ~100 emails / day; budget accordingly.

## More

Read [tools.md](./references/tools.md) for the bare command list.
`
}

func renderEmailCommands() string {
	return `# Email Commands

| Command | What it does |
|---------|--------------|
| ` + "`email config`" + ` | Open the bootstrap JSON in code-server (auto-creates a placeholder when missing) |
| ` + "`email status`" + ` | Show config state (api_key masked) and current from_address |
| ` + "`email send --to <addr> --subject \"<text>\" --body \"<text>\"`" + ` | Send a plain-text email |
| ` + "`email send --to user@example.com --subject \"hello\" --body \"world\"`" + ` | Concrete send example |
| ` + "`email send --to <addr> --subject \"<text>\" --html \"<html>\"`" + ` | Send an HTML email |
| ` + "`email send --to <addr> --subject \"<text>\" --html \"<h1>Hi</h1><p>...</p>\"`" + ` | Concrete HTML example |
| ` + "`email send --to <addr> --subject \"<text>\" --body-file /tmp/report.txt`" + ` | Read the plain-text body from a file |
| ` + "`email send --to <addr> --subject \"<text>\" --from <addr>`" + ` | Override the configured from_address (must be a verified domain) |
| ` + "`echo \"build done at $(date)\" | email send --to <addr> --subject \"build\"`" + ` | Pipe stdin into the body |
`
}

func renderAliyunCLICommands() string {
	return `# Aliyun CLI Commands

| Command | What it does |
|---------|--------------|
| ` + "`aliyun-cli install`" + ` | Install the official aliyun CLI binary |
| ` + "`aliyun-cli config`" + ` | Open ~/.aliyun/config.json in code-server to enter credentials |
| ` + "`aliyun-cli status`" + ` | Show CLI version and active profile |
| ` + "`aliyun configure list`" + ` | List configured profiles |
| ` + "`aliyun ecs DescribeRegions`" + ` | List all Aliyun ECS regions |
| ` + "`aliyun ecs DescribeInstances --region us-west-1`" + ` | List ECS instances in a region |
| ` + "`aliyun ecs DescribeInstanceAttribute --InstanceId i-xxx --region us-west-1`" + ` | Show one ECS instance's full details |
| ` + "`aliyun ecs StartInstance --InstanceId i-xxx --region us-west-1`" + ` | Start an ECS instance |
| ` + "`aliyun ecs StopInstance --InstanceId i-xxx --region us-west-1`" + ` | Stop an ECS instance |
| ` + "`aliyun ecs RebootInstance --InstanceId i-xxx --region us-west-1`" + ` | Reboot an ECS instance |
| ` + "`aliyun ecs DescribeSecurityGroupAttribute --SecurityGroupId sg-xxx --region us-west-1`" + ` | List ingress rules of a security group |
| ` + "`aliyun ecs AuthorizeSecurityGroup --SecurityGroupId sg-xxx --region us-west-1 --IpProtocol tcp --PortRange 80/80 --SourceCidrIp 0.0.0.0/0`" + ` | Open one inbound port on a security group |
| ` + "`aliyun ecs RevokeSecurityGroup --SecurityGroupId sg-xxx --region us-west-1 --IpProtocol tcp --PortRange 80/80 --SourceCidrIp 0.0.0.0/0`" + ` | Revoke a previously-opened port rule |
| ` + "`aliyun ecs DescribeDisks --region us-west-1`" + ` | List ESSD / cloud disks in a region |
| ` + "`aliyun ecs DescribeSnapshots --region us-west-1`" + ` | List snapshots in a region |
| ` + "`aliyun ecs DescribeKeyPairs --region us-west-1`" + ` | List SSH key pairs in a region |
| ` + "`aliyun ecs DescribeAvailableResource --RegionId us-west-1 --DestinationResource InstanceType --SpotStrategy SpotAsPriceGo`" + ` | Show spot-available instance types in a region |
| ` + "`aliyun vpc DescribeVpcs --region us-west-1`" + ` | List VPCs in a region |
| ` + "`aliyun vpc DescribeEipAddresses --region us-west-1`" + ` | List elastic IP addresses in a region |
| ` + "`aliyun oss ls oss://your-bucket/`" + ` | List objects in an OSS bucket |
| ` + "`aliyun oss cp ./local.file oss://your-bucket/path/`" + ` | Upload a local file to OSS |
| ` + "`aliyun ram ListUsers`" + ` | List RAM (IAM) users |
`
}

func renderCFTunnelCommands() string {
	return `# Cloudflare Tunnel Commands

## Config

| Command | What it does |
|---------|--------------|
| ` + "`cf-tunnel config`" + ` | Open ~/cicy-ai/db/cf.json in code-server (auto-creates placeholder) |
| ` + "`cf-tunnel status`" + ` | Show config + daemon state (api_token / tunnel_token masked) |

## Daemon (cloudflared service)

| Command | What it does |
|---------|--------------|
| ` + "`cf-tunnel daemon`" + ` | Show cloudflared install and service status |
| ` + "`cf-tunnel daemon install`" + ` | Fetch tunnel token, install cloudflared binary if missing, install and start service |
| ` + "`cf-tunnel daemon uninstall`" + ` | Stop and remove the cloudflared service |
| ` + "`cf-tunnel daemon start`" + ` | Start the cloudflared service |
| ` + "`cf-tunnel daemon stop`" + ` | Stop the cloudflared service |
| ` + "`cf-tunnel daemon restart`" + ` | Restart the cloudflared service |
| ` + "`cf-tunnel daemon logs`" + ` | Show last 50 lines of cloudflared logs |
| ` + "`cf-tunnel daemon logs 200`" + ` | Show last 200 lines of cloudflared logs |
| ` + "`cf-tunnel daemon token`" + ` | Fetch and cache the tunnel connector token in cf.json |

## Routes

| Command | What it does |
|---------|--------------|
| ` + "`cf-tunnel list`" + ` | List configured tunnel routes and local port status |
| ` + "`cf-tunnel add <port>`" + ` | Add a tunnel route and DNS record for the given port |
| ` + "`cf-tunnel add 5174 8010 13000`" + ` | Add routes for multiple ports at once |
| ` + "`cf-tunnel del <port>`" + ` | Remove a tunnel route and DNS record by port |
| ` + "`CF_ENV=dev cf-tunnel list`" + ` | Use the dev environment block from cf.json |
| ` + "`CF_ENV=dev cf-tunnel add 5174`" + ` | Add a dev-environment route |
| ` + "`CF_ENV=dev cf-tunnel del 5174`" + ` | Remove a dev-environment route |
`
}

func renderCPingCommands() string {
	return `# cping Commands

| Command | What it does |
|---------|--------------|
| ` + "`cping baidu.com`" + ` | Probe latency to baidu.com from this host (China-edge sanity) |
| ` + "`cping google.com`" + ` | Probe Google reachability (cross-border check) |
| ` + "`cping github.com`" + ` | Probe GitHub reachability |
| ` + "`cping your-domain.com`" + ` | Probe your own domain or tunnel hostname |
| ` + "`cping cloudflare.com`" + ` | Probe Cloudflare's edge directly |
| ` + "`cping 8.8.8.8`" + ` | Probe an IP directly (skips DNS) |
| ` + "`cping 1.1.1.1`" + ` | Probe Cloudflare's public DNS |
`
}

func renderGlobalAPITokenCommands() string {
	return `# Global API Token Commands

| Command | What it does |
|---------|--------------|
| ` + "`globalApiToken show`" + ` | Print the current api_token from ~/cicy-ai/global.json |
| ` + "`globalApiToken refresh`" + ` | Generate a new api_token and write it back |
`
}

func renderFRPServerCommands() string {
	return `# FRP Server Commands

| Command | What it does |
|---------|--------------|
| ` + "`frp-server start`" + ` | Start frps in the background |
| ` + "`frp-server stop`" + ` | Stop the running frps |
| ` + "`frp-server restart`" + ` | Stop then start |
| ` + "`frp-server status`" + ` | Show pid, config path, bind address, dashboard address |
| ` + "`frp-server reload`" + ` | Hot-reload config (falls back to restart when needed) |
| ` + "`frp-server connections`" + ` | List active client connections |
| ` + "`frp-server clients`" + ` | Alias of connections |
| ` + "`frp-server logs`" + ` | Tail the last 100 log lines |
| ` + "`frp-server logs 500`" + ` | Tail the last 500 log lines |
| ` + "`frp-server logs -f`" + ` | Follow the log live |
| ` + "`frp-server start --config /path/to/frps.toml`" + ` | Start with a non-default config path |
| ` + "`frp-server start --bin /path/to/frps`" + ` | Start using a non-default frps binary |
| ` + "`frp-server raw -- version`" + ` | Pass arbitrary args straight to the underlying frps binary |
`
}

func renderFRPClientCommands() string {
	return `# FRP Client Commands

| Command | What it does |
|---------|--------------|
| ` + "`frp-client start`" + ` | Start frpc in the background |
| ` + "`frp-client stop`" + ` | Stop the running frpc |
| ` + "`frp-client restart`" + ` | Stop then start |
| ` + "`frp-client status`" + ` | Show pid, config path, server addr, active proxies |
| ` + "`frp-client reload`" + ` | Hot-reload config (falls back to restart when needed) |
| ` + "`frp-client connections`" + ` | List currently active proxy connections |
| ` + "`frp-client logs`" + ` | Tail the last 100 log lines |
| ` + "`frp-client logs 500`" + ` | Tail the last 500 log lines |
| ` + "`frp-client logs -f`" + ` | Follow the log live |
| ` + "`frp-client start --config /path/to/frpc.toml`" + ` | Start with a non-default config path |
| ` + "`frp-client start --bin /path/to/frpc`" + ` | Start using a non-default frpc binary |
| ` + "`frp-client raw -- version`" + ` | Pass arbitrary args straight to the frpc binary |
| ` + "`ssh <host> '~/.local/bin/frpc status -c ~/.config/frp/frpc.toml'`" + ` | Inspect a remote machine's frpc state over SSH |
| ` + "`ssh <host> 'tail -100 ~/.local/frp/frpc.log'`" + ` | Tail the remote frpc log over SSH |
| ` + "`ssh <host> 'sudo systemctl restart frpc-cicy-$USER.service'`" + ` | Restart the remote frpc service unit |
| ` + "`ssh <host> 'launchctl kickstart -k \"gui/$(id -u)/com.cicy.frpc\"'`" + ` | Restart frpc on a macOS host via launchd |
`
}

func renderTMCommands() string {
	return `# CiCy Agent Commands

| Command | What it does |
|---------|--------------|
| ` + "`cicy-agent ls`" + ` | List all tmux panes |
| ` + "`cicy-agent tree`" + ` | Show panes/windows in a tree view |
| ` + "`cicy-agent windows`" + ` | List tmux windows |
| ` + "`cicy-agent capture w-10001`" + ` | Capture the content of pane w-10001 |
| ` + "`cicy-agent capture <pane>`" + ` | Capture any pane's content |
| ` + "`cicy-agent msg w-10001 \"hello\"`" + ` | Send text to a pane (no Enter) |
| ` + "`cicy-agent msg <pane> \"<text>\"`" + ` | Send arbitrary text to any pane |
| ` + "`cicy-agent msg_wait w-10001 \"hello\" 30`" + ` | Send text and wait up to 30s for a reply |
| ` + "`cicy-agent send-keys w-10001 Enter`" + ` | Send a raw keystroke (e.g. Enter, C-c) |
| ` + "`cicy-agent send-keys <pane> <keys>`" + ` | Send any tmux key sequence |
| ` + "`cicy-agent create my-pane`" + ` | Create a new pane |
| ` + "`cicy-agent clear w-10001`" + ` | Clear a pane's terminal |
| ` + "`cicy-agent restart`" + ` | Restart cicy-agent itself |
| ` + "`cicy-agent --node dev ls`" + ` | Run ` + "`ls`" + ` against the ` + "`dev`" + ` remote node |
| ` + "`cicy-agent --node dev capture w-10001`" + ` | Capture a remote node's pane |
| ` + "`TM_NODE=dev cicy-agent ls`" + ` | Env-var form to select a remote node |
| ` + "`TM_API_BASE=http://127.0.0.1:8021 cicy-agent ls`" + ` | Point at a specific api host:port directly |
`
}

func renderSSHCommands() string {
	return `# CiCy SSH Commands

| Command | What it does |
|---------|--------------|
| ` + "`ssh-list list`" + ` | List all Host entries from ~/.ssh/config with HostName, user, port |
| ` + "`ssh-list list --short`" + ` | Print alias names only (one per line) |
| ` + "`grep -E '^Host ' ~/.ssh/config`" + ` | Raw list of every Host alias configured |
| ` + "`awk '/^Host /{h=$2}/HostName/{print h\" -> \"$2}' ~/.ssh/config`" + ` | Show alias → HostName mapping |
| ` + "`ssh -G <alias>`" + ` | Print the effective config for an alias (resolved) |
| ` + "`ssh <alias>`" + ` | Open an interactive session to a host |
| ` + "`ssh <alias> '<command>'`" + ` | Run one command remotely and return output |
| ` + "`ssh <alias> 'uname -a; uptime'`" + ` | Quick remote info dump |
| ` + "`ssh <alias> 'tail -100 /var/log/syslog'`" + ` | Tail a remote log file |
| ` + "`ssh -J <jump-host> <alias>`" + ` | Connect through a one-off jump host |
| ` + "`ssh -p <port> user@host`" + ` | Connect to a host on a non-default port |
| ` + "`ssh -F ~/.ssh/config <alias>`" + ` | Force a specific config file |
| ` + "`ssh-copy-id <alias>`" + ` | Push your public key to a host's authorized_keys |
| ` + "`scp <alias>:/remote/path ./local/path`" + ` | Download a file from a remote host |
| ` + "`scp ./local/file <alias>:/remote/path/`" + ` | Upload a file to a remote host |
| ` + "`rsync -av ./local/ <alias>:/remote/`" + ` | Sync a directory to a remote host |
`
}

func renderAgentCodeServerTools() string {
	return `# Agent Code Server Commands

| Command | What it does |
|---------|--------------|
| ` + "`agent-code-server ping [page_client_id]`" + ` | Confirm the code-server extension is online (optionally for one tab) |
| ` + "`agent-code-server list`" + ` | Show connected page clients and ext-side WS status |
| ` + "`agent-code-server open <path>`" + ` | Open a file in the editor (see ` + "`open` syntax" + ` below) |
| ` + "`agent-code-server active`" + ` | JSON: path/language/line/column of the focused editor |
| ` + "`agent-code-server tabs`" + ` | JSON: every open file tab (path, label, isActive, isDirty, group) |

## ` + "`open`" + ` syntax

` + "`<path>`" + ` accepts an absolute path or a ` + "`file://`" + ` URI, with optional line/column or range suffix:

| Form | Effect |
|------|--------|
| ` + "`open /abs/path/foo.ts`" + ` | open the file |
| ` + "`open /abs/path/foo.ts:42`" + ` | open at line 42 |
| ` + "`open /abs/path/foo.ts:42:7`" + ` | open at line 42, column 7 |
| ` + "`open /abs/path/foo.ts:42:7-50:1`" + ` | open and select the range 42:7 → 50:1 |
| ` + "`open file:///abs/path/foo.ts`" + ` | ` + "`file://`" + ` URI form |
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

| Command | What it does |
|---------|--------------|
| ` + "`agent-summary w-10019`" + ` | Plain-text summary of an agent's conversation |
| ` + "`agent-summary <agent-id>`" + ` | Same, for any worker id |
| ` + "`agent-summary w-10019 --stats`" + ` | Token usage and message count only |
| ` + "`agent-summary <agent-id> --stats`" + ` | Stats for any worker |
| ` + "`agent-summary w-10019 --slim`" + ` | Slim conversation JSON (for piping to other tools) |
| ` + "`agent-summary w-10019 --text`" + ` | Structured plain-text dump (input for further AI processing) |
| ` + "`agent-summary w-10019 --ai`" + ` | AI-generated handoff document (uses default provider) |
| ` + "`agent-summary <agent-id> --ai`" + ` | AI handoff for any worker |
| ` + "`agent-summary w-10019 --ai --provider=deepseek`" + ` | AI summary via deepseek provider |
| ` + "`agent-summary w-10019 --ai --model=deepseek-chat`" + ` | AI summary with a specific model |
| ` + "`agent-summary w-10019 --ai --prompt=\"提炼到 5 个要点\"`" + ` | AI summary with a custom prompt |
| ` + "`agent-summary /path/to/current.json`" + ` | Summarize a specific snapshot file (skip worker-id lookup) |
`
}

func renderAgentWebpageTools() string {
	return `# Agent Webpage Commands

| Command | What it does |
|---------|--------------|
| ` + "`agent-webpage clients`" + ` | List currently connected webpage / chat clients |
| ` + "`agent-webpage ping`" + ` | Round-trip ping to the auto-selected webpage client |
| ` + "`agent-webpage ping <client_id>`" + ` | Ping a specific webpage client (e.g. web-abc123) |
| ` + "`agent-webpage ipc-ping`" + ` | Ping the desktop-side IPC bridge of the current webpage |
| ` + "`agent-webpage ipc-ping <client_id>`" + ` | IPC-ping a specific client |
| ` + "`agent-webpage exec-js 'document.title'`" + ` | Run JS in the webpage and return the result |
| ` + "`agent-webpage exec-js '<js>' <client_id>`" + ` | Run JS in a specific webpage client |
| ` + "`agent-webpage exec-js 'location.href' <client_id>`" + ` | Read a specific webpage's location |
| ` + "`agent-webpage current-active-agent-id`" + ` | Print ` + "`devStore.Workspace.activeCliPaneId`" + ` from the live webpage |
| ` + "`agent-webpage current-master-agent-id`" + ` | Print ` + "`devStore.Workspace.masterAgentId`" + ` |
| ` + "`agent-webpage current-active-agent-id <client_id>`" + ` | Same, targeting a specific client |
| ` + "`agent-webpage send <type> '<data_json>' <client_id>`" + ` | Send a custom WS event to a client and wait for any reply |
| ` + "`agent-webpage send <type> '<data_json>' <client_id> <expect_type>`" + ` | Send and wait for a specific reply type |
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
	return `# Cicy Mihomo Commands

| Command | What it does |
|---------|--------------|
| ` + "`cicy-mihomo install`" + ` | Download the platform-matching mihomo binary to ~/.local/bin/mihomo |
| ` + "`cicy-mihomo start`" + ` | Start mihomo in the background |
| ` + "`cicy-mihomo stop`" + ` | Stop the running mihomo |
| ` + "`cicy-mihomo restart`" + ` | Stop then start |
| ` + "`cicy-mihomo reload`" + ` | Hot-reload config via the controller API (no restart) |
| ` + "`cicy-mihomo status`" + ` | Show pid, binary path, config path, log path, port, controller addr |
| ` + "`cicy-mihomo template`" + ` | Print a starter mihomo.yaml to stdout |
| ` + "`cicy-mihomo gen-config`" + ` | Write the starter config to ~/cicy-ai/db/mihomo.yaml when missing |
| ` + "`cicy-mihomo show-config`" + ` | Print the current config (secrets masked) |
| ` + "`cicy-mihomo logs`" + ` | Tail the last 100 log lines |
| ` + "`cicy-mihomo logs 500`" + ` | Tail the last 500 log lines |
| ` + "`cicy-mihomo logs -f`" + ` | Follow the log live |
| ` + "`cicy-mihomo test`" + ` | Time HTTP requests through every configured proxy node and report per-node latency |
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

| Command | What it does |
|---------|--------------|
| ` + "`us-spot-proxy`" + ` | Provision a spot instance and attach the persistent data disk |
| ` + "`us-spot-proxy --destroy`" + ` | Delete the spot instance (keeps the persistent disk) |
| ` + "`us-spot-proxy --destroy-all`" + ` | Delete BOTH the spot instance and the persistent disk |
| ` + "`cicy-mihomo reload`" + ` | After provisioning, reload local mihomo so it sees the new node |
| ` + "`cicy-mihomo test`" + ` | Speed-test all proxy nodes (including the new us-spot one) |
| ` + "`ssh us-spot-proxy 'tail -20 /data/mihomo/mihomo.log'`" + ` | Tail the remote mihomo log |
| ` + "`ssh us-spot-proxy 'systemctl status mihomo'`" + ` | Check the remote mihomo systemd unit |
| ` + "`ssh us-spot-proxy 'df -h /data'`" + ` | Check free space on the persistent data disk |
`
}

// ── US Spot Dev ──────────────────────────────────────────────────────────────

func generateCodexUSSpotDev(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "us-spot-dev")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderUSSpotDevSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderUSSpotDevHelp()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "tools.md"), renderUSSpotDevCommands()); err != nil {
		return err
	}
	return nil
}

func renderUSSpotDevSkill() string {
	return `---
name: us-spot-dev
description: Provision a US (Silicon Valley) Aliyun spot ECS dev box with a persistent ESSD data disk. Use when the user asks to spin up, rebuild, or destroy the US spot dev environment.
---

# US Spot Dev

Provisions a cheap, disposable US (us-west-1) Aliyun spot ECS instance backed by a **persistent 100 GB ESSD data disk** (` + "`us-spot-dev-data`" + `).

The split keeps everything that matters — ` + "`/home/cicy`" + `, Docker images, ` + "`~/cicy-ai`" + `, repos — on the disk. If the spot instance is reclaimed, re-run ` + "`us-spot-dev`" + ` to get a fresh box with your data intact.

## Scope

Use this skill when the task involves:

- spinning up or re-provisioning the US spot dev instance
- destroying the instance (while keeping the data disk)
- rebuilding and pushing the container image

## Rules

1. ` + "`us-spot-dev`" + ` (no args) provisions a new spot instance, attaches the persistent disk, starts Docker and the ` + "`us-spot-dev`" + ` container, then bootstraps cicy on a fresh disk.
2. The data disk (` + "`us-spot-dev-data`" + `) is **never deleted** by any ` + "`us-spot-dev`" + ` command; it survives ` + "`--destroy`" + `.
3. Use ` + "`--json`" + ` for scriptable / agent-driven flows.
4. Read [help.md](./references/help.md) for the full workflow and [tools.md](./references/tools.md) for the command reference.
`
}

func renderUSSpotDevHelp() string {
	return `# US Spot Dev Help

## What it is

A persistent-disk + spot-instance pattern for a cheap US dev box:

- **Persistent disk** ` + "`us-spot-dev-data`" + ` (100 GB ESSD, us-west-1a) — never deleted.
  Holds ` + "`/home/cicy`" + `, ` + "`/data/docker`" + ` (Docker data-root), repos, ` + "`~/cicy-ai`" + `, SSH state.
- **Spot instance** (` + "`ecs.e-c1m4.xlarge`" + `, us-west-1a) — disposable. Billed by the hour, may be reclaimed.

On re-provision the Docker image is reused from disk. On a fresh disk ` + "`us-spot-dev`" + ` pulls the pre-built image from Docker Hub.

## Typical workflow

` + "```sh" + `
# First time / after reclaim: provision
us-spot-dev

# Tear down instance when not needed (disk kept)
us-spot-dev --destroy

# After changing Dockerfile: push new image
us-spot-dev --push-image
` + "```" + `

## SSH access

After provisioning, ` + "`~/.ssh/config`" + ` is updated with a ` + "`us-spot-dev`" + ` host entry:

` + "```sh" + `
ssh us-spot-dev
` + "```" + `

The DNS hostname is derived from the Cloudflare tunnel config and updated automatically.

## Config

No config file required. Credentials come from ` + "`~/cicy-ai/global.json`" + ` (Aliyun AK/SK, Cloudflare token).
`
}

func renderUSSpotDevCommands() string {
	return `# US Spot Dev Commands

| Command | What it does |
|---------|--------------|
| ` + "`us-spot-dev`" + ` | Provision spot instance + attach persistent disk + start Docker container |
| ` + "`us-spot-dev --destroy`" + ` | Delete the spot instance (persistent disk is always kept) |
| ` + "`us-spot-dev --push-image`" + ` | Rebuild container image on the running box and push to registry |
| ` + "`us-spot-dev --json`" + ` | Same as above but emit JSON output (agent-friendly) |
`
}

// ── HK Spot Dev ──────────────────────────────────────────────────────────────

func generateCodexHKSpotDev(targetRoot string) error {
	skillDir := filepath.Join(targetRoot, "hk-spot-dev")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return err
	}
	if err := writeText(filepath.Join(skillDir, "SKILL.md"), renderHKSpotDevSkill()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "help.md"), renderHKSpotDevHelp()); err != nil {
		return err
	}
	if err := writeText(filepath.Join(refsDir, "tools.md"), renderHKSpotDevCommands()); err != nil {
		return err
	}
	return nil
}

func renderHKSpotDevSkill() string {
	return `---
name: hk-spot-dev
description: Provision an HK (Hong Kong) Aliyun spot ECS dev box with a persistent ESSD data disk. Use when the user asks to spin up, rebuild, or destroy the HK spot dev environment.
---

# HK Spot Dev

Provisions a cheap, disposable Hong Kong (cn-hongkong) Aliyun spot ECS instance backed by a **persistent 100 GB ESSD data disk** (` + "`hk-spot-dev-data`" + `).

The same persistent-disk pattern as ` + "`us-spot-dev`" + `: if the spot instance is reclaimed, re-run ` + "`hk-spot-dev`" + ` to restore access with data intact.

## Scope

Use this skill when the task involves:

- spinning up or re-provisioning the HK spot dev instance
- destroying the instance (while keeping the data disk)
- rebuilding and pushing the container image

## Rules

1. ` + "`hk-spot-dev`" + ` (no args) provisions a new spot instance, attaches the persistent disk, starts Docker and the ` + "`hk-spot-dev`" + ` container, then bootstraps cicy on a fresh disk.
2. The data disk (` + "`hk-spot-dev-data`" + `) is **never deleted** by any ` + "`hk-spot-dev`" + ` command; it survives ` + "`--destroy`" + `.
3. Use ` + "`--json`" + ` for scriptable / agent-driven flows.
4. Read [help.md](./references/help.md) for the full workflow and [tools.md](./references/tools.md) for the command reference.
`
}

func renderHKSpotDevHelp() string {
	return `# HK Spot Dev Help

## What it is

A persistent-disk + spot-instance pattern for a cheap HK dev box:

- **Persistent disk** ` + "`hk-spot-dev-data`" + ` (100 GB ESSD, cn-hongkong-d) — never deleted.
  Holds ` + "`/home/cicy`" + `, ` + "`/data/docker`" + ` (Docker data-root), repos, ` + "`~/cicy-ai`" + `, SSH state.
- **Spot instance** (` + "`ecs.u1-c1m8.large`" + `, cn-hongkong-d) — disposable. Billed by the hour, may be reclaimed.

On re-provision the Docker image is reused from disk. On a fresh disk ` + "`hk-spot-dev`" + ` pulls the pre-built image from Docker Hub.

## Typical workflow

` + "```sh" + `
# First time / after reclaim: provision
hk-spot-dev

# Tear down instance when not needed (disk kept)
hk-spot-dev --destroy

# After changing Dockerfile: push new image
hk-spot-dev --push-image
` + "```" + `

## SSH access

After provisioning, ` + "`~/.ssh/config`" + ` is updated with a ` + "`hk-spot-dev`" + ` host entry:

` + "```sh" + `
ssh hk-spot-dev
` + "```" + `

The DNS hostname is derived from the Cloudflare tunnel config and updated automatically.

## Config

No config file required. Credentials come from ` + "`~/cicy-ai/global.json`" + ` (Aliyun AK/SK, Cloudflare token).
`
}

func renderHKSpotDevCommands() string {
	return `# HK Spot Dev Commands

| Command | What it does |
|---------|--------------|
| ` + "`hk-spot-dev`" + ` | Provision spot instance + attach persistent disk + start Docker container |
| ` + "`hk-spot-dev --destroy`" + ` | Delete the spot instance (persistent disk is always kept) |
| ` + "`hk-spot-dev --push-image`" + ` | Rebuild container image on the running box and push to registry |
| ` + "`hk-spot-dev --json`" + ` | Same as above but emit JSON output (agent-friendly) |
`
}
