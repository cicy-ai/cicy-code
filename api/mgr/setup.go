package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Tool struct {
	Name       string
	Command    string
	InstallCmd string
	Required   bool
	Installed  bool
}

//go:embed .tmux.conf
var embeddedTmuxConf string

//go:embed .cicy_tmux.conf
var embeddedCicyTmuxConf string

//go:embed resources/cicy-code-server-bridge-0.0.4.vsix
var embeddedCodeServerBridgeVSIX []byte

//go:embed resources/MS-CEINTL.vscode-language-pack-zh-hans-1.110.0.vsix
var embeddedCodeServerZhHansVSIX []byte

//go:embed cicy_skills_assets.tar.gz
var embeddedCicySkillsTar []byte

var cicySkillsInstallOnce sync.Once

const (
	cicyDefaultPyPIMirror    = "https://pypi.tuna.tsinghua.edu.cn/simple"
	cicySkillsInstallLogFile = "cicy-skills-install.log"
	defaultGitHubProxy       = "https://gh-proxy.com/"
)

// 获取用户 shell 的 rc 文件路径
func shellRC() string {
	shell := os.Getenv("SHELL")
	if strings.Contains(shell, "zsh") {
		return "~/.zshrc"
	}
	return "~/.bashrc"
}

func expandHomePath(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func extendPATH() {
	home, _ := os.UserHomeDir()
	parts := []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".opencode", "bin"),
	}
	parts = append(parts, strings.Split(os.Getenv("PATH"), ":")...)
	seen := map[string]bool{}
	var filtered []string
	for _, part := range parts {
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		filtered = append(filtered, part)
	}
	os.Setenv("PATH", strings.Join(filtered, ":"))
}

func sudoPrefix() string {
	if runtime.GOOS == "darwin" {
		return ""
	}
	if os.Geteuid() == 0 {
		return ""
	}
	if _, err := exec.LookPath("sudo"); err == nil {
		return "sudo "
	}
	return ""
}

func npmGlobalInstallCmd(pkg string) string {
	return `mkdir -p "$HOME/.npm-global/bin" "$HOME/.npm-global/lib" "$HOME/.npm-global/lib/node_modules" && npm install -g --prefix "$HOME/.npm-global" ` + pkg
}

func preinstalledRuntimeInstallCmd(cmd string) string {
	if isContainerRuntime() {
		return ""
	}
	return cmd
}

func openClawInstallCmd() string {
	return npmGlobalInstallCmd("openclaw@latest")
}

func claudeInstallCmd() string {
	return npmGlobalInstallCmd("@anthropic-ai/claude-code@latest")
}

func codexInstallCmd() string {
	return npmGlobalInstallCmd("@openai/codex@latest")
}

func opencodeInstallCmd() string {
	return npmGlobalInstallCmd("opencode-ai@latest")
}

func cursorInstallCmd() string {
	return preinstalledRuntimeInstallCmd("curl https://cursor.com/install -fsS | bash")
}

func cicyInstallCmd() string {
	return npmGlobalInstallCmd("cicy-claude@latest")
}

func kiroCliInstallCmd() string {
	return "__cicy_install_kiro_cli_with_progress"
}

func cicyWechatInstallCmd() string {
	return npmGlobalInstallCmd("cicy-wechat@latest")
}

func cicyFeishuInstallCmd() string {
	return npmGlobalInstallCmd("cicy-feishu@latest")
}

func packageInstallCmd(pkg string) string {
	if runtime.GOOS == "darwin" {
		return "brew install " + pkg
	}
	prefix := sudoPrefix()
	return prefix + "apt-get update && " + prefix + "apt-get install -y " + pkg
}

func opensshInstallCmd() string {
	if runtime.GOOS == "darwin" {
		return "brew install openssh"
	}
	prefix := sudoPrefix()
	return prefix + "apt-get update && " + prefix + "apt-get install -y openssh-client"
}

func nodeInstallCmd() string {
	if runtime.GOOS == "darwin" {
		return "brew install node"
	}
	prefix := sudoPrefix()
	return "curl -fsSL https://deb.nodesource.com/setup_22.x | " + prefix + "bash - && " + prefix + "apt-get install -y nodejs"
}

func codeServerInstallCmd() string {
	if isContainerRuntime() {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return "brew install code-server"
	}
	return "curl -fsSL https://code-server.dev/install.sh | sh"
}

func copilotInstallCmd() string {
	return npmGlobalInstallCmd("@github/copilot@latest")
}

func baseTools() []Tool {
	return []Tool{
		{"curl", "curl", packageInstallCmd("curl"), true, false},
		{"unzip", "unzip", packageInstallCmd("unzip"), true, false},
		{"jq", "jq", packageInstallCmd("jq"), true, false},
		{"ssh-keygen", "ssh-keygen", opensshInstallCmd(), true, false},
		{"tmux", "tmux", packageInstallCmd("tmux"), true, false},
		{"git", "git", packageInstallCmd("git"), true, false},
		{"node", "node", nodeInstallCmd(), true, false},
	}
}

func checkEnvironment() []Tool {
	extendPATH()
	tools := append(baseTools(), []Tool{
		{"openclaw", "openclaw", openClawInstallCmd(), true, false},
		{"claude", "claude", claudeInstallCmd(), true, false},
		{"codex", "codex", codexInstallCmd(), true, false},
		{"opencode", "opencode", opencodeInstallCmd(), true, false},
		{"cursor-agent", "cursor-agent", cursorInstallCmd(), true, false},
		{"hermes", "hermes", hermesInstallCmd(), true, false},
		{"code-server", "code-server", codeServerInstallCmd(), true, false},
	}...)

	fmt.Println("🔍 检查环境依赖...")
	for i := range tools {
		_, err := exec.LookPath(tools[i].Command)
		tools[i].Installed = err == nil
		status := "❌"
		if tools[i].Installed {
			status = "✅"
		}
		fmt.Printf("  %s %s\n", status, tools[i].Name)
	}

	return tools
}

func installMissing(tools []Tool) {
	extendPATH()
	missing := []Tool{}
	for _, tool := range tools {
		if tool.Required && !tool.Installed {
			missing = append(missing, tool)
		}
	}

	if len(missing) == 0 {
		fmt.Println("✅ 所有依赖已安装")
		return
	}

	fmt.Printf("📦 安装缺失依赖 (%d 个)...\n", len(missing))

	// 必须全部安装成功才能继续
	for _, tool := range missing {
		if tool.InstallCmd == "" {
			fmt.Printf("  %s ❌ 缺失且当前环境禁止自动安装\n", tool.Name)
			fmt.Printf("❌ 环境初始化失败，请检查镜像预装内容\n")
			os.Exit(1)
		}
		fmt.Printf("  安装 %s...", tool.Name)

		cmd := exec.Command("sh", "-c", tool.InstallCmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf(" ❌ 失败: %v\n", err)
			fmt.Printf("❌ 环境初始化失败，请检查网络连接和权限\n")
			os.Exit(1) // 有任何失败就退出
		} else {
			fmt.Printf(" ✅ 完成\n")
		}
	}
}

func selectAgents() []string {
	selected := []string{"claude"}
	fmt.Println("\n🤖 默认预装 AI 工具:")
	fmt.Println("  ✅ Claude")
	fmt.Printf("✅ 已选择: %v\n", selected)
	return selected
}

func hermesInstallCmd() string {
	rawScriptURL := defaultGitHubProxy + "https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh"
	archiveURL := defaultGitHubProxy + "https://codeload.github.com/NousResearch/hermes-agent/tar.gz/refs/heads/${branch}"
	return strings.Join([]string{
		"export HERMES_HOME=\"$HOME/.hermes\"",
		"export HERMES_INSTALL_DIR=\"$HOME/.hermes/hermes-agent\"",
		"export UV_INDEX_URL=\"https://pypi.tuna.tsinghua.edu.cn/simple\"",
		"export PIP_INDEX_URL=\"https://pypi.tuna.tsinghua.edu.cn/simple\"",
		"export REPO_URL_HTTPS=\"https://gh-proxy.com/https://github.com/NousResearch/hermes-agent.git\"",
		"export BRANCH=\"main\"",
		"export INSTALL_DIR=\"$HERMES_INSTALL_DIR\"",
		"cicy_clone_hermes() { branch=\"$1\"; install_dir=\"$2\"; tmp_dir=$(mktemp -d); archive_url=\"" + archiveURL + "\"; if ! curl -fsSL \"$archive_url\" -o \"$tmp_dir/hermes-agent.tar.gz\"; then rm -rf \"$tmp_dir\"; return 1; fi; if ! tar -xzf \"$tmp_dir/hermes-agent.tar.gz\" -C \"$tmp_dir\"; then rm -rf \"$tmp_dir\"; return 1; fi; root_dir=$(find \"$tmp_dir\" -maxdepth 1 -mindepth 1 -type d | head -n 1); if [ -z \"$root_dir\" ]; then rm -rf \"$tmp_dir\"; return 1; fi; rm -rf \"$install_dir\"; mkdir -p \"$(dirname \"$install_dir\")\"; mv \"$root_dir\" \"$install_dir\"; rm -rf \"$tmp_dir\"; return 0; }",
		"if cicy_clone_hermes \"$BRANCH\" \"$INSTALL_DIR\"; then curl -fsSL " + rawScriptURL + " | HERMES_HOME=\"$HERMES_HOME\" HERMES_INSTALL_DIR=\"$HERMES_INSTALL_DIR\" bash -s -- --no-venv --skip-setup </dev/null; else curl -fsSL " + rawScriptURL + " | bash -s -- --no-venv --skip-setup </dev/null; fi",
	}, " && ")
}

func selectedAgentConfigs() map[string]Tool {
	return map[string]Tool{
		"openclaw": {"openclaw", "openclaw", openClawInstallCmd(), true, false},
		"claude":   {"claude", "claude", claudeInstallCmd(), true, false},
		"cicy":     {"cicy", "cicy", cicyInstallCmd(), true, false},
		"codex":    {"codex", "codex", codexInstallCmd(), true, false},
		"opencode": {"opencode", "opencode", opencodeInstallCmd(), true, false},
		"cursor":   {"cursor-agent", "cursor-agent", cursorInstallCmd(), true, false},
		"hermes":   {"hermes", "hermes", hermesInstallCmd(), true, false},
	}
}

func ensureAgentToolInstalled(agentType string) {
	agentType = strings.TrimSpace(strings.ToLower(agentType))
	if agentType == "" {
		return
	}
	switch normalizeAgentType(agentType) {
	case "openclaw", "claude", "cicy", "codex", "opencode", "cursor", "hermes":
		return
	}
	config, exists := selectedAgentConfigs()[agentType]
	if !exists {
		return
	}
	extendPATH()
	if _, err := exec.LookPath(config.Command); err == nil {
		return
	}
	if config.InstallCmd == "" {
		log.Printf("[startup] missing agent tool in current runtime and auto-install disabled: %s", config.Name)
		return
	}
	log.Printf("[startup] installing missing agent tool: %s", config.Name)
	cmd := exec.Command("sh", "-c", config.InstallCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[startup] failed to install %s: %v", config.Name, err)
		return
	}
	extendPATH()
	log.Printf("[startup] installed missing agent tool: %s", config.Name)
}

func installSelectedAgents(selected []string) {
	if len(selected) == 0 {
		return
	}
	fmt.Printf("\n📦 AI 工具将在 tmux pane 启动时按需安装: %v\n", selected)
}

// builtinAgents defines the available agent catalog (no fixed port binding).
var builtinAgents = []struct {
	AgentType string
	Title     string
}{
	{"claude", "Claude"},
	{"codex", "Codex"},
	{"opencode", "OpenCode"},
	{"cursor", "Cursor"},
	{"kiro-cli", "Kiro CLI"},
	{"copilot", "GitHub Copilot"},
	{"cicy-wechat", "WeChat"},
	{"cicy-feishu", "Feishu"},
	{"openclaw", "OpenClaw"},
	{"hermes", "Hermes Agent"},
	{"cicy-claude", "CiCy"},
}

var nonLabAllowedBuiltinAgents = []string{"claude", "codex", "opencode"}

func effectiveAllowedAgentTypes() []string {
	if labMode {
		selected := make([]string, 0, len(builtinAgents))
		for _, ba := range builtinAgents {
			selected = append(selected, ba.AgentType)
		}
		return selected
	}
	selected := make([]string, 0, len(nonLabAllowedBuiltinAgents))
	selected = append(selected, nonLabAllowedBuiltinAgents...)
	return selected
}

func effectiveAgentOptions() []M {
	allowed := effectiveAllowedAgentTypes()
	options := make([]M, 0, len(allowed))
	for _, agentType := range allowed {
		options = append(options, M{
			"value": agentType,
			"label": builtinAgentTitle(agentType),
		})
	}
	return options
}

const primaryWorkerSession = "w-10001"
const primaryWorkerPaneID = "w-10001:main.0"

type builtinWorker struct {
	Port      int
	AgentType string
	Title     string
}

// selectedBuiltinWorkers assigns ports starting from 10001 in the order of selected.
func selectedBuiltinWorkers(selected []string) []builtinWorker {
	workers := make([]builtinWorker, 0, len(selected))
	for i, agentType := range selected {
		agentType = normalizeAgentType(agentType)
		if agentType == "" {
			continue
		}
		workers = append(workers, builtinWorker{
			Port:      10001 + i,
			AgentType: agentType,
			Title:     builtinAgentTitle(agentType),
		})
	}
	return workers
}

func builtinAgentTitle(agentType string) string {
	agentType = normalizeAgentType(agentType)
	for _, ba := range builtinAgents {
		if ba.AgentType == agentType {
			return ba.Title
		}
	}
	return agentType
}

func isBuiltinAgentType(agentType string) bool {
	agentType = normalizeAgentType(agentType)
	for _, ba := range builtinAgents {
		if ba.AgentType == agentType {
			return true
		}
	}
	return false
}

func isAllowedAgentType(agentType string) bool {
	agentType = normalizeAgentType(agentType)
	for _, allowedAgentType := range effectiveAllowedAgentTypes() {
		if allowedAgentType == agentType {
			return true
		}
	}
	return false
}

func parseSelectedAgents(agentList string) ([]string, error) {
	agentList = strings.TrimSpace(agentList)
	if agentList == "" {
		return nil, fmt.Errorf("empty agent list")
	}

	if strings.EqualFold(agentList, "all") {
		return effectiveAllowedAgentTypes(), nil
	}

	seen := map[string]bool{}
	var selected []string
	for _, raw := range strings.Split(agentList, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		agentType := normalizeAgentType(raw)
		if !isBuiltinAgentType(agentType) {
			return nil, fmt.Errorf("unsupported agent: %s", raw)
		}
		if !isAllowedAgentType(agentType) {
			return nil, fmt.Errorf("agent not allowed in current mode: %s", raw)
		}
		if seen[agentType] {
			continue
		}
		seen[agentType] = true
		selected = append(selected, agentType)
	}

	if len(selected) == 0 {
		return nil, fmt.Errorf("no valid agents selected")
	}
	return selected, nil
}

const defaultDevAgent = "claude"

func effectiveAgentsFlag() string {
	if value := strings.TrimSpace(agentsFlag); value != "" {
		return value
	}
	if labMode {
		return "all"
	}
	if devMode {
		return defaultDevAgent
	}
	return ""
}

func mustSelectedAgents() []string {
	effective := effectiveAgentsFlag()
	if effective == "" {
		return []string{defaultDevAgent}
	}

	selected, err := parseSelectedAgents(effective)
	if err != nil {
		log.Fatalf("[startup] invalid --agents value %q: %v", effective, err)
	}
	return selected
}

func ensureWorkerIndexAtLeast(n int) {
	if n <= 0 {
		return
	}
	var current int
	_ = store.QueryRow("SELECT value FROM global_vars WHERE key_name='worker_index'").Scan(&current)
	if current >= n {
		return
	}
	if _, err := store.Exec(
		store.Upsert("global_vars", "key_name", []string{"key_name", "value"}, []string{"value"}),
		"worker_index", n,
	); err != nil {
		log.Printf("[startup] failed to update worker_index to %d: %v", n, err)
	}
}

func setWorkerIndex(n int) {
	if n <= 0 {
		return
	}
	if _, err := store.Exec(
		store.Upsert("global_vars", "key_name", []string{"key_name", "value"}, []string{"value"}),
		"worker_index", n,
	); err != nil {
		log.Printf("[startup] failed to set worker_index to %d: %v", n, err)
	}
}

func syncWorkerIndexToExistingAgents() {
	var current sql.NullInt64
	if err := store.QueryRow("SELECT value FROM global_vars WHERE key_name='worker_index'").Scan(&current); err == nil {
		if current.Valid && current.Int64 > int64(defaultWorkerIndex) {
			return
		}
	}
	var maxPort int
	if err := store.QueryRow("SELECT COALESCE(MAX(ttyd_port), 0) FROM agent_config").Scan(&maxPort); err != nil {
		log.Printf("[startup] failed to sync worker_index from agent_config: %v", err)
		return
	}
	if maxPort > 0 {
		setWorkerIndex(maxPort)
	}
}

func syncBuiltinAgentTitles(selected []string) {
	for _, w := range selectedBuiltinWorkers(selected) {
		paneID := builtinWorkerSession(w.Port) + ":main.0"
		query := fmt.Sprintf("UPDATE agent_config SET title=?, updated_at=%s WHERE pane_id=? AND (COALESCE(TRIM(title), '')='' OR title=?)", store.Now())
		legacyTitle := ""
		if paneID == primaryWorkerPaneID {
			legacyTitle = "商业顾问"
		}
		if _, err := store.Exec(query, w.Title, paneID, legacyTitle); err != nil {
			log.Printf("[startup] failed to sync builtin title for %s: %v", paneID, err)
		}
	}
}

func hasSelectedAgentType(selected []string, agentType string) bool {
	for _, selectedAgentType := range selected {
		if selectedAgentType == agentType {
			return true
		}
	}
	return false
}

func builtinWorkerSession(port int) string {
	return fmt.Sprintf("w-%d", port)
}

func ensurePrimaryWorkerForBindings(selected []string) {
}

// ensureWorkerBoundToPrimary inserts a pane_agents row attaching workerSession
// under primaryWorkerSession (w-10001). Idempotent thanks to the
// UNIQUE(pane_id, agent_name) constraint. No-op when workerSession is the
// primary itself.
func ensureWorkerBoundToPrimary(workerSession string) {
	workerSession = shortPaneID(strings.TrimSpace(workerSession))
	if workerSession == "" || workerSession == primaryWorkerSession {
		return
	}
	var existingID int
	err := store.QueryRow(
		"SELECT id FROM pane_agents WHERE pane_id=? AND agent_name=?",
		primaryWorkerSession, workerSession,
	).Scan(&existingID)
	if err == nil && existingID > 0 {
		return
	}
	if _, err := store.Exec(
		"INSERT INTO pane_agents (pane_id, agent_name, status) VALUES (?,?,'active')",
		primaryWorkerSession, workerSession,
	); err != nil {
		log.Printf("[startup] failed to bind %s under %s: %v", workerSession, primaryWorkerSession, err)
		return
	}
	log.Printf("[startup] bound %s under %s", workerSession, primaryWorkerSession)
}

func createSelectedWorkers(selected []string) {
	fmt.Println("\n🚀 创建选中的 Workers...")
	workers := selectedBuiltinWorkers(selected)
	for _, w := range workers {
		paneID := builtinWorkerSession(w.Port) + ":main.0"
		var count int
		store.QueryRow("SELECT COUNT(*) FROM agent_config WHERE pane_id=?", paneID).Scan(&count)
		if count > 0 {
			// Update agent_type and title in case they changed
			store.Exec(fmt.Sprintf("UPDATE agent_config SET agent_type=?, title=?, updated_at=%s WHERE pane_id=?", store.Now()),
				w.AgentType, w.Title, paneID)
			fmt.Printf("  ⏭ %s - 已存在，已更新\n", w.Title)
		} else {
			createBuiltinWorker(w.Port, w.AgentType, w.Title)
		}
		// With more than one builtin agent, the non-primary ones get attached
		// under w-10001 so they appear in the same chat session by default.
		if len(workers) > 1 {
			ensureWorkerBoundToPrimary(builtinWorkerSession(w.Port))
		}
	}
	if len(workers) > 0 {
		setWorkerIndex(workers[len(workers)-1].Port)
	}
}

func createBuiltinWorker(port int, agentType, title string) {
	session := fmt.Sprintf("w-%d", port)
	token := getFirstToken()
	if _, err := createManagedPane(paneCreateOpts{
		session:         session,
		title:           title,
		role:            "master",
		defaultModel:    "",
		agentType:       agentType,
		workspace:       builtinWorkerWorkspace(session),
		initScript:      "",
		port:            port,
		token:           token,
		allowAllActions: true,
		replyInChinese:  true,
	}); err != nil {
		fmt.Printf("  ❌ %s 创建失败: %v\n", title, err)
		return
	}
	fmt.Printf("  ✅ %s (w-%d, port %d)\n", title, port, port)
}

func runSetup() {
	fmt.Println("🎯 Cicy Code 环境初始化")
	fmt.Println("=" + strings.Repeat("=", 30))

	// 1. 检查基础环境
	baseTools := baseTools()

	fmt.Println("🔍 检查基础环境...")
	for i := range baseTools {
		_, err := exec.LookPath(baseTools[i].Command)
		baseTools[i].Installed = err == nil
		status := "❌"
		if baseTools[i].Installed {
			status = "✅"
		}
		fmt.Printf("  %s %s\n", status, baseTools[i].Name)
	}

	// 2. 安装基础环境
	installMissing(baseTools)

	// 4. 让用户选择 AI 工具
	selectedAgents := selectAgents()

	// 5. 安装选中的 AI 工具
	installSelectedAgents(selectedAgents)

	// 6. 创建对应的 workers
	createSelectedWorkers(selectedAgents)

	fmt.Println("=" + strings.Repeat("=", 30))
	fmt.Println("🎉 环境初始化完成！")
}

// runSetupWithAgents runs setup non-interactively with specified agents.
// agentList is comma-separated, e.g. "codex,claude" or "all".
func runSetupWithAgents(agentList string) {
	fmt.Println("🎯 Cicy Code 环境初始化 (non-interactive)")
	fmt.Println("=" + strings.Repeat("=", 30))

	// 1. Check & install base tools
	baseTools := baseTools()
	fmt.Println("🔍 检查基础环境...")
	for i := range baseTools {
		_, err := exec.LookPath(baseTools[i].Command)
		baseTools[i].Installed = err == nil
		status := "❌"
		if baseTools[i].Installed {
			status = "✅"
		}
		fmt.Printf("  %s %s\n", status, baseTools[i].Name)
	}
	installMissing(baseTools)

	// 2. Parse agent list
	selected, err := parseSelectedAgents(agentList)
	if err != nil {
		fmt.Printf("❌ 无效的 --agents 参数: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("📦 安装 agents: %v\n", selected)
	installSelectedAgents(selected)
	createSelectedWorkers(selected)

	fmt.Println("=" + strings.Repeat("=", 30))
	fmt.Println("🎉 环境初始化完成！")
}

func checkEnv() {
	extendPATH()

	fmt.Println("🔍 检查基础环境...")
	base := baseTools()
	for i := range base {
		_, err := exec.LookPath(base[i].Command)
		base[i].Installed = err == nil
		status := "❌"
		if base[i].Installed {
			status = "✅"
		}
		fmt.Printf("  %s %s\n", status, base[i].Name)
	}
	installMissing(base)
	ensureSSHKeyPair()
	ensureTmuxConf()
	ensureCicyTmuxConf()
	ensureShellRCSourcesCicyTmuxConf()

	setupAIConfigs()

	var count int
	if err := store.QueryRow("SELECT COUNT(*) FROM agent_config").Scan(&count); err != nil {
		log.Fatalf("[startup] failed to query agent_config: %v", err)
	}
	effectiveAgentList := effectiveAgentsFlag()
	if count == 0 {
		if isContainerRuntime() {
			// Preinstalled container runtime must never block on interactive setup.
			// Respect explicit --agents=... when provided; otherwise keep the default
			// footprint minimal with only w-10001 Claude.
			if effectiveAgentList != "" {
				runSetupWithAgents(effectiveAgentList)
			} else {
				createSelectedWorkers([]string{"claude"})
			}
		} else if effectiveAgentList != "" {
			runSetupWithAgents(effectiveAgentList)
		} else {
			runSetup()
		}
	}

	selectedAgents := mustSelectedAgents()
	// If any active worker is configured to route traffic via the local
	// mihomo proxy, make sure it's running BEFORE we restore tmux/ttyd —
	// otherwise the workers come up trying to dial a dead :9001.
	startCicyMihomoIfNeeded()
	ensureBuiltinAgents(selectedAgents)
	syncWorkerIndexToExistingAgents()
	syncBuiltinAgentTitles(selectedAgents)
	ensureCicySkillsAsync()
	ensureCodeServer()
}

// anyActiveAgentUsesProxy returns true if at least one active row in
// agent_config has a non-empty `proxy` entry in its JSON config.
func anyActiveAgentUsesProxy() bool {
	var count int
	if err := store.QueryRow(
		`SELECT COUNT(*) FROM agent_config
		  WHERE active=1
		    AND COALESCE(config,'') LIKE '%"proxy"%'`,
	).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

// startCicyMihomoIfNeeded brings up the local mihomo proxy synchronously when
// any active worker is configured to route via it. This blocks startup until
// mihomo is up so the workers don't race a half-started proxy:
//
//  1. Synchronously install cicy-skills (so the cicy-mihomo wrapper exists)
//  2. Synchronously install the mihomo binary (so `cicy-mihomo start` actually works)
//  3. Start mihomo (no-op if already running)
func startCicyMihomoIfNeeded() {
	if !anyActiveAgentUsesProxy() {
		return
	}
	// Step 1: cicy-skills install (no-op if dev mode or already installed).
	ensureCicySkillsSync()

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	wrapper := filepath.Join(home, ".local", "bin", "cicy-mihomo")
	if _, err := os.Stat(wrapper); err != nil {
		log.Printf("[startup] cicy-mihomo wrapper still missing after install — proxy-using workers may fail")
		return
	}

	// Step 2: download mihomo binary if missing. ensureMihomoBinaryInstalled
	// is idempotent (skip when already on disk).
	logPath := filepath.Join(cicyStateDir, cicySkillsInstallLogFile)
	_ = os.MkdirAll(cicyStateDir, 0o755)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("[startup] failed to open mihomo install log: %v", err)
		return
	}
	defer logFile.Close()
	ensureMihomoBinaryInstalled(logFile, logPath)

	// Step 3: start mihomo. Skip if already running. `cicy-mihomo status`
	// always exits 0, so we parse stdout for "status: running".
	if out, err := exec.Command(wrapper, "status").Output(); err == nil && strings.Contains(string(out), "status: running") {
		return
	}
	// Generate a default config if one doesn't exist yet (idempotent — fails
	// loudly when one already exists, which is fine since we ignore the error).
	mihomoCfg := filepath.Join(home, "cicy-ai", "db", "mihomo.yaml")
	if _, err := os.Stat(mihomoCfg); os.IsNotExist(err) {
		if err := exec.Command(wrapper, "gen-config").Run(); err != nil {
			log.Printf("[startup] cicy-mihomo gen-config failed: %v", err)
			return
		}
	}
	cmd := exec.Command(wrapper, "start")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[startup] cicy-mihomo start failed: %v (proxy-using workers may not connect)", err)
		return
	}
	log.Printf("[startup] cicy-mihomo started for proxy-using workers")
}

func setupAIConfigs() {
	cfg := loadRuntimeAIConfig()
	apiKey := cfg.APIKey
	apiUrl := cfg.APIURL
	anthropicUrl := cfg.AnthropicURL
	defaultOpencodeModel := cfg.DefaultOpencodeModel
	defaultClaudeModel := cfg.DefaultClaudeModel
	codexModel := cfg.CodexModel

	if apiKey == "" {
		return
	}

	if apiUrl == "" {
		apiUrl = "http://2000.run:6543/v1"
	}
	if anthropicUrl == "" {
		anthropicUrl = "http://2000.run:6543"
	}
	if defaultOpencodeModel == "" {
		defaultOpencodeModel = "gpt-5.4"
	}
	if defaultClaudeModel == "" {
		defaultClaudeModel = "opus[1m]"
	}
	if codexModel == "" {
		codexModel = "gpt-5.4"
	}

	home, _ := os.UserHomeDir()
	log.Printf("[setup] configuring AI tools with API URL: %s", apiUrl)

	// Set env vars for OpenClaw worker (available via agentBootLines)
	os.Setenv("OPENCLAW_CONFIG_PATH", filepath.Join(home, ".openclaw", "openclaw.json"))
	os.Setenv("OPENAI_API_KEY", apiKey)
	os.Setenv("OPENAI_MODEL", defaultOpencodeModel)
	if openClawToken := strings.TrimSpace(readOpenClawTokenFromGlobalJSON()); openClawToken != "" {
		os.Setenv("OPENCLAW_GATEWAY_TOKEN", openClawToken)
	} else if openClawToken := strings.TrimSpace(readOpenClawTokenFromConfig()); openClawToken != "" {
		os.Setenv("OPENCLAW_GATEWAY_TOKEN", openClawToken)
	}
}

func ensureTmuxConf() {
	ensureManagedDotfile(".tmux.conf", embeddedTmuxConf)
}

func ensureCicyTmuxConf() {
	ensureManagedDotfile(".cicy_tmux.conf", embeddedCicyTmuxConf)
}

// ensureManagedDotfile installs ~/<name>:
//   - --dev: symlink it to <repo>/<name> (the working dir is the repo root in dev
//     mode), so edits to the source file take effect without a rebuild. Any
//     existing path is removed first; a real, non-matching file is saved to .bak.
//   - otherwise: materialize the embedded content, replacing whatever is there —
//     including a stale dev-mode symlink (we must never write through it).
func ensureManagedDotfile(name, embedded string) {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("[startup] failed to resolve home dir for %s: %v", name, err)
	}
	dst := filepath.Join(home, name)

	backupIfForeign := func() {
		if info, statErr := os.Lstat(dst); statErr == nil && info.Mode().IsRegular() {
			if data, readErr := os.ReadFile(dst); readErr == nil && len(data) > 0 && string(data) != embedded {
				if writeErr := os.WriteFile(dst+".bak", data, 0644); writeErr == nil {
					log.Printf("[startup] backed up %s -> %s.bak", dst, dst)
				}
			}
		}
	}

	if devMode {
		src := name
		if wd, wdErr := os.Getwd(); wdErr == nil {
			src = filepath.Join(wd, name)
		}
		if abs, absErr := filepath.Abs(src); absErr == nil {
			src = abs
		}
		if info, statErr := os.Lstat(dst); statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				if cur, _ := os.Readlink(dst); cur == src {
					return
				}
			} else {
				backupIfForeign()
			}
			_ = os.Remove(dst)
		}
		if linkErr := os.Symlink(src, dst); linkErr != nil {
			log.Fatalf("[startup] failed to symlink %s -> %s: %v", dst, src, linkErr)
		}
		log.Printf("[startup] dev: linked %s -> %s", dst, src)
		return
	}

	if info, statErr := os.Lstat(dst); statErr == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			if data, readErr := os.ReadFile(dst); readErr == nil && string(data) == embedded {
				return
			}
			backupIfForeign()
		}
		_ = os.Remove(dst)
	} else {
		log.Printf("[startup] installing %s", dst)
	}
	if writeErr := os.WriteFile(dst, []byte(embedded), 0644); writeErr != nil {
		log.Fatalf("[startup] failed to write %s: %v", dst, writeErr)
	}
}

// ensureShellRCSourcesCicyTmuxConf injects `source ~/.cicy_tmux.conf` into
// every shell rc the user might land in:
//   - .bashrc       (bash interactive non-login)
//   - .bash_profile (bash login — includes `bash -lc`, ssh, tmux new-window)
//   - .zshrc        (zsh interactive)
//
// Idempotent: skip any file that already references .cicy_tmux.conf in any
// form. PATH and other env are set inside cicy_tmux.conf itself.
func ensureShellRCSourcesCicyTmuxConf() {
	line := `[ -f "$HOME/.cicy_tmux.conf" ] && source "$HOME/.cicy_tmux.conf"`
	dotForm := `[ -f "$HOME/.cicy_tmux.conf" ] && . "$HOME/.cicy_tmux.conf"`
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, name := range []string{".bashrc", ".bash_profile", ".zshrc"} {
		path := filepath.Join(home, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip if rc doesn't exist
		}
		text := string(data)
		// Strip the legacy `.` (POSIX dot) form left by older entrypoint scripts.
		if strings.Contains(text, dotForm) {
			text = strings.ReplaceAll(text, dotForm+"\n", "")
			text = strings.ReplaceAll(text, dotForm, "")
		}
		if !strings.Contains(text, line) {
			text = strings.TrimRight(text, "\n") + "\n\n" + line + "\n"
		}
		if text != string(data) {
			_ = os.WriteFile(path, []byte(text), 0644)
		}
	}
}

func ensureSSHKeyPair() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("[startup] failed to resolve home dir for ssh key setup: %v", err)
	}
	sshDir := filepath.Join(home, ".ssh")
	privateKey := filepath.Join(sshDir, "id_rsa")
	publicKey := privateKey + ".pub"
	authKeysPath := filepath.Join(sshDir, "authkeys")
	authorizedKeysPath := filepath.Join(sshDir, "authorized_keys")
	configPath := filepath.Join(sshDir, "config")

	if err := os.MkdirAll(sshDir, 0700); err != nil {
		log.Fatalf("[startup] failed to create %s: %v", sshDir, err)
	}
	if err := os.Chmod(sshDir, 0700); err != nil {
		log.Printf("[startup] failed to chmod %s to 0700: %v", sshDir, err)
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		log.Fatalf("[startup] ssh-keygen not found after base tool setup: %v", err)
	}

	if _, err := os.Stat(privateKey); err == nil {
		if chmodErr := os.Chmod(privateKey, 0600); chmodErr != nil {
			log.Printf("[startup] failed to chmod %s to 0600: %v", privateKey, chmodErr)
		}
		if _, pubErr := os.Stat(publicKey); os.IsNotExist(pubErr) {
			cmd := exec.Command("ssh-keygen", "-y", "-f", privateKey)
			out, genErr := cmd.Output()
			if genErr != nil {
				log.Fatalf("[startup] failed to regenerate %s from %s: %v", publicKey, privateKey, genErr)
			}
			if writeErr := os.WriteFile(publicKey, out, 0644); writeErr != nil {
				log.Fatalf("[startup] failed to write %s: %v", publicKey, writeErr)
			}
			log.Printf("[startup] regenerated %s", publicKey)
		}
		if chmodErr := os.Chmod(publicKey, 0644); chmodErr != nil && !os.IsNotExist(chmodErr) {
			log.Printf("[startup] failed to chmod %s to 0644: %v", publicKey, chmodErr)
		}
	} else if !os.IsNotExist(err) {
		log.Fatalf("[startup] failed to inspect %s: %v", privateKey, err)
	} else {
		log.Printf("[startup] generating missing SSH keypair at %s", privateKey)
		cmd := exec.Command("ssh-keygen", "-t", "rsa", "-b", "4096", "-N", "", "-f", privateKey, "-q")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("[startup] failed to generate %s: %v", privateKey, err)
		}
		if chmodErr := os.Chmod(privateKey, 0600); chmodErr != nil {
			log.Printf("[startup] failed to chmod %s to 0600: %v", privateKey, chmodErr)
		}
		if chmodErr := os.Chmod(publicKey, 0644); chmodErr != nil {
			log.Printf("[startup] failed to chmod %s to 0644: %v", publicKey, chmodErr)
		}
	}

	pubKeyBytes, readErr := os.ReadFile(publicKey)
	if readErr != nil {
		log.Fatalf("[startup] failed to read %s: %v", publicKey, readErr)
	}
	pubKey := strings.TrimSpace(string(pubKeyBytes))
	if pubKey == "" {
		log.Fatalf("[startup] %s is empty", publicKey)
	}

	ensureSSHAuthKeysFile(authKeysPath, pubKey)
	ensureSSHAuthKeysFile(authorizedKeysPath, pubKey)
	ensureSSHConfigFile(configPath)
}

func ensureSSHAuthKeysFile(path string, pubKey string) {
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		log.Fatalf("[startup] failed to read %s: %v", path, err)
	}
	if err == nil {
		for _, line := range strings.Split(string(current), "\n") {
			if strings.TrimSpace(line) == pubKey {
				if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
					log.Printf("[startup] failed to chmod %s to 0600: %v", path, chmodErr)
				}
				return
			}
		}
	}

	var content string
	if len(current) > 0 {
		content = string(current)
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += pubKey + "\n"
	} else {
		content = pubKey + "\n"
	}
	if writeErr := os.WriteFile(path, []byte(content), 0600); writeErr != nil {
		log.Fatalf("[startup] failed to write %s: %v", path, writeErr)
	}
	if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
		log.Printf("[startup] failed to chmod %s to 0600: %v", path, chmodErr)
	}
	log.Printf("[startup] ensured %s contains the local public key", path)
}

func ensureSSHConfigFile(path string) {
	const managedBlock = "# cicy-code managed ssh defaults\nHost *\n  IdentityFile ~/.ssh/id_rsa\n  IdentitiesOnly yes\n"

	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		log.Fatalf("[startup] failed to read %s: %v", path, err)
	}
	if err == nil && strings.Contains(string(current), managedBlock) {
		if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
			log.Printf("[startup] failed to chmod %s to 0600: %v", path, chmodErr)
		}
		return
	}

	content := managedBlock
	if len(current) > 0 {
		content = string(current)
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + managedBlock
	}
	if writeErr := os.WriteFile(path, []byte(content), 0600); writeErr != nil {
		log.Fatalf("[startup] failed to write %s: %v", path, writeErr)
	}
	if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
		log.Printf("[startup] failed to chmod %s to 0600: %v", path, chmodErr)
	}
	log.Printf("[startup] ensured %s exists", path)
}

func cicySkillsDistBinaryPath() string {
	return filepath.Join(cicySkillsProjectDir(), "dist", "cicy-skills")
}

func cicySkillsProjectDir() string {
	return filepath.Join(cicySkillsDir, "cicy-skills")
}

func cicySkillsSkillDocPath(profile, skill string) string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, "."+profile, "skills", skill, "SKILL.md")
}

func cicySkillsCommandPath(name string) string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".local", "bin", name)
}

func needsCicySkillsInstall() bool {
	// "Installed" means: cicy-skills CLI is reachable and at least the
	// canonical agent skill (agent-code-server) has SKILL.md emitted into
	// every supported profile dir. We deliberately DO NOT require the
	// extracted source tree to live at ~/cicy-ai/skills/cicy-skills/ — a
	// dev `make install-local-cli` from anywhere else is equally valid.
	cliPath := cicySkillsCommandPath("cicy-skills")
	if strings.TrimSpace(cliPath) == "" {
		return true
	}
	if info, err := os.Stat(cliPath); err != nil || info.Mode()&0o111 == 0 {
		return true
	}
	for _, profile := range []string{"codex", "claude", "opencode"} {
		if _, err := os.Stat(cicySkillsSkillDocPath(profile, "agent-code-server")); err != nil {
			return true
		}
	}
	// Stale retired aliases left behind by an older install still mean we
	// should re-run install to scrub them.
	for _, name := range []string{"webpage", "webpage-ping", "ipc-ping", "agent-page-ping"} {
		path := cicySkillsCommandPath(name)
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// extractEmbeddedCicySkills untars the embedded cicy-skills tarball into dest.
// The tar carries the original permission bits, so dist/ binaries and shell
// scripts stay executable. dest is wiped first to avoid version drift.
func extractEmbeddedCicySkills(dest string) error {
	if len(embeddedCicySkillsTar) < 64 {
		return fmt.Errorf("embedded cicy-skills tarball is missing or empty (was the binary built with prepare_skills_embed?)")
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	gz, err := gzip.NewReader(bytes.NewReader(embeddedCicySkillsTar))
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue
		}
		target := filepath.Join(dest, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777|0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

func ensureCicySkillsAsync() {
	cicySkillsInstallOnce.Do(func() {
		// In dev mode the user manages cicy-skills via `make install-local-cli`
		// from the repo. Re-running the embedded install at every start would
		// silently overwrite their dev symlinks with the build-time snapshot,
		// so we stay out of the way entirely.
		if devMode {
			log.Printf("[startup] dev mode: skipping cicy-skills bootstrap (manage via make install-local-cli)")
			return
		}
		if !needsCicySkillsInstall() {
			return
		}
		go doCicySkillsInstall()
	})
}

// ensureCicySkillsSync runs the bootstrap inline. Safe to call alongside the
// async path — sync.Once guarantees the work happens at most once. Used when
// a downstream step (e.g. starting mihomo before workers) needs the wrapper
// symlinks and dist binaries to definitely be present before continuing.
func ensureCicySkillsSync() {
	cicySkillsInstallOnce.Do(func() {
		if devMode {
			log.Printf("[startup] dev mode: skipping cicy-skills bootstrap (manage via make install-local-cli)")
			return
		}
		if !needsCicySkillsInstall() {
			return
		}
		doCicySkillsInstall()
	})
}

func doCicySkillsInstall() {
	if err := os.MkdirAll(cicyStateDir, 0o755); err != nil {
		log.Printf("[startup] failed to create %s for cicy-skills bootstrap: %v", cicyStateDir, err)
		return
	}
	logPath := filepath.Join(cicyStateDir, cicySkillsInstallLogFile)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("[startup] failed to open cicy-skills install log: %v", err)
		return
	}
	defer logFile.Close()

	projectRoot := cicySkillsProjectDir()
	log.Printf("[startup] extracting embedded cicy-skills into %s", projectRoot)
	fmt.Fprintf(logFile, "[%s] extracting embedded cicy-skills into %s\n", time.Now().Format(time.RFC3339), projectRoot)
	if err := extractEmbeddedCicySkills(projectRoot); err != nil {
		log.Printf("[startup] cicy-skills extract failed: %v (log: %s)", err, logPath)
		fmt.Fprintf(logFile, "extract failed: %v\n", err)
		return
	}

	// Install Google provider Node deps if missing — only network step,
	// optional (Google skill is unusable without it but rest still work).
	providerDir := filepath.Join(projectRoot, "providers", "google-node")
	if _, err := os.Stat(filepath.Join(providerDir, "package.json")); err == nil {
		if _, err := os.Stat(filepath.Join(providerDir, "node_modules")); err != nil {
			fmt.Fprintln(logFile, "running npm install in providers/google-node")
			cmd := exec.Command("npm", "install", "--silent", "--no-audit", "--no-fund")
			cmd.Dir = providerDir
			cmd.Stdout = logFile
			cmd.Stderr = logFile
			cmd.Env = os.Environ()
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(logFile, "npm install failed (google provider may not work): %v\n", err)
			}
		}
	}

	// Run cicy-skills install all to materialize ~/.local/bin/* symlinks
	// and SKILL.md files via the registry.
	distBin := cicySkillsDistBinaryPath()
	if _, err := os.Stat(distBin); err != nil {
		log.Printf("[startup] cicy-skills bootstrap: %s missing after extract (log: %s)", distBin, logPath)
		return
	}
	if err := os.Chmod(distBin, 0o755); err != nil {
		log.Printf("[startup] chmod %s: %v", distBin, err)
	}
	log.Printf("[startup] running %s install all", distBin)
	fmt.Fprintf(logFile, "[%s] running %s install all\n", time.Now().Format(time.RFC3339), distBin)
	cmd := exec.Command(distBin, "install", "all")
	cmd.Env = append(os.Environ(), "CICY_SKILLS_ROOT="+projectRoot)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Run(); err != nil {
		log.Printf("[startup] cicy-skills install all failed: %v (log: %s)", err, logPath)
		return
	}

	extendPATH()

	// Honor user-driven uninstalls (skills-state.json) — `install all` would
	// otherwise resurrect anything the user disabled via the marketplace UI.
	applySkillsUninstallState(logFile)

	// After cicy-skills install all has linked the cicy-mihomo wrapper
	// onto PATH, kick off the binary download once (skip if already
	// present so reboots don't redownload).
	ensureMihomoBinaryInstalled(logFile, logPath)

	if needsCicySkillsInstall() {
		log.Printf("[startup] cicy-skills bootstrap incomplete (log: %s)", logPath)
		return
	}
	log.Printf("[startup] cicy-skills bootstrap completed")
}

// applySkillsUninstallState re-applies user-driven uninstalls after
// `cicy-skills install all` has re-materialized every catalog skill. Without
// this pass, container restarts would resurrect everything the user disabled
// via the marketplace UI.
func applySkillsUninstallState(logFile *os.File) {
	state, err := loadSkillsState()
	if err != nil {
		fmt.Fprintf(logFile, "[%s] load skills-state.json: %v\n", time.Now().Format(time.RFC3339), err)
		return
	}
	if len(state.Uninstalled) == 0 {
		return
	}
	catalog := marketSkillsCatalog()
	byName := make(map[string]*marketSkill, len(catalog))
	for i := range catalog {
		byName[catalog[i].Name] = &catalog[i]
	}
	for _, name := range state.Uninstalled {
		skill, ok := byName[name]
		if !ok {
			fmt.Fprintf(logFile, "[%s] skipping uninstalled state for unknown skill %q\n", time.Now().Format(time.RFC3339), name)
			continue
		}
		logs, err := uninstallMarketSkill(skill)
		for _, line := range logs {
			fmt.Fprintln(logFile, line)
		}
		if err != nil {
			fmt.Fprintf(logFile, "[%s] re-apply uninstall %s: %v\n", time.Now().Format(time.RFC3339), name, err)
			continue
		}
		log.Printf("[startup] re-applied uninstall for skill %s", name)
	}
}

// ensureMihomoBinaryInstalled runs `cicy-mihomo install` to download the real
// mihomo binary into ~/.local/bin/mihomo after the cicy-skills wrappers have
// landed. Idempotent: skipped if mihomo is already on PATH or at the canonical
// install location. Best-effort: a failure here doesn't fail the startup.
func ensureMihomoBinaryInstalled(logFile *os.File, logPath string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	mihomoTarget := filepath.Join(home, ".local", "bin", "mihomo")
	if runtime.GOOS == "windows" {
		mihomoTarget += ".exe"
	}
	// Skip if already installed somewhere reasonable.
	if info, err := os.Stat(mihomoTarget); err == nil && info.Mode()&0o111 != 0 {
		return
	}
	if _, err := exec.LookPath("mihomo"); err == nil {
		return
	}
	wrapper := filepath.Join(home, ".local", "bin", "cicy-mihomo")
	if _, err := os.Stat(wrapper); err != nil {
		// wrapper symlink missing — install all didn't run successfully; bail
		return
	}
	log.Printf("[startup] running cicy-mihomo install")
	fmt.Fprintf(logFile, "[%s] running cicy-mihomo install\n", time.Now().Format(time.RFC3339))
	cmd := exec.Command(wrapper, "install")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		log.Printf("[startup] cicy-mihomo install failed: %v (log: %s — proxy will be unavailable until installed manually)", err, logPath)
	}
}

func ensureCodeServer() {
	go ensureCodeServerAsync()
}

func ensureCodeServerUserSettings(home string) {
	userDir := filepath.Join(home, ".local", "share", "code-server", "User")
	if err := os.MkdirAll(userDir, 0755); err != nil {
		log.Printf("[startup] failed to create code-server User dir: %v", err)
		return
	}

	settingsPath := filepath.Join(userDir, "settings.json")
	settings := map[string]any{}
	if raw, err := os.ReadFile(settingsPath); err == nil && strings.TrimSpace(string(raw)) != "" {
		if err := json.Unmarshal(raw, &settings); err != nil {
			log.Printf("[startup] failed to parse code-server settings.json, recreating: %v", err)
			settings = map[string]any{}
		}
	}

	if _, ok := settings["workbench.colorTheme"]; !ok {
		settings["workbench.colorTheme"] = "Default Dark+"
	}
	if _, ok := settings["workbench.iconTheme"]; !ok {
		settings["workbench.iconTheme"] = "simple-icons"
	}

	payload, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		log.Printf("[startup] failed to marshal code-server settings: %v", err)
		return
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(settingsPath, payload, 0644); err != nil {
		log.Printf("[startup] failed to write code-server settings.json: %v", err)
	}

	// Ensure argv.json exists without a `locale` key. code-server resolves the
	// workbench locale as: --locale flag > argv.json `locale` > `vscode.nls.locale`
	// cookie > Accept-Language. A *missing* argv.json makes it default to "en" and
	// never consult the cookie, so we create an empty one (only if absent — don't
	// clobber a locale a user set via "Configure Display Language"). The per-user
	// language then flows through the cookie set in proxy.handleCodeServer.
	argvPath := filepath.Join(userDir, "argv.json")
	if _, err := os.Stat(argvPath); os.IsNotExist(err) {
		if err := os.WriteFile(argvPath, []byte("{}\n"), 0644); err != nil {
			log.Printf("[startup] failed to write code-server argv.json: %v", err)
		}
	}

	// VS Code's web workbench eagerly reads these on load; when they are absent
	// the requests surface as "Failed to resolve files / 无法读取文件" plus
	// cascading "Canceled" errors in the browser console. Pre-create them empty
	// (only if missing — never clobber real state).
	if err := os.MkdirAll(filepath.Join(userDir, "prompts"), 0755); err != nil {
		log.Printf("[startup] failed to create code-server User/prompts dir: %v", err)
	}
	for _, name := range []string{"extensions.json", "systemExtensionsCache.json"} {
		p := filepath.Join(userDir, name)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if err := os.WriteFile(p, []byte("[]\n"), 0644); err != nil {
				log.Printf("[startup] failed to write code-server %s: %v", name, err)
			}
		}
	}
}

func installCodeServerExtension(home string, extension string) {
	log.Printf("[startup] installing code-server extension: %s", extension)
	cmd := exec.Command("code-server", "--install-extension", extension, "--force")
	cmd.Dir = home
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PORT=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = env
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		log.Printf("[startup] failed to install code-server extension %s: %v output=%s", extension, runErr, strings.TrimSpace(string(output)))
		return
	}
	if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
		log.Printf("[startup] code-server extension installed: %s output=%s", extension, trimmed)
	}
}

func installEmbeddedCodeServerExtension(home string, fileName string, payload []byte) {
	if len(payload) == 0 {
		log.Printf("[startup] skipped empty embedded code-server extension: %s", fileName)
		return
	}
	tmpFile, err := os.CreateTemp("", "cicy-code-server-extension-*.vsix")
	if err != nil {
		log.Printf("[startup] failed to create temp vsix for %s: %v", fileName, err)
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		log.Printf("[startup] failed to write temp vsix for %s: %v", fileName, err)
		return
	}
	if err := tmpFile.Close(); err != nil {
		log.Printf("[startup] failed to close temp vsix for %s: %v", fileName, err)
		return
	}
	installCodeServerExtension(home, tmpPath)
}

func installBundledCodeServerExtensions(home string) {
	installEmbeddedCodeServerExtension(home, "cicy-code-server-bridge-0.0.4.vsix", embeddedCodeServerBridgeVSIX)
	installEmbeddedCodeServerExtension(home, "MS-CEINTL.vscode-language-pack-zh-hans-1.110.0.vsix", embeddedCodeServerZhHansVSIX)
	installCodeServerExtension(home, "laurenttreguier.vscode-simple-icons")
	ensureCodeServerLanguagePacks(home)
}

// ensureCodeServerLanguagePacks writes <user-data-dir>/languagepacks.json from
// the installed `vscode-language-pack-*` extensions. `code-server
// --install-extension` does NOT generate this file (unlike the in-app gallery
// install), so without it code-server's workbench NLS always falls back to
// English even when a language pack is present. The locale itself is selected
// per request via the `vscode.nls.locale` cookie (see proxy.handleCodeServer).
func ensureCodeServerLanguagePacks(home string) {
	dataDir := filepath.Join(home, ".local", "share", "code-server")
	extDir := filepath.Join(dataDir, "extensions")
	entries, err := os.ReadDir(extDir)
	if err != nil {
		return
	}
	packs := map[string]any{}
	for _, e := range entries {
		if !e.IsDir() || !strings.Contains(e.Name(), "vscode-language-pack-") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(extDir, e.Name(), "package.json"))
		if err != nil {
			continue
		}
		var pkg struct {
			Name        string `json:"name"`
			Publisher   string `json:"publisher"`
			Version     string `json:"version"`
			Contributes struct {
				Localizations []struct {
					LanguageID            string `json:"languageId"`
					LanguageName          string `json:"languageName"`
					LocalizedLanguageName string `json:"localizedLanguageName"`
					Translations          []struct {
						ID   string `json:"id"`
						Path string `json:"path"`
					} `json:"translations"`
				} `json:"localizations"`
			} `json:"contributes"`
		}
		if err := json.Unmarshal(raw, &pkg); err != nil || pkg.Name == "" {
			continue
		}
		extID := strings.ToLower(pkg.Publisher + "." + pkg.Name)
		for _, loc := range pkg.Contributes.Localizations {
			if loc.LanguageID == "" {
				continue
			}
			translations := map[string]string{}
			for _, t := range loc.Translations {
				p := filepath.Join(extDir, e.Name(), filepath.FromSlash(t.Path))
				if _, err := os.Stat(p); err != nil {
					continue
				}
				translations[t.ID] = p
			}
			if translations["vscode"] == "" {
				continue
			}
			label := loc.LocalizedLanguageName
			if label == "" {
				label = loc.LanguageName
			}
			packs[strings.ToLower(loc.LanguageID)] = map[string]any{
				"hash":  "cicy-" + extID + "-" + pkg.Version,
				"label": label,
				"extensions": []map[string]any{
					{"extensionIdentifier": map[string]any{"id": extID}, "version": pkg.Version},
				},
				"translations": translations,
			}
		}
	}
	if len(packs) == 0 {
		return
	}
	payload, err := json.Marshal(packs)
	if err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(dataDir, "languagepacks.json"), payload, 0644); err != nil {
		log.Printf("[startup] failed to write code-server languagepacks.json: %v", err)
		return
	}
	log.Printf("[startup] code-server languagepacks.json written (%d locale)", len(packs))
}

func ensureCodeServerAsync() {
	extendPATH()
	if _, err := exec.LookPath("code-server"); err != nil {
		installCmd := codeServerInstallCmd()
		if installCmd == "" {
			log.Printf("[startup] code-server missing in preinstalled runtime; rebuild the base image")
			return
		}
		fmt.Println("📦 后台安装 code-server...")
		cmd := exec.Command("sh", "-c", installCmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if runErr := cmd.Run(); runErr != nil {
			log.Printf("[startup] failed to install code-server: %v", runErr)
			return
		}
		extendPATH()
	}

	csPort := os.Getenv("CS_PORT")
	if csPort == "" {
		csPort = "8002"
	}
	if isPortListening(mustAtoi(csPort)) {
		log.Printf("[startup] code-server already running on :%s", csPort)
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("[startup] failed to resolve home dir for code-server: %v", err)
		return
	}
	_ = os.Remove(filepath.Join(home, ".local", "share", "code-server", "coder.json"))
	if mkErr := os.MkdirAll(filepath.Join(home, ".cicy"), 0755); mkErr != nil {
		log.Printf("[startup] failed to create ~/.cicy: %v", mkErr)
		return
	}
	ensureCodeServerUserSettings(home)

	logPath := filepath.Join(home, ".cicy", "code-server.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("[startup] failed to open code-server log: %v", err)
		return
	}

	cmd := exec.Command("code-server", "--bind-addr", "127.0.0.1:"+csPort, "--auth", "none", home)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PORT=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = env
	cmd.Dir = home
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if startErr := cmd.Start(); startErr != nil {
		_ = logFile.Close()
		log.Printf("[startup] failed to start code-server: %v", startErr)
		return
	}
	go func() {
		defer logFile.Close()
		if waitErr := cmd.Wait(); waitErr != nil {
			log.Printf("[startup] code-server exited: %v", waitErr)
		}
	}()

	if !waitPort(mustAtoi(csPort), 20*time.Second) {
		log.Printf("[startup] code-server did not become ready on :%s", csPort)
		return
	}
	log.Printf("[startup] code-server ready on :%s", csPort)

	go installBundledCodeServerExtensions(home)
}

func mustAtoi(s string) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		log.Fatalf("[startup] invalid port %q", s)
	}
	return n
}

// ensureBuiltinAgents restores tmux sessions and ttyd for all active agents.
// The selected builtin list is only used to sync builtin title/agent_type.
func ensureBuiltinAgents(selected []string) {
	workers := selectedBuiltinWorkers(selected)
	desiredByPaneID := make(map[string]builtinWorker, len(workers))
	for _, w := range workers {
		pid := builtinWorkerSession(w.Port) + ":main.0"
		desiredByPaneID[pid] = w
	}

	rows, err := store.Query("SELECT pane_id, ttyd_port, workspace, COALESCE(init_script,''), COALESCE(config,'{}'), COALESCE(agent_type,''), COALESCE(allow_all_actions,0), COALESCE(reply_in_chinese,0), COALESCE(use_official_auth,0) FROM agent_config WHERE active=1 ORDER BY ttyd_port ASC, pane_id ASC")
	if err != nil {
		return
	}
	defer rows.Close()

	token := getFirstToken()
	for rows.Next() {
		var paneID, workspace, initScript, configJSON, agentType string
		var allowAllActions bool
		var replyInChinese bool
		var useOfficialAuth bool
		var port int
		rows.Scan(&paneID, &port, &workspace, &initScript, &configJSON, &agentType, &allowAllActions, &replyInChinese, &useOfficialAuth)
		if paneID == "" || port == 0 {
			continue
		}

		// Sync agent_type and title if changed
		if desired, ok := desiredByPaneID[paneID]; ok && normalizeAgentType(agentType) != desired.AgentType {
			store.Exec(fmt.Sprintf("UPDATE agent_config SET agent_type=?, title=?, updated_at=%s WHERE pane_id=?", store.Now()),
				desired.AgentType, desired.Title, paneID)
			agentType = desired.AgentType
		}

		// Ensure tmux session
		sess := strings.Split(paneID, ":")[0]
		sessionCreated := false
		if exec.Command("tmux", "has-session", "-t", sess).Run() != nil {
			if workspace == "" {
				workspace = builtinWorkerWorkspace(sess)
			}
			ensureAgentToolInstalled(agentType)
			os.MkdirAll(workspace, 0755)
			exec.Command("tmux", "new-session", "-d", "-s", sess, "-n", "main", "-c", workspace).Run()
			log.Printf("[startup] created session %s", sess)
			sessionCreated = true
		}

		// Ensure ttyd
		if !isPortListening(port) {
			startInstance(paneID, port, token)
		}
		if sessionCreated {
			initPaneEnv(paneEnvOpts{
				paneID:          paneID,
				configJSON:      configJSON,
				workspace:       workspace,
				initScript:      initScript,
				agentType:       agentType,
				allowAllActions: allowAllActions,
				replyInChinese:  replyInChinese,
			})
		}
	}
}
